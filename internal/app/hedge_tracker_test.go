package app

import (
	"context"
	"errors"
	"polybot/clients/polymarketapi"
	"testing"
	"time"

	"go.uber.org/zap"
)

// mockHedgeAPIClient implements HedgeAPIClient for testing.
type mockHedgeAPIClient struct {
	positions []polymarketapi.Position
	err       error
}

func (m *mockHedgeAPIClient) GetPositions(ctx context.Context, wallet string, conditionID string, limit int) ([]polymarketapi.Position, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.positions, nil
}

func TestDefaultHedgeTrackerConfig(t *testing.T) {
	cfg := DefaultHedgeTrackerConfig()

	if cfg.FileName != "hedge_tracker.json" {
		t.Errorf("expected filename hedge_tracker.json, got %s", cfg.FileName)
	}
	if cfg.SaveInterval != 5*time.Minute {
		t.Errorf("expected save interval 5m, got %v", cfg.SaveInterval)
	}
	if cfg.MinHedgeSize != 100 {
		t.Errorf("expected min hedge size 100, got %f", cfg.MinHedgeSize)
	}
	if cfg.MinHedgeValue != 500 {
		t.Errorf("expected min hedge value 500, got %f", cfg.MinHedgeValue)
	}
	if cfg.SignificantSellPct != 0.50 {
		t.Errorf("expected significant sell pct 0.50, got %f", cfg.SignificantSellPct)
	}
	if cfg.PositionCheckInterval != 5*time.Minute {
		t.Errorf("expected position check interval 5m, got %v", cfg.PositionCheckInterval)
	}
	if cfg.MaxPositionChecks != 60 {
		t.Errorf("expected max position checks 60, got %d", cfg.MaxPositionChecks)
	}
	if cfg.MinExitsForAsymmetric != 5 {
		t.Errorf("expected min exits for asymmetric 5, got %d", cfg.MinExitsForAsymmetric)
	}
	if cfg.AsymmetricThreshold != 2.0 {
		t.Errorf("expected asymmetric threshold 2.0, got %f", cfg.AsymmetricThreshold)
	}
	if cfg.ResolutionCheckInterval != 1*time.Hour {
		t.Errorf("expected resolution check interval 1h, got %v", cfg.ResolutionCheckInterval)
	}
}

func TestNewHedgeTracker(t *testing.T) {
	cfg := DefaultHedgeTrackerConfig()
	ht := NewHedgeTracker(zap.NewNop(), nil, nil, cfg)

	if ht.logger == nil {
		t.Error("expected logger to be set")
	}
	if ht.hedgeStates == nil {
		t.Error("expected hedgeStates to be initialized")
	}
	if ht.pendingEvents == nil {
		t.Error("expected pendingEvents to be initialized")
	}
	if ht.exitStats == nil {
		t.Error("expected exitStats to be initialized")
	}
	if ht.lastPositionCheck == nil {
		t.Error("expected lastPositionCheck to be initialized")
	}
}

func TestHedgeTracker_IsEnabled(t *testing.T) {
	// Without GistID - disabled
	cfg := DefaultHedgeTrackerConfig()
	cfg.GistID = ""
	ht := NewHedgeTracker(zap.NewNop(), nil, nil, cfg)
	if ht.IsEnabled() {
		t.Error("expected disabled without GistID")
	}

	// With GistID but no gistClient - disabled
	cfg.GistID = "test-gist-id"
	ht = NewHedgeTracker(zap.NewNop(), nil, nil, cfg)
	if ht.IsEnabled() {
		t.Error("expected disabled without gistClient")
	}
}

func TestHedgeTracker_ShouldCheckPositions_RateLimiting(t *testing.T) {
	cfg := DefaultHedgeTrackerConfig()
	cfg.MaxPositionChecks = 3
	cfg.PositionCheckInterval = 1 * time.Minute
	ht := NewHedgeTracker(zap.NewNop(), nil, nil, cfg)

	wallet := "0xwallet"
	conditionID := "cond1"

	// First check should succeed
	if !ht.ShouldCheckPositions(wallet, conditionID) {
		t.Error("expected first check to succeed")
	}

	// Second check with same wallet+market should fail (cooldown)
	if ht.ShouldCheckPositions(wallet, conditionID) {
		t.Error("expected second check to fail due to cooldown")
	}

	// Different wallet should succeed
	if !ht.ShouldCheckPositions("0xother", conditionID) {
		t.Error("expected different wallet to succeed")
	}

	// Different market should succeed
	if !ht.ShouldCheckPositions(wallet, "cond2") {
		t.Error("expected different market to succeed")
	}

	// Fourth check should hit rate limit
	if ht.ShouldCheckPositions("0xfourth", "cond4") {
		t.Error("expected rate limit to be hit")
	}
}

func TestHedgeTracker_RecordExit(t *testing.T) {
	cfg := DefaultHedgeTrackerConfig()
	ht := NewHedgeTracker(zap.NewNop(), nil, nil, cfg)

	wallet := "0xwallet"

	// Record a winning exit (using internal method directly for testing)
	ht.mu.Lock()
	ht.recordExitInternal(wallet, &ExitRecord{
		ConditionID:   "cond1",
		Outcome:       "Yes",
		ExitPrice:     0.80,
		AvgEntryPrice: 0.50,
		Size:          100,
		RealizedPnl:   30.0,
		IsWinner:      true,
		HoldDuration:  3600, // 1 hour
		ExitedAt:      time.Now(),
	})
	ht.mu.Unlock()

	stats := ht.GetAsymmetricStats(wallet)
	if stats == nil {
		t.Fatal("expected stats to exist")
	}
	if stats.WinningExits != 1 {
		t.Errorf("expected 1 winning exit, got %d", stats.WinningExits)
	}
	if stats.AvgWinHoldDuration != 3600 {
		t.Errorf("expected avg win hold duration 3600, got %f", stats.AvgWinHoldDuration)
	}

	// Record a losing exit
	ht.mu.Lock()
	ht.recordExitInternal(wallet, &ExitRecord{
		ConditionID:   "cond2",
		Outcome:       "No",
		ExitPrice:     0.20,
		AvgEntryPrice: 0.50,
		Size:          100,
		RealizedPnl:   -30.0,
		IsWinner:      false,
		HoldDuration:  7200, // 2 hours
		ExitedAt:      time.Now(),
	})
	ht.mu.Unlock()

	stats = ht.GetAsymmetricStats(wallet)
	if stats.LosingExits != 1 {
		t.Errorf("expected 1 losing exit, got %d", stats.LosingExits)
	}
	if stats.AvgLossHoldDuration != 7200 {
		t.Errorf("expected avg loss hold duration 7200, got %f", stats.AvgLossHoldDuration)
	}
}

func TestHedgeTracker_ShouldAlertAsymmetric(t *testing.T) {
	cfg := DefaultHedgeTrackerConfig()
	cfg.MinExitsForAsymmetric = 2
	cfg.AsymmetricThreshold = 2.0
	ht := NewHedgeTracker(zap.NewNop(), nil, nil, cfg)

	wallet := "0xwallet"

	// Not enough exits
	ht.mu.Lock()
	ht.recordExitInternal(wallet, &ExitRecord{
		IsWinner:     true,
		HoldDuration: 3600,
	})
	ht.mu.Unlock()
	if ht.ShouldAlertAsymmetric(wallet) {
		t.Error("expected no alert with insufficient exits")
	}

	// Add enough exits with asymmetric pattern (winners exit faster)
	ht.mu.Lock()
	ht.recordExitInternal(wallet, &ExitRecord{
		IsWinner:     true,
		HoldDuration: 3600, // 1 hour for winners
	})
	ht.recordExitInternal(wallet, &ExitRecord{
		IsWinner:     false,
		HoldDuration: 10800, // 3 hours for losers
	})
	ht.recordExitInternal(wallet, &ExitRecord{
		IsWinner:     false,
		HoldDuration: 10800,
	})
	ht.mu.Unlock()

	// Should now alert (losers held 3x longer than winners)
	if !ht.ShouldAlertAsymmetric(wallet) {
		t.Error("expected alert for asymmetric pattern")
	}
}

func TestHedgeTracker_PendingEventCount(t *testing.T) {
	cfg := DefaultHedgeTrackerConfig()
	ht := NewHedgeTracker(zap.NewNop(), nil, nil, cfg)

	if ht.PendingEventCount() != 0 {
		t.Error("expected 0 pending events initially")
	}

	// Add a pending event
	ht.mu.Lock()
	ht.pendingEvents["test"] = &HedgeRemovalEvent{
		ID:     "test",
		Wallet: "0xwallet",
	}
	ht.mu.Unlock()

	if ht.PendingEventCount() != 1 {
		t.Errorf("expected 1 pending event, got %d", ht.PendingEventCount())
	}
}

func TestHedgeTracker_Stats(t *testing.T) {
	cfg := DefaultHedgeTrackerConfig()
	ht := NewHedgeTracker(zap.NewNop(), nil, nil, cfg)

	hedged, pending, exits := ht.Stats()
	if hedged != 0 || pending != 0 || exits != 0 {
		t.Error("expected all stats to be 0 initially")
	}

	// Add some state with hedged positions
	ht.mu.Lock()
	ht.hedgeStates["0xwallet1"] = &HedgeState{
		Wallet: "0xwallet1",
		Positions: map[string]HedgePosition{
			"cond1": {ConditionID: "cond1", IsHedged: true},
			"cond2": {ConditionID: "cond2", IsHedged: true},
		},
	}
	ht.hedgeStates["0xwallet2"] = &HedgeState{
		Wallet: "0xwallet2",
		Positions: map[string]HedgePosition{
			"cond1": {ConditionID: "cond1", IsHedged: false}, // Not hedged
		},
	}
	ht.pendingEvents["event1"] = &HedgeRemovalEvent{}
	ht.exitStats["0xwallet1"] = &AsymmetricExitStats{}
	ht.mu.Unlock()

	hedged, pending, exits = ht.Stats()
	if hedged != 2 {
		t.Errorf("expected 2 hedged positions, got %d", hedged)
	}
	if pending != 1 {
		t.Errorf("expected 1 pending event, got %d", pending)
	}
	if exits != 1 {
		t.Errorf("expected 1 exit stat, got %d", exits)
	}
}

func TestHedgePosition_IsHedged(t *testing.T) {
	cfg := DefaultHedgeTrackerConfig()
	cfg.MinHedgeSize = 100
	cfg.MinHedgeValue = 500

	// Test hedged position - needs 100+ shares at price that gives 500+ value
	// 1000 shares * 0.50 = 500 value (meets threshold)
	pos := HedgePosition{
		YesSize:     1000,
		NoSize:      1000,
		YesAvgPrice: 0.50,
		NoAvgPrice:  0.50,
	}
	// Calculate if hedged
	yesValue := pos.YesSize * pos.YesAvgPrice // 500
	noValue := pos.NoSize * pos.NoAvgPrice    // 500
	isHedged := pos.YesSize >= cfg.MinHedgeSize && pos.NoSize >= cfg.MinHedgeSize &&
		yesValue >= cfg.MinHedgeValue && noValue >= cfg.MinHedgeValue

	if !isHedged {
		t.Errorf("expected position to be hedged (yesValue=%f, noValue=%f)", yesValue, noValue)
	}

	// Test not hedged - one side too small
	pos2 := HedgePosition{
		YesSize:     50, // Below MinHedgeSize
		NoSize:      1000,
		YesAvgPrice: 0.50,
		NoAvgPrice:  0.50,
	}
	isHedged2 := pos2.YesSize >= cfg.MinHedgeSize && pos2.NoSize >= cfg.MinHedgeSize
	if isHedged2 {
		t.Error("expected position to not be hedged due to small yes size")
	}

	// Test not hedged - value too low
	pos3 := HedgePosition{
		YesSize:     200,  // 200 shares
		NoSize:      200,
		YesAvgPrice: 0.10, // 200 * 0.10 = 20 value (below 500)
		NoAvgPrice:  0.10,
	}
	yes3Value := pos3.YesSize * pos3.YesAvgPrice
	no3Value := pos3.NoSize * pos3.NoAvgPrice
	isHedged3 := pos3.YesSize >= cfg.MinHedgeSize && pos3.NoSize >= cfg.MinHedgeSize &&
		yes3Value >= cfg.MinHedgeValue && no3Value >= cfg.MinHedgeValue
	if isHedged3 {
		t.Error("expected position to not be hedged due to low value")
	}
}

func TestHedgeRemovalEvent_Fields(t *testing.T) {
	event := HedgeRemovalEvent{
		ID:             "0xwallet:cond1:1234567890",
		Wallet:         "0xwallet",
		ConditionID:    "cond1",
		MarketTitle:    "Test Market",
		YesSizeBefore:  1000,
		NoSizeBefore:   500,
		SoldSide:       "No",
		SoldSize:       450,
		YesSizeAfter:   1000,
		NoSizeAfter:    50,
		RemovedAt:      time.Now(),
		Resolved:       false,
		RemovedLoser:   false,
	}

	if event.Wallet != "0xwallet" {
		t.Error("unexpected wallet")
	}
	if event.SoldSide != "No" {
		t.Error("unexpected sold side")
	}
	if event.Resolved {
		t.Error("expected not resolved")
	}
}

func TestHedgeAlert_Fields(t *testing.T) {
	alert := HedgeAlert{
		Wallet:        "0xwallet",
		WalletURL:     "https://polymarket.com/profile/0xwallet",
		MarketTitle:   "Test Market",
		Timestamp:     time.Now(),
		Reason:        "hedge_removal",
		YesSizeBefore: 1000,
		NoSizeBefore:  500,
		SoldSide:      "No",
		SoldSize:      450,
		SoldPrice:     0.40,
		YesSizeAfter:  1000,
		NoSizeAfter:   50,
		ReductionPct:  0.90,
	}

	if alert.Reason != "hedge_removal" {
		t.Error("unexpected reason")
	}
	if alert.ReductionPct != 0.90 {
		t.Errorf("expected 0.90 reduction, got %f", alert.ReductionPct)
	}
}

func TestAsymmetricExitStats_Calculation(t *testing.T) {
	stats := AsymmetricExitStats{
		Wallet:              "0xwallet",
		WinningExits:        5,
		LosingExits:         5,
		TotalWinHoldTime:    5000,
		TotalLossHoldTime:   15000,
		AvgWinHoldDuration:  1000,
		AvgLossHoldDuration: 3000,
	}

	// Ratio should be 3.0 (losers held 3x longer)
	ratio := stats.AvgLossHoldDuration / stats.AvgWinHoldDuration
	if ratio != 3.0 {
		t.Errorf("expected ratio 3.0, got %f", ratio)
	}
}

func TestHedgeTrackerSnapshot_Serialization(t *testing.T) {
	snapshot := HedgeTrackerSnapshot{
		Version:   1,
		Timestamp: time.Now(),
		HedgeStates: map[string]*HedgeState{
			"0xwallet": {
				Wallet: "0xwallet",
				Positions: map[string]HedgePosition{
					"cond1": {
						ConditionID: "cond1",
						YesSize:     100,
						NoSize:      100,
						IsHedged:    true,
					},
				},
			},
		},
		PendingEvents: map[string]*HedgeRemovalEvent{
			"event1": {
				ID:     "event1",
				Wallet: "0xwallet",
			},
		},
		ExitStats: map[string]*AsymmetricExitStats{
			"0xwallet": {
				Wallet:       "0xwallet",
				WinningExits: 3,
				LosingExits:  2,
			},
		},
	}

	if snapshot.Version != 1 {
		t.Error("unexpected version")
	}
	if len(snapshot.HedgeStates) != 1 {
		t.Error("expected 1 hedge state")
	}
	if len(snapshot.PendingEvents) != 1 {
		t.Error("expected 1 pending event")
	}
	if len(snapshot.ExitStats) != 1 {
		t.Error("expected 1 exit stat")
	}
}

func TestProcessTrade_BuyIgnored(t *testing.T) {
	cfg := DefaultHedgeTrackerConfig()
	mock := &mockHedgeAPIClient{}
	ht := NewHedgeTracker(zap.NewNop(), mock, nil, cfg)

	// BUY trades should be ignored (only SELLs analyzed for hedge removal)
	alerts := ht.ProcessTrade(
		context.Background(),
		"0xwallet",
		"cond123",
		"Test Market",
		"test-market",
		"BUY",
		"Yes",
		100,
		0.50,
	)

	if len(alerts) != 0 {
		t.Errorf("expected 0 alerts for BUY, got %d", len(alerts))
	}
}

func TestProcessTrade_APIError(t *testing.T) {
	cfg := DefaultHedgeTrackerConfig()
	mock := &mockHedgeAPIClient{
		err: errors.New("API error"),
	}
	ht := NewHedgeTracker(zap.NewNop(), mock, nil, cfg)

	alerts := ht.ProcessTrade(
		context.Background(),
		"0xwallet",
		"cond123",
		"Test Market",
		"test-market",
		"SELL",
		"Yes",
		100,
		0.50,
	)

	if len(alerts) != 0 {
		t.Errorf("expected 0 alerts on API error, got %d", len(alerts))
	}
}

func TestProcessTrade_NoHedgeState(t *testing.T) {
	cfg := DefaultHedgeTrackerConfig()
	// Return positions for a non-hedged wallet (only Yes position)
	mock := &mockHedgeAPIClient{
		positions: []polymarketapi.Position{
			{Outcome: "Yes", Size: 1000, AvgPrice: 0.50},
		},
	}
	ht := NewHedgeTracker(zap.NewNop(), mock, nil, cfg)

	alerts := ht.ProcessTrade(
		context.Background(),
		"0xwallet",
		"cond123",
		"Test Market",
		"test-market",
		"SELL",
		"Yes",
		100,
		0.50,
	)

	// No previous hedge state, so no hedge removal alert
	if len(alerts) != 0 {
		t.Errorf("expected 0 alerts without previous hedge state, got %d", len(alerts))
	}

	// But state should be updated
	ht.mu.RLock()
	state := ht.hedgeStates["0xwallet"]
	ht.mu.RUnlock()

	if state == nil {
		t.Fatal("expected hedge state to be created")
	}
	pos := state.Positions["cond123"]
	if pos.YesSize != 1000 {
		t.Errorf("expected YesSize 1000, got %f", pos.YesSize)
	}
}

func TestProcessTrade_HedgeRemovalDetected(t *testing.T) {
	cfg := DefaultHedgeTrackerConfig()
	cfg.MinHedgeSize = 100
	cfg.MinHedgeValue = 50 // Lower for testing
	cfg.SignificantSellPct = 0.50

	// After the sell, wallet has reduced No position significantly
	mock := &mockHedgeAPIClient{
		positions: []polymarketapi.Position{
			{Outcome: "Yes", Size: 1000, AvgPrice: 0.50},
			{Outcome: "No", Size: 100, AvgPrice: 0.50}, // Reduced from 800 to 100
		},
	}
	ht := NewHedgeTracker(zap.NewNop(), mock, nil, cfg)

	// Set up previous hedge state (was hedged before)
	ht.mu.Lock()
	ht.hedgeStates["0xwallet"] = &HedgeState{
		Wallet: "0xwallet",
		Positions: map[string]HedgePosition{
			"cond123": {
				ConditionID: "cond123",
				YesSize:     1000,
				NoSize:      800, // Had 800 No shares before
				YesAvgPrice: 0.50,
				NoAvgPrice:  0.50,
				IsHedged:    true,
			},
		},
	}
	ht.mu.Unlock()

	alerts := ht.ProcessTrade(
		context.Background(),
		"0xwallet",
		"cond123",
		"Test Market",
		"test-market",
		"SELL",
		"No", // Selling No side
		700,  // Sold 700 shares
		0.50,
	)

	if len(alerts) != 1 {
		t.Fatalf("expected 1 hedge removal alert, got %d", len(alerts))
	}

	alert := alerts[0]
	if alert.Reason != "hedge_removal" {
		t.Errorf("expected reason hedge_removal, got %s", alert.Reason)
	}
	if alert.SoldSide != "No" {
		t.Errorf("expected sold side No, got %s", alert.SoldSide)
	}
	if alert.YesSizeBefore != 1000 {
		t.Errorf("expected YesSizeBefore 1000, got %f", alert.YesSizeBefore)
	}
	if alert.NoSizeBefore != 800 {
		t.Errorf("expected NoSizeBefore 800, got %f", alert.NoSizeBefore)
	}
	// Reduction: (800-100)/800 = 0.875 = 87.5%
	if alert.ReductionPct < 0.87 || alert.ReductionPct > 0.88 {
		t.Errorf("expected reduction ~0.875, got %f", alert.ReductionPct)
	}

	// Check pending event was created
	if ht.PendingEventCount() != 1 {
		t.Errorf("expected 1 pending event, got %d", ht.PendingEventCount())
	}
}

func TestProcessTrade_NoHedgeRemoval_InsufficientReduction(t *testing.T) {
	cfg := DefaultHedgeTrackerConfig()
	cfg.MinHedgeSize = 100
	cfg.MinHedgeValue = 50
	cfg.SignificantSellPct = 0.50 // Need 50% reduction

	// After sell, only 30% reduction (not enough)
	mock := &mockHedgeAPIClient{
		positions: []polymarketapi.Position{
			{Outcome: "Yes", Size: 1000, AvgPrice: 0.50},
			{Outcome: "No", Size: 700, AvgPrice: 0.50}, // Reduced from 1000 to 700 (30%)
		},
	}
	ht := NewHedgeTracker(zap.NewNop(), mock, nil, cfg)

	// Set up previous hedge state
	ht.mu.Lock()
	ht.hedgeStates["0xwallet"] = &HedgeState{
		Wallet: "0xwallet",
		Positions: map[string]HedgePosition{
			"cond123": {
				ConditionID: "cond123",
				YesSize:     1000,
				NoSize:      1000,
				YesAvgPrice: 0.50,
				NoAvgPrice:  0.50,
				IsHedged:    true,
			},
		},
	}
	ht.mu.Unlock()

	alerts := ht.ProcessTrade(
		context.Background(),
		"0xwallet",
		"cond123",
		"Test Market",
		"test-market",
		"SELL",
		"No",
		300, // Only 30% reduction
		0.50,
	)

	// 30% < 50% threshold, no alert
	if len(alerts) != 0 {
		t.Errorf("expected 0 alerts for insufficient reduction, got %d", len(alerts))
	}
}

func TestProcessTrade_UpdatesHedgeState(t *testing.T) {
	cfg := DefaultHedgeTrackerConfig()
	cfg.MinHedgeSize = 100
	cfg.MinHedgeValue = 50

	mock := &mockHedgeAPIClient{
		positions: []polymarketapi.Position{
			{Outcome: "Yes", Size: 500, AvgPrice: 0.60},
			{Outcome: "No", Size: 500, AvgPrice: 0.40},
		},
	}
	ht := NewHedgeTracker(zap.NewNop(), mock, nil, cfg)

	_ = ht.ProcessTrade(
		context.Background(),
		"0xnewwallet",
		"cond456",
		"Another Market",
		"another-market",
		"SELL",
		"Yes",
		50,
		0.60,
	)

	// Check state was created/updated
	ht.mu.RLock()
	state := ht.hedgeStates["0xnewwallet"]
	ht.mu.RUnlock()

	if state == nil {
		t.Fatal("expected state to be created")
	}

	pos := state.Positions["cond456"]
	if pos.YesSize != 500 {
		t.Errorf("expected YesSize 500, got %f", pos.YesSize)
	}
	if pos.NoSize != 500 {
		t.Errorf("expected NoSize 500, got %f", pos.NoSize)
	}
	// 500 * 0.60 = 300 and 500 * 0.40 = 200, both above MinHedgeValue=50
	// Both sizes >= MinHedgeSize=100
	if !pos.IsHedged {
		t.Error("expected position to be marked as hedged")
	}
}

func TestProcessTrade_AsymmetricAlertTriggered(t *testing.T) {
	cfg := DefaultHedgeTrackerConfig()
	cfg.MinHedgeSize = 100
	cfg.MinHedgeValue = 50
	cfg.MinExitsForAsymmetric = 2
	cfg.AsymmetricThreshold = 2.0

	mock := &mockHedgeAPIClient{
		positions: []polymarketapi.Position{
			{Outcome: "Yes", Size: 500, AvgPrice: 0.50},
			{Outcome: "No", Size: 500, AvgPrice: 0.50},
		},
	}
	ht := NewHedgeTracker(zap.NewNop(), mock, nil, cfg)

	wallet := "0xasymmetric"

	// Pre-populate asymmetric exit stats that should trigger alert
	ht.mu.Lock()
	ht.exitStats[wallet] = &AsymmetricExitStats{
		Wallet:              wallet,
		WinningExits:        3,
		LosingExits:         3,
		TotalWinHoldTime:    3000,  // 1000 avg
		TotalLossHoldTime:   15000, // 5000 avg (5x ratio)
		AvgWinHoldDuration:  1000,
		AvgLossHoldDuration: 5000,
	}
	ht.mu.Unlock()

	alerts := ht.ProcessTrade(
		context.Background(),
		wallet,
		"cond789",
		"Test Market",
		"test-market",
		"SELL",
		"Yes",
		50,
		0.50,
	)

	// Should get asymmetric exit alert (5x > 2x threshold)
	hasAsymmetric := false
	for _, alert := range alerts {
		if alert.Reason == "asymmetric_exit" {
			hasAsymmetric = true
			if alert.AsymmetricRatio != 5.0 {
				t.Errorf("expected ratio 5.0, got %f", alert.AsymmetricRatio)
			}
		}
	}

	if !hasAsymmetric {
		t.Error("expected asymmetric exit alert")
	}
}

func TestProcessTrade_NilAPIClient(t *testing.T) {
	cfg := DefaultHedgeTrackerConfig()
	ht := NewHedgeTracker(zap.NewNop(), nil, nil, cfg)

	// Should handle nil API client gracefully
	alerts := ht.ProcessTrade(
		context.Background(),
		"0xwallet",
		"cond123",
		"Test Market",
		"test-market",
		"SELL",
		"Yes",
		100,
		0.50,
	)

	if len(alerts) != 0 {
		t.Errorf("expected 0 alerts with nil API client, got %d", len(alerts))
	}
}

func TestHedgeTracker_ProcessUpdates_ContextCancel(t *testing.T) {
	cfg := DefaultHedgeTrackerConfig()
	ht := NewHedgeTracker(zap.NewNop(), nil, nil, cfg)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		ht.processUpdates(ctx)
		close(done)
	}()

	cancel()

	select {
	case <-done:
		// Good
	case <-time.After(100 * time.Millisecond):
		t.Error("processUpdates should stop when context is cancelled")
	}
}

func TestHedgeTracker_ProcessUpdates_DoneChannel(t *testing.T) {
	cfg := DefaultHedgeTrackerConfig()
	ht := NewHedgeTracker(zap.NewNop(), nil, nil, cfg)

	ctx := context.Background()

	done := make(chan struct{})
	go func() {
		ht.processUpdates(ctx)
		close(done)
	}()

	ht.Stop()

	select {
	case <-done:
		// Good
	case <-time.After(100 * time.Millisecond):
		t.Error("processUpdates should stop when doneCh is closed")
	}
}

func TestHedgeTracker_ApplyUpdate_HedgeState(t *testing.T) {
	cfg := DefaultHedgeTrackerConfig()
	ht := NewHedgeTracker(zap.NewNop(), nil, nil, cfg)

	state := &HedgeState{
		Wallet: "0xtest",
		Positions: map[string]HedgePosition{
			"cond1": {ConditionID: "cond1", IsHedged: true},
		},
	}

	ht.applyUpdate(hedgeUpdate{
		wallet:     "0xtest",
		updateType: "hedge_state",
		data:       state,
	})

	ht.mu.RLock()
	defer ht.mu.RUnlock()

	if ht.hedgeStates["0xtest"] == nil {
		t.Error("expected hedge state to be set")
	}
	if !ht.dirty {
		t.Error("expected dirty to be true")
	}
}

func TestHedgeTracker_ApplyUpdate_RemovalEvent(t *testing.T) {
	cfg := DefaultHedgeTrackerConfig()
	ht := NewHedgeTracker(zap.NewNop(), nil, nil, cfg)

	event := &HedgeRemovalEvent{
		ID:     "event1",
		Wallet: "0xtest",
	}

	ht.applyUpdate(hedgeUpdate{
		wallet:     "0xtest",
		updateType: "removal_event",
		data:       event,
	})

	ht.mu.RLock()
	defer ht.mu.RUnlock()

	if ht.pendingEvents["event1"] == nil {
		t.Error("expected pending event to be set")
	}
	if !ht.dirty {
		t.Error("expected dirty to be true")
	}
}

func TestHedgeTracker_ApplyUpdate_ExitRecord(t *testing.T) {
	cfg := DefaultHedgeTrackerConfig()
	ht := NewHedgeTracker(zap.NewNop(), nil, nil, cfg)

	record := &ExitRecord{
		ConditionID:  "cond1",
		IsWinner:     true,
		HoldDuration: 3600,
	}

	ht.applyUpdate(hedgeUpdate{
		wallet:     "0xtest",
		updateType: "exit_record",
		data:       record,
	})

	ht.mu.RLock()
	defer ht.mu.RUnlock()

	if ht.exitStats["0xtest"] == nil {
		t.Error("expected exit stats to be created")
	}
	if ht.exitStats["0xtest"].WinningExits != 1 {
		t.Error("expected 1 winning exit")
	}
	if !ht.dirty {
		t.Error("expected dirty to be true")
	}
}

func TestHedgeTracker_ApplyUpdate_Resolution(t *testing.T) {
	cfg := DefaultHedgeTrackerConfig()
	ht := NewHedgeTracker(zap.NewNop(), nil, nil, cfg)

	event := &HedgeRemovalEvent{
		ID:       "event1",
		Wallet:   "0xtest",
		Resolved: true,
	}

	ht.applyUpdate(hedgeUpdate{
		wallet:     "0xtest",
		updateType: "resolution",
		data:       event,
	})

	ht.mu.RLock()
	defer ht.mu.RUnlock()

	if ht.pendingEvents["event1"] == nil {
		t.Error("expected pending event to be updated")
	}
	if !ht.dirty {
		t.Error("expected dirty to be true")
	}
}

func TestHedgeTracker_ApplyUpdate_InvalidData(t *testing.T) {
	cfg := DefaultHedgeTrackerConfig()
	ht := NewHedgeTracker(zap.NewNop(), nil, nil, cfg)

	// Pass wrong type of data - should not panic
	ht.applyUpdate(hedgeUpdate{
		wallet:     "0xtest",
		updateType: "hedge_state",
		data:       "invalid",
	})

	// Should not have set anything
	ht.mu.RLock()
	defer ht.mu.RUnlock()

	if ht.hedgeStates["0xtest"] != nil {
		t.Error("expected hedge state to remain nil with invalid data")
	}
}

func TestHedgeTracker_PeriodicSave_ContextCancel(t *testing.T) {
	cfg := DefaultHedgeTrackerConfig()
	cfg.SaveInterval = 1 * time.Hour // Long interval
	ht := NewHedgeTracker(zap.NewNop(), nil, nil, cfg)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		ht.periodicSave(ctx)
		close(done)
	}()

	cancel()

	select {
	case <-done:
		// Good
	case <-time.After(200 * time.Millisecond):
		t.Error("periodicSave should stop when context is cancelled")
	}
}

func TestHedgeTracker_PeriodicSave_DoneChannel(t *testing.T) {
	cfg := DefaultHedgeTrackerConfig()
	cfg.SaveInterval = 1 * time.Hour
	ht := NewHedgeTracker(zap.NewNop(), nil, nil, cfg)

	ctx := context.Background()

	done := make(chan struct{})
	go func() {
		ht.periodicSave(ctx)
		close(done)
	}()

	ht.Stop()

	select {
	case <-done:
		// Good
	case <-time.After(200 * time.Millisecond):
		t.Error("periodicSave should stop when doneCh is closed")
	}
}

func TestHedgeTracker_RunResolutionChecker_ContextCancel(t *testing.T) {
	cfg := DefaultHedgeTrackerConfig()
	cfg.ResolutionCheckInterval = 1 * time.Hour
	ht := NewHedgeTracker(zap.NewNop(), nil, nil, cfg)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		ht.runResolutionChecker(ctx)
		close(done)
	}()

	cancel()

	select {
	case <-done:
		// Good
	case <-time.After(100 * time.Millisecond):
		t.Error("runResolutionChecker should stop when context is cancelled")
	}
}

func TestHedgeTracker_RunResolutionChecker_DoneChannel(t *testing.T) {
	cfg := DefaultHedgeTrackerConfig()
	cfg.ResolutionCheckInterval = 1 * time.Hour
	ht := NewHedgeTracker(zap.NewNop(), nil, nil, cfg)

	ctx := context.Background()

	done := make(chan struct{})
	go func() {
		ht.runResolutionChecker(ctx)
		close(done)
	}()

	ht.Stop()

	select {
	case <-done:
		// Good
	case <-time.After(100 * time.Millisecond):
		t.Error("runResolutionChecker should stop when doneCh is closed")
	}
}

func TestHedgeTracker_StartStop(t *testing.T) {
	cfg := DefaultHedgeTrackerConfig()
	cfg.SaveInterval = 1 * time.Hour
	cfg.ResolutionCheckInterval = 1 * time.Hour
	ht := NewHedgeTracker(zap.NewNop(), nil, nil, cfg)

	ctx, cancel := context.WithCancel(context.Background())

	// Start should not block
	ht.Start(ctx)

	// Give goroutines time to start
	time.Sleep(20 * time.Millisecond)

	// Stop should not block
	cancel()
	ht.Stop()

	// Give goroutines time to stop
	time.Sleep(50 * time.Millisecond)
}

func TestHedgeTracker_RecordExit_RecentExitsLimit(t *testing.T) {
	cfg := DefaultHedgeTrackerConfig()
	ht := NewHedgeTracker(zap.NewNop(), nil, nil, cfg)

	wallet := "0xwallet"

	// Add 25 exits - should keep only 20
	ht.mu.Lock()
	for i := 0; i < 25; i++ {
		ht.recordExitInternal(wallet, &ExitRecord{
			ConditionID:  "cond" + string(rune(i+'0')),
			IsWinner:     i%2 == 0,
			HoldDuration: int64(i * 1000),
		})
	}
	ht.mu.Unlock()

	stats := ht.GetAsymmetricStats(wallet)
	if stats == nil {
		t.Fatal("expected stats")
	}
	if len(stats.RecentExits) != 20 {
		t.Errorf("expected 20 recent exits, got %d", len(stats.RecentExits))
	}
}

func TestHedgeTracker_GetAsymmetricStats_NotFound(t *testing.T) {
	cfg := DefaultHedgeTrackerConfig()
	ht := NewHedgeTracker(zap.NewNop(), nil, nil, cfg)

	stats := ht.GetAsymmetricStats("0xnonexistent")
	if stats != nil {
		t.Error("expected nil for nonexistent wallet")
	}
}

func TestHedgeTracker_ShouldAlertAsymmetric_NoStats(t *testing.T) {
	cfg := DefaultHedgeTrackerConfig()
	ht := NewHedgeTracker(zap.NewNop(), nil, nil, cfg)

	if ht.ShouldAlertAsymmetric("0xnonexistent") {
		t.Error("expected false for nonexistent wallet")
	}
}

func TestHedgeTracker_ShouldAlertAsymmetric_NoWinningExits(t *testing.T) {
	cfg := DefaultHedgeTrackerConfig()
	cfg.MinExitsForAsymmetric = 2
	ht := NewHedgeTracker(zap.NewNop(), nil, nil, cfg)

	ht.mu.Lock()
	ht.exitStats["0xtest"] = &AsymmetricExitStats{
		Wallet:              "0xtest",
		WinningExits:        0,
		LosingExits:         5,
		AvgWinHoldDuration:  0, // No winning exits
		AvgLossHoldDuration: 3600,
	}
	ht.mu.Unlock()

	if ht.ShouldAlertAsymmetric("0xtest") {
		t.Error("expected false when no winning exits")
	}
}

func TestHedgeTracker_ShouldAlertAsymmetric_NoLosingExits(t *testing.T) {
	cfg := DefaultHedgeTrackerConfig()
	cfg.MinExitsForAsymmetric = 2
	ht := NewHedgeTracker(zap.NewNop(), nil, nil, cfg)

	ht.mu.Lock()
	ht.exitStats["0xtest"] = &AsymmetricExitStats{
		Wallet:              "0xtest",
		WinningExits:        5,
		LosingExits:         0,
		AvgWinHoldDuration:  3600,
		AvgLossHoldDuration: 0, // No losing exits
	}
	ht.mu.Unlock()

	if ht.ShouldAlertAsymmetric("0xtest") {
		t.Error("expected false when no losing exits")
	}
}

func TestHedgeTracker_ShouldAlertAsymmetric_BelowThreshold(t *testing.T) {
	cfg := DefaultHedgeTrackerConfig()
	cfg.MinExitsForAsymmetric = 2
	cfg.AsymmetricThreshold = 2.0
	ht := NewHedgeTracker(zap.NewNop(), nil, nil, cfg)

	ht.mu.Lock()
	ht.exitStats["0xtest"] = &AsymmetricExitStats{
		Wallet:              "0xtest",
		WinningExits:        3,
		LosingExits:         3,
		AvgWinHoldDuration:  3000,
		AvgLossHoldDuration: 4000, // 1.33x ratio - below 2x threshold
	}
	ht.mu.Unlock()

	if ht.ShouldAlertAsymmetric("0xtest") {
		t.Error("expected false when ratio below threshold")
	}
}

func TestHedgeTracker_ShouldAlertAsymmetric_Triggers(t *testing.T) {
	cfg := DefaultHedgeTrackerConfig()
	cfg.MinExitsForAsymmetric = 2
	cfg.AsymmetricThreshold = 2.0
	ht := NewHedgeTracker(zap.NewNop(), nil, nil, cfg)

	ht.mu.Lock()
	ht.exitStats["0xtest"] = &AsymmetricExitStats{
		Wallet:              "0xtest",
		WinningExits:        3,
		LosingExits:         3,
		AvgWinHoldDuration:  1000,
		AvgLossHoldDuration: 5000, // 5x ratio - should trigger
	}
	ht.mu.Unlock()

	if !ht.ShouldAlertAsymmetric("0xtest") {
		t.Error("expected true when ratio is above threshold")
	}
}

func TestHedgeTracker_ProcessUpdatesChannel(t *testing.T) {
	cfg := DefaultHedgeTrackerConfig()
	ht := NewHedgeTracker(zap.NewNop(), nil, nil, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start processing
	go ht.processUpdates(ctx)

	// Send updates via channel
	ht.updateCh <- hedgeUpdate{
		wallet:     "0xtest",
		updateType: "hedge_state",
		data: &HedgeState{
			Wallet: "0xtest",
			Positions: map[string]HedgePosition{
				"cond1": {ConditionID: "cond1"},
			},
		},
	}

	// Give time to process
	time.Sleep(50 * time.Millisecond)

	ht.mu.RLock()
	state := ht.hedgeStates["0xtest"]
	ht.mu.RUnlock()

	if state == nil {
		t.Error("expected state to be applied via channel")
	}
}

func TestHedgeTracker_LoadWithMock(t *testing.T) {
	cfg := DefaultHedgeTrackerConfig()
	cfg.GistID = "test-gist"
	cfg.FileName = "hedge_tracker.json"

	mockGist := NewMockGistStorage()
	ht := NewHedgeTracker(zap.NewNop(), nil, mockGist, cfg)

	// Set up mock content
	snapshotJSON := `{
		"version": 1,
		"timestamp": "2024-01-01T00:00:00Z",
		"hedge_states": {
			"0xwallet1": {
				"wallet": "0xwallet1",
				"positions": {
					"cond1": {"condition_id": "cond1", "yes_size": 100, "no_size": 100, "is_hedged": true}
				}
			}
		},
		"pending_events": {
			"event1": {"id": "event1", "wallet": "0xwallet1", "resolved": false}
		},
		"exit_stats": {
			"0xwallet1": {"wallet": "0xwallet1", "winning_exits": 3, "losing_exits": 2}
		}
	}`
	mockGist.SetContent("hedge_tracker.json", snapshotJSON)

	err := ht.Load(context.Background())
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Verify loaded data
	ht.mu.RLock()
	defer ht.mu.RUnlock()

	if len(ht.hedgeStates) != 1 {
		t.Errorf("expected 1 hedge state, got %d", len(ht.hedgeStates))
	}
	if len(ht.pendingEvents) != 1 {
		t.Errorf("expected 1 pending event, got %d", len(ht.pendingEvents))
	}
	if len(ht.exitStats) != 1 {
		t.Errorf("expected 1 exit stat, got %d", len(ht.exitStats))
	}

	stats := ht.exitStats["0xwallet1"]
	if stats == nil || stats.WinningExits != 3 || stats.LosingExits != 2 {
		t.Error("exit stats not loaded correctly")
	}
}

func TestHedgeTracker_SaveWithMock(t *testing.T) {
	cfg := DefaultHedgeTrackerConfig()
	cfg.GistID = "test-gist"
	cfg.FileName = "hedge_tracker.json"

	mockGist := NewMockGistStorage()
	ht := NewHedgeTracker(zap.NewNop(), nil, mockGist, cfg)

	// Add data
	ht.mu.Lock()
	ht.hedgeStates["0xwallet1"] = &HedgeState{
		Wallet: "0xwallet1",
		Positions: map[string]HedgePosition{
			"cond1": {ConditionID: "cond1", IsHedged: true},
		},
	}
	ht.pendingEvents["event1"] = &HedgeRemovalEvent{ID: "event1", Wallet: "0xwallet1"}
	ht.exitStats["0xwallet1"] = &AsymmetricExitStats{Wallet: "0xwallet1", WinningExits: 5}
	ht.dirty = true
	ht.mu.Unlock()

	err := ht.Save(context.Background())
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Verify saved content
	content := mockGist.GetContent("hedge_tracker.json")
	if content == "" {
		t.Fatal("expected content to be saved")
	}

	// Check dirty flag cleared
	if ht.dirty {
		t.Error("expected dirty flag to be cleared")
	}
}

func TestHedgeTracker_Save_NotDirty(t *testing.T) {
	cfg := DefaultHedgeTrackerConfig()
	cfg.GistID = "test-gist"

	mockGist := NewMockGistStorage()
	ht := NewHedgeTracker(zap.NewNop(), nil, mockGist, cfg)

	// Not dirty
	err := ht.Save(context.Background())
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Nothing should be saved
	if mockGist.GetContent(cfg.FileName) != "" {
		t.Error("expected no content saved when not dirty")
	}
}

func TestHedgeTracker_Save_NotEnabled(t *testing.T) {
	cfg := DefaultHedgeTrackerConfig()
	cfg.GistID = "" // Not enabled

	ht := NewHedgeTracker(zap.NewNop(), nil, nil, cfg)
	ht.dirty = true

	err := ht.Save(context.Background())
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestHedgeTracker_Load_NotEnabled(t *testing.T) {
	cfg := DefaultHedgeTrackerConfig()
	cfg.GistID = "" // Not enabled

	ht := NewHedgeTracker(zap.NewNop(), nil, nil, cfg)

	err := ht.Load(context.Background())
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestHedgeTracker_Load_EmptyContent(t *testing.T) {
	cfg := DefaultHedgeTrackerConfig()
	cfg.GistID = "test-gist"

	mockGist := NewMockGistStorage()
	// Don't set any content - will return empty string
	ht := NewHedgeTracker(zap.NewNop(), nil, mockGist, cfg)

	err := ht.Load(context.Background())
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestHedgeTracker_SaveError(t *testing.T) {
	cfg := DefaultHedgeTrackerConfig()
	cfg.GistID = "test-gist"

	mockGist := NewMockGistStorage()
	mockGist.SetSaveError(errors.New("mock save error"))
	ht := NewHedgeTracker(zap.NewNop(), nil, mockGist, cfg)

	ht.dirty = true

	err := ht.Save(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}

	// Dirty flag should remain set
	if !ht.dirty {
		t.Error("expected dirty flag to remain set")
	}
}

func TestHedgeTracker_LoadError(t *testing.T) {
	cfg := DefaultHedgeTrackerConfig()
	cfg.GistID = "test-gist"

	mockGist := NewMockGistStorage()
	mockGist.SetLoadError(errors.New("mock load error"))
	ht := NewHedgeTracker(zap.NewNop(), nil, mockGist, cfg)

	err := ht.Load(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
}
