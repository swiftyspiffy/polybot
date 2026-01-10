package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"polybot/clients"
	"polybot/clients/gist"
	"polybot/clients/polymarketapi"
	"polybot/config"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestNewRunner(t *testing.T) {
	cfg := &config.Config{
		Polymarket: config.PolymarketConfig{
			GammaAPIURL: "http://example.com",
			DataAPIURL:  "http://example.com",
		},
		Gist: config.GistConfig{
			Token: "",
		},
	}

	clts := &clients.Clients{
		Logger:     zap.NewNop(),
		Polymarket: polymarketapi.NewPolymarketApiClient(nil, cfg),
		Gist:       gist.NewClient(nil, cfg),
	}

	liveConfig := config.NewLiveConfig(cfg)
	runner := NewRunner(clts, liveConfig, nil, nil)

	if runner.clients != clts {
		t.Error("unexpected clients")
	}
	if runner.liveConfig != liveConfig {
		t.Error("unexpected liveConfig")
	}
}

func TestRefreshTopMarkets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/markets" {
			markets := []polymarketapi.GammaMarket{
				{
					ID:           "1",
					ConditionID:  "cond1",
					Active:       true,
					Closed:       false,
					Volume24hr:   1000,
					ClobTokenIDs: json.RawMessage(`["token1a", "token1b"]`),
				},
				{
					ID:           "2",
					ConditionID:  "cond2",
					Active:       true,
					Closed:       false,
					Volume24hr:   500,
					ClobTokenIDs: json.RawMessage(`["token2a", "token2b"]`),
				},
				{
					ID:          "3",
					ConditionID: "", // Empty condition ID - should be skipped
					Active:      true,
					Closed:      false,
				},
				{
					ID:          "4",
					ConditionID: "cond4",
					Active:      false, // Inactive - should be skipped
					Closed:      false,
				},
				{
					ID:          "5",
					ConditionID: "cond5",
					Active:      true,
					Closed:      true, // Closed - should be skipped
				},
			}
			json.NewEncoder(w).Encode(markets)
		}
	}))
	defer server.Close()

	cfg := &config.Config{
		Polymarket: config.PolymarketConfig{
			GammaAPIURL: server.URL,
			DataAPIURL:  server.URL,
		},
		Gist: config.GistConfig{Token: ""},
		Cache: config.CacheConfig{
			WalletCacheTTL: 5 * time.Minute,
			SaveInterval:   5 * time.Minute,
		},
		TradeMonitor: config.TradeMonitorConfig{
			PollInterval:     10 * time.Second,
			MinNotional:      1000,
			MaxMarketsForLow: 10,
		},
	}

	clts := &clients.Clients{
		Logger:     zap.NewNop(),
		Polymarket: polymarketapi.NewPolymarketApiClient(zap.NewNop(), cfg),
		Gist:       gist.NewClient(zap.NewNop(), cfg),
	}

	runner := NewRunner(clts, config.NewLiveConfig(cfg), nil, nil)

	// Initialize components needed for refreshTopMarkets
	runner.walletTracker = NewWalletTracker(zap.NewNop(), clts.Polymarket, cfg.Cache.WalletCacheTTL, 0.20, 0.85, nil)
	runner.tradeMonitor = NewTradeMonitor(
		zap.NewNop(),
		clts.Polymarket,
		runner.walletTracker,
		nil, // No contrarian cache
		nil, // No copy tracker
		nil, // No notifier
		TradeMonitorConfig{
			PollInterval:     cfg.TradeMonitor.PollInterval,
			MinNotional:      cfg.TradeMonitor.MinNotional,
			MaxMarketsForLow: cfg.TradeMonitor.MaxMarketsForLow,
		},
	)

	err := runner.refreshTopMarkets(context.Background(), 10)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Check that trade monitor has correct markets (only cond1 and cond2)
	runner.tradeMonitor.mu.RLock()
	markets := runner.tradeMonitor.markets
	runner.tradeMonitor.mu.RUnlock()

	if len(markets) != 2 {
		t.Errorf("expected 2 markets, got %d", len(markets))
	}
}

func TestRefreshTopMarkets_NoActiveMarkets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return all inactive/closed markets (with token IDs so they'd pass that check)
		markets := []polymarketapi.GammaMarket{
			{ID: "1", ConditionID: "cond1", Active: false, Closed: false, ClobTokenIDs: json.RawMessage(`["token1"]`)},
			{ID: "2", ConditionID: "cond2", Active: true, Closed: true, ClobTokenIDs: json.RawMessage(`["token2"]`)},
		}
		json.NewEncoder(w).Encode(markets)
	}))
	defer server.Close()

	cfg := &config.Config{
		Polymarket: config.PolymarketConfig{
			GammaAPIURL: server.URL,
			DataAPIURL:  server.URL,
		},
		Gist: config.GistConfig{Token: ""},
		Cache: config.CacheConfig{
			WalletCacheTTL: 5 * time.Minute,
		},
		TradeMonitor: config.TradeMonitorConfig{
			PollInterval:     10 * time.Second,
			MinNotional:      1000,
			MaxMarketsForLow: 10,
		},
	}

	clts := &clients.Clients{
		Logger:     zap.NewNop(),
		Polymarket: polymarketapi.NewPolymarketApiClient(zap.NewNop(), cfg),
		Gist:       gist.NewClient(zap.NewNop(), cfg),
	}

	runner := NewRunner(clts, config.NewLiveConfig(cfg), nil, nil)
	runner.walletTracker = NewWalletTracker(zap.NewNop(), clts.Polymarket, cfg.Cache.WalletCacheTTL, 0.20, 0.85, nil)
	runner.tradeMonitor = NewTradeMonitor(
		zap.NewNop(),
		clts.Polymarket,
		runner.walletTracker,
		nil, // No contrarian cache
		nil, // No copy tracker
		nil, // No notifier
		TradeMonitorConfig{},
	)

	err := runner.refreshTopMarkets(context.Background(), 10)
	if err == nil {
		t.Error("expected error for no active markets")
	}
}

func TestRefreshTopMarkets_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	cfg := &config.Config{
		Polymarket: config.PolymarketConfig{
			GammaAPIURL: server.URL,
			DataAPIURL:  server.URL,
		},
		Gist:  config.GistConfig{Token: ""},
		Cache: config.CacheConfig{WalletCacheTTL: 5 * time.Minute},
	}

	clts := &clients.Clients{
		Logger:     zap.NewNop(),
		Polymarket: polymarketapi.NewPolymarketApiClient(zap.NewNop(), cfg),
		Gist:       gist.NewClient(zap.NewNop(), cfg),
	}

	runner := NewRunner(clts, config.NewLiveConfig(cfg), nil, nil)
	runner.walletTracker = NewWalletTracker(zap.NewNop(), clts.Polymarket, cfg.Cache.WalletCacheTTL, 0.20, 0.85, nil)
	runner.tradeMonitor = NewTradeMonitor(zap.NewNop(), clts.Polymarket, runner.walletTracker, nil, nil, nil, TradeMonitorConfig{})

	err := runner.refreshTopMarkets(context.Background(), 10)
	if err == nil {
		t.Error("expected error on API failure")
	}
}

func TestRunMarketRefresher_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		markets := []polymarketapi.GammaMarket{
			{ID: "1", ConditionID: "cond1", Active: true, Closed: false, ClobTokenIDs: json.RawMessage(`["token1"]`)},
		}
		json.NewEncoder(w).Encode(markets)
	}))
	defer server.Close()

	cfg := &config.Config{
		Polymarket: config.PolymarketConfig{
			GammaAPIURL: server.URL,
			DataAPIURL:  server.URL,
		},
		Gist:  config.GistConfig{Token: ""},
		Cache: config.CacheConfig{WalletCacheTTL: 5 * time.Minute},
	}

	clts := &clients.Clients{
		Logger:     zap.NewNop(),
		Polymarket: polymarketapi.NewPolymarketApiClient(zap.NewNop(), cfg),
		Gist:       gist.NewClient(zap.NewNop(), cfg),
	}

	runner := NewRunner(clts, config.NewLiveConfig(cfg), nil, nil)
	runner.walletTracker = NewWalletTracker(zap.NewNop(), clts.Polymarket, cfg.Cache.WalletCacheTTL, 0.20, 0.85, nil)
	runner.tradeMonitor = NewTradeMonitor(zap.NewNop(), clts.Polymarket, runner.walletTracker, nil, nil, nil, TradeMonitorConfig{})

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		runner.runMarketRefresher(ctx, 10, 1*time.Hour)
		close(done)
	}()

	// Give it a moment
	time.Sleep(10 * time.Millisecond)

	cancel()

	select {
	case <-done:
		// Good
	case <-time.After(500 * time.Millisecond):
		t.Error("runMarketRefresher should stop when context is cancelled")
	}
}

func TestRunner_RunContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/markets" {
			markets := []polymarketapi.GammaMarket{
				{ID: "1", ConditionID: "cond1", Active: true, Closed: false, ClobTokenIDs: json.RawMessage(`["token1"]`)},
			}
			json.NewEncoder(w).Encode(markets)
		} else if r.URL.Path == "/trades" {
			json.NewEncoder(w).Encode([]polymarketapi.Trade{})
		}
	}))
	defer server.Close()

	cfg := &config.Config{
		IsProd: false,
		Polymarket: config.PolymarketConfig{
			GammaAPIURL: server.URL,
			DataAPIURL:  server.URL,
		},
		Gist: config.GistConfig{Token: ""}, // Disabled
		Cache: config.CacheConfig{
			WalletCacheTTL: 5 * time.Minute,
			SaveInterval:   1 * time.Hour,
		},
		TradeMonitor: config.TradeMonitorConfig{
			PollInterval:     1 * time.Hour, // Long interval
			MinNotional:      1000,
			MaxMarketsForLow: 10,
		},
		Markets: config.MarketsConfig{
			TopMarketsCount: 10,
			RefreshInterval: 1 * time.Hour, // Long interval
		},
	}

	clts := &clients.Clients{
		Logger:     zap.NewNop(),
		Polymarket: polymarketapi.NewPolymarketApiClient(zap.NewNop(), cfg),
		Gist:       gist.NewClient(zap.NewNop(), cfg),
	}

	runner := NewRunner(clts, config.NewLiveConfig(cfg), nil, nil)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		runner.Run(ctx)
		close(done)
	}()

	// Give it time to start
	time.Sleep(100 * time.Millisecond)

	cancel()

	select {
	case <-done:
		// Good
	case <-time.After(2 * time.Second):
		t.Error("Run should stop when context is cancelled")
	}
}
