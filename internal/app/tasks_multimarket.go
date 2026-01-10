package app

import (
	"context"
	"polybot/clients/polymarketapi"
	"sort"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// MarketSelection represents a selected market with its winning outcome.
type MarketSelection struct {
	ConditionID    string `json:"conditionId"`
	Title          string `json:"title"`
	WinningOutcome string `json:"winningOutcome"`
}

// MultiMarketWinnersRequest is the request for the multi-market winners task.
type MultiMarketWinnersRequest struct {
	Markets       []MarketSelection `json:"markets"`
	MinMarketsWon int               `json:"minMarketsWon"`
}

// MarketWinInfo contains info about a market a wallet won in.
type MarketWinInfo struct {
	ConditionID string `json:"conditionId"`
	Title       string `json:"title"`
	Slug        string `json:"slug"`
	Outcome     string `json:"outcome"`
}

// WalletWinnerResult contains results for a single wallet.
type WalletWinnerResult struct {
	Address     string          `json:"address"`
	MarketsWon  int             `json:"marketsWon"`
	Markets     []MarketWinInfo `json:"markets"`
	ProfileURL  string          `json:"profileUrl"`
}

// MultiMarketWinnersResult is the result of the multi-market winners task.
type MultiMarketWinnersResult struct {
	Status                  string               `json:"status"`
	TotalWalletsAnalyzed    int                  `json:"totalWalletsAnalyzed"`
	WalletsMatchingCriteria int                  `json:"walletsMatchingCriteria"`
	Results                 []WalletWinnerResult `json:"results"`
	DurationMs              int64                `json:"durationMs"`
	MarketsProcessed        int                  `json:"marketsProcessed"`
	Errors                  []string             `json:"errors,omitempty"`
}

// MultiMarketWinnersTask executes the multi-market winners analysis.
type MultiMarketWinnersTask struct {
	polymarket *polymarketapi.PolymarketApiClient
	logger     *zap.Logger
}

// NewMultiMarketWinnersTask creates a new task instance.
func NewMultiMarketWinnersTask(
	polymarket *polymarketapi.PolymarketApiClient,
	logger *zap.Logger,
) *MultiMarketWinnersTask {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &MultiMarketWinnersTask{
		polymarket: polymarket,
		logger:     logger,
	}
}

// Execute runs the multi-market winners analysis.
func (t *MultiMarketWinnersTask) Execute(
	ctx context.Context,
	req MultiMarketWinnersRequest,
) (*MultiMarketWinnersResult, error) {
	startTime := time.Now()

	if req.MinMarketsWon < 2 {
		req.MinMarketsWon = 2
	}

	result := &MultiMarketWinnersResult{
		Status:  "running",
		Results: []WalletWinnerResult{},
		Errors:  []string{},
	}

	// Map to track wallet -> markets won
	walletWins := make(map[string][]MarketWinInfo)
	var mu sync.Mutex

	// Process each market
	for _, market := range req.Markets {
		select {
		case <-ctx.Done():
			result.Status = "cancelled"
			result.DurationMs = time.Since(startTime).Milliseconds()
			return result, ctx.Err()
		default:
		}

		// Skip markets without a winning outcome
		if market.WinningOutcome == "" {
			t.logger.Info("skipping market without winning outcome",
				zap.String("conditionId", market.ConditionID),
				zap.String("title", market.Title),
			)
			result.Errors = append(result.Errors, "Skipped "+market.Title[:min(30, len(market.Title))]+"...: not resolved")
			continue
		}

		winners, err := t.getMarketWinners(ctx, market)
		if err != nil {
			t.logger.Warn("failed to get winners for market",
				zap.String("conditionId", market.ConditionID),
				zap.Error(err),
			)
			result.Errors = append(result.Errors, "Failed to process market "+market.ConditionID[:8]+"...: "+err.Error())
			continue
		}

		result.MarketsProcessed++

		mu.Lock()
		for wallet, marketInfo := range winners {
			walletWins[wallet] = append(walletWins[wallet], marketInfo)
		}
		mu.Unlock()

		t.logger.Info("processed market",
			zap.String("conditionId", market.ConditionID),
			zap.Int("winners", len(winners)),
		)
	}

	// Filter wallets by minimum markets won
	result.TotalWalletsAnalyzed = len(walletWins)

	for wallet, markets := range walletWins {
		if len(markets) >= req.MinMarketsWon {
			result.Results = append(result.Results, WalletWinnerResult{
				Address:    wallet,
				MarketsWon: len(markets),
				Markets:    markets,
				ProfileURL: "https://polymarket.com/profile/" + wallet,
			})
		}
	}

	result.WalletsMatchingCriteria = len(result.Results)

	// Sort by markets won (descending)
	sort.Slice(result.Results, func(i, j int) bool {
		return result.Results[i].MarketsWon > result.Results[j].MarketsWon
	})

	result.Status = "completed"
	result.DurationMs = time.Since(startTime).Milliseconds()

	t.logger.Info("multi-market winners task completed",
		zap.Int("totalWallets", result.TotalWalletsAnalyzed),
		zap.Int("matchingWallets", result.WalletsMatchingCriteria),
		zap.Int("marketsProcessed", result.MarketsProcessed),
		zap.Int64("durationMs", result.DurationMs),
	)

	return result, nil
}

// getMarketWinners fetches all winning wallets for a market.
// Returns map of wallet address -> market info.
func (t *MultiMarketWinnersTask) getMarketWinners(
	ctx context.Context,
	selection MarketSelection,
) (map[string]MarketWinInfo, error) {
	t.logger.Info("processing market for winners",
		zap.String("conditionId", selection.ConditionID),
		zap.String("winningOutcome", selection.WinningOutcome),
	)

	// Fetch all trades for this market
	winners := make(map[string]MarketWinInfo)
	cursor := ""
	maxIterations := 10 // Limit pagination to prevent infinite loops

	for i := 0; i < maxIterations; i++ {
		select {
		case <-ctx.Done():
			return winners, ctx.Err()
		default:
		}

		trades, err := t.polymarket.GetMarketTrades(ctx, selection.ConditionID, 1000, cursor)
		if err != nil {
			t.logger.Warn("failed to fetch trades page",
				zap.String("conditionId", selection.ConditionID),
				zap.String("cursor", cursor),
				zap.Error(err),
			)
			break
		}

		if len(trades) == 0 {
			t.logger.Info("no trades found for market",
				zap.String("conditionId", selection.ConditionID),
			)
			break
		}

		// Log first trade for debugging
		t.logger.Info("fetched trades",
			zap.String("conditionId", selection.ConditionID),
			zap.Int("count", len(trades)),
			zap.String("sampleOutcome", trades[0].Outcome),
			zap.String("winningOutcome", selection.WinningOutcome),
		)

		// Process trades - find wallets that traded the winning outcome
		matchCount := 0
		for _, trade := range trades {
			// Match by outcome name (case-insensitive)
			if strings.EqualFold(trade.Outcome, selection.WinningOutcome) {
				matchCount++
				if _, exists := winners[trade.ProxyWallet]; !exists {
					winners[trade.ProxyWallet] = MarketWinInfo{
						ConditionID: selection.ConditionID,
						Title:       selection.Title,
						Slug:        trade.Slug,
						Outcome:     selection.WinningOutcome,
					}
				}
			}
		}

		t.logger.Info("processed trades page",
			zap.String("conditionId", selection.ConditionID),
			zap.Int("matchCount", matchCount),
			zap.Int("uniqueWinners", len(winners)),
		)

		// Update cursor for next page
		if len(trades) < 1000 {
			break // No more pages
		}
		cursor = trades[len(trades)-1].ID
	}

	return winners, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
