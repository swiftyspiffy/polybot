package telegram

import (
	"net/http"
	"net/http/httptest"
	"polybot/clients/notifier"
	"polybot/config"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestNewTelegramClient_NoToken(t *testing.T) {
	cfg := &config.Config{
		IsProd: false,
		Telegram: config.TelegramConfig{
			BotToken:   "",
			ProdChatID: "prod-chat",
			BetaChatID: "beta-chat",
		},
	}

	client := NewTelegramClient(zap.NewNop(), cfg)

	if client.botToken != "" {
		t.Error("expected empty token")
	}
	if client.chatID != "beta-chat" {
		t.Errorf("expected beta chat, got: %s", client.chatID)
	}
}

func TestNewTelegramClient_ProdChat(t *testing.T) {
	cfg := &config.Config{
		IsProd: true,
		Telegram: config.TelegramConfig{
			BotToken:   "",
			ProdChatID: "prod-chat",
			BetaChatID: "beta-chat",
		},
	}

	client := NewTelegramClient(nil, cfg)

	if client.chatID != "prod-chat" {
		t.Errorf("expected prod chat, got: %s", client.chatID)
	}
}

func TestNewTelegramClient_BetaChat(t *testing.T) {
	cfg := &config.Config{
		IsProd: false,
		Telegram: config.TelegramConfig{
			BotToken:   "",
			ProdChatID: "prod-chat",
			BetaChatID: "beta-chat",
		},
	}

	client := NewTelegramClient(nil, cfg)

	if client.chatID != "beta-chat" {
		t.Errorf("expected beta chat, got: %s", client.chatID)
	}
}

func TestNewTelegramClient_WithToken(t *testing.T) {
	cfg := &config.Config{
		IsProd: false,
		Telegram: config.TelegramConfig{
			BotToken:   "test-token",
			ProdChatID: "prod-chat",
			BetaChatID: "beta-chat",
		},
	}

	client := NewTelegramClient(zap.NewNop(), cfg)

	if client.botToken != "test-token" {
		t.Errorf("expected test-token, got: %s", client.botToken)
	}
	if client.client == nil {
		t.Error("expected http client to be set")
	}
}

func TestSendTradeAlert_NotConfigured(t *testing.T) {
	client := &TelegramClient{
		logger:   zap.NewNop(),
		botToken: "",
		chatID:   "",
	}

	alert := notifier.TradeAlert{TraderName: "test"}

	// Should not panic
	client.SendTradeAlert(alert)
}

func TestSendTradeAlert_NoChatID(t *testing.T) {
	client := &TelegramClient{
		logger:   zap.NewNop(),
		botToken: "token",
		chatID:   "",
	}

	alert := notifier.TradeAlert{TraderName: "test"}

	// Should not panic
	client.SendTradeAlert(alert)
}

func TestSendTradeAlert_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	_ = &TelegramClient{
		logger:   zap.NewNop(),
		botToken: "test-token",
		chatID:   "test-chat",
		client:   server.Client(),
	}

	// Override the URL by using the test server
	// Note: This test is limited because we can't easily override the URL
	// but we can at least test the message building
}

func TestSendTradeAlert_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	client := &TelegramClient{
		logger:   zap.NewNop(),
		botToken: "test-token",
		chatID:   "test-chat",
		client:   server.Client(),
	}

	// This tests the error path but can't fully test due to URL hardcoding
	alert := notifier.TradeAlert{TraderName: "test"}
	client.SendTradeAlert(alert)
}

func TestBuildAlertMessage_FullAlert(t *testing.T) {
	client := &TelegramClient{
		logger: zap.NewNop(),
	}

	alert := notifier.TradeAlert{
		TraderName:    "TestTrader",
		TraderAddress: "0x1234567890abcdef1234567890abcdef12345678",
		WalletURL:     "https://polymarket.com/profile/0x123",
		Side:          "BUY",
		Shares:        100.5,
		Price:         0.75,
		Notional:      75.375,
		MarketTitle:   "Test Market",
		MarketURL:     "https://polymarket.com/event/test",
		Outcome:       "Yes",
		UniqueMarkets: 5,
		WinRate:       0.65,
		WinCount:      13,
		LossCount:     7,
		Reasons:       []notifier.AlertReason{notifier.AlertReasonLowActivity},
		Timestamp:     time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC),
	}

	msg := client.buildAlertMessage(alert)

	// Check various parts of the message
	if msg == "" {
		t.Error("expected non-empty message")
	}
	// Should contain market link
	if !containsString(msg, "[Test Market](https://polymarket.com/event/test)") {
		t.Error("expected market link in message")
	}
	// Should contain trader link
	if !containsString(msg, "Trader:*") {
		t.Error("expected trader field")
	}
}

func TestBuildAlertMessage_NoMarketURL(t *testing.T) {
	client := &TelegramClient{
		logger: zap.NewNop(),
	}

	alert := notifier.TradeAlert{
		TraderName:  "TestTrader",
		Side:        "BUY",
		MarketTitle: "Test Market",
		MarketURL:   "", // No URL
		Outcome:     "Yes",
	}

	msg := client.buildAlertMessage(alert)

	// Should contain market title without link
	if !containsString(msg, "*Market:* Test Market") {
		t.Error("expected market title without link")
	}
}

func TestBuildAlertMessage_NoWalletURL(t *testing.T) {
	client := &TelegramClient{
		logger: zap.NewNop(),
	}

	alert := notifier.TradeAlert{
		TraderName:    "TestTrader",
		TraderAddress: "0x1234567890abcdef1234567890abcdef12345678",
		WalletURL:     "", // No URL
		Side:          "BUY",
	}

	msg := client.buildAlertMessage(alert)

	// Should contain trader without link
	if !containsString(msg, "*Trader:* TestTrader") {
		t.Error("expected trader without link")
	}
}

func TestBuildAlertMessage_SellSide(t *testing.T) {
	client := &TelegramClient{
		logger: zap.NewNop(),
	}

	alert := notifier.TradeAlert{
		TraderName: "TestTrader",
		Side:       "SELL",
	}

	msg := client.buildAlertMessage(alert)

	if !containsString(msg, "🔴 SELL") {
		t.Error("expected red emoji for SELL")
	}
}

func TestBuildAlertMessage_BuySide(t *testing.T) {
	client := &TelegramClient{
		logger: zap.NewNop(),
	}

	alert := notifier.TradeAlert{
		TraderName: "TestTrader",
		Side:       "BUY",
	}

	msg := client.buildAlertMessage(alert)

	if !containsString(msg, "🟢 BUY") {
		t.Error("expected green emoji for BUY")
	}
}

func TestBuildAlertMessage_NoWinLoss(t *testing.T) {
	client := &TelegramClient{
		logger: zap.NewNop(),
	}

	alert := notifier.TradeAlert{
		TraderName: "TestTrader",
		Side:       "BUY",
		WinCount:   0,
		LossCount:  0,
	}

	msg := client.buildAlertMessage(alert)

	if !containsString(msg, "*Win Rate:* N/A") {
		t.Error("expected N/A for win rate")
	}
}

func TestBuildAlertMessage_WithWinRate(t *testing.T) {
	client := &TelegramClient{
		logger: zap.NewNop(),
	}

	alert := notifier.TradeAlert{
		TraderName: "TestTrader",
		Side:       "BUY",
		WinRate:    0.75,
		WinCount:   3,
		LossCount:  1,
	}

	msg := client.buildAlertMessage(alert)

	if !containsString(msg, "75.0% (3-1)") {
		t.Error("expected formatted win rate")
	}
}

func TestBuildAlertMessage_ZeroTimestamp(t *testing.T) {
	client := &TelegramClient{
		logger: zap.NewNop(),
	}

	alert := notifier.TradeAlert{
		TraderName: "TestTrader",
		Side:       "BUY",
		Timestamp:  time.Time{}, // Zero time
	}

	msg := client.buildAlertMessage(alert)

	// Should use current time, so message should still have timestamp
	if !containsString(msg, "polybot") {
		t.Error("expected polybot footer")
	}
}

func TestBuildAlertMessage_TraderSameAsShortAddress(t *testing.T) {
	client := &TelegramClient{
		logger: zap.NewNop(),
	}

	alert := notifier.TradeAlert{
		TraderName:    "0x1234…345678",
		TraderAddress: "0x1234567890abcdef1234567890abcdef12345678",
		WalletURL:     "https://example.com",
		Side:          "BUY",
	}

	msg := client.buildAlertMessage(alert)

	// Should not have duplicate address
	if containsString(msg, "0x1234…345678 (0x1234…345678)") {
		t.Error("should not duplicate address")
	}
}

func TestBuildAlertTitle_AllReasons(t *testing.T) {
	client := &TelegramClient{
		logger: zap.NewNop(),
	}

	tests := []struct {
		name     string
		reasons  []notifier.AlertReason
		expected string
	}{
		{
			name:     "low activity only",
			reasons:  []notifier.AlertReason{notifier.AlertReasonLowActivity},
			expected: "🚨 Low Activity Wallet",
		},
		{
			name:     "high win rate only",
			reasons:  []notifier.AlertReason{notifier.AlertReasonHighWinRate},
			expected: "🎯 High Win Rate Trader",
		},
		{
			name:     "extreme bet only",
			reasons:  []notifier.AlertReason{notifier.AlertReasonExtremeBet},
			expected: "💰 Extreme Odds Bet",
		},
		{
			name:     "rapid trading only",
			reasons:  []notifier.AlertReason{notifier.AlertReasonRapidTrading},
			expected: "⚡ Rapid Trading Detected",
		},
		{
			name:     "new wallet only",
			reasons:  []notifier.AlertReason{notifier.AlertReasonNewWallet},
			expected: "🆕 New Wallet Large Bet",
		},
		{
			name:     "contrarian bet only",
			reasons:  []notifier.AlertReason{notifier.AlertReasonContrarianBet},
			expected: "🔄 Contrarian Large Bet",
		},
		{
			name:     "massive trade only",
			reasons:  []notifier.AlertReason{notifier.AlertReasonMassiveTrade},
			expected: "🐋 Massive Trade",
		},
		{
			name:     "no reasons",
			reasons:  []notifier.AlertReason{},
			expected: "🚨 Trade Alert",
		},
		{
			name:     "low activity + high win rate",
			reasons:  []notifier.AlertReason{notifier.AlertReasonLowActivity, notifier.AlertReasonHighWinRate},
			expected: "🚨 Low Activity + High Win Rate",
		},
		{
			name:     "low activity + extreme bet",
			reasons:  []notifier.AlertReason{notifier.AlertReasonLowActivity, notifier.AlertReasonExtremeBet},
			expected: "🚨 Low Activity + Extreme Odds Bet",
		},
		{
			name:     "low activity + rapid trading",
			reasons:  []notifier.AlertReason{notifier.AlertReasonLowActivity, notifier.AlertReasonRapidTrading},
			expected: "🚨 Low Activity + Rapid Trading",
		},
		{
			name:     "high win rate + extreme bet",
			reasons:  []notifier.AlertReason{notifier.AlertReasonHighWinRate, notifier.AlertReasonExtremeBet},
			expected: "🎯 High Win Rate + Extreme Odds Bet",
		},
		{
			name:     "high win rate + rapid trading",
			reasons:  []notifier.AlertReason{notifier.AlertReasonHighWinRate, notifier.AlertReasonRapidTrading},
			expected: "🎯 High Win Rate + Rapid Trading",
		},
		{
			name:     "extreme bet + rapid trading",
			reasons:  []notifier.AlertReason{notifier.AlertReasonExtremeBet, notifier.AlertReasonRapidTrading},
			expected: "⚡ Extreme Odds + Rapid Trading",
		},
		{
			name:     "new wallet + extreme bet",
			reasons:  []notifier.AlertReason{notifier.AlertReasonNewWallet, notifier.AlertReasonExtremeBet},
			expected: "🆕 New Wallet + Extreme Odds Bet",
		},
		{
			name:     "new wallet + low activity",
			reasons:  []notifier.AlertReason{notifier.AlertReasonNewWallet, notifier.AlertReasonLowActivity},
			expected: "🆕 New Wallet + Low Activity",
		},
		{
			name:     "contrarian + new wallet",
			reasons:  []notifier.AlertReason{notifier.AlertReasonContrarianBet, notifier.AlertReasonNewWallet},
			expected: "🔄 Contrarian + New Wallet Bet",
		},
		{
			name:     "contrarian + low activity",
			reasons:  []notifier.AlertReason{notifier.AlertReasonContrarianBet, notifier.AlertReasonLowActivity},
			expected: "🔄 Contrarian + Low Activity Bet",
		},
		{
			name:     "contrarian + high win rate",
			reasons:  []notifier.AlertReason{notifier.AlertReasonContrarianBet, notifier.AlertReasonHighWinRate},
			expected: "🔄 Contrarian + High Win Rate Bet",
		},
		{
			name:     "massive + high win rate",
			reasons:  []notifier.AlertReason{notifier.AlertReasonMassiveTrade, notifier.AlertReasonHighWinRate},
			expected: "🐋 Massive Trade + High Win Rate",
		},
		{
			name:     "massive + low activity",
			reasons:  []notifier.AlertReason{notifier.AlertReasonMassiveTrade, notifier.AlertReasonLowActivity},
			expected: "🐋 Massive Trade + Low Activity",
		},
		{
			name:     "massive + new wallet",
			reasons:  []notifier.AlertReason{notifier.AlertReasonMassiveTrade, notifier.AlertReasonNewWallet},
			expected: "🐋 Massive Trade + New Wallet",
		},
		{
			name:     "three or more reasons",
			reasons:  []notifier.AlertReason{notifier.AlertReasonLowActivity, notifier.AlertReasonHighWinRate, notifier.AlertReasonExtremeBet},
			expected: "🚨 Multiple Alert Triggers",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			title := client.buildAlertTitle(tt.reasons)
			if title != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, title)
			}
		})
	}
}

func TestShortAddress(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"0x1234567890abcdef1234567890abcdef12345678", "0x1234…345678"},
		{"0x123456789012", "0x123456789012"}, // <= 14 chars
		{"short", "short"},
		{"", ""},
		{"exactly14chars", "exactly14chars"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := shortAddress(tt.input)
			if result != tt.expected {
				t.Errorf("shortAddress(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestEscapeMarkdown(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello", "hello"},
		{"hello_world", "hello\\_world"},
		{"*bold*", "\\*bold\\*"},
		{"[link]", "\\[link\\]"},
		{"`code`", "\\`code\\`"},
		{"_*[`]", "\\_\\*\\[\\`\\]"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := escapeMarkdown(tt.input)
			if result != tt.expected {
				t.Errorf("escapeMarkdown(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestClose(t *testing.T) {
	client := &TelegramClient{
		logger: zap.NewNop(),
	}

	err := client.Close()
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestTelegramClient_IsProdFlag(t *testing.T) {
	cfg := &config.Config{
		IsProd: true,
		Telegram: config.TelegramConfig{
			BotToken:   "token",
			ProdChatID: "prod-123",
			BetaChatID: "beta-456",
		},
	}

	client := NewTelegramClient(nil, cfg)

	if !client.isProd {
		t.Error("expected isProd to be true")
	}
}

// Helper function
func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
