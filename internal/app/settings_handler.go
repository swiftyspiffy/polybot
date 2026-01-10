package app

import (
	"context"
	"encoding/json"
	"net/http"
	"polybot/config"
	"time"

	"go.uber.org/zap"
)

// SettingsHandler handles settings-related HTTP requests.
type SettingsHandler struct {
	logger      *zap.Logger
	settings    *config.SettingsManager
	authHandler *AuthHandler
}

// NewSettingsHandler creates a new SettingsHandler.
func NewSettingsHandler(logger *zap.Logger, settings *config.SettingsManager, authHandler *AuthHandler) *SettingsHandler {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &SettingsHandler{
		logger:      logger,
		settings:    settings,
		authHandler: authHandler,
	}
}

// RegisterRoutes registers the settings routes on the given mux.
func (h *SettingsHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/settings", h.handleSettingsPage)
	mux.HandleFunc("/api/settings", h.handleSettingsAPI)
	mux.HandleFunc("/api/settings/reset", h.handleSettingsReset)
	mux.HandleFunc("/api/settings/info", h.handleSettingsInfo)
}

// requireAuth checks if the request is authenticated when auth is enabled.
// Returns true if allowed to proceed, false if a 401 response was sent.
func (h *SettingsHandler) requireAuth(w http.ResponseWriter, r *http.Request) bool {
	// If no auth handler, allow all access
	if h.authHandler == nil {
		return true
	}

	// If no credentials registered, allow all access
	if !h.authHandler.HasCredentials() {
		return true
	}

	// Check if authenticated
	if h.authHandler.IsAuthenticated(r) {
		return true
	}

	// Not authenticated - return 401
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error":   "authentication_required",
		"message": "You must be logged in to modify settings",
	})
	return false
}

// handleSettingsPage serves the settings page HTML.
func (h *SettingsHandler) handleSettingsPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(settingsPageHTML))
}

// handleSettingsAPI handles GET and POST requests for settings.
func (h *SettingsHandler) handleSettingsAPI(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.getSettings(w, r)
	case http.MethodPost:
		h.updateSettings(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// getSettings returns the current settings as JSON.
func (h *SettingsHandler) getSettings(w http.ResponseWriter, _ *http.Request) {
	cfg := h.settings.GetCurrentConfig()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(cfg); err != nil {
		h.logger.Error("failed to encode settings", zap.Error(err))
		http.Error(w, "Failed to encode settings", http.StatusInternalServerError)
		return
	}
}

// updateSettings updates settings from the request body.
func (h *SettingsHandler) updateSettings(w http.ResponseWriter, r *http.Request) {
	// Check authentication
	if !h.requireAuth(w, r) {
		return
	}

	var newConfig config.Config

	// Start with current config as base
	currentConfig := h.settings.GetCurrentConfig()
	newConfig = *currentConfig

	// Decode request body on top of current config
	if err := json.NewDecoder(r.Body).Decode(&newConfig); err != nil {
		h.logger.Error("failed to decode settings", zap.Error(err))
		http.Error(w, "Invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Validate
	validation := newConfig.Validate()
	if !validation.Valid {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"errors":  validation.Errors,
		})
		return
	}

	// Update and save
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	if err := h.settings.UpdateAndSave(ctx, &newConfig); err != nil {
		h.logger.Error("failed to update settings", zap.Error(err))
		http.Error(w, "Failed to update settings: "+err.Error(), http.StatusInternalServerError)
		return
	}

	h.logger.Info("settings updated via API")

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"success":    true,
		"applied_at": time.Now(),
	})
}

// handleSettingsReset resets settings to defaults.
func (h *SettingsHandler) handleSettingsReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Check authentication
	if !h.requireAuth(w, r) {
		return
	}

	// Get defaults
	defaults := config.Defaults()

	// Preserve env-only fields from current config
	current := h.settings.GetCurrentConfig()
	defaults.Discord.BotToken = current.Discord.BotToken
	defaults.Telegram.BotToken = current.Telegram.BotToken
	defaults.Gist = current.Gist
	defaults.ContrarianCache.GistID = current.ContrarianCache.GistID
	defaults.HedgeTracker.GistID = current.HedgeTracker.GistID
	defaults.PatternTracker.GistID = current.PatternTracker.GistID

	// Update and save
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	if err := h.settings.UpdateAndSave(ctx, defaults); err != nil {
		h.logger.Error("failed to reset settings", zap.Error(err))
		http.Error(w, "Failed to reset settings: "+err.Error(), http.StatusInternalServerError)
		return
	}

	h.logger.Info("settings reset to defaults via API")

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"success":    true,
		"applied_at": time.Now(),
	})
}

// handleSettingsInfo returns metadata about settings state.
func (h *SettingsHandler) handleSettingsInfo(w http.ResponseWriter, _ *http.Request) {
	info := h.settings.GetSettingsInfo()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(info)
}

// settingsPageHTML is the HTML for the settings page.
const settingsPageHTML = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Polybot Settings</title>
    <style>
        :root {
            --bg-primary: #0d1117;
            --bg-secondary: #161b22;
            --bg-tertiary: #21262d;
            --border-color: #30363d;
            --text-primary: #e6edf3;
            --text-secondary: #8b949e;
            --accent: #58a6ff;
            --success: #3fb950;
            --warning: #d29922;
            --error: #f85149;
        }
        * { box-sizing: border-box; margin: 0; padding: 0; }
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Helvetica, Arial, sans-serif;
            background: var(--bg-primary);
            color: var(--text-primary);
            line-height: 1.5;
            padding: 20px;
            max-width: 1200px;
            margin: 0 auto;
        }
        h1 { margin-bottom: 10px; }
        .header {
            display: flex;
            justify-content: space-between;
            align-items: center;
            margin-bottom: 20px;
            flex-wrap: wrap;
            gap: 10px;
        }
        .header-actions {
            display: flex;
            gap: 10px;
        }
        .nav-link {
            color: var(--accent);
            text-decoration: none;
            padding: 8px 16px;
            border: 1px solid var(--border-color);
            border-radius: 6px;
            transition: all 0.2s;
        }
        .nav-link:hover {
            background: var(--bg-tertiary);
        }
        .status-bar {
            background: var(--bg-secondary);
            border: 1px solid var(--border-color);
            border-radius: 6px;
            padding: 12px 16px;
            margin-bottom: 20px;
            display: flex;
            justify-content: space-between;
            align-items: center;
            flex-wrap: wrap;
            gap: 10px;
        }
        .status-item {
            display: flex;
            align-items: center;
            gap: 8px;
        }
        .status-dot {
            width: 8px;
            height: 8px;
            border-radius: 50%;
        }
        .status-dot.connected { background: var(--success); }
        .status-dot.disconnected { background: var(--error); }
        .section {
            background: var(--bg-secondary);
            border: 1px solid var(--border-color);
            border-radius: 6px;
            margin-bottom: 16px;
        }
        .section-header {
            padding: 16px;
            cursor: pointer;
            display: flex;
            justify-content: space-between;
            align-items: center;
            border-bottom: 1px solid transparent;
        }
        .section-header:hover {
            background: var(--bg-tertiary);
        }
        .section-header.expanded {
            border-bottom-color: var(--border-color);
        }
        .section-title {
            font-weight: 600;
            display: flex;
            align-items: center;
            gap: 8px;
        }
        .section-toggle {
            color: var(--text-secondary);
            font-size: 12px;
        }
        .section-content {
            display: none;
            padding: 16px;
        }
        .section-content.expanded {
            display: block;
        }
        .form-group {
            margin-bottom: 16px;
        }
        .form-row {
            display: grid;
            grid-template-columns: repeat(auto-fit, minmax(280px, 1fr));
            gap: 16px;
        }
        label {
            display: block;
            margin-bottom: 6px;
            color: var(--text-secondary);
            font-size: 13px;
        }
        input[type="text"],
        input[type="number"],
        select {
            width: 100%;
            padding: 8px 12px;
            background: var(--bg-tertiary);
            border: 1px solid var(--border-color);
            border-radius: 6px;
            color: var(--text-primary);
            font-size: 14px;
        }
        input:focus, select:focus {
            outline: none;
            border-color: var(--accent);
        }
        input[type="checkbox"] {
            width: 18px;
            height: 18px;
            accent-color: var(--accent);
        }
        .checkbox-label {
            display: flex;
            align-items: center;
            gap: 8px;
            cursor: pointer;
        }
        .help-text {
            font-size: 12px;
            color: var(--text-secondary);
            margin-top: 4px;
        }
        .btn {
            padding: 10px 20px;
            border: none;
            border-radius: 6px;
            cursor: pointer;
            font-size: 14px;
            font-weight: 500;
            transition: all 0.2s;
        }
        .btn-primary {
            background: var(--accent);
            color: white;
        }
        .btn-primary:hover {
            opacity: 0.9;
        }
        .btn-secondary {
            background: var(--bg-tertiary);
            color: var(--text-primary);
            border: 1px solid var(--border-color);
        }
        .btn-secondary:hover {
            background: var(--border-color);
        }
        .btn-danger {
            background: var(--error);
            color: white;
        }
        .btn-danger:hover {
            opacity: 0.9;
        }
        .actions {
            display: flex;
            gap: 10px;
            justify-content: flex-end;
            margin-top: 20px;
            padding-top: 20px;
            border-top: 1px solid var(--border-color);
        }
        .toast {
            position: fixed;
            bottom: 20px;
            right: 20px;
            padding: 12px 20px;
            border-radius: 6px;
            color: white;
            font-weight: 500;
            opacity: 0;
            transform: translateY(20px);
            transition: all 0.3s;
            z-index: 1000;
        }
        .toast.show {
            opacity: 1;
            transform: translateY(0);
        }
        .toast.success { background: var(--success); }
        .toast.error { background: var(--error); }
        .unsaved-indicator {
            display: none;
            color: var(--warning);
            font-size: 13px;
        }
        .unsaved-indicator.show {
            display: inline;
        }
        .loading {
            opacity: 0.5;
            pointer-events: none;
        }
        .auth-section {
            background: var(--bg-secondary);
            border: 1px solid var(--border-color);
            border-radius: 6px;
            padding: 16px;
            margin-bottom: 20px;
            display: flex;
            justify-content: space-between;
            align-items: center;
            flex-wrap: wrap;
            gap: 10px;
        }
        .auth-info {
            display: flex;
            align-items: center;
            gap: 10px;
        }
        .auth-actions {
            display: flex;
            gap: 10px;
        }
        .read-only-notice {
            background: var(--warning);
            color: black;
            padding: 8px 16px;
            border-radius: 6px;
            font-size: 13px;
        }
        .form-disabled input,
        .form-disabled select {
            opacity: 0.6;
            cursor: not-allowed;
        }
        .modal-overlay {
            display: none;
            position: fixed;
            top: 0;
            left: 0;
            right: 0;
            bottom: 0;
            background: rgba(0,0,0,0.7);
            z-index: 1000;
            justify-content: center;
            align-items: center;
        }
        .modal-overlay.show {
            display: flex;
        }
        .modal {
            background: var(--bg-secondary);
            border: 1px solid var(--border-color);
            border-radius: 8px;
            padding: 24px;
            max-width: 500px;
            width: 90%;
        }
        .modal h2 {
            margin-bottom: 16px;
        }
        .modal-actions {
            display: flex;
            gap: 10px;
            justify-content: flex-end;
            margin-top: 20px;
        }
        .credential-list {
            margin: 16px 0;
        }
        .credential-item {
            display: flex;
            justify-content: space-between;
            align-items: center;
            padding: 12px;
            background: var(--bg-tertiary);
            border-radius: 6px;
            margin-bottom: 8px;
        }
        .credential-info {
            display: flex;
            flex-direction: column;
            gap: 4px;
        }
        .credential-name {
            font-weight: 500;
        }
        .credential-meta {
            font-size: 12px;
            color: var(--text-secondary);
        }
        .admin-badge {
            background: var(--accent);
            color: white;
            padding: 2px 8px;
            border-radius: 4px;
            font-size: 11px;
            margin-left: 8px;
        }
        .registration-controls {
            margin-top: 20px;
            padding-top: 16px;
            border-top: 1px solid var(--border);
        }
        .registration-controls h3 {
            margin: 0 0 12px 0;
            font-size: 14px;
            color: var(--text-secondary);
        }
        .registration-status {
            padding: 8px 12px;
            border-radius: 6px;
            margin-bottom: 12px;
            font-size: 13px;
        }
        .registration-status.active {
            background: rgba(76, 175, 80, 0.15);
            color: #4caf50;
        }
        .registration-status.inactive {
            background: var(--bg-tertiary);
            color: var(--text-secondary);
        }
        .registration-options {
            display: flex;
            gap: 16px;
            margin-bottom: 12px;
        }
        .registration-options .form-row {
            display: flex;
            align-items: center;
            gap: 8px;
        }
        .registration-options label {
            font-size: 13px;
            color: var(--text-secondary);
        }
        .registration-options select {
            padding: 6px 10px;
            border-radius: 4px;
            border: 1px solid var(--border);
            background: var(--bg-secondary);
            color: var(--text-primary);
            font-size: 13px;
        }
        .registration-actions {
            display: flex;
            gap: 8px;
        }
        .btn-danger {
            background: #dc3545;
        }
        .btn-danger:hover {
            background: #c82333;
        }
    </style>
</head>
<body>
    <div class="header">
        <div>
            <h1>Polybot Settings</h1>
            <span class="unsaved-indicator" id="unsavedIndicator">* Unsaved changes</span>
        </div>
        <div class="header-actions">
            <a href="/" class="nav-link">Dashboard</a>
        </div>
    </div>

    <div class="status-bar">
        <div class="status-item">
            <span class="status-dot" id="gistStatus"></span>
            <span id="gistStatusText">Checking Gist...</span>
        </div>
        <div class="status-item">
            <span style="color: var(--text-secondary)">Last updated:</span>
            <span id="lastUpdated">-</span>
        </div>
    </div>

    <div class="auth-section" id="authSection" style="display: none;">
        <div class="auth-info">
            <span id="authStatus">Checking auth...</span>
        </div>
        <div class="auth-actions" id="authActions"></div>
    </div>

    <div class="read-only-notice" id="readOnlyNotice" style="display: none;">
        Settings are read-only. Log in with a passkey to make changes.
    </div>

    <form id="settingsForm">
        <!-- Trade Monitor Section -->
        <div class="section">
            <div class="section-header" onclick="toggleSection(this)">
                <span class="section-title">Trade Monitor</span>
                <span class="section-toggle">▼</span>
            </div>
            <div class="section-content">
                <div class="form-row">
                    <div class="form-group">
                        <label for="tm_min_notional">Min Notional ($)</label>
                        <input type="number" id="tm_min_notional" name="trade_monitor.min_notional" step="100">
                        <div class="help-text">Minimum trade size to alert on</div>
                    </div>
                    <div class="form-group">
                        <label for="tm_obvious_price">Obvious Price Threshold</label>
                        <input type="number" id="tm_obvious_price" name="trade_monitor.obvious_price" step="0.01" min="0" max="1">
                        <div class="help-text">Skip alerts for trades at or above this price (0-1)</div>
                    </div>
                    <div class="form-group">
                        <label for="tm_poll_interval">Poll Interval</label>
                        <input type="text" id="tm_poll_interval" name="trade_monitor.poll_interval" placeholder="10s">
                        <div class="help-text">How often to poll for trades (e.g., 10s, 1m)</div>
                    </div>
                </div>
                <div class="form-row">
                    <div class="form-group">
                        <label for="tm_high_win_rate">High Win Rate Threshold</label>
                        <input type="number" id="tm_high_win_rate" name="trade_monitor.high_win_rate_threshold" step="0.01" min="0" max="1">
                        <div class="help-text">Min win rate to trigger alert (0-1)</div>
                    </div>
                    <div class="form-group">
                        <label for="tm_min_resolved">Min Resolved For Win Rate</label>
                        <input type="number" id="tm_min_resolved" name="trade_monitor.min_resolved_for_win_rate" min="1">
                        <div class="help-text">Min positions to consider win rate</div>
                    </div>
                    <div class="form-group">
                        <label for="tm_win_rate_max_entry">Win Rate Max Entry Price</label>
                        <input type="number" id="tm_win_rate_max_entry" name="trade_monitor.win_rate_max_entry_price" step="0.01" min="0" max="1">
                        <div class="help-text">Max entry price to count as suspicious (0-1)</div>
                    </div>
                </div>
                <div class="form-row">
                    <div class="form-group">
                        <label for="tm_extreme_low_price">Extreme Low Price</label>
                        <input type="number" id="tm_extreme_low_price" name="trade_monitor.extreme_low_price" step="0.01" min="0" max="1">
                        <div class="help-text">Price threshold for extreme bets (0-1)</div>
                    </div>
                    <div class="form-group">
                        <label for="tm_extreme_min_notional">Extreme Min Notional ($)</label>
                        <input type="number" id="tm_extreme_min_notional" name="trade_monitor.extreme_min_notional" step="100">
                    </div>
                </div>
                <div class="form-row">
                    <div class="form-group">
                        <label for="tm_contrarian_max_price">Contrarian Max Price</label>
                        <input type="number" id="tm_contrarian_max_price" name="trade_monitor.contrarian_max_price" step="0.01" min="0" max="1">
                    </div>
                    <div class="form-group">
                        <label for="tm_contrarian_min_notional">Contrarian Min Notional ($)</label>
                        <input type="number" id="tm_contrarian_min_notional" name="trade_monitor.contrarian_min_notional" step="100">
                    </div>
                </div>
                <div class="form-row">
                    <div class="form-group">
                        <label for="tm_massive_min_notional">Massive Trade Min Notional ($)</label>
                        <input type="number" id="tm_massive_min_notional" name="trade_monitor.massive_trade_min_notional" step="1000">
                    </div>
                    <div class="form-group">
                        <label for="tm_massive_max_price">Massive Trade Max Price</label>
                        <input type="number" id="tm_massive_max_price" name="trade_monitor.massive_trade_max_price" step="0.01" min="0" max="1">
                    </div>
                </div>
                <div class="form-row">
                    <div class="form-group">
                        <label for="tm_new_wallet_max_markets">New Wallet Max Markets</label>
                        <input type="number" id="tm_new_wallet_max_markets" name="trade_monitor.new_wallet_max_markets" min="0">
                    </div>
                    <div class="form-group">
                        <label for="tm_new_wallet_min_notional">New Wallet Min Notional ($)</label>
                        <input type="number" id="tm_new_wallet_min_notional" name="trade_monitor.new_wallet_min_notional" step="100">
                    </div>
                </div>
                <div class="form-group">
                    <label class="checkbox-label">
                        <input type="checkbox" id="tm_use_websocket" name="trade_monitor.use_websocket">
                        <span>Use WebSocket (real-time)</span>
                    </label>
                </div>
            </div>
        </div>

        <!-- Markets Section -->
        <div class="section">
            <div class="section-header" onclick="toggleSection(this)">
                <span class="section-title">Markets</span>
                <span class="section-toggle">▼</span>
            </div>
            <div class="section-content">
                <div class="form-row">
                    <div class="form-group">
                        <label for="markets_top_count">Top Markets Count</label>
                        <input type="number" id="markets_top_count" name="markets.top_markets_count" min="1">
                    </div>
                    <div class="form-group">
                        <label for="markets_refresh_interval">Refresh Interval</label>
                        <input type="text" id="markets_refresh_interval" name="markets.refresh_interval" placeholder="1m">
                    </div>
                </div>
                <div class="form-group">
                    <label class="checkbox-label">
                        <input type="checkbox" id="markets_specific_only" name="markets.specific_markets_only">
                        <span>Specific Markets Only (ignore top-N)</span>
                    </label>
                </div>
            </div>
        </div>

        <!-- Cache Section -->
        <div class="section">
            <div class="section-header" onclick="toggleSection(this)">
                <span class="section-title">Cache</span>
                <span class="section-toggle">▼</span>
            </div>
            <div class="section-content">
                <div class="form-row">
                    <div class="form-group">
                        <label for="cache_wallet_ttl">Wallet Cache TTL</label>
                        <input type="text" id="cache_wallet_ttl" name="cache.wallet_cache_ttl" placeholder="1m">
                    </div>
                    <div class="form-group">
                        <label for="cache_save_interval">Save Interval</label>
                        <input type="text" id="cache_save_interval" name="cache.save_interval" placeholder="10m">
                    </div>
                </div>
            </div>
        </div>

        <!-- Health Server Section -->
        <div class="section">
            <div class="section-header" onclick="toggleSection(this)">
                <span class="section-title">Health Server</span>
                <span class="section-toggle">▼</span>
            </div>
            <div class="section-content">
                <div class="form-row">
                    <div class="form-group">
                        <label for="health_port">Port</label>
                        <input type="number" id="health_port" name="health_server.port" min="1" max="65535">
                    </div>
                </div>
                <div class="form-group">
                    <label class="checkbox-label">
                        <input type="checkbox" id="health_enabled" name="health_server.enabled">
                        <span>Enabled</span>
                    </label>
                </div>
            </div>
        </div>

        <div class="actions">
            <button type="button" class="btn btn-danger" onclick="resetToDefaults()">Reset to Defaults</button>
            <button type="button" class="btn btn-secondary" onclick="loadSettings()">Reload</button>
            <button type="submit" class="btn btn-primary">Save Settings</button>
        </div>
    </form>

    <div class="toast" id="toast"></div>

    <!-- Passkey Registration Modal -->
    <div class="modal-overlay" id="passkeyNameModal">
        <div class="modal">
            <h2>Register Passkey</h2>
            <p style="color: var(--text-secondary); margin-bottom: 16px;">
                Enter your username and a name for this device.
            </p>
            <div class="form-group">
                <label for="passkeyUsername">Username *</label>
                <input type="text" id="passkeyUsername" placeholder="johndoe" maxlength="50" required>
                <div class="help-text">Your username to identify your account</div>
            </div>
            <div class="form-group">
                <label for="passkeyName">Device Name</label>
                <input type="text" id="passkeyName" placeholder="My MacBook" maxlength="50">
                <div class="help-text">Optional name to identify this device</div>
            </div>
            <div class="modal-actions">
                <button type="button" class="btn btn-secondary" onclick="closePasskeyModal()">Cancel</button>
                <button type="button" class="btn btn-primary" onclick="confirmRegister()">Continue</button>
            </div>
        </div>
    </div>

    <!-- Credentials Management Modal -->
    <div class="modal-overlay" id="credentialsModal">
        <div class="modal">
            <h2>Manage Passkeys</h2>
            <div class="credential-list" id="credentialList">
                Loading...
            </div>
            <div class="registration-controls">
                <h3>Registration Settings</h3>
                <div id="registrationStatus" class="registration-status"></div>
                <div class="registration-options">
                    <div class="form-row">
                        <label for="regMinutes">Duration:</label>
                        <select id="regMinutes">
                            <option value="5">5 minutes</option>
                            <option value="10" selected>10 minutes</option>
                            <option value="30">30 minutes</option>
                            <option value="60">60 minutes</option>
                        </select>
                    </div>
                    <div class="form-row">
                        <label for="regUses">Max uses:</label>
                        <select id="regUses">
                            <option value="0">Unlimited</option>
                            <option value="1" selected>1 registration</option>
                            <option value="2">2 registrations</option>
                            <option value="5">5 registrations</option>
                        </select>
                    </div>
                </div>
                <div class="registration-actions">
                    <button type="button" class="btn btn-primary" onclick="enableRegistration()">Enable Registration</button>
                    <button type="button" class="btn btn-danger" id="disableRegBtn" onclick="disableRegistration()" style="display:none;">Disable Registration</button>
                </div>
            </div>
            <div class="modal-actions">
                <button type="button" class="btn btn-secondary" onclick="closeCredentialsModal()">Close</button>
            </div>
        </div>
    </div>

    <script>
        let originalSettings = null;
        let hasChanges = false;
        let authState = null;
        let isReadOnly = false;

        // Auth functions
        async function checkAuthStatus() {
            try {
                const response = await fetch('/api/auth/status');
                if (!response.ok) {
                    // Auth endpoint not available - auth disabled
                    document.getElementById('authSection').style.display = 'none';
                    return;
                }
                authState = await response.json();
                updateAuthUI();
            } catch (err) {
                console.error('Auth check failed:', err);
                document.getElementById('authSection').style.display = 'none';
            }
        }

        function updateAuthUI() {
            const section = document.getElementById('authSection');
            const statusEl = document.getElementById('authStatus');
            const actionsEl = document.getElementById('authActions');
            const readOnlyNotice = document.getElementById('readOnlyNotice');
            const form = document.getElementById('settingsForm');

            if (!authState || !authState.enabled) {
                section.style.display = 'none';
                readOnlyNotice.style.display = 'none';
                setReadOnly(false);
                return;
            }

            section.style.display = 'flex';

            if (!authState.has_credentials) {
                // No passkeys registered yet - allow registration
                statusEl.textContent = 'No passkeys registered. Register to secure settings.';
                actionsEl.innerHTML = '<button class="btn btn-primary" onclick="beginRegister()">Register Passkey</button>';
                readOnlyNotice.style.display = 'none';
                setReadOnly(false);
            } else if (!authState.authenticated) {
                // Passkeys exist but not logged in
                statusEl.textContent = 'Logged out';
                actionsEl.innerHTML = '<button class="btn btn-primary" onclick="beginLogin()">Login with Passkey</button>';
                if (authState.registration_open) {
                    actionsEl.innerHTML += ' <button class="btn btn-secondary" onclick="beginRegister()">Register Passkey</button>';
                }
                readOnlyNotice.style.display = 'block';
                setReadOnly(true);
            } else {
                // Logged in
                const userDisplay = authState.username || 'User';
                statusEl.textContent = 'Logged in as ' + userDisplay + (authState.is_admin ? ' (Admin)' : '');
                let buttons = '<button class="btn btn-secondary" onclick="logout()">Logout</button>';
                if (authState.is_admin) {
                    buttons += ' <button class="btn btn-secondary" onclick="showCredentialsModal()">Manage Passkeys</button>';
                }
                actionsEl.innerHTML = buttons;
                readOnlyNotice.style.display = 'none';
                setReadOnly(false);
            }
        }

        function setReadOnly(readonly) {
            isReadOnly = readonly;
            const form = document.getElementById('settingsForm');
            const inputs = form.querySelectorAll('input, select');
            inputs.forEach(input => {
                input.disabled = readonly;
            });
            form.classList.toggle('form-disabled', readonly);

            // Hide/show action buttons
            const actions = form.querySelector('.actions');
            if (actions) {
                actions.style.display = readonly ? 'none' : 'flex';
            }
        }

        function beginRegister() {
            document.getElementById('passkeyUsername').value = '';
            document.getElementById('passkeyName').value = '';
            document.getElementById('passkeyNameModal').classList.add('show');
            document.getElementById('passkeyUsername').focus();
        }

        function closePasskeyModal() {
            document.getElementById('passkeyNameModal').classList.remove('show');
        }

        async function confirmRegister() {
            const username = document.getElementById('passkeyUsername').value.trim();
            const displayName = document.getElementById('passkeyName').value.trim() || 'My Device';

            if (!username) {
                showToast('Username is required', 'error');
                document.getElementById('passkeyUsername').focus();
                return;
            }

            closePasskeyModal();

            try {
                // Begin registration
                const beginResp = await fetch('/api/auth/register/begin', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ username: username, display_name: displayName })
                });
                if (!beginResp.ok) {
                    const err = await beginResp.json();
                    throw new Error(err.error || 'Failed to begin registration');
                }
                const beginData = await beginResp.json();
                const sessionId = beginData.sessionId;
                const options = beginData.options;

                // Convert base64 to ArrayBuffer
                options.publicKey.challenge = base64ToBuffer(options.publicKey.challenge);
                options.publicKey.user.id = base64ToBuffer(options.publicKey.user.id);
                if (options.publicKey.excludeCredentials) {
                    options.publicKey.excludeCredentials = options.publicKey.excludeCredentials.map(c => ({
                        ...c,
                        id: base64ToBuffer(c.id)
                    }));
                }

                // Create credential
                const credential = await navigator.credentials.create(options);

                // Finish registration
                const finishResp = await fetch('/api/auth/register/finish?sessionId=' + encodeURIComponent(sessionId), {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({
                        sessionId: sessionId,
                        credential: {
                            id: credential.id,
                            rawId: bufferToBase64(credential.rawId),
                            type: credential.type,
                            response: {
                                clientDataJSON: bufferToBase64(credential.response.clientDataJSON),
                                attestationObject: bufferToBase64(credential.response.attestationObject)
                            }
                        }
                    })
                });
                if (!finishResp.ok) {
                    const err = await finishResp.json();
                    throw new Error(err.error || 'Failed to complete registration');
                }

                showToast('Passkey registered successfully!');
                checkAuthStatus();
            } catch (err) {
                if (err.name === 'NotAllowedError') {
                    showToast('Registration cancelled', 'error');
                } else {
                    showToast('Registration failed: ' + err.message, 'error');
                }
            }
        }

        async function beginLogin() {
            try {
                const beginResp = await fetch('/api/auth/login/begin', { method: 'POST' });
                if (!beginResp.ok) {
                    const err = await beginResp.json();
                    throw new Error(err.error || 'Failed to begin login');
                }
                const beginData = await beginResp.json();
                const sessionId = beginData.sessionId;
                const options = beginData.options;

                // Convert base64 to ArrayBuffer
                options.publicKey.challenge = base64ToBuffer(options.publicKey.challenge);
                if (options.publicKey.allowCredentials) {
                    options.publicKey.allowCredentials = options.publicKey.allowCredentials.map(c => ({
                        ...c,
                        id: base64ToBuffer(c.id)
                    }));
                }

                // Get credential
                const credential = await navigator.credentials.get(options);

                // Finish login
                const finishResp = await fetch('/api/auth/login/finish', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({
                        sessionId: sessionId,
                        credential: {
                            id: credential.id,
                            rawId: bufferToBase64(credential.rawId),
                            type: credential.type,
                            response: {
                                clientDataJSON: bufferToBase64(credential.response.clientDataJSON),
                                authenticatorData: bufferToBase64(credential.response.authenticatorData),
                                signature: bufferToBase64(credential.response.signature),
                                userHandle: credential.response.userHandle ? bufferToBase64(credential.response.userHandle) : null
                            }
                        }
                    })
                });
                if (!finishResp.ok) {
                    const err = await finishResp.json();
                    throw new Error(err.error || 'Failed to complete login');
                }

                showToast('Logged in successfully!');
                // Redirect to dashboard after successful login
                setTimeout(() => window.location.href = '/', 500);
            } catch (err) {
                if (err.name === 'NotAllowedError') {
                    showToast('Login cancelled', 'error');
                } else {
                    showToast('Login failed: ' + err.message, 'error');
                }
            }
        }

        async function logout() {
            try {
                await fetch('/api/auth/logout', { method: 'POST' });
                showToast('Logged out');
                checkAuthStatus();
            } catch (err) {
                showToast('Logout failed: ' + err.message, 'error');
            }
        }

        async function showCredentialsModal() {
            document.getElementById('credentialsModal').classList.add('show');
            await loadCredentials();
            updateRegistrationStatus();
        }

        function closeCredentialsModal() {
            document.getElementById('credentialsModal').classList.remove('show');
        }

        async function loadCredentials() {
            const list = document.getElementById('credentialList');
            try {
                const resp = await fetch('/api/auth/credentials');
                if (!resp.ok) throw new Error('Failed to load');
                const data = await resp.json();

                if (!data.credentials || data.credentials.length === 0) {
                    list.innerHTML = '<p style="color: var(--text-secondary)">No passkeys registered.</p>';
                    return;
                }

                list.innerHTML = data.credentials.map(c => {
                    const date = new Date(c.registered_at).toLocaleDateString();
                    const location = c.country || 'Unknown location';
                    return '<div class="credential-item">' +
                        '<div class="credential-info">' +
                        '<span class="credential-name">' + escapeHtml(c.username) +
                        (c.is_admin ? '<span class="admin-badge">Admin</span>' : '') + '</span>' +
                        '<span class="credential-meta">' + escapeHtml(c.display_name) + ' - ' + date + ' (' + escapeHtml(location) + ')</span>' +
                        '</div>' +
                        (c.can_delete ? '<button class="btn btn-danger" onclick="deleteCredential(\'' + c.id + '\')">Remove</button>' : '') +
                        '</div>';
                }).join('');
            } catch (err) {
                list.innerHTML = '<p style="color: var(--error)">Failed to load credentials.</p>';
            }
        }

        async function deleteCredential(id) {
            if (!confirm('Are you sure you want to remove this passkey?')) return;
            try {
                const resp = await fetch('/api/auth/credentials/' + id, { method: 'DELETE' });
                if (!resp.ok) {
                    const err = await resp.json();
                    throw new Error(err.error || 'Failed to delete');
                }
                showToast('Passkey removed');
                await loadCredentials();
                checkAuthStatus();
            } catch (err) {
                showToast('Failed to remove: ' + err.message, 'error');
            }
        }

        async function enableRegistration() {
            try {
                const minutes = parseInt(document.getElementById('regMinutes').value);
                const uses = parseInt(document.getElementById('regUses').value);

                const resp = await fetch('/api/auth/enable-registration', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ minutes, uses })
                });
                if (!resp.ok) {
                    const err = await resp.json();
                    throw new Error(err.error || 'Failed');
                }

                let msg = 'Registration enabled for ' + minutes + ' minutes';
                if (uses > 0) {
                    msg += ' (' + uses + ' use' + (uses > 1 ? 's' : '') + ')';
                }
                showToast(msg);
                await checkAuthStatus();
                updateRegistrationStatus();
            } catch (err) {
                showToast('Failed: ' + err.message, 'error');
            }
        }

        async function disableRegistration() {
            try {
                const resp = await fetch('/api/auth/enable-registration', { method: 'DELETE' });
                if (!resp.ok) {
                    const err = await resp.json();
                    throw new Error(err.error || 'Failed');
                }
                showToast('Registration disabled');
                await checkAuthStatus();
                updateRegistrationStatus();
            } catch (err) {
                showToast('Failed: ' + err.message, 'error');
            }
        }

        function updateRegistrationStatus() {
            const statusEl = document.getElementById('registrationStatus');
            const disableBtn = document.getElementById('disableRegBtn');

            if (!authState || !authState.registration) {
                statusEl.className = 'registration-status inactive';
                statusEl.textContent = 'Registration is closed';
                disableBtn.style.display = 'none';
                return;
            }

            const reg = authState.registration;
            if (reg.enabled) {
                const expiry = new Date(reg.expires_at);
                const now = new Date();
                const minsLeft = Math.max(0, Math.ceil((expiry - now) / 60000));

                let statusText = 'Registration open - ' + minsLeft + ' min remaining';
                if (reg.uses_left) {
                    statusText += ' (' + reg.uses_left + ' use' + (reg.uses_left > 1 ? 's' : '') + ' left)';
                }

                statusEl.className = 'registration-status active';
                statusEl.textContent = statusText;
                disableBtn.style.display = 'inline-block';
            } else {
                statusEl.className = 'registration-status inactive';
                statusEl.textContent = 'Registration is closed';
                disableBtn.style.display = 'none';
            }
        }

        function escapeHtml(str) {
            const div = document.createElement('div');
            div.textContent = str;
            return div.innerHTML;
        }

        function base64ToBuffer(base64) {
            const binary = atob(base64.replace(/-/g, '+').replace(/_/g, '/'));
            const bytes = new Uint8Array(binary.length);
            for (let i = 0; i < binary.length; i++) {
                bytes[i] = binary.charCodeAt(i);
            }
            return bytes.buffer;
        }

        function bufferToBase64(buffer) {
            const bytes = new Uint8Array(buffer);
            let binary = '';
            for (let i = 0; i < bytes.length; i++) {
                binary += String.fromCharCode(bytes[i]);
            }
            return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=/g, '');
        }

        function toggleSection(header) {
            header.classList.toggle('expanded');
            header.nextElementSibling.classList.toggle('expanded');
            const toggle = header.querySelector('.section-toggle');
            toggle.textContent = header.classList.contains('expanded') ? '▲' : '▼';
        }

        function showToast(message, type = 'success') {
            const toast = document.getElementById('toast');
            toast.textContent = message;
            toast.className = 'toast ' + type + ' show';
            setTimeout(() => toast.classList.remove('show'), 3000);
        }

        function markChanged() {
            hasChanges = true;
            document.getElementById('unsavedIndicator').classList.add('show');
        }

        function clearChanged() {
            hasChanges = false;
            document.getElementById('unsavedIndicator').classList.remove('show');
        }

        function parseDuration(str) {
            if (!str) return 0;
            // Convert Go duration string to nanoseconds
            const match = str.match(/^(\d+(?:\.\d+)?)(ns|us|µs|ms|s|m|h)$/);
            if (!match) return parseInt(str) || 0;
            const [, num, unit] = match;
            const n = parseFloat(num);
            switch (unit) {
                case 'ns': return n;
                case 'us': case 'µs': return n * 1000;
                case 'ms': return n * 1000000;
                case 's': return n * 1000000000;
                case 'm': return n * 60000000000;
                case 'h': return n * 3600000000000;
                default: return n;
            }
        }

        function formatDuration(ns) {
            if (ns >= 3600000000000) return (ns / 3600000000000) + 'h';
            if (ns >= 60000000000) return (ns / 60000000000) + 'm';
            if (ns >= 1000000000) return (ns / 1000000000) + 's';
            if (ns >= 1000000) return (ns / 1000000) + 'ms';
            return ns + 'ns';
        }

        async function loadSettings() {
            try {
                document.body.classList.add('loading');
                const response = await fetch('/api/settings');
                if (!response.ok) throw new Error('Failed to load settings');
                const settings = await response.json();
                originalSettings = settings;
                populateForm(settings);
                clearChanged();

                // Load info
                const infoResponse = await fetch('/api/settings/info');
                if (infoResponse.ok) {
                    const info = await infoResponse.json();
                    updateStatus(info);
                }
            } catch (err) {
                showToast('Failed to load settings: ' + err.message, 'error');
            } finally {
                document.body.classList.remove('loading');
            }
        }

        function populateForm(settings) {
            // Trade Monitor
            setValue('tm_min_notional', settings.trade_monitor?.min_notional);
            setValue('tm_obvious_price', settings.trade_monitor?.obvious_price);
            setValue('tm_poll_interval', formatDuration(settings.trade_monitor?.poll_interval || 0));
            setValue('tm_high_win_rate', settings.trade_monitor?.high_win_rate_threshold);
            setValue('tm_min_resolved', settings.trade_monitor?.min_resolved_for_win_rate);
            setValue('tm_win_rate_max_entry', settings.trade_monitor?.win_rate_max_entry_price);
            setValue('tm_extreme_low_price', settings.trade_monitor?.extreme_low_price);
            setValue('tm_extreme_min_notional', settings.trade_monitor?.extreme_min_notional);
            setValue('tm_contrarian_max_price', settings.trade_monitor?.contrarian_max_price);
            setValue('tm_contrarian_min_notional', settings.trade_monitor?.contrarian_min_notional);
            setValue('tm_massive_min_notional', settings.trade_monitor?.massive_trade_min_notional);
            setValue('tm_massive_max_price', settings.trade_monitor?.massive_trade_max_price);
            setValue('tm_new_wallet_max_markets', settings.trade_monitor?.new_wallet_max_markets);
            setValue('tm_new_wallet_min_notional', settings.trade_monitor?.new_wallet_min_notional);
            setChecked('tm_use_websocket', settings.trade_monitor?.use_websocket);

            // Markets
            setValue('markets_top_count', settings.markets?.top_markets_count);
            setValue('markets_refresh_interval', formatDuration(settings.markets?.refresh_interval || 0));
            setChecked('markets_specific_only', settings.markets?.specific_markets_only);

            // Cache
            setValue('cache_wallet_ttl', formatDuration(settings.cache?.wallet_cache_ttl || 0));
            setValue('cache_save_interval', formatDuration(settings.cache?.save_interval || 0));

            // Health Server
            setValue('health_port', settings.health_server?.port);
            setChecked('health_enabled', settings.health_server?.enabled);
        }

        function setValue(id, value) {
            const el = document.getElementById(id);
            if (el && value !== undefined) el.value = value;
        }

        function setChecked(id, value) {
            const el = document.getElementById(id);
            if (el) el.checked = !!value;
        }

        function updateStatus(info) {
            const dot = document.getElementById('gistStatus');
            const text = document.getElementById('gistStatusText');
            if (info.gist_enabled) {
                dot.className = 'status-dot connected';
                text.textContent = 'Gist Connected';
            } else {
                dot.className = 'status-dot disconnected';
                text.textContent = 'Gist Not Configured';
            }
            document.getElementById('lastUpdated').textContent =
                info.last_updated ? new Date(info.last_updated).toLocaleString() : '-';
        }

        function collectFormData() {
            const data = {
                trade_monitor: {
                    min_notional: parseFloat(document.getElementById('tm_min_notional').value) || 0,
                    obvious_price: parseFloat(document.getElementById('tm_obvious_price').value) || 0,
                    poll_interval: parseDuration(document.getElementById('tm_poll_interval').value),
                    high_win_rate_threshold: parseFloat(document.getElementById('tm_high_win_rate').value) || 0,
                    min_resolved_for_win_rate: parseInt(document.getElementById('tm_min_resolved').value) || 0,
                    win_rate_max_entry_price: parseFloat(document.getElementById('tm_win_rate_max_entry').value) || 0,
                    extreme_low_price: parseFloat(document.getElementById('tm_extreme_low_price').value) || 0,
                    extreme_min_notional: parseFloat(document.getElementById('tm_extreme_min_notional').value) || 0,
                    contrarian_max_price: parseFloat(document.getElementById('tm_contrarian_max_price').value) || 0,
                    contrarian_min_notional: parseFloat(document.getElementById('tm_contrarian_min_notional').value) || 0,
                    massive_trade_min_notional: parseFloat(document.getElementById('tm_massive_min_notional').value) || 0,
                    massive_trade_max_price: parseFloat(document.getElementById('tm_massive_max_price').value) || 0,
                    new_wallet_max_markets: parseInt(document.getElementById('tm_new_wallet_max_markets').value) || 0,
                    new_wallet_min_notional: parseFloat(document.getElementById('tm_new_wallet_min_notional').value) || 0,
                    use_websocket: document.getElementById('tm_use_websocket').checked
                },
                markets: {
                    top_markets_count: parseInt(document.getElementById('markets_top_count').value) || 0,
                    refresh_interval: parseDuration(document.getElementById('markets_refresh_interval').value),
                    specific_markets_only: document.getElementById('markets_specific_only').checked
                },
                cache: {
                    wallet_cache_ttl: parseDuration(document.getElementById('cache_wallet_ttl').value),
                    save_interval: parseDuration(document.getElementById('cache_save_interval').value)
                },
                health_server: {
                    port: parseInt(document.getElementById('health_port').value) || 8080,
                    enabled: document.getElementById('health_enabled').checked
                }
            };
            return data;
        }

        async function saveSettings(e) {
            e.preventDefault();
            try {
                document.body.classList.add('loading');
                const data = collectFormData();
                const response = await fetch('/api/settings', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify(data)
                });
                const result = await response.json();
                if (!response.ok) {
                    if (result.errors) {
                        const errMsg = result.errors.map(e => e.field + ': ' + e.message).join(', ');
                        throw new Error(errMsg);
                    }
                    throw new Error('Failed to save');
                }
                showToast('Settings saved successfully!');
                clearChanged();
                loadSettings();
            } catch (err) {
                showToast('Failed to save: ' + err.message, 'error');
            } finally {
                document.body.classList.remove('loading');
            }
        }

        async function resetToDefaults() {
            if (!confirm('Are you sure you want to reset all settings to defaults?')) return;
            try {
                document.body.classList.add('loading');
                const response = await fetch('/api/settings/reset', { method: 'POST' });
                if (!response.ok) throw new Error('Failed to reset');
                showToast('Settings reset to defaults');
                loadSettings();
            } catch (err) {
                showToast('Failed to reset: ' + err.message, 'error');
            } finally {
                document.body.classList.remove('loading');
            }
        }

        // Track changes
        document.getElementById('settingsForm').addEventListener('input', markChanged);
        document.getElementById('settingsForm').addEventListener('change', markChanged);
        document.getElementById('settingsForm').addEventListener('submit', saveSettings);

        // Warn on leave with unsaved changes
        window.addEventListener('beforeunload', (e) => {
            if (hasChanges) {
                e.preventDefault();
                e.returnValue = '';
            }
        });

        // Initial load
        loadSettings();
        checkAuthStatus();
    </script>
</body>
</html>
`
