package app

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"polybot/clients/gist"
	"polybot/config"

	"go.uber.org/zap"
)

// ContrarianStats tracks a wallet's contrarian betting performance.
type ContrarianStats struct {
	Wins   uint16 // Contrarian wins (max 65535)
	Losses uint16 // Contrarian losses (max 65535)
}

// TotalWins returns the total number of wins (contrarian wins).
func (s ContrarianStats) TotalWins() int {
	return int(s.Wins)
}

// ContrarianRate returns the percentage of total resolved positions that are contrarian wins.
func (s ContrarianStats) ContrarianRate() float64 {
	total := int(s.Wins) + int(s.Losses)
	if total == 0 {
		return 0
	}
	return float64(s.Wins) / float64(total)
}

// ContrarianCache tracks wallets with contrarian betting history.
// Uses a compact text format: "address:wins:losses" per line.
type ContrarianCache struct {
	logger       *zap.Logger
	gistClient   gist.Storage
	config       config.ContrarianCacheConfig
	githubToken  string

	mu      sync.RWMutex
	wallets map[string]ContrarianStats // address -> stats
	dirty   bool                       // true if cache has unsaved changes

	// Pending updates channel for async processing
	updateCh chan walletUpdate
	doneCh   chan struct{}
}

type walletUpdate struct {
	address string
	win     bool // true if contrarian win, false if contrarian loss
}

// NewContrarianCache creates a new contrarian cache tracker.
func NewContrarianCache(logger *zap.Logger, cfg *config.Config) *ContrarianCache {
	if logger == nil {
		logger = zap.NewNop()
	}

	// Create a separate gist client for contrarian cache
	var gistClient gist.Storage
	if cfg.Gist.Token != "" && cfg.ContrarianCache.GistID != "" {
		// Create a modified config with the contrarian gist ID
		gistCfg := &config.Config{
			Gist: config.GistConfig{
				Token:  cfg.Gist.Token,
				GistID: cfg.ContrarianCache.GistID,
			},
		}
		gistClient = gist.NewClient(logger, gistCfg)
	}

	cc := &ContrarianCache{
		logger:      logger.Named("contrarian-cache"),
		gistClient:  gistClient,
		config:      cfg.ContrarianCache,
		githubToken: cfg.Gist.Token,
		wallets:     make(map[string]ContrarianStats),
		updateCh:    make(chan walletUpdate, 1000), // Buffer for async updates
		doneCh:      make(chan struct{}),
	}

	return cc
}

// SetGistClient sets the gist client (useful for testing with mocks).
func (cc *ContrarianCache) SetGistClient(client gist.Storage) {
	cc.gistClient = client
}

// IsEnabled returns true if the contrarian cache is configured.
func (cc *ContrarianCache) IsEnabled() bool {
	return cc.gistClient != nil && cc.config.GistID != ""
}

// Start begins the async update processor.
func (cc *ContrarianCache) Start(ctx context.Context) {
	go cc.processUpdates(ctx)
	go cc.periodicSave(ctx)
}

// Stop gracefully shuts down the cache, saving any pending changes.
func (cc *ContrarianCache) Stop() {
	close(cc.doneCh)
}

// processUpdates handles async wallet stat updates.
func (cc *ContrarianCache) processUpdates(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-cc.doneCh:
			return
		case update := <-cc.updateCh:
			cc.applyUpdate(update)
		}
	}
}

// periodicSave saves the cache to gist periodically.
func (cc *ContrarianCache) periodicSave(ctx context.Context) {
	if !cc.IsEnabled() {
		return
	}

	ticker := time.NewTicker(cc.config.SaveInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Final save on shutdown
			cc.Save(context.Background())
			return
		case <-cc.doneCh:
			cc.Save(context.Background())
			return
		case <-ticker.C:
			if err := cc.Save(ctx); err != nil {
				cc.logger.Warn("failed to save contrarian cache", zap.Error(err))
			}
		}
	}
}

// applyUpdate applies a single update to the cache.
func (cc *ContrarianCache) applyUpdate(update walletUpdate) {
	cc.mu.Lock()
	defer cc.mu.Unlock()

	stats := cc.wallets[update.address]
	if update.win {
		if stats.Wins < 65535 {
			stats.Wins++
		}
	} else {
		if stats.Losses < 65535 {
			stats.Losses++
		}
	}
	cc.wallets[update.address] = stats
	cc.dirty = true
}

// RecordContrarianResult queues an async update for a wallet's contrarian result.
func (cc *ContrarianCache) RecordContrarianResult(address string, win bool) {
	if !cc.IsEnabled() {
		return
	}

	// Non-blocking send
	select {
	case cc.updateCh <- walletUpdate{address: strings.ToLower(address), win: win}:
	default:
		cc.logger.Debug("contrarian update channel full, dropping update")
	}
}

// GetStats returns the contrarian stats for a wallet.
func (cc *ContrarianCache) GetStats(address string) (ContrarianStats, bool) {
	cc.mu.RLock()
	defer cc.mu.RUnlock()
	stats, ok := cc.wallets[strings.ToLower(address)]
	return stats, ok
}

// ShouldAlert returns true if this wallet should trigger a contrarian winner alert.
func (cc *ContrarianCache) ShouldAlert(address string) bool {
	if !cc.IsEnabled() {
		return false
	}

	stats, ok := cc.GetStats(address)
	if !ok {
		return false
	}

	// Check minimum wins threshold
	if stats.TotalWins() < cc.config.MinWins {
		return false
	}

	// Check contrarian rate threshold
	if stats.ContrarianRate() < cc.config.MinContrarianRate {
		return false
	}

	return true
}

// Load loads the cache from gist.
func (cc *ContrarianCache) Load(ctx context.Context) error {
	if !cc.IsEnabled() {
		cc.logger.Info("contrarian cache not configured, skipping load")
		return nil
	}

	content, err := cc.gistClient.Load(ctx, cc.config.FileName, cc.config.GistID)
	if err != nil {
		return fmt.Errorf("load contrarian cache: %w", err)
	}

	cc.mu.Lock()
	defer cc.mu.Unlock()

	// Parse the compact format: "address:wins:losses" per line
	cc.wallets = make(map[string]ContrarianStats)
	scanner := bufio.NewScanner(strings.NewReader(content))
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		parts := strings.Split(line, ":")
		if len(parts) != 3 {
			cc.logger.Debug("skipping malformed line", zap.Int("line", lineNum))
			continue
		}

		address := strings.ToLower(parts[0])
		wins, err1 := strconv.ParseUint(parts[1], 10, 16)
		losses, err2 := strconv.ParseUint(parts[2], 10, 16)
		if err1 != nil || err2 != nil {
			cc.logger.Debug("skipping line with invalid numbers", zap.Int("line", lineNum))
			continue
		}

		cc.wallets[address] = ContrarianStats{
			Wins:   uint16(wins),
			Losses: uint16(losses),
		}
	}

	cc.logger.Info("loaded contrarian cache",
		zap.Int("wallets", len(cc.wallets)),
	)

	return nil
}

// Save saves the cache to gist if there are changes.
func (cc *ContrarianCache) Save(ctx context.Context) error {
	if !cc.IsEnabled() {
		return nil
	}

	cc.mu.Lock()
	if !cc.dirty {
		cc.mu.Unlock()
		return nil
	}

	// Check size limit - estimate ~50 bytes per entry
	estimatedSize := len(cc.wallets) * 50
	if int64(estimatedSize) > cc.config.MaxSizeBytes {
		cc.pruneOldest()
	}

	// Build compact format
	var buf bytes.Buffer
	for address, stats := range cc.wallets {
		fmt.Fprintf(&buf, "%s:%d:%d\n", address, stats.Wins, stats.Losses)
	}

	content := buf.String()
	cc.dirty = false
	walletCount := len(cc.wallets)
	cc.mu.Unlock()

	if err := cc.gistClient.Save(ctx, cc.config.FileName, content, cc.config.GistID); err != nil {
		cc.mu.Lock()
		cc.dirty = true // Mark dirty again since save failed
		cc.mu.Unlock()
		return fmt.Errorf("save contrarian cache: %w", err)
	}

	cc.logger.Info("saved contrarian cache",
		zap.Int("wallets", walletCount),
		zap.Int("bytes", len(content)),
	)

	return nil
}

// pruneOldest removes entries with the lowest total activity to stay under size limit.
// Called with lock held.
func (cc *ContrarianCache) pruneOldest() {
	// Simple strategy: remove entries with total < 2 first, then < 3, etc.
	targetSize := int(cc.config.MaxSizeBytes / 50) // ~50 bytes per entry

	for threshold := uint16(2); len(cc.wallets) > targetSize && threshold < 100; threshold++ {
		for addr, stats := range cc.wallets {
			if stats.Wins+stats.Losses < threshold {
				delete(cc.wallets, addr)
				if len(cc.wallets) <= targetSize {
					break
				}
			}
		}
	}

	cc.logger.Info("pruned contrarian cache",
		zap.Int("remaining", len(cc.wallets)),
	)
}

// Size returns the number of wallets in the cache.
func (cc *ContrarianCache) Size() int {
	cc.mu.RLock()
	defer cc.mu.RUnlock()
	return len(cc.wallets)
}

// IsContrarianPrice returns true if the price is considered contrarian.
func IsContrarianPrice(price float64, threshold float64) bool {
	return price < threshold || price > (1-threshold)
}
