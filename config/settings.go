package config

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"go.uber.org/zap"
)

const (
	// SettingsFileName is the name of the settings file in the Gist.
	SettingsFileName = "polybot_settings.json"
)

// SettingsSnapshot represents the settings stored in a Gist.
type SettingsSnapshot struct {
	Version   int       `json:"version"`
	UpdatedAt time.Time `json:"updated_at"`
	Config    *Config   `json:"config"`
}

// GistStorage is an interface for Gist operations.
// This allows for easy mocking in tests.
type GistStorage interface {
	IsEnabled() bool
	LoadJSON(ctx context.Context, filename string, dest any) error
	SaveJSON(ctx context.Context, filename string, data any) error
	GetGistID() string
}

// SettingsManager handles loading and saving settings from/to Gist.
type SettingsManager struct {
	logger       *zap.Logger
	gist         GistStorage
	settingsGist string // Separate Gist ID for settings (optional)
	liveConfig   *LiveConfig
}

// NewSettingsManager creates a new SettingsManager.
func NewSettingsManager(logger *zap.Logger, gist GistStorage, settingsGistID string, liveConfig *LiveConfig) *SettingsManager {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &SettingsManager{
		logger:       logger,
		gist:         gist,
		settingsGist: settingsGistID,
		liveConfig:   liveConfig,
	}
}

// IsEnabled returns true if settings persistence is available.
func (sm *SettingsManager) IsEnabled() bool {
	return sm.gist != nil && sm.gist.IsEnabled() && sm.settingsGist != ""
}

// LoadSettings loads settings from Gist and merges with env config.
// Priority: Gist > Environment Variables > Defaults
func (sm *SettingsManager) LoadSettings(ctx context.Context, envConfig *Config) (*Config, error) {
	// Start with defaults
	baseConfig := Defaults()

	// Merge env config on top of defaults (env vars override defaults)
	if envConfig != nil {
		baseConfig = mergeConfigs(baseConfig, envConfig)
	}

	// If Gist is not enabled, return env-merged config
	if !sm.IsEnabled() {
		sm.logger.Info("settings gist not configured, using env/defaults")
		return baseConfig, nil
	}

	// Try to load from Gist
	var snapshot SettingsSnapshot
	err := sm.loadFromGist(ctx, &snapshot)
	if err != nil {
		sm.logger.Warn("failed to load settings from gist, using env/defaults",
			zap.Error(err),
		)
		return baseConfig, nil
	}

	// Merge Gist settings on top of env config
	if snapshot.Config != nil {
		baseConfig = mergeConfigs(baseConfig, snapshot.Config)
		sm.logger.Info("loaded settings from gist",
			zap.Time("updated_at", snapshot.UpdatedAt),
			zap.Int("version", snapshot.Version),
		)
	}

	return baseConfig, nil
}

// SaveSettings saves the current config to Gist.
func (sm *SettingsManager) SaveSettings(ctx context.Context) error {
	if !sm.IsEnabled() {
		return fmt.Errorf("settings gist not configured")
	}

	cfg := sm.liveConfig.Get()

	snapshot := SettingsSnapshot{
		Version:   1,
		UpdatedAt: time.Now(),
		Config:    cfg,
	}

	if err := sm.saveToGist(ctx, snapshot); err != nil {
		return fmt.Errorf("save to gist: %w", err)
	}

	sm.logger.Info("saved settings to gist")
	return nil
}

// UpdateAndSave updates the config and saves to Gist.
func (sm *SettingsManager) UpdateAndSave(ctx context.Context, newConfig *Config) error {
	// Update live config (validates internally)
	if err := sm.liveConfig.Update(newConfig); err != nil {
		return fmt.Errorf("update config: %w", err)
	}

	// Save to Gist if enabled
	if sm.IsEnabled() {
		if err := sm.SaveSettings(ctx); err != nil {
			sm.logger.Error("failed to save settings to gist", zap.Error(err))
			// Don't fail the update, just log the error
		}
	}

	return nil
}

// UpdatePartialAndSave updates specific fields and saves to Gist.
func (sm *SettingsManager) UpdatePartialAndSave(ctx context.Context, partial *Config) error {
	// Get current config and merge
	current := sm.liveConfig.Get()
	merged := mergeConfigs(current, partial)

	return sm.UpdateAndSave(ctx, merged)
}

// loadFromGist loads settings from the configured Gist.
func (sm *SettingsManager) loadFromGist(ctx context.Context, dest *SettingsSnapshot) error {
	// Create a temporary gist client call that uses the settings gist ID
	// The gist client's LoadJSON uses the default gist ID, but we want to use
	// the settings-specific one

	// For now, we'll use a direct approach - load raw and parse
	return sm.gist.LoadJSON(ctx, SettingsFileName, dest)
}

// saveToGist saves settings to the configured Gist.
func (sm *SettingsManager) saveToGist(ctx context.Context, snapshot SettingsSnapshot) error {
	return sm.gist.SaveJSON(ctx, SettingsFileName, snapshot)
}

// GetCurrentConfig returns the current config.
func (sm *SettingsManager) GetCurrentConfig() *Config {
	return sm.liveConfig.Get()
}

// GetLiveConfig returns the LiveConfig for observers to register.
func (sm *SettingsManager) GetLiveConfig() *LiveConfig {
	return sm.liveConfig
}

// mergeConfigs merges overlay config onto base config.
// Only non-zero values from overlay are applied.
func mergeConfigs(base, overlay *Config) *Config {
	if base == nil {
		base = Defaults()
	}
	if overlay == nil {
		return base.Clone()
	}

	// Use JSON marshal/unmarshal to merge
	// This works because json.Unmarshal only overwrites fields present in the JSON
	result := base.Clone()

	// Marshal overlay to JSON (omits zero values)
	overlayJSON, err := json.Marshal(overlay)
	if err != nil {
		return result
	}

	// Unmarshal onto result (only overwrites non-zero fields)
	_ = json.Unmarshal(overlayJSON, result)

	// Preserve sensitive fields that aren't in JSON (prefer overlay if set, else base)
	result.Discord.BotToken = overlay.Discord.BotToken
	if result.Discord.BotToken == "" {
		result.Discord.BotToken = base.Discord.BotToken
	}
	result.Telegram.BotToken = overlay.Telegram.BotToken
	if result.Telegram.BotToken == "" {
		result.Telegram.BotToken = base.Telegram.BotToken
	}

	// For Gist config, prefer overlay values if set
	result.Gist.Token = overlay.Gist.Token
	if result.Gist.Token == "" {
		result.Gist.Token = base.Gist.Token
	}
	result.Gist.GistID = overlay.Gist.GistID
	if result.Gist.GistID == "" {
		result.Gist.GistID = base.Gist.GistID
	}
	result.Gist.TasksGistID = overlay.Gist.TasksGistID
	if result.Gist.TasksGistID == "" {
		result.Gist.TasksGistID = base.Gist.TasksGistID
	}

	result.ContrarianCache.GistID = overlay.ContrarianCache.GistID
	if result.ContrarianCache.GistID == "" {
		result.ContrarianCache.GistID = base.ContrarianCache.GistID
	}
	result.HedgeTracker.GistID = overlay.HedgeTracker.GistID
	if result.HedgeTracker.GistID == "" {
		result.HedgeTracker.GistID = base.HedgeTracker.GistID
	}
	result.PatternTracker.GistID = overlay.PatternTracker.GistID
	if result.PatternTracker.GistID == "" {
		result.PatternTracker.GistID = base.PatternTracker.GistID
	}

	return result
}

// SettingsInfo provides metadata about the current settings state.
type SettingsInfo struct {
	Source       string    `json:"source"`        // "gist", "env", "default"
	LastUpdated  time.Time `json:"last_updated"`
	GistEnabled  bool      `json:"gist_enabled"`
	GistID       string    `json:"gist_id,omitempty"`
	IsValid      bool      `json:"is_valid"`
	Errors       []string  `json:"errors,omitempty"`
}

// GetSettingsInfo returns metadata about the current settings.
func (sm *SettingsManager) GetSettingsInfo() SettingsInfo {
	cfg := sm.liveConfig.Get()
	validation := cfg.Validate()

	info := SettingsInfo{
		LastUpdated: sm.liveConfig.LastUpdated(),
		GistEnabled: sm.IsEnabled(),
		IsValid:     validation.Valid,
	}

	if sm.IsEnabled() {
		info.Source = "gist"
		info.GistID = sm.settingsGist
	} else {
		info.Source = "env"
	}

	for _, e := range validation.Errors {
		info.Errors = append(info.Errors, e.Field+": "+e.Message)
	}

	return info
}
