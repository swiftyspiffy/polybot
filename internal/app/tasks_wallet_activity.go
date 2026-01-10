package app

import (
	"context"
	"fmt"
	"polybot/clients/polymarketapi"
	"sort"
	"time"

	"go.uber.org/zap"
)

// WalletActivityRequest is the request for the wallet activity task.
type WalletActivityRequest struct {
	WalletAddress string `json:"walletAddress"`
	Duration      string `json:"duration"` // "1d", "1w", "2w", "1m", "3m", "6m", "1y"
}

// MarketCostBasis contains cost basis info for a single market.
type MarketCostBasis struct {
	ConditionID   string  `json:"conditionId"`
	Title         string  `json:"title"`
	Slug          string  `json:"slug"`
	TotalCostBasis float64 `json:"totalCostBasis"`
	TradeCount    int     `json:"tradeCount"`
	FirstTrade    int64   `json:"firstTrade"`
	LastTrade     int64   `json:"lastTrade"`
	Outcomes      map[string]*OutcomeCostBasis `json:"outcomes"`
}

// OutcomeCostBasis contains cost basis for a specific outcome.
type OutcomeCostBasis struct {
	Outcome    string  `json:"outcome"`
	CostBasis  float64 `json:"costBasis"`
	TradeCount int     `json:"tradeCount"`
}

// WalletActivityResult is the result of the wallet activity task.
type WalletActivityResult struct {
	Status            string            `json:"status"`
	WalletAddress     string            `json:"walletAddress"`
	Duration          string            `json:"duration"`
	StartTime         int64             `json:"startTime"`
	EndTime           int64             `json:"endTime"`
	TotalCostBasis    float64           `json:"totalCostBasis"`
	TotalTradeCount   int               `json:"totalTradeCount"`
	TotalMarkets      int               `json:"totalMarkets"`
	Markets           []MarketCostBasis `json:"markets"`
	DurationMs        int64             `json:"durationMs"`
	ActivitiesScanned int               `json:"activitiesScanned"`
	Errors            []string          `json:"errors,omitempty"`
}

// WalletActivityTask executes the wallet activity analysis.
type WalletActivityTask struct {
	polymarket *polymarketapi.PolymarketApiClient
	logger     *zap.Logger
}

// NewWalletActivityTask creates a new task instance.
func NewWalletActivityTask(
	polymarket *polymarketapi.PolymarketApiClient,
	logger *zap.Logger,
) *WalletActivityTask {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &WalletActivityTask{
		polymarket: polymarket,
		logger:     logger,
	}
}

// parseDuration converts duration string to time.Duration and returns start timestamp.
func parseDuration(duration string) (time.Time, error) {
	now := time.Now()
	switch duration {
	case "1d":
		return now.Add(-24 * time.Hour), nil
	case "1w":
		return now.Add(-7 * 24 * time.Hour), nil
	case "2w":
		return now.Add(-14 * 24 * time.Hour), nil
	case "1m":
		return now.Add(-30 * 24 * time.Hour), nil
	case "3m":
		return now.Add(-90 * 24 * time.Hour), nil
	case "6m":
		return now.Add(-180 * 24 * time.Hour), nil
	case "1y":
		return now.Add(-365 * 24 * time.Hour), nil
	default:
		// Default to 1 month
		return now.Add(-30 * 24 * time.Hour), nil
	}
}

// Execute runs the wallet activity analysis.
func (t *WalletActivityTask) Execute(
	ctx context.Context,
	req WalletActivityRequest,
) (*WalletActivityResult, error) {
	taskStartTime := time.Now()

	// Parse duration to get start time (use Unix seconds, not milliseconds)
	startTime, _ := parseDuration(req.Duration)
	startTimestamp := startTime.Unix() // seconds
	endTimestamp := time.Now().Unix()  // seconds

	result := &WalletActivityResult{
		Status:        "running",
		WalletAddress: req.WalletAddress,
		Duration:      req.Duration,
		StartTime:     startTimestamp,
		EndTime:       endTimestamp,
		Markets:       []MarketCostBasis{},
		Errors:        []string{},
	}

	// Map to aggregate cost basis by market
	marketMap := make(map[string]*MarketCostBasis)

	// Fetch activities - API has a hard limit of 500 activities
	// Use startTime parameter to filter to our time window
	totalActivities := 0

	activities, err := t.polymarket.GetUserActivityPaginated(
		ctx,
		req.WalletAddress,
		1000, // Request max, API will return up to 500
		"",
		startTimestamp, // Use API time filter
	)
	if err != nil {
		t.logger.Warn("failed to fetch activities",
			zap.String("wallet", req.WalletAddress),
			zap.Error(err),
		)
		result.Errors = append(result.Errors, "Failed to fetch activities: "+err.Error())
		result.Status = "failed"
		result.DurationMs = time.Since(taskStartTime).Milliseconds()
		return result, nil
	}

	t.logger.Info("fetched activities",
		zap.String("wallet", req.WalletAddress),
		zap.Int("count", len(activities)),
	)

	// Warn if we hit the API limit (data might be incomplete)
	if len(activities) >= 500 {
		result.Errors = append(result.Errors,
			fmt.Sprintf("API returned max 500 activities - results may be incomplete for very active wallets"))
	}

	// Process activities
	for _, activity := range activities {
		// Double-check time filter (API should handle this, but be safe)
		if activity.Timestamp < startTimestamp {
			continue
		}

		totalActivities++

		// Only count TRADE types for cost basis
		if activity.Type != "TRADE" {
			continue
		}

		// Get or create market entry
		market, exists := marketMap[activity.ConditionID]
		if !exists {
			market = &MarketCostBasis{
				ConditionID: activity.ConditionID,
				Title:       activity.Title,
				Slug:        activity.Slug,
				Outcomes:    make(map[string]*OutcomeCostBasis),
				FirstTrade:  activity.Timestamp,
				LastTrade:   activity.Timestamp,
			}
			marketMap[activity.ConditionID] = market
		}

		// Update market stats
		market.TotalCostBasis += activity.UsdcSize
		market.TradeCount++
		if activity.Timestamp < market.FirstTrade {
			market.FirstTrade = activity.Timestamp
		}
		if activity.Timestamp > market.LastTrade {
			market.LastTrade = activity.Timestamp
		}

		// Update outcome stats
		outcome, exists := market.Outcomes[activity.Outcome]
		if !exists {
			outcome = &OutcomeCostBasis{
				Outcome: activity.Outcome,
			}
			market.Outcomes[activity.Outcome] = outcome
		}
		outcome.CostBasis += activity.UsdcSize
		outcome.TradeCount++

		// Update totals
		result.TotalCostBasis += activity.UsdcSize
		result.TotalTradeCount++
	}

	result.ActivitiesScanned = totalActivities

	// Convert map to slice and sort by cost basis (descending)
	for _, market := range marketMap {
		result.Markets = append(result.Markets, *market)
	}

	sort.Slice(result.Markets, func(i, j int) bool {
		return result.Markets[i].TotalCostBasis > result.Markets[j].TotalCostBasis
	})

	result.TotalMarkets = len(result.Markets)
	result.Status = "completed"
	result.DurationMs = time.Since(taskStartTime).Milliseconds()

	t.logger.Info("wallet activity task completed",
		zap.String("wallet", req.WalletAddress),
		zap.Int("totalMarkets", result.TotalMarkets),
		zap.Float64("totalCostBasis", result.TotalCostBasis),
		zap.Int64("durationMs", result.DurationMs),
	)

	return result, nil
}
