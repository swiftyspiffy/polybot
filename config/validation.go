package config

import (
	"fmt"
	"time"
)

// ValidationError represents a validation error for a specific field.
type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// ValidationResult holds the result of config validation.
type ValidationResult struct {
	Valid  bool              `json:"valid"`
	Errors []ValidationError `json:"errors,omitempty"`
}

// Validate checks the config for invalid values.
func (c *Config) Validate() ValidationResult {
	var errors []ValidationError

	// TradeMonitor validation
	errors = append(errors, validateTradeMonitor(&c.TradeMonitor)...)

	// Markets validation
	errors = append(errors, validateMarkets(&c.Markets)...)

	// Cache validation
	errors = append(errors, validateCache(&c.Cache)...)

	// ContrarianCache validation
	errors = append(errors, validateContrarianCache(&c.ContrarianCache)...)

	// HedgeTracker validation
	errors = append(errors, validateHedgeTracker(&c.HedgeTracker)...)

	// PatternTracker validation
	errors = append(errors, validatePatternTracker(&c.PatternTracker)...)

	// HealthServer validation
	errors = append(errors, validateHealthServer(&c.HealthServer)...)

	return ValidationResult{
		Valid:  len(errors) == 0,
		Errors: errors,
	}
}

func validateTradeMonitor(tm *TradeMonitorConfig) []ValidationError {
	var errors []ValidationError

	if tm.PollInterval < 1*time.Second {
		errors = append(errors, ValidationError{
			Field:   "trade_monitor.poll_interval",
			Message: "must be at least 1 second",
		})
	}

	if tm.MinNotional < 0 {
		errors = append(errors, ValidationError{
			Field:   "trade_monitor.min_notional",
			Message: "must be non-negative",
		})
	}

	if tm.MaxMarketsForLow < 1 {
		errors = append(errors, ValidationError{
			Field:   "trade_monitor.max_markets_for_low",
			Message: "must be at least 1",
		})
	}

	if tm.HighWinRateThreshold < 0 || tm.HighWinRateThreshold > 1 {
		errors = append(errors, ValidationError{
			Field:   "trade_monitor.high_win_rate_threshold",
			Message: "must be between 0 and 1",
		})
	}

	if tm.MinResolvedForWinRate < 1 {
		errors = append(errors, ValidationError{
			Field:   "trade_monitor.min_resolved_for_win_rate",
			Message: "must be at least 1",
		})
	}

	if tm.WinRateMaxEntryPrice < 0 || tm.WinRateMaxEntryPrice > 1 {
		errors = append(errors, ValidationError{
			Field:   "trade_monitor.win_rate_max_entry_price",
			Message: "must be between 0 and 1",
		})
	}

	if tm.ExtremeLowPrice < 0 || tm.ExtremeLowPrice > 1 {
		errors = append(errors, ValidationError{
			Field:   "trade_monitor.extreme_low_price",
			Message: "must be between 0 and 1",
		})
	}

	if tm.ExtremeMinNotional < 0 {
		errors = append(errors, ValidationError{
			Field:   "trade_monitor.extreme_min_notional",
			Message: "must be non-negative",
		})
	}

	if tm.RapidTradeWindow < 1*time.Second {
		errors = append(errors, ValidationError{
			Field:   "trade_monitor.rapid_trade_window",
			Message: "must be at least 1 second",
		})
	}

	if tm.RapidTradeMinCount < 1 {
		errors = append(errors, ValidationError{
			Field:   "trade_monitor.rapid_trade_min_count",
			Message: "must be at least 1",
		})
	}

	if tm.RapidTradeMinTotal < 0 {
		errors = append(errors, ValidationError{
			Field:   "trade_monitor.rapid_trade_min_total",
			Message: "must be non-negative",
		})
	}

	if tm.NewWalletMaxMarkets < 0 {
		errors = append(errors, ValidationError{
			Field:   "trade_monitor.new_wallet_max_markets",
			Message: "must be non-negative",
		})
	}

	if tm.NewWalletMinNotional < 0 {
		errors = append(errors, ValidationError{
			Field:   "trade_monitor.new_wallet_min_notional",
			Message: "must be non-negative",
		})
	}

	if tm.ContrarianMaxPrice < 0 || tm.ContrarianMaxPrice > 1 {
		errors = append(errors, ValidationError{
			Field:   "trade_monitor.contrarian_max_price",
			Message: "must be between 0 and 1",
		})
	}

	if tm.ContrarianMinNotional < 0 {
		errors = append(errors, ValidationError{
			Field:   "trade_monitor.contrarian_min_notional",
			Message: "must be non-negative",
		})
	}

	if tm.MassiveTradeMinNotional < 0 {
		errors = append(errors, ValidationError{
			Field:   "trade_monitor.massive_trade_min_notional",
			Message: "must be non-negative",
		})
	}

	if tm.MassiveTradeMaxPrice < 0 || tm.MassiveTradeMaxPrice > 1 {
		errors = append(errors, ValidationError{
			Field:   "trade_monitor.massive_trade_max_price",
			Message: "must be between 0 and 1",
		})
	}

	if tm.ObviousPrice < 0 || tm.ObviousPrice > 1 {
		errors = append(errors, ValidationError{
			Field:   "trade_monitor.obvious_price",
			Message: "must be between 0 and 1",
		})
	}

	if tm.CopyTradeWindow < 1*time.Second {
		errors = append(errors, ValidationError{
			Field:   "trade_monitor.copy_trade_window",
			Message: "must be at least 1 second",
		})
	}

	if tm.CopyTradeMinCount < 1 {
		errors = append(errors, ValidationError{
			Field:   "trade_monitor.copy_trade_min_count",
			Message: "must be at least 1",
		})
	}

	if tm.CopyTradeLeaderMinWin < 0 || tm.CopyTradeLeaderMinWin > 1 {
		errors = append(errors, ValidationError{
			Field:   "trade_monitor.copy_trade_leader_min_win",
			Message: "must be between 0 and 1",
		})
	}

	if tm.CopyTradeLeaderMinRes < 1 {
		errors = append(errors, ValidationError{
			Field:   "trade_monitor.copy_trade_leader_min_res",
			Message: "must be at least 1",
		})
	}

	return errors
}

func validateMarkets(m *MarketsConfig) []ValidationError {
	var errors []ValidationError

	if m.TopMarketsCount < 1 {
		errors = append(errors, ValidationError{
			Field:   "markets.top_markets_count",
			Message: "must be at least 1",
		})
	}

	if m.RefreshInterval < 10*time.Second {
		errors = append(errors, ValidationError{
			Field:   "markets.refresh_interval",
			Message: "must be at least 10 seconds",
		})
	}

	return errors
}

func validateCache(c *CacheConfig) []ValidationError {
	var errors []ValidationError

	if c.WalletCacheTTL < 1*time.Second {
		errors = append(errors, ValidationError{
			Field:   "cache.wallet_cache_ttl",
			Message: "must be at least 1 second",
		})
	}

	if c.SaveInterval < 1*time.Second {
		errors = append(errors, ValidationError{
			Field:   "cache.save_interval",
			Message: "must be at least 1 second",
		})
	}

	if c.MaxSizeBytes < 1024 {
		errors = append(errors, ValidationError{
			Field:   "cache.max_size_bytes",
			Message: "must be at least 1KB",
		})
	}

	return errors
}

func validateContrarianCache(cc *ContrarianCacheConfig) []ValidationError {
	var errors []ValidationError

	if cc.SaveInterval < 1*time.Second {
		errors = append(errors, ValidationError{
			Field:   "contrarian_cache.save_interval",
			Message: "must be at least 1 second",
		})
	}

	if cc.MinWins < 1 {
		errors = append(errors, ValidationError{
			Field:   "contrarian_cache.min_wins",
			Message: "must be at least 1",
		})
	}

	if cc.MinContrarianRate < 0 || cc.MinContrarianRate > 1 {
		errors = append(errors, ValidationError{
			Field:   "contrarian_cache.min_contrarian_rate",
			Message: "must be between 0 and 1",
		})
	}

	if cc.ContrarianThreshold < 0 || cc.ContrarianThreshold > 0.5 {
		errors = append(errors, ValidationError{
			Field:   "contrarian_cache.contrarian_threshold",
			Message: "must be between 0 and 0.5",
		})
	}

	return errors
}

func validateHedgeTracker(ht *HedgeTrackerConfig) []ValidationError {
	var errors []ValidationError

	if ht.SaveInterval < 1*time.Second {
		errors = append(errors, ValidationError{
			Field:   "hedge_tracker.save_interval",
			Message: "must be at least 1 second",
		})
	}

	if ht.MinHedgeSize < 0 {
		errors = append(errors, ValidationError{
			Field:   "hedge_tracker.min_hedge_size",
			Message: "must be non-negative",
		})
	}

	if ht.MinHedgeValue < 0 {
		errors = append(errors, ValidationError{
			Field:   "hedge_tracker.min_hedge_value",
			Message: "must be non-negative",
		})
	}

	if ht.SignificantSellPct < 0 || ht.SignificantSellPct > 1 {
		errors = append(errors, ValidationError{
			Field:   "hedge_tracker.significant_sell_pct",
			Message: "must be between 0 and 1",
		})
	}

	if ht.PositionCheckInterval < 1*time.Second {
		errors = append(errors, ValidationError{
			Field:   "hedge_tracker.position_check_interval",
			Message: "must be at least 1 second",
		})
	}

	if ht.MaxPositionChecks < 1 {
		errors = append(errors, ValidationError{
			Field:   "hedge_tracker.max_position_checks",
			Message: "must be at least 1",
		})
	}

	if ht.MinExitsForAsymmetric < 1 {
		errors = append(errors, ValidationError{
			Field:   "hedge_tracker.min_exits_for_asymmetric",
			Message: "must be at least 1",
		})
	}

	if ht.AsymmetricThreshold < 1 {
		errors = append(errors, ValidationError{
			Field:   "hedge_tracker.asymmetric_threshold",
			Message: "must be at least 1",
		})
	}

	if ht.ResolutionCheckInterval < 1*time.Minute {
		errors = append(errors, ValidationError{
			Field:   "hedge_tracker.resolution_check_interval",
			Message: "must be at least 1 minute",
		})
	}

	return errors
}

func validatePatternTracker(pt *PatternTrackerConfig) []ValidationError {
	var errors []ValidationError

	if pt.SaveInterval < 1*time.Second {
		errors = append(errors, ValidationError{
			Field:   "pattern_tracker.save_interval",
			Message: "must be at least 1 second",
		})
	}

	// Conviction Doubling
	if pt.ConvictionMinAddSize < 0 {
		errors = append(errors, ValidationError{
			Field:   "pattern_tracker.conviction_min_add_size",
			Message: "must be non-negative",
		})
	}

	if pt.ConvictionMinAddValue < 0 {
		errors = append(errors, ValidationError{
			Field:   "pattern_tracker.conviction_min_add_value",
			Message: "must be non-negative",
		})
	}

	if pt.ConvictionMinLossPct < 0 || pt.ConvictionMinLossPct > 1 {
		errors = append(errors, ValidationError{
			Field:   "pattern_tracker.conviction_min_loss_pct",
			Message: "must be between 0 and 1",
		})
	}

	if pt.ConvictionCheckInterval < 1*time.Second {
		errors = append(errors, ValidationError{
			Field:   "pattern_tracker.conviction_check_interval",
			Message: "must be at least 1 second",
		})
	}

	// Perfect Exit Timing
	if pt.PerfectExitCheckDelay < 1*time.Minute {
		errors = append(errors, ValidationError{
			Field:   "pattern_tracker.perfect_exit_check_delay",
			Message: "must be at least 1 minute",
		})
	}

	if pt.PerfectExitMinExits < 1 {
		errors = append(errors, ValidationError{
			Field:   "pattern_tracker.perfect_exit_min_exits",
			Message: "must be at least 1",
		})
	}

	if pt.PerfectExitMinScore < 0 || pt.PerfectExitMinScore > 1 {
		errors = append(errors, ValidationError{
			Field:   "pattern_tracker.perfect_exit_min_score",
			Message: "must be between 0 and 1",
		})
	}

	if pt.PerfectExitCheckInterval < 1*time.Minute {
		errors = append(errors, ValidationError{
			Field:   "pattern_tracker.perfect_exit_check_interval",
			Message: "must be at least 1 minute",
		})
	}

	// Stealth Accumulation
	if pt.StealthTimeWindow < 1*time.Minute {
		errors = append(errors, ValidationError{
			Field:   "pattern_tracker.stealth_time_window",
			Message: "must be at least 1 minute",
		})
	}

	if pt.StealthMinTrades < 2 {
		errors = append(errors, ValidationError{
			Field:   "pattern_tracker.stealth_min_trades",
			Message: "must be at least 2",
		})
	}

	if pt.StealthMinTotalSize < 0 {
		errors = append(errors, ValidationError{
			Field:   "pattern_tracker.stealth_min_total_size",
			Message: "must be non-negative",
		})
	}

	if pt.StealthMinTotalValue < 0 {
		errors = append(errors, ValidationError{
			Field:   "pattern_tracker.stealth_min_total_value",
			Message: "must be non-negative",
		})
	}

	if pt.StealthMaxSingleTrade < 0 {
		errors = append(errors, ValidationError{
			Field:   "pattern_tracker.stealth_max_single_trade",
			Message: "must be non-negative",
		})
	}

	if pt.StealthMinSpreadMinutes < 1 {
		errors = append(errors, ValidationError{
			Field:   "pattern_tracker.stealth_min_spread_minutes",
			Message: "must be at least 1",
		})
	}

	// Pre-Move Positioning
	if pt.PreMoveCheckDelay < 1*time.Minute {
		errors = append(errors, ValidationError{
			Field:   "pattern_tracker.pre_move_check_delay",
			Message: "must be at least 1 minute",
		})
	}

	if pt.PreMoveMinNotional < 0 {
		errors = append(errors, ValidationError{
			Field:   "pattern_tracker.pre_move_min_notional",
			Message: "must be non-negative",
		})
	}

	if pt.PreMoveMinMoveSize < 0 || pt.PreMoveMinMoveSize > 1 {
		errors = append(errors, ValidationError{
			Field:   "pattern_tracker.pre_move_min_move_size",
			Message: "must be between 0 and 1",
		})
	}

	if pt.PreMoveMinTrades < 1 {
		errors = append(errors, ValidationError{
			Field:   "pattern_tracker.pre_move_min_trades",
			Message: "must be at least 1",
		})
	}

	if pt.PreMoveMinAlpha < 0 || pt.PreMoveMinAlpha > 1 {
		errors = append(errors, ValidationError{
			Field:   "pattern_tracker.pre_move_min_alpha",
			Message: "must be between 0 and 1",
		})
	}

	if pt.PreMoveCheckInterval < 1*time.Minute {
		errors = append(errors, ValidationError{
			Field:   "pattern_tracker.pre_move_check_interval",
			Message: "must be at least 1 minute",
		})
	}

	if pt.PreMoveAlertCooldown < 1*time.Minute {
		errors = append(errors, ValidationError{
			Field:   "pattern_tracker.pre_move_alert_cooldown",
			Message: "must be at least 1 minute",
		})
	}

	// Rate limiting
	if pt.PositionCheckInterval < 1*time.Second {
		errors = append(errors, ValidationError{
			Field:   "pattern_tracker.position_check_interval",
			Message: "must be at least 1 second",
		})
	}

	if pt.MaxPositionChecks < 1 {
		errors = append(errors, ValidationError{
			Field:   "pattern_tracker.max_position_checks",
			Message: "must be at least 1",
		})
	}

	return errors
}

func validateHealthServer(hs *HealthServerConfig) []ValidationError {
	var errors []ValidationError

	if hs.Port < 1 || hs.Port > 65535 {
		errors = append(errors, ValidationError{
			Field:   "health_server.port",
			Message: fmt.Sprintf("must be between 1 and 65535, got %d", hs.Port),
		})
	}

	return errors
}
