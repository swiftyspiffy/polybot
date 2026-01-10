package app

import (
	"context"
	"encoding/json"
	"polybot/clients/gist"
	"sort"
	"time"

	"go.uber.org/zap"
)

// CachePersister handles persisting the wallet cache and seen trades to GitHub Gist.
type CachePersister struct {
	logger             *zap.Logger
	gistClient         *gist.Client
	walletTracker      *WalletTracker
	tradeMonitor       *TradeMonitor
	uploadInterval     time.Duration
	cacheFileName      string
	seenTradesFileName string
	maxSizeBytes       int64
}

// NewCachePersister creates a new cache persister.
func NewCachePersister(
	logger *zap.Logger,
	gistClient *gist.Client,
	walletTracker *WalletTracker,
	tradeMonitor *TradeMonitor,
	uploadInterval time.Duration,
	cacheFileName string,
	seenTradesFileName string,
	maxSizeBytes int64,
) *CachePersister {
	if logger == nil {
		logger = zap.NewNop()
	}
	if cacheFileName == "" {
		cacheFileName = "wallet_cache.json"
	}
	if seenTradesFileName == "" {
		seenTradesFileName = "seen_trades.json"
	}

	return &CachePersister{
		logger:             logger,
		gistClient:         gistClient,
		walletTracker:      walletTracker,
		tradeMonitor:       tradeMonitor,
		uploadInterval:     uploadInterval,
		cacheFileName:      cacheFileName,
		seenTradesFileName: seenTradesFileName,
		maxSizeBytes:       maxSizeBytes,
	}
}

// LoadSeenTrades attempts to load seen trades from GitHub Gist.
// Returns the number of entries loaded, or 0 if no cache found.
func (cp *CachePersister) LoadSeenTrades(ctx context.Context) (int, error) {
	if !cp.gistClient.IsEnabled() {
		cp.logger.Info("gist client not configured, skipping seen trades load")
		return 0, nil
	}

	gistID := cp.gistClient.GetGistID()
	if gistID == "" {
		cp.logger.Info("no gist ID configured, skipping seen trades load")
		return 0, nil
	}

	if cp.tradeMonitor == nil {
		cp.logger.Debug("no trade monitor configured, skipping seen trades load")
		return 0, nil
	}

	// Load seen trades from gist
	content, err := cp.gistClient.Load(ctx, cp.seenTradesFileName)
	if err != nil {
		cp.logger.Warn("failed to load seen trades from gist",
			zap.String("gistID", gistID),
			zap.String("fileName", cp.seenTradesFileName),
			zap.Error(err),
		)
		return 0, err
	}

	// Handle empty content gracefully
	if content == "" {
		cp.logger.Debug("seen trades file is empty, starting fresh",
			zap.String("gistID", gistID),
			zap.String("fileName", cp.seenTradesFileName),
		)
		return 0, nil
	}

	// Parse the snapshot
	var snapshot SeenTradesSnapshot
	if err := json.Unmarshal([]byte(content), &snapshot); err != nil {
		cp.logger.Warn("failed to parse seen trades JSON",
			zap.String("gistID", gistID),
			zap.String("fileName", cp.seenTradesFileName),
			zap.Int("contentLen", len(content)),
			zap.Error(err),
		)
		return 0, err
	}

	imported := cp.tradeMonitor.ImportSeenTrades(&snapshot)

	cp.logger.Info("loaded seen trades from gist",
		zap.Int("imported", imported),
	)

	return imported, nil
}

// MaxSeenTrades is the maximum number of seen trades to persist to gist.
// This prevents the gist file from growing too large and causing truncation.
const MaxSeenTrades = 5000

// MaxWalletCacheEntries is the maximum number of wallet entries to persist to gist.
// This prevents the gist file from growing too large and causing truncation.
const MaxWalletCacheEntries = 2000

// SaveSeenTrades saves the seen trades to GitHub Gist.
func (cp *CachePersister) SaveSeenTrades(ctx context.Context) error {
	if !cp.gistClient.IsEnabled() {
		return nil
	}

	if cp.tradeMonitor == nil {
		return nil
	}

	count := cp.tradeMonitor.SeenTradesCount()
	if count == 0 {
		cp.logger.Debug("no seen trades to save")
		return nil
	}

	// Export the seen trades
	snapshot := cp.tradeMonitor.ExportSeenTrades()

	// Limit the number of trades to prevent gist from growing too large
	// Keep the most recent trades (they're at the end of the slice since maps iterate randomly,
	// but for deduplication purposes it doesn't matter which ones we keep)
	if len(snapshot.Trades) > MaxSeenTrades {
		trimmed := len(snapshot.Trades) - MaxSeenTrades
		snapshot.Trades = snapshot.Trades[trimmed:]
		cp.logger.Info("trimmed seen trades for gist save",
			zap.Int("original", count),
			zap.Int("saved", MaxSeenTrades),
			zap.Int("trimmed", trimmed),
		)
	}

	if err := cp.gistClient.SaveJSON(ctx, cp.seenTradesFileName, snapshot); err != nil {
		return err
	}

	cp.logger.Info("saved seen trades to gist",
		zap.String("gistID", cp.gistClient.GetGistID()),
		zap.Int("trades", len(snapshot.Trades)),
	)

	return nil
}

// LoadCache attempts to load the cache from GitHub Gist.
// Returns the number of entries loaded, or 0 if no cache found.
func (cp *CachePersister) LoadCache(ctx context.Context) (int, error) {
	if !cp.gistClient.IsEnabled() {
		cp.logger.Info("gist client not configured, skipping cache load")
		return 0, nil
	}

	gistID := cp.gistClient.GetGistID()
	if gistID == "" {
		cp.logger.Info("no gist ID configured, skipping cache load")
		return 0, nil
	}

	// Load cache from gist
	content, err := cp.gistClient.Load(ctx, cp.cacheFileName)
	if err != nil {
		cp.logger.Warn("failed to load wallet cache from gist",
			zap.String("gistID", gistID),
			zap.String("fileName", cp.cacheFileName),
			zap.Error(err),
		)
		return 0, err
	}

	// Handle empty content gracefully
	if content == "" {
		cp.logger.Debug("wallet cache file is empty, starting fresh",
			zap.String("gistID", gistID),
			zap.String("fileName", cp.cacheFileName),
		)
		return 0, nil
	}

	imported, err := cp.walletTracker.ImportCacheJSON([]byte(content))
	if err != nil {
		cp.logger.Warn("failed to parse wallet cache JSON",
			zap.String("gistID", gistID),
			zap.String("fileName", cp.cacheFileName),
			zap.Int("contentLen", len(content)),
			zap.Error(err),
		)
		return 0, err
	}

	cp.logger.Info("loaded cache from gist",
		zap.Int("imported", imported),
	)

	return imported, nil
}

// SaveCache saves the current cache to GitHub Gist.
func (cp *CachePersister) SaveCache(ctx context.Context) error {
	if !cp.gistClient.IsEnabled() {
		return nil
	}

	cacheSize := cp.walletTracker.CacheSize()
	if cacheSize == 0 {
		cp.logger.Debug("cache is empty, skipping save")
		return nil
	}

	// Trim oldest entries if cache exceeds max size
	if cp.maxSizeBytes > 0 {
		cp.walletTracker.TrimToMaxSize(cp.maxSizeBytes)
	}

	// Export and save the cache
	snapshot := cp.walletTracker.ExportCache()

	// Limit the number of wallet entries to prevent gist from growing too large
	if len(snapshot.Wallets) > MaxWalletCacheEntries {
		trimmed := len(snapshot.Wallets) - MaxWalletCacheEntries
		snapshot.Wallets = trimWalletCache(snapshot.Wallets, MaxWalletCacheEntries)
		cp.logger.Info("trimmed wallet cache for gist save",
			zap.Int("original", cacheSize),
			zap.Int("saved", MaxWalletCacheEntries),
			zap.Int("trimmed", trimmed),
		)
	}

	if err := cp.gistClient.SaveJSON(ctx, cp.cacheFileName, snapshot); err != nil {
		return err
	}

	cp.logger.Info("saved cache to gist",
		zap.String("gistID", cp.gistClient.GetGistID()),
		zap.Int("wallets", len(snapshot.Wallets)),
	)

	return nil
}

// Run starts the periodic cache save loop.
func (cp *CachePersister) Run(ctx context.Context) {
	if !cp.gistClient.IsEnabled() {
		cp.logger.Info("gist client not configured, cache persistence disabled")
		return
	}

	ticker := time.NewTicker(cp.uploadInterval)
	defer ticker.Stop()

	cp.logger.Info("cache persister started",
		zap.Duration("saveInterval", cp.uploadInterval),
	)

	for {
		select {
		case <-ctx.Done():
			// Final save on shutdown
			saveCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			if err := cp.SaveCache(saveCtx); err != nil {
				cp.logger.Error("failed to save cache on shutdown", zap.Error(err))
			}
			if err := cp.SaveSeenTrades(saveCtx); err != nil {
				cp.logger.Error("failed to save seen trades on shutdown", zap.Error(err))
			}
			cancel()
			cp.logger.Info("cache persister stopped")
			return

		case <-ticker.C:
			if err := cp.SaveCache(ctx); err != nil {
				cp.logger.Warn("failed to save cache", zap.Error(err))
			}
			if err := cp.SaveSeenTrades(ctx); err != nil {
				cp.logger.Warn("failed to save seen trades", zap.Error(err))
			}
		}
	}
}

// trimWalletCache returns a new map with only the maxEntries most recently fetched wallets.
func trimWalletCache(wallets map[string]WalletStats, maxEntries int) map[string]WalletStats {
	if len(wallets) <= maxEntries {
		return wallets
	}

	// Convert to slice for sorting
	type walletEntry struct {
		addr  string
		stats WalletStats
	}
	entries := make([]walletEntry, 0, len(wallets))
	for addr, stats := range wallets {
		entries = append(entries, walletEntry{addr: addr, stats: stats})
	}

	// Sort by FetchedAt descending (most recent first)
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].stats.FetchedAt.After(entries[j].stats.FetchedAt)
	})

	// Keep only the most recent entries
	result := make(map[string]WalletStats, maxEntries)
	for i := 0; i < maxEntries && i < len(entries); i++ {
		result[entries[i].addr] = entries[i].stats
	}

	return result
}
