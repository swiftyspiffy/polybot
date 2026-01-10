package app

import (
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestDefaultCopyTrackerConfig(t *testing.T) {
	cfg := DefaultCopyTrackerConfig()

	if cfg.TimeWindow != 10*time.Minute {
		t.Errorf("expected 10 minute window, got %v", cfg.TimeWindow)
	}
	if cfg.MinCopyCount != 3 {
		t.Errorf("expected min count 3, got %d", cfg.MinCopyCount)
	}
	if cfg.LeaderMinWinRate != 0.70 {
		t.Errorf("expected 0.70 win rate, got %f", cfg.LeaderMinWinRate)
	}
	if cfg.LeaderMinResolved != 5 {
		t.Errorf("expected 5 resolved, got %d", cfg.LeaderMinResolved)
	}
}

func TestNewCopyTracker(t *testing.T) {
	cfg := DefaultCopyTrackerConfig()
	tracker := NewCopyTracker(nil, cfg, nil)

	if tracker.logger == nil {
		t.Error("expected logger to be set")
	}
	if tracker.recentLeaderTrades == nil {
		t.Error("expected recentLeaderTrades to be initialized")
	}
	if tracker.copyCount == nil {
		t.Error("expected copyCount to be initialized")
	}
}

func TestCopyTracker_IsLeader_HighWinRate(t *testing.T) {
	cfg := CopyTrackerConfig{
		LeaderMinWinRate:  0.70,
		LeaderMinResolved: 5,
	}
	tracker := NewCopyTracker(zap.NewNop(), cfg, nil)

	// Not enough resolved positions
	stats := &WalletStats{
		WinRate:  0.80,
		WinCount: 3,
		LossCount: 1,
	}
	if tracker.IsLeader(stats) {
		t.Error("expected not to be leader with only 4 resolved positions")
	}

	// Enough resolved, high win rate
	stats = &WalletStats{
		WinRate:   0.75,
		WinCount:  6,
		LossCount: 2,
	}
	if !tracker.IsLeader(stats) {
		t.Error("expected to be leader with 8 resolved and 75% win rate")
	}

	// Enough resolved, low win rate
	stats = &WalletStats{
		WinRate:   0.50,
		WinCount:  5,
		LossCount: 5,
	}
	if tracker.IsLeader(stats) {
		t.Error("expected not to be leader with 50% win rate")
	}

	// Nil stats
	if tracker.IsLeader(nil) {
		t.Error("expected nil stats to not be leader")
	}
}

func TestCopyTracker_RecordLeaderTrade(t *testing.T) {
	cfg := DefaultCopyTrackerConfig()
	tracker := NewCopyTracker(zap.NewNop(), cfg, nil)

	tracker.RecordLeaderTrade("0xleader1", "cond1", "token1", "BUY")
	tracker.RecordLeaderTrade("0xleader2", "cond2", "token2", "SELL")

	if len(tracker.recentLeaderTrades) != 2 {
		t.Errorf("expected 2 leader trades, got %d", len(tracker.recentLeaderTrades))
	}

	// Check first trade
	if tracker.recentLeaderTrades[0].LeaderAddress != "0xleader1" {
		t.Error("unexpected leader address")
	}
	if tracker.recentLeaderTrades[0].Side != "BUY" {
		t.Error("unexpected side")
	}
}

func TestCopyTracker_CheckForCopy(t *testing.T) {
	cfg := CopyTrackerConfig{
		TimeWindow:   10 * time.Minute,
		MinCopyCount: 2,
	}
	tracker := NewCopyTracker(zap.NewNop(), cfg, nil)

	// Record a leader trade
	tracker.RecordLeaderTrade("0xleader", "cond1", "token1", "BUY")

	// Same wallet as leader - should not be copy
	isCopy, leader := tracker.CheckForCopy("0xleader", "cond1", "token1", "BUY")
	if isCopy {
		t.Error("expected leader's own trade to not be a copy")
	}

	// Different wallet copying same trade
	isCopy, leader = tracker.CheckForCopy("0xfollower", "cond1", "token1", "BUY")
	if !isCopy {
		t.Error("expected trade to be detected as copy")
	}
	if leader != "0xleader" {
		t.Errorf("expected leader to be 0xleader, got %s", leader)
	}

	// Different market - should not be copy
	isCopy, _ = tracker.CheckForCopy("0xfollower2", "cond2", "token2", "BUY")
	if isCopy {
		t.Error("expected different market to not be a copy")
	}

	// Same market, different side - should not be copy
	isCopy, _ = tracker.CheckForCopy("0xfollower3", "cond1", "token1", "SELL")
	if isCopy {
		t.Error("expected different side to not be a copy")
	}
}

func TestCopyTracker_ShouldAlert(t *testing.T) {
	cfg := CopyTrackerConfig{
		TimeWindow:   10 * time.Minute,
		MinCopyCount: 3,
	}
	tracker := NewCopyTracker(zap.NewNop(), cfg, nil)

	// Record multiple leader trades
	tracker.RecordLeaderTrade("0xleader1", "cond1", "token1", "BUY")
	tracker.RecordLeaderTrade("0xleader2", "cond2", "token2", "BUY")
	tracker.RecordLeaderTrade("0xleader3", "cond3", "token3", "BUY")

	follower := "0xfollower"

	// After 2 copies - should not alert
	tracker.CheckForCopy(follower, "cond1", "token1", "BUY")
	tracker.CheckForCopy(follower, "cond2", "token2", "BUY")
	if tracker.ShouldAlert(follower) {
		t.Error("expected no alert after only 2 copies")
	}

	// After 3 copies - should alert
	tracker.CheckForCopy(follower, "cond3", "token3", "BUY")
	if !tracker.ShouldAlert(follower) {
		t.Error("expected alert after 3 copies")
	}
}

func TestCopyTracker_GetCopyCount(t *testing.T) {
	cfg := DefaultCopyTrackerConfig()
	tracker := NewCopyTracker(zap.NewNop(), cfg, nil)

	tracker.RecordLeaderTrade("0xleader", "cond1", "token1", "BUY")
	tracker.RecordLeaderTrade("0xleader", "cond2", "token2", "BUY")

	// Initially zero
	if tracker.GetCopyCount("0xfollower") != 0 {
		t.Error("expected 0 copies initially")
	}

	// After copies
	tracker.CheckForCopy("0xfollower", "cond1", "token1", "BUY")
	if tracker.GetCopyCount("0xfollower") != 1 {
		t.Error("expected 1 copy")
	}

	tracker.CheckForCopy("0xfollower", "cond2", "token2", "BUY")
	if tracker.GetCopyCount("0xfollower") != 2 {
		t.Error("expected 2 copies")
	}
}

func TestCopyTracker_PruneOldTrades(t *testing.T) {
	cfg := CopyTrackerConfig{
		TimeWindow: 1 * time.Millisecond, // Very short for testing
	}
	tracker := NewCopyTracker(zap.NewNop(), cfg, nil)

	// Add a trade
	tracker.RecordLeaderTrade("0xleader", "cond1", "token1", "BUY")

	if len(tracker.recentLeaderTrades) != 1 {
		t.Error("expected 1 trade before prune")
	}

	// Wait for trade to expire
	time.Sleep(5 * time.Millisecond)

	// Prune old trades
	pruned := tracker.PruneOldTrades()
	if pruned != 1 {
		t.Errorf("expected 1 trade pruned, got %d", pruned)
	}

	if len(tracker.recentLeaderTrades) != 0 {
		t.Error("expected 0 trades after prune")
	}
}

func TestCopyTracker_Stats(t *testing.T) {
	cfg := DefaultCopyTrackerConfig()
	tracker := NewCopyTracker(zap.NewNop(), cfg, nil)

	leaderTrades, followers := tracker.Stats()
	if leaderTrades != 0 || followers != 0 {
		t.Error("expected empty stats initially")
	}

	tracker.RecordLeaderTrade("0xleader", "cond1", "token1", "BUY")
	tracker.RecordLeaderTrade("0xleader", "cond2", "token2", "BUY")

	leaderTrades, followers = tracker.Stats()
	if leaderTrades != 2 {
		t.Errorf("expected 2 leader trades, got %d", leaderTrades)
	}

	// Add a copy
	tracker.CheckForCopy("0xfollower", "cond1", "token1", "BUY")
	leaderTrades, followers = tracker.Stats()
	if followers != 1 {
		t.Errorf("expected 1 follower, got %d", followers)
	}
}

func TestCopyTracker_ResetCopyCount(t *testing.T) {
	cfg := DefaultCopyTrackerConfig()
	tracker := NewCopyTracker(zap.NewNop(), cfg, nil)

	tracker.RecordLeaderTrade("0xleader", "cond1", "token1", "BUY")
	tracker.CheckForCopy("0xfollower", "cond1", "token1", "BUY")

	if tracker.GetCopyCount("0xfollower") != 1 {
		t.Error("expected 1 copy before reset")
	}

	tracker.ResetCopyCount("0xfollower")

	if tracker.GetCopyCount("0xfollower") != 0 {
		t.Error("expected 0 copies after reset")
	}
}

func TestCopyTracker_GetTopCopiers(t *testing.T) {
	cfg := DefaultCopyTrackerConfig()
	tracker := NewCopyTracker(zap.NewNop(), cfg, nil)

	// Add some leader trades
	for i := 0; i < 5; i++ {
		tracker.RecordLeaderTrade("0xleader", "cond"+string(rune('0'+i)), "token"+string(rune('0'+i)), "BUY")
	}

	// follower1 copies 3 times
	tracker.CheckForCopy("0xfollower1", "cond0", "token0", "BUY")
	tracker.CheckForCopy("0xfollower1", "cond1", "token1", "BUY")
	tracker.CheckForCopy("0xfollower1", "cond2", "token2", "BUY")

	// follower2 copies 1 time
	tracker.CheckForCopy("0xfollower2", "cond0", "token0", "BUY")

	// follower3 copies 2 times
	tracker.CheckForCopy("0xfollower3", "cond0", "token0", "BUY")
	tracker.CheckForCopy("0xfollower3", "cond1", "token1", "BUY")

	top := tracker.GetTopCopiers(2)
	if len(top) != 2 {
		t.Errorf("expected 2 top copiers, got %d", len(top))
	}

	// Should be sorted by count descending
	if top[0].CopyCount != 3 {
		t.Errorf("expected top copier to have 3 copies, got %d", top[0].CopyCount)
	}
	if top[1].CopyCount != 2 {
		t.Errorf("expected second copier to have 2 copies, got %d", top[1].CopyCount)
	}
}

func TestCopyTracker_CheckForCopy_ExpiredTrade(t *testing.T) {
	cfg := CopyTrackerConfig{
		TimeWindow: 1 * time.Millisecond,
	}
	tracker := NewCopyTracker(zap.NewNop(), cfg, nil)

	tracker.RecordLeaderTrade("0xleader", "cond1", "token1", "BUY")

	// Wait for trade to expire
	time.Sleep(5 * time.Millisecond)

	// Should not find expired trade
	isCopy, _ := tracker.CheckForCopy("0xfollower", "cond1", "token1", "BUY")
	if isCopy {
		t.Error("expected expired leader trade to not be matched")
	}
}
