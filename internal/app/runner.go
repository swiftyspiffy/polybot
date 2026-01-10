package app

import (
	"context"
	"fmt"
	"net/http"
	clts "polybot/clients"
	"polybot/clients/polymarketapi"
	"polybot/config"
	"runtime"
	"runtime/debug"
	"time"

	"go.uber.org/zap"
)

// ensure Runner implements ConfigObserver
var _ config.ConfigObserver = (*Runner)(nil)

// Build info - populated from embedded VCS info at init time
var (
	BuildCommit = "dev"
	BuildTime   = "unknown"
)

func init() {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				if setting.Value != "" {
					BuildCommit = setting.Value
				}
			case "vcs.time":
				BuildTime = setting.Value
			}
		}
	}
}

type Runner struct {
	clients         *clts.Clients
	liveConfig      *config.LiveConfig
	settingsManager *config.SettingsManager
	authHandler     *AuthHandler
	walletTracker   *WalletTracker
	tradeMonitor    *TradeMonitor
	cachePersister  *CachePersister
	contrarianCache *ContrarianCache
	copyTracker     *CopyTracker
	hedgeTracker    *HedgeTracker
	patternTracker  *PatternTracker
	healthServer    *http.Server
	startTime       time.Time

	// Cached markets for WebSocket reconnection
	lastMarkets []polymarketapi.GammaMarket
}

// ServiceStats holds comprehensive service statistics.
type ServiceStats struct {
	// Build info
	Build struct {
		Commit    string `json:"commit"`
		Time      string `json:"time,omitempty"`
		GoVersion string `json:"go_version"`
	} `json:"build"`

	// Service info
	StartTime string `json:"start_time"`
	Uptime    string `json:"uptime"`
	UptimeSec int64  `json:"uptime_seconds"`

	// WebSocket stats
	WebSocket struct {
		Enabled          bool   `json:"enabled"` // true if WebSocket mode is configured
		Connected        bool   `json:"connected"`
		MessageCount     uint64 `json:"message_count"`
		LastMessageAt    string `json:"last_message_at,omitempty"`
		LastMessageAgo   string `json:"last_message_ago,omitempty"`
		TradesSeenViaWS  int    `json:"trades_seen_via_ws"`
		MarketsSeenViaWS int    `json:"markets_seen_via_ws"`
	} `json:"websocket"`

	// Market stats
	Markets struct {
		Count        int     `json:"count"`
		TokenCount   int     `json:"token_count"`
		TopVolume24h float64 `json:"top_volume_24h,omitempty"`
	} `json:"markets"`

	// Filter stats (trades processed)
	Filters struct {
		SkippedLowNotional  int `json:"skipped_low_notional"`
		SkippedNoWallet     int `json:"skipped_no_wallet"`
		SkippedHighActivity int `json:"skipped_high_activity"`
		SkippedObvious      int `json:"skipped_obvious"`
	} `json:"filters"`

	// Alert stats
	Alerts struct {
		Total               int `json:"total"`
		LowActivity         int `json:"low_activity"`
		HighWinRate         int `json:"high_win_rate"`
		ExtremeBet          int `json:"extreme_bet"`
		RapidTrading        int `json:"rapid_trading"`
		NewWallet           int `json:"new_wallet"`
		ContrarianBet       int `json:"contrarian_bet"`
		MassiveTrade        int `json:"massive_trade"`
		ContrarianWinner    int `json:"contrarian_winner"`
		CopyTrader          int `json:"copy_trader"`
		HedgeRemoval        int `json:"hedge_removal"`
		AsymmetricExit      int `json:"asymmetric_exit"`
		ResolutionConfirmed int `json:"resolution_confirmed"`
		ConvictionDoubling  int `json:"conviction_doubling"`
		PerfectExitTiming   int `json:"perfect_exit_timing"`
		StealthAccumulation int `json:"stealth_accumulation"`
	} `json:"alerts"`

	// Cache stats
	Caches struct {
		WalletCacheSize     int `json:"wallet_cache_size"`
		ContrarianCacheSize int `json:"contrarian_cache_size"`
		SeenTradesSize      int `json:"seen_trades_size"`
	} `json:"caches"`

	// Tracker stats
	Trackers struct {
		CopyTracker struct {
			LeaderTrades     int `json:"leader_trades"`
			TrackedFollowers int `json:"tracked_followers"`
		} `json:"copy_tracker"`
		HedgeTracker struct {
			HedgedWallets int `json:"hedged_wallets"`
			PendingEvents int `json:"pending_events"`
			TrackedExits  int `json:"tracked_exits"`
		} `json:"hedge_tracker"`
		PatternTracker struct {
			PendingExits    int `json:"pending_exits"`
			VerifiedWallets int `json:"verified_wallets"`
			Accumulations   int `json:"accumulations"`
		} `json:"pattern_tracker"`
	} `json:"trackers"`

	// Event type counts (from WebSocket)
	EventTypes map[string]int `json:"event_types,omitempty"`

	// Recent alerts feed
	RecentAlerts []RecentAlertInfo `json:"recent_alerts"`

	// Top alerting wallets
	TopWallets []WalletAlertCount `json:"top_wallets"`

	// Top alerting markets
	TopMarkets []MarketAlertInfo `json:"top_markets"`

	// Monitored market names
	MarketNames []string `json:"market_names"`

	// Alert rate (alerts per hour)
	AlertRate float64 `json:"alert_rate"`

	// Last alert info
	LastAlertAt  string `json:"last_alert_at,omitempty"`
	LastAlertAgo string `json:"last_alert_ago,omitempty"`

	// Time-based alert counts
	AlertsLastHour int `json:"alerts_last_hour"`
	AlertsLast24h  int `json:"alerts_last_24h"`
	AlertsLast7d   int `json:"alerts_last_7d"`

	// Alert history buckets for sparkline (last hour, 12 buckets = 5 min each)
	AlertSparkline []int `json:"alert_sparkline"`

	// Alert timeline (24h, hourly buckets)
	AlertTimeline []int `json:"alert_timeline"`

	// Alert sparkline 7d (7 days, 24 buckets = ~7h each)
	AlertSparkline7d []int `json:"alert_sparkline_7d"`

	// Notification status
	Notifications struct {
		DiscordEnabled   bool   `json:"discord_enabled"`
		DiscordChannelID string `json:"discord_channel_id,omitempty"`
		TelegramEnabled  bool   `json:"telegram_enabled"`
		TelegramChatID   string `json:"telegram_chat_id,omitempty"`
	} `json:"notifications"`

	// Runtime stats
	Runtime struct {
		Goroutines   int    `json:"goroutines"`
		HeapAlloc    uint64 `json:"heap_alloc"`     // bytes currently allocated on heap
		HeapSys      uint64 `json:"heap_sys"`       // bytes obtained from system for heap
		HeapInuse    uint64 `json:"heap_inuse"`     // bytes in in-use spans
		StackInuse   uint64 `json:"stack_inuse"`    // bytes in stack spans
		NumGC        uint32 `json:"num_gc"`         // number of completed GC cycles
		LastGC       string `json:"last_gc"`        // time of last GC
		GoVersion    string `json:"go_version"`     // Go version
		NumCPU       int    `json:"num_cpu"`        // number of CPUs
		GOOS         string `json:"goos"`           // operating system
		GOARCH       string `json:"goarch"`         // architecture
	} `json:"runtime"`
}

func NewRunner(clients *clts.Clients, liveConfig *config.LiveConfig, settingsManager *config.SettingsManager, authHandler *AuthHandler) *Runner {
	return &Runner{
		clients:         clients,
		liveConfig:      liveConfig,
		settingsManager: settingsManager,
		authHandler:     authHandler,
	}
}

// OnConfigUpdate is called when the config changes.
// Implements config.ConfigObserver interface.
func (r *Runner) OnConfigUpdate(cfg *config.Config) {
	r.clients.Logger.Info("config update received, propagating to components")

	// Update trade monitor config
	if r.tradeMonitor != nil {
		r.tradeMonitor.UpdateConfig(TradeMonitorConfig{
			PollInterval:            cfg.TradeMonitor.PollInterval,
			MinNotional:             cfg.TradeMonitor.MinNotional,
			MaxMarketsForLow:        cfg.TradeMonitor.MaxMarketsForLow,
			HighWinRateThreshold:    cfg.TradeMonitor.HighWinRateThreshold,
			MinResolvedForWinRate:   cfg.TradeMonitor.MinResolvedForWinRate,
			ExtremeLowPrice:         cfg.TradeMonitor.ExtremeLowPrice,
			ExtremeMinNotional:      cfg.TradeMonitor.ExtremeMinNotional,
			RapidTradeWindow:        cfg.TradeMonitor.RapidTradeWindow,
			RapidTradeMinCount:      cfg.TradeMonitor.RapidTradeMinCount,
			RapidTradeMinTotal:      cfg.TradeMonitor.RapidTradeMinTotal,
			NewWalletMaxMarkets:     cfg.TradeMonitor.NewWalletMaxMarkets,
			NewWalletMinNotional:    cfg.TradeMonitor.NewWalletMinNotional,
			ContrarianMaxPrice:      cfg.TradeMonitor.ContrarianMaxPrice,
			ContrarianMinNotional:   cfg.TradeMonitor.ContrarianMinNotional,
			MassiveTradeMinNotional: cfg.TradeMonitor.MassiveTradeMinNotional,
			MassiveTradeMaxPrice:    cfg.TradeMonitor.MassiveTradeMaxPrice,
			ObviousPrice:            cfg.TradeMonitor.ObviousPrice,
		})
	}

	// Update hedge tracker config
	if r.hedgeTracker != nil {
		r.hedgeTracker.UpdateConfig(HedgeTrackerConfig{
			GistID:                  cfg.HedgeTracker.GistID,
			FileName:                cfg.HedgeTracker.FileName,
			SaveInterval:            cfg.HedgeTracker.SaveInterval,
			MinHedgeSize:            cfg.HedgeTracker.MinHedgeSize,
			MinHedgeValue:           cfg.HedgeTracker.MinHedgeValue,
			SignificantSellPct:      cfg.HedgeTracker.SignificantSellPct,
			PositionCheckInterval:   cfg.HedgeTracker.PositionCheckInterval,
			MaxPositionChecks:       cfg.HedgeTracker.MaxPositionChecks,
			MinExitsForAsymmetric:   cfg.HedgeTracker.MinExitsForAsymmetric,
			AsymmetricThreshold:     cfg.HedgeTracker.AsymmetricThreshold,
			ResolutionCheckInterval: cfg.HedgeTracker.ResolutionCheckInterval,
		})
	}

	// Update pattern tracker config
	if r.patternTracker != nil {
		r.patternTracker.UpdateConfig(PatternTrackerConfig{
			GistID:                   cfg.PatternTracker.GistID,
			FileName:                 cfg.PatternTracker.FileName,
			SaveInterval:             cfg.PatternTracker.SaveInterval,
			ConvictionMinAddSize:     cfg.PatternTracker.ConvictionMinAddSize,
			ConvictionMinAddValue:    cfg.PatternTracker.ConvictionMinAddValue,
			ConvictionMinLossPct:     cfg.PatternTracker.ConvictionMinLossPct,
			ConvictionCheckInterval:  cfg.PatternTracker.ConvictionCheckInterval,
			PerfectExitCheckDelay:    cfg.PatternTracker.PerfectExitCheckDelay,
			PerfectExitMinExits:      cfg.PatternTracker.PerfectExitMinExits,
			PerfectExitMinScore:      cfg.PatternTracker.PerfectExitMinScore,
			PerfectExitCheckInterval: cfg.PatternTracker.PerfectExitCheckInterval,
			StealthTimeWindow:        cfg.PatternTracker.StealthTimeWindow,
			StealthMinTrades:         cfg.PatternTracker.StealthMinTrades,
			StealthMinTotalSize:      cfg.PatternTracker.StealthMinTotalSize,
			StealthMinTotalValue:     cfg.PatternTracker.StealthMinTotalValue,
			StealthMaxSingleTrade:    cfg.PatternTracker.StealthMaxSingleTrade,
			StealthMinSpreadMinutes:  cfg.PatternTracker.StealthMinSpreadMinutes,
			PositionCheckInterval:    cfg.PatternTracker.PositionCheckInterval,
			MaxPositionChecks:        cfg.PatternTracker.MaxPositionChecks,
			PreMoveCheckDelay:        cfg.PatternTracker.PreMoveCheckDelay,
			PreMoveMinNotional:       cfg.PatternTracker.PreMoveMinNotional,
			PreMoveMinMoveSize:       cfg.PatternTracker.PreMoveMinMoveSize,
			PreMoveMinTrades:         cfg.PatternTracker.PreMoveMinTrades,
			PreMoveMinAlpha:          cfg.PatternTracker.PreMoveMinAlpha,
			PreMoveCheckInterval:     cfg.PatternTracker.PreMoveCheckInterval,
			PreMoveAlertCooldown:     cfg.PatternTracker.PreMoveAlertCooldown,
		})
	}
}

func (r *Runner) Run(ctx context.Context) error {
	r.startTime = time.Now()
	logger := r.clients.Logger
	cfg := r.liveConfig.Get()

	// Register as config observer for hot-reload
	r.liveConfig.AddObserver(r)

	logger.Info("starting trade monitor with dynamic markets",
		zap.Int("topMarketsCount", cfg.Markets.TopMarketsCount),
		zap.Duration("marketRefreshInterval", cfg.Markets.RefreshInterval),
		zap.Duration("cacheSaveInterval", cfg.Cache.SaveInterval),
		zap.Strings("categories", cfg.Markets.Categories),
	)

	// Initialize contrarian cache (tracks wallets with contrarian betting history)
	r.contrarianCache = NewContrarianCache(logger, cfg)
	if r.contrarianCache.IsEnabled() {
		loadCtx, loadCancel := context.WithTimeout(ctx, 30*time.Second)
		if err := r.contrarianCache.Load(loadCtx); err != nil {
			logger.Warn("failed to load contrarian cache from gist", zap.Error(err))
		}
		loadCancel()
		r.contrarianCache.Start(ctx)
		logger.Info("contrarian cache initialized",
			zap.Int("wallets", r.contrarianCache.Size()),
		)
	}

	// Initialize wallet tracker
	r.walletTracker = NewWalletTracker(
		logger,
		r.clients.Polymarket,
		cfg.Cache.WalletCacheTTL,
		cfg.ContrarianCache.ContrarianThreshold,
		cfg.TradeMonitor.WinRateMaxEntryPrice,
		r.contrarianCache,
	)

	// Initialize copy tracker
	r.copyTracker = NewCopyTracker(
		logger,
		CopyTrackerConfig{
			TimeWindow:        cfg.TradeMonitor.CopyTradeWindow,
			MinCopyCount:      cfg.TradeMonitor.CopyTradeMinCount,
			LeaderMinWinRate:  cfg.TradeMonitor.CopyTradeLeaderMinWin,
			LeaderMinResolved: cfg.TradeMonitor.CopyTradeLeaderMinRes,
		},
		r.contrarianCache,
	)

	// Initialize hedge tracker
	r.hedgeTracker = NewHedgeTracker(
		logger,
		r.clients.Polymarket,
		r.clients.Gist,
		HedgeTrackerConfig{
			GistID:                  cfg.HedgeTracker.GistID,
			FileName:                cfg.HedgeTracker.FileName,
			SaveInterval:            cfg.HedgeTracker.SaveInterval,
			MinHedgeSize:            cfg.HedgeTracker.MinHedgeSize,
			MinHedgeValue:           cfg.HedgeTracker.MinHedgeValue,
			SignificantSellPct:      cfg.HedgeTracker.SignificantSellPct,
			PositionCheckInterval:   cfg.HedgeTracker.PositionCheckInterval,
			MaxPositionChecks:       cfg.HedgeTracker.MaxPositionChecks,
			MinExitsForAsymmetric:   cfg.HedgeTracker.MinExitsForAsymmetric,
			AsymmetricThreshold:     cfg.HedgeTracker.AsymmetricThreshold,
			ResolutionCheckInterval: cfg.HedgeTracker.ResolutionCheckInterval,
		},
	)
	if r.hedgeTracker.IsEnabled() {
		loadCtx, loadCancel := context.WithTimeout(ctx, 30*time.Second)
		if err := r.hedgeTracker.Load(loadCtx); err != nil {
			logger.Warn("failed to load hedge tracker from gist", zap.Error(err))
		}
		loadCancel()
		r.hedgeTracker.Start(ctx)
		logger.Info("hedge tracker initialized",
			zap.Int("pendingEvents", r.hedgeTracker.PendingEventCount()),
		)
	}

	// Initialize pattern tracker (conviction doubling, perfect exit timing, stealth accumulation)
	r.patternTracker = NewPatternTracker(
		logger,
		r.clients.Polymarket,
		r.clients.Gist,
		PatternTrackerConfig{
			GistID:                  cfg.PatternTracker.GistID,
			FileName:                cfg.PatternTracker.FileName,
			SaveInterval:            cfg.PatternTracker.SaveInterval,
			ConvictionMinAddSize:    cfg.PatternTracker.ConvictionMinAddSize,
			ConvictionMinAddValue:   cfg.PatternTracker.ConvictionMinAddValue,
			ConvictionMinLossPct:    cfg.PatternTracker.ConvictionMinLossPct,
			ConvictionCheckInterval: cfg.PatternTracker.ConvictionCheckInterval,
			PerfectExitCheckDelay:    cfg.PatternTracker.PerfectExitCheckDelay,
			PerfectExitMinExits:      cfg.PatternTracker.PerfectExitMinExits,
			PerfectExitMinScore:      cfg.PatternTracker.PerfectExitMinScore,
			PerfectExitCheckInterval: cfg.PatternTracker.PerfectExitCheckInterval,
			StealthTimeWindow:       cfg.PatternTracker.StealthTimeWindow,
			StealthMinTrades:        cfg.PatternTracker.StealthMinTrades,
			StealthMinTotalSize:     cfg.PatternTracker.StealthMinTotalSize,
			StealthMinTotalValue:    cfg.PatternTracker.StealthMinTotalValue,
			StealthMaxSingleTrade:   cfg.PatternTracker.StealthMaxSingleTrade,
			StealthMinSpreadMinutes: cfg.PatternTracker.StealthMinSpreadMinutes,
			PositionCheckInterval:   cfg.PatternTracker.PositionCheckInterval,
			MaxPositionChecks:       cfg.PatternTracker.MaxPositionChecks,
			PreMoveCheckDelay:       cfg.PatternTracker.PreMoveCheckDelay,
			PreMoveMinNotional:      cfg.PatternTracker.PreMoveMinNotional,
			PreMoveMinMoveSize:      cfg.PatternTracker.PreMoveMinMoveSize,
			PreMoveMinTrades:        cfg.PatternTracker.PreMoveMinTrades,
			PreMoveMinAlpha:         cfg.PatternTracker.PreMoveMinAlpha,
			PreMoveCheckInterval:    cfg.PatternTracker.PreMoveCheckInterval,
			PreMoveAlertCooldown:    cfg.PatternTracker.PreMoveAlertCooldown,
		},
	)
	if r.patternTracker.IsEnabled() {
		loadCtx, loadCancel := context.WithTimeout(ctx, 30*time.Second)
		if err := r.patternTracker.Load(loadCtx); err != nil {
			logger.Warn("failed to load pattern tracker from gist", zap.Error(err))
		}
		loadCancel()
		r.patternTracker.Start(ctx)
		pendingExits, verifiedWallets, accumulations := r.patternTracker.Stats()
		logger.Info("pattern tracker initialized",
			zap.Int("pendingExits", pendingExits),
			zap.Int("verifiedWallets", verifiedWallets),
			zap.Int("accumulations", accumulations),
		)
	}

	// Initialize trade monitor with config
	tradeMonitorCfg := TradeMonitorConfig{
		PollInterval:          cfg.TradeMonitor.PollInterval,
		MinNotional:           cfg.TradeMonitor.MinNotional,
		MaxMarketsForLow:      cfg.TradeMonitor.MaxMarketsForLow,
		HighWinRateThreshold:  cfg.TradeMonitor.HighWinRateThreshold,
		MinResolvedForWinRate: cfg.TradeMonitor.MinResolvedForWinRate,
		ExtremeLowPrice:       cfg.TradeMonitor.ExtremeLowPrice,
		ExtremeMinNotional:    cfg.TradeMonitor.ExtremeMinNotional,
		RapidTradeWindow:      cfg.TradeMonitor.RapidTradeWindow,
		RapidTradeMinCount:    cfg.TradeMonitor.RapidTradeMinCount,
		RapidTradeMinTotal:    cfg.TradeMonitor.RapidTradeMinTotal,
		NewWalletMaxMarkets:   cfg.TradeMonitor.NewWalletMaxMarkets,
		NewWalletMinNotional:  cfg.TradeMonitor.NewWalletMinNotional,
		ContrarianMaxPrice:      cfg.TradeMonitor.ContrarianMaxPrice,
		ContrarianMinNotional:   cfg.TradeMonitor.ContrarianMinNotional,
		MassiveTradeMinNotional: cfg.TradeMonitor.MassiveTradeMinNotional,
		MassiveTradeMaxPrice:    cfg.TradeMonitor.MassiveTradeMaxPrice,
		ObviousPrice:            cfg.TradeMonitor.ObviousPrice,
	}
	r.tradeMonitor = NewTradeMonitor(
		logger,
		r.clients.Polymarket,
		r.walletTracker,
		r.contrarianCache,
		r.copyTracker,
		r.clients.Notifier,
		tradeMonitorCfg,
	)

	// Wire up WebSocket events client
	if r.clients.PolymarketEvents != nil {
		r.tradeMonitor.SetEventsClient(r.clients.PolymarketEvents)
		logger.Info("WebSocket events client configured")
	}

	// Wire up hedge tracker
	if r.hedgeTracker != nil {
		r.tradeMonitor.SetHedgeTracker(r.hedgeTracker)
	}

	// Wire up pattern tracker
	if r.patternTracker != nil {
		r.tradeMonitor.SetPatternTracker(r.patternTracker)
	}

	// Set up wallet filter if configured
	if len(cfg.WalletFilter.SpecificWallets) > 0 {
		r.tradeMonitor.SetWalletFilter(cfg.WalletFilter.SpecificWallets)
	}

	// Initialize cache persister (needs tradeMonitor for seen trades persistence)
	r.cachePersister = NewCachePersister(
		logger,
		r.clients.Gist,
		r.walletTracker,
		r.tradeMonitor,
		cfg.Cache.SaveInterval,
		cfg.Cache.FileName,
		cfg.Cache.SeenTradesFileName,
		cfg.Cache.MaxSizeBytes,
	)

	// Try to load existing cache from GitHub Gist
	loadCtx, loadCancel := context.WithTimeout(ctx, 30*time.Second)
	if imported, err := r.cachePersister.LoadCache(loadCtx); err != nil {
		logger.Warn("failed to load cache from gist", zap.Error(err))
	} else if imported > 0 {
		logger.Info("restored wallet cache from gist",
			zap.Int("wallets", imported),
		)
	}

	// Load seen trades cache
	if imported, err := r.cachePersister.LoadSeenTrades(loadCtx); err != nil {
		logger.Warn("failed to load seen trades from gist", zap.Error(err))
	} else if imported > 0 {
		logger.Info("restored seen trades from gist",
			zap.Int("trades", imported),
		)
	}
	loadCancel()

	// Fetch initial top markets
	markets, err := r.fetchTopMarkets(ctx, cfg.Markets.TopMarketsCount)
	if err != nil {
		return fmt.Errorf("initial market fetch failed: %w", err)
	}
	r.lastMarkets = markets

	// Update trade monitor with market metadata
	if err := r.tradeMonitor.UpdateMarkets(markets); err != nil {
		return fmt.Errorf("failed to update markets: %w", err)
	}

	// Connect WebSocket if available
	if r.clients.PolymarketEvents != nil {
		if err := r.connectWebSocket(ctx); err != nil {
			logger.Warn("failed to connect WebSocket, falling back to polling", zap.Error(err))
		}
	}

	// Start health check server if enabled
	if cfg.HealthServer.Enabled {
		r.startHealthServer(cfg.HealthServer.Port)
		logger.Info("health server started", zap.Int("port", cfg.HealthServer.Port))
	}

	go r.tradeMonitor.Run(ctx)

	logger.Info("trade monitor started",
		zap.Float64("minNotional", tradeMonitorCfg.MinNotional),
		zap.Int("maxMarketsForLowActivity", tradeMonitorCfg.MaxMarketsForLow),
	)

	// Start market refresh loop
	go r.runMarketRefresher(ctx, cfg.Markets.TopMarketsCount, cfg.Markets.RefreshInterval)

	// Start cache persistence loop
	go r.cachePersister.Run(ctx)

	// Start WebSocket reconnection monitor
	if r.clients.PolymarketEvents != nil {
		go r.runWSReconnector(ctx)
	}

	<-ctx.Done()
	logger.Info("runner shutting down")

	// Close WebSocket connection
	if r.clients.PolymarketEvents != nil {
		_ = r.clients.PolymarketEvents.Close()
	}

	// Stop contrarian cache (saves pending changes)
	if r.contrarianCache != nil {
		r.contrarianCache.Stop()
	}

	// Stop hedge tracker (saves pending changes)
	if r.hedgeTracker != nil {
		r.hedgeTracker.Stop()
	}

	// Stop pattern tracker (saves pending changes)
	if r.patternTracker != nil {
		r.patternTracker.Stop()
	}

	// Shutdown health server
	if r.healthServer != nil {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = r.healthServer.Shutdown(shutdownCtx)
		shutdownCancel()
	}

	return nil
}

// connectWebSocket connects the WebSocket and subscribes to current markets.
func (r *Runner) connectWebSocket(ctx context.Context) error {
	tokenIDs := r.tradeMonitor.GetTokenIDs()
	if len(tokenIDs) == 0 {
		return fmt.Errorf("no token IDs to subscribe to")
	}

	// Pass the parent context, not a timeout context.
	// ConnectMarket uses ctx for both dialing AND for a goroutine that closes
	// the connection when ctx is canceled. If we use a timeout context here,
	// the connection gets closed as soon as this function returns.
	if err := r.clients.PolymarketEvents.ConnectMarket(ctx, tokenIDs); err != nil {
		return fmt.Errorf("connect market WebSocket: %w", err)
	}

	r.tradeMonitor.SetWSConnected(true)
	r.clients.Logger.Info("WebSocket connected",
		zap.Int("subscribedTokens", len(tokenIDs)),
	)

	return nil
}

// runWSReconnector monitors WebSocket health and reconnects if needed.
func (r *Runner) runWSReconnector(ctx context.Context) {
	logger := r.clients.Logger
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			stats := r.clients.PolymarketEvents.Stats()

			// Check if we haven't received messages in a while (might be disconnected)
			if stats.MessageCount > 0 && time.Since(stats.LastMessageAt) > 2*time.Minute {
				logger.Warn("WebSocket appears stale, attempting reconnect",
					zap.Duration("timeSinceLastMessage", time.Since(stats.LastMessageAt)),
				)
				r.attemptReconnect(ctx)
			}
		}
	}
}

// attemptReconnect attempts to reconnect the WebSocket.
func (r *Runner) attemptReconnect(ctx context.Context) {
	logger := r.clients.Logger

	// Close existing connection
	_ = r.clients.PolymarketEvents.Close()
	r.tradeMonitor.SetWSConnected(false)

	// Wait a moment before reconnecting
	time.Sleep(5 * time.Second)

	// Reconnect
	if err := r.connectWebSocket(ctx); err != nil {
		logger.Error("failed to reconnect WebSocket", zap.Error(err))
	}
}

// fetchTopMarkets fetches the top markets by 24h volume.
func (r *Runner) fetchTopMarkets(ctx context.Context, limit int) ([]polymarketapi.GammaMarket, error) {
	logger := r.clients.Logger
	cfg := r.liveConfig.Get()
	fetchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var markets []polymarketapi.GammaMarket
	seen := make(map[string]bool)

	// Fetch specific markets if configured
	if len(cfg.Markets.SpecificMarkets) > 0 {
		logger.Info("fetching specific markets",
			zap.Int("count", len(cfg.Markets.SpecificMarkets)))

		for _, conditionID := range cfg.Markets.SpecificMarkets {
			market, err := r.clients.Polymarket.GetMarketByConditionID(fetchCtx, conditionID)
			if err != nil {
				logger.Warn("failed to fetch specific market",
					zap.String("conditionID", conditionID),
					zap.Error(err))
				continue
			}
			if market.ConditionID != "" && len(market.GetTokenIDs()) > 0 {
				markets = append(markets, *market)
				seen[market.ConditionID] = true
			}
		}
	}

	// If specific-only mode, return just the specific markets
	if cfg.Markets.SpecificMarketsOnly {
		logger.Info("using specific markets only mode",
			zap.Int("marketCount", len(markets)))
		if len(markets) == 0 {
			return nil, fmt.Errorf("no specific markets found")
		}
		return markets, nil
	}

	// Otherwise, also fetch top markets by volume (filtered by categories if configured)
	categories := cfg.Markets.Categories
	topMarkets, err := r.clients.Polymarket.GetTopMarketsByVolumeFiltered(fetchCtx, limit, categories)
	if err != nil {
		return nil, fmt.Errorf("get top markets: %w", err)
	}
	if len(categories) > 0 {
		logger.Debug("fetched markets filtered by categories",
			zap.Strings("categories", categories),
			zap.Int("count", len(topMarkets)),
		)
	}

	// Filter to active markets with token IDs, avoiding duplicates
	for _, m := range topMarkets {
		if m.ConditionID != "" && m.Active && !m.Closed && len(m.GetTokenIDs()) > 0 {
			if !seen[m.ConditionID] {
				markets = append(markets, m)
				seen[m.ConditionID] = true
			}
		}
	}

	if len(markets) == 0 {
		return nil, fmt.Errorf("no active markets found")
	}

	return markets, nil
}

// refreshTopMarkets fetches the top markets by 24h volume and updates the trade monitor.
func (r *Runner) refreshTopMarkets(ctx context.Context, limit int) error {
	logger := r.clients.Logger

	markets, err := r.fetchTopMarkets(ctx, limit)
	if err != nil {
		return err
	}

	r.lastMarkets = markets

	if err := r.tradeMonitor.UpdateMarkets(markets); err != nil {
		return fmt.Errorf("update markets: %w", err)
	}

	// Get WebSocket stats for monitoring
	var wsMessages uint64
	var wsLastMessageAgo time.Duration
	var wsConnected bool
	if r.clients.PolymarketEvents != nil {
		stats := r.clients.PolymarketEvents.Stats()
		wsMessages = stats.MessageCount
		if !stats.LastMessageAt.IsZero() {
			wsLastMessageAgo = time.Since(stats.LastMessageAt).Round(time.Second)
		}
		wsConnected = r.tradeMonitor.IsWSConnected()
	}

	filterStats := r.tradeMonitor.FilterStats()
	logger.Info("refreshed monitored markets",
		zap.Int("marketCount", len(markets)),
		zap.Float64("topVolume24h", markets[0].Volume24hr),
		zap.Bool("wsConnected", wsConnected),
		zap.Uint64("wsMessages", wsMessages),
		zap.Duration("wsLastMessageAgo", wsLastMessageAgo),
		zap.Int("tradesSeenViaWS", r.tradeMonitor.SeenTradesCount()),
		zap.Int("marketsSeenViaWS", r.tradeMonitor.SeenMarketsCount()),
		zap.Any("wsEventTypes", r.tradeMonitor.EventTypeCounts()),
		zap.Int("skippedLowNotional", filterStats.SkippedLowNotional),
		zap.Int("skippedNoWallet", filterStats.SkippedNoWallet),
		zap.Int("skippedHighActivity", filterStats.SkippedHighActivity),
		zap.Int("alertsSent", filterStats.AlertsSent),
		zap.Int("alertsLowActivity", filterStats.AlertsLowActivity),
		zap.Int("alertsHighWinRate", filterStats.AlertsHighWinRate),
		zap.Int("alertsExtremeBet", filterStats.AlertsExtremeBet),
		zap.Int("alertsRapidTrading", filterStats.AlertsRapidTrading),
		zap.Int("alertsNewWallet", filterStats.AlertsNewWallet),
		zap.Int("alertsContrarianBet", filterStats.AlertsContrarianBet),
		zap.Int("alertsMassiveTrade", filterStats.AlertsMassiveTrade),
		zap.Int("alertsContrarianWinner", filterStats.AlertsContrarianWinner),
		zap.Int("alertsCopyTrader", filterStats.AlertsCopyTrader),
		zap.Int("alertsHedgeRemoval", filterStats.AlertsHedgeRemoval),
		zap.Int("alertsAsymmetricExit", filterStats.AlertsAsymmetricExit),
	)

	return nil
}

// runMarketRefresher periodically refreshes the list of monitored markets.
func (r *Runner) runMarketRefresher(ctx context.Context, limit int, interval time.Duration) {
	logger := r.clients.Logger
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := r.refreshTopMarkets(ctx, limit); err != nil {
				logger.Warn("failed to refresh top markets", zap.Error(err))
			}
		}
	}
}

// GetStats returns comprehensive service statistics.
func (r *Runner) GetStats() ServiceStats {
	var stats ServiceStats

	// Build info
	stats.Build.Commit = BuildCommit
	stats.Build.Time = BuildTime
	stats.Build.GoVersion = runtime.Version()

	// Service info
	stats.StartTime = r.startTime.UTC().Format(time.RFC3339)
	uptime := time.Since(r.startTime)
	stats.Uptime = uptime.Round(time.Second).String()
	stats.UptimeSec = int64(uptime.Seconds())

	// WebSocket stats
	stats.WebSocket.Enabled = r.clients.PolymarketEvents != nil
	if r.clients.PolymarketEvents != nil {
		wsStats := r.clients.PolymarketEvents.Stats()
		stats.WebSocket.MessageCount = wsStats.MessageCount
		if !wsStats.LastMessageAt.IsZero() {
			stats.WebSocket.LastMessageAt = wsStats.LastMessageAt.UTC().Format(time.RFC3339)
			stats.WebSocket.LastMessageAgo = time.Since(wsStats.LastMessageAt).Round(time.Second).String()
		}
	}
	if r.tradeMonitor != nil {
		stats.WebSocket.Connected = r.tradeMonitor.IsWSConnected()
		stats.WebSocket.TradesSeenViaWS = r.tradeMonitor.SeenTradesCount()
		stats.WebSocket.MarketsSeenViaWS = r.tradeMonitor.SeenMarketsCount()
		stats.EventTypes = r.tradeMonitor.EventTypeCounts()
	}

	// Market stats
	if len(r.lastMarkets) > 0 {
		stats.Markets.Count = len(r.lastMarkets)
		stats.Markets.TopVolume24h = r.lastMarkets[0].Volume24hr
	}
	if r.tradeMonitor != nil {
		stats.Markets.TokenCount = len(r.tradeMonitor.GetTokenIDs())
	}

	// Filter and alert stats
	if r.tradeMonitor != nil {
		fs := r.tradeMonitor.FilterStats()
		stats.Filters.SkippedLowNotional = fs.SkippedLowNotional
		stats.Filters.SkippedNoWallet = fs.SkippedNoWallet
		stats.Filters.SkippedHighActivity = fs.SkippedHighActivity
		stats.Filters.SkippedObvious = fs.SkippedObvious

		stats.Alerts.LowActivity = fs.AlertsLowActivity
		stats.Alerts.HighWinRate = fs.AlertsHighWinRate
		stats.Alerts.ExtremeBet = fs.AlertsExtremeBet
		stats.Alerts.RapidTrading = fs.AlertsRapidTrading
		stats.Alerts.NewWallet = fs.AlertsNewWallet
		stats.Alerts.ContrarianBet = fs.AlertsContrarianBet
		stats.Alerts.MassiveTrade = fs.AlertsMassiveTrade
		stats.Alerts.ContrarianWinner = fs.AlertsContrarianWinner
		stats.Alerts.CopyTrader = fs.AlertsCopyTrader
		stats.Alerts.HedgeRemoval = fs.AlertsHedgeRemoval
		stats.Alerts.AsymmetricExit = fs.AlertsAsymmetricExit
		stats.Alerts.ResolutionConfirmed = fs.AlertsResolutionConfirmed
		stats.Alerts.ConvictionDoubling = fs.AlertsConvictionDoubling
		stats.Alerts.PerfectExitTiming = fs.AlertsPerfectExitTiming
		stats.Alerts.StealthAccumulation = fs.AlertsStealthAccumulation
		// Total is sum of all heuristic counts (a single alert can trigger multiple heuristics)
		stats.Alerts.Total = stats.Alerts.LowActivity + stats.Alerts.HighWinRate +
			stats.Alerts.ExtremeBet + stats.Alerts.RapidTrading + stats.Alerts.NewWallet +
			stats.Alerts.ContrarianBet + stats.Alerts.MassiveTrade + stats.Alerts.ContrarianWinner +
			stats.Alerts.CopyTrader + stats.Alerts.HedgeRemoval + stats.Alerts.AsymmetricExit +
			stats.Alerts.ResolutionConfirmed + stats.Alerts.ConvictionDoubling +
			stats.Alerts.PerfectExitTiming + stats.Alerts.StealthAccumulation
	}

	// Cache stats
	if r.walletTracker != nil {
		stats.Caches.WalletCacheSize = r.walletTracker.CacheSize()
	}
	if r.contrarianCache != nil {
		stats.Caches.ContrarianCacheSize = r.contrarianCache.Size()
	}
	if r.tradeMonitor != nil {
		stats.Caches.SeenTradesSize = r.tradeMonitor.SeenTradesCount()
	}

	// Tracker stats
	if r.copyTracker != nil {
		leaderTrades, followers := r.copyTracker.Stats()
		stats.Trackers.CopyTracker.LeaderTrades = leaderTrades
		stats.Trackers.CopyTracker.TrackedFollowers = followers
	}
	if r.hedgeTracker != nil {
		hedged, pending, exits := r.hedgeTracker.Stats()
		stats.Trackers.HedgeTracker.HedgedWallets = hedged
		stats.Trackers.HedgeTracker.PendingEvents = pending
		stats.Trackers.HedgeTracker.TrackedExits = exits
	}
	if r.patternTracker != nil {
		pending, verified, accum := r.patternTracker.Stats()
		stats.Trackers.PatternTracker.PendingExits = pending
		stats.Trackers.PatternTracker.VerifiedWallets = verified
		stats.Trackers.PatternTracker.Accumulations = accum
	}

	// New dashboard features
	if r.tradeMonitor != nil {
		// Recent alerts feed
		stats.RecentAlerts = r.tradeMonitor.RecentAlerts()

		// Top alerting wallets (top 5)
		stats.TopWallets = r.tradeMonitor.TopAlertingWallets(5)

		// Top alerting markets (top 5)
		stats.TopMarkets = r.tradeMonitor.TopAlertingMarkets(5)

		// Monitored market names
		stats.MarketNames = r.tradeMonitor.MonitoredMarkets()

		// Alert rate (alerts per hour)
		uptime := time.Since(r.startTime)
		if uptime.Hours() > 0 {
			stats.AlertRate = float64(stats.Alerts.Total) / uptime.Hours()
		}

		// Last alert time
		lastAlert := r.tradeMonitor.LastAlertTime()
		if !lastAlert.IsZero() {
			stats.LastAlertAt = lastAlert.UTC().Format(time.RFC3339)
			stats.LastAlertAgo = time.Since(lastAlert).Round(time.Second).String()
		}

		// Time-based alert counts
		stats.AlertsLastHour, stats.AlertsLast24h, stats.AlertsLast7d = r.tradeMonitor.AlertCountsInPeriods()

		// Sparkline data (last hour, 12 buckets = 5 min each)
		stats.AlertSparkline = r.tradeMonitor.AlertHistoryBuckets(1*time.Hour, 12)

		// Timeline data (last 24h, 24 buckets = 1 hour each)
		stats.AlertTimeline = r.tradeMonitor.AlertHistoryBuckets(24*time.Hour, 24)

		// Sparkline data (7 days, 24 buckets = ~7h each)
		stats.AlertSparkline7d = r.tradeMonitor.AlertHistoryBuckets(7*24*time.Hour, 24)
	}

	// Notification status
	cfg := r.liveConfig.Get()
	stats.Notifications.DiscordEnabled = r.clients.Discord != nil
	if r.clients.Discord != nil && cfg != nil {
		if cfg.IsProd {
			stats.Notifications.DiscordChannelID = cfg.Discord.ProdChannelID
		} else {
			stats.Notifications.DiscordChannelID = cfg.Discord.BetaChannelID
		}
	}
	stats.Notifications.TelegramEnabled = r.clients.Telegram != nil
	if r.clients.Telegram != nil && cfg != nil {
		if cfg.IsProd {
			stats.Notifications.TelegramChatID = cfg.Telegram.ProdChatID
		} else {
			stats.Notifications.TelegramChatID = cfg.Telegram.BetaChatID
		}
	}

	// Runtime stats
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	stats.Runtime.Goroutines = runtime.NumGoroutine()
	stats.Runtime.HeapAlloc = memStats.HeapAlloc
	stats.Runtime.HeapSys = memStats.HeapSys
	stats.Runtime.HeapInuse = memStats.HeapInuse
	stats.Runtime.StackInuse = memStats.StackInuse
	stats.Runtime.NumGC = memStats.NumGC
	if memStats.LastGC > 0 {
		stats.Runtime.LastGC = time.Unix(0, int64(memStats.LastGC)).UTC().Format(time.RFC3339)
	}
	stats.Runtime.GoVersion = runtime.Version()
	stats.Runtime.NumCPU = runtime.NumCPU()
	stats.Runtime.GOOS = runtime.GOOS
	stats.Runtime.GOARCH = runtime.GOARCH

	return stats
}
