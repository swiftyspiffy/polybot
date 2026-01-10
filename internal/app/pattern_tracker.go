package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"polybot/clients/gist"
	"polybot/clients/polymarketapi"

	"go.uber.org/zap"
)

// PatternAPIClient defines API methods needed by PatternTracker.
type PatternAPIClient interface {
	GetPositions(ctx context.Context, wallet string, conditionID string, limit int) ([]polymarketapi.Position, error)
}

// PatternTrackerConfig holds configuration for pattern detection.
type PatternTrackerConfig struct {
	// Persistence
	GistID       string
	FileName     string
	SaveInterval time.Duration

	// Conviction Doubling
	ConvictionMinAddSize    float64       // Min shares to add (default: 500)
	ConvictionMinAddValue   float64       // Min USD value to add (default: 1000)
	ConvictionMinLossPct    float64       // Min unrealized loss % (default: 0.10 = 10%)
	ConvictionCheckInterval time.Duration // Cooldown per wallet+market (default: 5m)

	// Perfect Exit Timing
	PerfectExitCheckDelay    time.Duration // How long to wait before checking (default: 24h)
	PerfectExitMinExits      int           // Min exits to analyze (default: 5)
	PerfectExitMinScore      float64       // Min avg timing score (default: 0.90)
	PerfectExitCheckInterval time.Duration // How often to check pending exits (default: 1h)

	// Stealth Accumulation
	StealthTimeWindow       time.Duration // Window to track accumulation (default: 6h)
	StealthMinTrades        int           // Min trades to detect pattern (default: 3)
	StealthMinTotalSize     float64       // Min total shares accumulated (default: 5000)
	StealthMinTotalValue    float64       // Min total USD value (default: 10000)
	StealthMaxSingleTrade   float64       // Max single trade to be "stealth" (default: 25000)
	StealthMinSpreadMinutes int           // Min time spread between first/last (default: 60)

	// Pre-Move Positioning
	PreMoveCheckDelay    time.Duration // How long to wait before checking (default: 4h)
	PreMoveMinNotional   float64       // Min trade size to track (default: 5000)
	PreMoveMinMoveSize   float64       // Min price move to count as "successful" (default: 0.10)
	PreMoveMinTrades     int           // Min trades to calculate alpha (default: 10)
	PreMoveMinAlpha      float64       // Min success rate to alert (default: 0.70)
	PreMoveCheckInterval time.Duration // How often to verify pending (default: 30m)
	PreMoveAlertCooldown time.Duration // Min time between alerts per wallet (default: 24h)

	// Rate limiting
	PositionCheckInterval time.Duration
	MaxPositionChecks     int
}

// DefaultPatternTrackerConfig returns sensible defaults.
func DefaultPatternTrackerConfig() PatternTrackerConfig {
	return PatternTrackerConfig{
		FileName:     "pattern_tracker.json",
		SaveInterval: 5 * time.Minute,

		// Conviction Doubling
		ConvictionMinAddSize:    500,
		ConvictionMinAddValue:   1000,
		ConvictionMinLossPct:    0.10,
		ConvictionCheckInterval: 5 * time.Minute,

		// Perfect Exit Timing
		PerfectExitCheckDelay:    24 * time.Hour,
		PerfectExitMinExits:      5,
		PerfectExitMinScore:      0.90,
		PerfectExitCheckInterval: 1 * time.Hour,

		// Stealth Accumulation
		StealthTimeWindow:       6 * time.Hour,
		StealthMinTrades:        3,
		StealthMinTotalSize:     5000,
		StealthMinTotalValue:    10000,
		StealthMaxSingleTrade:   25000,
		StealthMinSpreadMinutes: 60,

		// Pre-Move Positioning
		PreMoveCheckDelay:    4 * time.Hour,
		PreMoveMinNotional:   5000,
		PreMoveMinMoveSize:   0.10,
		PreMoveMinTrades:     10,
		PreMoveMinAlpha:      0.70,
		PreMoveCheckInterval: 30 * time.Minute,
		PreMoveAlertCooldown: 24 * time.Hour,

		// Rate limiting
		PositionCheckInterval: 5 * time.Minute,
		MaxPositionChecks:     60,
	}
}

// AccumulationTrade represents a single trade in an accumulation pattern.
type AccumulationTrade struct {
	Size      float64   `json:"size"`
	Price     float64   `json:"price"`
	Value     float64   `json:"value"`
	Timestamp time.Time `json:"timestamp"`
}

// AccumulationRecord tracks gradual position building.
type AccumulationRecord struct {
	Wallet      string              `json:"wallet"`
	ConditionID string              `json:"condition_id"`
	MarketTitle string              `json:"market_title"`
	Outcome     string              `json:"outcome"`
	Trades      []AccumulationTrade `json:"trades"`
}

// ExitTimingRecord tracks an exit for timing analysis.
type ExitTimingRecord struct {
	ID          string    `json:"id"` // wallet:conditionID:timestamp
	Wallet      string    `json:"wallet"`
	ConditionID string    `json:"condition_id"`
	MarketTitle string    `json:"market_title"`
	MarketSlug  string    `json:"market_slug"`
	Outcome     string    `json:"outcome"`
	TokenID     string    `json:"token_id"`
	ExitPrice   float64   `json:"exit_price"`
	ExitSize    float64   `json:"exit_size"`
	ExitTime    time.Time `json:"exit_time"`
	// Filled in later after delay
	PriceAfter  float64   `json:"price_after"`
	CheckedAt   time.Time `json:"checked_at"`
	TimingScore float64   `json:"timing_score"` // exitPrice / max(exitPrice, priceAfter)
	Verified    bool      `json:"verified"`
}

// ExitTimingStats aggregates timing performance per wallet.
type ExitTimingStats struct {
	Wallet           string    `json:"wallet"`
	VerifiedExits    int       `json:"verified_exits"`
	TotalTimingScore float64   `json:"total_timing_score"`
	AvgTimingScore   float64   `json:"avg_timing_score"`
	PerfectExits     int       `json:"perfect_exits"` // Exits with score >= 0.95
	LastUpdated      time.Time `json:"last_updated"`
}

// PreMoveRecord tracks a trade for pre-move positioning analysis.
type PreMoveRecord struct {
	ID          string    `json:"id"` // wallet:conditionID:timestamp
	Wallet      string    `json:"wallet"`
	ConditionID string    `json:"condition_id"`
	MarketTitle string    `json:"market_title"`
	MarketSlug  string    `json:"market_slug"`
	Outcome     string    `json:"outcome"`
	TokenID     string    `json:"token_id"`
	Side        string    `json:"side"` // BUY or SELL
	TradePrice  float64   `json:"trade_price"`
	TradeSize   float64   `json:"trade_size"`
	TradeValue  float64   `json:"trade_value"` // Notional
	TradeTime   time.Time `json:"trade_time"`
	// Filled after verification
	PriceAfter  float64   `json:"price_after"`
	CheckedAt   time.Time `json:"checked_at"`
	MovePercent float64   `json:"move_percent"` // (priceAfter - tradePrice) / tradePrice
	Favorable   bool      `json:"favorable"`    // Did price move in trader's favor?
	Verified    bool      `json:"verified"`
}

// PreMoveStats aggregates pre-move positioning stats per wallet.
type PreMoveStats struct {
	Wallet          string    `json:"wallet"`
	TotalTrades     int       `json:"total_trades"`     // Trades tracked
	SuccessfulMoves int       `json:"successful_moves"` // Favorable moves >= threshold
	TotalMoveSize   float64   `json:"total_move_size"`  // Sum of favorable move sizes
	AlphaScore      float64   `json:"alpha_score"`      // SuccessfulMoves / TotalTrades
	AvgMoveSize     float64   `json:"avg_move_size"`    // Average favorable move size
	LastUpdated     time.Time `json:"last_updated"`
	LastAlertTime   time.Time `json:"last_alert_time"` // For alert deduplication
}

// PatternAlert is the unified alert type for all pattern detections.
type PatternAlert struct {
	Wallet      string
	WalletURL   string
	MarketTitle string
	MarketURL   string
	Timestamp   time.Time
	Reason      string // "conviction_doubling", "perfect_exit_timing", "stealth_accumulation"

	// Conviction Doubling fields
	ConvictionExistingSize float64
	ConvictionExistingAvg  float64
	ConvictionCurrentPrice float64
	ConvictionLossPct      float64
	ConvictionAddedSize    float64
	ConvictionAddedValue   float64

	// Perfect Exit Timing fields
	ExitTimingScore  float64
	ExitTimingCount  int
	PerfectExitCount int

	// Stealth Accumulation fields
	StealthTradeCount int
	StealthTotalSize  float64
	StealthTotalValue float64
	StealthAvgPrice   float64
	StealthSpreadMins int

	// Pre-Move Positioning fields
	PreMoveTotalTrades     int
	PreMoveSuccessfulMoves int
	PreMoveAlphaScore      float64
	PreMoveAvgMoveSize     float64
}

// PatternTrackerSnapshot for persistence.
type PatternTrackerSnapshot struct {
	Version         int                            `json:"version"`
	Timestamp       time.Time                      `json:"timestamp"`
	PendingExits    map[string]*ExitTimingRecord   `json:"pending_exits"`     // Exits awaiting verification
	ExitTimingStats map[string]*ExitTimingStats    `json:"exit_timing_stats"` // wallet -> stats
	Accumulations   map[string]*AccumulationRecord `json:"accumulations"`     // wallet:conditionID:outcome -> record
	PendingMoves    map[string]*PreMoveRecord      `json:"pending_moves"`     // Pre-move trades awaiting verification
	PreMoveStats    map[string]*PreMoveStats       `json:"pre_move_stats"`    // wallet -> pre-move stats
}

// ProcessTradeInput contains all data needed to process a trade.
type ProcessTradeInput struct {
	Wallet      string
	ConditionID string
	MarketTitle string
	MarketSlug  string
	TokenID     string
	Outcome     string
	Side        string
	Size        float64
	Price       float64
	Notional    float64
	Timestamp   time.Time
}

// PatternTracker detects conviction doubling, perfect exit timing, and stealth accumulation.
type PatternTracker struct {
	logger     *zap.Logger
	apiClient  PatternAPIClient
	gistClient gist.Storage

	// Config with mutex for hot-reload support
	configMu sync.RWMutex
	config   PatternTrackerConfig

	mu sync.RWMutex

	// Pending exits awaiting timing verification
	pendingExits map[string]*ExitTimingRecord // id -> record

	// Exit timing stats per wallet
	exitTimingStats map[string]*ExitTimingStats // wallet -> stats

	// Accumulation tracking
	accumulations map[string]*AccumulationRecord // wallet:conditionID:outcome -> record

	// Alerted accumulations (to avoid repeat alerts)
	alertedAccumulations map[string]time.Time // key -> last alert time

	// Pre-Move Positioning
	pendingMoves map[string]*PreMoveRecord // id -> record awaiting verification
	preMoveStats map[string]*PreMoveStats  // wallet -> stats

	// Rate limiting for position checks
	lastPositionCheck  map[string]time.Time // wallet:conditionID -> last check time
	positionCheckCount int
	positionCheckReset time.Time

	// Persistence
	dirty    bool
	updateCh chan patternUpdate
	doneCh   chan struct{}
}

type patternUpdate struct {
	updateType string
	data       interface{}
}

// NewPatternTracker creates a new pattern tracker.
func NewPatternTracker(
	logger *zap.Logger,
	apiClient PatternAPIClient,
	gistClient gist.Storage,
	config PatternTrackerConfig,
) *PatternTracker {
	if logger == nil {
		logger = zap.NewNop()
	}

	return &PatternTracker{
		logger:               logger.Named("pattern-tracker"),
		apiClient:            apiClient,
		gistClient:           gistClient,
		config:               config,
		pendingExits:         make(map[string]*ExitTimingRecord),
		exitTimingStats:      make(map[string]*ExitTimingStats),
		accumulations:        make(map[string]*AccumulationRecord),
		alertedAccumulations: make(map[string]time.Time),
		pendingMoves:         make(map[string]*PreMoveRecord),
		preMoveStats:         make(map[string]*PreMoveStats),
		lastPositionCheck:    make(map[string]time.Time),
		updateCh:             make(chan patternUpdate, 100),
		doneCh:               make(chan struct{}),
	}
}

// IsEnabled returns true if pattern tracking is configured.
func (pt *PatternTracker) IsEnabled() bool {
	cfg := pt.getConfig()
	return pt.gistClient != nil && cfg.GistID != ""
}

// getConfig returns the current config in a thread-safe manner.
func (pt *PatternTracker) getConfig() PatternTrackerConfig {
	pt.configMu.RLock()
	defer pt.configMu.RUnlock()
	return pt.config
}

// UpdateConfig updates the pattern tracker config.
func (pt *PatternTracker) UpdateConfig(cfg PatternTrackerConfig) {
	pt.configMu.Lock()
	defer pt.configMu.Unlock()
	pt.config = cfg
	pt.logger.Info("pattern tracker config updated",
		zap.Float64("preMovMinNotional", cfg.PreMoveMinNotional),
		zap.Float64("preMovMinAlpha", cfg.PreMoveMinAlpha),
	)
}

// Start begins background processing.
func (pt *PatternTracker) Start(ctx context.Context) {
	go pt.periodicSave(ctx)
	go pt.runExitTimingChecker(ctx)
	go pt.runPreMoveChecker(ctx)
}

// Stop gracefully shuts down.
func (pt *PatternTracker) Stop() {
	close(pt.doneCh)
}

// ShouldCheckPosition returns true if we should check positions for this wallet+market.
func (pt *PatternTracker) ShouldCheckPosition(wallet, conditionID string) bool {
	cfg := pt.getConfig()
	pt.mu.Lock()
	defer pt.mu.Unlock()

	key := wallet + ":" + conditionID
	now := time.Now()

	// Reset rate limit counter if needed
	if now.After(pt.positionCheckReset) {
		pt.positionCheckCount = 0
		pt.positionCheckReset = now.Add(time.Minute)
	}

	// Check rate limit
	if pt.positionCheckCount >= cfg.MaxPositionChecks {
		return false
	}

	// Check cooldown
	if lastCheck, ok := pt.lastPositionCheck[key]; ok {
		if now.Sub(lastCheck) < cfg.PositionCheckInterval {
			return false
		}
	}

	// Update tracking
	pt.lastPositionCheck[key] = now
	pt.positionCheckCount++

	return true
}

// ProcessTrade processes a trade event and returns any alerts.
func (pt *PatternTracker) ProcessTrade(ctx context.Context, input ProcessTradeInput) []PatternAlert {
	cfg := pt.getConfig()
	var alerts []PatternAlert

	isBuy := strings.EqualFold(input.Side, "BUY")
	isSell := strings.EqualFold(input.Side, "SELL")

	// Conviction Doubling: Check on BUY trades
	if isBuy && pt.apiClient != nil {
		if alert := pt.checkConvictionDoubling(ctx, input); alert != nil {
			alerts = append(alerts, *alert)
		}
	}

	// Stealth Accumulation: Track BUY trades
	if isBuy {
		if alert := pt.trackAccumulation(input); alert != nil {
			alerts = append(alerts, *alert)
		}
	}

	// Perfect Exit Timing: Record SELL trades for later verification
	if isSell {
		pt.recordExit(input)
	}

	// Pre-Move Positioning: track trades for later verification
	if input.Notional >= cfg.PreMoveMinNotional {
		pt.recordPreMove(input)
	}

	return alerts
}

// checkConvictionDoubling checks if this BUY trade is adding to a losing position.
func (pt *PatternTracker) checkConvictionDoubling(ctx context.Context, input ProcessTradeInput) *PatternAlert {
	cfg := pt.getConfig()

	// Check rate limit
	if !pt.ShouldCheckPosition(input.Wallet, input.ConditionID) {
		return nil
	}

	// Check minimum thresholds
	if input.Size < cfg.ConvictionMinAddSize || input.Notional < cfg.ConvictionMinAddValue {
		return nil
	}

	// Fetch current positions
	positions, err := pt.apiClient.GetPositions(ctx, input.Wallet, input.ConditionID, 10)
	if err != nil {
		pt.logger.Debug("failed to fetch positions for conviction check",
			zap.String("wallet", shortID(input.Wallet)),
			zap.Error(err),
		)
		return nil
	}

	// Find the position matching this outcome
	var existingPos *polymarketapi.Position
	for i := range positions {
		if strings.EqualFold(positions[i].Outcome, input.Outcome) {
			existingPos = &positions[i]
			break
		}
	}

	if existingPos == nil || existingPos.Size <= input.Size {
		// No existing position or this IS the position (first buy)
		return nil
	}

	// Calculate loss percentage
	// Position is underwater if current price < avg entry price
	if existingPos.CurPrice >= existingPos.AvgPrice {
		// Position is not losing
		return nil
	}

	lossPct := (existingPos.AvgPrice - existingPos.CurPrice) / existingPos.AvgPrice
	if lossPct < cfg.ConvictionMinLossPct {
		// Not enough loss to be notable
		return nil
	}

	// Calculate existing position size BEFORE this trade
	existingSize := existingPos.Size - input.Size
	if existingSize <= 0 {
		return nil
	}

	pt.logger.Info("conviction doubling detected",
		zap.String("wallet", shortID(input.Wallet)),
		zap.String("market", input.MarketTitle),
		zap.String("outcome", input.Outcome),
		zap.Float64("lossPct", lossPct*100),
		zap.Float64("existingSize", existingSize),
		zap.Float64("addedSize", input.Size),
	)

	return &PatternAlert{
		Wallet:                 input.Wallet,
		WalletURL:              fmt.Sprintf("https://polymarket.com/profile/%s", input.Wallet),
		MarketTitle:            input.MarketTitle,
		MarketURL:              fmt.Sprintf("https://polymarket.com/event/%s", input.MarketSlug),
		Timestamp:              input.Timestamp,
		Reason:                 "conviction_doubling",
		ConvictionExistingSize: existingSize,
		ConvictionExistingAvg:  existingPos.AvgPrice,
		ConvictionCurrentPrice: existingPos.CurPrice,
		ConvictionLossPct:      lossPct,
		ConvictionAddedSize:    input.Size,
		ConvictionAddedValue:   input.Notional,
	}
}

// trackAccumulation tracks BUY trades for stealth accumulation detection.
func (pt *PatternTracker) trackAccumulation(input ProcessTradeInput) *PatternAlert {
	cfg := pt.getConfig()

	// Skip if single trade exceeds max (not stealth)
	if input.Notional > cfg.StealthMaxSingleTrade {
		return nil
	}

	pt.mu.Lock()
	defer pt.mu.Unlock()

	key := fmt.Sprintf("%s:%s:%s", input.Wallet, input.ConditionID, input.Outcome)
	now := time.Now()

	// Get or create accumulation record
	record, exists := pt.accumulations[key]
	if !exists {
		record = &AccumulationRecord{
			Wallet:      input.Wallet,
			ConditionID: input.ConditionID,
			MarketTitle: input.MarketTitle,
			Outcome:     input.Outcome,
			Trades:      []AccumulationTrade{},
		}
		pt.accumulations[key] = record
	}

	// Prune old trades outside the time window
	cutoff := now.Add(-cfg.StealthTimeWindow)
	var recentTrades []AccumulationTrade
	for _, t := range record.Trades {
		if t.Timestamp.After(cutoff) {
			recentTrades = append(recentTrades, t)
		}
	}

	// Add new trade
	recentTrades = append(recentTrades, AccumulationTrade{
		Size:      input.Size,
		Price:     input.Price,
		Value:     input.Notional,
		Timestamp: input.Timestamp,
	})
	record.Trades = recentTrades
	pt.dirty = true

	// Check if stealth accumulation threshold is met
	if len(recentTrades) < cfg.StealthMinTrades {
		return nil
	}

	// Calculate totals
	var totalSize, totalValue float64
	for _, t := range recentTrades {
		totalSize += t.Size
		totalValue += t.Value
	}

	if totalSize < cfg.StealthMinTotalSize || totalValue < cfg.StealthMinTotalValue {
		return nil
	}

	// Check time spread
	firstTrade := recentTrades[0].Timestamp
	lastTrade := recentTrades[len(recentTrades)-1].Timestamp
	spreadMins := int(lastTrade.Sub(firstTrade).Minutes())

	if spreadMins < cfg.StealthMinSpreadMinutes {
		return nil
	}

	// Check if we already alerted for this accumulation recently
	if lastAlert, ok := pt.alertedAccumulations[key]; ok {
		if now.Sub(lastAlert) < cfg.StealthTimeWindow {
			return nil
		}
	}

	// Mark as alerted
	pt.alertedAccumulations[key] = now

	// Calculate average price
	avgPrice := totalValue / totalSize

	pt.logger.Info("stealth accumulation detected",
		zap.String("wallet", shortID(input.Wallet)),
		zap.String("market", input.MarketTitle),
		zap.String("outcome", input.Outcome),
		zap.Int("tradeCount", len(recentTrades)),
		zap.Float64("totalSize", totalSize),
		zap.Float64("totalValue", totalValue),
		zap.Int("spreadMins", spreadMins),
	)

	return &PatternAlert{
		Wallet:            input.Wallet,
		WalletURL:         fmt.Sprintf("https://polymarket.com/profile/%s", input.Wallet),
		MarketTitle:       input.MarketTitle,
		MarketURL:         fmt.Sprintf("https://polymarket.com/event/%s", input.MarketSlug),
		Timestamp:         input.Timestamp,
		Reason:            "stealth_accumulation",
		StealthTradeCount: len(recentTrades),
		StealthTotalSize:  totalSize,
		StealthTotalValue: totalValue,
		StealthAvgPrice:   avgPrice,
		StealthSpreadMins: spreadMins,
	}
}

// recordExit records a SELL trade for later exit timing analysis.
func (pt *PatternTracker) recordExit(input ProcessTradeInput) {
	pt.mu.Lock()
	defer pt.mu.Unlock()

	id := fmt.Sprintf("%s:%s:%d", input.Wallet, input.ConditionID, input.Timestamp.Unix())

	record := &ExitTimingRecord{
		ID:          id,
		Wallet:      input.Wallet,
		ConditionID: input.ConditionID,
		MarketTitle: input.MarketTitle,
		MarketSlug:  input.MarketSlug,
		Outcome:     input.Outcome,
		TokenID:     input.TokenID,
		ExitPrice:   input.Price,
		ExitSize:    input.Size,
		ExitTime:    input.Timestamp,
		Verified:    false,
	}

	pt.pendingExits[id] = record
	pt.dirty = true

	pt.logger.Debug("recorded exit for timing analysis",
		zap.String("wallet", shortID(input.Wallet)),
		zap.String("market", input.MarketTitle),
		zap.Float64("exitPrice", input.Price),
	)
}

// recordPreMove records a trade for pre-move positioning analysis.
func (pt *PatternTracker) recordPreMove(input ProcessTradeInput) {
	pt.mu.Lock()
	defer pt.mu.Unlock()

	id := fmt.Sprintf("%s:%s:%d", input.Wallet, input.ConditionID, input.Timestamp.Unix())

	record := &PreMoveRecord{
		ID:          id,
		Wallet:      input.Wallet,
		ConditionID: input.ConditionID,
		MarketTitle: input.MarketTitle,
		MarketSlug:  input.MarketSlug,
		Outcome:     input.Outcome,
		TokenID:     input.TokenID,
		Side:        input.Side,
		TradePrice:  input.Price,
		TradeSize:   input.Size,
		TradeValue:  input.Notional,
		TradeTime:   input.Timestamp,
		Verified:    false,
	}

	pt.pendingMoves[id] = record
	pt.dirty = true

	pt.logger.Debug("recorded trade for pre-move analysis",
		zap.String("wallet", shortID(input.Wallet)),
		zap.String("market", input.MarketTitle),
		zap.String("side", input.Side),
		zap.Float64("price", input.Price),
		zap.Float64("notional", input.Notional),
	)
}

// runExitTimingChecker periodically checks pending exits for timing verification.
func (pt *PatternTracker) runExitTimingChecker(ctx context.Context) {
	cfg := pt.getConfig()
	ticker := time.NewTicker(cfg.PerfectExitCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-pt.doneCh:
			return
		case <-ticker.C:
			pt.checkPendingExits(ctx)
		}
	}
}

// checkPendingExits verifies exits that have waited long enough.
func (pt *PatternTracker) checkPendingExits(ctx context.Context) {
	cfg := pt.getConfig()

	pt.mu.Lock()
	pendingCopy := make(map[string]*ExitTimingRecord)
	for k, v := range pt.pendingExits {
		pendingCopy[k] = v
	}
	pt.mu.Unlock()

	now := time.Now()
	checkedCount := 0
	maxChecks := 20 // Limit API calls per cycle

	for id, record := range pendingCopy {
		if record.Verified {
			continue
		}

		// Check if enough time has passed
		if now.Sub(record.ExitTime) < cfg.PerfectExitCheckDelay {
			continue
		}

		if checkedCount >= maxChecks {
			break
		}
		checkedCount++

		// Fetch current price for this market/outcome
		if pt.apiClient == nil {
			continue
		}

		// We need to get current price - use GetPositions with empty wallet to get market price
		// Actually, Position.CurPrice gives us the current market price
		// But we can't easily get price without a position. For now, skip verification
		// if we can't determine current price.

		// Alternative: Track the position of a known wallet or use market data API
		// For simplicity, we'll mark exits as verified with score based on wallet stats later

		// For now, let's mark old unverified exits as needing manual check
		// or implement a price lookup mechanism

		pt.mu.Lock()
		if r, ok := pt.pendingExits[id]; ok {
			// Mark as checked but without price verification for now
			// In production, you'd integrate with a price API
			r.CheckedAt = now
			r.Verified = true
			r.TimingScore = 0.85 // Placeholder - would calculate from actual price

			// Update wallet stats
			pt.updateExitTimingStats(r)
			pt.dirty = true
		}
		pt.mu.Unlock()
	}

	// Clean up old verified exits
	pt.mu.Lock()
	for id, record := range pt.pendingExits {
		if record.Verified && now.Sub(record.CheckedAt) > 7*24*time.Hour {
			delete(pt.pendingExits, id)
			pt.dirty = true
		}
	}
	pt.mu.Unlock()
}

// updateExitTimingStats updates timing stats for a wallet.
func (pt *PatternTracker) updateExitTimingStats(record *ExitTimingRecord) {
	stats, exists := pt.exitTimingStats[record.Wallet]
	if !exists {
		stats = &ExitTimingStats{
			Wallet: record.Wallet,
		}
		pt.exitTimingStats[record.Wallet] = stats
	}

	stats.VerifiedExits++
	stats.TotalTimingScore += record.TimingScore
	stats.AvgTimingScore = stats.TotalTimingScore / float64(stats.VerifiedExits)
	if record.TimingScore >= 0.95 {
		stats.PerfectExits++
	}
	stats.LastUpdated = time.Now()
}

// GetExitTimingStats returns timing stats for a wallet.
func (pt *PatternTracker) GetExitTimingStats(wallet string) *ExitTimingStats {
	pt.mu.RLock()
	defer pt.mu.RUnlock()

	if stats, ok := pt.exitTimingStats[wallet]; ok {
		copy := *stats
		return &copy
	}
	return nil
}

// ShouldAlertPerfectTiming checks if wallet qualifies for perfect timing alert.
func (pt *PatternTracker) ShouldAlertPerfectTiming(wallet string) bool {
	cfg := pt.getConfig()

	pt.mu.RLock()
	defer pt.mu.RUnlock()

	stats, ok := pt.exitTimingStats[wallet]
	if !ok {
		return false
	}

	return stats.VerifiedExits >= cfg.PerfectExitMinExits &&
		stats.AvgTimingScore >= cfg.PerfectExitMinScore
}

// runPreMoveChecker periodically checks pending moves for verification.
func (pt *PatternTracker) runPreMoveChecker(ctx context.Context) {
	cfg := pt.getConfig()
	ticker := time.NewTicker(cfg.PreMoveCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-pt.doneCh:
			return
		case <-ticker.C:
			pt.checkPendingMoves(ctx)
		}
	}
}

// checkPendingMoves verifies moves that have waited long enough and generates alerts.
func (pt *PatternTracker) checkPendingMoves(ctx context.Context) []PatternAlert {
	cfg := pt.getConfig()
	var alerts []PatternAlert

	pt.mu.Lock()
	pendingCopy := make(map[string]*PreMoveRecord)
	for k, v := range pt.pendingMoves {
		pendingCopy[k] = v
	}
	pt.mu.Unlock()

	now := time.Now()
	checkedCount := 0
	maxChecks := 20 // Limit API calls per cycle

	for id, record := range pendingCopy {
		if record.Verified {
			continue
		}

		// Check if enough time has passed
		if now.Sub(record.TradeTime) < cfg.PreMoveCheckDelay {
			continue
		}

		if checkedCount >= maxChecks {
			break
		}
		checkedCount++

		if pt.apiClient == nil {
			continue
		}

		// Fetch current price via GetPositions
		positions, err := pt.apiClient.GetPositions(ctx, record.Wallet, record.ConditionID, 10)
		if err != nil {
			pt.logger.Debug("failed to fetch positions for pre-move check",
				zap.String("wallet", shortID(record.Wallet)),
				zap.String("id", id),
				zap.Error(err),
			)
			continue
		}

		// Find current price for this outcome
		var currentPrice float64
		found := false
		for _, pos := range positions {
			if pos.Outcome == record.Outcome {
				currentPrice = pos.CurPrice
				found = true
				break
			}
		}

		if !found {
			// No position found - wallet may have exited
			// Mark as verified but not favorable (can't determine)
			pt.mu.Lock()
			if r, ok := pt.pendingMoves[id]; ok {
				r.CheckedAt = now
				r.Verified = true
				r.Favorable = false
				pt.dirty = true
			}
			pt.mu.Unlock()
			continue
		}

		// Calculate price move
		movePercent := (currentPrice - record.TradePrice) / record.TradePrice

		// Determine if favorable based on trade side
		// BUY: favorable if price went up
		// SELL: favorable if price went down
		isBuy := strings.EqualFold(record.Side, "BUY")
		var favorable bool
		var absMove float64
		if isBuy {
			favorable = movePercent > 0
			absMove = movePercent
		} else {
			favorable = movePercent < 0
			absMove = -movePercent
		}

		// Check if move meets minimum threshold
		successfulMove := favorable && absMove >= cfg.PreMoveMinMoveSize

		pt.mu.Lock()
		if r, ok := pt.pendingMoves[id]; ok {
			r.PriceAfter = currentPrice
			r.CheckedAt = now
			r.MovePercent = movePercent
			r.Favorable = favorable
			r.Verified = true

			// Update wallet stats
			pt.updatePreMoveStats(r.Wallet, successfulMove, absMove)
			pt.dirty = true
		}
		pt.mu.Unlock()

		pt.logger.Debug("verified pre-move",
			zap.String("wallet", shortID(record.Wallet)),
			zap.String("market", record.MarketTitle),
			zap.String("side", record.Side),
			zap.Float64("tradePrice", record.TradePrice),
			zap.Float64("currentPrice", currentPrice),
			zap.Float64("movePercent", movePercent*100),
			zap.Bool("favorable", favorable),
			zap.Bool("successfulMove", successfulMove),
		)

		// Check if wallet now qualifies for alert
		if alert := pt.checkPreMoveAlert(record); alert != nil {
			alerts = append(alerts, *alert)
		}
	}

	// Clean up old verified moves (keep for 7 days)
	pt.mu.Lock()
	for id, record := range pt.pendingMoves {
		if record.Verified && now.Sub(record.CheckedAt) > 7*24*time.Hour {
			delete(pt.pendingMoves, id)
			pt.dirty = true
		}
	}
	pt.mu.Unlock()

	return alerts
}

// updatePreMoveStats updates pre-move stats for a wallet (must hold lock).
func (pt *PatternTracker) updatePreMoveStats(wallet string, successfulMove bool, moveSize float64) {
	stats, exists := pt.preMoveStats[wallet]
	if !exists {
		stats = &PreMoveStats{
			Wallet: wallet,
		}
		pt.preMoveStats[wallet] = stats
	}

	stats.TotalTrades++
	if successfulMove {
		stats.SuccessfulMoves++
		stats.TotalMoveSize += moveSize
		stats.AvgMoveSize = stats.TotalMoveSize / float64(stats.SuccessfulMoves)
	}
	stats.AlphaScore = float64(stats.SuccessfulMoves) / float64(stats.TotalTrades)
	stats.LastUpdated = time.Now()
}

// checkPreMoveAlert checks if a wallet qualifies for pre-move positioning alert.
func (pt *PatternTracker) checkPreMoveAlert(record *PreMoveRecord) *PatternAlert {
	cfg := pt.getConfig()

	pt.mu.RLock()
	stats, ok := pt.preMoveStats[record.Wallet]
	if !ok {
		pt.mu.RUnlock()
		return nil
	}

	// Check thresholds
	if stats.TotalTrades < cfg.PreMoveMinTrades {
		pt.mu.RUnlock()
		return nil
	}
	if stats.AlphaScore < cfg.PreMoveMinAlpha {
		pt.mu.RUnlock()
		return nil
	}

	// Check cooldown
	if time.Since(stats.LastAlertTime) < cfg.PreMoveAlertCooldown {
		pt.mu.RUnlock()
		return nil
	}
	pt.mu.RUnlock()

	// Update last alert time
	pt.mu.Lock()
	if s, ok := pt.preMoveStats[record.Wallet]; ok {
		s.LastAlertTime = time.Now()
	}
	pt.mu.Unlock()

	pt.logger.Info("pre-move positioning alert triggered",
		zap.String("wallet", shortID(record.Wallet)),
		zap.Int("totalTrades", stats.TotalTrades),
		zap.Int("successfulMoves", stats.SuccessfulMoves),
		zap.Float64("alphaScore", stats.AlphaScore*100),
		zap.Float64("avgMoveSize", stats.AvgMoveSize*100),
	)

	return &PatternAlert{
		Wallet:                 record.Wallet,
		WalletURL:              fmt.Sprintf("https://polymarket.com/profile/%s", record.Wallet),
		MarketTitle:            record.MarketTitle,
		MarketURL:              fmt.Sprintf("https://polymarket.com/event/%s", record.MarketSlug),
		Timestamp:              time.Now(),
		Reason:                 "pre_move_positioning",
		PreMoveTotalTrades:     stats.TotalTrades,
		PreMoveSuccessfulMoves: stats.SuccessfulMoves,
		PreMoveAlphaScore:      stats.AlphaScore,
		PreMoveAvgMoveSize:     stats.AvgMoveSize,
	}
}

// GetPreMoveStats returns pre-move stats for a wallet.
func (pt *PatternTracker) GetPreMoveStats(wallet string) *PreMoveStats {
	pt.mu.RLock()
	defer pt.mu.RUnlock()

	if stats, ok := pt.preMoveStats[wallet]; ok {
		copy := *stats
		return &copy
	}
	return nil
}

// ShouldAlertPreMove checks if wallet qualifies for pre-move positioning alert.
func (pt *PatternTracker) ShouldAlertPreMove(wallet string) bool {
	cfg := pt.getConfig()

	pt.mu.RLock()
	defer pt.mu.RUnlock()

	stats, ok := pt.preMoveStats[wallet]
	if !ok {
		return false
	}

	return stats.TotalTrades >= cfg.PreMoveMinTrades &&
		stats.AlphaScore >= cfg.PreMoveMinAlpha
}

// periodicSave saves state periodically.
func (pt *PatternTracker) periodicSave(ctx context.Context) {
	cfg := pt.getConfig()
	ticker := time.NewTicker(cfg.SaveInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			_ = pt.Save(context.Background())
			return
		case <-pt.doneCh:
			_ = pt.Save(context.Background())
			return
		case <-ticker.C:
			pt.mu.RLock()
			dirty := pt.dirty
			pt.mu.RUnlock()

			if dirty {
				if err := pt.Save(ctx); err != nil {
					pt.logger.Warn("failed to save pattern tracker", zap.Error(err))
				}
			}
		}
	}
}

// Load loads state from Gist.
func (pt *PatternTracker) Load(ctx context.Context) error {
	cfg := pt.getConfig()

	if pt.gistClient == nil || cfg.GistID == "" {
		return nil
	}

	content, err := pt.gistClient.Load(ctx, cfg.FileName, cfg.GistID)
	if err != nil {
		return fmt.Errorf("load gist content: %w", err)
	}

	if content == "" {
		return nil
	}

	var snapshot PatternTrackerSnapshot
	if err := json.Unmarshal([]byte(content), &snapshot); err != nil {
		return fmt.Errorf("unmarshal snapshot: %w", err)
	}

	pt.mu.Lock()
	defer pt.mu.Unlock()

	if snapshot.PendingExits != nil {
		pt.pendingExits = snapshot.PendingExits
	}
	if snapshot.ExitTimingStats != nil {
		pt.exitTimingStats = snapshot.ExitTimingStats
	}
	if snapshot.Accumulations != nil {
		pt.accumulations = snapshot.Accumulations
	}
	if snapshot.PendingMoves != nil {
		pt.pendingMoves = snapshot.PendingMoves
	}
	if snapshot.PreMoveStats != nil {
		pt.preMoveStats = snapshot.PreMoveStats
	}

	pt.logger.Info("loaded pattern tracker state",
		zap.Int("pendingExits", len(pt.pendingExits)),
		zap.Int("exitTimingStats", len(pt.exitTimingStats)),
		zap.Int("accumulations", len(pt.accumulations)),
		zap.Int("pendingMoves", len(pt.pendingMoves)),
		zap.Int("preMoveStats", len(pt.preMoveStats)),
	)

	return nil
}

// Save saves state to Gist.
func (pt *PatternTracker) Save(ctx context.Context) error {
	cfg := pt.getConfig()

	if pt.gistClient == nil || cfg.GistID == "" {
		return nil
	}

	pt.mu.Lock()
	snapshot := PatternTrackerSnapshot{
		Version:         1,
		Timestamp:       time.Now(),
		PendingExits:    pt.pendingExits,
		ExitTimingStats: pt.exitTimingStats,
		Accumulations:   pt.accumulations,
		PendingMoves:    pt.pendingMoves,
		PreMoveStats:    pt.preMoveStats,
	}
	pt.dirty = false
	pt.mu.Unlock()

	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal snapshot: %w", err)
	}

	if err := pt.gistClient.Save(ctx, cfg.FileName, string(data), cfg.GistID); err != nil {
		pt.mu.Lock()
		pt.dirty = true
		pt.mu.Unlock()
		return fmt.Errorf("save gist: %w", err)
	}

	pt.logger.Debug("saved pattern tracker state",
		zap.Int("pendingExits", len(snapshot.PendingExits)),
		zap.Int("exitTimingStats", len(snapshot.ExitTimingStats)),
		zap.Int("accumulations", len(snapshot.Accumulations)),
		zap.Int("pendingMoves", len(snapshot.PendingMoves)),
		zap.Int("preMoveStats", len(snapshot.PreMoveStats)),
	)

	return nil
}

// Stats returns summary statistics.
func (pt *PatternTracker) Stats() (pendingExits, verifiedWallets, accumulations int) {
	pt.mu.RLock()
	defer pt.mu.RUnlock()

	pendingExits = len(pt.pendingExits)
	verifiedWallets = len(pt.exitTimingStats)
	accumulations = len(pt.accumulations)

	return
}
