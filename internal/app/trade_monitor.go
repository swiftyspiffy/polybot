package app

import (
	"context"
	"fmt"
	"polybot/clients/notifier"
	"polybot/clients/polymarketapi"
	"polybot/clients/polymarketevents"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// TradeMonitorConfig holds configuration for the trade monitor.
type TradeMonitorConfig struct {
	PollInterval     time.Duration // How often to poll for new trades (fallback)
	MinNotional      float64       // Minimum trade size in USD to alert on
	MaxMarketsForLow int           // Maximum unique markets to be considered "low activity"

	// High win rate detection
	HighWinRateThreshold  float64 // Minimum win rate to trigger alert (e.g., 0.70 = 70%)
	MinResolvedForWinRate int     // Minimum resolved positions to consider win rate

	// Extreme odds detection (low price = contrarian/longshot bet)
	ExtremeLowPrice    float64 // Price threshold for "extreme low" (e.g., 0.05 = 5¢)
	ExtremeMinNotional float64 // Minimum notional for extreme odds alerts

	// Rapid trading detection
	RapidTradeWindow   time.Duration // Time window to track trades (e.g., 5 minutes)
	RapidTradeMinCount int           // Minimum trades in window to trigger alert
	RapidTradeMinTotal float64       // Minimum total notional in window

	// New wallet detection
	NewWalletMaxMarkets  int     // Max prior markets to be considered "new" (e.g., 1)
	NewWalletMinNotional float64 // Minimum notional for new wallet alerts

	// Contrarian bet detection
	ContrarianMaxPrice   float64 // Max price to be considered "contrarian" (e.g., 0.10 = 10¢)
	ContrarianMinNotional float64 // Minimum notional for contrarian bet alerts

	// Massive trade detection
	MassiveTradeMinNotional  float64 // Minimum notional for massive trade alerts (e.g., 50000)
	MassiveTradeMaxPrice     float64 // Max entry price to alert on massive trades (e.g., 0.70 = ignore obvious 70¢+ bets)

	// Global obvious price filter - skip ALL alerts above this price
	ObviousPrice float64 // Max price to alert on (e.g., 0.85 = skip alerts for trades at 85¢+)
}

// DefaultTradeMonitorConfig returns sensible defaults.
func DefaultTradeMonitorConfig() TradeMonitorConfig {
	return TradeMonitorConfig{
		PollInterval:          10 * time.Second,
		MinNotional:           4000.0,
		MaxMarketsForLow:      5,             // < 5 unique markets = low activity
		HighWinRateThreshold:  0.90,          // 90% win rate
		MinResolvedForWinRate: 5,             // at least 5 resolved positions
		ExtremeLowPrice:       0.03,          // 3¢ or lower (longshot bet)
		ExtremeMinNotional:    2500,          // $2500 minimum for extreme odds
		RapidTradeWindow:      5 * time.Minute, // 5 minute window
		RapidTradeMinCount:    3,             // 3+ trades in window
		RapidTradeMinTotal:    5000,          // $5000 total in window
		NewWalletMaxMarkets:   1,             // 0-1 prior markets = new wallet
		NewWalletMinNotional:  10000,         // $10000 minimum for new wallet alerts
		ContrarianMaxPrice:      0.10,          // 10¢ or lower = betting against consensus
		ContrarianMinNotional:   5000,          // $5000 minimum for contrarian alerts
		MassiveTradeMinNotional: 50000,         // $50000 minimum for massive trade alerts
		MassiveTradeMaxPrice:    0.70,          // Only alert on massive trades at 70¢ or below
		ObviousPrice:            0.75,          // Skip all alerts for trades at 85¢ or above
	}
}

// Type aliases for cleaner code
type AlertReason = notifier.AlertReason

// Alert reason constants
const (
	AlertReasonLowActivity         = notifier.AlertReasonLowActivity
	AlertReasonHighWinRate         = notifier.AlertReasonHighWinRate
	AlertReasonExtremeBet          = notifier.AlertReasonExtremeBet
	AlertReasonRapidTrading        = notifier.AlertReasonRapidTrading
	AlertReasonNewWallet           = notifier.AlertReasonNewWallet
	AlertReasonContrarianBet       = notifier.AlertReasonContrarianBet
	AlertReasonMassiveTrade        = notifier.AlertReasonMassiveTrade
	AlertReasonContrarianWinner    = notifier.AlertReasonContrarianWinner
	AlertReasonCopyTrader          = notifier.AlertReasonCopyTrader
	AlertReasonHedgeRemoval        = notifier.AlertReasonHedgeRemoval
	AlertReasonAsymmetricExit      = notifier.AlertReasonAsymmetricExit
	AlertReasonResolutionConfirmed = notifier.AlertReasonResolutionConfirmed
	AlertReasonConvictionDoubling  = notifier.AlertReasonConvictionDoubling
	AlertReasonPerfectExitTiming   = notifier.AlertReasonPerfectExitTiming
	AlertReasonStealthAccumulation = notifier.AlertReasonStealthAccumulation
)

// MarketInfo holds metadata about a market for enriching WebSocket events.
type MarketInfo struct {
	ConditionID string
	Title       string
	Slug        string
	Image       string
	Outcomes    []string // e.g., ["Yes", "No"]
	TokenIDs    []string // Token IDs for this market
}

// TradeMonitor monitors trades via WebSocket and alerts on low-activity wallet activity.
type TradeMonitor struct {
	logger          *zap.Logger
	apiClient       *polymarketapi.PolymarketApiClient
	eventsClient    *polymarketevents.PolymarketEventsClient
	walletTracker   *WalletTracker
	contrarianCache *ContrarianCache
	copyTracker     *CopyTracker
	hedgeTracker    *HedgeTracker
	patternTracker  *PatternTracker
	notifier        notifier.Notifier

	// Config with mutex for hot-reload support
	configMu sync.RWMutex
	config   TradeMonitorConfig

	// Market metadata indexed by token ID for fast lookup
	mu          sync.RWMutex
	markets     []string               // condition IDs (for backwards compat)
	tokenToInfo map[string]*MarketInfo // token ID -> market info
	allTokenIDs []string               // all subscribed token IDs

	// Track seen trades to avoid duplicates
	seenMu     sync.Mutex
	seenTrades map[string]struct{}

	// Track unique markets seen (by asset ID) for monitoring
	seenMarketsMu sync.Mutex
	seenMarkets   map[string]struct{}

	// Track event types for debugging
	eventTypesMu sync.Mutex
	eventTypes   map[string]int

	// Filter stats for debugging
	filterStatsMu               sync.Mutex
	skippedLowNotional          int
	skippedNoWallet             int
	skippedWalletFilter         int
	skippedHighActivity         int
	skippedObvious              int
	alertsSent                  int
	alertsLowActivity           int
	alertsHighWinRate           int
	alertsExtremeBet            int
	alertsRapidTrading          int
	alertsNewWallet             int
	alertsContrarianBet         int
	alertsMassiveTrade          int
	alertsContrarianWinner      int
	alertsCopyTrader            int
	alertsHedgeRemoval          int
	alertsAsymmetricExit        int
	alertsResolutionConfirmed   int
	alertsConvictionDoubling    int
	alertsPerfectExitTiming     int
	alertsStealthAccumulation   int

	// Rapid trading detection - track recent trades per wallet
	recentTradesMu sync.Mutex
	recentTrades   map[string][]recentTrade // wallet -> recent trades

	// WebSocket connection state
	wsConnectedMu sync.RWMutex
	wsConnected   bool

	// Wallet filter (nil = monitor all wallets)
	specificWallets map[string]bool

	// Recent alerts for dashboard feed (last 10)
	recentAlertsMu sync.RWMutex
	recentAlerts   []RecentAlertInfo

	// Alert counts by wallet for leaderboard
	alertsByWalletMu sync.RWMutex
	alertsByWallet   map[string]int

	// Last alert timestamp
	lastAlertTimeMu sync.RWMutex
	lastAlertTime   time.Time

	// Alert history with timestamps for sparkline/timeline (last 24h)
	alertHistoryMu sync.RWMutex
	alertHistory   []time.Time

	// Alerts by market for market leaderboard
	alertsByMarketMu sync.RWMutex
	alertsByMarket   map[string]*MarketAlertInfo
}

// RecentAlertInfo holds summary info for a recent alert.
type RecentAlertInfo struct {
	Timestamp     time.Time `json:"timestamp"`
	WalletAddress string    `json:"wallet_address"`
	WalletName    string    `json:"wallet_name"`
	MarketTitle   string    `json:"market_title"`
	ConditionID   string    `json:"condition_id"`
	Outcome       string    `json:"outcome"`
	Side          string    `json:"side"`
	Notional      float64   `json:"notional"`
	Reasons       []string  `json:"reasons"`

	// Extended details for expanded view
	Price         float64 `json:"price"`
	Shares        float64 `json:"shares"`
	MarketURL     string  `json:"market_url"`
	MarketImage   string  `json:"market_image"`
	WalletURL     string  `json:"wallet_url"`
	WinRate       float64 `json:"win_rate"`
	WinCount      int     `json:"win_count"`
	LossCount     int     `json:"loss_count"`
	UniqueMarkets int     `json:"unique_markets"`
	HasInventory  bool    `json:"has_inventory"`
	InvShares     float64 `json:"inv_shares"`
	InvAvgPrice   float64 `json:"inv_avg_price"`
	InvValue      float64 `json:"inv_value"`
}

// MarketAlertInfo tracks alert counts per market.
type MarketAlertInfo struct {
	ConditionID string `json:"condition_id"`
	Title       string `json:"title"`
	Count       int    `json:"count"`
}

// recentTrade tracks a trade for rapid trading detection.
type recentTrade struct {
	timestamp time.Time
	notional  float64
}

// NewTradeMonitor creates a new trade monitor.
func NewTradeMonitor(
	logger *zap.Logger,
	apiClient *polymarketapi.PolymarketApiClient,
	walletTracker *WalletTracker,
	contrarianCache *ContrarianCache,
	copyTracker *CopyTracker,
	notif notifier.Notifier,
	config TradeMonitorConfig,
) *TradeMonitor {
	if logger == nil {
		logger = zap.NewNop()
	}

	return &TradeMonitor{
		logger:          logger,
		apiClient:       apiClient,
		walletTracker:   walletTracker,
		contrarianCache: contrarianCache,
		copyTracker:     copyTracker,
		notifier:        notif,
		config:          config,
		seenTrades:      make(map[string]struct{}),
		seenMarkets:     make(map[string]struct{}),
		eventTypes:      make(map[string]int),
		tokenToInfo:     make(map[string]*MarketInfo),
		recentTrades:    make(map[string][]recentTrade),
		recentAlerts:    make([]RecentAlertInfo, 0, 10),
		alertsByWallet:  make(map[string]int),
		alertHistory:    make([]time.Time, 0, 1000),
		alertsByMarket:  make(map[string]*MarketAlertInfo),
	}
}

// SetEventsClient sets the WebSocket events client.
func (tm *TradeMonitor) SetEventsClient(client *polymarketevents.PolymarketEventsClient) {
	tm.eventsClient = client
}

// SetHedgeTracker sets the hedge tracker for hedge pattern detection.
func (tm *TradeMonitor) SetHedgeTracker(tracker *HedgeTracker) {
	tm.hedgeTracker = tracker
}

// SetPatternTracker sets the pattern tracker for advanced pattern detection.
func (tm *TradeMonitor) SetPatternTracker(tracker *PatternTracker) {
	tm.patternTracker = tracker
}

// SetWalletFilter sets the wallet filter. Only trades from these wallets will be processed.
// Pass nil or empty slice to monitor all wallets.
func (tm *TradeMonitor) SetWalletFilter(wallets []string) {
	if len(wallets) == 0 {
		tm.specificWallets = nil
		return
	}
	tm.specificWallets = make(map[string]bool)
	for _, w := range wallets {
		tm.specificWallets[strings.ToLower(w)] = true
	}
	tm.logger.Info("wallet filter enabled",
		zap.Int("walletCount", len(tm.specificWallets)))
}

// shouldProcessWallet returns true if the wallet should be processed.
// Returns true for all wallets if no filter is set.
func (tm *TradeMonitor) shouldProcessWallet(address string) bool {
	if tm.specificWallets == nil {
		return true // No filter, process all
	}
	return tm.specificWallets[strings.ToLower(address)]
}

// getConfig returns the current config in a thread-safe manner.
func (tm *TradeMonitor) getConfig() TradeMonitorConfig {
	tm.configMu.RLock()
	defer tm.configMu.RUnlock()
	return tm.config
}

// UpdateConfig updates the trade monitor config.
// This is called when settings change via the settings API.
func (tm *TradeMonitor) UpdateConfig(cfg TradeMonitorConfig) {
	tm.configMu.Lock()
	defer tm.configMu.Unlock()
	tm.config = cfg
	tm.logger.Info("trade monitor config updated",
		zap.Float64("minNotional", cfg.MinNotional),
		zap.Float64("obviousPrice", cfg.ObviousPrice),
		zap.Float64("highWinRateThreshold", cfg.HighWinRateThreshold),
	)
}

// SetMarkets updates the list of markets to watch (condition IDs only, for backwards compat).
func (tm *TradeMonitor) SetMarkets(markets []string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.markets = markets
}

// UpdateMarkets updates the monitored markets with full metadata.
// This handles subscription changes for the WebSocket.
func (tm *TradeMonitor) UpdateMarkets(markets []polymarketapi.GammaMarket) error {
	tm.mu.Lock()

	// Build new token->info map
	newTokenToInfo := make(map[string]*MarketInfo)
	var newTokenIDs []string
	var conditionIDs []string

	for _, m := range markets {
		if m.ConditionID == "" || !m.Active || m.Closed {
			continue
		}

		tokenIDs := m.GetTokenIDs()
		if len(tokenIDs) == 0 {
			tm.logger.Debug("market has no token IDs",
				zap.String("conditionID", m.ConditionID),
				zap.String("question", m.Question),
			)
			continue
		}

		// Parse outcomes
		var outcomes []string
		_ = parseJSONArray(m.Outcomes, &outcomes)
		if len(outcomes) == 0 {
			outcomes = []string{"Yes", "No"} // Default
		}

		info := &MarketInfo{
			ConditionID: m.ConditionID,
			Title:       m.Question,
			Slug:        m.Slug,
			Image:       m.Image,
			Outcomes:    outcomes,
			TokenIDs:    tokenIDs,
		}

		for _, tokenID := range tokenIDs {
			newTokenToInfo[tokenID] = info
			newTokenIDs = append(newTokenIDs, tokenID)
		}
		conditionIDs = append(conditionIDs, m.ConditionID)
	}

	// Calculate what to subscribe/unsubscribe
	oldTokenIDs := tm.allTokenIDs
	toSubscribe := difference(newTokenIDs, oldTokenIDs)
	toUnsubscribe := difference(oldTokenIDs, newTokenIDs)

	// Update state
	tm.tokenToInfo = newTokenToInfo
	tm.allTokenIDs = newTokenIDs
	tm.markets = conditionIDs
	tm.mu.Unlock()

	// Update WebSocket subscriptions if connected
	wsConnected := tm.IsWSConnected()
	if tm.eventsClient != nil && wsConnected {
		if len(toUnsubscribe) > 0 {
			if err := tm.eventsClient.UnsubscribeAssets(toUnsubscribe); err != nil {
				tm.logger.Warn("failed to unsubscribe assets", zap.Error(err), zap.Int("count", len(toUnsubscribe)))
			} else {
				tm.logger.Info("unsubscribed from assets", zap.Int("count", len(toUnsubscribe)))
			}
		}
		if len(toSubscribe) > 0 {
			if err := tm.eventsClient.SubscribeAssets(toSubscribe); err != nil {
				tm.logger.Warn("failed to subscribe assets", zap.Error(err), zap.Int("count", len(toSubscribe)))
			} else {
				tm.logger.Info("subscribed to assets", zap.Int("count", len(toSubscribe)))
			}
		}
	}

	tm.logger.Info("updated markets",
		zap.Int("markets", len(conditionIDs)),
		zap.Int("tokens", len(newTokenIDs)),
		zap.Int("subscribed", len(toSubscribe)),
		zap.Int("unsubscribed", len(toUnsubscribe)),
		zap.Bool("wsConnected", wsConnected),
	)

	return nil
}

// GetTokenIDs returns all subscribed token IDs.
func (tm *TradeMonitor) GetTokenIDs() []string {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	return tm.allTokenIDs
}

// Run starts the trade monitoring loop using WebSocket events.
func (tm *TradeMonitor) Run(ctx context.Context) {
	cfg := tm.getConfig()
	tm.logger.Info("trade monitor started",
		zap.Float64("minNotional", cfg.MinNotional),
		zap.Int("maxMarketsForLow", cfg.MaxMarketsForLow),
	)

	// If we have an events client, use WebSocket mode
	if tm.eventsClient != nil {
		tm.runWebSocket(ctx)
		return
	}

	// Fallback to polling mode
	tm.runPolling(ctx)
}

// runWebSocket processes events from the WebSocket connection.
func (tm *TradeMonitor) runWebSocket(ctx context.Context) {
	tm.logger.Info("trade monitor using WebSocket mode")

	msgCh := tm.eventsClient.Messages()
	errCh := tm.eventsClient.Errors()

	for {
		select {
		case <-ctx.Done():
			tm.logger.Info("trade monitor shutting down")
			return

		case msg := <-msgCh:
			// Mark as connected when we receive messages
			tm.wsConnectedMu.Lock()
			wasConnected := tm.wsConnected
			tm.wsConnected = true
			tm.wsConnectedMu.Unlock()
			if !wasConnected {
				tm.logger.Info("WebSocket connection confirmed (receiving messages)")
			}
			tm.processWebSocketMessage(ctx, msg)

		case err := <-errCh:
			tm.logger.Warn("WebSocket error", zap.Error(err))
			tm.wsConnectedMu.Lock()
			tm.wsConnected = false
			tm.wsConnectedMu.Unlock()
			// The runner will handle reconnection
		}
	}
}

// processWebSocketMessage processes a single WebSocket message.
func (tm *TradeMonitor) processWebSocketMessage(ctx context.Context, msg []byte) {
	// Track event type for debugging
	eventType := polymarketevents.ParseEventType(msg)
	tm.eventTypesMu.Lock()
	tm.eventTypes[eventType]++
	tm.eventTypesMu.Unlock()

	event := polymarketevents.ParseTradeEvent(msg)
	if event == nil {
		return // Not a trade event (ParseTradeEvent already filters to trade/last_trade_price)
	}

	// Track unique markets seen (by asset ID)
	if event.AssetID != "" {
		tm.seenMarketsMu.Lock()
		tm.seenMarkets[event.AssetID] = struct{}{}
		tm.seenMarketsMu.Unlock()
	}

	tm.processTradeEvent(ctx, event)
}

// processTradeEvent processes a WebSocket trade event.
func (tm *TradeMonitor) processTradeEvent(ctx context.Context, event *polymarketevents.TradeEvent) {
	// Get config once at the start for thread-safe access
	cfg := tm.getConfig()

	// Create trade key for deduplication
	tradeKey := fmt.Sprintf("%s:%s", event.TransactionHash, event.AssetID)

	tm.seenMu.Lock()
	if _, seen := tm.seenTrades[tradeKey]; seen {
		tm.seenMu.Unlock()
		return
	}
	tm.seenTrades[tradeKey] = struct{}{}
	tm.seenMu.Unlock()

	// Get price and size
	price := event.GetPriceFloat()
	size := event.GetSizeFloat()
	notional := price * size

	if notional < cfg.MinNotional {
		tm.filterStatsMu.Lock()
		tm.skippedLowNotional++
		tm.filterStatsMu.Unlock()
		return
	}

	// Look up market info
	tm.mu.RLock()
	info := tm.tokenToInfo[event.AssetID]
	tm.mu.RUnlock()

	// Determine trader address (taker is usually the one we care about)
	traderAddr := event.TakerAddress
	if traderAddr == "" {
		traderAddr = event.MakerAddress
	}
	if traderAddr == "" {
		// last_trade_price events don't include wallet addresses, skip them
		tm.filterStatsMu.Lock()
		tm.skippedNoWallet++
		tm.filterStatsMu.Unlock()
		return
	}

	// Check wallet filter
	if !tm.shouldProcessWallet(traderAddr) {
		tm.filterStatsMu.Lock()
		tm.skippedWalletFilter++
		tm.filterStatsMu.Unlock()
		return
	}

	// Check wallet activity and win rate
	isLow, stats, err := tm.walletTracker.IsLowActivity(
		ctx,
		traderAddr,
		cfg.MaxMarketsForLow,
	)
	if err != nil {
		tm.logger.Warn("failed to check wallet activity",
			zap.String("wallet", shortID(traderAddr)),
			zap.Error(err),
		)
		return
	}

	// Build alert reasons
	var reasons []AlertReason

	// Check for low activity
	if isLow {
		reasons = append(reasons, AlertReasonLowActivity)
	}

	// Check for high win rate on non-obvious bets (entry price below threshold)
	// This filters out wins on "obvious" bets (e.g., buying at 98¢)
	// Also skip if this is a BUY/SELL at a high price (obvious outcome or cashing out)
	resolvedCount := stats.WinCount + stats.LossCount
	suspiciousCount := stats.SuspiciousWins + stats.SuspiciousLosses
	isObviousBuy := strings.EqualFold(event.Side, "BUY") && price >= 0.95
	isObviousSell := strings.EqualFold(event.Side, "SELL") && price >= 0.95
	if suspiciousCount >= cfg.MinResolvedForWinRate &&
		stats.SuspiciousWinRate >= cfg.HighWinRateThreshold &&
		!isObviousBuy && !isObviousSell {
		reasons = append(reasons, AlertReasonHighWinRate)
	}

	// Check for extreme low odds bet (longshot/contrarian)
	if price <= cfg.ExtremeLowPrice && notional >= cfg.ExtremeMinNotional {
		reasons = append(reasons, AlertReasonExtremeBet)
	}

	// Check for rapid trading (multiple trades in short window)
	tradeTime := time.Unix(event.GetTimestampUnix(), 0)
	isRapid, _, _ := tm.checkRapidTrading(traderAddr, notional, tradeTime)
	if isRapid {
		reasons = append(reasons, AlertReasonRapidTrading)
	}

	// Check for new wallet making large bet
	if stats.UniqueMarkets <= cfg.NewWalletMaxMarkets && notional >= cfg.NewWalletMinNotional {
		reasons = append(reasons, AlertReasonNewWallet)
	}

	// Determine outcome from token ID position (needed for hedge tracking)
	outcome := "Unknown"
	if info != nil && len(info.TokenIDs) > 0 && len(info.Outcomes) > 0 {
		for i, tokenID := range info.TokenIDs {
			if tokenID == event.AssetID && i < len(info.Outcomes) {
				outcome = info.Outcomes[i]
				break
			}
		}
	}

	// Check for contrarian bet (buying at low prices = betting against consensus)
	isBuy := strings.EqualFold(event.Side, "BUY")
	if isBuy && price <= cfg.ContrarianMaxPrice && notional >= cfg.ContrarianMinNotional {
		reasons = append(reasons, AlertReasonContrarianBet)
	}

	// Check for massive trade (whale alert) - only at non-obvious prices
	if notional >= cfg.MassiveTradeMinNotional && price <= cfg.MassiveTradeMaxPrice {
		reasons = append(reasons, AlertReasonMassiveTrade)
	}

	// Check for known contrarian winner (from historical cache)
	// Only alert if the current trade is at a non-obvious price (same threshold as massive trades)
	if tm.contrarianCache != nil && tm.contrarianCache.ShouldAlert(traderAddr) && price <= cfg.MassiveTradeMaxPrice {
		reasons = append(reasons, AlertReasonContrarianWinner)
	}

	// Copy trading detection
	if tm.copyTracker != nil {
		// Get market condition ID for copy tracking
		var conditionID, tokenID string
		if info != nil {
			conditionID = info.ConditionID
		}
		tokenID = event.AssetID

		// Check if this trader is a leader (high win rate or contrarian winner)
		if tm.copyTracker.IsLeader(stats) {
			tm.copyTracker.RecordLeaderTrade(traderAddr, conditionID, tokenID, strings.ToUpper(event.Side))
		}

		// Check if this trade is copying a leader
		if isCopy, _ := tm.copyTracker.CheckForCopy(traderAddr, conditionID, tokenID, strings.ToUpper(event.Side)); isCopy {
			if tm.copyTracker.ShouldAlert(traderAddr) {
				reasons = append(reasons, AlertReasonCopyTrader)
			}
		}
	}

	// Hedge pattern detection (for SELL trades)
	isSell := strings.EqualFold(event.Side, "SELL")
	var hedgeAlerts []HedgeAlert
	if tm.hedgeTracker != nil && isSell {
		var conditionID, marketTitle, marketSlug string
		if info != nil {
			conditionID = info.ConditionID
			marketTitle = info.Title
			marketSlug = info.Slug
		}
		if conditionID != "" && tm.hedgeTracker.ShouldCheckPositions(traderAddr, conditionID) {
			hedgeAlerts = tm.hedgeTracker.ProcessTrade(ctx, traderAddr, conditionID, marketTitle, marketSlug, event.Side, outcome, size, price)
			for _, ha := range hedgeAlerts {
				if ha.Reason == "hedge_removal" {
					reasons = append(reasons, AlertReasonHedgeRemoval)
				} else if ha.Reason == "asymmetric_exit" {
					reasons = append(reasons, AlertReasonAsymmetricExit)
				}
			}
		}
	}

	// Advanced pattern detection (conviction doubling, stealth accumulation, exit timing)
	var patternAlerts []PatternAlert
	if tm.patternTracker != nil {
		var conditionID, marketTitle, marketSlug string
		if info != nil {
			conditionID = info.ConditionID
			marketTitle = info.Title
			marketSlug = info.Slug
		}
		patternAlerts = tm.patternTracker.ProcessTrade(ctx, ProcessTradeInput{
			Wallet:      traderAddr,
			ConditionID: conditionID,
			MarketTitle: marketTitle,
			MarketSlug:  marketSlug,
			TokenID:     event.AssetID,
			Outcome:     outcome,
			Side:        strings.ToUpper(event.Side),
			Size:        size,
			Price:       price,
			Notional:    notional,
			Timestamp:   tradeTime,
		})
		for _, pa := range patternAlerts {
			switch pa.Reason {
			case "conviction_doubling":
				reasons = append(reasons, AlertReasonConvictionDoubling)
			case "stealth_accumulation":
				reasons = append(reasons, AlertReasonStealthAccumulation)
			}
		}
		// Check for perfect exit timing (based on historical stats)
		if tm.patternTracker.ShouldAlertPerfectTiming(traderAddr) {
			reasons = append(reasons, AlertReasonPerfectExitTiming)
		}
	}

	// Skip if no alert reasons
	if len(reasons) == 0 {
		tm.filterStatsMu.Lock()
		tm.skippedHighActivity++
		tm.filterStatsMu.Unlock()
		return
	}

	// Skip obvious trades (high price = high probability outcome)
	if cfg.ObviousPrice > 0 && price >= cfg.ObviousPrice {
		tm.filterStatsMu.Lock()
		tm.skippedObvious++
		tm.filterStatsMu.Unlock()
		return
	}

	// Skip traders with no resolved positions (N/A win rate) or win rate <= 50%
	// Exception: allow special alerts regardless of win rate
	hasSpecialReason := false
	for _, r := range reasons {
		if r == AlertReasonNewWallet || r == AlertReasonContrarianBet || r == AlertReasonMassiveTrade || r == AlertReasonContrarianWinner || r == AlertReasonCopyTrader || r == AlertReasonHedgeRemoval || r == AlertReasonAsymmetricExit || r == AlertReasonConvictionDoubling || r == AlertReasonPerfectExitTiming || r == AlertReasonStealthAccumulation {
			hasSpecialReason = true
			break
		}
	}
	if !hasSpecialReason && (resolvedCount == 0 || stats.WinRate <= 0.50) {
		return
	}

	// Build wallet and market URLs
	walletURL := fmt.Sprintf("https://polymarket.com/profile/%s", traderAddr)
	var marketURL string
	var marketTitle, marketImage string
	var conditionID string
	if info != nil {
		marketTitle = info.Title
		marketImage = info.Image
		conditionID = info.ConditionID
		if info.Slug != "" {
			marketURL = fmt.Sprintf("https://polymarket.com/event/%s", info.Slug)
		}
	}

	// Fetch inventory for this wallet in this market
	inv := tm.fetchInventory(ctx, traderAddr, conditionID, outcome, isSell)

	// Build and send alert
	alert := notifier.TradeAlert{
		TraderName:        shortID(traderAddr),
		TraderAddress:     traderAddr,
		WalletURL:         walletURL,
		Side:              strings.ToUpper(event.Side),
		Shares:            size,
		Price:             price,
		Notional:          notional,
		MarketTitle:       marketTitle,
		MarketURL:         marketURL,
		MarketImage:       marketImage,
		ConditionID:       conditionID,
		Outcome:           outcome,
		UniqueMarkets:     stats.UniqueMarkets,
		WinRate:           stats.WinRate,
		WinCount:          stats.WinCount,
		LossCount:         stats.LossCount,
		InventoryShares:   inv.Shares,
		InventoryAvgPrice: inv.AvgPrice,
		InventoryValue:    inv.CurrentValue,
		HasInventory:      inv.HasInventory,
		ClosedCostBasis:   inv.ClosedCostBasis,
		ClosedRealizedPnl: inv.ClosedRealizedPnl,
		HasClosedInfo:     inv.HasClosedInfo,
		Reasons:           reasons,
		Timestamp:         tradeTime,
	}

	// Add hedge info if available
	for _, ha := range hedgeAlerts {
		if ha.Reason == "hedge_removal" {
			alert.HedgeYesSizeBefore = ha.YesSizeBefore
			alert.HedgeNoSizeBefore = ha.NoSizeBefore
			alert.HedgeYesSizeAfter = ha.YesSizeAfter
			alert.HedgeNoSizeAfter = ha.NoSizeAfter
			alert.HedgeSoldSide = ha.SoldSide
			alert.HedgeSoldPct = ha.ReductionPct
			alert.HasHedgeInfo = true
		} else if ha.Reason == "asymmetric_exit" {
			alert.AsymmetricWinExits = ha.WinExitCount
			alert.AsymmetricLossExits = ha.LossExitCount
			alert.AsymmetricWinAvgHoldSec = ha.AvgWinHoldTime.Seconds()
			alert.AsymmetricLossAvgHoldSec = ha.AvgLossHoldTime.Seconds()
			alert.AsymmetricRatio = ha.AsymmetricRatio
			alert.HasAsymmetricInfo = true
		}
	}

	// Add pattern alert info if available
	for _, pa := range patternAlerts {
		switch pa.Reason {
		case "conviction_doubling":
			alert.ConvictionExistingSize = pa.ConvictionExistingSize
			alert.ConvictionExistingAvg = pa.ConvictionExistingAvg
			alert.ConvictionCurrentPrice = pa.ConvictionCurrentPrice
			alert.ConvictionLossPct = pa.ConvictionLossPct
			alert.ConvictionAddedSize = pa.ConvictionAddedSize
			alert.ConvictionAddedValue = pa.ConvictionAddedValue
			alert.HasConvictionInfo = true
		case "stealth_accumulation":
			alert.StealthTradeCount = pa.StealthTradeCount
			alert.StealthTotalSize = pa.StealthTotalSize
			alert.StealthTotalValue = pa.StealthTotalValue
			alert.StealthAvgPrice = pa.StealthAvgPrice
			alert.StealthSpreadMins = pa.StealthSpreadMins
			alert.HasStealthInfo = true
		}
	}

	// Add perfect exit timing stats if available
	if tm.patternTracker != nil {
		if stats := tm.patternTracker.GetExitTimingStats(traderAddr); stats != nil && stats.VerifiedExits >= tm.patternTracker.config.PerfectExitMinExits {
			alert.PerfectExitScore = stats.AvgTimingScore
			alert.PerfectExitCount = stats.VerifiedExits
			alert.PerfectExitPerfectCount = stats.PerfectExits
			alert.HasPerfectExitInfo = true
		}
	}

	tm.sendAlert(alert)
}

// runPolling runs the fallback polling mode.
func (tm *TradeMonitor) runPolling(ctx context.Context) {
	cfg := tm.getConfig()
	ticker := time.NewTicker(cfg.PollInterval)
	defer ticker.Stop()

	tm.logger.Info("trade monitor using polling mode",
		zap.Duration("pollInterval", cfg.PollInterval),
	)

	// Initial poll
	tm.poll(ctx)

	for {
		select {
		case <-ctx.Done():
			tm.logger.Info("trade monitor shutting down")
			return
		case <-ticker.C:
			tm.poll(ctx)
		}
	}
}

func (tm *TradeMonitor) poll(ctx context.Context) {
	tm.mu.RLock()
	markets := tm.markets
	tm.mu.RUnlock()

	if len(markets) == 0 {
		return
	}

	trades, err := tm.apiClient.GetTrades(ctx, markets, 100)
	if err != nil {
		tm.logger.Warn("failed to fetch trades", zap.Error(err))
		return
	}

	for _, trade := range trades {
		tm.processTrade(ctx, trade)
	}
}

// SetWSConnected sets the WebSocket connection state.
func (tm *TradeMonitor) SetWSConnected(connected bool) {
	tm.wsConnectedMu.Lock()
	tm.wsConnected = connected
	tm.wsConnectedMu.Unlock()
}

// IsWSConnected returns the WebSocket connection state.
func (tm *TradeMonitor) IsWSConnected() bool {
	tm.wsConnectedMu.RLock()
	defer tm.wsConnectedMu.RUnlock()
	return tm.wsConnected
}

func (tm *TradeMonitor) processTrade(ctx context.Context, trade polymarketapi.Trade) {
	// Get config once at the start for thread-safe access
	cfg := tm.getConfig()

	// Skip if already seen
	tradeKey := tm.tradeKey(trade)
	tm.seenMu.Lock()
	if _, seen := tm.seenTrades[tradeKey]; seen {
		tm.seenMu.Unlock()
		return
	}
	tm.seenTrades[tradeKey] = struct{}{}
	tm.seenMu.Unlock()

	// Calculate notional value
	notional := trade.Size * trade.Price
	if notional < cfg.MinNotional {
		return
	}

	// Check wallet activity and win rate
	isLow, stats, err := tm.walletTracker.IsLowActivity(
		ctx,
		trade.ProxyWallet,
		cfg.MaxMarketsForLow,
	)
	if err != nil {
		tm.logger.Warn("failed to check wallet activity",
			zap.String("wallet", shortID(trade.ProxyWallet)),
			zap.Error(err),
		)
		return
	}

	// Build alert reasons
	var reasons []AlertReason

	// Check for low activity
	if isLow {
		reasons = append(reasons, AlertReasonLowActivity)
	}

	// Check for high win rate on non-obvious bets (entry price below threshold)
	// This filters out wins on "obvious" bets (e.g., buying at 98¢)
	// Also skip if this is a BUY/SELL at a high price (obvious outcome or cashing out)
	resolvedCount := stats.WinCount + stats.LossCount
	suspiciousCount := stats.SuspiciousWins + stats.SuspiciousLosses
	isObviousBuy := strings.EqualFold(trade.Side, "BUY") && trade.Price >= 0.95
	isObviousSell := strings.EqualFold(trade.Side, "SELL") && trade.Price >= 0.95
	if suspiciousCount >= cfg.MinResolvedForWinRate &&
		stats.SuspiciousWinRate >= cfg.HighWinRateThreshold &&
		!isObviousBuy && !isObviousSell {
		reasons = append(reasons, AlertReasonHighWinRate)
	}

	// Check for extreme low odds bet (longshot/contrarian)
	if trade.Price <= cfg.ExtremeLowPrice && notional >= cfg.ExtremeMinNotional {
		reasons = append(reasons, AlertReasonExtremeBet)
	}

	// Check for rapid trading (multiple trades in short window)
	tradeTime := time.Unix(trade.Timestamp, 0)
	isRapid, _, _ := tm.checkRapidTrading(trade.ProxyWallet, notional, tradeTime)
	if isRapid {
		reasons = append(reasons, AlertReasonRapidTrading)
	}

	// Check for new wallet making large bet
	if stats.UniqueMarkets <= cfg.NewWalletMaxMarkets && notional >= cfg.NewWalletMinNotional {
		reasons = append(reasons, AlertReasonNewWallet)
	}

	// Check for contrarian bet (buying at low prices = betting against consensus)
	isBuy := strings.EqualFold(trade.Side, "BUY")
	if isBuy && trade.Price <= cfg.ContrarianMaxPrice && notional >= cfg.ContrarianMinNotional {
		reasons = append(reasons, AlertReasonContrarianBet)
	}

	// Check for massive trade (whale alert) - only at non-obvious prices
	if notional >= cfg.MassiveTradeMinNotional && trade.Price <= cfg.MassiveTradeMaxPrice {
		reasons = append(reasons, AlertReasonMassiveTrade)
	}

	// Check for known contrarian winner (from historical cache)
	// Only alert if the current trade is at a non-obvious price (same threshold as massive trades)
	if tm.contrarianCache != nil && tm.contrarianCache.ShouldAlert(trade.ProxyWallet) && trade.Price <= cfg.MassiveTradeMaxPrice {
		reasons = append(reasons, AlertReasonContrarianWinner)
	}

	// Copy trading detection
	if tm.copyTracker != nil {
		conditionID := trade.ConditionID
		tokenID := trade.Asset

		// Check if this trader is a leader (high win rate or contrarian winner)
		if tm.copyTracker.IsLeader(stats) {
			tm.copyTracker.RecordLeaderTrade(trade.ProxyWallet, conditionID, tokenID, strings.ToUpper(trade.Side))
		}

		// Check if this trade is copying a leader
		if isCopy, _ := tm.copyTracker.CheckForCopy(trade.ProxyWallet, conditionID, tokenID, strings.ToUpper(trade.Side)); isCopy {
			if tm.copyTracker.ShouldAlert(trade.ProxyWallet) {
				reasons = append(reasons, AlertReasonCopyTrader)
			}
		}
	}

	// Skip if no alert reasons
	if len(reasons) == 0 {
		return
	}

	// Skip obvious trades (high price = high probability outcome)
	if cfg.ObviousPrice > 0 && trade.Price >= cfg.ObviousPrice {
		tm.filterStatsMu.Lock()
		tm.skippedObvious++
		tm.filterStatsMu.Unlock()
		return
	}

	// Skip traders with no resolved positions (N/A win rate) or win rate <= 50%
	// Exception: allow special alerts regardless of win rate
	hasSpecialReason := false
	for _, r := range reasons {
		if r == AlertReasonNewWallet || r == AlertReasonContrarianBet || r == AlertReasonMassiveTrade || r == AlertReasonContrarianWinner || r == AlertReasonCopyTrader || r == AlertReasonHedgeRemoval || r == AlertReasonAsymmetricExit {
			hasSpecialReason = true
			break
		}
	}
	if !hasSpecialReason && (resolvedCount == 0 || stats.WinRate <= 0.50) {
		return
	}

	// Build wallet and market URLs
	walletURL := fmt.Sprintf("https://polymarket.com/profile/%s", trade.ProxyWallet)
	var marketURL string
	if trade.Slug != "" {
		marketURL = fmt.Sprintf("https://polymarket.com/event/%s", trade.Slug)
	}

	// Fetch inventory for this wallet in this market
	isSell := strings.EqualFold(trade.Side, "SELL")
	inv := tm.fetchInventory(ctx, trade.ProxyWallet, trade.ConditionID, trade.Outcome, isSell)

	// Create and send alert
	alert := notifier.TradeAlert{
		TraderName:        tm.traderDisplayName(trade),
		TraderAddress:     trade.ProxyWallet,
		WalletURL:         walletURL,
		Side:              trade.Side,
		Shares:            trade.Size,
		Price:             trade.Price,
		Notional:          notional,
		MarketTitle:       trade.Title,
		MarketURL:         marketURL,
		MarketImage:       trade.Icon,
		ConditionID:       trade.ConditionID,
		Outcome:           trade.Outcome,
		UniqueMarkets:     stats.UniqueMarkets,
		WinRate:           stats.WinRate,
		WinCount:          stats.WinCount,
		LossCount:         stats.LossCount,
		InventoryShares:   inv.Shares,
		InventoryAvgPrice: inv.AvgPrice,
		InventoryValue:    inv.CurrentValue,
		HasInventory:      inv.HasInventory,
		ClosedCostBasis:   inv.ClosedCostBasis,
		ClosedRealizedPnl: inv.ClosedRealizedPnl,
		HasClosedInfo:     inv.HasClosedInfo,
		Reasons:           reasons,
		Timestamp:         tradeTime,
	}

	tm.sendAlert(alert)
}

func (tm *TradeMonitor) tradeKey(trade polymarketapi.Trade) string {
	// Use transaction hash + asset as unique key
	return fmt.Sprintf("%s:%s", trade.TransactionHash, trade.Asset)
}

func (tm *TradeMonitor) traderDisplayName(trade polymarketapi.Trade) string {
	if trade.Name != "" {
		return trade.Name
	}
	if trade.Pseudonym != "" {
		return trade.Pseudonym
	}
	return shortID(trade.ProxyWallet)
}

// inventoryResult holds the result of fetching inventory and closed position info.
type inventoryResult struct {
	Shares       float64
	AvgPrice     float64
	CurrentValue float64
	HasInventory bool

	// Closed position info (for sells that closed the position)
	ClosedCostBasis   float64
	ClosedRealizedPnl float64
	HasClosedInfo     bool
}

// fetchInventory fetches the wallet's current position in the given market.
// For sells, also fetches closed position info if the position was closed.
func (tm *TradeMonitor) fetchInventory(ctx context.Context, wallet, conditionID, outcome string, isSell bool) inventoryResult {
	result := inventoryResult{}

	if wallet == "" || conditionID == "" {
		return result
	}

	positions, err := tm.apiClient.GetPositions(ctx, wallet, conditionID, 10)
	if err != nil {
		tm.logger.Debug("failed to fetch inventory",
			zap.String("wallet", shortID(wallet)),
			zap.String("conditionID", shortID(conditionID)),
			zap.Error(err),
		)
		return result
	}

	// Find the position matching the outcome
	for _, p := range positions {
		if p.Outcome == outcome {
			result.Shares = p.Size
			result.AvgPrice = p.AvgPrice
			result.CurrentValue = p.CurrentValue
			result.HasInventory = true
			return result
		}
	}

	// No position found for this outcome - wallet has no inventory
	result.HasInventory = true

	// For sells with no remaining position, fetch closed position info
	if isSell {
		closedPositions, err := tm.apiClient.GetClosedPositions(ctx, wallet, 20, 0)
		if err != nil {
			tm.logger.Debug("failed to fetch closed positions",
				zap.String("wallet", shortID(wallet)),
				zap.Error(err),
			)
			return result
		}

		// Find the most recent closed position for this market/outcome
		for _, cp := range closedPositions {
			if cp.ConditionID == conditionID && cp.Outcome == outcome {
				result.ClosedCostBasis = cp.AvgPrice
				result.ClosedRealizedPnl = cp.RealizedPnl
				result.HasClosedInfo = true
				break
			}
		}
	}

	return result
}

func (tm *TradeMonitor) sendAlert(alert notifier.TradeAlert) {
	tm.filterStatsMu.Lock()
	tm.alertsSent++
	for _, r := range alert.Reasons {
		switch r {
		case AlertReasonLowActivity:
			tm.alertsLowActivity++
		case AlertReasonHighWinRate:
			tm.alertsHighWinRate++
		case AlertReasonExtremeBet:
			tm.alertsExtremeBet++
		case AlertReasonRapidTrading:
			tm.alertsRapidTrading++
		case AlertReasonNewWallet:
			tm.alertsNewWallet++
		case AlertReasonContrarianBet:
			tm.alertsContrarianBet++
		case AlertReasonMassiveTrade:
			tm.alertsMassiveTrade++
		case AlertReasonContrarianWinner:
			tm.alertsContrarianWinner++
		case AlertReasonCopyTrader:
			tm.alertsCopyTrader++
		case AlertReasonHedgeRemoval:
			tm.alertsHedgeRemoval++
		case AlertReasonAsymmetricExit:
			tm.alertsAsymmetricExit++
		case AlertReasonResolutionConfirmed:
			tm.alertsResolutionConfirmed++
		case AlertReasonConvictionDoubling:
			tm.alertsConvictionDoubling++
		case AlertReasonPerfectExitTiming:
			tm.alertsPerfectExitTiming++
		case AlertReasonStealthAccumulation:
			tm.alertsStealthAccumulation++
		}
	}
	tm.filterStatsMu.Unlock()

	// Build reasons string for logging
	reasonStrs := make([]string, len(alert.Reasons))
	for i, r := range alert.Reasons {
		reasonStrs[i] = string(r)
	}

	// Track recent alerts (keep last 100 for filtering)
	alertInfo := RecentAlertInfo{
		Timestamp:     time.Now(),
		WalletAddress: alert.TraderAddress,
		WalletName:    alert.TraderName,
		MarketTitle:   alert.MarketTitle,
		ConditionID:   alert.ConditionID,
		Outcome:       alert.Outcome,
		Side:          alert.Side,
		Notional:      alert.Notional,
		Reasons:       reasonStrs,
		// Extended details
		Price:         alert.Price,
		Shares:        alert.Shares,
		MarketURL:     alert.MarketURL,
		MarketImage:   alert.MarketImage,
		WalletURL:     alert.WalletURL,
		WinRate:       alert.WinRate,
		WinCount:      alert.WinCount,
		LossCount:     alert.LossCount,
		UniqueMarkets: alert.UniqueMarkets,
		HasInventory:  alert.HasInventory,
		InvShares:     alert.InventoryShares,
		InvAvgPrice:   alert.InventoryAvgPrice,
		InvValue:      alert.InventoryValue,
	}
	tm.recentAlertsMu.Lock()
	tm.recentAlerts = append([]RecentAlertInfo{alertInfo}, tm.recentAlerts...)
	if len(tm.recentAlerts) > 100 {
		tm.recentAlerts = tm.recentAlerts[:100]
	}
	tm.recentAlertsMu.Unlock()

	// Track alerts by wallet
	tm.alertsByWalletMu.Lock()
	tm.alertsByWallet[alert.TraderAddress]++
	tm.alertsByWalletMu.Unlock()

	// Track last alert time
	now := time.Now()
	tm.lastAlertTimeMu.Lock()
	tm.lastAlertTime = now
	tm.lastAlertTimeMu.Unlock()

	// Track alert history for sparkline/timeline (keep last 24h)
	tm.alertHistoryMu.Lock()
	tm.alertHistory = append(tm.alertHistory, now)
	// Prune entries older than 24h
	cutoff := now.Add(-24 * time.Hour)
	startIdx := 0
	for i, t := range tm.alertHistory {
		if t.After(cutoff) {
			startIdx = i
			break
		}
	}
	if startIdx > 0 {
		tm.alertHistory = tm.alertHistory[startIdx:]
	}
	tm.alertHistoryMu.Unlock()

	// Track alerts by market
	if alert.ConditionID != "" {
		tm.alertsByMarketMu.Lock()
		if tm.alertsByMarket[alert.ConditionID] == nil {
			tm.alertsByMarket[alert.ConditionID] = &MarketAlertInfo{
				ConditionID: alert.ConditionID,
				Title:       alert.MarketTitle,
			}
		}
		tm.alertsByMarket[alert.ConditionID].Count++
		tm.alertsByMarketMu.Unlock()
	}

	// Log the alert
	tm.logger.Info("TRADE ALERT",
		zap.Strings("reasons", reasonStrs),
		zap.String("trader", alert.TraderName),
		zap.String("address", shortID(alert.TraderAddress)),
		zap.String("side", alert.Side),
		zap.Float64("shares", alert.Shares),
		zap.Float64("price", alert.Price),
		zap.Float64("notional", alert.Notional),
		zap.String("market", alert.MarketTitle),
		zap.String("outcome", alert.Outcome),
		zap.Int("uniqueMarkets", alert.UniqueMarkets),
		zap.Float64("winRate", alert.WinRate),
		zap.String("winRecord", fmt.Sprintf("%d-%d", alert.WinCount, alert.LossCount)),
	)

	// Send to all registered notifiers
	if tm.notifier != nil {
		tm.notifier.SendTradeAlert(alert)
	}
}

// PruneSeenTrades removes old entries from the seen trades map.
// Call periodically to prevent memory growth.
func (tm *TradeMonitor) PruneSeenTrades(maxAge time.Duration) {
	// Simple approach: clear the map periodically
	// A more sophisticated approach would track timestamps per entry
	tm.seenMu.Lock()
	defer tm.seenMu.Unlock()

	// If map is getting large, clear it
	if len(tm.seenTrades) > 10000 {
		tm.seenTrades = make(map[string]struct{})
		tm.logger.Info("pruned seen trades cache")
	}
}

// checkRapidTrading checks if a wallet is trading rapidly and records the trade.
// Returns true if rapid trading is detected, along with trade count and total notional.
func (tm *TradeMonitor) checkRapidTrading(wallet string, notional float64, tradeTime time.Time) (isRapid bool, count int, total float64) {
	cfg := tm.getConfig()
	tm.recentTradesMu.Lock()
	defer tm.recentTradesMu.Unlock()

	now := time.Now()
	cutoff := now.Add(-cfg.RapidTradeWindow)

	// Get existing trades and filter to recent ones
	trades := tm.recentTrades[wallet]
	var recent []recentTrade
	for _, t := range trades {
		if t.timestamp.After(cutoff) {
			recent = append(recent, t)
		}
	}

	// Add the current trade
	recent = append(recent, recentTrade{
		timestamp: tradeTime,
		notional:  notional,
	})

	// Store updated list
	tm.recentTrades[wallet] = recent

	// Calculate totals
	count = len(recent)
	for _, t := range recent {
		total += t.notional
	}

	// Check if rapid trading threshold is met
	isRapid = count >= cfg.RapidTradeMinCount && total >= cfg.RapidTradeMinTotal
	return
}

// pruneRecentTrades removes old entries from the recent trades map.
func (tm *TradeMonitor) pruneRecentTrades() {
	cfg := tm.getConfig()
	tm.recentTradesMu.Lock()
	defer tm.recentTradesMu.Unlock()

	cutoff := time.Now().Add(-cfg.RapidTradeWindow)
	for wallet, trades := range tm.recentTrades {
		var recent []recentTrade
		for _, t := range trades {
			if t.timestamp.After(cutoff) {
				recent = append(recent, t)
			}
		}
		if len(recent) == 0 {
			delete(tm.recentTrades, wallet)
		} else {
			tm.recentTrades[wallet] = recent
		}
	}
}

// SeenTradesSnapshot represents a serializable snapshot of seen trades.
type SeenTradesSnapshot struct {
	Version   int       `json:"version"`
	Timestamp time.Time `json:"timestamp"`
	Trades    []string  `json:"trades"`
}

// ExportSeenTrades exports the seen trades as a snapshot.
func (tm *TradeMonitor) ExportSeenTrades() *SeenTradesSnapshot {
	tm.seenMu.Lock()
	defer tm.seenMu.Unlock()

	trades := make([]string, 0, len(tm.seenTrades))
	for key := range tm.seenTrades {
		trades = append(trades, key)
	}

	return &SeenTradesSnapshot{
		Version:   1,
		Timestamp: time.Now(),
		Trades:    trades,
	}
}

// ImportSeenTrades imports a snapshot of seen trades.
func (tm *TradeMonitor) ImportSeenTrades(snapshot *SeenTradesSnapshot) int {
	if snapshot == nil || len(snapshot.Trades) == 0 {
		return 0
	}

	tm.seenMu.Lock()
	defer tm.seenMu.Unlock()

	imported := 0
	for _, key := range snapshot.Trades {
		if _, exists := tm.seenTrades[key]; !exists {
			tm.seenTrades[key] = struct{}{}
			imported++
		}
	}

	tm.logger.Info("imported seen trades",
		zap.Int("imported", imported),
		zap.Int("total", len(tm.seenTrades)),
		zap.Time("snapshotTime", snapshot.Timestamp),
	)

	return imported
}

// SeenTradesCount returns the number of seen trades.
func (tm *TradeMonitor) SeenTradesCount() int {
	tm.seenMu.Lock()
	defer tm.seenMu.Unlock()
	return len(tm.seenTrades)
}

// SeenMarketsCount returns the number of unique markets (asset IDs) seen via WebSocket.
func (tm *TradeMonitor) SeenMarketsCount() int {
	tm.seenMarketsMu.Lock()
	defer tm.seenMarketsMu.Unlock()
	return len(tm.seenMarkets)
}

// EventTypeCounts returns a copy of the event type counts for debugging.
func (tm *TradeMonitor) EventTypeCounts() map[string]int {
	tm.eventTypesMu.Lock()
	defer tm.eventTypesMu.Unlock()
	result := make(map[string]int, len(tm.eventTypes))
	for k, v := range tm.eventTypes {
		result[k] = v
	}
	return result
}

// FilterStats holds filter statistics for debugging.
type FilterStats struct {
	SkippedLowNotional         int
	SkippedNoWallet            int
	SkippedHighActivity        int
	SkippedObvious             int
	AlertsSent                 int
	AlertsLowActivity          int
	AlertsHighWinRate          int
	AlertsExtremeBet           int
	AlertsRapidTrading         int
	AlertsNewWallet            int
	AlertsContrarianBet        int
	AlertsMassiveTrade         int
	AlertsContrarianWinner     int
	AlertsCopyTrader           int
	AlertsHedgeRemoval         int
	AlertsAsymmetricExit       int
	AlertsResolutionConfirmed  int
	AlertsConvictionDoubling   int
	AlertsPerfectExitTiming    int
	AlertsStealthAccumulation  int
}

// FilterStats returns the current filter statistics.
func (tm *TradeMonitor) FilterStats() FilterStats {
	tm.filterStatsMu.Lock()
	defer tm.filterStatsMu.Unlock()
	return FilterStats{
		SkippedLowNotional:        tm.skippedLowNotional,
		SkippedNoWallet:           tm.skippedNoWallet,
		SkippedHighActivity:       tm.skippedHighActivity,
		SkippedObvious:            tm.skippedObvious,
		AlertsSent:                tm.alertsSent,
		AlertsLowActivity:         tm.alertsLowActivity,
		AlertsHighWinRate:         tm.alertsHighWinRate,
		AlertsExtremeBet:          tm.alertsExtremeBet,
		AlertsRapidTrading:        tm.alertsRapidTrading,
		AlertsNewWallet:           tm.alertsNewWallet,
		AlertsContrarianBet:       tm.alertsContrarianBet,
		AlertsMassiveTrade:        tm.alertsMassiveTrade,
		AlertsContrarianWinner:    tm.alertsContrarianWinner,
		AlertsCopyTrader:          tm.alertsCopyTrader,
		AlertsHedgeRemoval:        tm.alertsHedgeRemoval,
		AlertsAsymmetricExit:      tm.alertsAsymmetricExit,
		AlertsResolutionConfirmed: tm.alertsResolutionConfirmed,
		AlertsConvictionDoubling:  tm.alertsConvictionDoubling,
		AlertsPerfectExitTiming:   tm.alertsPerfectExitTiming,
		AlertsStealthAccumulation: tm.alertsStealthAccumulation,
	}
}

// RecentAlerts returns the most recent alerts (up to 10).
func (tm *TradeMonitor) RecentAlerts() []RecentAlertInfo {
	tm.recentAlertsMu.RLock()
	defer tm.recentAlertsMu.RUnlock()
	result := make([]RecentAlertInfo, len(tm.recentAlerts))
	copy(result, tm.recentAlerts)
	return result
}

// WalletAlertCounts represents a wallet and its alert count.
type WalletAlertCount struct {
	Address string `json:"address"`
	Count   int    `json:"count"`
}

// TopAlertingWallets returns the top N wallets by alert count.
func (tm *TradeMonitor) TopAlertingWallets(limit int) []WalletAlertCount {
	tm.alertsByWalletMu.RLock()
	defer tm.alertsByWalletMu.RUnlock()

	// Convert map to slice
	wallets := make([]WalletAlertCount, 0, len(tm.alertsByWallet))
	for addr, count := range tm.alertsByWallet {
		wallets = append(wallets, WalletAlertCount{Address: addr, Count: count})
	}

	// Sort by count descending
	for i := 0; i < len(wallets)-1; i++ {
		for j := i + 1; j < len(wallets); j++ {
			if wallets[j].Count > wallets[i].Count {
				wallets[i], wallets[j] = wallets[j], wallets[i]
			}
		}
	}

	// Return top N
	if limit > len(wallets) {
		limit = len(wallets)
	}
	return wallets[:limit]
}

// LastAlertTime returns the time of the last alert sent.
func (tm *TradeMonitor) LastAlertTime() time.Time {
	tm.lastAlertTimeMu.RLock()
	defer tm.lastAlertTimeMu.RUnlock()
	return tm.lastAlertTime
}

// MonitoredMarkets returns the list of monitored market titles.
func (tm *TradeMonitor) MonitoredMarkets() []string {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	// Deduplicate by condition ID since multiple tokens map to same market
	seen := make(map[string]bool)
	var titles []string
	for _, info := range tm.tokenToInfo {
		if info != nil && !seen[info.ConditionID] {
			seen[info.ConditionID] = true
			titles = append(titles, info.Title)
		}
	}
	return titles
}

// AlertHistory returns alert timestamps for the specified duration.
func (tm *TradeMonitor) AlertHistory(duration time.Duration) []time.Time {
	tm.alertHistoryMu.RLock()
	defer tm.alertHistoryMu.RUnlock()

	cutoff := time.Now().Add(-duration)
	var result []time.Time
	for _, t := range tm.alertHistory {
		if t.After(cutoff) {
			result = append(result, t)
		}
	}
	return result
}

// AlertCountsInPeriods returns alert counts for 1h, 24h, and 7d periods.
func (tm *TradeMonitor) AlertCountsInPeriods() (hour, day, week int) {
	tm.alertHistoryMu.RLock()
	defer tm.alertHistoryMu.RUnlock()

	now := time.Now()
	hourCutoff := now.Add(-1 * time.Hour)
	dayCutoff := now.Add(-24 * time.Hour)
	weekCutoff := now.Add(-7 * 24 * time.Hour)

	for _, t := range tm.alertHistory {
		if t.After(hourCutoff) {
			hour++
		}
		if t.After(dayCutoff) {
			day++
		}
		if t.After(weekCutoff) {
			week++
		}
	}
	return
}

// TopAlertingMarkets returns the top N markets by alert count.
func (tm *TradeMonitor) TopAlertingMarkets(limit int) []MarketAlertInfo {
	tm.alertsByMarketMu.RLock()
	defer tm.alertsByMarketMu.RUnlock()

	// Convert map to slice
	markets := make([]MarketAlertInfo, 0, len(tm.alertsByMarket))
	for _, info := range tm.alertsByMarket {
		if info != nil {
			markets = append(markets, *info)
		}
	}

	// Sort by count descending
	for i := 0; i < len(markets)-1; i++ {
		for j := i + 1; j < len(markets); j++ {
			if markets[j].Count > markets[i].Count {
				markets[i], markets[j] = markets[j], markets[i]
			}
		}
	}

	// Return top N
	if limit > len(markets) {
		limit = len(markets)
	}
	return markets[:limit]
}

// AlertHistoryBuckets returns alert counts bucketed by time intervals for sparkline.
// Returns an array of counts, one per bucket, from oldest to newest.
func (tm *TradeMonitor) AlertHistoryBuckets(duration time.Duration, buckets int) []int {
	tm.alertHistoryMu.RLock()
	defer tm.alertHistoryMu.RUnlock()

	now := time.Now()
	bucketDuration := duration / time.Duration(buckets)
	result := make([]int, buckets)

	for _, t := range tm.alertHistory {
		age := now.Sub(t)
		if age < 0 || age > duration {
			continue
		}
		bucketIdx := int(age / bucketDuration)
		if bucketIdx >= buckets {
			bucketIdx = buckets - 1
		}
		// Reverse index so newest is at the end
		result[buckets-1-bucketIdx]++
	}

	return result
}
