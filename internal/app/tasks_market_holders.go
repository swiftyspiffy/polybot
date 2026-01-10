package app

import (
	"context"
	"polybot/clients/polymarketapi"
	"sort"
	"time"

	"go.uber.org/zap"
)

// MarketHoldersRequest is the request for the market holders task.
type MarketHoldersRequest struct {
	ConditionID string `json:"conditionId"`
	TopN        int    `json:"topN"` // Number of top holders to return (default 50)
}

// OutcomeHolder represents a holder's position in a specific outcome.
type OutcomeHolder struct {
	Wallet      string  `json:"wallet"`
	ProfileURL  string  `json:"profileUrl"`
	Size        float64 `json:"size"`        // Net shares held
	AvgPrice    float64 `json:"avgPrice"`    // Average entry price
	TotalBought float64 `json:"totalBought"` // Total USDC spent buying
	TotalSold   float64 `json:"totalSold"`   // Total USDC received selling
	TradeCount  int     `json:"tradeCount"`
}

// OutcomeHolders contains all holders for a specific outcome.
type OutcomeHolders struct {
	Outcome      string          `json:"outcome"`
	OutcomeIndex int             `json:"outcomeIndex"`
	TotalHolders int             `json:"totalHolders"`
	TopHolders   []OutcomeHolder `json:"topHolders"`
}

// MarketHoldersResult is the result of the market holders task.
type MarketHoldersResult struct {
	Status          string           `json:"status"`
	ConditionID     string           `json:"conditionId"`
	Title           string           `json:"title"`
	Slug            string           `json:"slug"`
	Outcomes        []OutcomeHolders `json:"outcomes"`
	TotalTraders    int              `json:"totalTraders"`
	TradesProcessed int              `json:"tradesProcessed"`
	DurationMs      int64            `json:"durationMs"`
	Errors          []string         `json:"errors,omitempty"`
}

// MarketHoldersTask executes the market holders analysis.
type MarketHoldersTask struct {
	polymarket *polymarketapi.PolymarketApiClient
	logger     *zap.Logger
}

// NewMarketHoldersTask creates a new task instance.
func NewMarketHoldersTask(
	polymarket *polymarketapi.PolymarketApiClient,
	logger *zap.Logger,
) *MarketHoldersTask {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &MarketHoldersTask{
		polymarket: polymarket,
		logger:     logger,
	}
}

// walletPosition tracks a wallet's position during aggregation.
type walletPosition struct {
	size        float64
	totalBought float64
	totalSold   float64
	buyShares   float64
	tradeCount  int
}

// Execute runs the market holders analysis.
func (t *MarketHoldersTask) Execute(
	ctx context.Context,
	req MarketHoldersRequest,
) (*MarketHoldersResult, error) {
	startTime := time.Now()

	if req.TopN <= 0 {
		req.TopN = 50
	}

	result := &MarketHoldersResult{
		Status:      "running",
		ConditionID: req.ConditionID,
		Outcomes:    []OutcomeHolders{},
		Errors:      []string{},
	}

	// Fetch market metadata
	market, err := t.polymarket.GetMarketByConditionID(ctx, req.ConditionID)
	if err != nil {
		t.logger.Warn("failed to fetch market metadata",
			zap.String("conditionId", req.ConditionID),
			zap.Error(err),
		)
		result.Errors = append(result.Errors, "Failed to fetch market: "+err.Error())
	} else {
		result.Title = market.Question
		result.Slug = market.Slug
	}

	// Map: outcome -> wallet -> position
	outcomePositions := make(map[string]map[string]*walletPosition)
	allTraders := make(map[string]bool)

	// Fetch all trades with pagination
	cursor := ""
	maxIterations := 50 // Safety limit
	tradesProcessed := 0

	for i := 0; i < maxIterations; i++ {
		select {
		case <-ctx.Done():
			result.Status = "cancelled"
			result.DurationMs = time.Since(startTime).Milliseconds()
			return result, ctx.Err()
		default:
		}

		trades, err := t.polymarket.GetMarketTrades(ctx, req.ConditionID, 1000, cursor)
		if err != nil {
			t.logger.Warn("failed to fetch trades page",
				zap.String("conditionId", req.ConditionID),
				zap.String("cursor", cursor),
				zap.Error(err),
			)
			result.Errors = append(result.Errors, "Failed to fetch some trades: "+err.Error())
			break
		}

		if len(trades) == 0 {
			break
		}

		t.logger.Info("fetched trades page",
			zap.String("conditionId", req.ConditionID),
			zap.Int("count", len(trades)),
			zap.Int("iteration", i),
		)

		// Process trades
		for _, trade := range trades {
			tradesProcessed++
			allTraders[trade.ProxyWallet] = true

			// Get or create outcome map
			if _, exists := outcomePositions[trade.Outcome]; !exists {
				outcomePositions[trade.Outcome] = make(map[string]*walletPosition)
			}

			// Get or create wallet position
			pos, exists := outcomePositions[trade.Outcome][trade.ProxyWallet]
			if !exists {
				pos = &walletPosition{}
				outcomePositions[trade.Outcome][trade.ProxyWallet] = pos
			}

			pos.tradeCount++
			usdcValue := trade.Size * trade.Price

			if trade.Side == "BUY" {
				pos.size += trade.Size
				pos.totalBought += usdcValue
				pos.buyShares += trade.Size
			} else { // SELL
				pos.size -= trade.Size
				pos.totalSold += usdcValue
			}
		}

		// Check if more pages
		if len(trades) < 1000 {
			break
		}
		cursor = trades[len(trades)-1].ID
	}

	result.TradesProcessed = tradesProcessed
	result.TotalTraders = len(allTraders)

	// Convert to result format
	outcomeIndex := 0
	for outcome, walletPositions := range outcomePositions {
		holders := []OutcomeHolder{}

		for wallet, pos := range walletPositions {
			// Only include wallets with positive positions
			if pos.size > 0.01 { // Small threshold to avoid dust
				avgPrice := float64(0)
				if pos.buyShares > 0 {
					avgPrice = pos.totalBought / pos.buyShares
				}

				holders = append(holders, OutcomeHolder{
					Wallet:      wallet,
					ProfileURL:  "https://polymarket.com/profile/" + wallet,
					Size:        pos.size,
					AvgPrice:    avgPrice,
					TotalBought: pos.totalBought,
					TotalSold:   pos.totalSold,
					TradeCount:  pos.tradeCount,
				})
			}
		}

		// Sort by size (descending)
		sort.Slice(holders, func(i, j int) bool {
			return holders[i].Size > holders[j].Size
		})

		// Limit to top N
		topHolders := holders
		if len(holders) > req.TopN {
			topHolders = holders[:req.TopN]
		}

		result.Outcomes = append(result.Outcomes, OutcomeHolders{
			Outcome:      outcome,
			OutcomeIndex: outcomeIndex,
			TotalHolders: len(holders),
			TopHolders:   topHolders,
		})
		outcomeIndex++
	}

	// Sort outcomes by name for consistent ordering
	sort.Slice(result.Outcomes, func(i, j int) bool {
		return result.Outcomes[i].Outcome < result.Outcomes[j].Outcome
	})

	result.Status = "completed"
	result.DurationMs = time.Since(startTime).Milliseconds()

	t.logger.Info("market holders task completed",
		zap.String("conditionId", req.ConditionID),
		zap.Int("outcomes", len(result.Outcomes)),
		zap.Int("totalTraders", result.TotalTraders),
		zap.Int("tradesProcessed", result.TradesProcessed),
		zap.Int64("durationMs", result.DurationMs),
	)

	return result, nil
}
