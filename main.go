package main

import (
	"context"
	"os"
	"os/signal"
	clts "polybot/clients"
	"polybot/config"
	"polybot/internal/app"
	"strings"
	"syscall"
	"time"

	"go.uber.org/zap"
)

const (
	// loadTimeout is the maximum time to wait for loading from gist
	loadTimeout = 30 * time.Second
)

func main() {
	logger, err := zap.NewProduction()
	if err != nil {
		panic(err)
	}
	defer logger.Sync()

	// Load config from environment variables
	envConfig := config.Load()
	logger.Info("starting bot", zap.Bool("isProd", envConfig.IsProd))

	// Create LiveConfig with env config as initial value
	liveConfig := config.NewLiveConfig(envConfig)

	// Initialize clients (needed for Gist access)
	logger.Info("instantiating clients")
	clients := clts.NewClients(logger, envConfig)

	// Get settings Gist ID from env
	settingsGistID := os.Getenv("SETTINGS_GIST_ID")

	// Create SettingsManager
	settingsManager := config.NewSettingsManager(logger, clients.Gist, settingsGistID, liveConfig)

	// Load settings from Gist if enabled
	if settingsManager.IsEnabled() {
		logger.Info("loading settings from gist", zap.String("gist_id", settingsGistID))
		loadCtx, loadCancel := context.WithTimeout(context.Background(), loadTimeout)
		cfg, err := settingsManager.LoadSettings(loadCtx, envConfig)
		loadCancel()
		if err != nil {
			logger.Warn("failed to load settings from gist, using env/defaults", zap.Error(err))
		} else if cfg != nil {
			if err := liveConfig.Update(cfg); err != nil {
				logger.Warn("failed to apply gist settings", zap.Error(err))
			} else {
				logger.Info("settings loaded from gist")
			}
		}
	} else {
		logger.Info("settings gist not configured, using env/defaults")
	}

	// Create AuthHandler for passkey authentication
	var authHandler *app.AuthHandler
	rpID := os.Getenv("WEBAUTHN_RP_ID")
	rpOriginsStr := os.Getenv("WEBAUTHN_RP_ORIGINS")
	if settingsGistID != "" && rpID != "" && rpOriginsStr != "" {
		rpOrigins := strings.Split(rpOriginsStr, ",")
		var err error
		authHandler, err = app.NewAuthHandler(logger, clients.Gist, settingsGistID, rpID, rpOrigins)
		if err != nil {
			logger.Warn("failed to create auth handler", zap.Error(err))
		} else {
			loadCtx, loadCancel := context.WithTimeout(context.Background(), loadTimeout)
			err := authHandler.LoadCredentials(loadCtx)
			loadCancel()
			if err != nil {
				logger.Warn("failed to load passkeys from gist", zap.Error(err))
			}
		}
	} else if settingsGistID != "" {
		logger.Info("passkey auth not configured (WEBAUTHN_RP_ID and WEBAUTHN_RP_ORIGINS required)")
	}

	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer stop()

	runner := app.NewRunner(clients, liveConfig, settingsManager, authHandler)
	if err := runner.Run(ctx); err != nil {
		logger.Fatal("runner failed", zap.Error(err))
	}
}
