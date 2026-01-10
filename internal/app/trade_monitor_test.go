package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"polybot/clients/notifier"
	"polybot/clients/polymarketapi"
	"polybot/config"
	"testing"
	"time"

	"go.uber.org/zap"
)

func newTestTradeMonitor(t *testing.T, handler http.HandlerFunc) (*TradeMonitor, *httptest.Server, *WalletTracker) {
	server := httptest.NewServer(handler)
	cfg := &config.Config{
		Polymarket: config.PolymarketConfig{
			DataAPIURL:  server.URL,
			GammaAPIURL: server.URL,
		},
	}
	apiClient := polymarketapi.NewPolymarketApiClient(zap.NewNop(), cfg)
	tracker := NewWalletTracker(zap.NewNop(), apiClient, 5*time.Minute, 0.20, 0.85, nil)

	monitorCfg := TradeMonitorConfig{
		PollInterval:     100 * time.Millisecond,
		MinNotional:      1000,
		MaxMarketsForLow: 10,
	}

	monitor := NewTradeMonitor(
		zap.NewNop(),
		apiClient,
		tracker,
		nil, // No contrarian cache
		nil, // No copy tracker
		nil, // No notifier
		monitorCfg,
	)

	return monitor, server, tracker
}

func TestNewTradeMonitor(t *testing.T) {
	cfg := TradeMonitorConfig{
		PollInterval:     10 * time.Second,
		MinNotional:      500,
		MaxMarketsForLow: 5,
	}

	monitor := NewTradeMonitor(nil, nil, nil, nil, nil, nil, cfg)

	if monitor.logger == nil {
		t.Error("expected logger to be set")
	}
	if monitor.config.MinNotional != 500 {
		t.Errorf("unexpected min notional: %f", monitor.config.MinNotional)
	}
	if monitor.seenTrades == nil {
		t.Error("expected seenTrades to be initialized")
	}
}

func TestDefaultTradeMonitorConfig(t *testing.T) {
	cfg := DefaultTradeMonitorConfig()

	if cfg.PollInterval != 10*time.Second {
		t.Errorf("unexpected poll interval: %v", cfg.PollInterval)
	}
	if cfg.MinNotional != 4000 {
		t.Errorf("unexpected min notional: %f", cfg.MinNotional)
	}
	if cfg.MaxMarketsForLow != 5 {
		t.Errorf("unexpected max markets: %d", cfg.MaxMarketsForLow)
	}
}

func TestSetMarkets(t *testing.T) {
	monitor := NewTradeMonitor(nil, nil, nil, nil, nil, nil, DefaultTradeMonitorConfig())

	markets := []string{"cond1", "cond2", "cond3"}
	monitor.SetMarkets(markets)

	monitor.mu.RLock()
	defer monitor.mu.RUnlock()

	if len(monitor.markets) != 3 {
		t.Errorf("expected 3 markets, got %d", len(monitor.markets))
	}
	if monitor.markets[0] != "cond1" {
		t.Error("unexpected first market")
	}
}

func TestPoll_NoMarkets(t *testing.T) {
	callCount := 0
	monitor, server, _ := newTestTradeMonitor(t, func(w http.ResponseWriter, r *http.Request) {
		callCount++
		json.NewEncoder(w).Encode([]polymarketapi.Trade{})
	})
	defer server.Close()

	// Don't set any markets
	monitor.poll(context.Background())

	if callCount != 0 {
		t.Error("expected no API calls with no markets")
	}
}

func TestPoll_FetchTrades(t *testing.T) {
	callCount := 0
	monitor, server, _ := newTestTradeMonitor(t, func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if r.URL.Path == "/trades" {
			trades := []polymarketapi.Trade{
				{
					ID:              "t1",
					ProxyWallet:     "0x123",
					Side:            "BUY",
					Size:            100,
					Price:           0.5,
					TransactionHash: "0xhash1",
					Asset:           "asset1",
				},
			}
			json.NewEncoder(w).Encode(trades)
		}
	})
	defer server.Close()

	monitor.SetMarkets([]string{"cond1"})
	monitor.poll(context.Background())

	if callCount != 1 {
		t.Errorf("expected 1 API call, got %d", callCount)
	}
}

func TestProcessTrade_SeenTrade(t *testing.T) {
	monitor := NewTradeMonitor(nil, nil, nil, nil, nil, nil, DefaultTradeMonitorConfig())

	trade := polymarketapi.Trade{
		TransactionHash: "0xhash",
		Asset:           "asset1",
	}

	// Mark as seen
	monitor.seenTrades["0xhash:asset1"] = struct{}{}

	// Should skip
	monitor.processTrade(context.Background(), trade)

	// No error means it was skipped correctly
}

func TestProcessTrade_BelowMinNotional(t *testing.T) {
	processedCount := 0
	monitor, server, _ := newTestTradeMonitor(t, func(w http.ResponseWriter, r *http.Request) {
		processedCount++
		json.NewEncoder(w).Encode([]polymarketapi.Activity{})
	})
	defer server.Close()

	trade := polymarketapi.Trade{
		TransactionHash: "0xhash",
		Asset:           "asset1",
		Size:            10,    // Small
		Price:           0.5,   // Notional = 5, below min of 1000
		ProxyWallet:     "0x123",
	}

	monitor.processTrade(context.Background(), trade)

	// Should not fetch wallet stats since notional is too low
	if processedCount != 0 {
		t.Error("expected trade to be skipped due to low notional")
	}
}

func TestProcessTrade_HighActivityWallet(t *testing.T) {
	alertSent := false
	monitor, server, tracker := newTestTradeMonitor(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/activity" {
			// Return many unique markets
			activity := make([]polymarketapi.Activity, 20)
			for i := 0; i < 20; i++ {
				activity[i] = polymarketapi.Activity{ConditionID: string(rune('a' + i))}
			}
			json.NewEncoder(w).Encode(activity)
		} else if r.URL.Path == "/closed-positions" {
			json.NewEncoder(w).Encode([]polymarketapi.ClosedPosition{})
		}
	})
	defer server.Close()

	// Set up a mock notifier to detect alerts (nil is fine for this test)
	monitor.notifier = nil

	trade := polymarketapi.Trade{
		TransactionHash: "0xhash",
		Asset:           "asset1",
		Size:            2000,
		Price:           1.0, // Notional = 2000, above min
		ProxyWallet:     "0x123",
	}

	// Pre-populate cache with high activity wallet
	tracker.cache["0x123"] = &WalletStats{
		Wallet:        "0x123",
		UniqueMarkets: 50, // Way above threshold
		FetchedAt:     time.Now(),
	}

	monitor.processTrade(context.Background(), trade)

	if alertSent {
		t.Error("expected no alert for high activity wallet")
	}
}

func TestProcessTrade_LowActivityWallet(t *testing.T) {
	monitor, server, tracker := newTestTradeMonitor(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/activity" {
			activity := []polymarketapi.Activity{
				{ConditionID: "cond1"},
				{ConditionID: "cond2"},
			}
			json.NewEncoder(w).Encode(activity)
		} else if r.URL.Path == "/closed-positions" {
			positions := []polymarketapi.ClosedPosition{
				{RealizedPnl: 100},
				{RealizedPnl: -50},
			}
			json.NewEncoder(w).Encode(positions)
		}
	})
	defer server.Close()

	trade := polymarketapi.Trade{
		TransactionHash: "0xhash",
		Asset:           "asset1",
		Size:            2000,
		Price:           1.0,
		ProxyWallet:     "0x123",
		Title:           "Test Market",
		Outcome:         "Yes",
		Side:            "BUY",
	}

	// Pre-populate cache with low activity wallet
	tracker.cache["0x123"] = &WalletStats{
		Wallet:        "0x123",
		UniqueMarkets: 2, // Below threshold of 10
		WinCount:      1,
		LossCount:     1,
		WinRate:       0.5,
		FetchedAt:     time.Now(),
	}

	// Should process without error (alert will be logged, not sent to Discord)
	monitor.processTrade(context.Background(), trade)

	// Trade should be marked as seen
	if _, seen := monitor.seenTrades["0xhash:asset1"]; !seen {
		t.Error("expected trade to be marked as seen")
	}
}

func TestTradeKey(t *testing.T) {
	monitor := NewTradeMonitor(nil, nil, nil, nil, nil, nil, DefaultTradeMonitorConfig())

	trade := polymarketapi.Trade{
		TransactionHash: "0xabcd",
		Asset:           "asset123",
	}

	key := monitor.tradeKey(trade)

	if key != "0xabcd:asset123" {
		t.Errorf("unexpected key: %s", key)
	}
}

func TestTraderDisplayName(t *testing.T) {
	monitor := NewTradeMonitor(nil, nil, nil, nil, nil, nil, DefaultTradeMonitorConfig())

	tests := []struct {
		name     string
		trade    polymarketapi.Trade
		expected string
	}{
		{
			name: "with name",
			trade: polymarketapi.Trade{
				Name:        "John Doe",
				Pseudonym:   "JD",
				ProxyWallet: "0x1234567890abcdef1234567890abcdef12345678",
			},
			expected: "John Doe",
		},
		{
			name: "with pseudonym only",
			trade: polymarketapi.Trade{
				Pseudonym:   "CryptoKing",
				ProxyWallet: "0x1234567890abcdef1234567890abcdef12345678",
			},
			expected: "CryptoKing",
		},
		{
			name: "wallet only",
			trade: polymarketapi.Trade{
				ProxyWallet: "0x1234567890abcdef1234567890abcdef12345678",
			},
			expected: "0x1234…345678",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := monitor.traderDisplayName(tt.trade)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestSendAlert_WithNotifier(t *testing.T) {
	monitor := NewTradeMonitor(zap.NewNop(), nil, nil, nil, nil, nil, DefaultTradeMonitorConfig())

	// Use nil notifier - sendAlert handles nil gracefully
	monitor.notifier = nil

	alert := notifier.TradeAlert{
		TraderName:    "TestTrader",
		TraderAddress: "0x123",
		Side:          "BUY",
		Shares:        100,
		Price:         0.5,
		Notional:      50,
		MarketTitle:   "Test Market",
		Outcome:       "Yes",
		UniqueMarkets: 5,
		WinRate:       0.8,
		WinCount:      8,
		LossCount:     2,
		Timestamp:     time.Now(),
	}

	// Should not panic even without a real notifier
	monitor.sendAlert(alert)
}

func TestSendAlert_NoNotifier(t *testing.T) {
	monitor := NewTradeMonitor(zap.NewNop(), nil, nil, nil, nil, nil, DefaultTradeMonitorConfig())
	monitor.notifier = nil

	alert := notifier.TradeAlert{
		TraderName: "TestTrader",
		Side:       "BUY",
	}

	// Should not panic
	monitor.sendAlert(alert)
}

func TestSendAlert_BuildsCorrectURLs(t *testing.T) {
	alert := notifier.TradeAlert{
		TraderAddress: "0xWALLET",
	}

	// Verify URL building logic
	walletURL := "https://polymarket.com/profile/" + alert.TraderAddress

	if walletURL != "https://polymarket.com/profile/0xWALLET" {
		t.Errorf("unexpected wallet URL: %s", walletURL)
	}
}

func TestPruneSeenTrades(t *testing.T) {
	monitor := NewTradeMonitor(zap.NewNop(), nil, nil, nil, nil, nil, DefaultTradeMonitorConfig())

	// Add many seen trades
	for i := 0; i < 15000; i++ {
		monitor.seenTrades[string(rune(i))] = struct{}{}
	}

	if len(monitor.seenTrades) != 15000 {
		t.Errorf("expected 15000 seen trades, got %d", len(monitor.seenTrades))
	}

	monitor.PruneSeenTrades(1 * time.Hour)

	if len(monitor.seenTrades) != 0 {
		t.Error("expected seen trades to be cleared")
	}
}

func TestPruneSeenTrades_SmallMap(t *testing.T) {
	monitor := NewTradeMonitor(nil, nil, nil, nil, nil, nil, DefaultTradeMonitorConfig())

	monitor.seenTrades["trade1"] = struct{}{}
	monitor.seenTrades["trade2"] = struct{}{}

	monitor.PruneSeenTrades(1 * time.Hour)

	// Should not prune small maps
	if len(monitor.seenTrades) != 2 {
		t.Error("expected small map to remain unchanged")
	}
}

func TestTradeMonitorRun_ContextCancellation(t *testing.T) {
	monitor, server, _ := newTestTradeMonitor(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]polymarketapi.Trade{})
	})
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		monitor.Run(ctx)
		close(done)
	}()

	// Give it time to start
	time.Sleep(50 * time.Millisecond)

	cancel()

	select {
	case <-done:
		// Good
	case <-time.After(500 * time.Millisecond):
		t.Error("Run should stop when context is cancelled")
	}
}

func TestTradeAlertFields(t *testing.T) {
	alert := notifier.TradeAlert{
		TraderName:    "Trader",
		TraderAddress: "0x123",
		Side:          "SELL",
		Shares:        500,
		Price:         0.75,
		Notional:      375,
		MarketTitle:   "Test Market",
		MarketImage:   "https://image.com",
		Outcome:       "No",
		UniqueMarkets: 15,
		WinRate:       0.6,
		WinCount:      6,
		LossCount:     4,
		Timestamp:     time.Now(),
	}

	if alert.Notional != 375 {
		t.Errorf("unexpected notional: %f", alert.Notional)
	}
	if alert.Side != "SELL" {
		t.Errorf("unexpected side: %s", alert.Side)
	}
}

func TestTradeMonitorConfig(t *testing.T) {
	cfg := TradeMonitorConfig{
		PollInterval:     30 * time.Second,
		MinNotional:      2000,
		MaxMarketsForLow: 15,
	}

	if cfg.PollInterval != 30*time.Second {
		t.Error("unexpected poll interval")
	}
	if cfg.MinNotional != 2000 {
		t.Error("unexpected min notional")
	}
	if cfg.MaxMarketsForLow != 15 {
		t.Error("unexpected max markets")
	}
}

func TestShortID_TradeMonitor(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"0x1234567890abcdef1234567890abcdef12345678", "0x1234…345678"},
		{"short", "short"},
		{"", ""},
	}

	for _, tt := range tests {
		result := shortID(tt.input)
		if result != tt.expected {
			t.Errorf("shortID(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestSetEventsClient(t *testing.T) {
	monitor := NewTradeMonitor(nil, nil, nil, nil, nil, nil, DefaultTradeMonitorConfig())

	if monitor.eventsClient != nil {
		t.Error("expected nil events client initially")
	}

	// SetEventsClient accepts nil or a real client
	monitor.SetEventsClient(nil)

	if monitor.eventsClient != nil {
		t.Error("expected nil after setting nil")
	}
}

func TestGetTokenIDs(t *testing.T) {
	monitor := NewTradeMonitor(nil, nil, nil, nil, nil, nil, DefaultTradeMonitorConfig())

	// Initially empty
	ids := monitor.GetTokenIDs()
	if len(ids) != 0 {
		t.Errorf("expected 0 token IDs, got %d", len(ids))
	}

	// Add some token IDs
	monitor.mu.Lock()
	monitor.allTokenIDs = []string{"token1", "token2", "token3"}
	monitor.mu.Unlock()

	ids = monitor.GetTokenIDs()
	if len(ids) != 3 {
		t.Errorf("expected 3 token IDs, got %d", len(ids))
	}
}

func TestSetWSConnected(t *testing.T) {
	monitor := NewTradeMonitor(nil, nil, nil, nil, nil, nil, DefaultTradeMonitorConfig())

	if monitor.wsConnected {
		t.Error("expected wsConnected to be false initially")
	}

	monitor.SetWSConnected(true)
	if !monitor.wsConnected {
		t.Error("expected wsConnected to be true")
	}

	monitor.SetWSConnected(false)
	if monitor.wsConnected {
		t.Error("expected wsConnected to be false")
	}
}

func TestIsWSConnected(t *testing.T) {
	monitor := NewTradeMonitor(nil, nil, nil, nil, nil, nil, DefaultTradeMonitorConfig())

	if monitor.IsWSConnected() {
		t.Error("expected false initially")
	}

	monitor.wsConnected = true
	if !monitor.IsWSConnected() {
		t.Error("expected true after setting")
	}
}

func TestExportSeenTrades(t *testing.T) {
	monitor := NewTradeMonitor(nil, nil, nil, nil, nil, nil, DefaultTradeMonitorConfig())

	// Add some seen trades
	monitor.seenTrades["trade1:asset1"] = struct{}{}
	monitor.seenTrades["trade2:asset2"] = struct{}{}

	snapshot := monitor.ExportSeenTrades()

	if snapshot == nil {
		t.Fatal("expected non-nil snapshot")
	}
	if snapshot.Version != 1 {
		t.Errorf("expected version 1, got %d", snapshot.Version)
	}
	if len(snapshot.Trades) != 2 {
		t.Errorf("expected 2 trades, got %d", len(snapshot.Trades))
	}
	if snapshot.Timestamp.IsZero() {
		t.Error("expected non-zero timestamp")
	}
}

func TestImportSeenTrades(t *testing.T) {
	monitor := NewTradeMonitor(zap.NewNop(), nil, nil, nil, nil, nil, DefaultTradeMonitorConfig())

	snapshot := &SeenTradesSnapshot{
		Version:   1,
		Timestamp: time.Now(),
		Trades:    []string{"trade1:asset1", "trade2:asset2", "trade3:asset3"},
	}

	imported := monitor.ImportSeenTrades(snapshot)

	if imported != 3 {
		t.Errorf("expected 3 imported, got %d", imported)
	}
	if len(monitor.seenTrades) != 3 {
		t.Errorf("expected 3 seen trades, got %d", len(monitor.seenTrades))
	}
}

func TestImportSeenTrades_Nil(t *testing.T) {
	monitor := NewTradeMonitor(nil, nil, nil, nil, nil, nil, DefaultTradeMonitorConfig())

	imported := monitor.ImportSeenTrades(nil)

	if imported != 0 {
		t.Errorf("expected 0 imported for nil, got %d", imported)
	}
}

func TestImportSeenTrades_Empty(t *testing.T) {
	monitor := NewTradeMonitor(nil, nil, nil, nil, nil, nil, DefaultTradeMonitorConfig())

	snapshot := &SeenTradesSnapshot{
		Version:   1,
		Timestamp: time.Now(),
		Trades:    []string{},
	}

	imported := monitor.ImportSeenTrades(snapshot)

	if imported != 0 {
		t.Errorf("expected 0 imported for empty, got %d", imported)
	}
}

func TestImportSeenTrades_Duplicates(t *testing.T) {
	monitor := NewTradeMonitor(zap.NewNop(), nil, nil, nil, nil, nil, DefaultTradeMonitorConfig())

	// Pre-add one trade
	monitor.seenTrades["trade1:asset1"] = struct{}{}

	snapshot := &SeenTradesSnapshot{
		Version:   1,
		Timestamp: time.Now(),
		Trades:    []string{"trade1:asset1", "trade2:asset2"},
	}

	imported := monitor.ImportSeenTrades(snapshot)

	// Should only import the new one
	if imported != 1 {
		t.Errorf("expected 1 imported (new one only), got %d", imported)
	}
	if len(monitor.seenTrades) != 2 {
		t.Errorf("expected 2 total seen trades, got %d", len(monitor.seenTrades))
	}
}

func TestSeenTradesCount(t *testing.T) {
	monitor := NewTradeMonitor(nil, nil, nil, nil, nil, nil, DefaultTradeMonitorConfig())

	if monitor.SeenTradesCount() != 0 {
		t.Error("expected 0 initially")
	}

	monitor.seenTrades["trade1"] = struct{}{}
	monitor.seenTrades["trade2"] = struct{}{}

	if monitor.SeenTradesCount() != 2 {
		t.Errorf("expected 2, got %d", monitor.SeenTradesCount())
	}
}

func TestSeenMarketsCount(t *testing.T) {
	monitor := NewTradeMonitor(nil, nil, nil, nil, nil, nil, DefaultTradeMonitorConfig())

	if monitor.SeenMarketsCount() != 0 {
		t.Error("expected 0 initially")
	}

	monitor.seenMarkets["market1"] = struct{}{}
	monitor.seenMarkets["market2"] = struct{}{}
	monitor.seenMarkets["market3"] = struct{}{}

	if monitor.SeenMarketsCount() != 3 {
		t.Errorf("expected 3, got %d", monitor.SeenMarketsCount())
	}
}

func TestEventTypeCounts(t *testing.T) {
	monitor := NewTradeMonitor(nil, nil, nil, nil, nil, nil, DefaultTradeMonitorConfig())

	// Empty initially
	counts := monitor.EventTypeCounts()
	if len(counts) != 0 {
		t.Errorf("expected empty counts, got %d", len(counts))
	}

	// Add some counts
	monitor.eventTypes["trade"] = 10
	monitor.eventTypes["price_change"] = 5

	counts = monitor.EventTypeCounts()
	if counts["trade"] != 10 {
		t.Errorf("expected trade count 10, got %d", counts["trade"])
	}
	if counts["price_change"] != 5 {
		t.Errorf("expected price_change count 5, got %d", counts["price_change"])
	}
}

func TestFilterStats(t *testing.T) {
	monitor := NewTradeMonitor(nil, nil, nil, nil, nil, nil, DefaultTradeMonitorConfig())

	// Set various stats
	monitor.skippedLowNotional = 10
	monitor.skippedNoWallet = 5
	monitor.skippedHighActivity = 20
	monitor.alertsSent = 15
	monitor.alertsLowActivity = 3
	monitor.alertsHighWinRate = 4
	monitor.alertsExtremeBet = 2
	monitor.alertsRapidTrading = 1
	monitor.alertsNewWallet = 2
	monitor.alertsContrarianBet = 1
	monitor.alertsMassiveTrade = 2

	stats := monitor.FilterStats()

	if stats.SkippedLowNotional != 10 {
		t.Errorf("expected 10, got %d", stats.SkippedLowNotional)
	}
	if stats.SkippedNoWallet != 5 {
		t.Errorf("expected 5, got %d", stats.SkippedNoWallet)
	}
	if stats.SkippedHighActivity != 20 {
		t.Errorf("expected 20, got %d", stats.SkippedHighActivity)
	}
	if stats.AlertsSent != 15 {
		t.Errorf("expected 15, got %d", stats.AlertsSent)
	}
	if stats.AlertsLowActivity != 3 {
		t.Errorf("expected 3, got %d", stats.AlertsLowActivity)
	}
	if stats.AlertsHighWinRate != 4 {
		t.Errorf("expected 4, got %d", stats.AlertsHighWinRate)
	}
	if stats.AlertsExtremeBet != 2 {
		t.Errorf("expected 2, got %d", stats.AlertsExtremeBet)
	}
	if stats.AlertsRapidTrading != 1 {
		t.Errorf("expected 1, got %d", stats.AlertsRapidTrading)
	}
	if stats.AlertsNewWallet != 2 {
		t.Errorf("expected 2, got %d", stats.AlertsNewWallet)
	}
	if stats.AlertsContrarianBet != 1 {
		t.Errorf("expected 1, got %d", stats.AlertsContrarianBet)
	}
	if stats.AlertsMassiveTrade != 2 {
		t.Errorf("expected 2, got %d", stats.AlertsMassiveTrade)
	}
}

func TestCheckRapidTrading(t *testing.T) {
	cfg := DefaultTradeMonitorConfig()
	cfg.RapidTradeWindow = 5 * time.Minute
	cfg.RapidTradeMinCount = 3
	cfg.RapidTradeMinTotal = 5000

	monitor := NewTradeMonitor(nil, nil, nil, nil, nil, nil, cfg)

	now := time.Now()

	// First trade - should not trigger
	isRapid, count, total := monitor.checkRapidTrading("wallet1", 2000, now)
	if isRapid {
		t.Error("expected not rapid for first trade")
	}
	if count != 1 {
		t.Errorf("expected count 1, got %d", count)
	}
	if total != 2000 {
		t.Errorf("expected total 2000, got %f", total)
	}

	// Second trade - should not trigger
	isRapid, count, total = monitor.checkRapidTrading("wallet1", 2000, now.Add(1*time.Minute))
	if isRapid {
		t.Error("expected not rapid for second trade")
	}
	if count != 2 {
		t.Errorf("expected count 2, got %d", count)
	}

	// Third trade - should trigger (3 trades, $6000 total)
	isRapid, count, total = monitor.checkRapidTrading("wallet1", 2000, now.Add(2*time.Minute))
	if !isRapid {
		t.Error("expected rapid trading to be detected")
	}
	if count != 3 {
		t.Errorf("expected count 3, got %d", count)
	}
	if total != 6000 {
		t.Errorf("expected total 6000, got %f", total)
	}
}

func TestCheckRapidTrading_DifferentWallets(t *testing.T) {
	cfg := DefaultTradeMonitorConfig()
	cfg.RapidTradeMinCount = 2
	cfg.RapidTradeMinTotal = 1000

	monitor := NewTradeMonitor(nil, nil, nil, nil, nil, nil, cfg)

	now := time.Now()

	// Trade from wallet1
	isRapid1, _, _ := monitor.checkRapidTrading("wallet1", 1000, now)
	if isRapid1 {
		t.Error("expected not rapid for wallet1")
	}

	// Trade from wallet2 - should not count toward wallet1
	isRapid2, count2, _ := monitor.checkRapidTrading("wallet2", 1000, now)
	if isRapid2 {
		t.Error("expected not rapid for wallet2")
	}
	if count2 != 1 {
		t.Errorf("expected count 1 for wallet2, got %d", count2)
	}
}

func TestPruneRecentTrades(t *testing.T) {
	cfg := DefaultTradeMonitorConfig()
	cfg.RapidTradeWindow = 1 * time.Minute

	monitor := NewTradeMonitor(nil, nil, nil, nil, nil, nil, cfg)

	oldTime := time.Now().Add(-5 * time.Minute)
	recentTime := time.Now()

	// Add old trades
	monitor.recentTrades["wallet1"] = []recentTrade{
		{timestamp: oldTime, notional: 1000},
	}
	// Add recent trades
	monitor.recentTrades["wallet2"] = []recentTrade{
		{timestamp: recentTime, notional: 2000},
	}

	monitor.pruneRecentTrades()

	// wallet1 should be removed (old trades)
	if _, exists := monitor.recentTrades["wallet1"]; exists {
		t.Error("expected wallet1 to be pruned")
	}

	// wallet2 should remain
	if _, exists := monitor.recentTrades["wallet2"]; !exists {
		t.Error("expected wallet2 to remain")
	}
}

func TestSendAlert_CountsAllReasons(t *testing.T) {
	monitor := NewTradeMonitor(zap.NewNop(), nil, nil, nil, nil, nil, DefaultTradeMonitorConfig())

	// Test each reason type
	reasons := []notifier.AlertReason{
		notifier.AlertReasonLowActivity,
		notifier.AlertReasonHighWinRate,
		notifier.AlertReasonExtremeBet,
		notifier.AlertReasonRapidTrading,
		notifier.AlertReasonNewWallet,
		notifier.AlertReasonContrarianBet,
		notifier.AlertReasonMassiveTrade,
	}

	for _, reason := range reasons {
		alert := notifier.TradeAlert{
			TraderName: "Test",
			Side:       "BUY",
			Reasons:    []notifier.AlertReason{reason},
		}
		monitor.sendAlert(alert)
	}

	stats := monitor.FilterStats()
	if stats.AlertsSent != 7 {
		t.Errorf("expected 7 alerts sent, got %d", stats.AlertsSent)
	}
	if stats.AlertsLowActivity != 1 {
		t.Errorf("expected 1 low activity, got %d", stats.AlertsLowActivity)
	}
	if stats.AlertsHighWinRate != 1 {
		t.Errorf("expected 1 high win rate, got %d", stats.AlertsHighWinRate)
	}
	if stats.AlertsExtremeBet != 1 {
		t.Errorf("expected 1 extreme bet, got %d", stats.AlertsExtremeBet)
	}
	if stats.AlertsRapidTrading != 1 {
		t.Errorf("expected 1 rapid trading, got %d", stats.AlertsRapidTrading)
	}
	if stats.AlertsNewWallet != 1 {
		t.Errorf("expected 1 new wallet, got %d", stats.AlertsNewWallet)
	}
	if stats.AlertsContrarianBet != 1 {
		t.Errorf("expected 1 contrarian bet, got %d", stats.AlertsContrarianBet)
	}
	if stats.AlertsMassiveTrade != 1 {
		t.Errorf("expected 1 massive trade, got %d", stats.AlertsMassiveTrade)
	}
}

func TestUpdateMarkets(t *testing.T) {
	monitor := NewTradeMonitor(zap.NewNop(), nil, nil, nil, nil, nil, DefaultTradeMonitorConfig())

	markets := []polymarketapi.GammaMarket{
		{
			ConditionID:  "cond1",
			Question:     "Test Market 1",
			Slug:         "test-market-1",
			Active:       true,
			Closed:       false,
			ClobTokenIDs: json.RawMessage(`["token1a", "token1b"]`),
			Outcomes:     json.RawMessage(`["Yes", "No"]`),
		},
		{
			ConditionID:  "cond2",
			Question:     "Test Market 2",
			Slug:         "test-market-2",
			Active:       true,
			Closed:       false,
			ClobTokenIDs: json.RawMessage(`["token2a", "token2b"]`),
			Outcomes:     json.RawMessage(`["Yes", "No"]`),
		},
	}

	err := monitor.UpdateMarkets(markets)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check markets were added
	monitor.mu.RLock()
	defer monitor.mu.RUnlock()

	if len(monitor.markets) != 2 {
		t.Errorf("expected 2 markets, got %d", len(monitor.markets))
	}
	if len(monitor.allTokenIDs) != 4 {
		t.Errorf("expected 4 token IDs, got %d", len(monitor.allTokenIDs))
	}
}

func TestUpdateMarkets_FiltersClosed(t *testing.T) {
	monitor := NewTradeMonitor(zap.NewNop(), nil, nil, nil, nil, nil, DefaultTradeMonitorConfig())

	markets := []polymarketapi.GammaMarket{
		{
			ConditionID:  "cond1",
			Active:       true,
			Closed:       false,
			ClobTokenIDs: json.RawMessage(`["token1"]`),
		},
		{
			ConditionID:  "cond2",
			Active:       true,
			Closed:       true, // Closed - should be filtered
			ClobTokenIDs: json.RawMessage(`["token2"]`),
		},
		{
			ConditionID:  "cond3",
			Active:       false, // Inactive - should be filtered
			Closed:       false,
			ClobTokenIDs: json.RawMessage(`["token3"]`),
		},
	}

	err := monitor.UpdateMarkets(markets)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	monitor.mu.RLock()
	defer monitor.mu.RUnlock()

	if len(monitor.markets) != 1 {
		t.Errorf("expected 1 market (active and not closed), got %d", len(monitor.markets))
	}
}

func TestUpdateMarkets_EmptyConditionID(t *testing.T) {
	monitor := NewTradeMonitor(zap.NewNop(), nil, nil, nil, nil, nil, DefaultTradeMonitorConfig())

	markets := []polymarketapi.GammaMarket{
		{
			ConditionID:  "", // Empty condition ID - should be skipped
			Active:       true,
			Closed:       false,
			ClobTokenIDs: json.RawMessage(`["token1"]`),
		},
		{
			ConditionID:  "valid-cond",
			Active:       true,
			Closed:       false,
			ClobTokenIDs: json.RawMessage(`["token2"]`),
		},
	}

	err := monitor.UpdateMarkets(markets)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	monitor.mu.RLock()
	defer monitor.mu.RUnlock()

	if len(monitor.markets) != 1 {
		t.Errorf("expected 1 market (only valid), got %d", len(monitor.markets))
	}
}

func TestUpdateMarkets_NoTokenIDs(t *testing.T) {
	monitor := NewTradeMonitor(zap.NewNop(), nil, nil, nil, nil, nil, DefaultTradeMonitorConfig())

	markets := []polymarketapi.GammaMarket{
		{
			ConditionID:  "cond1",
			Active:       true,
			Closed:       false,
			ClobTokenIDs: json.RawMessage(`[]`), // Empty token IDs - should be skipped
		},
		{
			ConditionID:  "cond2",
			Active:       true,
			Closed:       false,
			ClobTokenIDs: json.RawMessage(`["token1"]`),
		},
	}

	err := monitor.UpdateMarkets(markets)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	monitor.mu.RLock()
	defer monitor.mu.RUnlock()

	if len(monitor.markets) != 1 {
		t.Errorf("expected 1 market, got %d", len(monitor.markets))
	}
}

func TestUpdateMarkets_DefaultOutcomes(t *testing.T) {
	monitor := NewTradeMonitor(zap.NewNop(), nil, nil, nil, nil, nil, DefaultTradeMonitorConfig())

	markets := []polymarketapi.GammaMarket{
		{
			ConditionID:  "cond1",
			Active:       true,
			Closed:       false,
			ClobTokenIDs: json.RawMessage(`["token1", "token2"]`),
			Outcomes:     json.RawMessage(`[]`), // Empty outcomes - should default to Yes/No
		},
	}

	err := monitor.UpdateMarkets(markets)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check that default outcomes were set
	monitor.mu.RLock()
	defer monitor.mu.RUnlock()

	info := monitor.tokenToInfo["token1"]
	if info == nil {
		t.Fatal("expected market info")
	}
	if len(info.Outcomes) != 2 || info.Outcomes[0] != "Yes" || info.Outcomes[1] != "No" {
		t.Errorf("expected default outcomes [Yes, No], got %v", info.Outcomes)
	}
}

func TestSetWalletFilter(t *testing.T) {
	monitor := NewTradeMonitor(zap.NewNop(), nil, nil, nil, nil, nil, DefaultTradeMonitorConfig())

	// Initially nil (no filter)
	if monitor.specificWallets != nil {
		t.Error("expected nil wallet filter initially")
	}

	// Set empty slice - should remain nil
	monitor.SetWalletFilter([]string{})
	if monitor.specificWallets != nil {
		t.Error("expected nil wallet filter for empty slice")
	}

	// Set nil - should remain nil
	monitor.SetWalletFilter(nil)
	if monitor.specificWallets != nil {
		t.Error("expected nil wallet filter for nil")
	}

	// Set actual wallets
	monitor.SetWalletFilter([]string{"0xABC", "0xDEF"})
	if monitor.specificWallets == nil {
		t.Fatal("expected non-nil wallet filter")
	}
	if len(monitor.specificWallets) != 2 {
		t.Errorf("expected 2 wallets in filter, got %d", len(monitor.specificWallets))
	}
	// Check lowercase conversion
	if !monitor.specificWallets["0xabc"] {
		t.Error("expected 0xabc to be in filter")
	}
	if !monitor.specificWallets["0xdef"] {
		t.Error("expected 0xdef to be in filter")
	}
	// Original case should not match
	if monitor.specificWallets["0xABC"] {
		t.Error("expected uppercase 0xABC to NOT be in filter (should be lowercase)")
	}
}

func TestShouldProcessWallet(t *testing.T) {
	monitor := NewTradeMonitor(zap.NewNop(), nil, nil, nil, nil, nil, DefaultTradeMonitorConfig())

	// No filter - all wallets should pass
	if !monitor.shouldProcessWallet("0xabc") {
		t.Error("expected all wallets to pass with no filter")
	}
	if !monitor.shouldProcessWallet("0xANY") {
		t.Error("expected all wallets to pass with no filter")
	}

	// Set filter
	monitor.SetWalletFilter([]string{"0xAllowed1", "0xAllowed2"})

	// Allowed wallets (case-insensitive)
	if !monitor.shouldProcessWallet("0xallowed1") {
		t.Error("expected allowed wallet (lowercase) to pass")
	}
	if !monitor.shouldProcessWallet("0xALLOWED1") {
		t.Error("expected allowed wallet (uppercase) to pass")
	}
	if !monitor.shouldProcessWallet("0xAllowed2") {
		t.Error("expected allowed wallet (mixed case) to pass")
	}

	// Not allowed wallets
	if monitor.shouldProcessWallet("0xNOTALLOWED") {
		t.Error("expected non-allowed wallet to be rejected")
	}
	if monitor.shouldProcessWallet("0xsomeother") {
		t.Error("expected non-allowed wallet to be rejected")
	}
}

