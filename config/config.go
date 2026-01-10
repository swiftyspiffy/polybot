package config

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all application configuration.
type Config struct {
	// Environment
	IsProd bool `json:"is_prod"`

	// Discord
	Discord DiscordConfig `json:"discord"`

	// Telegram
	Telegram TelegramConfig `json:"telegram"`

	// Trade monitoring
	TradeMonitor TradeMonitorConfig `json:"trade_monitor"`

	// Market fetching
	Markets MarketsConfig `json:"markets"`

	// Wallet filtering
	WalletFilter WalletFilterConfig `json:"wallet_filter"`

	// Cache persistence
	Cache CacheConfig `json:"cache"`

	// Contrarian winner tracking
	ContrarianCache ContrarianCacheConfig `json:"contrarian_cache"`

	// Hedge pattern tracking
	HedgeTracker HedgeTrackerConfig `json:"hedge_tracker"`

	// Advanced pattern tracking
	PatternTracker PatternTrackerConfig `json:"pattern_tracker"`

	// GitHub Gist - excluded from settings (env var only)
	Gist GistConfig `json:"-"`

	// Polymarket API
	Polymarket PolymarketConfig `json:"polymarket"`

	// Health server
	HealthServer HealthServerConfig `json:"health_server"`
}

// DiscordConfig holds Discord-related configuration.
type DiscordConfig struct {
	BotToken      string `json:"-"` // Excluded - env var only
	ProdChannelID string `json:"prod_channel_id"`
	BetaChannelID string `json:"beta_channel_id"`
}

// TelegramConfig holds Telegram-related configuration.
type TelegramConfig struct {
	BotToken   string `json:"-"` // Excluded - env var only
	ProdChatID string `json:"prod_chat_id"`
	BetaChatID string `json:"beta_chat_id"`
}

// TradeMonitorConfig holds trade monitoring configuration.
type TradeMonitorConfig struct {
	PollInterval     time.Duration `json:"poll_interval"`
	MinNotional      float64       `json:"min_notional"`
	MaxMarketsForLow int           `json:"max_markets_for_low"`
	UseWebSocket     bool          `json:"use_websocket"` // If false, use polling mode (default)

	// High win rate detection
	HighWinRateThreshold  float64 `json:"high_win_rate_threshold"`   // Minimum win rate to trigger alert (e.g., 0.70 = 70%)
	MinResolvedForWinRate int     `json:"min_resolved_for_win_rate"` // Minimum resolved positions to consider win rate
	WinRateMaxEntryPrice  float64 `json:"win_rate_max_entry_price"`  // Max entry price to count as "suspicious" win (e.g., 0.70 = ignore obvious 70¢+ bets)

	// Extreme odds detection (low price = longshot/contrarian bet)
	ExtremeLowPrice    float64 `json:"extreme_low_price"`    // Price threshold for "extreme low" (e.g., 0.05 = 5¢)
	ExtremeMinNotional float64 `json:"extreme_min_notional"` // Minimum notional for extreme odds alerts

	// Rapid trading detection
	RapidTradeWindow   time.Duration `json:"rapid_trade_window"`    // Time window to track trades (e.g., 5 minutes)
	RapidTradeMinCount int           `json:"rapid_trade_min_count"` // Minimum trades in window to trigger alert
	RapidTradeMinTotal float64       `json:"rapid_trade_min_total"` // Minimum total notional in window

	// New wallet detection
	NewWalletMaxMarkets  int     `json:"new_wallet_max_markets"`  // Max prior markets to be considered "new" (e.g., 1)
	NewWalletMinNotional float64 `json:"new_wallet_min_notional"` // Minimum notional for new wallet alerts

	// Contrarian bet detection
	ContrarianMaxPrice    float64 `json:"contrarian_max_price"`    // Max price to be considered "contrarian" (e.g., 0.10 = 10¢)
	ContrarianMinNotional float64 `json:"contrarian_min_notional"` // Minimum notional for contrarian bet alerts

	// Massive trade detection
	MassiveTradeMinNotional float64 `json:"massive_trade_min_notional"` // Minimum notional for massive trade alerts (e.g., 50000)
	MassiveTradeMaxPrice    float64 `json:"massive_trade_max_price"`    // Max entry price to alert on (e.g., 0.70 = ignore obvious 70¢+ bets)

	// Global obvious price filter
	ObviousPrice float64 `json:"obvious_price"` // Skip ALL alerts for trades at or above this price (e.g., 0.85 = skip 85¢+ trades)

	// Copy trading detection
	CopyTradeWindow       time.Duration `json:"copy_trade_window"`         // Time window after leader trade to detect copies (e.g., 10 min)
	CopyTradeMinCount     int           `json:"copy_trade_min_count"`      // Minimum copy trades to trigger alert (e.g., 3)
	CopyTradeLeaderMinWin float64       `json:"copy_trade_leader_min_win"` // Minimum win rate to be considered a leader (e.g., 0.70)
	CopyTradeLeaderMinRes int           `json:"copy_trade_leader_min_res"` // Minimum resolved positions for leader win rate
}

// MarketsConfig holds market fetching configuration.
type MarketsConfig struct {
	TopMarketsCount     int           `json:"top_markets_count"`
	RefreshInterval     time.Duration `json:"refresh_interval"`
	SpecificMarkets     []string      `json:"specific_markets"`      // Condition IDs to always monitor
	SpecificMarketsOnly bool          `json:"specific_markets_only"` // If true, only monitor specific markets (ignore top-N)
	Categories          []string      `json:"categories"`            // Tag slugs to filter by (e.g., "sports", "politics"). Empty = all categories
}

// CacheConfig holds cache persistence configuration.
type CacheConfig struct {
	WalletCacheTTL     time.Duration `json:"wallet_cache_ttl"`
	SaveInterval       time.Duration `json:"save_interval"`
	FileName           string        `json:"file_name"`
	SeenTradesFileName string        `json:"seen_trades_file_name"`
	MaxSizeBytes       int64         `json:"max_size_bytes"`
}

// ContrarianCacheConfig holds contrarian winner tracking configuration.
type ContrarianCacheConfig struct {
	GistID              string        `json:"-"` // Excluded - env var only
	FileName            string        `json:"file_name"`
	SaveInterval        time.Duration `json:"save_interval"`
	MaxSizeBytes        int64         `json:"max_size_bytes"`
	MinWins             int           `json:"min_wins"`           // Minimum contrarian wins to be considered (e.g., 3)
	MinContrarianRate   float64       `json:"min_contrarian_rate"` // Min % of wins that are contrarian (e.g., 0.70 = 70%)
	ContrarianThreshold float64       `json:"contrarian_threshold"` // Price threshold for contrarian (< this or > 1-this)
}

// HedgeTrackerConfig holds hedge pattern tracking configuration.
type HedgeTrackerConfig struct {
	GistID                  string        `json:"-"` // Excluded - env var only
	FileName                string        `json:"file_name"`
	SaveInterval            time.Duration `json:"save_interval"`
	MinHedgeSize            float64       `json:"min_hedge_size"`             // Minimum shares on each side to be considered hedged (e.g., 100)
	MinHedgeValue           float64       `json:"min_hedge_value"`            // Minimum USD value on each side (e.g., $500)
	SignificantSellPct      float64       `json:"significant_sell_pct"`       // Percentage sell to trigger alert (e.g., 0.50 = 50%)
	PositionCheckInterval   time.Duration `json:"position_check_interval"`    // Cooldown between position checks for same wallet+market
	MaxPositionChecks       int           `json:"max_position_checks"`        // Rate limit for position API calls per minute
	MinExitsForAsymmetric   int           `json:"min_exits_for_asymmetric"`   // Minimum exits to detect asymmetric pattern
	AsymmetricThreshold     float64       `json:"asymmetric_threshold"`       // Ratio threshold (e.g., 2.0 = exits winners 2x faster)
	ResolutionCheckInterval time.Duration `json:"resolution_check_interval"`  // How often to check pending events for resolution
}

// PatternTrackerConfig holds advanced pattern detection configuration.
type PatternTrackerConfig struct {
	GistID       string        `json:"-"` // Excluded - env var only
	FileName     string        `json:"file_name"`
	SaveInterval time.Duration `json:"save_interval"`

	// Conviction Doubling
	ConvictionMinAddSize    float64       `json:"conviction_min_add_size"`    // Min shares to add (e.g., 500)
	ConvictionMinAddValue   float64       `json:"conviction_min_add_value"`   // Min USD value to add (e.g., 1000)
	ConvictionMinLossPct    float64       `json:"conviction_min_loss_pct"`    // Min unrealized loss % (e.g., 0.10 = 10%)
	ConvictionCheckInterval time.Duration `json:"conviction_check_interval"`  // Cooldown per wallet+market

	// Perfect Exit Timing
	PerfectExitCheckDelay    time.Duration `json:"perfect_exit_check_delay"`    // How long to wait before checking (e.g., 24h)
	PerfectExitMinExits      int           `json:"perfect_exit_min_exits"`      // Min exits to analyze (e.g., 5)
	PerfectExitMinScore      float64       `json:"perfect_exit_min_score"`      // Min avg timing score (e.g., 0.90)
	PerfectExitCheckInterval time.Duration `json:"perfect_exit_check_interval"` // How often to check pending exits

	// Stealth Accumulation
	StealthTimeWindow       time.Duration `json:"stealth_time_window"`        // Window to track accumulation (e.g., 6h)
	StealthMinTrades        int           `json:"stealth_min_trades"`         // Min trades to detect pattern (e.g., 3)
	StealthMinTotalSize     float64       `json:"stealth_min_total_size"`     // Min total shares accumulated (e.g., 5000)
	StealthMinTotalValue    float64       `json:"stealth_min_total_value"`    // Min total USD value (e.g., 10000)
	StealthMaxSingleTrade   float64       `json:"stealth_max_single_trade"`   // Max single trade to be "stealth" (e.g., 25000)
	StealthMinSpreadMinutes int           `json:"stealth_min_spread_minutes"` // Min time spread between first/last (e.g., 60)

	// Pre-Move Positioning
	PreMoveCheckDelay    time.Duration `json:"pre_move_check_delay"`    // How long to wait before checking (e.g., 4h)
	PreMoveMinNotional   float64       `json:"pre_move_min_notional"`   // Min trade size to track (e.g., 5000)
	PreMoveMinMoveSize   float64       `json:"pre_move_min_move_size"`  // Min price move to count as "successful" (e.g., 0.10 = 10%)
	PreMoveMinTrades     int           `json:"pre_move_min_trades"`     // Min trades to calculate alpha (e.g., 10)
	PreMoveMinAlpha      float64       `json:"pre_move_min_alpha"`      // Min success rate to alert (e.g., 0.70 = 70%)
	PreMoveCheckInterval time.Duration `json:"pre_move_check_interval"` // How often to verify pending (e.g., 30m)
	PreMoveAlertCooldown time.Duration `json:"pre_move_alert_cooldown"` // Min time between alerts per wallet (e.g., 24h)

	// Rate limiting
	PositionCheckInterval time.Duration `json:"position_check_interval"`
	MaxPositionChecks     int           `json:"max_position_checks"`
}

// GistConfig holds GitHub Gist configuration.
type GistConfig struct {
	Token       string `json:"-"` // Excluded - env var only
	GistID      string `json:"-"` // Excluded - env var only
	TasksGistID string `json:"-"` // Excluded - env var only - for tasks feature
}

// PolymarketConfig holds Polymarket API configuration.
type PolymarketConfig struct {
	GammaAPIURL string `json:"gamma_api_url"`
	DataAPIURL  string `json:"data_api_url"`
}

// HealthServerConfig holds health check server configuration.
type HealthServerConfig struct {
	Enabled bool `json:"enabled"`
	Port    int  `json:"port"`
}

// WalletFilterConfig holds wallet filtering configuration.
type WalletFilterConfig struct {
	SpecificWallets []string `json:"specific_wallets"` // Wallet addresses to monitor (empty = all)
}

// Clone creates a deep copy of the config.
func (c *Config) Clone() *Config {
	if c == nil {
		return nil
	}
	clone := *c
	// Deep copy slices
	if c.Markets.SpecificMarkets != nil {
		clone.Markets.SpecificMarkets = make([]string, len(c.Markets.SpecificMarkets))
		copy(clone.Markets.SpecificMarkets, c.Markets.SpecificMarkets)
	}
	if c.Markets.Categories != nil {
		clone.Markets.Categories = make([]string, len(c.Markets.Categories))
		copy(clone.Markets.Categories, c.Markets.Categories)
	}
	if c.WalletFilter.SpecificWallets != nil {
		clone.WalletFilter.SpecificWallets = make([]string, len(c.WalletFilter.SpecificWallets))
		copy(clone.WalletFilter.SpecificWallets, c.WalletFilter.SpecificWallets)
	}
	return &clone
}

// ToJSON serializes the config to JSON.
func (c *Config) ToJSON() ([]byte, error) {
	return json.MarshalIndent(c, "", "  ")
}

// ConfigFromJSON deserializes JSON into a config, merging with base.
func ConfigFromJSON(data []byte, base *Config) (*Config, error) {
	if base == nil {
		base = Defaults()
	}
	cfg := base.Clone()
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Defaults returns a config with hardcoded default values.
func Defaults() *Config {
	return &Config{
		IsProd: false,
		Discord: DiscordConfig{
			ProdChannelID: "",
			BetaChannelID: "",
		},
		Telegram: TelegramConfig{},
		TradeMonitor: TradeMonitorConfig{
			PollInterval:          10 * time.Second,
			MinNotional:           4000.0,
			MaxMarketsForLow:      5,
			UseWebSocket:          true,
			HighWinRateThreshold:  0.90,
			MinResolvedForWinRate: 5,
			WinRateMaxEntryPrice:  0.70,
			ExtremeLowPrice:       0.03,
			ExtremeMinNotional:    2500.0,
			RapidTradeWindow:      5 * time.Minute,
			RapidTradeMinCount:    3,
			RapidTradeMinTotal:    5000.0,
			NewWalletMaxMarkets:   1,
			NewWalletMinNotional:  10000.0,
			ContrarianMaxPrice:    0.10,
			ContrarianMinNotional: 5000.0,
			MassiveTradeMinNotional: 50000.0,
			MassiveTradeMaxPrice:    0.70,
			ObviousPrice:            0.75,
			CopyTradeWindow:         10 * time.Minute,
			CopyTradeMinCount:       3,
			CopyTradeLeaderMinWin:   0.70,
			CopyTradeLeaderMinRes:   5,
		},
		Markets: MarketsConfig{
			TopMarketsCount:     20,
			RefreshInterval:     1 * time.Minute,
			SpecificMarketsOnly: true,
		},
		WalletFilter: WalletFilterConfig{},
		Cache: CacheConfig{
			WalletCacheTTL:     1 * time.Minute,
			SaveInterval:       10 * time.Minute,
			FileName:           "wallet_cache.json",
			SeenTradesFileName: "seen_trades.json",
			MaxSizeBytes:       50 * 1024 * 1024,
		},
		ContrarianCache: ContrarianCacheConfig{
			FileName:            "contrarian_winners.txt",
			SaveInterval:        5 * time.Minute,
			MaxSizeBytes:        50 * 1024 * 1024,
			MinWins:             3,
			MinContrarianRate:   0.70,
			ContrarianThreshold: 0.20,
		},
		HedgeTracker: HedgeTrackerConfig{
			FileName:                "hedge_tracker.json",
			SaveInterval:            5 * time.Minute,
			MinHedgeSize:            100.0,
			MinHedgeValue:           500.0,
			SignificantSellPct:      0.50,
			PositionCheckInterval:   5 * time.Minute,
			MaxPositionChecks:       60,
			MinExitsForAsymmetric:   5,
			AsymmetricThreshold:     2.0,
			ResolutionCheckInterval: 1 * time.Hour,
		},
		PatternTracker: PatternTrackerConfig{
			FileName:     "pattern_tracker.json",
			SaveInterval: 5 * time.Minute,
			ConvictionMinAddSize:    500,
			ConvictionMinAddValue:   1000,
			ConvictionMinLossPct:    0.10,
			ConvictionCheckInterval: 5 * time.Minute,
			PerfectExitCheckDelay:    24 * time.Hour,
			PerfectExitMinExits:      5,
			PerfectExitMinScore:      0.90,
			PerfectExitCheckInterval: 1 * time.Hour,
			StealthTimeWindow:       6 * time.Hour,
			StealthMinTrades:        3,
			StealthMinTotalSize:     5000,
			StealthMinTotalValue:    10000,
			StealthMaxSingleTrade:   25000,
			StealthMinSpreadMinutes: 60,
			PreMoveCheckDelay:    4 * time.Hour,
			PreMoveMinNotional:   5000,
			PreMoveMinMoveSize:   0.10,
			PreMoveMinTrades:     10,
			PreMoveMinAlpha:      0.70,
			PreMoveCheckInterval: 30 * time.Minute,
			PreMoveAlertCooldown: 24 * time.Hour,
			PositionCheckInterval: 5 * time.Minute,
			MaxPositionChecks:     60,
		},
		Polymarket: PolymarketConfig{
			GammaAPIURL: "https://gamma-api.polymarket.com",
			DataAPIURL:  "https://data-api.polymarket.com",
		},
		HealthServer: HealthServerConfig{
			Enabled: true,
			Port:    8080,
		},
	}
}

// Load loads configuration from environment variables with defaults.
func Load() *Config {
	return &Config{
		IsProd: envBool("STAGE", "PROD"),

		Discord: DiscordConfig{
			BotToken:      envString("DISCORD_BOT_TOKEN", ""),
			ProdChannelID: envString("DISCORD_PROD_CHANNEL_ID", ""),
			BetaChannelID: envString("DISCORD_BETA_CHANNEL_ID", ""),
		},

		Telegram: TelegramConfig{
			BotToken:   envString("TELEGRAM_BOT_KEY", ""),
			ProdChatID: envString("TELEGRAM_PROD_CHAT_ID", ""),
			BetaChatID: envString("TELEGRAM_BETA_CHAT_ID", ""),
		},

		TradeMonitor: TradeMonitorConfig{
			PollInterval:          envDuration("TRADE_POLL_INTERVAL", 10*time.Second),
			MinNotional:           envFloat("TRADE_MIN_NOTIONAL", 4000.0),
			MaxMarketsForLow:      envInt("TRADE_MAX_MARKETS_FOR_LOW", 5),
			UseWebSocket:          envBool("USE_WEBSOCKET", "true"),
			HighWinRateThreshold:   envFloat("TRADE_HIGH_WIN_RATE", 0.90),
			MinResolvedForWinRate:  envInt("TRADE_MIN_RESOLVED_FOR_WIN_RATE", 5),
			WinRateMaxEntryPrice:   envFloat("TRADE_WIN_RATE_MAX_ENTRY_PRICE", 0.70),
			ExtremeLowPrice:       envFloat("TRADE_EXTREME_LOW_PRICE", 0.03),
			ExtremeMinNotional:    envFloat("TRADE_EXTREME_MIN_NOTIONAL", 2500.0),
			RapidTradeWindow:      envDuration("TRADE_RAPID_WINDOW", 5*time.Minute),
			RapidTradeMinCount:    envInt("TRADE_RAPID_MIN_COUNT", 3),
			RapidTradeMinTotal:    envFloat("TRADE_RAPID_MIN_TOTAL", 5000.0),
			NewWalletMaxMarkets:   envInt("TRADE_NEW_WALLET_MAX_MARKETS", 1),
			NewWalletMinNotional:  envFloat("TRADE_NEW_WALLET_MIN_NOTIONAL", 10000.0),
			ContrarianMaxPrice:      envFloat("TRADE_CONTRARIAN_MAX_PRICE", 0.10),
			ContrarianMinNotional:   envFloat("TRADE_CONTRARIAN_MIN_NOTIONAL", 5000.0),
			MassiveTradeMinNotional: envFloat("TRADE_MASSIVE_MIN_NOTIONAL", 50000.0),
			MassiveTradeMaxPrice:    envFloat("TRADE_MASSIVE_MAX_PRICE", 0.70),
			ObviousPrice:            envFloat("TRADE_OBVIOUS_PRICE", 0.75),
			CopyTradeWindow:         envDuration("COPY_TRADE_WINDOW", 10*time.Minute),
			CopyTradeMinCount:       envInt("COPY_TRADE_MIN_COUNT", 3),
			CopyTradeLeaderMinWin:   envFloat("COPY_TRADE_LEADER_MIN_WIN", 0.70),
			CopyTradeLeaderMinRes:   envInt("COPY_TRADE_LEADER_MIN_RESOLVED", 5),
		},

		Markets: MarketsConfig{
			TopMarketsCount:     envInt("TOP_MARKETS_COUNT", 20),
			RefreshInterval:     envDuration("MARKET_REFRESH_INTERVAL", 1*time.Minute),
			SpecificMarkets:     envStringSlice("SPECIFIC_MARKETS"),
			SpecificMarketsOnly: envBool("SPECIFIC_MARKETS_ONLY", "true"),
			Categories:          envStringSlice("MARKET_CATEGORIES"),
		},

		WalletFilter: WalletFilterConfig{
			SpecificWallets: normalizeWallets(envStringSlice("SPECIFIC_WALLETS")),
		},

		Cache: CacheConfig{
			WalletCacheTTL:     envDuration("WALLET_CACHE_TTL", 1*time.Minute),
			SaveInterval:       envDuration("CACHE_SAVE_INTERVAL", 10*time.Minute),
			FileName:           envString("CACHE_FILE_NAME", "wallet_cache.json"),
			SeenTradesFileName: envString("SEEN_TRADES_FILE_NAME", "seen_trades.json"),
			MaxSizeBytes:       envInt64("CACHE_MAX_SIZE_BYTES", 50*1024*1024), // 50MB
		},

		ContrarianCache: ContrarianCacheConfig{
			GistID:              envString("CONTRARIAN_CACHE_GIST_ID", ""),
			FileName:            envString("CONTRARIAN_CACHE_FILE_NAME", "contrarian_winners.txt"),
			SaveInterval:        envDuration("CONTRARIAN_CACHE_SAVE_INTERVAL", 5*time.Minute),
			MaxSizeBytes:        envInt64("CONTRARIAN_CACHE_MAX_SIZE_BYTES", 50*1024*1024), // 50MB
			MinWins:             envInt("CONTRARIAN_MIN_WINS", 3),
			MinContrarianRate:   envFloat("CONTRARIAN_MIN_RATE", 0.70),       // 70% of wins must be contrarian
			ContrarianThreshold: envFloat("CONTRARIAN_THRESHOLD", 0.20),      // < 20% or > 80%
		},

		HedgeTracker: HedgeTrackerConfig{
			GistID:                  envString("HEDGE_TRACKER_GIST_ID", ""),
			FileName:                envString("HEDGE_TRACKER_FILE_NAME", "hedge_tracker.json"),
			SaveInterval:            envDuration("HEDGE_TRACKER_SAVE_INTERVAL", 5*time.Minute),
			MinHedgeSize:            envFloat("HEDGE_MIN_SIZE", 100.0),            // 100 shares minimum
			MinHedgeValue:           envFloat("HEDGE_MIN_VALUE", 500.0),           // $500 minimum per side
			SignificantSellPct:      envFloat("HEDGE_SIGNIFICANT_SELL_PCT", 0.50), // 50% sell triggers alert
			PositionCheckInterval:   envDuration("HEDGE_POSITION_CHECK_INTERVAL", 5*time.Minute),
			MaxPositionChecks:       envInt("HEDGE_MAX_POSITION_CHECKS", 60),
			MinExitsForAsymmetric:   envInt("HEDGE_MIN_EXITS_FOR_ASYMMETRIC", 5),
			AsymmetricThreshold:     envFloat("HEDGE_ASYMMETRIC_THRESHOLD", 2.0), // 2x faster exits
			ResolutionCheckInterval: envDuration("HEDGE_RESOLUTION_CHECK_INTERVAL", 1*time.Hour),
		},

		PatternTracker: PatternTrackerConfig{
			GistID:       envString("PATTERN_TRACKER_GIST_ID", ""),
			FileName:     envString("PATTERN_TRACKER_FILE_NAME", "pattern_tracker.json"),
			SaveInterval: envDuration("PATTERN_TRACKER_SAVE_INTERVAL", 5*time.Minute),

			// Conviction Doubling
			ConvictionMinAddSize:    envFloat("CONVICTION_MIN_ADD_SIZE", 500),
			ConvictionMinAddValue:   envFloat("CONVICTION_MIN_ADD_VALUE", 1000),
			ConvictionMinLossPct:    envFloat("CONVICTION_MIN_LOSS_PCT", 0.10),
			ConvictionCheckInterval: envDuration("CONVICTION_CHECK_INTERVAL", 5*time.Minute),

			// Perfect Exit Timing
			PerfectExitCheckDelay:    envDuration("PERFECT_EXIT_CHECK_DELAY", 24*time.Hour),
			PerfectExitMinExits:      envInt("PERFECT_EXIT_MIN_EXITS", 5),
			PerfectExitMinScore:      envFloat("PERFECT_EXIT_MIN_SCORE", 0.90),
			PerfectExitCheckInterval: envDuration("PERFECT_EXIT_CHECK_INTERVAL", 1*time.Hour),

			// Stealth Accumulation
			StealthTimeWindow:       envDuration("STEALTH_TIME_WINDOW", 6*time.Hour),
			StealthMinTrades:        envInt("STEALTH_MIN_TRADES", 3),
			StealthMinTotalSize:     envFloat("STEALTH_MIN_TOTAL_SIZE", 5000),
			StealthMinTotalValue:    envFloat("STEALTH_MIN_TOTAL_VALUE", 10000),
			StealthMaxSingleTrade:   envFloat("STEALTH_MAX_SINGLE_TRADE", 25000),
			StealthMinSpreadMinutes: envInt("STEALTH_MIN_SPREAD_MINUTES", 60),

			// Pre-Move Positioning
			PreMoveCheckDelay:    envDuration("PRE_MOVE_CHECK_DELAY", 4*time.Hour),
			PreMoveMinNotional:   envFloat("PRE_MOVE_MIN_NOTIONAL", 5000),
			PreMoveMinMoveSize:   envFloat("PRE_MOVE_MIN_MOVE_SIZE", 0.10),
			PreMoveMinTrades:     envInt("PRE_MOVE_MIN_TRADES", 10),
			PreMoveMinAlpha:      envFloat("PRE_MOVE_MIN_ALPHA", 0.70),
			PreMoveCheckInterval: envDuration("PRE_MOVE_CHECK_INTERVAL", 30*time.Minute),
			PreMoveAlertCooldown: envDuration("PRE_MOVE_ALERT_COOLDOWN", 24*time.Hour),

			// Rate limiting
			PositionCheckInterval: envDuration("PATTERN_POSITION_CHECK_INTERVAL", 5*time.Minute),
			MaxPositionChecks:     envInt("PATTERN_MAX_POSITION_CHECKS", 60),
		},

		Gist: GistConfig{
			Token:       envString("GITHUB_TOKEN", ""),
			GistID:      envString("CACHE_GIST_ID", ""),
			TasksGistID: envString("TASKS_GIST_ID", ""),
		},

		Polymarket: PolymarketConfig{
			GammaAPIURL: envString("POLYMARKET_GAMMA_API_URL", "https://gamma-api.polymarket.com"),
			DataAPIURL:  envString("POLYMARKET_DATA_API_URL", "https://data-api.polymarket.com"),
		},

		HealthServer: HealthServerConfig{
			Enabled: envBoolDefault("HEALTH_SERVER_ENABLED", true),
			Port:    envInt("HEALTH_SERVER_PORT", 8080),
		},
	}
}

// Helper functions for parsing environment variables

func envString(key, defaultVal string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return defaultVal
}

func envInt(key string, defaultVal int) int {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return defaultVal
}

func envInt64(key string, defaultVal int64) int64 {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if i, err := strconv.ParseInt(v, 10, 64); err == nil {
			return i
		}
	}
	return defaultVal
}

func envFloat(key string, defaultVal float64) float64 {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return defaultVal
}

func envDuration(key string, defaultVal time.Duration) time.Duration {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return defaultVal
}

func envBool(key, trueValue string) bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(key)), trueValue)
}

func envBoolDefault(key string, defaultVal bool) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return defaultVal
	}
	return strings.EqualFold(v, "true") || strings.EqualFold(v, "1") || strings.EqualFold(v, "yes")
}

func envStringSlice(key string) []string {
	val := os.Getenv(key)
	if val == "" {
		return nil
	}
	parts := strings.Split(val, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

func envStringSliceDefault(key string, defaultVal []string) []string {
	val := os.Getenv(key)
	if val == "" {
		return defaultVal
	}
	parts := strings.Split(val, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

func normalizeWallets(wallets []string) []string {
	if wallets == nil {
		return nil
	}
	result := make([]string, len(wallets))
	for i, w := range wallets {
		result[i] = strings.ToLower(w)
	}
	return result
}
