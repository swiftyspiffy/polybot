package app

import (
	"context"
	"errors"
	"polybot/clients/polymarketapi"
	"testing"
	"time"

	"go.uber.org/zap"
)

// mockPatternAPIClient implements PatternAPIClient for testing.
type mockPatternAPIClient struct {
	positions []polymarketapi.Position
	err       error
}

func (m *mockPatternAPIClient) GetPositions(ctx context.Context, wallet string, conditionID string, limit int) ([]polymarketapi.Position, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.positions, nil
}

func TestDefaultPatternTrackerConfig(t *testing.T) {
	cfg := DefaultPatternTrackerConfig()

	if cfg.FileName != "pattern_tracker.json" {
		t.Errorf("expected filename pattern_tracker.json, got %s", cfg.FileName)
	}
	if cfg.SaveInterval != 5*time.Minute {
		t.Errorf("expected save interval 5m, got %v", cfg.SaveInterval)
	}

	// Conviction Doubling defaults
	if cfg.ConvictionMinAddSize != 500 {
		t.Errorf("expected conviction min add size 500, got %f", cfg.ConvictionMinAddSize)
	}
	if cfg.ConvictionMinAddValue != 1000 {
		t.Errorf("expected conviction min add value 1000, got %f", cfg.ConvictionMinAddValue)
	}
	if cfg.ConvictionMinLossPct != 0.10 {
		t.Errorf("expected conviction min loss pct 0.10, got %f", cfg.ConvictionMinLossPct)
	}

	// Perfect Exit Timing defaults
	if cfg.PerfectExitCheckDelay != 24*time.Hour {
		t.Errorf("expected perfect exit check delay 24h, got %v", cfg.PerfectExitCheckDelay)
	}
	if cfg.PerfectExitMinExits != 5 {
		t.Errorf("expected perfect exit min exits 5, got %d", cfg.PerfectExitMinExits)
	}
	if cfg.PerfectExitMinScore != 0.90 {
		t.Errorf("expected perfect exit min score 0.90, got %f", cfg.PerfectExitMinScore)
	}

	// Stealth Accumulation defaults
	if cfg.StealthTimeWindow != 6*time.Hour {
		t.Errorf("expected stealth time window 6h, got %v", cfg.StealthTimeWindow)
	}
	if cfg.StealthMinTrades != 3 {
		t.Errorf("expected stealth min trades 3, got %d", cfg.StealthMinTrades)
	}
	if cfg.StealthMinTotalSize != 5000 {
		t.Errorf("expected stealth min total size 5000, got %f", cfg.StealthMinTotalSize)
	}
	if cfg.StealthMinTotalValue != 10000 {
		t.Errorf("expected stealth min total value 10000, got %f", cfg.StealthMinTotalValue)
	}
	if cfg.StealthMaxSingleTrade != 25000 {
		t.Errorf("expected stealth max single trade 25000, got %f", cfg.StealthMaxSingleTrade)
	}
	if cfg.StealthMinSpreadMinutes != 60 {
		t.Errorf("expected stealth min spread minutes 60, got %d", cfg.StealthMinSpreadMinutes)
	}

	// Rate limiting defaults
	if cfg.PositionCheckInterval != 5*time.Minute {
		t.Errorf("expected position check interval 5m, got %v", cfg.PositionCheckInterval)
	}
	if cfg.MaxPositionChecks != 60 {
		t.Errorf("expected max position checks 60, got %d", cfg.MaxPositionChecks)
	}
}

func TestNewPatternTracker(t *testing.T) {
	cfg := DefaultPatternTrackerConfig()
	pt := NewPatternTracker(zap.NewNop(), nil, nil, cfg)

	if pt.logger == nil {
		t.Error("expected logger to be set")
	}
	if pt.pendingExits == nil {
		t.Error("expected pendingExits to be initialized")
	}
	if pt.exitTimingStats == nil {
		t.Error("expected exitTimingStats to be initialized")
	}
	if pt.accumulations == nil {
		t.Error("expected accumulations to be initialized")
	}
	if pt.alertedAccumulations == nil {
		t.Error("expected alertedAccumulations to be initialized")
	}
	if pt.lastPositionCheck == nil {
		t.Error("expected lastPositionCheck to be initialized")
	}
}

func TestPatternTracker_IsEnabled(t *testing.T) {
	// Without GistID - disabled
	cfg := DefaultPatternTrackerConfig()
	cfg.GistID = ""
	pt := NewPatternTracker(zap.NewNop(), nil, nil, cfg)
	if pt.IsEnabled() {
		t.Error("expected disabled without GistID")
	}

	// With GistID but no gistClient - disabled
	cfg.GistID = "test-gist-id"
	pt = NewPatternTracker(zap.NewNop(), nil, nil, cfg)
	if pt.IsEnabled() {
		t.Error("expected disabled without gistClient")
	}
}

func TestPatternTracker_ShouldCheckPosition_RateLimiting(t *testing.T) {
	cfg := DefaultPatternTrackerConfig()
	cfg.MaxPositionChecks = 2
	cfg.PositionCheckInterval = time.Minute
	pt := NewPatternTracker(zap.NewNop(), nil, nil, cfg)

	// First two checks should pass
	if !pt.ShouldCheckPosition("wallet1", "condition1") {
		t.Error("first check should pass")
	}
	if !pt.ShouldCheckPosition("wallet2", "condition2") {
		t.Error("second check should pass")
	}

	// Third check should fail (rate limit)
	if pt.ShouldCheckPosition("wallet3", "condition3") {
		t.Error("third check should fail due to rate limit")
	}

	// Same wallet+condition should fail (cooldown)
	if pt.ShouldCheckPosition("wallet1", "condition1") {
		t.Error("same wallet+condition should fail due to cooldown")
	}
}

func TestPatternTracker_ProcessTrade_ConvictionDoubling_NoAPI(t *testing.T) {
	cfg := DefaultPatternTrackerConfig()
	pt := NewPatternTracker(zap.NewNop(), nil, nil, cfg)

	alerts := pt.ProcessTrade(context.Background(), ProcessTradeInput{
		Wallet:      "0xtest",
		ConditionID: "condition123",
		MarketTitle: "Test Market",
		MarketSlug:  "test-market",
		Outcome:     "Yes",
		Side:        "BUY",
		Size:        1000,
		Price:       0.50,
		Notional:    500,
		Timestamp:   time.Now(),
	})

	// Without API client, no conviction alerts should be generated
	for _, a := range alerts {
		if a.Reason == "conviction_doubling" {
			t.Error("should not generate conviction alert without API client")
		}
	}
}

func TestPatternTracker_ProcessTrade_ConvictionDoubling_NewPosition(t *testing.T) {
	mockAPI := &mockPatternAPIClient{
		positions: []polymarketapi.Position{
			{
				Outcome:  "Yes",
				Size:     1000, // This IS the trade, no prior position
				AvgPrice: 0.50,
				CurPrice: 0.40,
			},
		},
	}

	cfg := DefaultPatternTrackerConfig()
	cfg.MaxPositionChecks = 100
	cfg.PositionCheckInterval = 0
	pt := NewPatternTracker(zap.NewNop(), mockAPI, nil, cfg)

	alerts := pt.ProcessTrade(context.Background(), ProcessTradeInput{
		Wallet:      "0xtest",
		ConditionID: "condition123",
		MarketTitle: "Test Market",
		MarketSlug:  "test-market",
		Outcome:     "Yes",
		Side:        "BUY",
		Size:        1000,
		Price:       0.40,
		Notional:    1500,
		Timestamp:   time.Now(),
	})

	// No conviction alert for new position
	for _, a := range alerts {
		if a.Reason == "conviction_doubling" {
			t.Error("should not generate conviction alert for new position")
		}
	}
}

func TestPatternTracker_ProcessTrade_ConvictionDoubling_AddingToLosingPosition(t *testing.T) {
	mockAPI := &mockPatternAPIClient{
		positions: []polymarketapi.Position{
			{
				Outcome:  "Yes",
				Size:     2000, // 1500 existing + 500 new = 2000
				AvgPrice: 0.50,
				CurPrice: 0.40, // 20% loss
			},
		},
	}

	cfg := DefaultPatternTrackerConfig()
	cfg.MaxPositionChecks = 100
	cfg.PositionCheckInterval = 0
	cfg.ConvictionMinAddSize = 500
	cfg.ConvictionMinAddValue = 1000
	cfg.ConvictionMinLossPct = 0.10
	pt := NewPatternTracker(zap.NewNop(), mockAPI, nil, cfg)

	alerts := pt.ProcessTrade(context.Background(), ProcessTradeInput{
		Wallet:      "0xtest",
		ConditionID: "condition123",
		MarketTitle: "Test Market",
		MarketSlug:  "test-market",
		Outcome:     "Yes",
		Side:        "BUY",
		Size:        500,
		Price:       0.40,
		Notional:    1500,
		Timestamp:   time.Now(),
	})

	// Should generate conviction alert
	found := false
	for _, a := range alerts {
		if a.Reason == "conviction_doubling" {
			found = true
			if a.ConvictionLossPct < 0.15 || a.ConvictionLossPct > 0.25 {
				t.Errorf("expected loss pct around 0.20, got %f", a.ConvictionLossPct)
			}
			if a.ConvictionAddedSize != 500 {
				t.Errorf("expected added size 500, got %f", a.ConvictionAddedSize)
			}
		}
	}
	if !found {
		t.Error("expected conviction doubling alert")
	}
}

func TestPatternTracker_ProcessTrade_ConvictionDoubling_PositionNotLosing(t *testing.T) {
	mockAPI := &mockPatternAPIClient{
		positions: []polymarketapi.Position{
			{
				Outcome:  "Yes",
				Size:     2000,
				AvgPrice: 0.40,
				CurPrice: 0.50, // Winning position
			},
		},
	}

	cfg := DefaultPatternTrackerConfig()
	cfg.MaxPositionChecks = 100
	cfg.PositionCheckInterval = 0
	pt := NewPatternTracker(zap.NewNop(), mockAPI, nil, cfg)

	alerts := pt.ProcessTrade(context.Background(), ProcessTradeInput{
		Wallet:      "0xtest",
		ConditionID: "condition123",
		MarketTitle: "Test Market",
		MarketSlug:  "test-market",
		Outcome:     "Yes",
		Side:        "BUY",
		Size:        500,
		Price:       0.50,
		Notional:    1500,
		Timestamp:   time.Now(),
	})

	// No conviction alert for winning position
	for _, a := range alerts {
		if a.Reason == "conviction_doubling" {
			t.Error("should not generate conviction alert for winning position")
		}
	}
}

func TestPatternTracker_ProcessTrade_ConvictionDoubling_APIError(t *testing.T) {
	mockAPI := &mockPatternAPIClient{
		err: errors.New("api error"),
	}

	cfg := DefaultPatternTrackerConfig()
	cfg.MaxPositionChecks = 100
	cfg.PositionCheckInterval = 0
	pt := NewPatternTracker(zap.NewNop(), mockAPI, nil, cfg)

	alerts := pt.ProcessTrade(context.Background(), ProcessTradeInput{
		Wallet:      "0xtest",
		ConditionID: "condition123",
		MarketTitle: "Test Market",
		MarketSlug:  "test-market",
		Outcome:     "Yes",
		Side:        "BUY",
		Size:        500,
		Price:       0.40,
		Notional:    1500,
		Timestamp:   time.Now(),
	})

	// No conviction alert on API error
	for _, a := range alerts {
		if a.Reason == "conviction_doubling" {
			t.Error("should not generate conviction alert on API error")
		}
	}
}

func TestPatternTracker_ProcessTrade_StealthAccumulation(t *testing.T) {
	cfg := DefaultPatternTrackerConfig()
	cfg.StealthMinTrades = 3
	cfg.StealthMinTotalSize = 100
	cfg.StealthMinTotalValue = 100
	cfg.StealthMinSpreadMinutes = 1
	cfg.StealthTimeWindow = 1 * time.Hour
	cfg.StealthMaxSingleTrade = 10000
	pt := NewPatternTracker(zap.NewNop(), nil, nil, cfg)

	baseTime := time.Now().Add(-30 * time.Minute)

	// First trade - no alert
	alerts := pt.ProcessTrade(context.Background(), ProcessTradeInput{
		Wallet:      "0xtest",
		ConditionID: "condition123",
		MarketTitle: "Test Market",
		MarketSlug:  "test-market",
		Outcome:     "Yes",
		Side:        "BUY",
		Size:        50,
		Price:       0.50,
		Notional:    25,
		Timestamp:   baseTime,
	})
	if len(alerts) > 0 {
		t.Error("first trade should not trigger alert")
	}

	// Second trade - no alert
	alerts = pt.ProcessTrade(context.Background(), ProcessTradeInput{
		Wallet:      "0xtest",
		ConditionID: "condition123",
		MarketTitle: "Test Market",
		MarketSlug:  "test-market",
		Outcome:     "Yes",
		Side:        "BUY",
		Size:        50,
		Price:       0.50,
		Notional:    25,
		Timestamp:   baseTime.Add(10 * time.Minute),
	})
	if len(alerts) > 0 {
		t.Error("second trade should not trigger alert")
	}

	// Third trade - should trigger stealth accumulation alert
	alerts = pt.ProcessTrade(context.Background(), ProcessTradeInput{
		Wallet:      "0xtest",
		ConditionID: "condition123",
		MarketTitle: "Test Market",
		MarketSlug:  "test-market",
		Outcome:     "Yes",
		Side:        "BUY",
		Size:        50,
		Price:       0.50,
		Notional:    100,
		Timestamp:   baseTime.Add(20 * time.Minute),
	})

	found := false
	for _, a := range alerts {
		if a.Reason == "stealth_accumulation" {
			found = true
			if a.StealthTradeCount != 3 {
				t.Errorf("expected 3 trades, got %d", a.StealthTradeCount)
			}
		}
	}
	if !found {
		t.Error("expected stealth accumulation alert")
	}
}

func TestPatternTracker_ProcessTrade_StealthAccumulation_LargeTrade(t *testing.T) {
	cfg := DefaultPatternTrackerConfig()
	cfg.StealthMaxSingleTrade = 100
	pt := NewPatternTracker(zap.NewNop(), nil, nil, cfg)

	// Large trade should not be tracked for stealth
	alerts := pt.ProcessTrade(context.Background(), ProcessTradeInput{
		Wallet:      "0xtest",
		ConditionID: "condition123",
		MarketTitle: "Test Market",
		MarketSlug:  "test-market",
		Outcome:     "Yes",
		Side:        "BUY",
		Size:        1000,
		Price:       0.50,
		Notional:    500, // Exceeds StealthMaxSingleTrade
		Timestamp:   time.Now(),
	})

	for _, a := range alerts {
		if a.Reason == "stealth_accumulation" {
			t.Error("large trade should not trigger stealth alert")
		}
	}
}

func TestPatternTracker_ProcessTrade_RecordsSellExits(t *testing.T) {
	cfg := DefaultPatternTrackerConfig()
	pt := NewPatternTracker(zap.NewNop(), nil, nil, cfg)

	pt.ProcessTrade(context.Background(), ProcessTradeInput{
		Wallet:      "0xtest",
		ConditionID: "condition123",
		MarketTitle: "Test Market",
		MarketSlug:  "test-market",
		Outcome:     "Yes",
		Side:        "SELL",
		Size:        100,
		Price:       0.60,
		Notional:    60,
		Timestamp:   time.Now(),
	})

	// Check that exit was recorded
	if len(pt.pendingExits) != 1 {
		t.Errorf("expected 1 pending exit, got %d", len(pt.pendingExits))
	}
}

func TestPatternTracker_ProcessTrade_BuyDoesNotRecordExit(t *testing.T) {
	cfg := DefaultPatternTrackerConfig()
	pt := NewPatternTracker(zap.NewNop(), nil, nil, cfg)

	pt.ProcessTrade(context.Background(), ProcessTradeInput{
		Wallet:      "0xtest",
		ConditionID: "condition123",
		MarketTitle: "Test Market",
		MarketSlug:  "test-market",
		Outcome:     "Yes",
		Side:        "BUY",
		Size:        100,
		Price:       0.50,
		Notional:    50,
		Timestamp:   time.Now(),
	})

	// Check that no exit was recorded
	if len(pt.pendingExits) != 0 {
		t.Errorf("expected 0 pending exits, got %d", len(pt.pendingExits))
	}
}

func TestPatternTracker_ShouldAlertPerfectTiming(t *testing.T) {
	cfg := DefaultPatternTrackerConfig()
	cfg.PerfectExitMinExits = 3
	cfg.PerfectExitMinScore = 0.85
	pt := NewPatternTracker(zap.NewNop(), nil, nil, cfg)

	// No stats - no alert
	if pt.ShouldAlertPerfectTiming("wallet1") {
		t.Error("should not alert with no stats")
	}

	// Add stats below threshold
	pt.exitTimingStats["wallet1"] = &ExitTimingStats{
		Wallet:        "wallet1",
		VerifiedExits: 2,
		AvgTimingScore: 0.90,
	}
	if pt.ShouldAlertPerfectTiming("wallet1") {
		t.Error("should not alert with exits below minimum")
	}

	// Add stats at threshold but low score
	pt.exitTimingStats["wallet2"] = &ExitTimingStats{
		Wallet:        "wallet2",
		VerifiedExits: 5,
		AvgTimingScore: 0.80,
	}
	if pt.ShouldAlertPerfectTiming("wallet2") {
		t.Error("should not alert with score below minimum")
	}

	// Add stats meeting all criteria
	pt.exitTimingStats["wallet3"] = &ExitTimingStats{
		Wallet:        "wallet3",
		VerifiedExits: 5,
		AvgTimingScore: 0.92,
		PerfectExits:  3,
	}
	if !pt.ShouldAlertPerfectTiming("wallet3") {
		t.Error("should alert when meeting all criteria")
	}
}

func TestPatternTracker_GetExitTimingStats(t *testing.T) {
	cfg := DefaultPatternTrackerConfig()
	pt := NewPatternTracker(zap.NewNop(), nil, nil, cfg)

	// No stats
	stats := pt.GetExitTimingStats("wallet1")
	if stats != nil {
		t.Error("expected nil stats for unknown wallet")
	}

	// Add stats
	pt.exitTimingStats["wallet1"] = &ExitTimingStats{
		Wallet:        "wallet1",
		VerifiedExits: 10,
		AvgTimingScore: 0.95,
		PerfectExits:  5,
	}

	stats = pt.GetExitTimingStats("wallet1")
	if stats == nil {
		t.Fatal("expected stats for known wallet")
	}
	if stats.VerifiedExits != 10 {
		t.Errorf("expected 10 verified exits, got %d", stats.VerifiedExits)
	}
	if stats.AvgTimingScore != 0.95 {
		t.Errorf("expected avg timing score 0.95, got %f", stats.AvgTimingScore)
	}
	if stats.PerfectExits != 5 {
		t.Errorf("expected 5 perfect exits, got %d", stats.PerfectExits)
	}
}

func TestPatternTracker_Stats(t *testing.T) {
	cfg := DefaultPatternTrackerConfig()
	pt := NewPatternTracker(zap.NewNop(), nil, nil, cfg)

	// Add some data
	pt.pendingExits["exit1"] = &ExitTimingRecord{}
	pt.pendingExits["exit2"] = &ExitTimingRecord{}
	pt.exitTimingStats["wallet1"] = &ExitTimingStats{}
	pt.accumulations["acc1"] = &AccumulationRecord{}
	pt.accumulations["acc2"] = &AccumulationRecord{}
	pt.accumulations["acc3"] = &AccumulationRecord{}

	pending, verified, accumulations := pt.Stats()

	if pending != 2 {
		t.Errorf("expected 2 pending exits, got %d", pending)
	}
	if verified != 1 {
		t.Errorf("expected 1 verified wallet, got %d", verified)
	}
	if accumulations != 3 {
		t.Errorf("expected 3 accumulations, got %d", accumulations)
	}
}

func TestPatternTracker_NilLogger(t *testing.T) {
	cfg := DefaultPatternTrackerConfig()
	pt := NewPatternTracker(nil, nil, nil, cfg)

	if pt.logger == nil {
		t.Error("expected nop logger when nil is passed")
	}

	// Should not panic with nil logger
	pt.ProcessTrade(context.Background(), ProcessTradeInput{
		Wallet:      "0xtest",
		ConditionID: "condition123",
		MarketTitle: "Test Market",
		MarketSlug:  "test-market",
		Outcome:     "Yes",
		Side:        "BUY",
		Size:        100,
		Price:       0.50,
		Notional:    50,
		Timestamp:   time.Now(),
	})
}

func TestPatternTracker_StartStop(t *testing.T) {
	cfg := DefaultPatternTrackerConfig()
	cfg.PerfectExitCheckInterval = 10 * time.Millisecond
	cfg.SaveInterval = 10 * time.Millisecond
	pt := NewPatternTracker(zap.NewNop(), nil, nil, cfg)

	ctx, cancel := context.WithCancel(context.Background())

	// Start should not panic
	pt.Start(ctx)

	// Let goroutines run briefly
	time.Sleep(50 * time.Millisecond)

	// Stop should not panic
	pt.Stop()
	cancel()

	// Give goroutines time to exit
	time.Sleep(20 * time.Millisecond)
}

func TestPatternTracker_UpdateExitTimingStats(t *testing.T) {
	cfg := DefaultPatternTrackerConfig()
	pt := NewPatternTracker(zap.NewNop(), nil, nil, cfg)

	// First exit for wallet
	record1 := &ExitTimingRecord{
		Wallet:      "wallet1",
		TimingScore: 0.90,
	}
	pt.updateExitTimingStats(record1)

	stats := pt.exitTimingStats["wallet1"]
	if stats == nil {
		t.Fatal("expected stats to be created")
	}
	if stats.VerifiedExits != 1 {
		t.Errorf("expected 1 verified exit, got %d", stats.VerifiedExits)
	}
	if stats.AvgTimingScore != 0.90 {
		t.Errorf("expected avg timing score 0.90, got %f", stats.AvgTimingScore)
	}
	if stats.PerfectExits != 0 {
		t.Errorf("expected 0 perfect exits, got %d", stats.PerfectExits)
	}

	// Second exit (perfect timing)
	record2 := &ExitTimingRecord{
		Wallet:      "wallet1",
		TimingScore: 0.98,
	}
	pt.updateExitTimingStats(record2)

	stats = pt.exitTimingStats["wallet1"]
	if stats.VerifiedExits != 2 {
		t.Errorf("expected 2 verified exits, got %d", stats.VerifiedExits)
	}
	if stats.PerfectExits != 1 {
		t.Errorf("expected 1 perfect exit (score >= 0.95), got %d", stats.PerfectExits)
	}
	expectedAvg := (0.90 + 0.98) / 2
	if stats.AvgTimingScore < expectedAvg-0.01 || stats.AvgTimingScore > expectedAvg+0.01 {
		t.Errorf("expected avg timing score around %f, got %f", expectedAvg, stats.AvgTimingScore)
	}
}

func TestPatternTracker_CheckPendingExits(t *testing.T) {
	// Need mock API client for checkPendingExits to process
	mockAPI := &mockPatternAPIClient{
		positions: []polymarketapi.Position{},
	}

	cfg := DefaultPatternTrackerConfig()
	cfg.PerfectExitCheckDelay = 1 * time.Millisecond // Very short for testing
	pt := NewPatternTracker(zap.NewNop(), mockAPI, nil, cfg)

	// Add a pending exit that's old enough to be checked
	oldTime := time.Now().Add(-1 * time.Hour)
	pt.pendingExits["exit1"] = &ExitTimingRecord{
		ID:        "exit1",
		Wallet:    "wallet1",
		ExitPrice: 0.70,
		ExitTime:  oldTime,
		Verified:  false,
	}

	// Add a pending exit that's too recent
	pt.pendingExits["exit2"] = &ExitTimingRecord{
		ID:        "exit2",
		Wallet:    "wallet2",
		ExitPrice: 0.60,
		ExitTime:  time.Now(),
		Verified:  false,
	}

	// Run check
	pt.checkPendingExits(context.Background())

	// Old exit should be verified (with placeholder score since no API)
	exit1 := pt.pendingExits["exit1"]
	if exit1 == nil {
		t.Fatal("exit1 should still exist")
	}
	if !exit1.Verified {
		t.Error("exit1 should be marked as verified")
	}

	// Recent exit should not be verified
	exit2 := pt.pendingExits["exit2"]
	if exit2 == nil {
		t.Fatal("exit2 should still exist")
	}
	if exit2.Verified {
		t.Error("exit2 should not be verified yet")
	}

	// Check that wallet stats were updated
	stats := pt.exitTimingStats["wallet1"]
	if stats == nil {
		t.Error("expected wallet1 stats to be created")
	}
}

func TestPatternTracker_CheckPendingExits_NoAPIClient(t *testing.T) {
	cfg := DefaultPatternTrackerConfig()
	cfg.PerfectExitCheckDelay = 1 * time.Millisecond
	pt := NewPatternTracker(zap.NewNop(), nil, nil, cfg) // No API client

	// Add a pending exit
	pt.pendingExits["exit1"] = &ExitTimingRecord{
		ID:        "exit1",
		Wallet:    "wallet1",
		ExitPrice: 0.70,
		ExitTime:  time.Now().Add(-1 * time.Hour),
		Verified:  false,
	}

	// Run check - should not panic and skip verification without API
	pt.checkPendingExits(context.Background())

	// Exit should NOT be verified since no API client
	exit1 := pt.pendingExits["exit1"]
	if exit1 == nil {
		t.Fatal("exit1 should still exist")
	}
	if exit1.Verified {
		t.Error("exit1 should not be verified without API client")
	}
}

func TestPatternTracker_CheckPendingExits_CleansOldVerified(t *testing.T) {
	cfg := DefaultPatternTrackerConfig()
	cfg.PerfectExitCheckDelay = 1 * time.Millisecond
	pt := NewPatternTracker(zap.NewNop(), nil, nil, cfg)

	// Add a verified exit that's very old (should be cleaned up)
	pt.pendingExits["old_verified"] = &ExitTimingRecord{
		ID:        "old_verified",
		Wallet:    "wallet1",
		ExitTime:  time.Now().Add(-10 * 24 * time.Hour), // 10 days ago
		Verified:  true,
		CheckedAt: time.Now().Add(-8 * 24 * time.Hour), // Verified 8 days ago
	}

	// Run check
	pt.checkPendingExits(context.Background())

	// Old verified exit should be cleaned up
	if _, exists := pt.pendingExits["old_verified"]; exists {
		t.Error("old verified exit should be cleaned up")
	}
}

func TestPatternTracker_Load_NoGistClient(t *testing.T) {
	cfg := DefaultPatternTrackerConfig()
	pt := NewPatternTracker(zap.NewNop(), nil, nil, cfg)

	// Load should return nil when no gist client
	err := pt.Load(context.Background())
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

func TestPatternTracker_Load_NoGistID(t *testing.T) {
	cfg := DefaultPatternTrackerConfig()
	cfg.GistID = ""
	pt := NewPatternTracker(zap.NewNop(), nil, nil, cfg)

	// Load should return nil when no gist ID
	err := pt.Load(context.Background())
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

func TestPatternTracker_Save_NoGistClient(t *testing.T) {
	cfg := DefaultPatternTrackerConfig()
	pt := NewPatternTracker(zap.NewNop(), nil, nil, cfg)

	// Save should return nil when no gist client
	err := pt.Save(context.Background())
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

func TestPatternTracker_Save_NoGistID(t *testing.T) {
	cfg := DefaultPatternTrackerConfig()
	cfg.GistID = ""
	pt := NewPatternTracker(zap.NewNop(), nil, nil, cfg)

	// Save should return nil when no gist ID
	err := pt.Save(context.Background())
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

func TestPatternTracker_ConvictionDoubling_BelowMinThresholds(t *testing.T) {
	mockAPI := &mockPatternAPIClient{
		positions: []polymarketapi.Position{
			{
				Outcome:  "Yes",
				Size:     2000,
				AvgPrice: 0.50,
				CurPrice: 0.40, // 20% loss
			},
		},
	}

	cfg := DefaultPatternTrackerConfig()
	cfg.MaxPositionChecks = 100
	cfg.PositionCheckInterval = 0
	cfg.ConvictionMinAddSize = 1000  // Require 1000 shares
	cfg.ConvictionMinAddValue = 5000 // Require $5000
	pt := NewPatternTracker(zap.NewNop(), mockAPI, nil, cfg)

	// Trade below minimum thresholds
	alerts := pt.ProcessTrade(context.Background(), ProcessTradeInput{
		Wallet:      "0xtest",
		ConditionID: "condition123",
		MarketTitle: "Test Market",
		MarketSlug:  "test-market",
		Outcome:     "Yes",
		Side:        "BUY",
		Size:        500,   // Below 1000 min
		Price:       0.40,
		Notional:    200,   // Below $5000 min
		Timestamp:   time.Now(),
	})

	// Should not generate alert due to thresholds
	for _, a := range alerts {
		if a.Reason == "conviction_doubling" {
			t.Error("should not generate conviction alert when below thresholds")
		}
	}
}

func TestPatternTracker_ConvictionDoubling_LossBelowThreshold(t *testing.T) {
	mockAPI := &mockPatternAPIClient{
		positions: []polymarketapi.Position{
			{
				Outcome:  "Yes",
				Size:     2000,
				AvgPrice: 0.50,
				CurPrice: 0.48, // Only 4% loss
			},
		},
	}

	cfg := DefaultPatternTrackerConfig()
	cfg.MaxPositionChecks = 100
	cfg.PositionCheckInterval = 0
	cfg.ConvictionMinAddSize = 100
	cfg.ConvictionMinAddValue = 100
	cfg.ConvictionMinLossPct = 0.10 // Require 10% loss
	pt := NewPatternTracker(zap.NewNop(), mockAPI, nil, cfg)

	alerts := pt.ProcessTrade(context.Background(), ProcessTradeInput{
		Wallet:      "0xtest",
		ConditionID: "condition123",
		MarketTitle: "Test Market",
		MarketSlug:  "test-market",
		Outcome:     "Yes",
		Side:        "BUY",
		Size:        500,
		Price:       0.48,
		Notional:    1500,
		Timestamp:   time.Now(),
	})

	// Should not generate alert - loss is only 4%, threshold is 10%
	for _, a := range alerts {
		if a.Reason == "conviction_doubling" {
			t.Error("should not generate conviction alert when loss below threshold")
		}
	}
}

func TestPatternTracker_ConvictionDoubling_NoMatchingOutcome(t *testing.T) {
	mockAPI := &mockPatternAPIClient{
		positions: []polymarketapi.Position{
			{
				Outcome:  "No", // Different outcome
				Size:     2000,
				AvgPrice: 0.50,
				CurPrice: 0.40,
			},
		},
	}

	cfg := DefaultPatternTrackerConfig()
	cfg.MaxPositionChecks = 100
	cfg.PositionCheckInterval = 0
	pt := NewPatternTracker(zap.NewNop(), mockAPI, nil, cfg)

	alerts := pt.ProcessTrade(context.Background(), ProcessTradeInput{
		Wallet:      "0xtest",
		ConditionID: "condition123",
		MarketTitle: "Test Market",
		MarketSlug:  "test-market",
		Outcome:     "Yes", // Different from position
		Side:        "BUY",
		Size:        500,
		Price:       0.40,
		Notional:    1500,
		Timestamp:   time.Now(),
	})

	// Should not generate alert - no matching position
	for _, a := range alerts {
		if a.Reason == "conviction_doubling" {
			t.Error("should not generate conviction alert when no matching outcome")
		}
	}
}

func TestPatternTracker_StealthAccumulation_NotEnoughSpread(t *testing.T) {
	cfg := DefaultPatternTrackerConfig()
	cfg.StealthMinTrades = 3
	cfg.StealthMinTotalSize = 100
	cfg.StealthMinTotalValue = 100
	cfg.StealthMinSpreadMinutes = 60 // Require 60 minutes spread
	cfg.StealthTimeWindow = 2 * time.Hour
	cfg.StealthMaxSingleTrade = 10000
	pt := NewPatternTracker(zap.NewNop(), nil, nil, cfg)

	baseTime := time.Now()

	// Three trades but only 10 minutes apart (less than 60 min required)
	pt.ProcessTrade(context.Background(), ProcessTradeInput{
		Wallet:      "0xtest",
		ConditionID: "condition123",
		MarketTitle: "Test Market",
		MarketSlug:  "test-market",
		Outcome:     "Yes",
		Side:        "BUY",
		Size:        50,
		Price:       0.50,
		Notional:    50,
		Timestamp:   baseTime,
	})

	pt.ProcessTrade(context.Background(), ProcessTradeInput{
		Wallet:      "0xtest",
		ConditionID: "condition123",
		MarketTitle: "Test Market",
		MarketSlug:  "test-market",
		Outcome:     "Yes",
		Side:        "BUY",
		Size:        50,
		Price:       0.50,
		Notional:    50,
		Timestamp:   baseTime.Add(5 * time.Minute),
	})

	alerts := pt.ProcessTrade(context.Background(), ProcessTradeInput{
		Wallet:      "0xtest",
		ConditionID: "condition123",
		MarketTitle: "Test Market",
		MarketSlug:  "test-market",
		Outcome:     "Yes",
		Side:        "BUY",
		Size:        50,
		Price:       0.50,
		Notional:    50,
		Timestamp:   baseTime.Add(10 * time.Minute), // Only 10 min spread
	})

	// Should not alert - spread is only 10 minutes, need 60
	for _, a := range alerts {
		if a.Reason == "stealth_accumulation" {
			t.Error("should not generate stealth alert when spread below threshold")
		}
	}
}

func TestPatternTracker_StealthAccumulation_DuplicateAlertPrevention(t *testing.T) {
	cfg := DefaultPatternTrackerConfig()
	cfg.StealthMinTrades = 3
	cfg.StealthMinTotalSize = 100
	cfg.StealthMinTotalValue = 100
	cfg.StealthMinSpreadMinutes = 1
	cfg.StealthTimeWindow = 2 * time.Hour
	cfg.StealthMaxSingleTrade = 10000
	pt := NewPatternTracker(zap.NewNop(), nil, nil, cfg)

	baseTime := time.Now().Add(-90 * time.Minute)

	// Build up to trigger alert
	pt.ProcessTrade(context.Background(), ProcessTradeInput{
		Wallet: "0xtest", ConditionID: "cond1", MarketTitle: "Test", MarketSlug: "test",
		Outcome: "Yes", Side: "BUY", Size: 50, Price: 0.50, Notional: 50, Timestamp: baseTime,
	})
	pt.ProcessTrade(context.Background(), ProcessTradeInput{
		Wallet: "0xtest", ConditionID: "cond1", MarketTitle: "Test", MarketSlug: "test",
		Outcome: "Yes", Side: "BUY", Size: 50, Price: 0.50, Notional: 50, Timestamp: baseTime.Add(30 * time.Minute),
	})
	alerts := pt.ProcessTrade(context.Background(), ProcessTradeInput{
		Wallet: "0xtest", ConditionID: "cond1", MarketTitle: "Test", MarketSlug: "test",
		Outcome: "Yes", Side: "BUY", Size: 50, Price: 0.50, Notional: 50, Timestamp: baseTime.Add(60 * time.Minute),
	})

	// First alert should trigger
	firstAlertFound := false
	for _, a := range alerts {
		if a.Reason == "stealth_accumulation" {
			firstAlertFound = true
		}
	}
	if !firstAlertFound {
		t.Error("first stealth accumulation should trigger alert")
	}

	// Fourth trade should NOT trigger another alert (already alerted)
	alerts = pt.ProcessTrade(context.Background(), ProcessTradeInput{
		Wallet: "0xtest", ConditionID: "cond1", MarketTitle: "Test", MarketSlug: "test",
		Outcome: "Yes", Side: "BUY", Size: 50, Price: 0.50, Notional: 50, Timestamp: baseTime.Add(70 * time.Minute),
	})

	for _, a := range alerts {
		if a.Reason == "stealth_accumulation" {
			t.Error("should not trigger duplicate stealth alert")
		}
	}
}

// Pre-Move Positioning Tests

func TestDefaultPatternTrackerConfig_PreMove(t *testing.T) {
	cfg := DefaultPatternTrackerConfig()

	if cfg.PreMoveCheckDelay != 4*time.Hour {
		t.Errorf("expected pre-move check delay 4h, got %v", cfg.PreMoveCheckDelay)
	}
	if cfg.PreMoveMinNotional != 5000 {
		t.Errorf("expected pre-move min notional 5000, got %f", cfg.PreMoveMinNotional)
	}
	if cfg.PreMoveMinMoveSize != 0.10 {
		t.Errorf("expected pre-move min move size 0.10, got %f", cfg.PreMoveMinMoveSize)
	}
	if cfg.PreMoveMinTrades != 10 {
		t.Errorf("expected pre-move min trades 10, got %d", cfg.PreMoveMinTrades)
	}
	if cfg.PreMoveMinAlpha != 0.70 {
		t.Errorf("expected pre-move min alpha 0.70, got %f", cfg.PreMoveMinAlpha)
	}
	if cfg.PreMoveCheckInterval != 30*time.Minute {
		t.Errorf("expected pre-move check interval 30m, got %v", cfg.PreMoveCheckInterval)
	}
	if cfg.PreMoveAlertCooldown != 24*time.Hour {
		t.Errorf("expected pre-move alert cooldown 24h, got %v", cfg.PreMoveAlertCooldown)
	}
}

func TestNewPatternTracker_PreMoveInitialized(t *testing.T) {
	cfg := DefaultPatternTrackerConfig()
	pt := NewPatternTracker(zap.NewNop(), nil, nil, cfg)

	if pt.pendingMoves == nil {
		t.Error("expected pendingMoves to be initialized")
	}
	if pt.preMoveStats == nil {
		t.Error("expected preMoveStats to be initialized")
	}
}

func TestPatternTracker_ProcessTrade_RecordsPreMove(t *testing.T) {
	cfg := DefaultPatternTrackerConfig()
	cfg.PreMoveMinNotional = 1000 // Lower for testing
	pt := NewPatternTracker(zap.NewNop(), nil, nil, cfg)

	// Trade above threshold should be recorded
	pt.ProcessTrade(context.Background(), ProcessTradeInput{
		Wallet:      "0xtest",
		ConditionID: "condition123",
		MarketTitle: "Test Market",
		MarketSlug:  "test-market",
		Outcome:     "Yes",
		Side:        "BUY",
		Size:        2000,
		Price:       0.50,
		Notional:    2000, // Above 1000 threshold
		Timestamp:   time.Now(),
	})

	if len(pt.pendingMoves) != 1 {
		t.Errorf("expected 1 pending move, got %d", len(pt.pendingMoves))
	}
}

func TestPatternTracker_ProcessTrade_DoesNotRecordBelowThreshold(t *testing.T) {
	cfg := DefaultPatternTrackerConfig()
	cfg.PreMoveMinNotional = 5000
	pt := NewPatternTracker(zap.NewNop(), nil, nil, cfg)

	// Trade below threshold should NOT be recorded
	pt.ProcessTrade(context.Background(), ProcessTradeInput{
		Wallet:      "0xtest",
		ConditionID: "condition123",
		MarketTitle: "Test Market",
		MarketSlug:  "test-market",
		Outcome:     "Yes",
		Side:        "BUY",
		Size:        500,
		Price:       0.50,
		Notional:    250, // Below 5000 threshold
		Timestamp:   time.Now(),
	})

	if len(pt.pendingMoves) != 0 {
		t.Errorf("expected 0 pending moves, got %d", len(pt.pendingMoves))
	}
}

func TestPatternTracker_UpdatePreMoveStats(t *testing.T) {
	cfg := DefaultPatternTrackerConfig()
	pt := NewPatternTracker(zap.NewNop(), nil, nil, cfg)

	// First successful move
	pt.updatePreMoveStats("wallet1", true, 0.15)

	stats := pt.preMoveStats["wallet1"]
	if stats == nil {
		t.Fatal("expected stats to be created")
	}
	if stats.TotalTrades != 1 {
		t.Errorf("expected 1 total trade, got %d", stats.TotalTrades)
	}
	if stats.SuccessfulMoves != 1 {
		t.Errorf("expected 1 successful move, got %d", stats.SuccessfulMoves)
	}
	if stats.AlphaScore != 1.0 {
		t.Errorf("expected alpha score 1.0, got %f", stats.AlphaScore)
	}
	if stats.AvgMoveSize != 0.15 {
		t.Errorf("expected avg move size 0.15, got %f", stats.AvgMoveSize)
	}

	// Second unsuccessful move
	pt.updatePreMoveStats("wallet1", false, 0.05)

	stats = pt.preMoveStats["wallet1"]
	if stats.TotalTrades != 2 {
		t.Errorf("expected 2 total trades, got %d", stats.TotalTrades)
	}
	if stats.SuccessfulMoves != 1 {
		t.Errorf("expected 1 successful move (unchanged), got %d", stats.SuccessfulMoves)
	}
	if stats.AlphaScore != 0.5 {
		t.Errorf("expected alpha score 0.5, got %f", stats.AlphaScore)
	}

	// Third successful move
	pt.updatePreMoveStats("wallet1", true, 0.25)

	stats = pt.preMoveStats["wallet1"]
	if stats.TotalTrades != 3 {
		t.Errorf("expected 3 total trades, got %d", stats.TotalTrades)
	}
	if stats.SuccessfulMoves != 2 {
		t.Errorf("expected 2 successful moves, got %d", stats.SuccessfulMoves)
	}
	expectedAlpha := 2.0 / 3.0
	if stats.AlphaScore < expectedAlpha-0.01 || stats.AlphaScore > expectedAlpha+0.01 {
		t.Errorf("expected alpha score around %f, got %f", expectedAlpha, stats.AlphaScore)
	}
	expectedAvg := (0.15 + 0.25) / 2.0
	if stats.AvgMoveSize < expectedAvg-0.01 || stats.AvgMoveSize > expectedAvg+0.01 {
		t.Errorf("expected avg move size around %f, got %f", expectedAvg, stats.AvgMoveSize)
	}
}

func TestPatternTracker_ShouldAlertPreMove(t *testing.T) {
	cfg := DefaultPatternTrackerConfig()
	cfg.PreMoveMinTrades = 5
	cfg.PreMoveMinAlpha = 0.70
	pt := NewPatternTracker(zap.NewNop(), nil, nil, cfg)

	// No stats - no alert
	if pt.ShouldAlertPreMove("wallet1") {
		t.Error("should not alert with no stats")
	}

	// Add stats below trade threshold
	pt.preMoveStats["wallet1"] = &PreMoveStats{
		Wallet:          "wallet1",
		TotalTrades:     3, // Below 5 min
		SuccessfulMoves: 3,
		AlphaScore:      1.0,
	}
	if pt.ShouldAlertPreMove("wallet1") {
		t.Error("should not alert with trades below minimum")
	}

	// Add stats at trade threshold but low alpha
	pt.preMoveStats["wallet2"] = &PreMoveStats{
		Wallet:          "wallet2",
		TotalTrades:     10,
		SuccessfulMoves: 5,
		AlphaScore:      0.50, // Below 0.70
	}
	if pt.ShouldAlertPreMove("wallet2") {
		t.Error("should not alert with alpha below minimum")
	}

	// Add stats meeting all criteria
	pt.preMoveStats["wallet3"] = &PreMoveStats{
		Wallet:          "wallet3",
		TotalTrades:     10,
		SuccessfulMoves: 8,
		AlphaScore:      0.80, // Above 0.70
	}
	if !pt.ShouldAlertPreMove("wallet3") {
		t.Error("should alert when meeting all criteria")
	}
}

func TestPatternTracker_GetPreMoveStats(t *testing.T) {
	cfg := DefaultPatternTrackerConfig()
	pt := NewPatternTracker(zap.NewNop(), nil, nil, cfg)

	// No stats
	stats := pt.GetPreMoveStats("wallet1")
	if stats != nil {
		t.Error("expected nil stats for unknown wallet")
	}

	// Add stats
	pt.preMoveStats["wallet1"] = &PreMoveStats{
		Wallet:          "wallet1",
		TotalTrades:     15,
		SuccessfulMoves: 12,
		AlphaScore:      0.80,
		AvgMoveSize:     0.18,
	}

	stats = pt.GetPreMoveStats("wallet1")
	if stats == nil {
		t.Fatal("expected stats for known wallet")
	}
	if stats.TotalTrades != 15 {
		t.Errorf("expected 15 total trades, got %d", stats.TotalTrades)
	}
	if stats.SuccessfulMoves != 12 {
		t.Errorf("expected 12 successful moves, got %d", stats.SuccessfulMoves)
	}
	if stats.AlphaScore != 0.80 {
		t.Errorf("expected alpha score 0.80, got %f", stats.AlphaScore)
	}
	if stats.AvgMoveSize != 0.18 {
		t.Errorf("expected avg move size 0.18, got %f", stats.AvgMoveSize)
	}
}

func TestPatternTracker_CheckPendingMoves_BuyFavorable(t *testing.T) {
	mockAPI := &mockPatternAPIClient{
		positions: []polymarketapi.Position{
			{
				Outcome:  "Yes",
				Size:     2000,
				CurPrice: 0.60, // Price went up from 0.50
			},
		},
	}

	cfg := DefaultPatternTrackerConfig()
	cfg.PreMoveCheckDelay = 1 * time.Millisecond
	cfg.PreMoveMinMoveSize = 0.10
	cfg.PreMoveMinTrades = 1
	cfg.PreMoveMinAlpha = 0.70
	pt := NewPatternTracker(zap.NewNop(), mockAPI, nil, cfg)

	// Add a pending BUY move that's old enough
	oldTime := time.Now().Add(-1 * time.Hour)
	pt.pendingMoves["move1"] = &PreMoveRecord{
		ID:          "move1",
		Wallet:      "wallet1",
		ConditionID: "cond1",
		MarketTitle: "Test",
		MarketSlug:  "test",
		Outcome:     "Yes",
		Side:        "BUY",
		TradePrice:  0.50,
		TradeTime:   oldTime,
		Verified:    false,
	}

	pt.checkPendingMoves(context.Background())

	// Check move was verified
	move := pt.pendingMoves["move1"]
	if move == nil {
		t.Fatal("move1 should still exist")
	}
	if !move.Verified {
		t.Error("move1 should be verified")
	}
	if !move.Favorable {
		t.Error("BUY with price up should be favorable")
	}
	expectedMove := (0.60 - 0.50) / 0.50
	if move.MovePercent < expectedMove-0.01 || move.MovePercent > expectedMove+0.01 {
		t.Errorf("expected move percent around %f, got %f", expectedMove, move.MovePercent)
	}
}

func TestPatternTracker_CheckPendingMoves_SellFavorable(t *testing.T) {
	mockAPI := &mockPatternAPIClient{
		positions: []polymarketapi.Position{
			{
				Outcome:  "Yes",
				Size:     0, // Closed position
				CurPrice: 0.40, // Price went down from 0.60
			},
		},
	}

	cfg := DefaultPatternTrackerConfig()
	cfg.PreMoveCheckDelay = 1 * time.Millisecond
	cfg.PreMoveMinMoveSize = 0.10
	pt := NewPatternTracker(zap.NewNop(), mockAPI, nil, cfg)

	oldTime := time.Now().Add(-1 * time.Hour)
	pt.pendingMoves["move1"] = &PreMoveRecord{
		ID:          "move1",
		Wallet:      "wallet1",
		ConditionID: "cond1",
		MarketTitle: "Test",
		MarketSlug:  "test",
		Outcome:     "Yes",
		Side:        "SELL",
		TradePrice:  0.60,
		TradeTime:   oldTime,
		Verified:    false,
	}

	pt.checkPendingMoves(context.Background())

	move := pt.pendingMoves["move1"]
	if move == nil {
		t.Fatal("move1 should still exist")
	}
	if !move.Verified {
		t.Error("move1 should be verified")
	}
	if !move.Favorable {
		t.Error("SELL with price down should be favorable")
	}
}

func TestPatternTracker_CheckPendingMoves_NoPosition(t *testing.T) {
	mockAPI := &mockPatternAPIClient{
		positions: []polymarketapi.Position{}, // No positions
	}

	cfg := DefaultPatternTrackerConfig()
	cfg.PreMoveCheckDelay = 1 * time.Millisecond
	pt := NewPatternTracker(zap.NewNop(), mockAPI, nil, cfg)

	oldTime := time.Now().Add(-1 * time.Hour)
	pt.pendingMoves["move1"] = &PreMoveRecord{
		ID:          "move1",
		Wallet:      "wallet1",
		ConditionID: "cond1",
		Outcome:     "Yes",
		Side:        "BUY",
		TradePrice:  0.50,
		TradeTime:   oldTime,
		Verified:    false,
	}

	pt.checkPendingMoves(context.Background())

	move := pt.pendingMoves["move1"]
	if move == nil {
		t.Fatal("move1 should still exist")
	}
	if !move.Verified {
		t.Error("move1 should be verified even without position")
	}
	if move.Favorable {
		t.Error("move without position should not be marked favorable")
	}
}

func TestPatternTracker_CheckPendingMoves_TooRecent(t *testing.T) {
	mockAPI := &mockPatternAPIClient{
		positions: []polymarketapi.Position{
			{Outcome: "Yes", CurPrice: 0.70},
		},
	}

	cfg := DefaultPatternTrackerConfig()
	cfg.PreMoveCheckDelay = 4 * time.Hour // 4 hour delay
	pt := NewPatternTracker(zap.NewNop(), mockAPI, nil, cfg)

	// Add a recent move (should not be checked)
	pt.pendingMoves["move1"] = &PreMoveRecord{
		ID:         "move1",
		Wallet:     "wallet1",
		Outcome:    "Yes",
		Side:       "BUY",
		TradePrice: 0.50,
		TradeTime:  time.Now().Add(-1 * time.Hour), // Only 1 hour ago
		Verified:   false,
	}

	pt.checkPendingMoves(context.Background())

	move := pt.pendingMoves["move1"]
	if move.Verified {
		t.Error("recent move should not be verified")
	}
}

func TestPatternTracker_CheckPendingMoves_CleansOldVerified(t *testing.T) {
	cfg := DefaultPatternTrackerConfig()
	cfg.PreMoveCheckDelay = 1 * time.Millisecond
	pt := NewPatternTracker(zap.NewNop(), nil, nil, cfg)

	// Add an old verified move (should be cleaned up)
	pt.pendingMoves["old_move"] = &PreMoveRecord{
		ID:        "old_move",
		Wallet:    "wallet1",
		TradeTime: time.Now().Add(-10 * 24 * time.Hour),
		Verified:  true,
		CheckedAt: time.Now().Add(-8 * 24 * time.Hour), // Verified 8 days ago
	}

	pt.checkPendingMoves(context.Background())

	if _, exists := pt.pendingMoves["old_move"]; exists {
		t.Error("old verified move should be cleaned up")
	}
}

func TestPatternTracker_LoadWithMock(t *testing.T) {
	cfg := DefaultPatternTrackerConfig()
	cfg.GistID = "test-gist"
	cfg.FileName = "pattern_tracker.json"

	mockGist := NewMockGistStorage()
	pt := NewPatternTracker(zap.NewNop(), nil, mockGist, cfg)

	// Set up mock content
	snapshotJSON := `{
		"version": 1,
		"timestamp": "2024-01-01T00:00:00Z",
		"pending_exits": {
			"exit1": {"id": "exit1", "wallet": "0xwallet1", "exit_price": 0.75, "verified": false}
		},
		"exit_timing_stats": {
			"0xwallet1": {"wallet": "0xwallet1", "verified_exits": 5, "avg_timing_score": 0.92}
		},
		"accumulations": {
			"0xwallet1:cond1:Yes": {"wallet": "0xwallet1", "condition_id": "cond1", "outcome": "Yes"}
		},
		"pending_moves": {
			"move1": {"id": "move1", "wallet": "0xwallet1", "side": "BUY", "trade_price": 0.50}
		},
		"pre_move_stats": {
			"0xwallet1": {"wallet": "0xwallet1", "total_trades": 10, "successful_moves": 7, "alpha_score": 0.70}
		}
	}`
	mockGist.SetContent("pattern_tracker.json", snapshotJSON)

	err := pt.Load(context.Background())
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Verify loaded data
	pt.mu.RLock()
	defer pt.mu.RUnlock()

	if len(pt.pendingExits) != 1 {
		t.Errorf("expected 1 pending exit, got %d", len(pt.pendingExits))
	}
	if len(pt.exitTimingStats) != 1 {
		t.Errorf("expected 1 exit timing stat, got %d", len(pt.exitTimingStats))
	}
	if len(pt.accumulations) != 1 {
		t.Errorf("expected 1 accumulation, got %d", len(pt.accumulations))
	}
	if len(pt.pendingMoves) != 1 {
		t.Errorf("expected 1 pending move, got %d", len(pt.pendingMoves))
	}
	if len(pt.preMoveStats) != 1 {
		t.Errorf("expected 1 pre-move stat, got %d", len(pt.preMoveStats))
	}

	stats := pt.preMoveStats["0xwallet1"]
	if stats == nil || stats.TotalTrades != 10 || stats.AlphaScore != 0.70 {
		t.Error("pre-move stats not loaded correctly")
	}
}

func TestPatternTracker_SaveWithMock(t *testing.T) {
	cfg := DefaultPatternTrackerConfig()
	cfg.GistID = "test-gist"
	cfg.FileName = "pattern_tracker.json"

	mockGist := NewMockGistStorage()
	pt := NewPatternTracker(zap.NewNop(), nil, mockGist, cfg)

	// Add some data
	pt.mu.Lock()
	pt.pendingExits["exit1"] = &ExitTimingRecord{ID: "exit1", Wallet: "0xwallet1"}
	pt.exitTimingStats["0xwallet1"] = &ExitTimingStats{Wallet: "0xwallet1", VerifiedExits: 5}
	pt.accumulations["key1"] = &AccumulationRecord{Wallet: "0xwallet1"}
	pt.pendingMoves["move1"] = &PreMoveRecord{ID: "move1", Wallet: "0xwallet1"}
	pt.preMoveStats["0xwallet1"] = &PreMoveStats{Wallet: "0xwallet1", TotalTrades: 10}
	pt.dirty = true
	pt.mu.Unlock()

	err := pt.Save(context.Background())
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Verify saved content
	content := mockGist.GetContent("pattern_tracker.json")
	if content == "" {
		t.Fatal("expected content to be saved")
	}

	// Check dirty flag cleared
	if pt.dirty {
		t.Error("expected dirty flag to be cleared")
	}
}

func TestPatternTracker_Save_AlwaysSaves(t *testing.T) {
	cfg := DefaultPatternTrackerConfig()
	cfg.GistID = "test-gist"

	mockGist := NewMockGistStorage()
	pt := NewPatternTracker(zap.NewNop(), nil, mockGist, cfg)

	// Pattern tracker always saves when enabled (doesn't check dirty)
	err := pt.Save(context.Background())
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Content should be saved even if not dirty
	if mockGist.GetContent(cfg.FileName) == "" {
		t.Error("expected content to be saved")
	}
}

func TestPatternTracker_Save_NotEnabled(t *testing.T) {
	cfg := DefaultPatternTrackerConfig()
	cfg.GistID = "" // Not enabled

	pt := NewPatternTracker(zap.NewNop(), nil, nil, cfg)
	pt.dirty = true

	err := pt.Save(context.Background())
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPatternTracker_Load_NotEnabled(t *testing.T) {
	cfg := DefaultPatternTrackerConfig()
	cfg.GistID = "" // Not enabled

	pt := NewPatternTracker(zap.NewNop(), nil, nil, cfg)

	err := pt.Load(context.Background())
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPatternTracker_Load_EmptyContent(t *testing.T) {
	cfg := DefaultPatternTrackerConfig()
	cfg.GistID = "test-gist"

	mockGist := NewMockGistStorage()
	// Don't set any content - will return empty string
	pt := NewPatternTracker(zap.NewNop(), nil, mockGist, cfg)

	err := pt.Load(context.Background())
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPatternTracker_SaveError(t *testing.T) {
	cfg := DefaultPatternTrackerConfig()
	cfg.GistID = "test-gist"

	mockGist := NewMockGistStorage()
	mockGist.SetSaveError(errors.New("mock save error"))
	pt := NewPatternTracker(zap.NewNop(), nil, mockGist, cfg)

	pt.dirty = true

	err := pt.Save(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}

	// Dirty flag should remain set
	if !pt.dirty {
		t.Error("expected dirty flag to remain set")
	}
}

func TestPatternTracker_LoadError(t *testing.T) {
	cfg := DefaultPatternTrackerConfig()
	cfg.GistID = "test-gist"

	mockGist := NewMockGistStorage()
	mockGist.SetLoadError(errors.New("mock load error"))
	pt := NewPatternTracker(zap.NewNop(), nil, mockGist, cfg)

	err := pt.Load(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestPatternTracker_PeriodicSave_ContextCancel(t *testing.T) {
	cfg := DefaultPatternTrackerConfig()
	cfg.SaveInterval = 1 * time.Hour
	pt := NewPatternTracker(zap.NewNop(), nil, nil, cfg)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		pt.periodicSave(ctx)
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

func TestPatternTracker_PeriodicSave_DoneChannel(t *testing.T) {
	cfg := DefaultPatternTrackerConfig()
	cfg.SaveInterval = 1 * time.Hour
	pt := NewPatternTracker(zap.NewNop(), nil, nil, cfg)

	ctx := context.Background()

	done := make(chan struct{})
	go func() {
		pt.periodicSave(ctx)
		close(done)
	}()

	pt.Stop()

	select {
	case <-done:
		// Good
	case <-time.After(200 * time.Millisecond):
		t.Error("periodicSave should stop when doneCh is closed")
	}
}
