package app

import (
	"context"
	"encoding/json"
	"polybot/clients/polymarketapi"
	"sync"
	"time"

	"go.uber.org/zap"
)

// WalletStats holds computed statistics for a wallet.
type WalletStats struct {
	Wallet             string
	UniqueMarkets      int
	TotalTrades        int
	WinCount           int     // Total wins (all entry prices)
	LossCount          int     // Total losses (all entry prices)
	WinRate            float64 // 0.0 to 1.0 (all entry prices)
	SuspiciousWins     int     // Wins where entry price was below threshold (non-obvious bets)
	SuspiciousLosses   int     // Losses where entry price was below threshold
	SuspiciousWinRate  float64 // Win rate counting only non-obvious entry prices
	FetchedAt          time.Time
}

// WalletTracker caches wallet statistics to avoid repeated API calls.
type WalletTracker struct {
	logger    *zap.Logger
	apiClient *polymarketapi.PolymarketApiClient

	cacheTTL             time.Duration
	contrarianThreshold  float64 // Price threshold for contrarian (< this or > 1-this)
	winRateMaxEntryPrice float64 // Max entry price to count as "suspicious" win (e.g., 0.85)
	contrarianCache      *ContrarianCache

	mu    sync.RWMutex
	cache map[string]*WalletStats
}

// NewWalletTracker creates a new wallet tracker with the given cache TTL.
func NewWalletTracker(
	logger *zap.Logger,
	apiClient *polymarketapi.PolymarketApiClient,
	cacheTTL time.Duration,
	contrarianThreshold float64,
	winRateMaxEntryPrice float64,
	contrarianCache *ContrarianCache,
) *WalletTracker {
	if logger == nil {
		logger = zap.NewNop()
	}
	if cacheTTL <= 0 {
		cacheTTL = 5 * time.Minute
	}
	if contrarianThreshold <= 0 {
		contrarianThreshold = 0.20
	}
	if winRateMaxEntryPrice <= 0 {
		winRateMaxEntryPrice = 0.70 // Default: ignore "obvious" bets at 70¢+
	}

	return &WalletTracker{
		logger:               logger,
		apiClient:            apiClient,
		cacheTTL:             cacheTTL,
		contrarianThreshold:  contrarianThreshold,
		winRateMaxEntryPrice: winRateMaxEntryPrice,
		contrarianCache:      contrarianCache,
		cache:                make(map[string]*WalletStats),
	}
}

// GetStats returns cached stats for a wallet, fetching from API if needed.
func (wt *WalletTracker) GetStats(ctx context.Context, wallet string) (*WalletStats, error) {
	// Check cache first
	wt.mu.RLock()
	cached, ok := wt.cache[wallet]
	wt.mu.RUnlock()

	if ok && time.Since(cached.FetchedAt) < wt.cacheTTL {
		return cached, nil
	}

	// Fetch fresh data
	stats, err := wt.fetchStats(ctx, wallet)
	if err != nil {
		// If we have stale cache, return it on error
		if cached != nil {
			wt.logger.Warn("using stale cache due to fetch error",
				zap.String("wallet", shortID(wallet)),
				zap.Error(err),
			)
			return cached, nil
		}
		return nil, err
	}

	// Update cache
	wt.mu.Lock()
	wt.cache[wallet] = stats
	wt.mu.Unlock()

	return stats, nil
}

// IsLowActivity returns true if the wallet has fewer than maxMarkets unique markets.
func (wt *WalletTracker) IsLowActivity(ctx context.Context, wallet string, maxMarkets int) (bool, *WalletStats, error) {
	stats, err := wt.GetStats(ctx, wallet)
	if err != nil {
		return false, nil, err
	}

	return stats.UniqueMarkets < maxMarkets, stats, nil
}

// fetchStats fetches and computes stats for a wallet from the API.
func (wt *WalletTracker) fetchStats(ctx context.Context, wallet string) (*WalletStats, error) {
	// Fetch activity to count unique markets
	activity, err := wt.apiClient.GetUserActivity(ctx, wallet, 500)
	if err != nil {
		return nil, err
	}

	// Count unique markets from activity
	marketsSeen := make(map[string]struct{})
	for _, a := range activity {
		if a.ConditionID != "" {
			marketsSeen[a.ConditionID] = struct{}{}
		}
	}

	// Fetch closed positions to calculate win rate (API limits to 50 per request)
	positions, err := wt.apiClient.GetClosedPositions(ctx, wallet, 50, 0)
	if err != nil {
		wt.logger.Warn("failed to fetch closed positions, win rate unavailable",
			zap.String("wallet", shortID(wallet)),
			zap.Error(err),
		)
		// Continue without win rate data
		positions = nil
	}

	// Fetch second batch if we got a full first batch
	if len(positions) == 50 {
		positions2, err := wt.apiClient.GetClosedPositions(ctx, wallet, 50, 50)
		if err != nil {
			wt.logger.Warn("failed to fetch second batch of closed positions",
				zap.String("wallet", shortID(wallet)),
				zap.Error(err),
			)
		} else {
			positions = append(positions, positions2...)
		}
	}

	winCount := 0
	lossCount := 0
	suspiciousWins := 0
	suspiciousLosses := 0

	for _, p := range positions {
		isWin := p.RealizedPnl > 0
		isLoss := p.RealizedPnl < 0

		// Check if this was a "non-obvious" bet (entry price below threshold)
		// A bet at 0.98 is obvious (near-certain win), but 0.40 requires conviction
		isSuspiciousEntry := p.AvgPrice <= wt.winRateMaxEntryPrice

		if isWin {
			winCount++
			if isSuspiciousEntry {
				suspiciousWins++
			}
		} else if isLoss {
			lossCount++
			if isSuspiciousEntry {
				suspiciousLosses++
			}
		}

		// Track contrarian results asynchronously
		if wt.contrarianCache != nil && (isWin || isLoss) {
			isContrarian := IsContrarianPrice(p.AvgPrice, wt.contrarianThreshold)
			if isContrarian {
				wt.contrarianCache.RecordContrarianResult(wallet, isWin)
			}
		}
	}

	winRate := 0.0
	total := winCount + lossCount
	if total > 0 {
		winRate = float64(winCount) / float64(total)
	}

	// Calculate suspicious win rate (only counting non-obvious bets)
	suspiciousWinRate := 0.0
	suspiciousTotal := suspiciousWins + suspiciousLosses
	if suspiciousTotal > 0 {
		suspiciousWinRate = float64(suspiciousWins) / float64(suspiciousTotal)
	}

	stats := &WalletStats{
		Wallet:            wallet,
		UniqueMarkets:     len(marketsSeen),
		TotalTrades:       len(activity),
		WinCount:          winCount,
		LossCount:         lossCount,
		WinRate:           winRate,
		SuspiciousWins:    suspiciousWins,
		SuspiciousLosses:  suspiciousLosses,
		SuspiciousWinRate: suspiciousWinRate,
		FetchedAt:         time.Now(),
	}

	wt.logger.Debug("fetched wallet stats",
		zap.String("wallet", shortID(wallet)),
		zap.Int("uniqueMarkets", stats.UniqueMarkets),
		zap.Int("totalTrades", stats.TotalTrades),
		zap.Float64("winRate", stats.WinRate),
		zap.Int("suspiciousWins", stats.SuspiciousWins),
		zap.Float64("suspiciousWinRate", stats.SuspiciousWinRate),
	)

	return stats, nil
}

// CacheSize returns the current number of cached wallets.
func (wt *WalletTracker) CacheSize() int {
	wt.mu.RLock()
	defer wt.mu.RUnlock()
	return len(wt.cache)
}

// PruneStale removes stale entries from the cache.
func (wt *WalletTracker) PruneStale() int {
	wt.mu.Lock()
	defer wt.mu.Unlock()

	pruned := 0
	staleThreshold := 2 * wt.cacheTTL

	for wallet, stats := range wt.cache {
		if time.Since(stats.FetchedAt) > staleThreshold {
			delete(wt.cache, wallet)
			pruned++
		}
	}

	return pruned
}

// TrimToMaxSize removes oldest entries until the cache JSON is under maxBytes.
// Returns the number of entries removed.
func (wt *WalletTracker) TrimToMaxSize(maxBytes int64) int {
	if maxBytes <= 0 {
		return 0
	}

	wt.mu.Lock()
	defer wt.mu.Unlock()

	// Check current size
	snapshot := &CacheSnapshot{
		Version:   1,
		Timestamp: time.Now(),
		Wallets:   make(map[string]WalletStats, len(wt.cache)),
	}
	for k, v := range wt.cache {
		snapshot.Wallets[k] = *v
	}

	data, err := json.Marshal(snapshot)
	if err != nil || int64(len(data)) <= maxBytes {
		return 0
	}

	// Build sorted list by FetchedAt (oldest first)
	type walletEntry struct {
		wallet    string
		fetchedAt time.Time
	}
	entries := make([]walletEntry, 0, len(wt.cache))
	for wallet, stats := range wt.cache {
		entries = append(entries, walletEntry{wallet: wallet, fetchedAt: stats.FetchedAt})
	}

	// Sort oldest first
	for i := 0; i < len(entries)-1; i++ {
		for j := i + 1; j < len(entries); j++ {
			if entries[j].fetchedAt.Before(entries[i].fetchedAt) {
				entries[i], entries[j] = entries[j], entries[i]
			}
		}
	}

	// Remove oldest entries until under limit
	removed := 0
	for _, entry := range entries {
		delete(wt.cache, entry.wallet)
		removed++

		// Check new size
		snapshot.Wallets = make(map[string]WalletStats, len(wt.cache))
		for k, v := range wt.cache {
			snapshot.Wallets[k] = *v
		}
		data, err = json.Marshal(snapshot)
		if err != nil || int64(len(data)) <= maxBytes {
			break
		}
	}

	if removed > 0 {
		wt.logger.Info("trimmed cache to max size",
			zap.Int("removed", removed),
			zap.Int("remaining", len(wt.cache)),
			zap.Int64("sizeBytes", int64(len(data))),
		)
	}

	return removed
}

// CacheSnapshot represents a serializable snapshot of the wallet cache.
type CacheSnapshot struct {
	Version   int                    `json:"version"`
	Timestamp time.Time              `json:"timestamp"`
	Wallets   map[string]WalletStats `json:"wallets"`
}

// ExportCache exports the current cache as a JSON-serializable snapshot.
func (wt *WalletTracker) ExportCache() *CacheSnapshot {
	wt.mu.RLock()
	defer wt.mu.RUnlock()

	// Make a copy of the cache
	wallets := make(map[string]WalletStats, len(wt.cache))
	for k, v := range wt.cache {
		wallets[k] = *v
	}

	return &CacheSnapshot{
		Version:   1,
		Timestamp: time.Now(),
		Wallets:   wallets,
	}
}

// ExportCacheJSON exports the cache as JSON bytes.
func (wt *WalletTracker) ExportCacheJSON() ([]byte, error) {
	snapshot := wt.ExportCache()
	return json.Marshal(snapshot)
}

// ImportCache imports a cache snapshot, merging with existing data.
// Newer entries (by FetchedAt) take precedence.
func (wt *WalletTracker) ImportCache(snapshot *CacheSnapshot) int {
	if snapshot == nil || len(snapshot.Wallets) == 0 {
		return 0
	}

	wt.mu.Lock()
	defer wt.mu.Unlock()

	imported := 0
	for wallet, stats := range snapshot.Wallets {
		existing, exists := wt.cache[wallet]
		// Import if doesn't exist or if imported data is newer
		if !exists || stats.FetchedAt.After(existing.FetchedAt) {
			statsCopy := stats
			wt.cache[wallet] = &statsCopy
			imported++
		}
	}

	wt.logger.Info("imported wallet cache",
		zap.Int("imported", imported),
		zap.Int("totalCached", len(wt.cache)),
		zap.Time("snapshotTime", snapshot.Timestamp),
	)

	return imported
}

// ImportCacheJSON imports a cache from JSON bytes.
func (wt *WalletTracker) ImportCacheJSON(data []byte) (int, error) {
	var snapshot CacheSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return 0, err
	}

	return wt.ImportCache(&snapshot), nil
}
