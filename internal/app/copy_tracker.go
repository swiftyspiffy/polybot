package app

import (
	"sync"
	"time"

	"go.uber.org/zap"
)

// CopyTrackerConfig holds configuration for copy trading detection.
type CopyTrackerConfig struct {
	TimeWindow        time.Duration // How long after a leader trade to consider copies (e.g., 10 min)
	MinCopyCount      int           // Minimum copy trades to trigger alert (e.g., 3)
	LeaderMinWinRate  float64       // Minimum win rate to be considered a leader (e.g., 0.70)
	LeaderMinResolved int           // Minimum resolved positions for win rate leaders
}

// DefaultCopyTrackerConfig returns sensible defaults.
func DefaultCopyTrackerConfig() CopyTrackerConfig {
	return CopyTrackerConfig{
		TimeWindow:        10 * time.Minute,
		MinCopyCount:      3,
		LeaderMinWinRate:  0.70,
		LeaderMinResolved: 5,
	}
}

// LeaderTrade represents a recent trade by a leader wallet.
type LeaderTrade struct {
	LeaderAddress string
	ConditionID   string // market
	TokenID       string // which outcome
	Side          string // BUY or SELL
	Timestamp     time.Time
}

// CopyTracker detects potential copy trading behavior.
// It tracks recent trades from "leader" wallets (high win rate or contrarian winners)
// and flags wallets that consistently trade shortly after leaders.
type CopyTracker struct {
	logger          *zap.Logger
	config          CopyTrackerConfig
	contrarianCache *ContrarianCache

	mu                 sync.RWMutex
	recentLeaderTrades []LeaderTrade  // trades from leaders in the time window
	copyCount          map[string]int // wallet address -> number of detected copy trades
}

// NewCopyTracker creates a new copy trading detector.
func NewCopyTracker(
	logger *zap.Logger,
	config CopyTrackerConfig,
	contrarianCache *ContrarianCache,
) *CopyTracker {
	if logger == nil {
		logger = zap.NewNop()
	}

	return &CopyTracker{
		logger:             logger.Named("copy-tracker"),
		config:             config,
		contrarianCache:    contrarianCache,
		recentLeaderTrades: make([]LeaderTrade, 0),
		copyCount:          make(map[string]int),
	}
}

// IsLeader checks if a wallet qualifies as a "leader" based on win rate or contrarian history.
func (ct *CopyTracker) IsLeader(walletStats *WalletStats) bool {
	if walletStats == nil {
		return false
	}

	// Check high win rate
	resolvedCount := walletStats.WinCount + walletStats.LossCount
	if resolvedCount >= ct.config.LeaderMinResolved &&
		walletStats.WinRate >= ct.config.LeaderMinWinRate {
		return true
	}

	// Check contrarian winner status
	if ct.contrarianCache != nil && ct.contrarianCache.ShouldAlert(walletStats.Wallet) {
		return true
	}

	return false
}

// RecordLeaderTrade records a trade from a leader wallet.
func (ct *CopyTracker) RecordLeaderTrade(leaderAddress, conditionID, tokenID, side string) {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	ct.recentLeaderTrades = append(ct.recentLeaderTrades, LeaderTrade{
		LeaderAddress: leaderAddress,
		ConditionID:   conditionID,
		TokenID:       tokenID,
		Side:          side,
		Timestamp:     time.Now(),
	})

	ct.logger.Debug("recorded leader trade",
		zap.String("leader", shortID(leaderAddress)),
		zap.String("market", shortID(conditionID)),
		zap.String("side", side),
	)
}

// CheckForCopy checks if a trade appears to be copying a leader.
// Returns true if this is a copy trade, along with the leader's address.
// Also increments the copy count for the follower.
func (ct *CopyTracker) CheckForCopy(followerAddress, conditionID, tokenID, side string) (isCopy bool, leaderAddress string) {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	cutoff := time.Now().Add(-ct.config.TimeWindow)

	// Look for matching leader trade
	for _, lt := range ct.recentLeaderTrades {
		// Skip old trades
		if lt.Timestamp.Before(cutoff) {
			continue
		}

		// Check if this matches the follower's trade
		if lt.ConditionID == conditionID &&
			lt.TokenID == tokenID &&
			lt.Side == side &&
			lt.LeaderAddress != followerAddress {
			// Found a match - this is a potential copy trade
			ct.copyCount[followerAddress]++

			ct.logger.Debug("detected potential copy trade",
				zap.String("follower", shortID(followerAddress)),
				zap.String("leader", shortID(lt.LeaderAddress)),
				zap.String("market", shortID(conditionID)),
				zap.Int("copyCount", ct.copyCount[followerAddress]),
			)

			return true, lt.LeaderAddress
		}
	}

	return false, ""
}

// ShouldAlert returns true if the follower has copied leaders enough times to warrant an alert.
func (ct *CopyTracker) ShouldAlert(followerAddress string) bool {
	ct.mu.RLock()
	defer ct.mu.RUnlock()

	return ct.copyCount[followerAddress] >= ct.config.MinCopyCount
}

// GetCopyCount returns the number of detected copy trades for a wallet.
func (ct *CopyTracker) GetCopyCount(walletAddress string) int {
	ct.mu.RLock()
	defer ct.mu.RUnlock()

	return ct.copyCount[walletAddress]
}

// PruneOldTrades removes leader trades older than the time window.
// Should be called periodically to prevent memory growth.
func (ct *CopyTracker) PruneOldTrades() int {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	cutoff := time.Now().Add(-ct.config.TimeWindow)
	pruned := 0

	// Filter to keep only recent trades
	recent := make([]LeaderTrade, 0, len(ct.recentLeaderTrades))
	for _, lt := range ct.recentLeaderTrades {
		if lt.Timestamp.After(cutoff) {
			recent = append(recent, lt)
		} else {
			pruned++
		}
	}

	ct.recentLeaderTrades = recent
	return pruned
}

// Stats returns current tracking statistics.
func (ct *CopyTracker) Stats() (leaderTrades int, trackedFollowers int) {
	ct.mu.RLock()
	defer ct.mu.RUnlock()

	return len(ct.recentLeaderTrades), len(ct.copyCount)
}

// ResetCopyCount resets the copy count for a specific wallet.
// Could be used if we want to "forgive" after a period of time.
func (ct *CopyTracker) ResetCopyCount(walletAddress string) {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	delete(ct.copyCount, walletAddress)
}

// GetTopCopiers returns wallets with the highest copy counts.
func (ct *CopyTracker) GetTopCopiers(limit int) []struct {
	Address   string
	CopyCount int
} {
	ct.mu.RLock()
	defer ct.mu.RUnlock()

	type copier struct {
		Address   string
		CopyCount int
	}

	copiers := make([]copier, 0, len(ct.copyCount))
	for addr, count := range ct.copyCount {
		copiers = append(copiers, copier{Address: addr, CopyCount: count})
	}

	// Sort by count descending (simple bubble sort for small lists)
	for i := 0; i < len(copiers)-1; i++ {
		for j := i + 1; j < len(copiers); j++ {
			if copiers[j].CopyCount > copiers[i].CopyCount {
				copiers[i], copiers[j] = copiers[j], copiers[i]
			}
		}
	}

	if limit > len(copiers) {
		limit = len(copiers)
	}

	result := make([]struct {
		Address   string
		CopyCount int
	}, limit)
	for i := 0; i < limit; i++ {
		result[i].Address = copiers[i].Address
		result[i].CopyCount = copiers[i].CopyCount
	}

	return result
}
