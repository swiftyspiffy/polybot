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

// HedgeAPIClient defines the API methods needed by HedgeTracker.
type HedgeAPIClient interface {
	GetPositions(ctx context.Context, wallet string, conditionID string, limit int) ([]polymarketapi.Position, error)
}

// HedgeTrackerConfig holds configuration for hedge detection.
type HedgeTrackerConfig struct {
	// Persistence
	GistID       string
	FileName     string
	SaveInterval time.Duration
	MaxSizeBytes int64

	// Hedge detection thresholds
	MinHedgeSize       float64 // Min shares on both sides to be "hedged" (e.g., 100)
	MinHedgeValue      float64 // Min USD value on both sides (e.g., 500)
	SignificantSellPct float64 // % of position to trigger alert (e.g., 0.50 = 50%)

	// Position fetch throttling
	PositionCheckInterval time.Duration // Min time between position fetches per wallet+market
	MaxPositionChecks     int           // Max position checks per minute

	// Asymmetric exit detection
	MinExitsForAsymmetric int     // Min exits to analyze pattern (e.g., 5)
	AsymmetricThreshold   float64 // Ratio threshold (e.g., 2.0 = exits winners 2x faster)

	// Resolution tracking
	ResolutionCheckInterval time.Duration // How often to check for resolutions
	MaxPendingEvents        int           // Max pending events to track
}

// DefaultHedgeTrackerConfig returns sensible defaults.
func DefaultHedgeTrackerConfig() HedgeTrackerConfig {
	return HedgeTrackerConfig{
		FileName:                "hedge_tracker.json",
		SaveInterval:            5 * time.Minute,
		MaxSizeBytes:            50 * 1024 * 1024, // 50MB
		MinHedgeSize:            100,
		MinHedgeValue:           500,
		SignificantSellPct:      0.50,
		PositionCheckInterval:   5 * time.Minute,
		MaxPositionChecks:       60,
		MinExitsForAsymmetric:   5,
		AsymmetricThreshold:     2.0,
		ResolutionCheckInterval: 1 * time.Hour,
		MaxPendingEvents:        1000,
	}
}

// HedgePosition represents a wallet's positions on both sides of a market.
type HedgePosition struct {
	ConditionID  string    `json:"condition_id"`
	MarketTitle  string    `json:"market_title"`
	YesSize      float64   `json:"yes_size"`
	NoSize       float64   `json:"no_size"`
	YesAvgPrice  float64   `json:"yes_avg_price"`
	NoAvgPrice   float64   `json:"no_avg_price"`
	LastUpdated  time.Time `json:"last_updated"`
	IsHedged     bool      `json:"is_hedged"`
}

// HedgeState tracks all hedge positions for a wallet.
type HedgeState struct {
	Wallet    string                   `json:"wallet"`
	Positions map[string]HedgePosition `json:"positions"` // conditionID -> position
	LastCheck time.Time                `json:"last_check"`
}

// HedgeRemovalEvent records when a hedge is removed for later resolution verification.
type HedgeRemovalEvent struct {
	ID          string `json:"id"` // wallet:conditionID:timestamp
	Wallet      string `json:"wallet"`
	ConditionID string `json:"condition_id"`
	MarketTitle string `json:"market_title"`
	MarketSlug  string `json:"market_slug"`

	// Positions before removal
	YesSizeBefore  float64 `json:"yes_size_before"`
	NoSizeBefore   float64 `json:"no_size_before"`
	YesPriceBefore float64 `json:"yes_price_before"`
	NoPriceBefore  float64 `json:"no_price_before"`

	// The removal trade
	SoldSide  string  `json:"sold_side"` // "Yes" or "No"
	SoldSize  float64 `json:"sold_size"`
	SoldPrice float64 `json:"sold_price"`

	// Positions after removal
	YesSizeAfter float64 `json:"yes_size_after"`
	NoSizeAfter  float64 `json:"no_size_after"`

	// Timestamps
	RemovedAt time.Time `json:"removed_at"`
	AlertedAt time.Time `json:"alerted_at"`

	// Resolution tracking
	Resolved        bool      `json:"resolved"`
	ResolvedAt      time.Time `json:"resolved_at,omitempty"`
	WinningOutcome  string    `json:"winning_outcome,omitempty"`
	RemovedLoser    bool      `json:"removed_loser,omitempty"`
	FollowUpAlerted bool      `json:"followup_alerted"`
}

// ExitRecord tracks a single exit for asymmetric analysis.
type ExitRecord struct {
	ConditionID   string    `json:"condition_id"`
	Outcome       string    `json:"outcome"`
	ExitPrice     float64   `json:"exit_price"`
	AvgEntryPrice float64   `json:"avg_entry_price"`
	Size          float64   `json:"size"`
	RealizedPnl   float64   `json:"realized_pnl"`
	IsWinner      bool      `json:"is_winner"`
	HoldDuration  int64     `json:"hold_duration_s"` // seconds
	ExitedAt      time.Time `json:"exited_at"`
}

// AsymmetricExitStats tracks exit timing patterns per wallet.
type AsymmetricExitStats struct {
	Wallet              string       `json:"wallet"`
	WinningExits        int          `json:"winning_exits"`
	LosingExits         int          `json:"losing_exits"`
	AvgWinHoldDuration  float64      `json:"avg_win_hold_s"`
	AvgLossHoldDuration float64      `json:"avg_loss_hold_s"`
	TotalWinHoldTime    float64      `json:"total_win_hold_s"`
	TotalLossHoldTime   float64      `json:"total_loss_hold_s"`
	RecentExits         []ExitRecord `json:"recent_exits,omitempty"`
	LastUpdated         time.Time    `json:"last_updated"`
}

// HedgeAlert contains data for hedge-related notifications.
type HedgeAlert struct {
	Wallet      string
	WalletURL   string
	MarketTitle string
	MarketURL   string
	Timestamp   time.Time
	Reason      string // "hedge_removal", "asymmetric_exit", "hedge_followup"

	// Hedge removal fields
	YesSizeBefore float64
	NoSizeBefore  float64
	SoldSide      string
	SoldSize      float64
	SoldPrice     float64
	YesSizeAfter  float64
	NoSizeAfter   float64
	ReductionPct  float64 // How much of the sold side was reduced

	// Follow-up alert fields
	WinningOutcome string
	RemovedLoser   bool

	// Asymmetric exit fields
	AvgWinHoldTime  time.Duration
	AvgLossHoldTime time.Duration
	WinExitCount    int
	LossExitCount   int
	AsymmetricRatio float64
}

// hedgeUpdate represents an async state update.
type hedgeUpdate struct {
	updateType string // "hedge_state", "removal_event", "exit_record", "resolution"
	wallet     string
	data       interface{}
}

// HedgeTrackerSnapshot is the persisted state format.
type HedgeTrackerSnapshot struct {
	Version       int                             `json:"version"`
	Timestamp     time.Time                       `json:"timestamp"`
	HedgeStates   map[string]*HedgeState          `json:"hedge_states"`   // wallet -> state
	PendingEvents map[string]*HedgeRemovalEvent   `json:"pending_events"` // eventID -> event
	ExitStats     map[string]*AsymmetricExitStats `json:"exit_stats"`     // wallet -> stats
}

// HedgeTracker detects hedge removal patterns and asymmetric exits.
type HedgeTracker struct {
	logger     *zap.Logger
	apiClient  HedgeAPIClient
	gistClient gist.Storage

	// Config with mutex for hot-reload support
	configMu sync.RWMutex
	config   HedgeTrackerConfig

	mu sync.RWMutex

	// Wallet hedge states
	hedgeStates map[string]*HedgeState // wallet -> state

	// Pending hedge removal events awaiting resolution
	pendingEvents map[string]*HedgeRemovalEvent // eventID -> event

	// Asymmetric exit stats
	exitStats map[string]*AsymmetricExitStats // wallet -> stats

	// Rate limiting for position checks
	lastPositionCheck  map[string]time.Time // wallet:conditionID -> last check time
	positionCheckCount int
	positionCheckReset time.Time

	// Persistence
	dirty    bool
	updateCh chan hedgeUpdate
	doneCh   chan struct{}
}

// NewHedgeTracker creates a new hedge tracker.
func NewHedgeTracker(
	logger *zap.Logger,
	apiClient HedgeAPIClient,
	gistClient gist.Storage,
	config HedgeTrackerConfig,
) *HedgeTracker {
	if logger == nil {
		logger = zap.NewNop()
	}

	return &HedgeTracker{
		logger:            logger.Named("hedge-tracker"),
		apiClient:         apiClient,
		gistClient:        gistClient,
		config:            config,
		hedgeStates:       make(map[string]*HedgeState),
		pendingEvents:     make(map[string]*HedgeRemovalEvent),
		exitStats:         make(map[string]*AsymmetricExitStats),
		lastPositionCheck: make(map[string]time.Time),
		updateCh:          make(chan hedgeUpdate, 100),
		doneCh:            make(chan struct{}),
	}
}

// IsEnabled returns true if hedge tracking is configured.
func (ht *HedgeTracker) IsEnabled() bool {
	cfg := ht.getConfig()
	return ht.gistClient != nil && cfg.GistID != ""
}

// getConfig returns the current config in a thread-safe manner.
func (ht *HedgeTracker) getConfig() HedgeTrackerConfig {
	ht.configMu.RLock()
	defer ht.configMu.RUnlock()
	return ht.config
}

// UpdateConfig updates the hedge tracker config.
func (ht *HedgeTracker) UpdateConfig(cfg HedgeTrackerConfig) {
	ht.configMu.Lock()
	defer ht.configMu.Unlock()
	ht.config = cfg
	ht.logger.Info("hedge tracker config updated",
		zap.Float64("minHedgeSize", cfg.MinHedgeSize),
		zap.Float64("minHedgeValue", cfg.MinHedgeValue),
	)
}

// Start begins async update processing and resolution checking.
func (ht *HedgeTracker) Start(ctx context.Context) {
	go ht.processUpdates(ctx)
	go ht.periodicSave(ctx)
	go ht.runResolutionChecker(ctx)
}

// Stop gracefully shuts down, saving pending changes.
func (ht *HedgeTracker) Stop() {
	close(ht.doneCh)
}

// processUpdates handles async state updates.
func (ht *HedgeTracker) processUpdates(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-ht.doneCh:
			return
		case update := <-ht.updateCh:
			ht.applyUpdate(update)
		}
	}
}

// applyUpdate applies a state update.
func (ht *HedgeTracker) applyUpdate(update hedgeUpdate) {
	ht.mu.Lock()
	defer ht.mu.Unlock()

	switch update.updateType {
	case "hedge_state":
		if state, ok := update.data.(*HedgeState); ok {
			ht.hedgeStates[update.wallet] = state
			ht.dirty = true
		}
	case "removal_event":
		if event, ok := update.data.(*HedgeRemovalEvent); ok {
			ht.pendingEvents[event.ID] = event
			ht.dirty = true
		}
	case "exit_record":
		if record, ok := update.data.(*ExitRecord); ok {
			ht.recordExitInternal(update.wallet, record)
			ht.dirty = true
		}
	case "resolution":
		if event, ok := update.data.(*HedgeRemovalEvent); ok {
			ht.pendingEvents[event.ID] = event
			ht.dirty = true
		}
	}
}

// recordExitInternal updates exit stats for a wallet (must hold lock).
func (ht *HedgeTracker) recordExitInternal(wallet string, record *ExitRecord) {
	stats := ht.exitStats[wallet]
	if stats == nil {
		stats = &AsymmetricExitStats{
			Wallet:      wallet,
			RecentExits: make([]ExitRecord, 0),
		}
		ht.exitStats[wallet] = stats
	}

	// Update aggregated stats
	if record.IsWinner {
		stats.WinningExits++
		stats.TotalWinHoldTime += float64(record.HoldDuration)
		if stats.WinningExits > 0 {
			stats.AvgWinHoldDuration = stats.TotalWinHoldTime / float64(stats.WinningExits)
		}
	} else {
		stats.LosingExits++
		stats.TotalLossHoldTime += float64(record.HoldDuration)
		if stats.LosingExits > 0 {
			stats.AvgLossHoldDuration = stats.TotalLossHoldTime / float64(stats.LosingExits)
		}
	}

	// Keep recent exits (max 20)
	stats.RecentExits = append(stats.RecentExits, *record)
	if len(stats.RecentExits) > 20 {
		stats.RecentExits = stats.RecentExits[len(stats.RecentExits)-20:]
	}

	stats.LastUpdated = time.Now()
}

// periodicSave saves state periodically.
func (ht *HedgeTracker) periodicSave(ctx context.Context) {
	cfg := ht.getConfig()
	ticker := time.NewTicker(cfg.SaveInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Final save on shutdown
			saveCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			_ = ht.Save(saveCtx)
			cancel()
			return
		case <-ht.doneCh:
			saveCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			_ = ht.Save(saveCtx)
			cancel()
			return
		case <-ticker.C:
			if err := ht.Save(ctx); err != nil {
				ht.logger.Warn("failed to save hedge tracker state", zap.Error(err))
			}
		}
	}
}

// runResolutionChecker periodically checks for market resolutions.
func (ht *HedgeTracker) runResolutionChecker(ctx context.Context) {
	cfg := ht.getConfig()
	ticker := time.NewTicker(cfg.ResolutionCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ht.doneCh:
			return
		case <-ticker.C:
			ht.checkResolutions(ctx)
		}
	}
}

// checkResolutions checks pending events for market resolutions.
func (ht *HedgeTracker) checkResolutions(ctx context.Context) {
	ht.mu.RLock()
	pendingCopy := make([]*HedgeRemovalEvent, 0)
	for _, event := range ht.pendingEvents {
		if !event.Resolved {
			pendingCopy = append(pendingCopy, event)
		}
	}
	ht.mu.RUnlock()

	for _, event := range pendingCopy {
		resolved, winningOutcome := ht.checkMarketResolution(ctx, event.Wallet, event.ConditionID)
		if resolved && winningOutcome != "" {
			ht.processResolution(event, winningOutcome)
		}
	}
}

// checkMarketResolution checks if a market has resolved and returns the winning outcome.
func (ht *HedgeTracker) checkMarketResolution(ctx context.Context, wallet, conditionID string) (bool, string) {
	// Try to get positions - if Redeemable is true, market is resolved
	positions, err := ht.apiClient.GetPositions(ctx, wallet, conditionID, 10)
	if err != nil {
		return false, ""
	}

	for _, p := range positions {
		if p.Redeemable {
			// Market is resolved - determine winning outcome by which has value
			// Winner will have CurPrice near 1.0
			if p.CurPrice > 0.5 {
				return true, p.Outcome
			}
		}
	}

	return false, ""
}

// processResolution handles a resolved market.
func (ht *HedgeTracker) processResolution(event *HedgeRemovalEvent, winningOutcome string) {
	ht.mu.Lock()
	defer ht.mu.Unlock()

	event.Resolved = true
	event.ResolvedAt = time.Now()
	event.WinningOutcome = winningOutcome

	// Check if they removed the losing side (suspicious!)
	event.RemovedLoser = !strings.EqualFold(event.SoldSide, winningOutcome)

	ht.dirty = true

	if event.RemovedLoser {
		ht.logger.Info("hedge removal confirmed - removed losing side",
			zap.String("wallet", shortID(event.Wallet)),
			zap.String("market", event.MarketTitle),
			zap.String("soldSide", event.SoldSide),
			zap.String("winner", winningOutcome),
		)
	}
}

// ShouldCheckPositions returns true if we should fetch positions for this trade.
func (ht *HedgeTracker) ShouldCheckPositions(wallet, conditionID string) bool {
	cfg := ht.getConfig()
	ht.mu.Lock()
	defer ht.mu.Unlock()

	key := wallet + ":" + conditionID
	now := time.Now()

	// Reset rate limit counter every minute
	if now.Sub(ht.positionCheckReset) > time.Minute {
		ht.positionCheckCount = 0
		ht.positionCheckReset = now
	}

	// Check global rate limit
	if ht.positionCheckCount >= cfg.MaxPositionChecks {
		return false
	}

	// Check per-wallet+market cooldown
	if lastCheck, ok := ht.lastPositionCheck[key]; ok {
		if now.Sub(lastCheck) < cfg.PositionCheckInterval {
			return false
		}
	}

	// Update tracking
	ht.lastPositionCheck[key] = now
	ht.positionCheckCount++

	return true
}

// ProcessTrade processes a trade and returns any hedge alerts.
func (ht *HedgeTracker) ProcessTrade(
	ctx context.Context,
	wallet string,
	conditionID string,
	marketTitle string,
	marketSlug string,
	side string,
	outcome string,
	size float64,
	price float64,
) []HedgeAlert {
	cfg := ht.getConfig()
	var alerts []HedgeAlert

	// Only analyze sells for hedge removal
	if !strings.EqualFold(side, "SELL") {
		return alerts
	}

	// Need API client to fetch positions
	if ht.apiClient == nil {
		return alerts
	}

	// Fetch current positions
	positions, err := ht.apiClient.GetPositions(ctx, wallet, conditionID, 10)
	if err != nil {
		ht.logger.Debug("failed to fetch positions for hedge check",
			zap.String("wallet", shortID(wallet)),
			zap.Error(err),
		)
		return alerts
	}

	// Parse positions into Yes/No
	var yesPos, noPos *polymarketapi.Position
	for i := range positions {
		p := &positions[i]
		if strings.EqualFold(p.Outcome, "Yes") {
			yesPos = p
		} else if strings.EqualFold(p.Outcome, "No") {
			noPos = p
		}
	}

	// Get previous hedge state
	ht.mu.RLock()
	prevState := ht.hedgeStates[wallet]
	var prevHedge *HedgePosition
	if prevState != nil {
		if pos, ok := prevState.Positions[conditionID]; ok {
			prevHedge = &pos
		}
	}
	ht.mu.RUnlock()

	// Calculate current state
	yesSize := 0.0
	noSize := 0.0
	yesPrice := 0.0
	noPrice := 0.0
	if yesPos != nil {
		yesSize = yesPos.Size
		yesPrice = yesPos.AvgPrice
	}
	if noPos != nil {
		noSize = noPos.Size
		noPrice = noPos.AvgPrice
	}

	// Determine if currently hedged
	yesValue := yesSize * yesPrice
	noValue := noSize * noPrice
	isHedged := yesSize >= cfg.MinHedgeSize && noSize >= cfg.MinHedgeSize &&
		yesValue >= cfg.MinHedgeValue && noValue >= cfg.MinHedgeValue

	// Check for hedge removal
	if prevHedge != nil && prevHedge.IsHedged {
		// Was hedged before - check if hedge was removed
		var soldSideSize, prevSoldSize float64
		if strings.EqualFold(outcome, "Yes") {
			soldSideSize = yesSize
			prevSoldSize = prevHedge.YesSize
		} else {
			soldSideSize = noSize
			prevSoldSize = prevHedge.NoSize
		}

		// Calculate reduction
		if prevSoldSize > 0 {
			reduction := (prevSoldSize - soldSideSize) / prevSoldSize
			if reduction >= cfg.SignificantSellPct {
				// Hedge removal detected!
				event := &HedgeRemovalEvent{
					ID:             fmt.Sprintf("%s:%s:%d", wallet, conditionID, time.Now().Unix()),
					Wallet:         wallet,
					ConditionID:    conditionID,
					MarketTitle:    marketTitle,
					MarketSlug:     marketSlug,
					YesSizeBefore:  prevHedge.YesSize,
					NoSizeBefore:   prevHedge.NoSize,
					YesPriceBefore: prevHedge.YesAvgPrice,
					NoPriceBefore:  prevHedge.NoAvgPrice,
					SoldSide:       outcome,
					SoldSize:       size,
					SoldPrice:      price,
					YesSizeAfter:   yesSize,
					NoSizeAfter:    noSize,
					RemovedAt:      time.Now(),
					AlertedAt:      time.Now(),
				}

				// Store event for resolution tracking
				ht.mu.Lock()
				ht.pendingEvents[event.ID] = event
				ht.dirty = true
				ht.mu.Unlock()

				// Create alert
				alert := HedgeAlert{
					Wallet:        wallet,
					WalletURL:     fmt.Sprintf("https://polymarket.com/profile/%s", wallet),
					MarketTitle:   marketTitle,
					MarketURL:     fmt.Sprintf("https://polymarket.com/event/%s", marketSlug),
					Timestamp:     time.Now(),
					Reason:        "hedge_removal",
					YesSizeBefore: prevHedge.YesSize,
					NoSizeBefore:  prevHedge.NoSize,
					SoldSide:      outcome,
					SoldSize:      size,
					SoldPrice:     price,
					YesSizeAfter:  yesSize,
					NoSizeAfter:   noSize,
					ReductionPct:  reduction,
				}
				alerts = append(alerts, alert)

				ht.logger.Info("hedge removal detected",
					zap.String("wallet", shortID(wallet)),
					zap.String("market", marketTitle),
					zap.String("soldSide", outcome),
					zap.Float64("reduction", reduction*100),
				)
			}
		}
	}

	// Update hedge state
	newPos := HedgePosition{
		ConditionID:  conditionID,
		MarketTitle:  marketTitle,
		YesSize:      yesSize,
		NoSize:       noSize,
		YesAvgPrice:  yesPrice,
		NoAvgPrice:   noPrice,
		LastUpdated:  time.Now(),
		IsHedged:     isHedged,
	}

	ht.mu.Lock()
	if ht.hedgeStates[wallet] == nil {
		ht.hedgeStates[wallet] = &HedgeState{
			Wallet:    wallet,
			Positions: make(map[string]HedgePosition),
		}
	}
	ht.hedgeStates[wallet].Positions[conditionID] = newPos
	ht.hedgeStates[wallet].LastCheck = time.Now()
	ht.dirty = true
	ht.mu.Unlock()

	// Check for asymmetric exit pattern
	if ht.ShouldAlertAsymmetric(wallet) {
		stats := ht.GetAsymmetricStats(wallet)
		if stats != nil {
			ratio := stats.AvgLossHoldDuration / stats.AvgWinHoldDuration
			alert := HedgeAlert{
				Wallet:          wallet,
				WalletURL:       fmt.Sprintf("https://polymarket.com/profile/%s", wallet),
				Timestamp:       time.Now(),
				Reason:          "asymmetric_exit",
				AvgWinHoldTime:  time.Duration(stats.AvgWinHoldDuration) * time.Second,
				AvgLossHoldTime: time.Duration(stats.AvgLossHoldDuration) * time.Second,
				WinExitCount:    stats.WinningExits,
				LossExitCount:   stats.LosingExits,
				AsymmetricRatio: ratio,
			}
			alerts = append(alerts, alert)
		}
	}

	return alerts
}

// RecordExit records an exit for asymmetric analysis.
func (ht *HedgeTracker) RecordExit(wallet string, record ExitRecord) {
	select {
	case ht.updateCh <- hedgeUpdate{
		updateType: "exit_record",
		wallet:     wallet,
		data:       &record,
	}:
	default:
		ht.logger.Debug("hedge update channel full, dropping exit record")
	}
}

// GetAsymmetricStats returns exit stats for a wallet.
func (ht *HedgeTracker) GetAsymmetricStats(wallet string) *AsymmetricExitStats {
	ht.mu.RLock()
	defer ht.mu.RUnlock()

	if stats, ok := ht.exitStats[wallet]; ok {
		// Return a copy
		statsCopy := *stats
		return &statsCopy
	}
	return nil
}

// ShouldAlertAsymmetric returns true if wallet shows asymmetric exit pattern.
func (ht *HedgeTracker) ShouldAlertAsymmetric(wallet string) bool {
	cfg := ht.getConfig()
	ht.mu.RLock()
	defer ht.mu.RUnlock()

	stats, ok := ht.exitStats[wallet]
	if !ok {
		return false
	}

	// Need minimum exits on both sides
	totalExits := stats.WinningExits + stats.LosingExits
	if totalExits < cfg.MinExitsForAsymmetric {
		return false
	}
	if stats.WinningExits < 2 || stats.LosingExits < 2 {
		return false
	}

	// Check if asymmetric (exits losers much slower than winners)
	if stats.AvgWinHoldDuration <= 0 {
		return false
	}

	ratio := stats.AvgLossHoldDuration / stats.AvgWinHoldDuration
	return ratio >= cfg.AsymmetricThreshold
}

// GetPendingFollowUpAlerts returns events that need follow-up alerts.
func (ht *HedgeTracker) GetPendingFollowUpAlerts() []HedgeAlert {
	ht.mu.Lock()
	defer ht.mu.Unlock()

	var alerts []HedgeAlert
	for _, event := range ht.pendingEvents {
		if event.Resolved && event.RemovedLoser && !event.FollowUpAlerted {
			alert := HedgeAlert{
				Wallet:         event.Wallet,
				WalletURL:      fmt.Sprintf("https://polymarket.com/profile/%s", event.Wallet),
				MarketTitle:    event.MarketTitle,
				MarketURL:      fmt.Sprintf("https://polymarket.com/event/%s", event.MarketSlug),
				Timestamp:      time.Now(),
				Reason:         "hedge_followup",
				YesSizeBefore:  event.YesSizeBefore,
				NoSizeBefore:   event.NoSizeBefore,
				SoldSide:       event.SoldSide,
				SoldSize:       event.SoldSize,
				SoldPrice:      event.SoldPrice,
				WinningOutcome: event.WinningOutcome,
				RemovedLoser:   true,
			}
			alerts = append(alerts, alert)
			event.FollowUpAlerted = true
			ht.dirty = true
		}
	}
	return alerts
}

// PendingEventCount returns the number of pending events.
func (ht *HedgeTracker) PendingEventCount() int {
	ht.mu.RLock()
	defer ht.mu.RUnlock()
	return len(ht.pendingEvents)
}

// Load loads state from gist.
func (ht *HedgeTracker) Load(ctx context.Context) error {
	if !ht.IsEnabled() {
		return nil
	}

	cfg := ht.getConfig()
	content, err := ht.gistClient.Load(ctx, cfg.FileName, cfg.GistID)
	if err != nil {
		return fmt.Errorf("load hedge tracker: %w", err)
	}

	if content == "" {
		return nil
	}

	var snapshot HedgeTrackerSnapshot
	if err := json.Unmarshal([]byte(content), &snapshot); err != nil {
		return fmt.Errorf("unmarshal hedge tracker: %w", err)
	}

	ht.mu.Lock()
	defer ht.mu.Unlock()

	if snapshot.HedgeStates != nil {
		ht.hedgeStates = snapshot.HedgeStates
	}
	if snapshot.PendingEvents != nil {
		ht.pendingEvents = snapshot.PendingEvents
	}
	if snapshot.ExitStats != nil {
		ht.exitStats = snapshot.ExitStats
	}

	ht.logger.Info("loaded hedge tracker state",
		zap.Int("hedgeStates", len(ht.hedgeStates)),
		zap.Int("pendingEvents", len(ht.pendingEvents)),
		zap.Int("exitStats", len(ht.exitStats)),
	)

	return nil
}

// Save saves state to gist.
func (ht *HedgeTracker) Save(ctx context.Context) error {
	if !ht.IsEnabled() {
		return nil
	}

	ht.mu.Lock()
	if !ht.dirty {
		ht.mu.Unlock()
		return nil
	}

	snapshot := HedgeTrackerSnapshot{
		Version:       1,
		Timestamp:     time.Now(),
		HedgeStates:   ht.hedgeStates,
		PendingEvents: ht.pendingEvents,
		ExitStats:     ht.exitStats,
	}

	ht.dirty = false
	ht.mu.Unlock()

	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		ht.mu.Lock()
		ht.dirty = true
		ht.mu.Unlock()
		return fmt.Errorf("marshal hedge tracker: %w", err)
	}

	cfg := ht.getConfig()
	if err := ht.gistClient.Save(ctx, cfg.FileName, string(data), cfg.GistID); err != nil {
		ht.mu.Lock()
		ht.dirty = true
		ht.mu.Unlock()
		return fmt.Errorf("save hedge tracker: %w", err)
	}

	ht.logger.Debug("saved hedge tracker state",
		zap.Int("hedgeStates", len(snapshot.HedgeStates)),
		zap.Int("pendingEvents", len(snapshot.PendingEvents)),
	)

	return nil
}

// Stats returns current tracking statistics.
func (ht *HedgeTracker) Stats() (hedgedWallets, pendingEvents, trackedExits int) {
	ht.mu.RLock()
	defer ht.mu.RUnlock()

	hedgedCount := 0
	for _, state := range ht.hedgeStates {
		for _, pos := range state.Positions {
			if pos.IsHedged {
				hedgedCount++
			}
		}
	}

	return hedgedCount, len(ht.pendingEvents), len(ht.exitStats)
}
