package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

// WebSocket upgrader for real-time stats
var wsUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// startHealthServer starts an HTTP server for health checks and stats.
func (r *Runner) startHealthServer(port int) {
	mux := http.NewServeMux()

	// Register auth routes if auth handler is available
	if r.authHandler != nil {
		r.authHandler.RegisterRoutes(mux)
	}

	// Register settings routes if settings manager is available
	if r.settingsManager != nil {
		settingsHandler := NewSettingsHandler(r.clients.Logger, r.settingsManager, r.authHandler)
		settingsHandler.RegisterRoutes(mux)
	}

	// Register tasks routes (only if tasks gist is configured)
	cfg := r.liveConfig.Get()
	tasksHandler := NewTasksHandler(r.clients.Logger, r.clients.Polymarket, r.authHandler, r.clients.Gist, cfg.Gist.TasksGistID)
	tasksEnabled := tasksHandler.IsEnabled()
	r.clients.Logger.Info("tasks feature status",
		zap.Bool("enabled", tasksEnabled),
		zap.Bool("hasGistID", cfg.Gist.TasksGistID != ""),
		zap.Bool("hasGistClient", r.clients.Gist != nil),
		zap.Bool("gistClientEnabled", r.clients.Gist != nil && r.clients.Gist.IsEnabled()),
	)
	if tasksEnabled {
		tasksHandler.RegisterRoutes(mux)
	}

	// Features endpoint - returns enabled features
	mux.HandleFunc("/api/features", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"tasks": tasksEnabled,
		})
	})

	// Health check endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	// JSON stats endpoint
	mux.HandleFunc("/stats", func(w http.ResponseWriter, _ *http.Request) {
		stats := r.GetStats()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(stats)
	})

	// WebSocket endpoint for real-time stats
	mux.HandleFunc("/ws", func(w http.ResponseWriter, req *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, req, nil)
		if err != nil {
			r.clients.Logger.Error("websocket upgrade failed", zap.Error(err))
			return
		}
		defer conn.Close()

		// Send stats every second
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				stats := r.GetStats()
				if err := conn.WriteJSON(stats); err != nil {
					return // Client disconnected
				}
			}
		}
	})

	// HTML dashboard
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(dashboardHTML))
	})

	r.healthServer = &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: mux,
	}

	go func() {
		if err := r.healthServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			r.clients.Logger.Error("health server error", zap.Error(err))
		}
	}()
}

const dashboardHTML = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Polybot Stats</title>
    <style>
        :root {
            --bg-primary: #0d1117;
            --bg-secondary: #161b22;
            --bg-tertiary: #21262d;
            --border-color: #30363d;
            --text-primary: #c9d1d9;
            --text-secondary: #8b949e;
            --text-heading: #f0f6fc;
            --accent-blue: #58a6ff;
            --accent-green: #3fb950;
            --accent-red: #f85149;
            --accent-yellow: #d29922;
            --accent-purple: #a371f7;
            --accent-orange: #f0883e;
        }
        .light-mode {
            --bg-primary: #ffffff;
            --bg-secondary: #f6f8fa;
            --bg-tertiary: #eaeef2;
            --border-color: #d0d7de;
            --text-primary: #24292f;
            --text-secondary: #57606a;
            --text-heading: #1f2328;
            --accent-blue: #0969da;
            --accent-green: #1a7f37;
            --accent-red: #cf222e;
            --accent-yellow: #9a6700;
            --accent-purple: #8250df;
            --accent-orange: #bc4c00;
        }
        * { box-sizing: border-box; margin: 0; padding: 0; }
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, monospace;
            background: var(--bg-primary);
            color: var(--text-primary);
            padding: 20px;
            line-height: 1.5;
            transition: background 0.3s, color 0.3s;
        }
        h1 { color: var(--accent-blue); margin-bottom: 20px; font-size: 24px; }
        h2 { color: var(--text-secondary); font-size: 14px; text-transform: uppercase; margin: 20px 0 10px; letter-spacing: 1px; }
        .grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(300px, 1fr)); gap: 20px; }
        .card {
            background: var(--bg-secondary);
            border: 1px solid var(--border-color);
            border-radius: 8px;
            padding: 16px;
            transition: background 0.3s, border-color 0.3s;
        }
        .card h3 { color: var(--accent-blue); font-size: 16px; margin-bottom: 12px; }
        .stat-row { display: flex; justify-content: space-between; padding: 6px 0; border-bottom: 1px solid var(--bg-tertiary); }
        .stat-row:last-child { border-bottom: none; }
        .stat-label { color: var(--text-secondary); }
        .stat-value { color: var(--text-heading); font-weight: 600; }
        .stat-value.green { color: var(--accent-green); }
        .stat-value.red { color: var(--accent-red); }
        .stat-value.yellow { color: var(--accent-yellow); }
        .stat-value.blue { color: var(--accent-blue); }
        .stat-value.purple { color: var(--accent-purple); }
        .connected { color: var(--accent-green); }
        .disconnected { color: var(--accent-red); }
        .uptime { font-size: 32px; color: var(--accent-blue); margin: 10px 0; }
        .header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 20px; flex-wrap: wrap; gap: 10px; }
        .header-controls { display: flex; align-items: center; gap: 15px; }
        .status { display: flex; align-items: center; gap: 8px; }
        .status-dot { width: 10px; height: 10px; border-radius: 50%; }
        .status-dot.connected { background: var(--accent-green); }
        .status-dot.disconnected { background: var(--accent-red); animation: blink 1s infinite; }
        @keyframes blink { 50% { opacity: 0.5; } }
        .alerts-grid { display: grid; grid-template-columns: repeat(2, 1fr); gap: 8px; }
        .alert-item { background: var(--bg-tertiary); padding: 8px 12px; border-radius: 4px; transition: background 0.3s; }
        .alert-count { font-size: 20px; font-weight: bold; }
        .feed-item { background: var(--bg-tertiary); padding: 12px; border-radius: 6px; margin-bottom: 8px; border-left: 3px solid var(--accent-blue); transition: background 0.3s; }
        .feed-item.severity-high { border-left-color: var(--accent-red); }
        .feed-item.severity-medium { border-left-color: var(--accent-yellow); }
        .feed-time { color: var(--text-secondary); font-size: 12px; }
        .feed-wallet { color: var(--accent-blue); font-weight: 600; }
        .feed-market { color: var(--text-primary); font-size: 14px; }
        .feed-reasons { display: flex; gap: 4px; flex-wrap: wrap; margin-top: 6px; }
        .reason-tag { background: #388bfd33; color: var(--accent-blue); padding: 2px 8px; border-radius: 4px; font-size: 11px; }
        .market-list { max-height: 200px; overflow-y: auto; }
        .market-item { padding: 6px 0; border-bottom: 1px solid var(--bg-tertiary); font-size: 14px; }
        .market-item:last-child { border-bottom: none; }
        .wallet-row { display: flex; justify-content: space-between; padding: 8px 0; border-bottom: 1px solid var(--bg-tertiary); }
        .wallet-row:last-child { border-bottom: none; }
        .wallet-addr { font-family: monospace; color: var(--accent-blue); font-size: 13px; }
        .wallet-count { font-weight: bold; color: var(--accent-green); }
        .status-badge { display: inline-flex; align-items: center; gap: 4px; padding: 4px 10px; border-radius: 12px; font-size: 12px; }
        .status-badge.enabled { background: #238636; color: #fff; }
        .status-badge.disabled { background: #6e7681; color: #fff; }
        .alert-meta { display: flex; gap: 20px; margin-bottom: 12px; padding-bottom: 12px; border-bottom: 1px solid var(--border-color); flex-wrap: wrap; }
        .alert-meta-item { text-align: center; }
        .alert-meta-value { font-size: 24px; font-weight: bold; color: var(--accent-blue); }
        .alert-meta-label { font-size: 12px; color: var(--text-secondary); }
        .footer { margin-top: 30px; padding: 20px; text-align: center; border-top: 1px solid var(--border-color); color: var(--text-secondary); font-size: 13px; }
        .footer a { color: var(--accent-blue); text-decoration: none; }
        .footer a:hover { text-decoration: underline; }
        .build-info { display: flex; justify-content: center; gap: 20px; flex-wrap: wrap; }
        /* Theme toggle */
        .theme-toggle { background: var(--bg-tertiary); border: 1px solid var(--border-color); border-radius: 20px; padding: 5px 12px; cursor: pointer; font-size: 14px; color: var(--text-primary); transition: all 0.3s; }
        .theme-toggle:hover { border-color: var(--accent-blue); }
        /* Sparkline */
        .sparkline { height: 40px; display: flex; align-items: flex-end; gap: 2px; }
        .sparkline-bar { background: var(--accent-blue); border-radius: 2px 2px 0 0; min-width: 8px; transition: height 0.3s; }
        /* Timeline chart */
        .timeline { height: 60px; display: flex; align-items: flex-end; gap: 1px; padding: 10px 0; }
        .timeline-bar { background: var(--accent-blue); border-radius: 2px 2px 0 0; flex: 1; min-height: 2px; transition: height 0.3s; position: relative; }
        .timeline-bar:hover { background: var(--accent-green); }
        .timeline-bar:hover::after { content: attr(data-count); position: absolute; bottom: 100%; left: 50%; transform: translateX(-50%); background: var(--bg-tertiary); padding: 2px 6px; border-radius: 4px; font-size: 11px; white-space: nowrap; }
        .timeline-labels { display: flex; justify-content: space-between; font-size: 10px; color: var(--text-secondary); margin-top: 4px; }
        /* Heuristic chart */
        .heuristic-chart { display: flex; flex-direction: column; gap: 6px; }
        .heuristic-bar-row { display: flex; align-items: center; gap: 8px; }
        .heuristic-label { width: 100px; font-size: 11px; color: var(--text-secondary); text-align: right; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
        .heuristic-bar-container { flex: 1; height: 16px; background: var(--bg-tertiary); border-radius: 3px; overflow: hidden; }
        .heuristic-bar { height: 100%; border-radius: 3px; transition: width 0.5s; }
        .heuristic-count { width: 30px; font-size: 12px; font-weight: bold; }
        /* Search and filter */
        .search-box { display: flex; gap: 10px; margin-bottom: 15px; flex-wrap: wrap; }
        .search-input { background: var(--bg-tertiary); border: 1px solid var(--border-color); border-radius: 6px; padding: 8px 12px; color: var(--text-primary); font-size: 14px; flex: 1; min-width: 200px; }
        .search-input:focus { outline: none; border-color: var(--accent-blue); }
        .filter-select { background: var(--bg-tertiary); border: 1px solid var(--border-color); border-radius: 6px; padding: 8px 12px; color: var(--text-primary); font-size: 14px; cursor: pointer; }
        /* Export button */
        .export-btn { background: var(--bg-tertiary); border: 1px solid var(--border-color); border-radius: 6px; padding: 8px 16px; color: var(--text-primary); cursor: pointer; font-size: 13px; transition: all 0.2s; }
        .export-btn:hover { border-color: var(--accent-blue); background: var(--accent-blue); color: white; }
        /* Time period tabs */
        .time-tabs { display: flex; gap: 5px; margin-bottom: 10px; }
        .time-tab { background: var(--bg-tertiary); border: 1px solid var(--border-color); border-radius: 4px; padding: 4px 12px; cursor: pointer; font-size: 12px; color: var(--text-secondary); transition: all 0.2s; }
        .time-tab.active { background: var(--accent-blue); border-color: var(--accent-blue); color: white; }
        /* Keyboard shortcuts modal */
        .shortcuts-modal { display: none; position: fixed; top: 0; left: 0; right: 0; bottom: 0; background: rgba(0,0,0,0.7); z-index: 1000; align-items: center; justify-content: center; }
        .shortcuts-modal.show { display: flex; }
        .shortcuts-content { background: var(--bg-secondary); border: 1px solid var(--border-color); border-radius: 12px; padding: 24px; max-width: 400px; width: 90%; }
        .shortcuts-content h3 { color: var(--accent-blue); margin-bottom: 16px; }
        .shortcut-row { display: flex; justify-content: space-between; padding: 8px 0; border-bottom: 1px solid var(--bg-tertiary); }
        .shortcut-key { background: var(--bg-tertiary); padding: 2px 8px; border-radius: 4px; font-family: monospace; }
        /* Watchlist */
        .watchlist-input { display: flex; gap: 8px; margin-bottom: 10px; }
        .watchlist-input input { flex: 1; }
        .watchlist-btn { background: var(--accent-blue); border: none; border-radius: 6px; padding: 8px 16px; color: white; cursor: pointer; font-size: 13px; }
        .watchlist-item { display: flex; justify-content: space-between; align-items: center; padding: 8px; background: var(--bg-tertiary); border-radius: 4px; margin-bottom: 4px; }
        .watchlist-remove { background: none; border: none; color: var(--accent-red); cursor: pointer; font-size: 16px; }
        /* Alert expansion */
        .expand-icon { font-size: 10px; color: var(--text-secondary); transition: transform 0.2s; }
        .alert-details { display: none; margin-top: 12px; padding-top: 12px; border-top: 1px solid var(--border-color); }
        .alert-details.expanded { display: block; }
        .alert-details-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(150px, 1fr)); gap: 12px; }
        .detail-section { background: var(--bg-primary); padding: 10px; border-radius: 6px; }
        .detail-header { font-size: 11px; text-transform: uppercase; color: var(--text-secondary); margin-bottom: 8px; letter-spacing: 0.5px; }
        .detail-row { display: flex; justify-content: space-between; font-size: 13px; padding: 3px 0; }
        .detail-row span:first-child { color: var(--text-secondary); }
        .detail-row span:last-child { color: var(--text-primary); font-weight: 500; }
        .detail-row .green { color: var(--accent-green); }
        .detail-links { display: flex; gap: 10px; margin-top: 12px; }
        .detail-link { background: var(--bg-primary); color: var(--accent-blue); padding: 6px 12px; border-radius: 4px; font-size: 12px; text-decoration: none; transition: background 0.2s; }
        .detail-link:hover { background: var(--accent-blue); color: white; }
    </style>
</head>
<body>
    <div class="header">
        <h1>🤖 Polybot Dashboard</h1>
        <div class="header-controls">
            <a href="/tasks" id="tasksBtn" class="theme-toggle" style="text-decoration: none; display: none;" title="Tasks">📋 Tasks</a>
            <a href="/settings" class="theme-toggle" style="text-decoration: none;" title="Settings">⚙️ Settings</a>
            <button class="theme-toggle" onclick="toggleTheme()" title="Toggle theme (T)">🌙 Dark</button>
            <button class="export-btn" onclick="exportAlerts('json')" title="Export alerts (E)">📥 Export</button>
            <button class="theme-toggle" onclick="showShortcuts()" title="Keyboard shortcuts (?)">⌨️</button>
            <div class="status">
                <div id="wsDot" class="status-dot disconnected"></div>
                <span id="wsStatus">Connecting...</span>
            </div>
        </div>
    </div>

    <!-- Keyboard shortcuts modal -->
    <div id="shortcutsModal" class="shortcuts-modal" onclick="hideShortcuts()">
        <div class="shortcuts-content" onclick="event.stopPropagation()">
            <h3>⌨️ Keyboard Shortcuts</h3>
            <div class="shortcut-row"><span>Toggle theme</span><span class="shortcut-key">T</span></div>
            <div class="shortcut-row"><span>Export alerts</span><span class="shortcut-key">E</span></div>
            <div class="shortcut-row"><span>Focus search</span><span class="shortcut-key">/</span></div>
            <div class="shortcut-row"><span>Clear search</span><span class="shortcut-key">Esc</span></div>
            <div class="shortcut-row"><span>Show shortcuts</span><span class="shortcut-key">?</span></div>
            <div class="shortcut-row"><span>Scroll to top</span><span class="shortcut-key">Home</span></div>
        </div>
    </div>

    <div class="grid" style="margin-bottom: 20px;">
        <div class="card">
            <div class="stat-row">
                <span class="stat-label">Started</span>
                <span id="startTime" class="stat-value">-</span>
            </div>
            <div class="stat-row">
                <span class="stat-label">Uptime</span>
                <span id="uptime" class="stat-value blue" style="font-size: 24px;">-</span>
            </div>
        </div>

        <div class="card">
            <h3>📈 Activity (Last Hour)</h3>
            <div class="sparkline" id="sparkline"></div>
            <div class="time-tabs" style="margin-top: 12px;">
                <span class="time-tab active" id="tab1h" onclick="setTimePeriod('1h')">1h: <span id="alerts1h">-</span></span>
                <span class="time-tab" id="tab24h" onclick="setTimePeriod('24h')">24h: <span id="alerts24h">-</span></span>
                <span class="time-tab" id="tab7d" onclick="setTimePeriod('7d')">7d: <span id="alerts7d">-</span></span>
            </div>
        </div>
    </div>

    <div class="grid" style="margin-bottom: 20px;">
        <div class="card" style="grid-column: span 2;">
            <h3>📜 Recent Alerts</h3>
            <div class="search-box">
                <input type="text" id="alertSearch" class="search-input" placeholder="Search wallet or market..." onkeyup="filterAlerts()">
                <select id="alertFilter" class="filter-select" onchange="filterAlerts()">
                    <option value="">All Heuristics</option>
                    <option value="high_win_rate">High Win Rate</option>
                    <option value="massive_trade">Massive Trade</option>
                    <option value="contrarian_winner">Contrarian Winner</option>
                    <option value="rapid_trading">Rapid Trading</option>
                    <option value="new_wallet">New Wallet</option>
                    <option value="contrarian_bet">Contrarian Bet</option>
                    <option value="extreme_bet">Extreme Bet</option>
                    <option value="low_activity">Low Activity</option>
                    <option value="copy_trader">Copy Trader</option>
                    <option value="hedge_removal">Hedge Removal</option>
                    <option value="perfect_exit_timing">Perfect Exit</option>
                    <option value="stealth_accumulation">Stealth Accum</option>
                    <option value="conviction_doubling">Conviction Dbl</option>
                    <option value="asymmetric_exit">Asymmetric Exit</option>
                </select>
            </div>
            <div id="recentAlerts">
                <div style="color: var(--text-secondary); text-align: center; padding: 20px;">No alerts yet</div>
            </div>
        </div>
    </div>

    <h2>🚨 Alerts Sent</h2>
    <div class="card">
        <div class="alert-meta">
            <div class="alert-meta-item">
                <div id="alertTotal" class="alert-meta-value green">-</div>
                <div class="alert-meta-label">Total Alerts</div>
            </div>
            <div class="alert-meta-item">
                <div id="alertRate" class="alert-meta-value">-</div>
                <div class="alert-meta-label">Per Hour</div>
            </div>
            <div class="alert-meta-item">
                <div id="lastAlertAgo" class="alert-meta-value">-</div>
                <div class="alert-meta-label">Last Alert</div>
            </div>
        </div>
        <div class="alerts-grid">
            <div class="alert-item"><span class="stat-label">High Win Rate</span><br><span id="alertWinRate" class="alert-count green">-</span></div>
            <div class="alert-item"><span class="stat-label">Massive Trade</span><br><span id="alertMassive" class="alert-count yellow">-</span></div>
            <div class="alert-item"><span class="stat-label">Rapid Trading</span><br><span id="alertRapid" class="alert-count blue">-</span></div>
            <div class="alert-item"><span class="stat-label">New Wallet</span><br><span id="alertNew" class="alert-count">-</span></div>
            <div class="alert-item"><span class="stat-label">Contrarian Bet</span><br><span id="alertContrarian" class="alert-count">-</span></div>
            <div class="alert-item"><span class="stat-label">Contrarian Winner</span><br><span id="alertContrarianWin" class="alert-count green">-</span></div>
            <div class="alert-item"><span class="stat-label">Extreme Bet</span><br><span id="alertExtreme" class="alert-count red">-</span></div>
            <div class="alert-item"><span class="stat-label">Low Activity</span><br><span id="alertLow" class="alert-count">-</span></div>
            <div class="alert-item"><span class="stat-label">Copy Trader</span><br><span id="alertCopy" class="alert-count">-</span></div>
            <div class="alert-item"><span class="stat-label">Hedge Removal</span><br><span id="alertHedge" class="alert-count">-</span></div>
            <div class="alert-item"><span class="stat-label">Perfect Exit</span><br><span id="alertPerfect" class="alert-count green">-</span></div>
            <div class="alert-item"><span class="stat-label">Stealth Accum</span><br><span id="alertStealth" class="alert-count yellow">-</span></div>
            <div class="alert-item"><span class="stat-label">Conviction Dbl</span><br><span id="alertConviction" class="alert-count">-</span></div>
            <div class="alert-item"><span class="stat-label">Asymmetric Exit</span><br><span id="alertAsym" class="alert-count">-</span></div>
        </div>
    </div>

    <div class="grid" style="margin-top: 20px;">
        <div class="card">
            <h3>📊 24h Timeline</h3>
            <div class="timeline" id="timeline"></div>
            <div class="timeline-labels">
                <span>24h ago</span>
                <span>12h ago</span>
                <span>Now</span>
            </div>
        </div>
        <div class="card">
            <h3>📉 Heuristic Breakdown</h3>
            <div class="heuristic-chart" id="heuristicChart"></div>
        </div>
    </div>

    <div class="grid" style="margin-top: 20px;">
        <div class="card">
            <h3>🏆 Top Alerting Wallets</h3>
            <div id="topWallets">
                <div style="color: var(--text-secondary); text-align: center; padding: 20px;">No alerts yet</div>
            </div>
        </div>
        <div class="card">
            <h3>🏅 Top Alerting Markets</h3>
            <div id="topMarkets">
                <div style="color: var(--text-secondary); text-align: center; padding: 20px;">No alerts yet</div>
            </div>
        </div>
    </div>

    <div class="card" style="margin-top: 20px;">
        <h3>👁️ Wallet Watchlist</h3>
        <div class="watchlist-input">
            <input type="text" id="watchlistInput" class="search-input" placeholder="Add wallet address to watch..." onkeypress="if(event.key==='Enter')addToWatchlist()">
            <button class="watchlist-btn" onclick="addToWatchlist()">+ Add</button>
        </div>
        <div id="watchlistItems"></div>
        <div style="color: var(--text-secondary); font-size: 12px; margin-top: 8px;">Watchlist is saved in your browser's local storage.</div>
    </div>

    <h2>📡 System Status</h2>
    <div class="grid">
        <div class="card">
            <h3 id="dataSourceTitle">📡 Data Source</h3>
            <div class="stat-row">
                <span class="stat-label">Mode</span>
                <span id="wsMode" class="stat-value">-</span>
            </div>
            <div class="stat-row" id="wsConnectedRow">
                <span class="stat-label">Connected</span>
                <span id="wsConnected" class="stat-value">-</span>
            </div>
            <div class="stat-row" id="msgCountRow">
                <span class="stat-label">Messages Received</span>
                <span id="msgCount" class="stat-value">-</span>
            </div>
            <div class="stat-row" id="lastMsgRow">
                <span class="stat-label">Last Message</span>
                <span id="lastMsg" class="stat-value">-</span>
            </div>
            <div class="stat-row">
                <span class="stat-label">Trades Seen</span>
                <span id="tradesWS" class="stat-value">-</span>
            </div>
            <div class="stat-row">
                <span class="stat-label">Markets Seen</span>
                <span id="marketsWS" class="stat-value">-</span>
            </div>
        </div>

        <div class="card">
            <h3>📊 Markets</h3>
            <div class="stat-row">
                <span class="stat-label">Monitored Markets</span>
                <span id="marketCount" class="stat-value">-</span>
            </div>
            <div class="stat-row">
                <span class="stat-label">Token IDs</span>
                <span id="tokenCount" class="stat-value">-</span>
            </div>
            <div class="stat-row">
                <span class="stat-label">Top Volume (24h)</span>
                <span id="topVolume" class="stat-value">-</span>
            </div>
            <div id="marketListContainer" style="margin-top: 12px; display: none;">
                <div style="color: #8b949e; font-size: 12px; margin-bottom: 6px;">MONITORED:</div>
                <div id="marketList" class="market-list"></div>
            </div>
        </div>

        <div class="card">
            <h3>📢 Notifications</h3>
            <div class="stat-row" style="cursor: pointer;" onclick="toggleNotifDetails('discord')">
                <span class="stat-label">Discord <span id="discordExpand" style="font-size: 10px;">▶</span></span>
                <span id="discordStatus" class="status-badge disabled">-</span>
            </div>
            <div id="discordDetails" style="display: none; padding: 8px 0 8px 16px; border-left: 2px solid #58a6ff; margin: 4px 0;">
                <div style="font-size: 12px; color: #8b949e;">Channel ID:</div>
                <div id="discordChannelID" style="font-family: monospace; font-size: 13px; color: #58a6ff;">-</div>
            </div>
            <div class="stat-row" style="cursor: pointer;" onclick="toggleNotifDetails('telegram')">
                <span class="stat-label">Telegram <span id="telegramExpand" style="font-size: 10px;">▶</span></span>
                <span id="telegramStatus" class="status-badge disabled">-</span>
            </div>
            <div id="telegramDetails" style="display: none; padding: 8px 0 8px 16px; border-left: 2px solid #58a6ff; margin: 4px 0;">
                <div style="font-size: 12px; color: #8b949e;">Chat ID:</div>
                <div id="telegramChatID" style="font-family: monospace; font-size: 13px; color: #58a6ff;">-</div>
            </div>
        </div>

        <div class="card">
            <h3>🔍 Filters</h3>
            <div class="stat-row">
                <span class="stat-label">Skipped (Low Notional)</span>
                <span id="skipLow" class="stat-value">-</span>
            </div>
            <div class="stat-row">
                <span class="stat-label">Skipped (No Wallet)</span>
                <span id="skipNoWallet" class="stat-value">-</span>
            </div>
            <div class="stat-row">
                <span class="stat-label">Skipped (High Activity)</span>
                <span id="skipHigh" class="stat-value">-</span>
            </div>
            <div class="stat-row">
                <span class="stat-label">Skipped (Obvious)</span>
                <span id="skipObvious" class="stat-value">-</span>
            </div>
        </div>

        <div class="card">
            <h3>💾 Caches</h3>
            <div class="stat-row">
                <span class="stat-label">Wallet Cache</span>
                <span id="walletCache" class="stat-value">-</span>
            </div>
            <div class="stat-row">
                <span class="stat-label">Contrarian Cache</span>
                <span id="contrarianCache" class="stat-value">-</span>
            </div>
            <div class="stat-row">
                <span class="stat-label">Seen Trades</span>
                <span id="seenTrades" class="stat-value">-</span>
            </div>
        </div>

        <div class="card">
            <h3>🎯 Trackers</h3>
            <div class="stat-row">
                <span class="stat-label">Copy: Leader Trades</span>
                <span id="copyLeader" class="stat-value">-</span>
            </div>
            <div class="stat-row">
                <span class="stat-label">Copy: Followers</span>
                <span id="copyFollowers" class="stat-value">-</span>
            </div>
            <div class="stat-row">
                <span class="stat-label">Hedge: Wallets</span>
                <span id="hedgeWallets" class="stat-value">-</span>
            </div>
            <div class="stat-row">
                <span class="stat-label">Pattern: Pending</span>
                <span id="patternPending" class="stat-value">-</span>
            </div>
            <div class="stat-row">
                <span class="stat-label">Pattern: Verified</span>
                <span id="patternVerified" class="stat-value">-</span>
            </div>
        </div>
    </div>

    <h2>⚙️ Runtime</h2>
    <div class="grid">
        <div class="card">
            <h3>💾 Memory</h3>
            <div class="stat-row">
                <span class="stat-label">Heap Allocated</span>
                <span id="heapAlloc" class="stat-value">-</span>
            </div>
            <div class="stat-row">
                <span class="stat-label">Heap In Use</span>
                <span id="heapInuse" class="stat-value">-</span>
            </div>
            <div class="stat-row">
                <span class="stat-label">Stack In Use</span>
                <span id="stackInuse" class="stat-value">-</span>
            </div>
            <div class="stat-row">
                <span class="stat-label">Heap System</span>
                <span id="heapSys" class="stat-value">-</span>
            </div>
        </div>

        <div class="card">
            <h3>🔧 Process</h3>
            <div class="stat-row">
                <span class="stat-label">Goroutines</span>
                <span id="goroutines" class="stat-value">-</span>
            </div>
            <div class="stat-row">
                <span class="stat-label">GC Cycles</span>
                <span id="numGC" class="stat-value">-</span>
            </div>
            <div class="stat-row">
                <span class="stat-label">Last GC</span>
                <span id="lastGC" class="stat-value">-</span>
            </div>
        </div>

        <div class="card">
            <h3>🖥️ System</h3>
            <div class="stat-row">
                <span class="stat-label">Go Version</span>
                <span id="goVersion" class="stat-value">-</span>
            </div>
            <div class="stat-row">
                <span class="stat-label">CPUs</span>
                <span id="numCPU" class="stat-value">-</span>
            </div>
            <div class="stat-row">
                <span class="stat-label">OS / Arch</span>
                <span id="osArch" class="stat-value">-</span>
            </div>
        </div>
    </div>

    <script>
        // Check enabled features and show/hide UI elements
        fetch('/api/features')
            .then(r => r.json())
            .then(features => {
                if (features.tasks) {
                    document.getElementById('tasksBtn').style.display = '';
                }
            })
            .catch(err => console.error('Failed to fetch features:', err));

        function connect() {
            const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
            const wsUrl = protocol + '//' + window.location.host + '/ws';
            console.log('Connecting to WebSocket:', wsUrl);

            let ws;
            try {
                ws = new WebSocket(wsUrl);
            } catch (e) {
                console.error('WebSocket creation failed:', e);
                document.getElementById('wsStatus').textContent = 'Error: ' + e.message;
                setTimeout(connect, 5000);
                return;
            }

            const dot = document.getElementById('wsDot');
            const status = document.getElementById('wsStatus');

            ws.onopen = () => {
                console.log('WebSocket connected');
                dot.className = 'status-dot connected';
                status.textContent = 'Live';
                status.className = 'connected';
            };

            ws.onclose = (e) => {
                console.log('WebSocket closed:', e.code, e.reason);
                dot.className = 'status-dot disconnected';
                status.textContent = 'Reconnecting...';
                status.className = 'disconnected';
                setTimeout(connect, 2000);
            };

            ws.onerror = (e) => {
                console.error('WebSocket error:', e);
                ws.close();
            };

            ws.onmessage = (e) => {
                const s = JSON.parse(e.data);

                // Build info
                const commitHash = s.build.commit || 'dev';
                const shortCommit = commitHash.length > 7 ? commitHash.substring(0, 7) : commitHash;
                document.getElementById('commitHash').textContent = shortCommit;
                document.getElementById('commitLink').href = 'https://github.com/swiftyspiffy/polybot/commit/' + commitHash;
                document.getElementById('buildGoVersion').textContent = s.build.go_version || '-';

                // Service info
                document.getElementById('startTime').textContent = new Date(s.start_time).toLocaleString();
                document.getElementById('uptime').textContent = s.uptime;

                // Data Source (WebSocket or Polling)
                const wsEnabled = s.websocket.enabled;
                document.getElementById('wsMode').textContent = wsEnabled ? '📡 WebSocket' : '🔄 Polling';
                document.getElementById('wsMode').className = 'stat-value ' + (wsEnabled ? 'blue' : 'yellow');

                // Show/hide WebSocket-specific rows
                document.getElementById('wsConnectedRow').style.display = wsEnabled ? '' : 'none';
                document.getElementById('msgCountRow').style.display = wsEnabled ? '' : 'none';
                document.getElementById('lastMsgRow').style.display = wsEnabled ? '' : 'none';

                if (wsEnabled) {
                    document.getElementById('wsConnected').textContent = s.websocket.connected ? '✅ Yes' : '❌ No';
                    document.getElementById('wsConnected').className = 'stat-value ' + (s.websocket.connected ? 'green' : 'red');
                    document.getElementById('msgCount').textContent = s.websocket.message_count.toLocaleString();
                    document.getElementById('lastMsg').textContent = s.websocket.last_message_ago || 'N/A';
                }
                document.getElementById('tradesWS').textContent = s.websocket.trades_seen_via_ws.toLocaleString();
                document.getElementById('marketsWS').textContent = s.websocket.markets_seen_via_ws.toLocaleString();

                // Markets
                document.getElementById('marketCount').textContent = s.markets.count;
                document.getElementById('tokenCount').textContent = s.markets.token_count;
                document.getElementById('topVolume').textContent = '$' + (s.markets.top_volume_24h || 0).toLocaleString(undefined, {maximumFractionDigits: 0});

                // Market names list
                if (s.market_names && s.market_names.length > 0) {
                    document.getElementById('marketListContainer').style.display = 'block';
                    document.getElementById('marketList').innerHTML = s.market_names
                        .map(name => '<div class="market-item">' + name.substring(0, 60) + (name.length > 60 ? '...' : '') + '</div>')
                        .join('');
                }

                // Notification status
                const discordEl = document.getElementById('discordStatus');
                const telegramEl = document.getElementById('telegramStatus');
                if (s.notifications.discord_enabled) {
                    discordEl.textContent = '✓ Enabled';
                    discordEl.className = 'status-badge enabled';
                    document.getElementById('discordChannelID').textContent = s.notifications.discord_channel_id || 'N/A';
                } else {
                    discordEl.textContent = '✗ Disabled';
                    discordEl.className = 'status-badge disabled';
                }
                if (s.notifications.telegram_enabled) {
                    telegramEl.textContent = '✓ Enabled';
                    telegramEl.className = 'status-badge enabled';
                    document.getElementById('telegramChatID').textContent = s.notifications.telegram_chat_id || 'N/A';
                } else {
                    telegramEl.textContent = '✗ Disabled';
                    telegramEl.className = 'status-badge disabled';
                }

                // Filters
                document.getElementById('skipLow').textContent = s.filters.skipped_low_notional.toLocaleString();
                document.getElementById('skipNoWallet').textContent = s.filters.skipped_no_wallet.toLocaleString();
                document.getElementById('skipHigh').textContent = s.filters.skipped_high_activity.toLocaleString();
                document.getElementById('skipObvious').textContent = s.filters.skipped_obvious.toLocaleString();

                // Caches
                document.getElementById('walletCache').textContent = s.caches.wallet_cache_size.toLocaleString();
                document.getElementById('contrarianCache').textContent = s.caches.contrarian_cache_size.toLocaleString();
                document.getElementById('seenTrades').textContent = s.caches.seen_trades_size.toLocaleString();

                // Trackers
                document.getElementById('copyLeader').textContent = s.trackers.copy_tracker.leader_trades;
                document.getElementById('copyFollowers').textContent = s.trackers.copy_tracker.tracked_followers;
                document.getElementById('hedgeWallets').textContent = s.trackers.hedge_tracker.hedged_wallets;
                document.getElementById('patternPending').textContent = s.trackers.pattern_tracker.pending_exits;
                document.getElementById('patternVerified').textContent = s.trackers.pattern_tracker.verified_wallets;

                // Alerts
                document.getElementById('alertTotal').textContent = s.alerts.total.toLocaleString();
                document.getElementById('alertRate').textContent = s.alert_rate.toFixed(1);
                document.getElementById('lastAlertAgo').textContent = s.last_alert_ago || 'Never';
                document.getElementById('alertWinRate').textContent = s.alerts.high_win_rate;
                document.getElementById('alertMassive').textContent = s.alerts.massive_trade;
                document.getElementById('alertRapid').textContent = s.alerts.rapid_trading;
                document.getElementById('alertNew').textContent = s.alerts.new_wallet;
                document.getElementById('alertContrarian').textContent = s.alerts.contrarian_bet;
                document.getElementById('alertContrarianWin').textContent = s.alerts.contrarian_winner;
                document.getElementById('alertExtreme').textContent = s.alerts.extreme_bet;
                document.getElementById('alertLow').textContent = s.alerts.low_activity;
                document.getElementById('alertCopy').textContent = s.alerts.copy_trader;
                document.getElementById('alertHedge').textContent = s.alerts.hedge_removal;
                document.getElementById('alertPerfect').textContent = s.alerts.perfect_exit_timing;
                document.getElementById('alertStealth').textContent = s.alerts.stealth_accumulation;
                document.getElementById('alertConviction').textContent = s.alerts.conviction_doubling;
                document.getElementById('alertAsym').textContent = s.alerts.asymmetric_exit;

                // Time-based alert counts
                document.getElementById('alerts1h').textContent = s.alerts_last_hour || 0;
                document.getElementById('alerts24h').textContent = s.alerts_last_24h || 0;
                document.getElementById('alerts7d').textContent = s.alerts_last_7d || 0;

                // Store sparkline data for all periods
                window.sparklineData = {
                    '1h': s.alert_sparkline || [],
                    '24h': s.alert_timeline || [],
                    '7d': s.alert_sparkline_7d || []
                };

                // Render sparkline based on selected period
                const activePeriod = window.selectedTimePeriod || '1h';
                renderSparkline(window.sparklineData[activePeriod]);

                // Timeline (always shows 24h)
                renderTimeline(s.alert_timeline || []);

                // Heuristic breakdown chart
                renderHeuristicChart(s.alerts);

                // Recent alerts feed (store for filtering)
                window.currentAlerts = s.recent_alerts || [];
                renderAlerts(window.currentAlerts);

                // Top alerting wallets
                const topWalletsEl = document.getElementById('topWallets');
                if (s.top_wallets && s.top_wallets.length > 0) {
                    topWalletsEl.innerHTML = s.top_wallets.map((w, i) => {
                        const shortAddr = w.address.substring(0, 8) + '...' + w.address.substring(w.address.length - 6);
                        const profileUrl = 'https://polymarket.com/profile/' + w.address;
                        const medal = i === 0 ? '🥇 ' : i === 1 ? '🥈 ' : i === 2 ? '🥉 ' : (i + 1) + '. ';
                        const inWatchlist = getWatchlist().includes(w.address.toLowerCase());
                        const watchIcon = inWatchlist ? ' 👁️' : '';
                        return '<div class="wallet-row">' +
                            '<a href="' + profileUrl + '" target="_blank" class="wallet-addr" style="text-decoration: none;">' + medal + shortAddr + watchIcon + ' ↗</a>' +
                            '<span class="wallet-count">' + w.count + ' alerts</span>' +
                            '</div>';
                    }).join('');
                } else {
                    topWalletsEl.innerHTML = '<div style="color: var(--text-secondary); text-align: center; padding: 20px;">No alerts yet</div>';
                }

                // Top alerting markets
                const topMarketsEl = document.getElementById('topMarkets');
                if (s.top_markets && s.top_markets.length > 0) {
                    topMarketsEl.innerHTML = s.top_markets.map((m, i) => {
                        const medal = i === 0 ? '🥇 ' : i === 1 ? '🥈 ' : i === 2 ? '🥉 ' : (i + 1) + '. ';
                        const title = m.title ? m.title.substring(0, 50) + (m.title.length > 50 ? '...' : '') : 'Unknown';
                        return '<div class="wallet-row">' +
                            '<span class="stat-label">' + medal + title + '</span>' +
                            '<span class="wallet-count">' + m.count + ' alerts</span>' +
                            '</div>';
                    }).join('');
                } else {
                    topMarketsEl.innerHTML = '<div style="color: var(--text-secondary); text-align: center; padding: 20px;">No alerts yet</div>';
                }

                // Runtime
                const formatBytes = (bytes) => {
                    if (bytes < 1024) return bytes + ' B';
                    if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB';
                    if (bytes < 1024 * 1024 * 1024) return (bytes / (1024 * 1024)).toFixed(1) + ' MB';
                    return (bytes / (1024 * 1024 * 1024)).toFixed(2) + ' GB';
                };
                document.getElementById('heapAlloc').textContent = formatBytes(s.runtime.heap_alloc);
                document.getElementById('heapInuse').textContent = formatBytes(s.runtime.heap_inuse);
                document.getElementById('stackInuse').textContent = formatBytes(s.runtime.stack_inuse);
                document.getElementById('heapSys').textContent = formatBytes(s.runtime.heap_sys);
                document.getElementById('goroutines').textContent = s.runtime.goroutines;
                document.getElementById('numGC').textContent = s.runtime.num_gc;
                document.getElementById('lastGC').textContent = s.runtime.last_gc ? new Date(s.runtime.last_gc).toLocaleTimeString() : 'N/A';
                document.getElementById('goVersion').textContent = s.runtime.go_version;
                document.getElementById('numCPU').textContent = s.runtime.num_cpu;
                document.getElementById('osArch').textContent = s.runtime.goos + '/' + s.runtime.goarch;
            };
        }

        function toggleNotifDetails(type) {
            const details = document.getElementById(type + 'Details');
            const expand = document.getElementById(type + 'Expand');
            if (details.style.display === 'none') {
                details.style.display = 'block';
                expand.textContent = '▼';
            } else {
                details.style.display = 'none';
                expand.textContent = '▶';
            }
        }

        // Theme toggle
        function toggleTheme() {
            const body = document.body;
            const btn = document.querySelector('.theme-toggle');
            if (body.classList.contains('light-mode')) {
                body.classList.remove('light-mode');
                btn.textContent = '🌙 Dark';
                localStorage.setItem('theme', 'dark');
            } else {
                body.classList.add('light-mode');
                btn.textContent = '☀️ Light';
                localStorage.setItem('theme', 'light');
            }
        }

        // Load saved theme
        if (localStorage.getItem('theme') === 'light') {
            document.body.classList.add('light-mode');
            document.querySelector('.theme-toggle').textContent = '☀️ Light';
        }

        // Keyboard shortcuts
        document.addEventListener('keydown', (e) => {
            if (e.target.tagName === 'INPUT' || e.target.tagName === 'SELECT') return;
            switch(e.key.toLowerCase()) {
                case 't': toggleTheme(); break;
                case 'e': exportAlerts('json'); break;
                case '/': e.preventDefault(); document.getElementById('alertSearch').focus(); break;
                case '?': showShortcuts(); break;
                case 'escape': hideShortcuts(); document.getElementById('alertSearch').blur(); break;
            }
        });

        function showShortcuts() { document.getElementById('shortcutsModal').classList.add('show'); }
        function hideShortcuts() { document.getElementById('shortcutsModal').classList.remove('show'); }

        // Export alerts
        function exportAlerts(format) {
            const alerts = window.currentAlerts || [];
            if (alerts.length === 0) { alert('No alerts to export'); return; }
            const data = format === 'json' ? JSON.stringify(alerts, null, 2) : alertsToCSV(alerts);
            const blob = new Blob([data], { type: format === 'json' ? 'application/json' : 'text/csv' });
            const url = URL.createObjectURL(blob);
            const a = document.createElement('a');
            a.href = url;
            a.download = 'polybot-alerts-' + new Date().toISOString().split('T')[0] + '.' + format;
            a.click();
            URL.revokeObjectURL(url);
        }

        function alertsToCSV(alerts) {
            const header = 'timestamp,wallet_address,wallet_name,market_title,outcome,side,notional,reasons\n';
            return header + alerts.map(a =>
                [a.timestamp, a.wallet_address, a.wallet_name, '"' + a.market_title.replace(/"/g, '""') + '"',
                 a.outcome, a.side, a.notional, '"' + a.reasons.join(';') + '"'].join(',')
            ).join('\n');
        }

        // Render sparkline
        function renderSparkline(data) {
            const container = document.getElementById('sparkline');
            if (!data || data.length === 0) {
                container.innerHTML = '<span style="color: var(--text-secondary); font-size: 12px;">No data yet</span>';
                return;
            }
            const max = Math.max(...data, 1);
            container.innerHTML = data.map(v =>
                '<div class="sparkline-bar" style="height: ' + Math.max(2, (v / max) * 40) + 'px;" title="' + v + ' alerts"></div>'
            ).join('');
        }

        // Render timeline
        function renderTimeline(data) {
            const container = document.getElementById('timeline');
            if (!data || data.length === 0) {
                container.innerHTML = '<span style="color: var(--text-secondary); font-size: 12px;">No data yet</span>';
                return;
            }
            const max = Math.max(...data, 1);
            container.innerHTML = data.map((v, i) =>
                '<div class="timeline-bar" style="height: ' + Math.max(2, (v / max) * 60) + 'px;" data-count="' + v + ' alerts (' + (24 - i) + 'h ago)"></div>'
            ).join('');
        }

        // Render heuristic chart
        function renderHeuristicChart(alerts) {
            if (!alerts) return;
            const heuristics = [
                { name: 'High Win', value: alerts.high_win_rate || 0, color: 'var(--accent-green)' },
                { name: 'Contrarian Win', value: alerts.contrarian_winner || 0, color: 'var(--accent-green)' },
                { name: 'Massive', value: alerts.massive_trade || 0, color: 'var(--accent-yellow)' },
                { name: 'Extreme', value: alerts.extreme_bet || 0, color: 'var(--accent-red)' },
                { name: 'Rapid', value: alerts.rapid_trading || 0, color: 'var(--accent-blue)' },
                { name: 'New Wallet', value: alerts.new_wallet || 0, color: 'var(--accent-purple)' },
                { name: 'Contrarian', value: alerts.contrarian_bet || 0, color: 'var(--accent-orange)' },
                { name: 'Copy', value: alerts.copy_trader || 0, color: 'var(--accent-blue)' },
            ].filter(h => h.value > 0).sort((a, b) => b.value - a.value).slice(0, 6);

            if (heuristics.length === 0) {
                document.getElementById('heuristicChart').innerHTML = '<span style="color: var(--text-secondary); font-size: 12px;">No data yet</span>';
                return;
            }
            const max = Math.max(...heuristics.map(h => h.value), 1);
            document.getElementById('heuristicChart').innerHTML = heuristics.map(h =>
                '<div class="heuristic-bar-row">' +
                '<span class="heuristic-label">' + h.name + '</span>' +
                '<div class="heuristic-bar-container"><div class="heuristic-bar" style="width: ' + ((h.value / max) * 100) + '%; background: ' + h.color + ';"></div></div>' +
                '<span class="heuristic-count" style="color: ' + h.color + ';">' + h.value + '</span>' +
                '</div>'
            ).join('');
        }

        // Track expanded alerts by unique key (wallet + timestamp)
        window.expandedAlerts = window.expandedAlerts || new Set();

        // Render alerts with filtering
        function renderAlerts(alerts) {
            const search = document.getElementById('alertSearch').value.toLowerCase();
            const filter = document.getElementById('alertFilter').value;
            const watchlist = getWatchlist();

            let filtered = alerts;
            if (search) {
                filtered = filtered.filter(a =>
                    a.wallet_address.toLowerCase().includes(search) ||
                    a.wallet_name.toLowerCase().includes(search) ||
                    a.market_title.toLowerCase().includes(search)
                );
            }
            if (filter) {
                filtered = filtered.filter(a => a.reasons.includes(filter));
            }

            const el = document.getElementById('recentAlerts');
            if (filtered.length > 0) {
                el.innerHTML = filtered.slice(0, 20).map((a, idx) => {
                    const time = new Date(a.timestamp).toLocaleTimeString();
                    const fullTime = new Date(a.timestamp).toLocaleString();
                    const shortAddr = a.wallet_address.substring(0, 6) + '...' + a.wallet_address.substring(a.wallet_address.length - 4);
                    const name = a.wallet_name || shortAddr;
                    const profileUrl = a.wallet_url || 'https://polymarket.com/profile/' + a.wallet_address;
                    const marketUrl = a.market_url || '#';
                    const reasons = a.reasons.map(r => '<span class="reason-tag">' + r + '</span>').join('');
                    const severity = a.reasons.length >= 3 ? 'severity-high' : a.reasons.length >= 2 ? 'severity-medium' : '';
                    const inWatchlist = watchlist.includes(a.wallet_address.toLowerCase());
                    const watchIcon = inWatchlist ? ' 👁️' : '';
                    const alertKey = a.wallet_address + '-' + a.timestamp;
                    const alertId = 'alert-' + idx;

                    // Build expanded details section (restore expanded state if previously expanded)
                    const isExpanded = window.expandedAlerts.has(alertKey);
                    let details = '<div class="alert-details' + (isExpanded ? ' expanded' : '') + '" id="' + alertId + '-details" data-alert-key="' + alertKey + '">';
                    details += '<div class="alert-details-grid">';

                    // Trade info
                    details += '<div class="detail-section"><div class="detail-header">Trade Details</div>';
                    details += '<div class="detail-row"><span>Shares:</span><span>' + (a.shares || 0).toLocaleString(undefined, {maximumFractionDigits: 2}) + '</span></div>';
                    details += '<div class="detail-row"><span>Price:</span><span>$' + (a.price || 0).toFixed(3) + '</span></div>';
                    details += '<div class="detail-row"><span>Notional:</span><span>$' + (a.notional || 0).toLocaleString(undefined, {maximumFractionDigits: 2}) + '</span></div>';
                    details += '<div class="detail-row"><span>Time:</span><span>' + fullTime + '</span></div>';
                    details += '</div>';

                    // Wallet stats
                    details += '<div class="detail-section"><div class="detail-header">Wallet Stats</div>';
                    details += '<div class="detail-row"><span>Win Rate:</span><span class="' + (a.win_rate >= 0.7 ? 'green' : '') + '">' + ((a.win_rate || 0) * 100).toFixed(1) + '%</span></div>';
                    details += '<div class="detail-row"><span>Record:</span><span>' + (a.win_count || 0) + 'W - ' + (a.loss_count || 0) + 'L</span></div>';
                    details += '<div class="detail-row"><span>Markets:</span><span>' + (a.unique_markets || 0) + '</span></div>';
                    details += '</div>';

                    // Inventory (if available)
                    if (a.has_inventory) {
                        details += '<div class="detail-section"><div class="detail-header">Position After</div>';
                        details += '<div class="detail-row"><span>Shares:</span><span>' + (a.inv_shares || 0).toLocaleString(undefined, {maximumFractionDigits: 2}) + '</span></div>';
                        details += '<div class="detail-row"><span>Avg Price:</span><span>$' + (a.inv_avg_price || 0).toFixed(3) + '</span></div>';
                        details += '<div class="detail-row"><span>Value:</span><span>$' + (a.inv_value || 0).toLocaleString(undefined, {maximumFractionDigits: 2}) + '</span></div>';
                        details += '</div>';
                    }

                    details += '</div>'; // end grid

                    // Links
                    details += '<div class="detail-links">';
                    if (marketUrl !== '#') {
                        details += '<a href="' + marketUrl + '" target="_blank" class="detail-link">View Market ↗</a>';
                    }
                    details += '<a href="' + profileUrl + '" target="_blank" class="detail-link">View Wallet ↗</a>';
                    details += '</div>';

                    details += '</div>';

                    return '<div class="feed-item ' + severity + '" onclick="toggleAlertDetails(\'' + alertId + '\')" style="cursor: pointer;">' +
                        '<div style="display: flex; justify-content: space-between; align-items: center;">' +
                        '<a href="' + profileUrl + '" target="_blank" class="feed-wallet" style="text-decoration: none;" onclick="event.stopPropagation();">' + name + watchIcon + ' ↗</a>' +
                        '<div style="display: flex; align-items: center; gap: 8px;"><span class="feed-time">' + time + '</span><span class="expand-icon" id="' + alertId + '-icon">' + (isExpanded ? '▲' : '▼') + '</span></div>' +
                        '</div>' +
                        '<div class="feed-market">' + a.side + ' ' + a.outcome + ' @ $' + a.notional.toLocaleString(undefined, {maximumFractionDigits: 0}) + '</div>' +
                        '<div style="color: var(--text-secondary); font-size: 13px;">' + a.market_title.substring(0, 70) + (a.market_title.length > 70 ? '...' : '') + '</div>' +
                        '<div class="feed-reasons">' + reasons + '</div>' +
                        details +
                        '</div>';
                }).join('');
            } else {
                el.innerHTML = '<div style="color: var(--text-secondary); text-align: center; padding: 20px;">' +
                    (search || filter ? 'No matching alerts' : 'No alerts yet') + '</div>';
            }
        }

        function toggleAlertDetails(alertId) {
            const details = document.getElementById(alertId + '-details');
            const icon = document.getElementById(alertId + '-icon');
            const alertKey = details.getAttribute('data-alert-key');
            if (details.classList.contains('expanded')) {
                details.classList.remove('expanded');
                icon.textContent = '▼';
                window.expandedAlerts.delete(alertKey);
            } else {
                details.classList.add('expanded');
                icon.textContent = '▲';
                window.expandedAlerts.add(alertKey);
            }
        }

        function filterAlerts() {
            renderAlerts(window.currentAlerts || []);
        }

        // Time period tabs - switches sparkline data
        function setTimePeriod(period) {
            document.querySelectorAll('.time-tab').forEach(t => t.classList.remove('active'));
            document.getElementById('tab' + period).classList.add('active');
            window.selectedTimePeriod = period;
            // Update sparkline with data for selected period
            if (window.sparklineData && window.sparklineData[period]) {
                renderSparkline(window.sparklineData[period]);
            }
            // Update the card title
            const titles = { '1h': 'Last Hour', '24h': 'Last 24 Hours', '7d': 'Last 7 Days' };
            document.querySelector('#sparkline').parentElement.querySelector('h3').textContent = '📈 Activity (' + titles[period] + ')';
        }

        // Watchlist functions
        function getWatchlist() {
            try { return JSON.parse(localStorage.getItem('watchlist') || '[]'); } catch { return []; }
        }

        function saveWatchlist(list) {
            localStorage.setItem('watchlist', JSON.stringify(list));
            renderWatchlist();
        }

        function addToWatchlist() {
            const input = document.getElementById('watchlistInput');
            const addr = input.value.trim().toLowerCase();
            if (!addr || !addr.startsWith('0x')) { alert('Enter a valid wallet address'); return; }
            const list = getWatchlist();
            if (list.includes(addr)) { alert('Already in watchlist'); return; }
            list.push(addr);
            saveWatchlist(list);
            input.value = '';
        }

        function removeFromWatchlist(addr) {
            const list = getWatchlist().filter(w => w !== addr);
            saveWatchlist(list);
        }

        function renderWatchlist() {
            const list = getWatchlist();
            const el = document.getElementById('watchlistItems');
            if (list.length === 0) {
                el.innerHTML = '<div style="color: var(--text-secondary); font-size: 13px;">No wallets in watchlist</div>';
                return;
            }
            el.innerHTML = list.map(addr => {
                const short = addr.substring(0, 10) + '...' + addr.substring(addr.length - 6);
                const profileUrl = 'https://polymarket.com/profile/' + addr;
                return '<div class="watchlist-item">' +
                    '<a href="' + profileUrl + '" target="_blank" class="wallet-addr" style="text-decoration: none;">' + short + ' ↗</a>' +
                    '<button class="watchlist-remove" onclick="removeFromWatchlist(\'' + addr + '\')">✕</button>' +
                    '</div>';
            }).join('');
        }

        // Initialize watchlist
        renderWatchlist();

        console.log('Polybot dashboard loaded, initializing WebSocket...');
        connect();
    </script>

    <div class="footer">
        <div class="build-info">
            <span>Build: <a id="commitLink" href="#" target="_blank"><code id="commitHash">-</code></a></span>
            <span>Go: <span id="buildGoVersion">-</span></span>
        </div>
        <div style="margin-top: 8px;">
            Built with ❤️ by <a href="https://github.com/swiftyspiffy" target="_blank">swiftyspiffy</a>
        </div>
    </div>
</body>
</html>
`
