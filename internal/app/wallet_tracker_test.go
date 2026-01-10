package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"polybot/config"
	"polybot/clients/polymarketapi"
	"testing"
	"time"

	"go.uber.org/zap"
)

func newTestWalletTracker(t *testing.T, handler http.HandlerFunc) (*WalletTracker, *httptest.Server) {
	server := httptest.NewServer(handler)
	cfg := &config.Config{
		Polymarket: config.PolymarketConfig{
			DataAPIURL: server.URL,
		},
	}
	client := polymarketapi.NewPolymarketApiClient(zap.NewNop(), cfg)
	tracker := NewWalletTracker(zap.NewNop(), client, 5*time.Minute, 0.20, 0.85, nil)
	return tracker, server
}

func TestNewWalletTracker(t *testing.T) {
	tracker := NewWalletTracker(nil, nil, 0, 0.20, 0.85, nil)

	if tracker.logger == nil {
		t.Error("expected logger to be set")
	}
	if tracker.cacheTTL != 5*time.Minute {
		t.Errorf("expected default TTL of 5m, got %v", tracker.cacheTTL)
	}
	if tracker.cache == nil {
		t.Error("expected cache to be initialized")
	}
}

func TestNewWalletTracker_CustomTTL(t *testing.T) {
	tracker := NewWalletTracker(zap.NewNop(), nil, 10*time.Minute, 0.20, 0.85, nil)

	if tracker.cacheTTL != 10*time.Minute {
		t.Errorf("expected TTL of 10m, got %v", tracker.cacheTTL)
	}
}

func TestGetStats_FetchAndCache(t *testing.T) {
	callCount := 0
	tracker, server := newTestWalletTracker(t, func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if r.URL.Path == "/activity" {
			activity := []polymarketapi.Activity{
				{ConditionID: "cond1"},
				{ConditionID: "cond2"},
				{ConditionID: "cond1"}, // Duplicate
			}
			json.NewEncoder(w).Encode(activity)
		} else if r.URL.Path == "/closed-positions" {
			positions := []polymarketapi.ClosedPosition{
				{RealizedPnl: 100},
				{RealizedPnl: -50},
				{RealizedPnl: 75},
			}
			json.NewEncoder(w).Encode(positions)
		}
	})
	defer server.Close()

	ctx := context.Background()

	// First call should fetch
	stats, err := tracker.GetStats(ctx, "0x123")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if stats.UniqueMarkets != 2 {
		t.Errorf("expected 2 unique markets, got %d", stats.UniqueMarkets)
	}
	if stats.WinCount != 2 {
		t.Errorf("expected 2 wins, got %d", stats.WinCount)
	}
	if stats.LossCount != 1 {
		t.Errorf("expected 1 loss, got %d", stats.LossCount)
	}
	expectedWinRate := 2.0 / 3.0
	if stats.WinRate != expectedWinRate {
		t.Errorf("expected win rate %f, got %f", expectedWinRate, stats.WinRate)
	}

	// Second call should use cache
	stats2, err := tracker.GetStats(ctx, "0x123")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if stats2 != stats {
		t.Error("expected same cached stats")
	}
	// Only 2 calls (activity + positions) for the first request
	if callCount != 2 {
		t.Errorf("expected 2 API calls, got %d", callCount)
	}
}

func TestGetStats_CacheExpiry(t *testing.T) {
	callCount := 0
	tracker, server := newTestWalletTracker(t, func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if r.URL.Path == "/activity" {
			json.NewEncoder(w).Encode([]polymarketapi.Activity{})
		} else if r.URL.Path == "/closed-positions" {
			json.NewEncoder(w).Encode([]polymarketapi.ClosedPosition{})
		}
	})
	defer server.Close()

	// Set very short TTL
	tracker.cacheTTL = 1 * time.Millisecond

	ctx := context.Background()

	// First call
	_, err := tracker.GetStats(ctx, "0x123")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Wait for cache to expire
	time.Sleep(5 * time.Millisecond)

	// Second call should fetch again
	_, err = tracker.GetStats(ctx, "0x123")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Should have 4 calls (2 per request)
	if callCount != 4 {
		t.Errorf("expected 4 API calls, got %d", callCount)
	}
}

func TestGetStats_StaleOnError(t *testing.T) {
	firstCall := true
	tracker, server := newTestWalletTracker(t, func(w http.ResponseWriter, r *http.Request) {
		if firstCall {
			if r.URL.Path == "/activity" {
				json.NewEncoder(w).Encode([]polymarketapi.Activity{{ConditionID: "cond1"}})
			} else {
				json.NewEncoder(w).Encode([]polymarketapi.ClosedPosition{})
			}
		} else {
			w.WriteHeader(http.StatusInternalServerError)
		}
	})
	defer server.Close()

	tracker.cacheTTL = 1 * time.Millisecond
	ctx := context.Background()

	// First call succeeds
	stats, err := tracker.GetStats(ctx, "0x123")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if stats.UniqueMarkets != 1 {
		t.Errorf("expected 1 unique market, got %d", stats.UniqueMarkets)
	}

	firstCall = false
	time.Sleep(5 * time.Millisecond)

	// Second call fails but returns stale cache
	stats2, err := tracker.GetStats(ctx, "0x123")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if stats2.UniqueMarkets != 1 {
		t.Error("expected stale cache to be returned")
	}
}

func TestIsLowActivity(t *testing.T) {
	tracker, server := newTestWalletTracker(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/activity" {
			activity := []polymarketapi.Activity{
				{ConditionID: "cond1"},
				{ConditionID: "cond2"},
				{ConditionID: "cond3"},
			}
			json.NewEncoder(w).Encode(activity)
		} else {
			json.NewEncoder(w).Encode([]polymarketapi.ClosedPosition{})
		}
	})
	defer server.Close()

	ctx := context.Background()

	// 3 markets < 5, so should be low activity
	isLow, stats, err := tracker.IsLowActivity(ctx, "0x123", 5)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !isLow {
		t.Error("expected low activity")
	}
	if stats.UniqueMarkets != 3 {
		t.Errorf("expected 3 markets, got %d", stats.UniqueMarkets)
	}

	// 3 markets >= 3, so should NOT be low activity
	isLow, _, err = tracker.IsLowActivity(ctx, "0x123", 3)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if isLow {
		t.Error("expected not low activity")
	}
}

func TestCacheSize(t *testing.T) {
	tracker := NewWalletTracker(nil, nil, 5*time.Minute, 0.20, 0.85, nil)

	if tracker.CacheSize() != 0 {
		t.Error("expected empty cache")
	}

	tracker.cache["wallet1"] = &WalletStats{Wallet: "wallet1"}
	tracker.cache["wallet2"] = &WalletStats{Wallet: "wallet2"}

	if tracker.CacheSize() != 2 {
		t.Errorf("expected cache size 2, got %d", tracker.CacheSize())
	}
}

func TestPruneStale(t *testing.T) {
	tracker := NewWalletTracker(nil, nil, 5*time.Minute, 0.20, 0.85, nil)

	now := time.Now()
	tracker.cache["fresh"] = &WalletStats{
		Wallet:    "fresh",
		FetchedAt: now,
	}
	tracker.cache["stale"] = &WalletStats{
		Wallet:    "stale",
		FetchedAt: now.Add(-20 * time.Minute), // > 2 * TTL
	}

	pruned := tracker.PruneStale()

	if pruned != 1 {
		t.Errorf("expected 1 pruned, got %d", pruned)
	}
	if tracker.CacheSize() != 1 {
		t.Errorf("expected 1 remaining, got %d", tracker.CacheSize())
	}
	if _, ok := tracker.cache["fresh"]; !ok {
		t.Error("expected fresh entry to remain")
	}
}

func TestTrimToMaxSize(t *testing.T) {
	tracker := NewWalletTracker(nil, nil, 5*time.Minute, 0.20, 0.85, nil)

	now := time.Now()
	// Add entries with different timestamps
	for i := 0; i < 100; i++ {
		tracker.cache[string(rune('a'+i))] = &WalletStats{
			Wallet:        string(rune('a' + i)),
			UniqueMarkets: i,
			FetchedAt:     now.Add(time.Duration(i) * time.Minute),
		}
	}

	initialSize := tracker.CacheSize()
	if initialSize != 100 {
		t.Errorf("expected 100 entries, got %d", initialSize)
	}

	// Trim to a small size (should remove oldest entries)
	removed := tracker.TrimToMaxSize(1000) // Very small limit

	if removed == 0 {
		t.Error("expected some entries to be removed")
	}
	if tracker.CacheSize() >= initialSize {
		t.Error("expected cache to be smaller")
	}
}

func TestTrimToMaxSize_NoTrimNeeded(t *testing.T) {
	tracker := NewWalletTracker(nil, nil, 5*time.Minute, 0.20, 0.85, nil)

	tracker.cache["wallet1"] = &WalletStats{Wallet: "wallet1", FetchedAt: time.Now()}

	// Very large limit, no trim needed
	removed := tracker.TrimToMaxSize(50 * 1024 * 1024) // 50MB

	if removed != 0 {
		t.Errorf("expected 0 removed, got %d", removed)
	}
}

func TestTrimToMaxSize_ZeroLimit(t *testing.T) {
	tracker := NewWalletTracker(nil, nil, 5*time.Minute, 0.20, 0.85, nil)

	tracker.cache["wallet1"] = &WalletStats{Wallet: "wallet1", FetchedAt: time.Now()}

	removed := tracker.TrimToMaxSize(0)

	if removed != 0 {
		t.Error("expected no removal with zero limit")
	}
}

func TestTrimToMaxSize_NegativeLimit(t *testing.T) {
	tracker := NewWalletTracker(nil, nil, 5*time.Minute, 0.20, 0.85, nil)

	tracker.cache["wallet1"] = &WalletStats{Wallet: "wallet1", FetchedAt: time.Now()}

	removed := tracker.TrimToMaxSize(-100)

	if removed != 0 {
		t.Error("expected no removal with negative limit")
	}
}

func TestExportCache(t *testing.T) {
	tracker := NewWalletTracker(nil, nil, 5*time.Minute, 0.20, 0.85, nil)

	now := time.Now()
	tracker.cache["wallet1"] = &WalletStats{
		Wallet:        "wallet1",
		UniqueMarkets: 5,
		WinCount:      3,
		FetchedAt:     now,
	}

	snapshot := tracker.ExportCache()

	if snapshot.Version != 1 {
		t.Errorf("expected version 1, got %d", snapshot.Version)
	}
	if len(snapshot.Wallets) != 1 {
		t.Errorf("expected 1 wallet, got %d", len(snapshot.Wallets))
	}
	if snapshot.Wallets["wallet1"].UniqueMarkets != 5 {
		t.Error("unexpected wallet data")
	}
}

func TestExportCacheJSON(t *testing.T) {
	tracker := NewWalletTracker(nil, nil, 5*time.Minute, 0.20, 0.85, nil)

	tracker.cache["wallet1"] = &WalletStats{
		Wallet:    "wallet1",
		FetchedAt: time.Now(),
	}

	data, err := tracker.ExportCacheJSON()
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	var snapshot CacheSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		t.Errorf("failed to unmarshal: %v", err)
	}
	if len(snapshot.Wallets) != 1 {
		t.Error("unexpected snapshot")
	}
}

func TestImportCache(t *testing.T) {
	tracker := NewWalletTracker(zap.NewNop(), nil, 5*time.Minute, 0.20, 0.85, nil)

	now := time.Now()
	snapshot := &CacheSnapshot{
		Version:   1,
		Timestamp: now,
		Wallets: map[string]WalletStats{
			"wallet1": {Wallet: "wallet1", UniqueMarkets: 10, FetchedAt: now},
			"wallet2": {Wallet: "wallet2", UniqueMarkets: 20, FetchedAt: now},
		},
	}

	imported := tracker.ImportCache(snapshot)

	if imported != 2 {
		t.Errorf("expected 2 imported, got %d", imported)
	}
	if tracker.CacheSize() != 2 {
		t.Errorf("expected cache size 2, got %d", tracker.CacheSize())
	}
}

func TestImportCache_Merge(t *testing.T) {
	tracker := NewWalletTracker(zap.NewNop(), nil, 5*time.Minute, 0.20, 0.85, nil)

	now := time.Now()
	// Existing entry
	tracker.cache["wallet1"] = &WalletStats{
		Wallet:        "wallet1",
		UniqueMarkets: 5,
		FetchedAt:     now,
	}

	// Import with older and newer data
	snapshot := &CacheSnapshot{
		Version:   1,
		Timestamp: now,
		Wallets: map[string]WalletStats{
			"wallet1": {Wallet: "wallet1", UniqueMarkets: 3, FetchedAt: now.Add(-1 * time.Hour)}, // Older
			"wallet2": {Wallet: "wallet2", UniqueMarkets: 20, FetchedAt: now},                    // New
		},
	}

	imported := tracker.ImportCache(snapshot)

	// Only wallet2 should be imported (wallet1 is older)
	if imported != 1 {
		t.Errorf("expected 1 imported, got %d", imported)
	}
	if tracker.cache["wallet1"].UniqueMarkets != 5 {
		t.Error("expected existing wallet1 data to remain")
	}
	if tracker.cache["wallet2"].UniqueMarkets != 20 {
		t.Error("expected wallet2 to be imported")
	}
}

func TestImportCache_Nil(t *testing.T) {
	tracker := NewWalletTracker(nil, nil, 5*time.Minute, 0.20, 0.85, nil)

	imported := tracker.ImportCache(nil)

	if imported != 0 {
		t.Error("expected 0 imported for nil snapshot")
	}
}

func TestImportCache_Empty(t *testing.T) {
	tracker := NewWalletTracker(nil, nil, 5*time.Minute, 0.20, 0.85, nil)

	snapshot := &CacheSnapshot{
		Version: 1,
		Wallets: map[string]WalletStats{},
	}

	imported := tracker.ImportCache(snapshot)

	if imported != 0 {
		t.Error("expected 0 imported for empty snapshot")
	}
}

func TestImportCacheJSON(t *testing.T) {
	tracker := NewWalletTracker(zap.NewNop(), nil, 5*time.Minute, 0.20, 0.85, nil)

	now := time.Now()
	snapshot := CacheSnapshot{
		Version:   1,
		Timestamp: now,
		Wallets: map[string]WalletStats{
			"wallet1": {Wallet: "wallet1", UniqueMarkets: 10, FetchedAt: now},
		},
	}

	data, _ := json.Marshal(snapshot)

	imported, err := tracker.ImportCacheJSON(data)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if imported != 1 {
		t.Errorf("expected 1 imported, got %d", imported)
	}
}

func TestImportCacheJSON_InvalidJSON(t *testing.T) {
	tracker := NewWalletTracker(nil, nil, 5*time.Minute, 0.20, 0.85, nil)

	_, err := tracker.ImportCacheJSON([]byte("invalid json"))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestWalletStatsFields(t *testing.T) {
	stats := WalletStats{
		Wallet:        "0x123",
		UniqueMarkets: 10,
		TotalTrades:   50,
		WinCount:      8,
		LossCount:     2,
		WinRate:       0.8,
		FetchedAt:     time.Now(),
	}

	if stats.Wallet != "0x123" {
		t.Error("unexpected wallet")
	}
	if stats.TotalTrades != 50 {
		t.Error("unexpected total trades")
	}
}

func TestCacheSnapshotFields(t *testing.T) {
	now := time.Now()
	snapshot := CacheSnapshot{
		Version:   1,
		Timestamp: now,
		Wallets: map[string]WalletStats{
			"w1": {Wallet: "w1"},
		},
	}

	if snapshot.Version != 1 {
		t.Error("unexpected version")
	}
	if len(snapshot.Wallets) != 1 {
		t.Error("unexpected wallets count")
	}
}
