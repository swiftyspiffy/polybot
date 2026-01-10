package clients

import (
	"polybot/clients/discord"
	"polybot/clients/gist"
	"polybot/clients/notifier"
	"polybot/clients/polymarketapi"
	"polybot/clients/polymarketevents"
	"polybot/clients/telegram"
	"polybot/config"

	"go.uber.org/zap"
)

type Clients struct {
	Logger *zap.Logger

	Discord          *discord.DiscordClient
	Telegram         *telegram.TelegramClient
	Notifier         notifier.Notifier // Combined notifier for all channels
	Polymarket       *polymarketapi.PolymarketApiClient
	PolymarketEvents *polymarketevents.PolymarketEventsClient
	Gist             *gist.Client
}

func NewClients(logger *zap.Logger, cfg *config.Config) *Clients {
	discordClient := discord.NewDiscordClient(logger, cfg)
	telegramClient := telegram.NewTelegramClient(logger, cfg)

	// Create combined notifier for all channels
	multiNotifier := notifier.NewMultiNotifier(discordClient, telegramClient)

	c := &Clients{
		Logger:     logger,
		Discord:    discordClient,
		Telegram:   telegramClient,
		Notifier:   multiNotifier,
		Polymarket: polymarketapi.NewPolymarketApiClient(logger, cfg),
		Gist:       gist.NewClient(logger, cfg),
	}

	// Only create WebSocket client if configured to use it
	if cfg.TradeMonitor.UseWebSocket {
		c.PolymarketEvents = polymarketevents.NewPolymarketEventsClient(logger)
	}

	return c
}
