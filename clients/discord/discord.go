package discord

import (
	"fmt"
	"polybot/clients/notifier"
	"polybot/config"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"go.uber.org/zap"
)

// DiscordClient sends alerts to Discord.
// Implements notifier.Notifier interface.
type DiscordClient struct {
	logger    *zap.Logger
	session   *discordgo.Session
	channelID string
	isProd    bool
}

func NewDiscordClient(logger *zap.Logger, cfg *config.Config) *DiscordClient {
	if logger == nil {
		logger = zap.NewNop()
	}

	channelID := cfg.Discord.BetaChannelID
	if cfg.IsProd {
		channelID = cfg.Discord.ProdChannelID
	}

	token := cfg.Discord.BotToken
	if token == "" {
		logger.Warn("DISCORD_BOT_TOKEN not set, Discord alerts disabled")
		return &DiscordClient{
			logger:    logger,
			channelID: channelID,
			isProd:    cfg.IsProd,
		}
	}

	session, err := discordgo.New("Bot " + token)
	if err != nil {
		logger.Error("failed to create discord session", zap.Error(err))
		return &DiscordClient{
			logger:    logger,
			channelID: channelID,
			isProd:    cfg.IsProd,
		}
	}

	logger.Info("discord bot initialized",
		zap.Bool("isProd", cfg.IsProd),
		zap.String("channelID", channelID),
	)

	return &DiscordClient{
		logger:    logger,
		session:   session,
		channelID: channelID,
		isProd:    cfg.IsProd,
	}
}

// SendMessage sends a plain text message (kept for backwards compatibility).
func (dc *DiscordClient) SendMessage(message string) {
	if dc.session == nil {
		dc.logger.Warn("discord session not initialized, skipping message")
		return
	}

	_, err := dc.session.ChannelMessageSend(dc.channelID, message)
	if err != nil {
		dc.logger.Error("failed to send discord message", zap.Error(err))
		return
	}

	dc.logger.Info("sent discord message")
}

// SendTradeAlert sends a rich embedded trade alert.
// Implements notifier.Notifier interface.
func (dc *DiscordClient) SendTradeAlert(alert notifier.TradeAlert) {
	if dc.session == nil {
		dc.logger.Warn("discord session not initialized, skipping alert")
		return
	}

	embed := dc.buildTradeEmbed(alert)

	_, err := dc.session.ChannelMessageSendEmbed(dc.channelID, embed)
	if err != nil {
		dc.logger.Error("failed to send discord embed", zap.Error(err))
		return
	}

	dc.logger.Info("sent discord trade alert",
		zap.String("trader", alert.TraderName),
		zap.String("market", alert.MarketTitle),
	)
}

func (dc *DiscordClient) buildTradeEmbed(alert notifier.TradeAlert) *discordgo.MessageEmbed {
	// Choose color based on side
	color := 0x2ECC71 // Green for BUY
	sideEmoji := "🟢"
	if strings.ToUpper(alert.Side) == "SELL" {
		color = 0xE74C3C // Red for SELL
		sideEmoji = "🔴"
	}

	// Build title based on alert reasons
	title := dc.buildAlertTitle(alert.Reasons)

	// Format trader display with link
	traderDisplay := alert.TraderName
	if alert.TraderAddress != "" {
		shortAddr := shortAddress(alert.TraderAddress)
		if traderDisplay != shortAddr {
			traderDisplay = fmt.Sprintf("%s (%s)", alert.TraderName, shortAddr)
		}
	}
	// Make trader name a clickable link to wallet
	if alert.WalletURL != "" {
		traderDisplay = fmt.Sprintf("[%s](%s)", traderDisplay, alert.WalletURL)
	}

	// Format trade info
	tradeInfo := fmt.Sprintf("%.2f shares @ $%.3f", alert.Shares, alert.Price)

	// Format win rate
	winRateStr := "N/A"
	if alert.WinCount+alert.LossCount > 0 {
		winRateStr = fmt.Sprintf("%.1f%% (%d-%d)", alert.WinRate*100, alert.WinCount, alert.LossCount)
	}

	// Format inventory info
	inventoryStr := "N/A"
	if alert.HasInventory {
		if alert.InventoryShares > 0 {
			// Show current position after this trade (estimated due to API lag)
			inventoryStr = fmt.Sprintf("~%.2f shares @ $%.3f avg (est.)\nValue: ~$%.2f",
				alert.InventoryShares, alert.InventoryAvgPrice, alert.InventoryValue)
		} else if alert.HasClosedInfo {
			// Position was fully closed - show cost basis and P&L
			pnlSign := "+"
			if alert.ClosedRealizedPnl < 0 {
				pnlSign = ""
			}
			inventoryStr = fmt.Sprintf("Closed position\nCost basis: $%.3f\nRealized P&L: %s$%.2f",
				alert.ClosedCostBasis, pnlSign, alert.ClosedRealizedPnl)
		} else {
			// Position was fully closed but no closed info available
			inventoryStr = "0 shares (closed)"
		}
	}

	// Build fields in table-like format
	fields := []*discordgo.MessageEmbedField{
		{
			Name:   "Trader",
			Value:  traderDisplay,
			Inline: true,
		},
		{
			Name:   "Side",
			Value:  fmt.Sprintf("%s %s", sideEmoji, alert.Side),
			Inline: true,
		},
		{
			Name:   "Trade",
			Value:  tradeInfo,
			Inline: true,
		},
		{
			Name:   "Notional",
			Value:  fmt.Sprintf("$%.2f", alert.Notional),
			Inline: true,
		},
		{
			Name:   "Position After (est.)",
			Value:  inventoryStr,
			Inline: true,
		},
		{
			Name:   "Win Rate (resolved)",
			Value:  winRateStr,
			Inline: true,
		},
	}

	// Build description with market info
	description := fmt.Sprintf("**%s**\nOutcome: %s", alert.MarketTitle, alert.Outcome)

	// Format timestamp for footer (PST)
	pst, _ := time.LoadLocation("America/Los_Angeles")
	ts := alert.Timestamp
	if ts.IsZero() {
		ts = time.Now()
	}
	footerText := fmt.Sprintf("polybot * %s", ts.In(pst).Format("1/2/2006, 3:04:05PM (MST)"))

	embed := &discordgo.MessageEmbed{
		Title:       title,
		URL:         alert.WalletURL, // Makes title clickable
		Description: description,
		Color:       color,
		Fields:      fields,
		Footer: &discordgo.MessageEmbedFooter{
			Text: footerText,
		},
		Timestamp: ts.Format(time.RFC3339),
	}

	// Add market image as the main embed image
	if alert.MarketImage != "" {
		embed.Image = &discordgo.MessageEmbedImage{
			URL: alert.MarketImage,
		}
	}

	return embed
}

func (dc *DiscordClient) buildAlertTitle(reasons []notifier.AlertReason) string {
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

	// Count active reasons for multi-flag handling
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

	// For 3+ reasons, use generic multi-alert title
	if count >= 3 {
		return "🚨 Multiple Alert Triggers"
	}

	// Build title based on combinations (two reasons)
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

func shortAddress(addr string) string {
	if len(addr) <= 14 {
		return addr
	}
	return addr[:6] + "…" + addr[len(addr)-6:]
}

// Close closes the Discord session.
func (dc *DiscordClient) Close() error {
	if dc.session != nil {
		return dc.session.Close()
	}
	return nil
}
