package telegram

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"polybot/clients/notifier"
	"polybot/config"
	"strings"
	"time"

	"go.uber.org/zap"
)

const telegramAPIURL = "https://api.telegram.org/bot%s/%s"

// TelegramClient sends alerts to Telegram.
// Implements notifier.Notifier interface.
type TelegramClient struct {
	logger   *zap.Logger
	botToken string
	chatID   string
	isProd   bool
	client   *http.Client
}

func NewTelegramClient(logger *zap.Logger, cfg *config.Config) *TelegramClient {
	if logger == nil {
		logger = zap.NewNop()
	}

	chatID := cfg.Telegram.BetaChatID
	if cfg.IsProd {
		chatID = cfg.Telegram.ProdChatID
	}

	token := cfg.Telegram.BotToken
	if token == "" {
		logger.Warn("TELEGRAM_BOT_KEY not set, Telegram alerts disabled")
		return &TelegramClient{
			logger: logger,
			chatID: chatID,
			isProd: cfg.IsProd,
		}
	}

	logger.Info("telegram bot initialized",
		zap.Bool("isProd", cfg.IsProd),
		zap.String("chatID", chatID),
	)

	return &TelegramClient{
		logger:   logger,
		botToken: token,
		chatID:   chatID,
		isProd:   cfg.IsProd,
		client:   &http.Client{Timeout: 10 * time.Second},
	}
}

// SendTradeAlert sends a trade alert notification.
// Implements notifier.Notifier interface.
func (tc *TelegramClient) SendTradeAlert(alert notifier.TradeAlert) {
	if tc.botToken == "" || tc.chatID == "" {
		tc.logger.Warn("telegram not configured, skipping alert")
		return
	}

	message := tc.buildAlertMessage(alert)

	if err := tc.sendMessage(message); err != nil {
		tc.logger.Error("failed to send telegram message", zap.Error(err))
		return
	}

	tc.logger.Info("sent telegram trade alert",
		zap.String("trader", alert.TraderName),
		zap.String("market", alert.MarketTitle),
	)
}

func (tc *TelegramClient) buildAlertMessage(alert notifier.TradeAlert) string {
	var sb strings.Builder

	// Title based on reasons
	title := tc.buildAlertTitle(alert.Reasons)
	sb.WriteString(fmt.Sprintf("*%s*\n\n", escapeMarkdown(title)))

	// Market info
	if alert.MarketURL != "" {
		sb.WriteString(fmt.Sprintf("*Market:* [%s](%s)\n", escapeMarkdown(alert.MarketTitle), alert.MarketURL))
	} else {
		sb.WriteString(fmt.Sprintf("*Market:* %s\n", escapeMarkdown(alert.MarketTitle)))
	}
	sb.WriteString(fmt.Sprintf("*Outcome:* %s\n\n", escapeMarkdown(alert.Outcome)))

	// Trader info
	traderDisplay := alert.TraderName
	if alert.TraderAddress != "" {
		shortAddr := shortAddress(alert.TraderAddress)
		if traderDisplay != shortAddr {
			traderDisplay = fmt.Sprintf("%s (%s)", alert.TraderName, shortAddr)
		}
	}
	if alert.WalletURL != "" {
		sb.WriteString(fmt.Sprintf("*Trader:* [%s](%s)\n", escapeMarkdown(traderDisplay), alert.WalletURL))
	} else {
		sb.WriteString(fmt.Sprintf("*Trader:* %s\n", escapeMarkdown(traderDisplay)))
	}

	// Trade details
	sideEmoji := "🟢"
	if strings.ToUpper(alert.Side) == "SELL" {
		sideEmoji = "🔴"
	}
	sb.WriteString(fmt.Sprintf("*Side:* %s %s\n", sideEmoji, alert.Side))
	sb.WriteString(fmt.Sprintf("*Trade:* %.2f shares @ $%.3f\n", alert.Shares, alert.Price))
	sb.WriteString(fmt.Sprintf("*Notional:* $%.2f\n\n", alert.Notional))

	// Position info
	if alert.HasInventory {
		if alert.InventoryShares > 0 {
			sb.WriteString(fmt.Sprintf("*Position After (est.):* ~%.2f shares @ $%.3f avg (~$%.2f)\n",
				alert.InventoryShares, alert.InventoryAvgPrice, alert.InventoryValue))
		} else if alert.HasClosedInfo {
			pnlSign := "+"
			if alert.ClosedRealizedPnl < 0 {
				pnlSign = ""
			}
			sb.WriteString(fmt.Sprintf("*Position After:* Closed (cost basis: $%.3f, P&L: %s$%.2f)\n",
				alert.ClosedCostBasis, pnlSign, alert.ClosedRealizedPnl))
		} else {
			sb.WriteString("*Position After:* 0 shares (closed)\n")
		}
	}

	// Wallet stats
	winRateStr := "N/A"
	if alert.WinCount+alert.LossCount > 0 {
		winRateStr = fmt.Sprintf("%.1f%% (%d-%d)", alert.WinRate*100, alert.WinCount, alert.LossCount)
	}
	sb.WriteString(fmt.Sprintf("*Win Rate:* %s\n", winRateStr))

	// Timestamp
	pst, _ := time.LoadLocation("America/Los_Angeles")
	ts := alert.Timestamp
	if ts.IsZero() {
		ts = time.Now()
	}
	sb.WriteString(fmt.Sprintf("\n_polybot • %s_", ts.In(pst).Format("1/2/2006, 3:04:05PM (MST)")))

	return sb.String()
}

func (tc *TelegramClient) buildAlertTitle(reasons []notifier.AlertReason) string {
	hasLowActivity := false
	hasHighWinRate := false
	hasExtremeBet := false
	hasRapidTrading := false
	hasNewWallet := false
	hasContrarianBet := false
	hasMassiveTrade := false
	hasContrarianWinner := false
	hasCopyTrader := false
	hasHedgeRemoval := false
	hasAsymmetricExit := false
	hasResolutionConfirmed := false
	hasConvictionDoubling := false
	hasPerfectExitTiming := false
	hasStealthAccumulation := false
	hasPreMovePositioning := false

	for _, r := range reasons {
		switch r {
		case notifier.AlertReasonLowActivity:
			hasLowActivity = true
		case notifier.AlertReasonHighWinRate:
			hasHighWinRate = true
		case notifier.AlertReasonExtremeBet:
			hasExtremeBet = true
		case notifier.AlertReasonRapidTrading:
			hasRapidTrading = true
		case notifier.AlertReasonNewWallet:
			hasNewWallet = true
		case notifier.AlertReasonContrarianBet:
			hasContrarianBet = true
		case notifier.AlertReasonMassiveTrade:
			hasMassiveTrade = true
		case notifier.AlertReasonContrarianWinner:
			hasContrarianWinner = true
		case notifier.AlertReasonCopyTrader:
			hasCopyTrader = true
		case notifier.AlertReasonHedgeRemoval:
			hasHedgeRemoval = true
		case notifier.AlertReasonAsymmetricExit:
			hasAsymmetricExit = true
		case notifier.AlertReasonResolutionConfirmed:
			hasResolutionConfirmed = true
		case notifier.AlertReasonConvictionDoubling:
			hasConvictionDoubling = true
		case notifier.AlertReasonPerfectExitTiming:
			hasPerfectExitTiming = true
		case notifier.AlertReasonStealthAccumulation:
			hasStealthAccumulation = true
		case notifier.AlertReasonPreMovePositioning:
			hasPreMovePositioning = true
		}
	}

	count := 0
	if hasLowActivity {
		count++
	}
	if hasHighWinRate {
		count++
	}
	if hasExtremeBet {
		count++
	}
	if hasRapidTrading {
		count++
	}
	if hasNewWallet {
		count++
	}
	if hasContrarianBet {
		count++
	}
	if hasMassiveTrade {
		count++
	}
	if hasContrarianWinner {
		count++
	}
	if hasCopyTrader {
		count++
	}
	if hasHedgeRemoval {
		count++
	}
	if hasAsymmetricExit {
		count++
	}
	if hasResolutionConfirmed {
		count++
	}
	if hasConvictionDoubling {
		count++
	}
	if hasPerfectExitTiming {
		count++
	}
	if hasStealthAccumulation {
		count++
	}
	if hasPreMovePositioning {
		count++
	}

	if count >= 3 {
		return "🚨 Multiple Alert Triggers"
	}

	// Two reasons combinations
	if hasMassiveTrade && hasHighWinRate {
		return "🐋 Massive Trade + High Win Rate"
	}
	if hasMassiveTrade && hasLowActivity {
		return "🐋 Massive Trade + Low Activity"
	}
	if hasMassiveTrade && hasNewWallet {
		return "🐋 Massive Trade + New Wallet"
	}
	if hasContrarianBet && hasNewWallet {
		return "🔄 Contrarian + New Wallet Bet"
	}
	if hasContrarianBet && hasLowActivity {
		return "🔄 Contrarian + Low Activity Bet"
	}
	if hasContrarianBet && hasHighWinRate {
		return "🔄 Contrarian + High Win Rate Bet"
	}
	if hasNewWallet && hasExtremeBet {
		return "🆕 New Wallet + Extreme Odds Bet"
	}
	if hasNewWallet && hasLowActivity {
		return "🆕 New Wallet + Low Activity"
	}
	if hasLowActivity && hasHighWinRate {
		return "🚨 Low Activity + High Win Rate"
	}
	if hasLowActivity && hasExtremeBet {
		return "🚨 Low Activity + Extreme Odds Bet"
	}
	if hasLowActivity && hasRapidTrading {
		return "🚨 Low Activity + Rapid Trading"
	}
	if hasHighWinRate && hasExtremeBet {
		return "🎯 High Win Rate + Extreme Odds Bet"
	}
	if hasHighWinRate && hasRapidTrading {
		return "🎯 High Win Rate + Rapid Trading"
	}
	if hasExtremeBet && hasRapidTrading {
		return "⚡ Extreme Odds + Rapid Trading"
	}

	// Contrarian winner combos (high priority - proven track record)
	if hasContrarianWinner && hasContrarianBet {
		return "🏆 Proven Contrarian + Another Contrarian Bet"
	}
	if hasContrarianWinner && hasMassiveTrade {
		return "🏆 Proven Contrarian + Massive Trade"
	}

	// Copy trader combos
	if hasCopyTrader && hasContrarianWinner {
		return "🔍 Copy Trader Following Contrarian Winner"
	}
	if hasCopyTrader && hasHighWinRate {
		return "🔍 Copy Trader with High Win Rate"
	}

	// Hedge-related combos (high priority - potential insider activity)
	if hasResolutionConfirmed {
		return "⚠️ Hedge Removal Confirmed"
	}
	if hasHedgeRemoval && hasAsymmetricExit {
		return "🛡️ Hedge Removal + Asymmetric Exit Pattern"
	}
	if hasHedgeRemoval && hasHighWinRate {
		return "🛡️ Hedge Removal + High Win Rate"
	}
	if hasAsymmetricExit && hasHighWinRate {
		return "📊 Asymmetric Exit + High Win Rate"
	}

	// Advanced pattern combos (high priority - sophisticated insider signals)
	if hasConvictionDoubling && hasHighWinRate {
		return "💪 Conviction Doubling + High Win Rate"
	}
	if hasConvictionDoubling && hasContrarianWinner {
		return "💪 Conviction Doubling + Proven Contrarian"
	}
	if hasPerfectExitTiming && hasHighWinRate {
		return "⏱️ Perfect Exit Timing + High Win Rate"
	}
	if hasPerfectExitTiming && hasAsymmetricExit {
		return "⏱️ Perfect Exit Timing + Asymmetric Exit"
	}
	if hasStealthAccumulation && hasLowActivity {
		return "🥷 Stealth Accumulation + Low Activity"
	}
	if hasStealthAccumulation && hasContrarianBet {
		return "🥷 Stealth Accumulation + Contrarian Bet"
	}
	if hasStealthAccumulation && hasHighWinRate {
		return "🥷 Stealth Accumulation + High Win Rate"
	}

	// Pre-Move Positioning combos (high priority - demonstrated alpha)
	if hasPreMovePositioning && hasHighWinRate {
		return "🎯 Pre-Move Positioning + High Win Rate"
	}
	if hasPreMovePositioning && hasContrarianWinner {
		return "🎯 Pre-Move Positioning + Proven Contrarian"
	}
	if hasPreMovePositioning && hasMassiveTrade {
		return "🎯 Pre-Move Positioning + Massive Trade"
	}

	// Single reasons - Advanced patterns (highest priority)
	if hasPreMovePositioning {
		return "🎯 Pre-Move Positioning"
	}
	if hasPerfectExitTiming {
		return "⏱️ Perfect Exit Timing"
	}
	if hasConvictionDoubling {
		return "💪 Conviction Doubling"
	}
	if hasStealthAccumulation {
		return "🥷 Stealth Accumulation"
	}

	// Single reasons - Hedge patterns
	if hasHedgeRemoval {
		return "🛡️ Hedge Removal Detected"
	}
	if hasAsymmetricExit {
		return "📊 Asymmetric Exit Pattern"
	}
	if hasCopyTrader {
		return "🔍 Copy Trader Detected"
	}
	if hasContrarianWinner {
		return "🏆 Proven Contrarian Winner"
	}
	if hasMassiveTrade {
		return "🐋 Massive Trade"
	}
	if hasContrarianBet {
		return "🔄 Contrarian Large Bet"
	}
	if hasNewWallet {
		return "🆕 New Wallet Large Bet"
	}
	if hasRapidTrading {
		return "⚡ Rapid Trading Detected"
	}
	if hasExtremeBet {
		return "💰 Extreme Odds Bet"
	}
	if hasHighWinRate {
		return "🎯 High Win Rate Trader"
	}
	if hasLowActivity {
		return "🚨 Low Activity Wallet"
	}
	return "🚨 Trade Alert"
}

func (tc *TelegramClient) sendMessage(text string) error {
	url := fmt.Sprintf(telegramAPIURL, tc.botToken, "sendMessage")

	payload := map[string]interface{}{
		"chat_id":    tc.chatID,
		"text":       text,
		"parse_mode": "Markdown",
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	resp, err := tc.client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("telegram API returned status %d", resp.StatusCode)
	}

	return nil
}

// Close cleans up resources. Implements notifier.Notifier interface.
func (tc *TelegramClient) Close() error {
	return nil
}

func shortAddress(addr string) string {
	if len(addr) <= 14 {
		return addr
	}
	return addr[:6] + "…" + addr[len(addr)-6:]
}

// escapeMarkdown escapes special characters for Telegram Markdown.
func escapeMarkdown(s string) string {
	replacer := strings.NewReplacer(
		"_", "\\_",
		"*", "\\*",
		"[", "\\[",
		"]", "\\]",
		"`", "\\`",
	)
	return replacer.Replace(s)
}
