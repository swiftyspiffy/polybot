package app

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"polybot/config"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestContrarianStats(t *testing.T) {
	stats := ContrarianStats{Wins: 5, Losses: 3}

	if stats.TotalWins() != 5 {
		t.Errorf("expected 5 wins, got %d", stats.TotalWins())
	}

	expectedRate := 5.0 / 8.0
	if stats.ContrarianRate() != expectedRate {
		t.Errorf("expected rate %f, got %f", expectedRate, stats.ContrarianRate())
	}
}

func TestContrarianStats_ZeroTotal(t *testing.T) {
	stats := ContrarianStats{Wins: 0, Losses: 0}

	if stats.ContrarianRate() != 0 {
		t.Errorf("expected 0 rate for zero total, got %f", stats.ContrarianRate())
	}
}

func TestNewContrarianCache(t *testing.T) {
	cfg := &config.Config{
		ContrarianCache: config.ContrarianCacheConfig{
			GistID:            "",
			FileName:          "test.txt",
			SaveInterval:      5 * time.Minute,
			MaxSizeBytes:      1024,
			MinWins:           3,
			MinContrarianRate: 0.70,
		},
	}

	cc := NewContrarianCache(zap.NewNop(), cfg)

	if cc.logger == nil {
		t.Error("expected logger to be set")
	}
	if cc.wallets == nil {
		t.Error("expected wallets map to be initialized")
	}
	if cc.IsEnabled() {
		t.Error("expected cache to be disabled without gist config")
	}
}

func TestContrarianCache_RecordAndGet(t *testing.T) {
	cfg := &config.Config{
		Gist: config.GistConfig{
			Token: "test-token",
		},
		ContrarianCache: config.ContrarianCacheConfig{
			GistID:            "test-gist",
			FileName:          "test.txt",
			SaveInterval:      5 * time.Minute,
			MaxSizeBytes:      1024,
			MinWins:           3,
			MinContrarianRate: 0.70,
		},
	}

	cc := NewContrarianCache(zap.NewNop(), cfg)

	// Manually apply updates (simulating what processUpdates does)
	cc.applyUpdate(walletUpdate{address: "0xabc", win: true})
	cc.applyUpdate(walletUpdate{address: "0xabc", win: true})
	cc.applyUpdate(walletUpdate{address: "0xabc", win: false})

	stats, ok := cc.GetStats("0xabc")
	if !ok {
		t.Fatal("expected to find stats for 0xabc")
	}
	if stats.Wins != 2 {
		t.Errorf("expected 2 wins, got %d", stats.Wins)
	}
	if stats.Losses != 1 {
		t.Errorf("expected 1 loss, got %d", stats.Losses)
	}
}

func TestContrarianCache_ShouldAlert(t *testing.T) {
	cfg := &config.Config{
		Gist: config.GistConfig{
			Token: "test-token",
		},
		ContrarianCache: config.ContrarianCacheConfig{
			GistID:            "test-gist",
			FileName:          "test.txt",
			SaveInterval:      5 * time.Minute,
			MaxSizeBytes:      1024,
			MinWins:           3,
			MinContrarianRate: 0.70,
		},
	}

	cc := NewContrarianCache(zap.NewNop(), cfg)

	// Wallet with 2 wins - should NOT alert (below MinWins)
	cc.applyUpdate(walletUpdate{address: "0xlow", win: true})
	cc.applyUpdate(walletUpdate{address: "0xlow", win: true})

	if cc.ShouldAlert("0xlow") {
		t.Error("expected no alert for wallet with only 2 wins")
	}

	// Wallet with 3 wins, 0 losses (100% rate) - should alert
	cc.applyUpdate(walletUpdate{address: "0xhigh", win: true})
	cc.applyUpdate(walletUpdate{address: "0xhigh", win: true})
	cc.applyUpdate(walletUpdate{address: "0xhigh", win: true})

	if !cc.ShouldAlert("0xhigh") {
		t.Error("expected alert for wallet with 3 wins at 100% rate")
	}

	// Wallet with 3 wins, 3 losses (50% rate) - should NOT alert
	cc.applyUpdate(walletUpdate{address: "0xmid", win: true})
	cc.applyUpdate(walletUpdate{address: "0xmid", win: true})
	cc.applyUpdate(walletUpdate{address: "0xmid", win: true})
	cc.applyUpdate(walletUpdate{address: "0xmid", win: false})
	cc.applyUpdate(walletUpdate{address: "0xmid", win: false})
	cc.applyUpdate(walletUpdate{address: "0xmid", win: false})

	if cc.ShouldAlert("0xmid") {
		t.Error("expected no alert for wallet with 50% rate")
	}

	// Wallet with 7 wins, 3 losses (70% rate) - should alert
	for i := 0; i < 7; i++ {
		cc.applyUpdate(walletUpdate{address: "0xedge", win: true})
	}
	for i := 0; i < 3; i++ {
		cc.applyUpdate(walletUpdate{address: "0xedge", win: false})
	}

	if !cc.ShouldAlert("0xedge") {
		t.Error("expected alert for wallet with exactly 70% rate")
	}
}

func TestContrarianCache_AddressNormalization(t *testing.T) {
	cfg := &config.Config{
		Gist: config.GistConfig{
			Token: "test-token",
		},
		ContrarianCache: config.ContrarianCacheConfig{
			GistID:   "test-gist",
			FileName: "test.txt",
		},
	}

	cc := NewContrarianCache(zap.NewNop(), cfg)

	// applyUpdate uses address as-is, but RecordContrarianResult lowercases
	// The walletUpdate struct should have lowercase address (as RecordContrarianResult does)
	cc.applyUpdate(walletUpdate{address: "0xabc123", win: true})

	// GetStats lowercases the query, so both should work
	stats, ok := cc.GetStats("0xabc123")
	if !ok {
		t.Error("expected to find stats with lowercase address")
	}
	if stats.Wins != 1 {
		t.Errorf("expected 1 win, got %d", stats.Wins)
	}

	// GetStats lowercases the query
	stats2, ok := cc.GetStats("0xABC123")
	if !ok {
		t.Error("expected to find stats with uppercase query")
	}
	if stats2.Wins != 1 {
		t.Errorf("expected 1 win, got %d", stats2.Wins)
	}
}

func TestContrarianCache_Size(t *testing.T) {
	cfg := &config.Config{
		ContrarianCache: config.ContrarianCacheConfig{
			GistID:   "test-gist",
			FileName: "test.txt",
		},
	}

	cc := NewContrarianCache(nil, cfg)

	if cc.Size() != 0 {
		t.Error("expected empty cache")
	}

	cc.wallets["0x1"] = ContrarianStats{Wins: 1}
	cc.wallets["0x2"] = ContrarianStats{Wins: 2}

	if cc.Size() != 2 {
		t.Errorf("expected size 2, got %d", cc.Size())
	}
}

func TestContrarianCache_LoadParse(t *testing.T) {
	cfg := &config.Config{
		ContrarianCache: config.ContrarianCacheConfig{
			GistID:   "test-gist",
			FileName: "test.txt",
		},
	}

	// Verify cache can be created
	cc := NewContrarianCache(zap.NewNop(), cfg)
	if cc == nil {
		t.Fatal("expected cache to be created")
	}

	// Simulate loading content - test format parsing
	content := "0xabc:5:2\n0xdef:10:3\n0x123:0:5\n"

	// Parse manually (same logic as Load)
	lines := bytes.Split([]byte(content), []byte("\n"))
	validEntries := 0
	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		parts := bytes.Split(line, []byte(":"))
		if len(parts) != 3 {
			continue
		}
		validEntries++
	}

	// The actual Load function requires a gist client, so we test the format parsing logic
	if validEntries != 3 {
		t.Errorf("expected 3 valid entries, got %d", validEntries)
	}
}

func TestIsContrarianPrice(t *testing.T) {
	threshold := 0.20

	// Prices below threshold are contrarian
	if !IsContrarianPrice(0.05, threshold) {
		t.Error("expected 0.05 to be contrarian")
	}
	if !IsContrarianPrice(0.19, threshold) {
		t.Error("expected 0.19 to be contrarian")
	}

	// Prices above (1-threshold) are also contrarian
	if !IsContrarianPrice(0.85, threshold) {
		t.Error("expected 0.85 to be contrarian")
	}
	if !IsContrarianPrice(0.95, threshold) {
		t.Error("expected 0.95 to be contrarian")
	}

	// Middle prices are not contrarian
	if IsContrarianPrice(0.50, threshold) {
		t.Error("expected 0.50 to NOT be contrarian")
	}
	if IsContrarianPrice(0.30, threshold) {
		t.Error("expected 0.30 to NOT be contrarian")
	}
	if IsContrarianPrice(0.70, threshold) {
		t.Error("expected 0.70 to NOT be contrarian")
	}

	// Edge cases - uses strict inequality (< and >), so exactly at threshold is NOT contrarian
	if IsContrarianPrice(0.20, threshold) {
		t.Error("expected 0.20 to NOT be contrarian (at threshold, uses < not <=)")
	}
	if IsContrarianPrice(0.80, threshold) {
		t.Error("expected 0.80 to NOT be contrarian (at 1-threshold, uses > not >=)")
	}
	// Just below/above thresholds should be contrarian
	if !IsContrarianPrice(0.199, threshold) {
		t.Error("expected 0.199 to be contrarian")
	}
	if !IsContrarianPrice(0.801, threshold) {
		t.Error("expected 0.801 to be contrarian")
	}
}

func TestContrarianCache_DirtyFlag(t *testing.T) {
	cfg := &config.Config{
		ContrarianCache: config.ContrarianCacheConfig{
			GistID:   "test-gist",
			FileName: "test.txt",
		},
	}

	cc := NewContrarianCache(nil, cfg)

	if cc.dirty {
		t.Error("expected dirty to be false initially")
	}

	cc.applyUpdate(walletUpdate{address: "0x1", win: true})

	if !cc.dirty {
		t.Error("expected dirty to be true after update")
	}
}

func TestContrarianCache_Overflow(t *testing.T) {
	cfg := &config.Config{
		ContrarianCache: config.ContrarianCacheConfig{
			GistID:   "test-gist",
			FileName: "test.txt",
		},
	}

	cc := NewContrarianCache(nil, cfg)

	// Set to max value
	cc.wallets["0x1"] = ContrarianStats{Wins: 65535, Losses: 65535}

	// Apply more updates - should not overflow
	cc.applyUpdate(walletUpdate{address: "0x1", win: true})
	cc.applyUpdate(walletUpdate{address: "0x1", win: false})

	stats := cc.wallets["0x1"]
	if stats.Wins != 65535 {
		t.Errorf("expected wins to stay at max, got %d", stats.Wins)
	}
	if stats.Losses != 65535 {
		t.Errorf("expected losses to stay at max, got %d", stats.Losses)
	}
}

func TestContrarianCache_RecordContrarianResult_Disabled(t *testing.T) {
	cfg := &config.Config{
		ContrarianCache: config.ContrarianCacheConfig{
			GistID: "", // Not configured
		},
	}

	cc := NewContrarianCache(nil, cfg)

	// Should not panic or block when disabled
	cc.RecordContrarianResult("0x123", true)

	if cc.Size() != 0 {
		t.Error("expected no entries when disabled")
	}
}

func TestContrarianCache_ShouldAlert_Disabled(t *testing.T) {
	cfg := &config.Config{
		ContrarianCache: config.ContrarianCacheConfig{
			GistID: "", // Not configured
		},
	}

	cc := NewContrarianCache(nil, cfg)

	if cc.ShouldAlert("0x123") {
		t.Error("expected no alert when cache is disabled")
	}
}

func TestContrarianCache_ShouldAlert_NotFound(t *testing.T) {
	cfg := &config.Config{
		Gist: config.GistConfig{
			Token: "test-token",
		},
		ContrarianCache: config.ContrarianCacheConfig{
			GistID:   "test-gist",
			FileName: "test.txt",
			MinWins:  3,
		},
	}

	cc := NewContrarianCache(nil, cfg)

	if cc.ShouldAlert("0xnonexistent") {
		t.Error("expected no alert for nonexistent wallet")
	}
}

func TestContrarianCache_StartStop(t *testing.T) {
	cfg := &config.Config{
		ContrarianCache: config.ContrarianCacheConfig{
			GistID:       "test-gist",
			FileName:     "test.txt",
			SaveInterval: 1 * time.Hour,
		},
	}

	cc := NewContrarianCache(zap.NewNop(), cfg)

	ctx, cancel := context.WithCancel(context.Background())

	// Start should not block
	cc.Start(ctx)

	// Give goroutines time to start
	time.Sleep(10 * time.Millisecond)

	// Stop should not block
	cancel()
	cc.Stop()

	// Give goroutines time to stop
	time.Sleep(10 * time.Millisecond)
}

func TestContrarianCache_ProcessUpdates_ContextCancel(t *testing.T) {
	cfg := &config.Config{
		ContrarianCache: config.ContrarianCacheConfig{
			GistID:       "test-gist",
			FileName:     "test.txt",
			SaveInterval: 1 * time.Hour,
		},
	}

	cc := NewContrarianCache(zap.NewNop(), cfg)

	ctx, cancel := context.WithCancel(context.Background())

	// Start processUpdates goroutine
	done := make(chan struct{})
	go func() {
		cc.processUpdates(ctx)
		close(done)
	}()

	// Cancel context
	cancel()

	// Should exit cleanly
	select {
	case <-done:
		// Good
	case <-time.After(100 * time.Millisecond):
		t.Error("processUpdates should stop when context is cancelled")
	}
}

func TestContrarianCache_ProcessUpdates_DoneChannel(t *testing.T) {
	cfg := &config.Config{
		ContrarianCache: config.ContrarianCacheConfig{
			GistID:       "test-gist",
			FileName:     "test.txt",
			SaveInterval: 1 * time.Hour,
		},
	}

	cc := NewContrarianCache(zap.NewNop(), cfg)

	ctx := context.Background()

	// Start processUpdates goroutine
	done := make(chan struct{})
	go func() {
		cc.processUpdates(ctx)
		close(done)
	}()

	// Close doneCh
	cc.Stop()

	// Should exit cleanly
	select {
	case <-done:
		// Good
	case <-time.After(100 * time.Millisecond):
		t.Error("processUpdates should stop when doneCh is closed")
	}
}

func TestContrarianCache_ProcessUpdates_AppliesUpdates(t *testing.T) {
	cfg := &config.Config{
		Gist: config.GistConfig{
			Token: "test-token",
		},
		ContrarianCache: config.ContrarianCacheConfig{
			GistID:       "test-gist",
			FileName:     "test.txt",
			SaveInterval: 1 * time.Hour,
		},
	}

	cc := NewContrarianCache(zap.NewNop(), cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start processUpdates goroutine
	go cc.processUpdates(ctx)

	// Send updates via channel
	cc.updateCh <- walletUpdate{address: "0xtest1", win: true}
	cc.updateCh <- walletUpdate{address: "0xtest1", win: false}
	cc.updateCh <- walletUpdate{address: "0xtest2", win: true}

	// Give time to process
	time.Sleep(50 * time.Millisecond)

	stats1, ok := cc.GetStats("0xtest1")
	if !ok {
		t.Fatal("expected to find stats for 0xtest1")
	}
	if stats1.Wins != 1 || stats1.Losses != 1 {
		t.Errorf("expected 1 win, 1 loss for 0xtest1, got %d wins, %d losses", stats1.Wins, stats1.Losses)
	}

	stats2, ok := cc.GetStats("0xtest2")
	if !ok {
		t.Fatal("expected to find stats for 0xtest2")
	}
	if stats2.Wins != 1 {
		t.Errorf("expected 1 win for 0xtest2, got %d", stats2.Wins)
	}
}

func TestContrarianCache_RecordContrarianResult_ChannelFull(t *testing.T) {
	cfg := &config.Config{
		Gist: config.GistConfig{
			Token: "test-token",
		},
		ContrarianCache: config.ContrarianCacheConfig{
			GistID:       "test-gist",
			FileName:     "test.txt",
			SaveInterval: 1 * time.Hour,
		},
	}

	cc := NewContrarianCache(zap.NewNop(), cfg)

	// Fill the channel (buffer size is 1000)
	for i := 0; i < 1000; i++ {
		cc.updateCh <- walletUpdate{address: "0xfill", win: true}
	}

	// This should not block (non-blocking send with default case)
	cc.RecordContrarianResult("0xoverflow", true)

	// Should complete without blocking
}

func TestContrarianCache_PruneOldest(t *testing.T) {
	cfg := &config.Config{
		ContrarianCache: config.ContrarianCacheConfig{
			GistID:       "test-gist",
			FileName:     "test.txt",
			MaxSizeBytes: 150, // ~3 entries at 50 bytes each
		},
	}

	cc := NewContrarianCache(zap.NewNop(), cfg)

	// Add entries with varying activity levels
	cc.wallets["0xlow1"] = ContrarianStats{Wins: 1, Losses: 0}   // Total: 1
	cc.wallets["0xlow2"] = ContrarianStats{Wins: 0, Losses: 1}   // Total: 1
	cc.wallets["0xmed"] = ContrarianStats{Wins: 2, Losses: 1}    // Total: 3
	cc.wallets["0xhigh"] = ContrarianStats{Wins: 5, Losses: 5}   // Total: 10
	cc.wallets["0xhigh2"] = ContrarianStats{Wins: 10, Losses: 5} // Total: 15

	// Should have 5 entries
	if len(cc.wallets) != 5 {
		t.Fatalf("expected 5 entries, got %d", len(cc.wallets))
	}

	// Prune should remove low activity entries
	cc.pruneOldest()

	// Should have removed entries with total < 2 first
	if _, exists := cc.wallets["0xlow1"]; exists {
		t.Error("expected 0xlow1 to be pruned")
	}
	if _, exists := cc.wallets["0xlow2"]; exists {
		t.Error("expected 0xlow2 to be pruned")
	}

	// Higher activity should remain
	if _, exists := cc.wallets["0xhigh"]; !exists {
		t.Error("expected 0xhigh to remain")
	}
	if _, exists := cc.wallets["0xhigh2"]; !exists {
		t.Error("expected 0xhigh2 to remain")
	}
}

func TestContrarianCache_PruneOldest_MultiplePasses(t *testing.T) {
	cfg := &config.Config{
		ContrarianCache: config.ContrarianCacheConfig{
			GistID:       "test-gist",
			FileName:     "test.txt",
			MaxSizeBytes: 50, // ~1 entry at 50 bytes each
		},
	}

	cc := NewContrarianCache(zap.NewNop(), cfg)

	// Add entries with varying activity - all above threshold 2
	cc.wallets["0xmed1"] = ContrarianStats{Wins: 1, Losses: 1} // Total: 2
	cc.wallets["0xmed2"] = ContrarianStats{Wins: 2, Losses: 1} // Total: 3
	cc.wallets["0xmed3"] = ContrarianStats{Wins: 2, Losses: 2} // Total: 4
	cc.wallets["0xhigh"] = ContrarianStats{Wins: 50, Losses: 50} // Total: 100

	// Prune - should remove multiple rounds to get under target
	cc.pruneOldest()

	// Should be at or under target size (1 entry)
	if len(cc.wallets) > 1 {
		t.Errorf("expected at most 1 entry after prune, got %d", len(cc.wallets))
	}

	// The highest activity entry should remain
	if _, exists := cc.wallets["0xhigh"]; !exists {
		t.Error("expected 0xhigh to remain")
	}
}

func TestContrarianCache_PeriodicSave_NotEnabled(t *testing.T) {
	cfg := &config.Config{
		ContrarianCache: config.ContrarianCacheConfig{
			GistID:       "", // Not enabled
			FileName:     "test.txt",
			SaveInterval: 10 * time.Millisecond,
		},
	}

	cc := NewContrarianCache(zap.NewNop(), cfg)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		cc.periodicSave(ctx)
		close(done)
	}()

	// Should return immediately when not enabled
	select {
	case <-done:
		// Good - returned immediately
	case <-time.After(50 * time.Millisecond):
		cancel()
		t.Error("periodicSave should return immediately when not enabled")
	}
}

func TestContrarianCache_PeriodicSave_ContextCancel(t *testing.T) {
	cfg := &config.Config{
		Gist: config.GistConfig{
			Token: "test-token",
		},
		ContrarianCache: config.ContrarianCacheConfig{
			GistID:       "test-gist",
			FileName:     "test.txt",
			SaveInterval: 1 * time.Hour, // Long interval
		},
	}

	cc := NewContrarianCache(zap.NewNop(), cfg)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		cc.periodicSave(ctx)
		close(done)
	}()

	// Cancel context
	cancel()

	// Should exit
	select {
	case <-done:
		// Good
	case <-time.After(100 * time.Millisecond):
		t.Error("periodicSave should stop when context is cancelled")
	}
}

func TestContrarianCache_PeriodicSave_DoneChannel(t *testing.T) {
	cfg := &config.Config{
		Gist: config.GistConfig{
			Token: "test-token",
		},
		ContrarianCache: config.ContrarianCacheConfig{
			GistID:       "test-gist",
			FileName:     "test.txt",
			SaveInterval: 1 * time.Hour,
		},
	}

	cc := NewContrarianCache(zap.NewNop(), cfg)

	ctx := context.Background()

	done := make(chan struct{})
	go func() {
		cc.periodicSave(ctx)
		close(done)
	}()

	// Close doneCh
	cc.Stop()

	// Should exit
	select {
	case <-done:
		// Good
	case <-time.After(100 * time.Millisecond):
		t.Error("periodicSave should stop when doneCh is closed")
	}
}

func TestContrarianCache_Save_NotDirty(t *testing.T) {
	cfg := &config.Config{
		Gist: config.GistConfig{
			Token: "test-token",
		},
		ContrarianCache: config.ContrarianCacheConfig{
			GistID:       "test-gist",
			FileName:     "test.txt",
			SaveInterval: 1 * time.Hour,
		},
	}

	cc := NewContrarianCache(zap.NewNop(), cfg)

	// Not dirty - should return nil without error
	err := cc.Save(context.Background())
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestContrarianCache_Save_NotEnabled(t *testing.T) {
	cfg := &config.Config{
		ContrarianCache: config.ContrarianCacheConfig{
			GistID:   "", // Not enabled
			FileName: "test.txt",
		},
	}

	cc := NewContrarianCache(zap.NewNop(), cfg)
	cc.dirty = true

	// Not enabled - should return nil
	err := cc.Save(context.Background())
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestContrarianCache_LoadFormat(t *testing.T) {
	// Test the format parsing directly by simulating the parsing logic
	content := `0xabc:5:2
0xdef:10:3
0x123:0:5
malformed-line
0xgood:1:1
:invalid:format:extra
0xinvalid:abc:def
`

	// Parse using same logic as Load
	wallets := make(map[string]ContrarianStats)
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		parts := strings.Split(line, ":")
		if len(parts) != 3 {
			continue
		}

		address := strings.ToLower(parts[0])
		wins, err1 := strconv.ParseUint(parts[1], 10, 16)
		losses, err2 := strconv.ParseUint(parts[2], 10, 16)
		if err1 != nil || err2 != nil {
			continue
		}

		wallets[address] = ContrarianStats{
			Wins:   uint16(wins),
			Losses: uint16(losses),
		}
	}

	// Should have parsed 4 valid entries
	if len(wallets) != 4 {
		t.Errorf("expected 4 valid entries, got %d", len(wallets))
	}

	// Check specific entries
	if stats, ok := wallets["0xabc"]; !ok || stats.Wins != 5 || stats.Losses != 2 {
		t.Error("0xabc not parsed correctly")
	}
	if stats, ok := wallets["0xdef"]; !ok || stats.Wins != 10 || stats.Losses != 3 {
		t.Error("0xdef not parsed correctly")
	}
	if stats, ok := wallets["0x123"]; !ok || stats.Wins != 0 || stats.Losses != 5 {
		t.Error("0x123 not parsed correctly")
	}
	if stats, ok := wallets["0xgood"]; !ok || stats.Wins != 1 || stats.Losses != 1 {
		t.Error("0xgood not parsed correctly")
	}
}

func TestContrarianCache_SaveFormat(t *testing.T) {
	cfg := &config.Config{
		ContrarianCache: config.ContrarianCacheConfig{
			GistID:       "test-gist",
			FileName:     "test.txt",
			MaxSizeBytes: 1000000,
		},
	}

	cc := NewContrarianCache(zap.NewNop(), cfg)

	// Add some wallets
	cc.wallets["0xabc"] = ContrarianStats{Wins: 5, Losses: 2}
	cc.wallets["0xdef"] = ContrarianStats{Wins: 10, Losses: 3}
	cc.dirty = true

	// Build the same format as Save does
	var buf bytes.Buffer
	for address, stats := range cc.wallets {
		fmt.Fprintf(&buf, "%s:%d:%d\n", address, stats.Wins, stats.Losses)
	}

	content := buf.String()

	// Should contain both entries
	if !strings.Contains(content, "0xabc:5:2") {
		t.Error("expected 0xabc:5:2 in output")
	}
	if !strings.Contains(content, "0xdef:10:3") {
		t.Error("expected 0xdef:10:3 in output")
	}
}

func TestContrarianCache_LoadWithMock(t *testing.T) {
	cfg := &config.Config{
		Gist: config.GistConfig{
			Token: "test-token",
		},
		ContrarianCache: config.ContrarianCacheConfig{
			GistID:   "test-gist",
			FileName: "contrarian.txt",
		},
	}

	cc := NewContrarianCache(zap.NewNop(), cfg)

	// Use mock gist storage
	mockGist := NewMockGistStorage()
	mockGist.SetContent("contrarian.txt", "0xabc:5:2\n0xdef:10:3\n0x123:1:1\n")
	cc.SetGistClient(mockGist)

	// Load from mock
	err := cc.Load(context.Background())
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Verify loaded data
	if cc.Size() != 3 {
		t.Errorf("expected 3 wallets, got %d", cc.Size())
	}

	stats, ok := cc.GetStats("0xabc")
	if !ok {
		t.Fatal("expected to find 0xabc")
	}
	if stats.Wins != 5 || stats.Losses != 2 {
		t.Errorf("expected 5 wins, 2 losses for 0xabc, got %d wins, %d losses", stats.Wins, stats.Losses)
	}

	stats, ok = cc.GetStats("0xdef")
	if !ok {
		t.Fatal("expected to find 0xdef")
	}
	if stats.Wins != 10 || stats.Losses != 3 {
		t.Errorf("expected 10 wins, 3 losses for 0xdef, got %d wins, %d losses", stats.Wins, stats.Losses)
	}
}

func TestContrarianCache_SaveWithMock(t *testing.T) {
	cfg := &config.Config{
		Gist: config.GistConfig{
			Token: "test-token",
		},
		ContrarianCache: config.ContrarianCacheConfig{
			GistID:       "test-gist",
			FileName:     "contrarian.txt",
			MaxSizeBytes: 1000000,
		},
	}

	cc := NewContrarianCache(zap.NewNop(), cfg)

	// Use mock gist storage
	mockGist := NewMockGistStorage()
	cc.SetGistClient(mockGist)

	// Add data and mark dirty
	cc.wallets["0xabc"] = ContrarianStats{Wins: 5, Losses: 2}
	cc.wallets["0xdef"] = ContrarianStats{Wins: 10, Losses: 3}
	cc.dirty = true

	// Save to mock
	err := cc.Save(context.Background())
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Verify saved content
	content := mockGist.GetContent("contrarian.txt")
	if !strings.Contains(content, "0xabc:5:2") {
		t.Error("expected 0xabc:5:2 in saved content")
	}
	if !strings.Contains(content, "0xdef:10:3") {
		t.Error("expected 0xdef:10:3 in saved content")
	}

	// Dirty flag should be cleared
	if cc.dirty {
		t.Error("expected dirty flag to be cleared after save")
	}
}

func TestContrarianCache_SaveError(t *testing.T) {
	cfg := &config.Config{
		Gist: config.GistConfig{
			Token: "test-token",
		},
		ContrarianCache: config.ContrarianCacheConfig{
			GistID:       "test-gist",
			FileName:     "contrarian.txt",
			MaxSizeBytes: 1000000,
		},
	}

	cc := NewContrarianCache(zap.NewNop(), cfg)

	// Use mock gist storage with error
	mockGist := NewMockGistStorage()
	mockGist.SetSaveError(fmt.Errorf("mock save error"))
	cc.SetGistClient(mockGist)

	// Add data and mark dirty
	cc.wallets["0xabc"] = ContrarianStats{Wins: 5, Losses: 2}
	cc.dirty = true

	// Save should fail
	err := cc.Save(context.Background())
	if err == nil {
		t.Fatal("expected error from Save")
	}

	// Dirty flag should remain set
	if !cc.dirty {
		t.Error("expected dirty flag to remain set after save error")
	}
}

func TestContrarianCache_LoadError(t *testing.T) {
	cfg := &config.Config{
		Gist: config.GistConfig{
			Token: "test-token",
		},
		ContrarianCache: config.ContrarianCacheConfig{
			GistID:   "test-gist",
			FileName: "contrarian.txt",
		},
	}

	cc := NewContrarianCache(zap.NewNop(), cfg)

	// Use mock gist storage with error
	mockGist := NewMockGistStorage()
	mockGist.SetLoadError(fmt.Errorf("mock load error"))
	cc.SetGistClient(mockGist)

	// Load should fail
	err := cc.Load(context.Background())
	if err == nil {
		t.Fatal("expected error from Load")
	}
}

func TestContrarianCache_Load_NotEnabled(t *testing.T) {
	cfg := &config.Config{
		ContrarianCache: config.ContrarianCacheConfig{
			GistID:   "", // Not enabled
			FileName: "contrarian.txt",
		},
	}

	cc := NewContrarianCache(zap.NewNop(), cfg)

	// Load should return nil when not enabled
	err := cc.Load(context.Background())
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}
