package config

import (
	"os"
	"testing"
	"time"
)

func TestLoad_Defaults(t *testing.T) {
	// Clear any env vars that might affect the test
	envVars := []string{
		"STAGE", "DISCORD_BOT_TOKEN", "DISCORD_PROD_CHANNEL_ID", "DISCORD_BETA_CHANNEL_ID",
		"TRADE_POLL_INTERVAL", "TRADE_MIN_NOTIONAL", "TRADE_MAX_MARKETS_FOR_LOW",
		"TOP_MARKETS_COUNT", "MARKET_REFRESH_INTERVAL",
		"WALLET_CACHE_TTL", "CACHE_SAVE_INTERVAL", "CACHE_FILE_NAME", "CACHE_MAX_SIZE_BYTES",
		"GITHUB_TOKEN", "CACHE_GIST_ID",
		"POLYMARKET_GAMMA_API_URL", "POLYMARKET_DATA_API_URL",
	}
	for _, v := range envVars {
		os.Unsetenv(v)
	}

	cfg := Load()

	// Test defaults
	if cfg.IsProd {
		t.Error("expected IsProd to be false by default")
	}

	if cfg.Discord.BotToken != "" {
		t.Error("expected empty bot token by default")
	}
	if cfg.Discord.ProdChannelID != "" {
		t.Errorf("expected empty prod channel ID, got: %s", cfg.Discord.ProdChannelID)
	}
	if cfg.Discord.BetaChannelID != "" {
		t.Errorf("expected empty beta channel ID, got: %s", cfg.Discord.BetaChannelID)
	}

	if cfg.TradeMonitor.PollInterval != 10*time.Second {
		t.Errorf("unexpected poll interval: %v", cfg.TradeMonitor.PollInterval)
	}
	if cfg.TradeMonitor.MinNotional != 4000.0 {
		t.Errorf("unexpected min notional: %f", cfg.TradeMonitor.MinNotional)
	}
	if cfg.TradeMonitor.MaxMarketsForLow != 5 {
		t.Errorf("unexpected max markets for low: %d", cfg.TradeMonitor.MaxMarketsForLow)
	}

	if cfg.Markets.TopMarketsCount != 20 {
		t.Errorf("unexpected top markets count: %d", cfg.Markets.TopMarketsCount)
	}
	if cfg.Markets.RefreshInterval != 1*time.Minute {
		t.Errorf("unexpected refresh interval: %v", cfg.Markets.RefreshInterval)
	}

	if cfg.Cache.WalletCacheTTL != 1*time.Minute {
		t.Errorf("unexpected wallet cache TTL: %v", cfg.Cache.WalletCacheTTL)
	}
	if cfg.Cache.SaveInterval != 10*time.Minute {
		t.Errorf("unexpected save interval: %v", cfg.Cache.SaveInterval)
	}
	if cfg.Cache.FileName != "wallet_cache.json" {
		t.Errorf("unexpected file name: %s", cfg.Cache.FileName)
	}
	if cfg.Cache.MaxSizeBytes != 50*1024*1024 {
		t.Errorf("unexpected max size bytes: %d", cfg.Cache.MaxSizeBytes)
	}

	if cfg.Gist.Token != "" {
		t.Error("expected empty gist token by default")
	}
	if cfg.Gist.GistID != "" {
		t.Error("expected empty gist ID by default")
	}

	if cfg.Polymarket.GammaAPIURL != "https://gamma-api.polymarket.com" {
		t.Errorf("unexpected gamma API URL: %s", cfg.Polymarket.GammaAPIURL)
	}
	if cfg.Polymarket.DataAPIURL != "https://data-api.polymarket.com" {
		t.Errorf("unexpected data API URL: %s", cfg.Polymarket.DataAPIURL)
	}
}

func TestLoad_FromEnv(t *testing.T) {
	// Set env vars
	os.Setenv("STAGE", "PROD")
	os.Setenv("DISCORD_BOT_TOKEN", "test-token")
	os.Setenv("DISCORD_PROD_CHANNEL_ID", "prod-123")
	os.Setenv("DISCORD_BETA_CHANNEL_ID", "beta-456")
	os.Setenv("TRADE_POLL_INTERVAL", "30s")
	os.Setenv("TRADE_MIN_NOTIONAL", "500.5")
	os.Setenv("TRADE_MAX_MARKETS_FOR_LOW", "5")
	os.Setenv("TOP_MARKETS_COUNT", "50")
	os.Setenv("MARKET_REFRESH_INTERVAL", "10m")
	os.Setenv("WALLET_CACHE_TTL", "15m")
	os.Setenv("CACHE_SAVE_INTERVAL", "3m")
	os.Setenv("CACHE_FILE_NAME", "custom_cache.json")
	os.Setenv("CACHE_MAX_SIZE_BYTES", "10485760")
	os.Setenv("GITHUB_TOKEN", "gh-token")
	os.Setenv("CACHE_GIST_ID", "gist-id-123")
	os.Setenv("POLYMARKET_GAMMA_API_URL", "https://custom-gamma.com")
	os.Setenv("POLYMARKET_DATA_API_URL", "https://custom-data.com")

	defer func() {
		// Clean up
		os.Unsetenv("STAGE")
		os.Unsetenv("DISCORD_BOT_TOKEN")
		os.Unsetenv("DISCORD_PROD_CHANNEL_ID")
		os.Unsetenv("DISCORD_BETA_CHANNEL_ID")
		os.Unsetenv("TRADE_POLL_INTERVAL")
		os.Unsetenv("TRADE_MIN_NOTIONAL")
		os.Unsetenv("TRADE_MAX_MARKETS_FOR_LOW")
		os.Unsetenv("TOP_MARKETS_COUNT")
		os.Unsetenv("MARKET_REFRESH_INTERVAL")
		os.Unsetenv("WALLET_CACHE_TTL")
		os.Unsetenv("CACHE_SAVE_INTERVAL")
		os.Unsetenv("CACHE_FILE_NAME")
		os.Unsetenv("CACHE_MAX_SIZE_BYTES")
		os.Unsetenv("GITHUB_TOKEN")
		os.Unsetenv("CACHE_GIST_ID")
		os.Unsetenv("POLYMARKET_GAMMA_API_URL")
		os.Unsetenv("POLYMARKET_DATA_API_URL")
	}()

	cfg := Load()

	if !cfg.IsProd {
		t.Error("expected IsProd to be true")
	}
	if cfg.Discord.BotToken != "test-token" {
		t.Errorf("unexpected bot token: %s", cfg.Discord.BotToken)
	}
	if cfg.Discord.ProdChannelID != "prod-123" {
		t.Errorf("unexpected prod channel ID: %s", cfg.Discord.ProdChannelID)
	}
	if cfg.Discord.BetaChannelID != "beta-456" {
		t.Errorf("unexpected beta channel ID: %s", cfg.Discord.BetaChannelID)
	}
	if cfg.TradeMonitor.PollInterval != 30*time.Second {
		t.Errorf("unexpected poll interval: %v", cfg.TradeMonitor.PollInterval)
	}
	if cfg.TradeMonitor.MinNotional != 500.5 {
		t.Errorf("unexpected min notional: %f", cfg.TradeMonitor.MinNotional)
	}
	if cfg.TradeMonitor.MaxMarketsForLow != 5 {
		t.Errorf("unexpected max markets for low: %d", cfg.TradeMonitor.MaxMarketsForLow)
	}
	if cfg.Markets.TopMarketsCount != 50 {
		t.Errorf("unexpected top markets count: %d", cfg.Markets.TopMarketsCount)
	}
	if cfg.Markets.RefreshInterval != 10*time.Minute {
		t.Errorf("unexpected refresh interval: %v", cfg.Markets.RefreshInterval)
	}
	if cfg.Cache.WalletCacheTTL != 15*time.Minute {
		t.Errorf("unexpected wallet cache TTL: %v", cfg.Cache.WalletCacheTTL)
	}
	if cfg.Cache.SaveInterval != 3*time.Minute {
		t.Errorf("unexpected save interval: %v", cfg.Cache.SaveInterval)
	}
	if cfg.Cache.FileName != "custom_cache.json" {
		t.Errorf("unexpected file name: %s", cfg.Cache.FileName)
	}
	if cfg.Cache.MaxSizeBytes != 10485760 {
		t.Errorf("unexpected max size bytes: %d", cfg.Cache.MaxSizeBytes)
	}
	if cfg.Gist.Token != "gh-token" {
		t.Errorf("unexpected gist token: %s", cfg.Gist.Token)
	}
	if cfg.Gist.GistID != "gist-id-123" {
		t.Errorf("unexpected gist ID: %s", cfg.Gist.GistID)
	}
	if cfg.Polymarket.GammaAPIURL != "https://custom-gamma.com" {
		t.Errorf("unexpected gamma API URL: %s", cfg.Polymarket.GammaAPIURL)
	}
	if cfg.Polymarket.DataAPIURL != "https://custom-data.com" {
		t.Errorf("unexpected data API URL: %s", cfg.Polymarket.DataAPIURL)
	}
}

func TestEnvString(t *testing.T) {
	os.Setenv("TEST_STRING", "hello")
	defer os.Unsetenv("TEST_STRING")

	if v := envString("TEST_STRING", "default"); v != "hello" {
		t.Errorf("expected 'hello', got '%s'", v)
	}
	if v := envString("NONEXISTENT", "default"); v != "default" {
		t.Errorf("expected 'default', got '%s'", v)
	}

	// Test whitespace trimming
	os.Setenv("TEST_WHITESPACE", "  trimmed  ")
	defer os.Unsetenv("TEST_WHITESPACE")
	if v := envString("TEST_WHITESPACE", "default"); v != "trimmed" {
		t.Errorf("expected 'trimmed', got '%s'", v)
	}
}

func TestEnvInt(t *testing.T) {
	os.Setenv("TEST_INT", "42")
	defer os.Unsetenv("TEST_INT")

	if v := envInt("TEST_INT", 0); v != 42 {
		t.Errorf("expected 42, got %d", v)
	}
	if v := envInt("NONEXISTENT", 100); v != 100 {
		t.Errorf("expected 100, got %d", v)
	}

	// Test invalid int
	os.Setenv("TEST_INVALID_INT", "not-a-number")
	defer os.Unsetenv("TEST_INVALID_INT")
	if v := envInt("TEST_INVALID_INT", 50); v != 50 {
		t.Errorf("expected 50 for invalid int, got %d", v)
	}
}

func TestEnvInt64(t *testing.T) {
	os.Setenv("TEST_INT64", "9223372036854775807")
	defer os.Unsetenv("TEST_INT64")

	if v := envInt64("TEST_INT64", 0); v != 9223372036854775807 {
		t.Errorf("expected max int64, got %d", v)
	}
	if v := envInt64("NONEXISTENT", 100); v != 100 {
		t.Errorf("expected 100, got %d", v)
	}

	// Test invalid int64
	os.Setenv("TEST_INVALID_INT64", "not-a-number")
	defer os.Unsetenv("TEST_INVALID_INT64")
	if v := envInt64("TEST_INVALID_INT64", 50); v != 50 {
		t.Errorf("expected 50 for invalid int64, got %d", v)
	}
}

func TestEnvFloat(t *testing.T) {
	os.Setenv("TEST_FLOAT", "3.14159")
	defer os.Unsetenv("TEST_FLOAT")

	if v := envFloat("TEST_FLOAT", 0); v != 3.14159 {
		t.Errorf("expected 3.14159, got %f", v)
	}
	if v := envFloat("NONEXISTENT", 2.5); v != 2.5 {
		t.Errorf("expected 2.5, got %f", v)
	}

	// Test invalid float
	os.Setenv("TEST_INVALID_FLOAT", "not-a-number")
	defer os.Unsetenv("TEST_INVALID_FLOAT")
	if v := envFloat("TEST_INVALID_FLOAT", 1.5); v != 1.5 {
		t.Errorf("expected 1.5 for invalid float, got %f", v)
	}
}

func TestEnvDuration(t *testing.T) {
	os.Setenv("TEST_DURATION", "5m30s")
	defer os.Unsetenv("TEST_DURATION")

	expected := 5*time.Minute + 30*time.Second
	if v := envDuration("TEST_DURATION", 0); v != expected {
		t.Errorf("expected %v, got %v", expected, v)
	}
	if v := envDuration("NONEXISTENT", 10*time.Second); v != 10*time.Second {
		t.Errorf("expected 10s, got %v", v)
	}

	// Test invalid duration
	os.Setenv("TEST_INVALID_DURATION", "not-a-duration")
	defer os.Unsetenv("TEST_INVALID_DURATION")
	if v := envDuration("TEST_INVALID_DURATION", 1*time.Minute); v != 1*time.Minute {
		t.Errorf("expected 1m for invalid duration, got %v", v)
	}
}

func TestEnvBool(t *testing.T) {
	os.Setenv("TEST_BOOL_TRUE", "PROD")
	os.Setenv("TEST_BOOL_FALSE", "DEV")
	os.Setenv("TEST_BOOL_CASE", "prod")
	defer func() {
		os.Unsetenv("TEST_BOOL_TRUE")
		os.Unsetenv("TEST_BOOL_FALSE")
		os.Unsetenv("TEST_BOOL_CASE")
	}()

	if !envBool("TEST_BOOL_TRUE", "PROD") {
		t.Error("expected true for PROD")
	}
	if envBool("TEST_BOOL_FALSE", "PROD") {
		t.Error("expected false for DEV")
	}
	// Test case insensitivity
	if !envBool("TEST_BOOL_CASE", "PROD") {
		t.Error("expected true for case-insensitive match")
	}
	if envBool("NONEXISTENT", "PROD") {
		t.Error("expected false for nonexistent")
	}
}

func TestEnvStringSlice(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		expected []string
	}{
		{
			name:     "empty",
			envValue: "",
			expected: nil,
		},
		{
			name:     "single value",
			envValue: "abc",
			expected: []string{"abc"},
		},
		{
			name:     "multiple values",
			envValue: "abc,def,ghi",
			expected: []string{"abc", "def", "ghi"},
		},
		{
			name:     "with whitespace",
			envValue: "abc , def , ghi ",
			expected: []string{"abc", "def", "ghi"},
		},
		{
			name:     "empty elements filtered",
			envValue: "abc,,def,",
			expected: []string{"abc", "def"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Setenv("TEST_STRING_SLICE", tt.envValue)
			defer os.Unsetenv("TEST_STRING_SLICE")

			result := envStringSlice("TEST_STRING_SLICE")

			if tt.expected == nil {
				if result != nil {
					t.Errorf("expected nil, got %v", result)
				}
				return
			}

			if len(result) != len(tt.expected) {
				t.Errorf("expected %d elements, got %d", len(tt.expected), len(result))
				return
			}

			for i, v := range tt.expected {
				if result[i] != v {
					t.Errorf("expected %s at index %d, got %s", v, i, result[i])
				}
			}
		})
	}

	// Test nonexistent
	os.Unsetenv("TEST_NONEXISTENT_SLICE")
	if result := envStringSlice("TEST_NONEXISTENT_SLICE"); result != nil {
		t.Errorf("expected nil for nonexistent, got %v", result)
	}
}

func TestNormalizeWallets(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		expected []string
	}{
		{
			name:     "nil input",
			input:    nil,
			expected: nil,
		},
		{
			name:     "empty slice",
			input:    []string{},
			expected: []string{},
		},
		{
			name:     "lowercase input",
			input:    []string{"0xabc", "0xdef"},
			expected: []string{"0xabc", "0xdef"},
		},
		{
			name:     "mixed case input",
			input:    []string{"0xABC", "0xDeF"},
			expected: []string{"0xabc", "0xdef"},
		},
		{
			name:     "uppercase input",
			input:    []string{"0xABC123", "0xDEF456"},
			expected: []string{"0xabc123", "0xdef456"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeWallets(tt.input)

			if tt.expected == nil {
				if result != nil {
					t.Errorf("expected nil, got %v", result)
				}
				return
			}

			if len(result) != len(tt.expected) {
				t.Errorf("expected %d elements, got %d", len(tt.expected), len(result))
				return
			}

			for i, v := range tt.expected {
				if result[i] != v {
					t.Errorf("expected %s at index %d, got %s", v, i, result[i])
				}
			}
		})
	}
}

func TestLoad_FilteringOptions(t *testing.T) {
	// Clear any env vars
	os.Unsetenv("SPECIFIC_MARKETS")
	os.Unsetenv("SPECIFIC_MARKETS_ONLY")
	os.Unsetenv("SPECIFIC_WALLETS")

	// Test defaults
	cfg := Load()
	if cfg.Markets.SpecificMarkets != nil {
		t.Errorf("expected nil SpecificMarkets by default, got %v", cfg.Markets.SpecificMarkets)
	}
	if cfg.Markets.SpecificMarketsOnly {
		t.Error("expected SpecificMarketsOnly false by default")
	}
	if cfg.WalletFilter.SpecificWallets != nil {
		t.Errorf("expected nil SpecificWallets by default, got %v", cfg.WalletFilter.SpecificWallets)
	}

	// Test with values
	os.Setenv("SPECIFIC_MARKETS", "cond1,cond2,cond3")
	os.Setenv("SPECIFIC_MARKETS_ONLY", "true")
	os.Setenv("SPECIFIC_WALLETS", "0xABC,0xDEF")
	defer func() {
		os.Unsetenv("SPECIFIC_MARKETS")
		os.Unsetenv("SPECIFIC_MARKETS_ONLY")
		os.Unsetenv("SPECIFIC_WALLETS")
	}()

	cfg = Load()

	if len(cfg.Markets.SpecificMarkets) != 3 {
		t.Errorf("expected 3 specific markets, got %d", len(cfg.Markets.SpecificMarkets))
	}
	if !cfg.Markets.SpecificMarketsOnly {
		t.Error("expected SpecificMarketsOnly true")
	}
	if len(cfg.WalletFilter.SpecificWallets) != 2 {
		t.Errorf("expected 2 specific wallets, got %d", len(cfg.WalletFilter.SpecificWallets))
	}
	// Check wallet normalization (lowercase)
	if cfg.WalletFilter.SpecificWallets[0] != "0xabc" {
		t.Errorf("expected lowercase wallet, got %s", cfg.WalletFilter.SpecificWallets[0])
	}
}
