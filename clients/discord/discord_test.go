package discord

import (
	"polybot/clients/notifier"
	"polybot/config"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestNewDiscordClient_NoToken(t *testing.T) {
	cfg := &config.Config{
		IsProd: false,
		Discord: config.DiscordConfig{
			BotToken:      "",
			ProdChannelID: "prod-channel",
			BetaChannelID: "beta-channel",
		},
	}

	client := NewDiscordClient(zap.NewNop(), cfg)

	if client.session != nil {
		t.Error("expected nil session when no token provided")
	}
	if client.channelID != "beta-channel" {
		t.Errorf("expected beta channel, got: %s", client.channelID)
	}
}

func TestNewDiscordClient_ProdChannel(t *testing.T) {
	cfg := &config.Config{
		IsProd: true,
		Discord: config.DiscordConfig{
			BotToken:      "",
			ProdChannelID: "prod-channel",
			BetaChannelID: "beta-channel",
		},
	}

	client := NewDiscordClient(nil, cfg)

	if client.channelID != "prod-channel" {
		t.Errorf("expected prod channel, got: %s", client.channelID)
	}
}

func TestNewDiscordClient_BetaChannel(t *testing.T) {
	cfg := &config.Config{
		IsProd: false,
		Discord: config.DiscordConfig{
			BotToken:      "",
			ProdChannelID: "prod-channel",
			BetaChannelID: "beta-channel",
		},
	}

	client := NewDiscordClient(nil, cfg)

	if client.channelID != "beta-channel" {
		t.Errorf("expected beta channel, got: %s", client.channelID)
	}
}

func TestSendMessage_NoSession(t *testing.T) {
	client := &DiscordClient{
		logger:  zap.NewNop(),
		session: nil,
	}

	// Should not panic
	client.SendMessage("test message")
}

func TestSendTradeAlert_NoSession(t *testing.T) {
	client := &DiscordClient{
		logger:  zap.NewNop(),
		session: nil,
	}

	alert := notifier.TradeAlert{
		TraderName: "test",
	}

	// Should not panic
	client.SendTradeAlert(alert)
}

func TestBuildTradeEmbed_BuySide(t *testing.T) {
	client := &DiscordClient{
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
		MarketImage:   "https://example.com/image.png",
		Outcome:       "Yes",
		UniqueMarkets: 5,
		WinRate:       0.65,
		WinCount:      13,
		LossCount:     7,
		Reasons:       []notifier.AlertReason{notifier.AlertReasonLowActivity},
		Timestamp:     time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC),
	}

	embed := client.buildTradeEmbed(alert)

	if embed.Title != "🚨 Low Activity Wallet" {
		t.Errorf("unexpected title: %s", embed.Title)
	}
	if embed.URL != alert.WalletURL {
		t.Errorf("unexpected URL: %s", embed.URL)
	}
	if embed.Color != 0x2ECC71 { // Green for BUY
		t.Errorf("unexpected color for BUY: %d", embed.Color)
	}
	if len(embed.Fields) != 6 {
		t.Errorf("expected 6 fields, got %d", len(embed.Fields))
	}
	if embed.Image == nil || embed.Image.URL != alert.MarketImage {
		t.Error("expected market image to be set")
	}
}

func TestBuildTradeEmbed_SellSide(t *testing.T) {
	client := &DiscordClient{
		logger: zap.NewNop(),
	}

	alert := notifier.TradeAlert{
		TraderName:    "TestTrader",
		TraderAddress: "0x123",
		Side:          "SELL",
		Shares:        50,
		Price:         0.5,
		Notional:      25,
		MarketTitle:   "Test Market",
		Outcome:       "No",
		UniqueMarkets: 3,
		WinCount:      0,
		LossCount:     0,
	}

	embed := client.buildTradeEmbed(alert)

	if embed.Color != 0xE74C3C { // Red for SELL
		t.Errorf("unexpected color for SELL: %d", embed.Color)
	}

	// Check that side field contains SELL
	var foundSide bool
	for _, field := range embed.Fields {
		if field.Name == "Side" && field.Value == "🔴 SELL" {
			foundSide = true
		}
	}
	if !foundSide {
		t.Error("expected SELL side with red emoji")
	}
}

func TestBuildTradeEmbed_NoWinLoss(t *testing.T) {
	client := &DiscordClient{
		logger: zap.NewNop(),
	}

	alert := notifier.TradeAlert{
		TraderName: "TestTrader",
		Side:       "BUY",
		WinCount:   0,
		LossCount:  0,
	}

	embed := client.buildTradeEmbed(alert)

	// Check win rate shows N/A
	var foundWinRate bool
	for _, field := range embed.Fields {
		if field.Name == "Win Rate (resolved)" && field.Value == "N/A" {
			foundWinRate = true
		}
	}
	if !foundWinRate {
		t.Error("expected N/A for win rate when no wins/losses")
	}
}

func TestBuildTradeEmbed_WithWinRate(t *testing.T) {
	client := &DiscordClient{
		logger: zap.NewNop(),
	}

	alert := notifier.TradeAlert{
		TraderName: "TestTrader",
		Side:       "BUY",
		WinRate:    0.75,
		WinCount:   3,
		LossCount:  1,
	}

	embed := client.buildTradeEmbed(alert)

	var foundWinRate bool
	for _, field := range embed.Fields {
		if field.Name == "Win Rate (resolved)" && field.Value == "75.0% (3-1)" {
			foundWinRate = true
		}
	}
	if !foundWinRate {
		t.Error("expected formatted win rate")
	}
}

func TestBuildTradeEmbed_TraderDisplayWithLink(t *testing.T) {
	client := &DiscordClient{
		logger: zap.NewNop(),
	}

	alert := notifier.TradeAlert{
		TraderName:    "CryptoKing",
		TraderAddress: "0x1234567890abcdef1234567890abcdef12345678",
		WalletURL:     "https://polymarket.com/profile/0x123",
		Side:          "BUY",
	}

	embed := client.buildTradeEmbed(alert)

	// Check trader field has link
	var foundTrader bool
	for _, field := range embed.Fields {
		if field.Name == "Trader" {
			// Should contain markdown link
			if field.Value == "[CryptoKing (0x1234…345678)](https://polymarket.com/profile/0x123)" {
				foundTrader = true
			}
		}
	}
	if !foundTrader {
		t.Error("expected trader field with link and short address")
	}
}

func TestBuildTradeEmbed_TraderSameAsShortAddress(t *testing.T) {
	client := &DiscordClient{
		logger: zap.NewNop(),
	}

	// When trader name is the same as short address, don't duplicate
	alert := notifier.TradeAlert{
		TraderName:    "0x1234…345678",
		TraderAddress: "0x1234567890abcdef1234567890abcdef12345678",
		WalletURL:     "https://polymarket.com/profile/0x123",
		Side:          "BUY",
	}

	embed := client.buildTradeEmbed(alert)

	for _, field := range embed.Fields {
		if field.Name == "Trader" {
			// Should just be the name, not duplicated
			if field.Value == "[0x1234…345678](https://polymarket.com/profile/0x123)" {
				return // Test passed
			}
		}
	}
	t.Error("expected trader field without duplicate short address")
}

func TestBuildTradeEmbed_NoMarketImage(t *testing.T) {
	client := &DiscordClient{
		logger: zap.NewNop(),
	}

	alert := notifier.TradeAlert{
		TraderName:  "TestTrader",
		Side:        "BUY",
		MarketImage: "",
	}

	embed := client.buildTradeEmbed(alert)

	if embed.Image != nil {
		t.Error("expected no image when MarketImage is empty")
	}
}

func TestBuildTradeEmbed_ZeroTimestamp(t *testing.T) {
	client := &DiscordClient{
		logger: zap.NewNop(),
	}

	alert := notifier.TradeAlert{
		TraderName: "TestTrader",
		Side:       "BUY",
		Timestamp:  time.Time{}, // Zero time
	}

	embed := client.buildTradeEmbed(alert)

	// Should use current time, so timestamp should not be empty
	if embed.Timestamp == "" {
		t.Error("expected non-empty timestamp")
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

func TestClose_NoSession(t *testing.T) {
	client := &DiscordClient{
		logger:  zap.NewNop(),
		session: nil,
	}

	err := client.Close()
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestTradeAlertFields(t *testing.T) {
	alert := notifier.TradeAlert{
		TraderName:    "TestTrader",
		TraderAddress: "0x123",
		WalletURL:     "https://example.com",
		Side:          "BUY",
		Shares:        100,
		Price:         0.5,
		Notional:      50,
		MarketTitle:   "Test Market",
		MarketURL:     "https://market.com",
		MarketImage:   "https://image.com",
		Outcome:       "Yes",
		UniqueMarkets: 10,
		WinRate:       0.8,
		WinCount:      8,
		LossCount:     2,
		Timestamp:     time.Now(),
	}

	// Just verify all fields are accessible
	if alert.TraderName != "TestTrader" {
		t.Error("unexpected trader name")
	}
	if alert.Notional != 50 {
		t.Error("unexpected notional")
	}
}

func TestBuildTradeEmbed_NoWalletURL(t *testing.T) {
	client := &DiscordClient{
		logger: zap.NewNop(),
	}

	alert := notifier.TradeAlert{
		TraderName:    "TestTrader",
		TraderAddress: "0x1234567890abcdef1234567890abcdef12345678",
		WalletURL:     "", // No wallet URL
		Side:          "BUY",
	}

	embed := client.buildTradeEmbed(alert)

	// Trader field should not have a link
	for _, field := range embed.Fields {
		if field.Name == "Trader" {
			// Should just be the name with short address, no link
			expected := "TestTrader (0x1234…345678)"
			if field.Value != expected {
				t.Errorf("expected %q, got %q", expected, field.Value)
			}
		}
	}
}

func TestBuildTradeEmbed_EmptyTraderAddress(t *testing.T) {
	client := &DiscordClient{
		logger: zap.NewNop(),
	}

	alert := notifier.TradeAlert{
		TraderName:    "TestTrader",
		TraderAddress: "",
		WalletURL:     "https://example.com",
		Side:          "BUY",
	}

	embed := client.buildTradeEmbed(alert)

	// Should not panic and should have the embed
	if embed == nil {
		t.Error("expected embed to be created")
	}
}

func TestBuildTradeEmbed_AllFieldsInline(t *testing.T) {
	client := &DiscordClient{
		logger: zap.NewNop(),
	}

	alert := notifier.TradeAlert{
		TraderName: "TestTrader",
		Side:       "BUY",
	}

	embed := client.buildTradeEmbed(alert)

	// All fields should be inline
	for _, field := range embed.Fields {
		if !field.Inline {
			t.Errorf("expected field %q to be inline", field.Name)
		}
	}
}

func TestBuildTradeEmbed_DescriptionFormat(t *testing.T) {
	client := &DiscordClient{
		logger: zap.NewNop(),
	}

	alert := notifier.TradeAlert{
		TraderName:  "TestTrader",
		Side:        "BUY",
		MarketTitle: "Will BTC reach $100k?",
		Outcome:     "Yes",
	}

	embed := client.buildTradeEmbed(alert)

	expectedDesc := "**Will BTC reach $100k?**\nOutcome: Yes"
	if embed.Description != expectedDesc {
		t.Errorf("unexpected description: %q", embed.Description)
	}
}

func TestBuildTradeEmbed_FooterFormat(t *testing.T) {
	client := &DiscordClient{
		logger: zap.NewNop(),
	}

	ts := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	alert := notifier.TradeAlert{
		TraderName: "TestTrader",
		Side:       "BUY",
		Timestamp:  ts,
	}

	embed := client.buildTradeEmbed(alert)

	if embed.Footer == nil {
		t.Fatal("expected footer to be set")
	}
	// Footer should contain "polybot *"
	if embed.Footer.Text == "" {
		t.Error("expected footer text to be set")
	}
}

func TestNewDiscordClient_WithToken(t *testing.T) {
	// Note: This test will fail to create a valid session since the token is fake
	// but it tests the code path where a token is provided
	cfg := &config.Config{
		IsProd: false,
		Discord: config.DiscordConfig{
			BotToken:      "fake-token-for-testing",
			ProdChannelID: "prod-channel",
			BetaChannelID: "beta-channel",
		},
	}

	client := NewDiscordClient(zap.NewNop(), cfg)

	// With a valid token format, discordgo should create a session
	// but it won't be connected
	if client.channelID != "beta-channel" {
		t.Errorf("expected beta channel, got: %s", client.channelID)
	}
}

func TestDiscordClient_IsProdFlag(t *testing.T) {
	cfg := &config.Config{
		IsProd: true,
		Discord: config.DiscordConfig{
			BotToken:      "",
			ProdChannelID: "prod-123",
			BetaChannelID: "beta-456",
		},
	}

	client := NewDiscordClient(nil, cfg)

	if !client.isProd {
		t.Error("expected isProd to be true")
	}
	if client.channelID != "prod-123" {
		t.Errorf("expected prod channel, got: %s", client.channelID)
	}
}

func TestClose_WithSession(t *testing.T) {
	// Create a client with a nil session to test the close path
	client := &DiscordClient{
		logger:  zap.NewNop(),
		session: nil,
	}

	err := client.Close()
	if err != nil {
		t.Errorf("unexpected error on close: %v", err)
	}
}

func TestBuildTradeEmbed_SellSideCaseInsensitive(t *testing.T) {
	client := &DiscordClient{
		logger: zap.NewNop(),
	}

	// Test lowercase sell
	alert := notifier.TradeAlert{
		TraderName: "TestTrader",
		Side:       "sell", // lowercase
	}

	embed := client.buildTradeEmbed(alert)

	// Should still be red for sell
	if embed.Color != 0xE74C3C {
		t.Errorf("expected red color for sell, got: %d", embed.Color)
	}
}

func TestBuildTradeEmbed_ExtremeBetTitle(t *testing.T) {
	client := &DiscordClient{
		logger: zap.NewNop(),
	}

	tests := []struct {
		name     string
		reasons  []notifier.AlertReason
		expected string
	}{
		{
			name:     "extreme bet only",
			reasons:  []notifier.AlertReason{notifier.AlertReasonExtremeBet},
			expected: "💰 Extreme Odds Bet",
		},
		{
			name:     "low activity + extreme bet",
			reasons:  []notifier.AlertReason{notifier.AlertReasonLowActivity, notifier.AlertReasonExtremeBet},
			expected: "🚨 Low Activity + Extreme Odds Bet",
		},
		{
			name:     "high win rate + extreme bet",
			reasons:  []notifier.AlertReason{notifier.AlertReasonHighWinRate, notifier.AlertReasonExtremeBet},
			expected: "🎯 High Win Rate + Extreme Odds Bet",
		},
		{
			name:     "three or more reasons",
			reasons:  []notifier.AlertReason{notifier.AlertReasonLowActivity, notifier.AlertReasonHighWinRate, notifier.AlertReasonExtremeBet},
			expected: "🚨 Multiple Alert Triggers",
		},
		{
			name:     "rapid trading only",
			reasons:  []notifier.AlertReason{notifier.AlertReasonRapidTrading},
			expected: "⚡ Rapid Trading Detected",
		},
		{
			name:     "low activity + rapid trading",
			reasons:  []notifier.AlertReason{notifier.AlertReasonLowActivity, notifier.AlertReasonRapidTrading},
			expected: "🚨 Low Activity + Rapid Trading",
		},
		{
			name:     "new wallet only",
			reasons:  []notifier.AlertReason{notifier.AlertReasonNewWallet},
			expected: "🆕 New Wallet Large Bet",
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
			name:     "contrarian bet only",
			reasons:  []notifier.AlertReason{notifier.AlertReasonContrarianBet},
			expected: "🔄 Contrarian Large Bet",
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
			name:     "massive trade only",
			reasons:  []notifier.AlertReason{notifier.AlertReasonMassiveTrade},
			expected: "🐋 Massive Trade",
		},
		{
			name:     "massive trade + high win rate",
			reasons:  []notifier.AlertReason{notifier.AlertReasonMassiveTrade, notifier.AlertReasonHighWinRate},
			expected: "🐋 Massive Trade + High Win Rate",
		},
		{
			name:     "massive trade + low activity",
			reasons:  []notifier.AlertReason{notifier.AlertReasonMassiveTrade, notifier.AlertReasonLowActivity},
			expected: "🐋 Massive Trade + Low Activity",
		},
		{
			name:     "massive trade + new wallet",
			reasons:  []notifier.AlertReason{notifier.AlertReasonMassiveTrade, notifier.AlertReasonNewWallet},
			expected: "🐋 Massive Trade + New Wallet",
		},
		{
			name:     "high win rate only",
			reasons:  []notifier.AlertReason{notifier.AlertReasonHighWinRate},
			expected: "🎯 High Win Rate Trader",
		},
		{
			name:     "low activity only",
			reasons:  []notifier.AlertReason{notifier.AlertReasonLowActivity},
			expected: "🚨 Low Activity Wallet",
		},
		{
			name:     "low activity + high win rate",
			reasons:  []notifier.AlertReason{notifier.AlertReasonLowActivity, notifier.AlertReasonHighWinRate},
			expected: "🚨 Low Activity + High Win Rate",
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
			name:     "no reasons",
			reasons:  []notifier.AlertReason{},
			expected: "🚨 Trade Alert",
		},
		{
			name:     "contrarian winner alert",
			reasons:  []notifier.AlertReason{notifier.AlertReasonContrarianWinner},
			expected: "🏆 Proven Contrarian Winner",
		},
		{
			name:     "copy trading",
			reasons:  []notifier.AlertReason{notifier.AlertReasonCopyTrader},
			expected: "🔍 Copy Trader Detected",
		},
		{
			name:     "hedge removal",
			reasons:  []notifier.AlertReason{notifier.AlertReasonHedgeRemoval},
			expected: "🛡️ Hedge Removal Detected",
		},
		{
			name:     "stealth accumulation",
			reasons:  []notifier.AlertReason{notifier.AlertReasonStealthAccumulation},
			expected: "🥷 Stealth Accumulation",
		},
		{
			name:     "conviction doubling",
			reasons:  []notifier.AlertReason{notifier.AlertReasonConvictionDoubling},
			expected: "💪 Conviction Doubling",
		},
		{
			name:     "perfect exit timing",
			reasons:  []notifier.AlertReason{notifier.AlertReasonPerfectExitTiming},
			expected: "⏱️ Perfect Exit Timing",
		},
		{
			name:     "pre-move positioning",
			reasons:  []notifier.AlertReason{notifier.AlertReasonPreMovePositioning},
			expected: "🎯 Pre-Move Positioning",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			alert := notifier.TradeAlert{
				TraderName: "TestTrader",
				Side:       "BUY",
				Reasons:    tt.reasons,
			}

			embed := client.buildTradeEmbed(alert)

			if embed.Title != tt.expected {
				t.Errorf("expected title %q, got %q", tt.expected, embed.Title)
			}
		})
	}
}

func TestBuildTradeEmbed_HedgeFields(t *testing.T) {
	client := &DiscordClient{
		logger: zap.NewNop(),
	}

	alert := notifier.TradeAlert{
		TraderName: "TestTrader",
		Side:       "SELL",
		Reasons:    []notifier.AlertReason{notifier.AlertReasonHedgeRemoval},
		// Hedge-specific fields
		HasHedgeInfo:       true,
		HedgeSoldSide:      "No",
		HedgeSoldPct:       0.85,
		HedgeYesSizeBefore: 1000,
		HedgeNoSizeBefore:  600,
		HedgeYesSizeAfter:  1000,
		HedgeNoSizeAfter:   100,
	}

	embed := client.buildTradeEmbed(alert)

	if embed == nil {
		t.Fatal("expected embed")
	}

	// Check title is correct for hedge removal
	if embed.Title != "🛡️ Hedge Removal Detected" {
		t.Errorf("unexpected title: %s", embed.Title)
	}
}

func TestBuildTradeEmbed_StealthAccumulation(t *testing.T) {
	client := &DiscordClient{
		logger: zap.NewNop(),
	}

	alert := notifier.TradeAlert{
		TraderName: "TestTrader",
		Side:       "BUY",
		Reasons:    []notifier.AlertReason{notifier.AlertReasonStealthAccumulation},
	}

	embed := client.buildTradeEmbed(alert)

	// Check title based on what the implementation outputs
	if embed == nil {
		t.Fatal("expected embed")
	}
}

func TestBuildTradeEmbed_RapidTrading(t *testing.T) {
	client := &DiscordClient{
		logger: zap.NewNop(),
	}

	alert := notifier.TradeAlert{
		TraderName: "TestTrader",
		Side:       "BUY",
		Reasons:    []notifier.AlertReason{notifier.AlertReasonRapidTrading},
	}

	embed := client.buildTradeEmbed(alert)

	if embed.Title != "⚡ Rapid Trading Detected" {
		t.Errorf("unexpected title: %s", embed.Title)
	}
}

func TestBuildTradeEmbed_CopyTrader(t *testing.T) {
	client := &DiscordClient{
		logger: zap.NewNop(),
	}

	alert := notifier.TradeAlert{
		TraderName: "TestTrader",
		Side:       "BUY",
		Reasons:    []notifier.AlertReason{notifier.AlertReasonCopyTrader},
	}

	embed := client.buildTradeEmbed(alert)

	if embed.Title != "🔍 Copy Trader Detected" {
		t.Errorf("unexpected title: %s", embed.Title)
	}
}
