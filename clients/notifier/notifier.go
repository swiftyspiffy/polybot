package notifier

import (
	"time"
)

// AlertReason indicates why an alert was triggered.
type AlertReason string

const (
	AlertReasonLowActivity        AlertReason = "low_activity"
	AlertReasonHighWinRate        AlertReason = "high_win_rate"
	AlertReasonExtremeBet         AlertReason = "extreme_bet"
	AlertReasonRapidTrading       AlertReason = "rapid_trading"
	AlertReasonNewWallet          AlertReason = "new_wallet"
	AlertReasonContrarianBet      AlertReason = "contrarian_bet"
	AlertReasonMassiveTrade       AlertReason = "massive_trade"
	AlertReasonContrarianWinner   AlertReason = "contrarian_winner"   // Known contrarian winner from cache
	AlertReasonCopyTrader         AlertReason = "copy_trader"         // Wallet repeatedly copies leader trades
	AlertReasonHedgeRemoval        AlertReason = "hedge_removal"        // Hedged wallet sells one side significantly
	AlertReasonAsymmetricExit      AlertReason = "asymmetric_exit"      // Wallet exits winners faster than losers
	AlertReasonResolutionConfirmed AlertReason = "resolution_confirmed" // Hedge removal confirmed after market resolution
	AlertReasonConvictionDoubling   AlertReason = "conviction_doubling"   // Adding to losing position with confidence
	AlertReasonPerfectExitTiming    AlertReason = "perfect_exit_timing"   // Consistently exits near price peaks
	AlertReasonStealthAccumulation  AlertReason = "stealth_accumulation"  // Gradual position building to avoid detection
	AlertReasonPreMovePositioning   AlertReason = "pre_move_positioning"  // Consistently positioned before price moves
)

// TradeAlert contains all the data needed for a trade alert notification.
type TradeAlert struct {
	// Wallet info
	TraderName    string
	TraderAddress string
	WalletURL     string

	// Trade info
	Side     string // BUY or SELL
	Shares   float64
	Price    float64
	Notional float64

	// Market info
	MarketTitle string
	MarketURL   string
	MarketImage string
	ConditionID string // Market condition ID for tracking
	Outcome     string

	// Wallet stats
	UniqueMarkets int
	WinRate       float64
	WinCount      int
	LossCount     int

	// Inventory info (wallet's position in this market after this trade)
	InventoryShares   float64 // Current shares held after this trade
	InventoryAvgPrice float64 // Average price paid for position
	InventoryValue    float64 // Current value of position
	HasInventory      bool    // True if inventory data was fetched successfully

	// Closed position info (for sells that closed the position)
	ClosedCostBasis   float64 // Average price paid (cost basis) for closed position
	ClosedRealizedPnl float64 // Realized profit/loss from closing position
	HasClosedInfo     bool    // True if closed position data was fetched

	// Hedge position info (for hedge removal alerts)
	HedgeYesSizeBefore float64 // Yes position size before trade
	HedgeNoSizeBefore  float64 // No position size before trade
	HedgeYesSizeAfter  float64 // Yes position size after trade
	HedgeNoSizeAfter   float64 // No position size after trade
	HedgeSoldSide      string  // Which side was sold ("Yes" or "No")
	HedgeSoldPct       float64 // Percentage of position sold
	HasHedgeInfo       bool    // True if hedge data was calculated

	// Resolution confirmation info (for follow-up alerts)
	ResolutionWinner     string // Winning outcome ("Yes" or "No")
	ResolutionRemovedLoser bool   // True if they removed the losing side
	HasResolutionInfo    bool   // True if resolution data is present

	// Asymmetric exit info
	AsymmetricWinExits       int     // Number of winning exits
	AsymmetricLossExits      int     // Number of losing exits
	AsymmetricWinAvgHoldSec  float64 // Avg hold time for winners (seconds)
	AsymmetricLossAvgHoldSec float64 // Avg hold time for losers (seconds)
	AsymmetricRatio          float64 // Ratio of loss hold time to win hold time
	HasAsymmetricInfo        bool    // True if asymmetric data is present

	// Conviction doubling info
	ConvictionExistingSize float64 // Size of existing position before adding
	ConvictionExistingAvg  float64 // Avg entry price of existing position
	ConvictionCurrentPrice float64 // Current market price (underwater)
	ConvictionLossPct      float64 // How much underwater (0.10 = 10% loss)
	ConvictionAddedSize    float64 // Size of new position added
	ConvictionAddedValue   float64 // USD value of position added
	HasConvictionInfo      bool    // True if conviction data is present

	// Perfect exit timing info
	PerfectExitScore       float64 // Average timing score (0-1)
	PerfectExitCount       int     // Number of verified exits
	PerfectExitPerfectCount int    // Number of exits with score >= 0.95
	HasPerfectExitInfo     bool    // True if exit timing data is present

	// Stealth accumulation info
	StealthTradeCount int     // Number of trades in accumulation
	StealthTotalSize  float64 // Total shares accumulated
	StealthTotalValue float64 // Total USD value accumulated
	StealthAvgPrice   float64 // Average price paid
	StealthSpreadMins int     // Time spread in minutes
	HasStealthInfo    bool    // True if stealth data is present

	// Pre-move positioning info
	PreMoveTotalTrades     int     // Total trades tracked
	PreMoveSuccessfulMoves int     // Favorable moves >= threshold
	PreMoveAlphaScore      float64 // Success rate (0-1)
	PreMoveAvgMoveSize     float64 // Average favorable move size
	HasPreMoveInfo         bool    // True if pre-move data is present

	// Alert metadata
	Reasons   []AlertReason
	Timestamp time.Time
}

// Notifier is the interface for sending trade alerts to various channels.
type Notifier interface {
	// SendTradeAlert sends a trade alert notification.
	SendTradeAlert(alert TradeAlert)

	// Close cleans up any resources.
	Close() error
}

// MultiNotifier broadcasts alerts to multiple notifiers.
type MultiNotifier struct {
	notifiers []Notifier
}

// NewMultiNotifier creates a new MultiNotifier with the given notifiers.
func NewMultiNotifier(notifiers ...Notifier) *MultiNotifier {
	// Filter out nil notifiers
	var active []Notifier
	for _, n := range notifiers {
		if n != nil {
			active = append(active, n)
		}
	}
	return &MultiNotifier{notifiers: active}
}

// SendTradeAlert sends the alert to all registered notifiers.
func (m *MultiNotifier) SendTradeAlert(alert TradeAlert) {
	for _, n := range m.notifiers {
		n.SendTradeAlert(alert)
	}
}

// Close closes all registered notifiers.
func (m *MultiNotifier) Close() error {
	var lastErr error
	for _, n := range m.notifiers {
		if err := n.Close(); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

// Count returns the number of active notifiers.
func (m *MultiNotifier) Count() int {
	return len(m.notifiers)
}
