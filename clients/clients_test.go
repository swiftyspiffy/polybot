package clients

import (
	"polybot/config"
	"testing"

	"go.uber.org/zap"
)

func TestNewClients(t *testing.T) {
	cfg := &config.Config{
		Discord: config.DiscordConfig{
			BotToken:      "",
			ProdChannelID: "prod",
			BetaChannelID: "beta",
		},
		TradeMonitor: config.TradeMonitorConfig{
			UseWebSocket: true,
		},
		Polymarket: config.PolymarketConfig{
			GammaAPIURL: "https://gamma.example.com",
			DataAPIURL:  "https://data.example.com",
		},
		Gist: config.GistConfig{
			Token:  "",
			GistID: "",
		},
	}

	logger := zap.NewNop()
	clients := NewClients(logger, cfg)

	if clients.Logger != logger {
		t.Error("unexpected logger")
	}
	if clients.Discord == nil {
		t.Error("expected Discord client to be set")
	}
	if clients.Polymarket == nil {
		t.Error("expected Polymarket client to be set")
	}
	if clients.PolymarketEvents == nil {
		t.Error("expected PolymarketEvents client to be set when UseWebSocket is true")
	}
	if clients.Gist == nil {
		t.Error("expected Gist client to be set")
	}
}

func TestNewClients_PollingMode(t *testing.T) {
	cfg := &config.Config{
		Discord: config.DiscordConfig{},
		TradeMonitor: config.TradeMonitorConfig{
			UseWebSocket: false,
		},
		Polymarket: config.PolymarketConfig{
			GammaAPIURL: "https://gamma.example.com",
			DataAPIURL:  "https://data.example.com",
		},
		Gist: config.GistConfig{},
	}

	clients := NewClients(zap.NewNop(), cfg)

	if clients.PolymarketEvents != nil {
		t.Error("expected PolymarketEvents client to be nil when UseWebSocket is false")
	}
}

func TestNewClients_NilLogger(t *testing.T) {
	cfg := &config.Config{
		Discord: config.DiscordConfig{},
		Polymarket: config.PolymarketConfig{
			GammaAPIURL: "https://gamma.example.com",
			DataAPIURL:  "https://data.example.com",
		},
		Gist: config.GistConfig{},
	}

	clients := NewClients(nil, cfg)

	if clients.Logger != nil {
		t.Error("expected nil logger to remain nil")
	}
	// Other clients should still be initialized
	if clients.Discord == nil {
		t.Error("expected Discord client to be set")
	}
}
