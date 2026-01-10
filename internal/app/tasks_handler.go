package app

import (
	"context"
	"encoding/json"
	"net/http"
	"polybot/clients/gist"
	"polybot/clients/polymarketapi"
	"time"

	"go.uber.org/zap"
)

// TasksHandler handles task-related HTTP requests.
type TasksHandler struct {
	logger      *zap.Logger
	polymarket  *polymarketapi.PolymarketApiClient
	authHandler *AuthHandler
	gist        *gist.Client
	tasksGistID string
}

// NewTasksHandler creates a new TasksHandler.
func NewTasksHandler(
	logger *zap.Logger,
	polymarket *polymarketapi.PolymarketApiClient,
	authHandler *AuthHandler,
	gistClient *gist.Client,
	tasksGistID string,
) *TasksHandler {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &TasksHandler{
		logger:      logger,
		polymarket:  polymarket,
		authHandler: authHandler,
		gist:        gistClient,
		tasksGistID: tasksGistID,
	}
}

// IsEnabled returns true if the tasks feature is enabled (gist configured).
func (h *TasksHandler) IsEnabled() bool {
	return h.tasksGistID != "" && h.gist != nil && h.gist.IsEnabled()
}

// SavedTask represents a task saved to the gist.
type SavedTask struct {
	ID                   int                       `json:"id"`
	Type                 string                    `json:"type"`
	Name                 string                    `json:"name"`
	Description          string                    `json:"description"`
	Status               string                    `json:"status"`
	StartTime            time.Time                 `json:"startTime"`
	EndTime              *time.Time                `json:"endTime,omitempty"`
	Result               *MultiMarketWinnersResult `json:"result,omitempty"`
	WalletActivityResult *WalletActivityResult     `json:"walletActivityResult,omitempty"`
	MarketHoldersResult  *MarketHoldersResult      `json:"marketHoldersResult,omitempty"`
	Error                string                    `json:"error,omitempty"`
}

// SavedTasksData is the structure saved to the gist.
type SavedTasksData struct {
	Tasks      []SavedTask `json:"tasks"`
	LastTaskID int         `json:"lastTaskId"`
}

// RegisterRoutes registers task HTTP routes.
func (h *TasksHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/tasks", h.handleTasksPage)
	mux.HandleFunc("/api/tasks/markets/search", h.handleMarketsSearch)
	mux.HandleFunc("/api/tasks/markets/search-all", h.handleMarketsSearchAll)
	mux.HandleFunc("/api/tasks/multimarket-winners", h.handleMultiMarketWinners)
	mux.HandleFunc("/api/tasks/history", h.handleTasksHistory)
	mux.HandleFunc("/api/tasks/wallet-activity", h.handleWalletActivity)
	mux.HandleFunc("/api/tasks/market-holders", h.handleMarketHolders)
}

// requireAuth checks if the request is authenticated (when auth is configured).
func (h *TasksHandler) requireAuth(w http.ResponseWriter, r *http.Request) bool {
	if h.authHandler == nil {
		return true
	}
	if !h.authHandler.HasCredentials() {
		return true
	}
	if h.authHandler.IsAuthenticated(r) {
		return true
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	json.NewEncoder(w).Encode(map[string]any{
		"error":   "authentication_required",
		"message": "You must be logged in to access tasks",
	})
	return false
}

// handleTasksPage serves the tasks HTML page.
func (h *TasksHandler) handleTasksPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(tasksPageHTML))
}

// handleMarketsSearch searches for resolved markets.
func (h *TasksHandler) handleMarketsSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !h.requireAuth(w, r) {
		return
	}

	query := r.URL.Query().Get("q")
	closedAfterStr := r.URL.Query().Get("closedAfter")
	closedBeforeStr := r.URL.Query().Get("closedBefore")

	// Build search options
	opts := polymarketapi.MarketSearchOptions{
		Query: query,
	}

	// Parse date filters
	if closedAfterStr != "" {
		if t, err := time.Parse("2006-01-02", closedAfterStr); err == nil {
			opts.ClosedAfter = &t
		}
	}
	if closedBeforeStr != "" {
		if t, err := time.Parse("2006-01-02", closedBeforeStr); err == nil {
			// Add 1 day to include the end date
			t = t.Add(24 * time.Hour)
			opts.ClosedBefore = &t
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	markets, err := h.polymarket.GetClosedMarketsWithOptions(ctx, 50, 0, opts)
	if err != nil {
		h.logger.Error("failed to search markets", zap.Error(err))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to search markets"})
		return
	}

	// Convert to response format
	type marketResult struct {
		ConditionID    string `json:"conditionId"`
		Title          string `json:"title"`
		Slug           string `json:"slug"`
		WinningOutcome string `json:"winningOutcome"`
		Image          string `json:"image"`
		ClosedTime     string `json:"closedTime,omitempty"`
	}

	results := make([]marketResult, 0, len(markets))
	for _, m := range markets {
		winner, _ := m.GetWinningOutcome()
		// Only include resolved markets with a winning outcome
		if winner == "" {
			continue
		}
		results = append(results, marketResult{
			ConditionID:    m.ConditionID,
			Title:          m.Question,
			Slug:           m.Slug,
			WinningOutcome: winner,
			Image:          m.Image,
			ClosedTime:     m.ClosedTime,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"markets": results,
	})
}

// handleMarketsSearchAll searches for all markets (open and closed).
func (h *TasksHandler) handleMarketsSearchAll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !h.requireAuth(w, r) {
		return
	}

	query := r.URL.Query().Get("q")
	if query == "" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"markets": []any{}})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	// Search active markets
	activeMarkets, err := h.polymarket.SearchActiveMarkets(ctx, query, 25)
	if err != nil {
		h.logger.Warn("failed to search active markets", zap.Error(err))
	}

	// Search closed markets
	closedMarkets, err := h.polymarket.GetClosedMarketsWithOptions(ctx, 25, 0, polymarketapi.MarketSearchOptions{Query: query})
	if err != nil {
		h.logger.Warn("failed to search closed markets", zap.Error(err))
	}

	// Convert to response format
	type marketResult struct {
		ConditionID string `json:"conditionId"`
		Title       string `json:"title"`
		Slug        string `json:"slug"`
		Image       string `json:"image"`
		Active      bool   `json:"active"`
	}

	seen := make(map[string]bool)
	results := make([]marketResult, 0)

	// Add active markets first
	for _, m := range activeMarkets {
		if m.ConditionID == "" || seen[m.ConditionID] {
			continue
		}
		seen[m.ConditionID] = true
		results = append(results, marketResult{
			ConditionID: m.ConditionID,
			Title:       m.Question,
			Slug:        m.Slug,
			Image:       m.Image,
			Active:      true,
		})
	}

	// Add closed markets
	for _, m := range closedMarkets {
		if m.ConditionID == "" || seen[m.ConditionID] {
			continue
		}
		seen[m.ConditionID] = true
		results = append(results, marketResult{
			ConditionID: m.ConditionID,
			Title:       m.Question,
			Slug:        m.Slug,
			Image:       m.Image,
			Active:      false,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"markets": results,
	})
}

// handleMultiMarketWinners runs the multi-market winners analysis.
func (h *TasksHandler) handleMultiMarketWinners(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !h.requireAuth(w, r) {
		return
	}

	var req MultiMarketWinnersRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid request body"})
		return
	}

	if len(req.Markets) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "At least one market must be selected"})
		return
	}

	if len(req.Markets) > 20 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Maximum 20 markets can be selected"})
		return
	}

	// Create task and execute
	task := NewMultiMarketWinnersTask(h.polymarket, h.logger)

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()

	result, err := task.Execute(ctx, req)
	if err != nil {
		h.logger.Error("task execution failed", zap.Error(err))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Task execution failed: " + err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// handleTasksHistory loads or saves task history.
func (h *TasksHandler) handleTasksHistory(w http.ResponseWriter, r *http.Request) {
	if !h.requireAuth(w, r) {
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.loadTaskHistory(w, r)
	case http.MethodPost:
		h.saveTaskHistory(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// loadTaskHistory loads task history from gist.
func (h *TasksHandler) loadTaskHistory(w http.ResponseWriter, r *http.Request) {
	if !h.IsEnabled() {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(SavedTasksData{Tasks: []SavedTask{}, LastTaskID: 0})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	content, err := h.gist.Load(ctx, "tasks.json", h.tasksGistID)
	if err != nil {
		h.logger.Warn("failed to load task history", zap.Error(err))
		// Return empty history on error
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(SavedTasksData{Tasks: []SavedTask{}, LastTaskID: 0})
		return
	}

	var data SavedTasksData
	if err := json.Unmarshal([]byte(content), &data); err != nil {
		h.logger.Warn("failed to parse task history", zap.Error(err))
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(SavedTasksData{Tasks: []SavedTask{}, LastTaskID: 0})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

// saveTaskHistory saves task history to gist.
func (h *TasksHandler) saveTaskHistory(w http.ResponseWriter, r *http.Request) {
	if !h.IsEnabled() {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Tasks gist not configured"})
		return
	}

	var data SavedTasksData
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid request body"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		h.logger.Error("failed to marshal task history", zap.Error(err))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to save task history"})
		return
	}

	if err := h.gist.Save(ctx, "tasks.json", string(jsonData), h.tasksGistID); err != nil {
		h.logger.Error("failed to save task history to gist", zap.Error(err))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to save task history"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "saved"})
}

// handleWalletActivity runs the wallet activity/cost basis analysis.
func (h *TasksHandler) handleWalletActivity(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !h.requireAuth(w, r) {
		return
	}

	var req WalletActivityRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid request body"})
		return
	}

	if req.WalletAddress == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Wallet address is required"})
		return
	}

	// Validate duration
	validDurations := map[string]bool{
		"1d": true, "1w": true, "2w": true, "1m": true,
		"3m": true, "6m": true, "1y": true,
	}
	if !validDurations[req.Duration] {
		req.Duration = "1m" // Default to 1 month
	}

	// Create task and execute
	task := NewWalletActivityTask(h.polymarket, h.logger)

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()

	result, err := task.Execute(ctx, req)
	if err != nil {
		h.logger.Error("wallet activity task execution failed", zap.Error(err))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Task execution failed: " + err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// handleMarketHolders runs the market holders analysis.
func (h *TasksHandler) handleMarketHolders(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !h.requireAuth(w, r) {
		return
	}

	var req MarketHoldersRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid request body"})
		return
	}

	if req.ConditionID == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Condition ID is required"})
		return
	}

	// Create task and execute
	task := NewMarketHoldersTask(h.polymarket, h.logger)

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()

	result, err := task.Execute(ctx, req)
	if err != nil {
		h.logger.Error("market holders task execution failed", zap.Error(err))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Task execution failed: " + err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

const tasksPageHTML = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Polybot Tasks</title>
    <style>
        :root {
            --bg-primary: #0d1117;
            --bg-secondary: #161b22;
            --bg-tertiary: #21262d;
            --text-primary: #f0f6fc;
            --text-secondary: #8b949e;
            --border: #30363d;
            --accent: #58a6ff;
            --accent-hover: #79b8ff;
            --success: #3fb950;
            --error: #f85149;
            --warning: #d29922;
        }

        * {
            box-sizing: border-box;
            margin: 0;
            padding: 0;
        }

        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
            background: var(--bg-primary);
            color: var(--text-primary);
            line-height: 1.6;
            min-height: 100vh;
        }

        .header {
            background: var(--bg-secondary);
            border-bottom: 1px solid var(--border);
            padding: 16px 24px;
            display: flex;
            justify-content: space-between;
            align-items: center;
        }

        .header h1 {
            font-size: 20px;
            font-weight: 600;
        }

        .header-actions {
            display: flex;
            gap: 16px;
            align-items: center;
        }

        .nav-link {
            color: var(--text-secondary);
            text-decoration: none;
            font-size: 14px;
        }

        .nav-link:hover {
            color: var(--accent);
        }

        .auth-section {
            padding: 12px 24px;
            background: var(--bg-tertiary);
            border-bottom: 1px solid var(--border);
            display: flex;
            justify-content: space-between;
            align-items: center;
        }

        .auth-status {
            font-size: 14px;
            color: var(--text-secondary);
        }

        .auth-status.authenticated {
            color: var(--success);
        }

        .main-content {
            display: flex;
            min-height: calc(100vh - 120px);
        }

        .sidebar {
            width: 240px;
            background: var(--bg-secondary);
            border-right: 1px solid var(--border);
            padding: 16px;
        }

        .sidebar h2 {
            font-size: 12px;
            text-transform: uppercase;
            color: var(--text-secondary);
            margin-bottom: 12px;
            letter-spacing: 0.5px;
        }

        .task-list {
            display: flex;
            flex-direction: column;
            gap: 4px;
        }

        .task-item {
            padding: 10px 12px;
            border-radius: 6px;
            cursor: pointer;
            font-size: 14px;
            color: var(--text-secondary);
            transition: all 0.15s;
        }

        .task-item:hover {
            background: var(--bg-tertiary);
            color: var(--text-primary);
        }

        .task-item.active {
            background: var(--accent);
            color: white;
        }

        .content {
            flex: 1;
            padding: 24px;
            max-width: 1000px;
        }

        .task-header {
            margin-bottom: 24px;
        }

        .task-header h2 {
            font-size: 24px;
            margin-bottom: 8px;
        }

        .task-header p {
            color: var(--text-secondary);
        }

        .section {
            background: var(--bg-secondary);
            border: 1px solid var(--border);
            border-radius: 8px;
            padding: 20px;
            margin-bottom: 20px;
        }

        .section h3 {
            font-size: 14px;
            margin-bottom: 12px;
            color: var(--text-secondary);
        }

        .date-filters {
            margin-bottom: 12px;
        }

        .quick-filters {
            display: flex;
            align-items: center;
            gap: 8px;
            margin-bottom: 10px;
            flex-wrap: wrap;
        }

        .filter-label {
            font-size: 13px;
            color: var(--text-secondary);
        }

        .filter-btn {
            padding: 5px 12px;
            background: var(--bg-tertiary);
            border: 1px solid var(--border);
            border-radius: 4px;
            color: var(--text-secondary);
            font-size: 12px;
            cursor: pointer;
            transition: all 0.15s;
        }

        .filter-btn:hover {
            background: var(--bg-primary);
            color: var(--text-primary);
        }

        .filter-btn.active {
            background: var(--accent);
            border-color: var(--accent);
            color: white;
        }

        .custom-date-range {
            display: flex;
            align-items: center;
            gap: 12px;
            flex-wrap: wrap;
        }

        .custom-date-range label {
            display: flex;
            align-items: center;
            gap: 6px;
            font-size: 13px;
            color: var(--text-secondary);
        }

        .custom-date-range input[type="date"] {
            padding: 5px 8px;
            background: var(--bg-primary);
            border: 1px solid var(--border);
            border-radius: 4px;
            color: var(--text-primary);
            font-size: 12px;
        }

        .custom-date-range input[type="date"]:focus {
            outline: none;
            border-color: var(--accent);
        }

        .clear-btn {
            padding: 5px 10px;
        }

        .market-search {
            position: relative;
        }

        .market-search input {
            width: 100%;
            padding: 10px 14px;
            background: var(--bg-primary);
            border: 1px solid var(--border);
            border-radius: 6px;
            color: var(--text-primary);
            font-size: 14px;
        }

        .market-search input:focus {
            outline: none;
            border-color: var(--accent);
        }

        .market-search input:disabled {
            opacity: 0.6;
            cursor: not-allowed;
        }

        .market-search .search-loading {
            position: absolute;
            right: 12px;
            top: 50%;
            transform: translateY(-50%);
            width: 18px;
            height: 18px;
            border: 2px solid var(--border);
            border-top-color: var(--accent);
            border-radius: 50%;
            animation: spin 1s linear infinite;
            display: none;
        }

        .market-search .search-loading.show {
            display: block;
        }

        .search-results {
            position: absolute;
            top: 100%;
            left: 0;
            right: 0;
            background: var(--bg-secondary);
            border: 1px solid var(--border);
            border-radius: 6px;
            margin-top: 4px;
            max-height: 300px;
            overflow-y: auto;
            z-index: 100;
            display: none;
        }

        .search-results.show {
            display: block;
        }

        .search-result-item {
            padding: 10px 14px;
            cursor: pointer;
            border-bottom: 1px solid var(--border);
            display: flex;
            align-items: center;
            gap: 10px;
        }

        .search-result-item:last-child {
            border-bottom: none;
        }

        .search-result-item:hover {
            background: var(--bg-tertiary);
        }

        .search-result-item img {
            width: 32px;
            height: 32px;
            border-radius: 4px;
            object-fit: cover;
        }

        .search-result-info {
            flex: 1;
            min-width: 0;
        }

        .search-result-title {
            font-size: 14px;
            white-space: nowrap;
            overflow: hidden;
            text-overflow: ellipsis;
        }

        .search-result-outcome {
            font-size: 12px;
            color: var(--success);
        }

        .selected-markets {
            display: flex;
            flex-wrap: wrap;
            gap: 8px;
            margin-top: 12px;
        }

        .market-chip {
            display: flex;
            align-items: center;
            gap: 8px;
            padding: 6px 10px;
            background: var(--bg-tertiary);
            border: 1px solid var(--border);
            border-radius: 20px;
            font-size: 13px;
        }

        .market-chip .remove {
            cursor: pointer;
            color: var(--text-secondary);
            font-size: 16px;
            line-height: 1;
        }

        .market-chip .remove:hover {
            color: var(--error);
        }

        .task-options {
            display: flex;
            gap: 20px;
            align-items: center;
        }

        .task-options label {
            font-size: 14px;
            color: var(--text-secondary);
        }

        .task-options input[type="number"] {
            width: 60px;
            padding: 6px 10px;
            background: var(--bg-primary);
            border: 1px solid var(--border);
            border-radius: 4px;
            color: var(--text-primary);
            font-size: 14px;
        }

        .btn {
            padding: 10px 20px;
            border-radius: 6px;
            border: none;
            font-size: 14px;
            font-weight: 500;
            cursor: pointer;
            transition: all 0.15s;
        }

        .btn-primary {
            background: var(--accent);
            color: white;
        }

        .btn-primary:hover {
            background: var(--accent-hover);
        }

        .btn-primary:disabled {
            background: var(--bg-tertiary);
            color: var(--text-secondary);
            cursor: not-allowed;
        }

        .btn-secondary {
            background: var(--bg-tertiary);
            color: var(--text-primary);
        }

        .results-section {
            display: none;
        }

        .results-section.show {
            display: block;
        }

        .results-summary {
            padding: 16px;
            background: var(--bg-tertiary);
            border-radius: 6px;
            margin-bottom: 16px;
            display: flex;
            gap: 24px;
        }

        .summary-stat {
            display: flex;
            flex-direction: column;
        }

        .summary-stat .value {
            font-size: 24px;
            font-weight: 600;
        }

        .summary-stat .label {
            font-size: 12px;
            color: var(--text-secondary);
        }

        .results-table {
            width: 100%;
            border-collapse: collapse;
        }

        .results-table th,
        .results-table td {
            padding: 12px;
            text-align: left;
            border-bottom: 1px solid var(--border);
        }

        .results-table th {
            font-size: 12px;
            text-transform: uppercase;
            color: var(--text-secondary);
            font-weight: 500;
            cursor: pointer;
            user-select: none;
        }

        .results-table th:hover {
            color: var(--text-primary);
        }

        .results-table th.sorted::after {
            content: ' ↓';
        }

        .results-table th.sorted.asc::after {
            content: ' ↑';
        }

        .results-table td {
            font-size: 14px;
        }

        .wallet-address {
            font-family: monospace;
            font-size: 13px;
        }

        .wallet-address a {
            color: var(--accent);
            text-decoration: none;
        }

        .wallet-address a:hover {
            text-decoration: underline;
        }

        .markets-list {
            display: flex;
            flex-wrap: wrap;
            gap: 4px;
        }

        .market-tag {
            padding: 2px 8px;
            background: var(--bg-tertiary);
            border-radius: 4px;
            font-size: 12px;
            white-space: nowrap;
        }

        .loading {
            display: none;
            align-items: center;
            justify-content: center;
            padding: 40px;
            color: var(--text-secondary);
        }

        .loading.show {
            display: flex;
        }

        .spinner {
            width: 24px;
            height: 24px;
            border: 2px solid var(--border);
            border-top-color: var(--accent);
            border-radius: 50%;
            animation: spin 1s linear infinite;
            margin-right: 12px;
        }

        @keyframes spin {
            to { transform: rotate(360deg); }
        }

        .login-prompt {
            text-align: center;
            padding: 60px 20px;
        }

        .login-prompt h2 {
            margin-bottom: 12px;
        }

        .login-prompt p {
            color: var(--text-secondary);
            margin-bottom: 20px;
        }

        .toast {
            position: fixed;
            bottom: 20px;
            right: 20px;
            padding: 12px 20px;
            background: var(--bg-secondary);
            border: 1px solid var(--border);
            border-radius: 6px;
            z-index: 1000;
            animation: slideIn 0.3s;
        }

        .toast.error {
            border-color: var(--error);
        }

        .toast.success {
            border-color: var(--success);
        }

        @keyframes slideIn {
            from { transform: translateY(100%); opacity: 0; }
            to { transform: translateY(0); opacity: 1; }
        }

        /* Running Tasks Section */
        .running-tasks-section {
            padding: 16px;
            border-bottom: 1px solid var(--border);
            display: none;
        }

        .running-tasks-section.show {
            display: block;
        }

        .running-tasks-section h2 {
            font-size: 12px;
            text-transform: uppercase;
            color: var(--text-secondary);
            margin-bottom: 12px;
            letter-spacing: 0.5px;
        }

        .running-task-item {
            display: flex;
            align-items: center;
            gap: 10px;
            padding: 10px 12px;
            background: var(--bg-tertiary);
            border-radius: 6px;
            margin-bottom: 8px;
            cursor: pointer;
            transition: all 0.15s;
        }

        .running-task-item:hover {
            background: var(--bg-primary);
        }

        .running-task-item.status-running {
            border-left: 3px solid var(--accent);
        }

        .running-task-item.status-completed {
            border-left: 3px solid var(--success);
        }

        .running-task-item.status-failed {
            border-left: 3px solid var(--error);
        }

        .task-spinner {
            width: 16px;
            height: 16px;
            border: 2px solid var(--border);
            border-top-color: var(--accent);
            border-radius: 50%;
            animation: spin 1s linear infinite;
        }

        .task-icon {
            width: 16px;
            height: 16px;
            display: flex;
            align-items: center;
            justify-content: center;
            font-size: 14px;
        }

        .task-icon.success { color: var(--success); }
        .task-icon.error { color: var(--error); }

        .running-task-info {
            flex: 1;
            min-width: 0;
        }

        .running-task-name {
            font-size: 13px;
            white-space: nowrap;
            overflow: hidden;
            text-overflow: ellipsis;
        }

        .running-task-meta {
            font-size: 11px;
            color: var(--text-secondary);
        }

        /* Task Details Modal */
        .modal-overlay {
            position: fixed;
            top: 0;
            left: 0;
            right: 0;
            bottom: 0;
            background: rgba(0, 0, 0, 0.7);
            display: none;
            align-items: center;
            justify-content: center;
            z-index: 1000;
        }

        .modal-overlay.show {
            display: flex;
        }

        .modal {
            background: var(--bg-secondary);
            border: 1px solid var(--border);
            border-radius: 12px;
            width: 90%;
            max-width: 900px;
            max-height: 80vh;
            overflow: hidden;
            display: flex;
            flex-direction: column;
        }

        .modal-header {
            display: flex;
            justify-content: space-between;
            align-items: center;
            padding: 16px 20px;
            border-bottom: 1px solid var(--border);
        }

        .modal-header h3 {
            font-size: 16px;
        }

        .modal-close {
            background: none;
            border: none;
            color: var(--text-secondary);
            font-size: 24px;
            cursor: pointer;
            line-height: 1;
        }

        .modal-close:hover {
            color: var(--text-primary);
        }

        .modal-body {
            padding: 20px;
            overflow-y: auto;
            flex: 1;
        }

        .modal-status {
            display: inline-flex;
            align-items: center;
            gap: 6px;
            padding: 4px 10px;
            border-radius: 12px;
            font-size: 12px;
            margin-bottom: 16px;
        }

        .modal-status.running {
            background: rgba(88, 166, 255, 0.15);
            color: var(--accent);
        }

        .modal-status.completed {
            background: rgba(63, 185, 80, 0.15);
            color: var(--success);
        }

        .modal-status.failed {
            background: rgba(248, 81, 73, 0.15);
            color: var(--error);
        }

        .modal-header-actions {
            display: flex;
            align-items: center;
            gap: 12px;
        }

        .btn-sm {
            padding: 6px 12px;
            font-size: 12px;
        }

        .btn-secondary {
            background: var(--bg-tertiary);
            border: 1px solid var(--border);
            color: var(--text-primary);
        }

        .btn-secondary:hover {
            background: var(--bg-primary);
            border-color: var(--accent);
        }

        /* Finished Tasks Section */
        .finished-tasks-section {
            padding: 16px;
            border-top: 1px solid var(--border);
            display: none;
        }

        .finished-tasks-section.show {
            display: block;
        }

        .finished-tasks-section h2 {
            font-size: 12px;
            text-transform: uppercase;
            color: var(--text-secondary);
            margin-bottom: 12px;
            letter-spacing: 0.5px;
        }

        .finished-task-item {
            display: flex;
            align-items: center;
            gap: 10px;
            padding: 10px 12px;
            background: var(--bg-tertiary);
            border-radius: 6px;
            margin-bottom: 8px;
            cursor: pointer;
            transition: all 0.15s;
            position: relative;
        }

        .finished-task-item:hover {
            background: var(--bg-primary);
        }

        .finished-task-item.status-completed {
            border-left: 3px solid var(--success);
        }

        .finished-task-item.status-failed {
            border-left: 3px solid var(--error);
        }

        .finished-task-item .delete-btn {
            position: absolute;
            right: 8px;
            top: 50%;
            transform: translateY(-50%);
            background: none;
            border: none;
            color: var(--text-secondary);
            font-size: 16px;
            cursor: pointer;
            padding: 4px 8px;
            border-radius: 4px;
            opacity: 0;
            transition: all 0.15s;
        }

        .finished-task-item:hover .delete-btn {
            opacity: 1;
        }

        .finished-task-item .delete-btn:hover {
            color: var(--error);
            background: rgba(248, 81, 73, 0.1);
        }

        /* Wallet Activity Task Styles */
        .input-tabs {
            display: flex;
            gap: 8px;
            margin-bottom: 12px;
        }

        .input-tab {
            padding: 8px 16px;
            background: var(--bg-tertiary);
            border: 1px solid var(--border);
            border-radius: 6px;
            color: var(--text-secondary);
            font-size: 13px;
            cursor: pointer;
            transition: all 0.15s;
        }

        .input-tab:hover {
            background: var(--bg-primary);
            color: var(--text-primary);
        }

        .input-tab.active {
            background: var(--accent);
            border-color: var(--accent);
            color: white;
        }

        .wallet-input-mode {
            margin-top: 8px;
        }

        .wallet-address-field {
            width: 100%;
            padding: 10px 14px;
            background: var(--bg-primary);
            border: 1px solid var(--border);
            border-radius: 6px;
            color: var(--text-primary);
            font-size: 14px;
            font-family: monospace;
        }

        .wallet-address-field:focus {
            outline: none;
            border-color: var(--accent);
        }

        .selected-wallet {
            display: flex;
            align-items: center;
            gap: 12px;
            padding: 12px 16px;
            background: var(--bg-tertiary);
            border: 1px solid var(--border);
            border-radius: 8px;
            margin-top: 12px;
        }

        .selected-wallet img {
            width: 40px;
            height: 40px;
            border-radius: 50%;
            object-fit: cover;
        }

        .selected-wallet-info {
            flex: 1;
        }

        .selected-wallet-name {
            font-size: 14px;
            font-weight: 500;
        }

        .selected-wallet-address {
            font-size: 12px;
            color: var(--text-secondary);
            font-family: monospace;
        }

        .selected-wallet .remove {
            cursor: pointer;
            color: var(--text-secondary);
            font-size: 20px;
            line-height: 1;
        }

        .selected-wallet .remove:hover {
            color: var(--error);
        }

        .duration-filters {
            display: flex;
            flex-wrap: wrap;
            gap: 8px;
        }

        .cost-basis-value {
            font-weight: 600;
            color: var(--accent);
        }

        .market-cost-row td {
            vertical-align: top;
        }

        .outcome-breakdown {
            font-size: 12px;
            color: var(--text-secondary);
            margin-top: 4px;
        }

        .outcome-breakdown span {
            display: inline-block;
            margin-right: 12px;
        }
    </style>
</head>
<body>
    <div class="header">
        <h1>Polybot Tasks</h1>
        <div class="header-actions">
            <a href="/" class="nav-link">Dashboard</a>
            <a href="/settings" class="nav-link">Settings</a>
        </div>
    </div>

    <div class="auth-section" id="authSection" style="display: none;">
        <span class="auth-status" id="authStatus">Checking authentication...</span>
        <div id="authActions"></div>
    </div>

    <div class="login-prompt" id="loginPrompt" style="display: none;">
        <h2>Authentication Required</h2>
        <p>You must be logged in to access tasks.</p>
        <button class="btn btn-primary" onclick="window.location.href='/settings'">Go to Settings to Login</button>
    </div>

    <div class="main-content" id="mainContent">
        <div class="sidebar">
            <div class="running-tasks-section" id="runningTasksSection">
                <h2>Running Tasks</h2>
                <div id="runningTasksList"></div>
            </div>
            <h2 style="padding: 16px 16px 0 16px; font-size: 12px; text-transform: uppercase; color: var(--text-secondary); letter-spacing: 0.5px;">Available Tasks</h2>
            <div class="task-list" style="padding: 12px 16px;">
                <div class="task-item active" data-task="multimarket-winners" onclick="switchTask('multimarket-winners')">
                    Multi-Market Winners
                </div>
                <div class="task-item" data-task="wallet-activity" onclick="switchTask('wallet-activity')">
                    Wallet Activity
                </div>
                <div class="task-item" data-task="market-holders" onclick="switchTask('market-holders')">
                    Market Holders
                </div>
            </div>
            <div class="finished-tasks-section" id="finishedTasksSection">
                <h2>Finished Tasks</h2>
                <div id="finishedTasksList"></div>
            </div>
        </div>

        <div class="content">
            <!-- Multi-Market Winners Task -->
            <div id="multimarket-winners" class="task-panel">
                <div class="task-header">
                    <h2>Multi-Market Winners</h2>
                    <p>Find wallets that won across multiple resolved markets</p>
                </div>

                <div class="section">
                    <h3>Select Markets</h3>
                    <div class="date-filters">
                        <div class="quick-filters">
                            <span class="filter-label">Closed:</span>
                            <button class="filter-btn" onclick="setDateFilter('1d')">Last 24h</button>
                            <button class="filter-btn" onclick="setDateFilter('7d')">Last 7 days</button>
                            <button class="filter-btn" onclick="setDateFilter('14d')">Last 2 weeks</button>
                            <button class="filter-btn" onclick="setDateFilter('all')">All time</button>
                        </div>
                        <div class="custom-date-range">
                            <label>
                                From: <input type="date" id="dateFrom" onchange="updateDateFilter()">
                            </label>
                            <label>
                                To: <input type="date" id="dateTo" onchange="updateDateFilter()">
                            </label>
                            <button class="filter-btn clear-btn" onclick="clearDateFilter()">Clear</button>
                        </div>
                    </div>
                    <div class="market-search">
                        <input type="text" id="marketSearchInput" placeholder="Search markets and press Enter..." autocomplete="off">
                        <div class="search-loading" id="searchLoading"></div>
                        <div class="search-results" id="searchResults"></div>
                    </div>
                    <div class="selected-markets" id="selectedMarkets"></div>
                </div>

                <div class="section">
                    <h3>Options</h3>
                    <div class="task-options">
                        <label>
                            Minimum markets won:
                            <input type="number" id="minMarketsWon" value="2" min="2" max="20">
                        </label>
                    </div>
                </div>

                <div class="section">
                    <button class="btn btn-primary" id="runTaskBtn" onclick="runTask()" disabled>
                        Start Task
                    </button>
                </div>

                <div class="loading" id="loadingIndicator">
                    <div class="spinner"></div>
                    <span>Analyzing markets...</span>
                </div>

                <div class="results-section" id="resultsSection">
                    <div class="section">
                        <h3>Results</h3>
                        <div class="results-summary" id="resultsSummary"></div>
                        <table class="results-table">
                            <thead>
                                <tr>
                                    <th onclick="sortResults('address')">Wallet</th>
                                    <th onclick="sortResults('marketsWon')" class="sorted">Markets Won</th>
                                    <th>Markets</th>
                                </tr>
                            </thead>
                            <tbody id="resultsBody"></tbody>
                        </table>
                    </div>
                </div>
            </div>

            <!-- Wallet Activity Task -->
            <div id="wallet-activity" class="task-panel" style="display: none;">
                <div class="task-header">
                    <h2>Wallet Activity</h2>
                    <p>Analyze a wallet's trading activity and cost basis across markets</p>
                </div>

                <div class="section">
                    <h3>Enter Wallet Address</h3>
                    <p style="color: var(--text-secondary); font-size: 13px; margin-bottom: 12px;">
                        Enter a Polymarket wallet address to analyze their trading activity.
                        <a href="https://polymarket.com/leaderboard" target="_blank" style="color: var(--accent);">Find wallets on the leaderboard</a>
                    </p>
                    <input type="text" id="walletAddressInput" placeholder="Enter wallet address (0x...)" class="wallet-address-field">
                    <div id="selectedWallet" class="selected-wallet" style="display: none;"></div>
                </div>

                <div class="section">
                    <h3>Time Period</h3>
                    <div class="duration-filters">
                        <button class="filter-btn" onclick="setWalletDuration('1d')">Last 24h</button>
                        <button class="filter-btn" onclick="setWalletDuration('1w')">Last Week</button>
                        <button class="filter-btn active" onclick="setWalletDuration('2w')">Last 2 Weeks</button>
                        <button class="filter-btn" onclick="setWalletDuration('1m')">Last Month</button>
                        <button class="filter-btn" onclick="setWalletDuration('3m')">Last 3 Months</button>
                        <button class="filter-btn" onclick="setWalletDuration('6m')">Last 6 Months</button>
                        <button class="filter-btn" onclick="setWalletDuration('1y')">Last Year</button>
                    </div>
                </div>

                <div class="section">
                    <button class="btn btn-primary" id="runWalletTaskBtn" onclick="runWalletActivityTask()" disabled>
                        Analyze Activity
                    </button>
                </div>
            </div>

            <!-- Market Holders Task -->
            <div id="market-holders" class="task-panel" style="display: none;">
                <div class="task-header">
                    <h2>Market Holders</h2>
                    <p>Find the largest current holders of each outcome in a market</p>
                </div>

                <div class="section">
                    <h3>Search for a Market</h3>
                    <p style="color: var(--text-secondary); font-size: 13px; margin-bottom: 12px;">
                        Search for any market (open or closed) to see the largest position holders.
                    </p>
                    <div class="market-search">
                        <input type="text" id="holdersMarketSearchInput" placeholder="Search markets and press Enter..." autocomplete="off">
                        <div class="search-loading" id="holdersSearchLoading"></div>
                        <div class="search-results" id="holdersSearchResults"></div>
                    </div>
                    <div id="selectedHoldersMarket" class="selected-wallet" style="display: none;"></div>
                </div>

                <div class="section">
                    <h3>Options</h3>
                    <div class="task-options">
                        <label>
                            Top holders per outcome:
                            <input type="number" id="topHoldersCount" value="50" min="10" max="100">
                        </label>
                    </div>
                </div>

                <div class="section">
                    <button class="btn btn-primary" id="runHoldersTaskBtn" onclick="runMarketHoldersTask()" disabled>
                        Find Holders
                    </button>
                </div>
            </div>
        </div>
    </div>

    <!-- Task Details Modal -->
    <div class="modal-overlay" id="taskModal" onclick="closeModal(event)">
        <div class="modal" onclick="event.stopPropagation()">
            <div class="modal-header">
                <h3 id="modalTitle">Task Details</h3>
                <div class="modal-header-actions">
                    <button class="btn btn-secondary btn-sm" id="exportCsvBtn" onclick="exportTaskToCsv()" style="display: none;">Export CSV</button>
                    <button class="modal-close" onclick="closeModal()">&times;</button>
                </div>
            </div>
            <div class="modal-body" id="modalBody">
                <!-- Content inserted dynamically -->
            </div>
        </div>
    </div>

    <script>
        let authState = null;
        let selectedMarkets = [];
        let searchTimeout = null;
        let results = [];
        let dateFilterFrom = null;
        let dateFilterTo = null;
        let activeQuickFilter = 'all';
        let sortColumn = 'marketsWon';
        let sortAsc = false;
        let runningTasks = [];
        let taskIdCounter = 0;

        // Wallet Activity state
        let selectedWallet = null;
        let walletDuration = '2w';
        let currentModalTask = null;

        // Initialize
        document.addEventListener('DOMContentLoaded', () => {
            checkAuthStatus();
            setupMarketSearch();
            setupWalletAddressInput();
            loadTaskHistory();
        });

        async function loadTaskHistory() {
            try {
                const response = await fetch('/api/tasks/history');
                if (!response.ok) return;

                const data = await response.json();
                if (data.tasks && data.tasks.length > 0) {
                    // Convert saved tasks to runningTasks format
                    runningTasks = data.tasks.map(t => ({
                        id: t.id,
                        type: t.type,
                        name: t.name,
                        description: t.description,
                        status: t.status,
                        startTime: new Date(t.startTime),
                        endTime: t.endTime ? new Date(t.endTime) : null,
                        result: t.result,
                        walletActivityResult: t.walletActivityResult,
                        error: t.error,
                        markets: [] // Not needed for display
                    }));
                    taskIdCounter = data.lastTaskId || 0;
                    updateRunningTasksUI();
                }
            } catch (err) {
                console.error('Failed to load task history:', err);
            }
        }

        async function saveTaskHistory() {
            try {
                // Only save completed/failed tasks
                const tasksToSave = runningTasks
                    .filter(t => t.status !== 'running')
                    .map(t => ({
                        id: t.id,
                        type: t.type,
                        name: t.name,
                        description: t.description,
                        status: t.status,
                        startTime: t.startTime,
                        endTime: t.endTime,
                        result: t.result,
                        walletActivityResult: t.walletActivityResult,
                        error: t.error
                    }));

                await fetch('/api/tasks/history', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({
                        tasks: tasksToSave,
                        lastTaskId: taskIdCounter
                    })
                });
            } catch (err) {
                console.error('Failed to save task history:', err);
            }
        }

        async function checkAuthStatus() {
            try {
                const response = await fetch('/api/auth/status');
                if (!response.ok) {
                    // Auth not configured - allow access
                    showMainContent();
                    return;
                }
                authState = await response.json();
                updateAuthUI();
            } catch (err) {
                console.error('Auth check failed:', err);
                showMainContent();
            }
        }

        function updateAuthUI() {
            const authSection = document.getElementById('authSection');
            const loginPrompt = document.getElementById('loginPrompt');
            const mainContent = document.getElementById('mainContent');
            const statusEl = document.getElementById('authStatus');

            if (!authState || !authState.enabled) {
                authSection.style.display = 'none';
                showMainContent();
                return;
            }

            authSection.style.display = 'flex';

            if (!authState.has_credentials) {
                // No credentials yet - allow access
                statusEl.textContent = 'No authentication configured';
                showMainContent();
                return;
            }

            if (authState.authenticated) {
                statusEl.textContent = 'Logged in as ' + (authState.username || 'User');
                statusEl.classList.add('authenticated');
                showMainContent();
            } else {
                statusEl.textContent = 'Not logged in';
                loginPrompt.style.display = 'block';
                mainContent.style.display = 'none';
            }
        }

        function showMainContent() {
            document.getElementById('mainContent').style.display = 'flex';
            document.getElementById('loginPrompt').style.display = 'none';
        }

        function setupMarketSearch() {
            const input = document.getElementById('marketSearchInput');
            const resultsDiv = document.getElementById('searchResults');

            input.addEventListener('keydown', (e) => {
                if (e.key === 'Enter') {
                    e.preventDefault();
                    searchMarkets(input.value);
                }
            });

            input.addEventListener('focus', () => {
                if (resultsDiv.children.length > 0) {
                    resultsDiv.classList.add('show');
                }
            });

            document.addEventListener('click', (e) => {
                if (!e.target.closest('.market-search')) {
                    resultsDiv.classList.remove('show');
                }
            });
        }

        function setDateFilter(period) {
            activeQuickFilter = period;
            const now = new Date();
            let fromDate = null;

            if (period === '1d') {
                fromDate = new Date(now.getTime() - 24 * 60 * 60 * 1000);
            } else if (period === '7d') {
                fromDate = new Date(now.getTime() - 7 * 24 * 60 * 60 * 1000);
            } else if (period === '14d') {
                fromDate = new Date(now.getTime() - 14 * 24 * 60 * 60 * 1000);
            }

            dateFilterFrom = fromDate ? fromDate.toISOString().split('T')[0] : null;
            dateFilterTo = null;

            // Update UI
            document.getElementById('dateFrom').value = dateFilterFrom || '';
            document.getElementById('dateTo').value = '';
            updateQuickFilterButtons();
        }

        function updateDateFilter() {
            dateFilterFrom = document.getElementById('dateFrom').value || null;
            dateFilterTo = document.getElementById('dateTo').value || null;
            activeQuickFilter = null;
            updateQuickFilterButtons();
        }

        function clearDateFilter() {
            dateFilterFrom = null;
            dateFilterTo = null;
            activeQuickFilter = 'all';
            document.getElementById('dateFrom').value = '';
            document.getElementById('dateTo').value = '';
            updateQuickFilterButtons();
        }

        function updateQuickFilterButtons() {
            document.querySelectorAll('.quick-filters .filter-btn').forEach(btn => {
                btn.classList.remove('active');
                if (activeQuickFilter && btn.textContent.toLowerCase().includes(
                    activeQuickFilter === '1d' ? '24h' :
                    activeQuickFilter === '7d' ? '7 days' :
                    activeQuickFilter === '14d' ? '2 weeks' :
                    activeQuickFilter === 'all' ? 'all time' : ''
                )) {
                    btn.classList.add('active');
                }
            });
        }

        async function searchMarkets(query) {
            const input = document.getElementById('marketSearchInput');
            const resultsDiv = document.getElementById('searchResults');
            const loading = document.getElementById('searchLoading');

            if (query.length < 2) {
                showToast('Enter at least 2 characters to search', 'error');
                return;
            }

            // Show loading state
            input.disabled = true;
            loading.classList.add('show');
            resultsDiv.classList.remove('show');

            try {
                // Build URL with date filters
                let url = '/api/tasks/markets/search?q=' + encodeURIComponent(query);
                if (dateFilterFrom) {
                    url += '&closedAfter=' + encodeURIComponent(dateFilterFrom);
                }
                if (dateFilterTo) {
                    url += '&closedBefore=' + encodeURIComponent(dateFilterTo);
                }

                const response = await fetch(url);
                if (!response.ok) {
                    const err = await response.json();
                    if (err.error === 'authentication_required') {
                        showToast('Please log in to search markets', 'error');
                        return;
                    }
                    throw new Error(err.error || 'Search failed');
                }

                const data = await response.json();
                displaySearchResults(data.markets);
            } catch (err) {
                showToast('Search failed: ' + err.message, 'error');
            } finally {
                // Hide loading state
                input.disabled = false;
                loading.classList.remove('show');
                input.focus();
            }
        }

        function displaySearchResults(markets) {
            const resultsDiv = document.getElementById('searchResults');
            resultsDiv.innerHTML = '';

            if (markets.length === 0) {
                resultsDiv.innerHTML = '<div style="padding: 12px; color: var(--text-secondary);">No markets found</div>';
                resultsDiv.classList.add('show');
                return;
            }

            markets.forEach(market => {
                // Skip already selected markets
                if (selectedMarkets.find(m => m.conditionId === market.conditionId)) {
                    return;
                }

                const item = document.createElement('div');
                item.className = 'search-result-item';
                item.innerHTML = ` + "`" + `
                    ${market.image ? '<img src="' + market.image + '" alt="">' : ''}
                    <div class="search-result-info">
                        <div class="search-result-title">${escapeHtml(market.title)}</div>
                        <div class="search-result-outcome">${market.winningOutcome ? 'Winner: ' + market.winningOutcome : '<span style="color: #d29922;">Undecided</span>'}</div>
                    </div>
                ` + "`" + `;
                item.onclick = () => selectMarket(market);
                resultsDiv.appendChild(item);
            });

            resultsDiv.classList.add('show');
        }

        function selectMarket(market) {
            if (selectedMarkets.find(m => m.conditionId === market.conditionId)) {
                return;
            }

            if (selectedMarkets.length >= 20) {
                showToast('Maximum 20 markets can be selected', 'error');
                return;
            }

            selectedMarkets.push(market);
            updateSelectedMarketsUI();
            document.getElementById('searchResults').classList.remove('show');
            document.getElementById('marketSearchInput').value = '';
        }

        function removeMarket(conditionId) {
            selectedMarkets = selectedMarkets.filter(m => m.conditionId !== conditionId);
            updateSelectedMarketsUI();
        }

        function updateSelectedMarketsUI() {
            const container = document.getElementById('selectedMarkets');
            const runBtn = document.getElementById('runTaskBtn');

            container.innerHTML = '';
            selectedMarkets.forEach(market => {
                const chip = document.createElement('div');
                chip.className = 'market-chip';
                chip.innerHTML = ` + "`" + `
                    <span>${escapeHtml(market.title.substring(0, 40))}${market.title.length > 40 ? '...' : ''}</span>
                    <span class="remove" onclick="removeMarket('${market.conditionId}')">&times;</span>
                ` + "`" + `;
                container.appendChild(chip);
            });

            runBtn.disabled = selectedMarkets.length < 2;
        }

        function runTask() {
            if (selectedMarkets.length < 2) {
                showToast('Select at least 2 markets', 'error');
                return;
            }

            // Create task entry
            const taskId = ++taskIdCounter;
            const marketsCopy = [...selectedMarkets];
            const minMarketsWon = parseInt(document.getElementById('minMarketsWon').value) || 2;

            const task = {
                id: taskId,
                type: 'multimarket-winners',
                name: 'Multi-Market Winners',
                description: marketsCopy.length + ' markets, min ' + minMarketsWon + ' wins',
                status: 'running',
                startTime: new Date(),
                markets: marketsCopy,
                minMarketsWon: minMarketsWon,
                result: null,
                error: null
            };

            runningTasks.push(task);
            updateRunningTasksUI();
            showToast('Task started', 'success');

            // Run task asynchronously
            executeTask(task);
        }

        async function executeTask(task) {
            try {
                const response = await fetch('/api/tasks/multimarket-winners', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({
                        markets: task.markets.map(m => ({
                            conditionId: m.conditionId,
                            title: m.title,
                            winningOutcome: m.winningOutcome
                        })),
                        minMarketsWon: task.minMarketsWon
                    })
                });

                if (!response.ok) {
                    const err = await response.json();
                    throw new Error(err.error || 'Task failed');
                }

                const data = await response.json();
                task.status = 'completed';
                task.result = data;
                task.endTime = new Date();
                showToast('Task completed: ' + data.walletsMatchingCriteria + ' winners found', 'success');
            } catch (err) {
                task.status = 'failed';
                task.error = err.message;
                task.endTime = new Date();
                showToast('Task failed: ' + err.message, 'error');
            }

            updateRunningTasksUI();
            saveTaskHistory();
        }

        function updateRunningTasksUI() {
            const runningSection = document.getElementById('runningTasksSection');
            const runningList = document.getElementById('runningTasksList');
            const finishedSection = document.getElementById('finishedTasksSection');
            const finishedList = document.getElementById('finishedTasksList');

            // Separate running and finished tasks
            const running = runningTasks.filter(t => t.status === 'running');
            const finished = runningTasks.filter(t => t.status !== 'running');

            // Update running tasks section
            if (running.length === 0) {
                runningSection.classList.remove('show');
            } else {
                runningSection.classList.add('show');
                runningList.innerHTML = '';

                running.forEach(task => {
                    const item = document.createElement('div');
                    item.className = 'running-task-item status-running';
                    item.onclick = () => openTaskModal(task.id);

                    const icon = '<div class="task-spinner"></div>';
                    const meta = 'Running...';

                    item.innerHTML = icon + '<div class="running-task-info"><div class="running-task-name">' + escapeHtml(task.name) + '</div><div class="running-task-meta">' + escapeHtml(meta) + '</div></div>';
                    runningList.appendChild(item);
                });
            }

            // Update finished tasks section
            if (finished.length === 0) {
                finishedSection.classList.remove('show');
            } else {
                finishedSection.classList.add('show');
                finishedList.innerHTML = '';

                // Show most recent first
                const sorted = [...finished].sort((a, b) => (b.endTime || b.startTime) - (a.endTime || a.startTime));

                sorted.forEach(task => {
                    const item = document.createElement('div');
                    item.className = 'finished-task-item status-' + task.status;

                    let icon = '';
                    if (task.status === 'completed') {
                        icon = '<div class="task-icon success">&#10003;</div>';
                    } else if (task.status === 'failed') {
                        icon = '<div class="task-icon error">&#10007;</div>';
                    }

                    let meta = '';
                    if (task.endTime) {
                        const duration = ((task.endTime - task.startTime) / 1000).toFixed(1);
                        meta = duration + 's';
                        if (task.result) {
                            meta += ' - ' + task.result.walletsMatchingCriteria + ' winners';
                        } else if (task.walletActivityResult) {
                            meta += ' - $' + formatNumber(task.walletActivityResult.totalCostBasis) + ' cost basis';
                        }
                    }

                    item.innerHTML = icon + '<div class="running-task-info" onclick="openTaskModal(' + task.id + ')"><div class="running-task-name">' + escapeHtml(task.name) + '</div><div class="running-task-meta">' + escapeHtml(meta) + '</div></div><button class="delete-btn" onclick="event.stopPropagation(); deleteTask(' + task.id + ')" title="Delete task">&times;</button>';
                    finishedList.appendChild(item);
                });
            }
        }

        function deleteTask(taskId) {
            const task = runningTasks.find(t => t.id === taskId);
            if (!task) return;

            // Don't allow deleting running tasks
            if (task.status === 'running') {
                showToast('Cannot delete a running task', 'error');
                return;
            }

            // Remove from array
            runningTasks = runningTasks.filter(t => t.id !== taskId);

            // Update UI
            updateRunningTasksUI();

            // Sync to gist
            saveTaskHistory();

            showToast('Task deleted', 'success');
        }

        function openTaskModal(taskId) {
            const task = runningTasks.find(t => t.id === taskId);
            if (!task) return;

            currentModalTask = task;

            const modal = document.getElementById('taskModal');
            const title = document.getElementById('modalTitle');
            const body = document.getElementById('modalBody');
            const exportBtn = document.getElementById('exportCsvBtn');

            title.textContent = task.name + ' #' + task.id;

            // Show export button only for completed tasks with results
            exportBtn.style.display = (task.status === 'completed' && task.result) ? 'inline-block' : 'none';

            let statusClass = task.status;
            let statusText = task.status.charAt(0).toUpperCase() + task.status.slice(1);

            let html = '<div class="modal-status ' + statusClass + '">' + statusText + '</div>';
            html += '<p style="margin-bottom: 16px; color: var(--text-secondary);">' + escapeHtml(task.description) + '</p>';

            if (task.status === 'failed' && task.error) {
                html += '<div style="padding: 12px; background: rgba(248, 81, 73, 0.1); border-radius: 6px; color: var(--error); margin-bottom: 16px;">' + escapeHtml(task.error) + '</div>';
            }

            if (task.result) {
                const r = task.result;
                html += '<div class="results-summary">';
                html += '<div class="summary-stat"><span class="value">' + r.totalWalletsAnalyzed + '</span><span class="label">Total Wallets</span></div>';
                html += '<div class="summary-stat"><span class="value">' + r.walletsMatchingCriteria + '</span><span class="label">Multi-Winners</span></div>';
                html += '<div class="summary-stat"><span class="value">' + r.marketsProcessed + '</span><span class="label">Markets</span></div>';
                html += '<div class="summary-stat"><span class="value">' + (r.durationMs / 1000).toFixed(1) + 's</span><span class="label">Duration</span></div>';
                html += '</div>';

                if (r.results && r.results.length > 0) {
                    html += '<table class="results-table" style="margin-top: 16px;"><thead><tr><th>Wallet</th><th>Markets Won</th><th>Markets</th></tr></thead><tbody>';
                    r.results.forEach(wallet => {
                        html += '<tr>';
                        html += '<td class="wallet-address"><a href="' + wallet.profileUrl + '" target="_blank">' + wallet.address.substring(0, 6) + '...' + wallet.address.substring(wallet.address.length - 4) + '</a></td>';
                        html += '<td>' + wallet.marketsWon + '</td>';
                        html += '<td><div class="markets-list">';
                        wallet.markets.forEach(m => {
                            html += '<span class="market-tag" title="' + escapeHtml(m.title) + '">' + escapeHtml(m.outcome) + '</span>';
                        });
                        html += '</div></td></tr>';
                    });
                    html += '</tbody></table>';
                } else {
                    html += '<p style="color: var(--text-secondary); margin-top: 16px;">No wallets matched the criteria.</p>';
                }

                if (r.errors && r.errors.length > 0) {
                    html += '<div style="margin-top: 16px;"><h4 style="font-size: 14px; margin-bottom: 8px; color: var(--warning);">Warnings</h4>';
                    r.errors.forEach(err => {
                        html += '<div style="font-size: 12px; color: var(--text-secondary); margin-bottom: 4px;">' + escapeHtml(err) + '</div>';
                    });
                    html += '</div>';
                }
            } else if (task.status === 'running') {
                html += '<div style="display: flex; align-items: center; gap: 12px; color: var(--text-secondary);"><div class="spinner"></div>Processing markets...</div>';
            }

            body.innerHTML = html;
            modal.classList.add('show');
        }

        function closeModal(event) {
            if (event && event.target !== event.currentTarget) return;
            document.getElementById('taskModal').classList.remove('show');
        }

        // Close modal on Escape key
        document.addEventListener('keydown', (e) => {
            if (e.key === 'Escape') {
                closeModal();
            }
        });

        function displayResults(data) {
            const resultsSection = document.getElementById('resultsSection');
            const summary = document.getElementById('resultsSummary');
            const tbody = document.getElementById('resultsBody');

            summary.innerHTML = ` + "`" + `
                <div class="summary-stat">
                    <span class="value">${data.totalWalletsAnalyzed}</span>
                    <span class="label">Total Wallets Analyzed</span>
                </div>
                <div class="summary-stat">
                    <span class="value">${data.walletsMatchingCriteria}</span>
                    <span class="label">Multi-Market Winners</span>
                </div>
                <div class="summary-stat">
                    <span class="value">${data.marketsProcessed}</span>
                    <span class="label">Markets Processed</span>
                </div>
                <div class="summary-stat">
                    <span class="value">${(data.durationMs / 1000).toFixed(1)}s</span>
                    <span class="label">Duration</span>
                </div>
            ` + "`" + `;

            renderResultsTable();
            resultsSection.classList.add('show');
        }

        function renderResultsTable() {
            const tbody = document.getElementById('resultsBody');
            tbody.innerHTML = '';

            // Sort results
            const sorted = [...results].sort((a, b) => {
                let cmp = 0;
                if (sortColumn === 'marketsWon') {
                    cmp = a.marketsWon - b.marketsWon;
                } else if (sortColumn === 'address') {
                    cmp = a.address.localeCompare(b.address);
                }
                return sortAsc ? cmp : -cmp;
            });

            sorted.forEach(wallet => {
                const row = document.createElement('tr');
                row.innerHTML = ` + "`" + `
                    <td class="wallet-address">
                        <a href="${wallet.profileUrl}" target="_blank">
                            ${wallet.address.substring(0, 6)}...${wallet.address.substring(wallet.address.length - 4)}
                        </a>
                    </td>
                    <td>${wallet.marketsWon}</td>
                    <td>
                        <div class="markets-list">
                            ${wallet.markets.map(m => ` + "`" + `
                                <span class="market-tag" title="${escapeHtml(m.title)}">
                                    ${escapeHtml(m.outcome)}
                                </span>
                            ` + "`" + `).join('')}
                        </div>
                    </td>
                ` + "`" + `;
                tbody.appendChild(row);
            });

            // Update sort indicators
            document.querySelectorAll('.results-table th').forEach(th => {
                th.classList.remove('sorted', 'asc');
            });
            const sortedTh = document.querySelector(` + "`" + `.results-table th[onclick="sortResults('${sortColumn}')"]` + "`" + `);
            if (sortedTh) {
                sortedTh.classList.add('sorted');
                if (sortAsc) sortedTh.classList.add('asc');
            }
        }

        function sortResults(column) {
            if (sortColumn === column) {
                sortAsc = !sortAsc;
            } else {
                sortColumn = column;
                sortAsc = false;
            }
            renderResultsTable();
        }

        function escapeHtml(str) {
            if (!str) return '';
            const div = document.createElement('div');
            div.textContent = str;
            return div.innerHTML;
        }

        function showToast(message, type = 'info') {
            const toast = document.createElement('div');
            toast.className = 'toast ' + type;
            toast.textContent = message;
            document.body.appendChild(toast);
            setTimeout(() => toast.remove(), 3000);
        }

        // Task switching
        function switchTask(taskId) {
            // Update sidebar
            document.querySelectorAll('.task-item').forEach(item => {
                item.classList.remove('active');
                if (item.dataset.task === taskId) {
                    item.classList.add('active');
                }
            });

            // Update panels
            document.querySelectorAll('.task-panel').forEach(panel => {
                panel.style.display = panel.id === taskId ? 'block' : 'none';
            });
        }

        // Wallet Activity functions
        function setupWalletAddressInput() {
            const input = document.getElementById('walletAddressInput');
            input.addEventListener('input', () => {
                const address = input.value.trim();
                if (address.length >= 40 && address.startsWith('0x')) {
                    selectWalletByAddress(address);
                }
            });

            input.addEventListener('keydown', (e) => {
                if (e.key === 'Enter') {
                    e.preventDefault();
                    const address = input.value.trim();
                    if (address.length >= 40 && address.startsWith('0x')) {
                        selectWalletByAddress(address);
                    } else {
                        showToast('Please enter a valid wallet address', 'error');
                    }
                }
            });
        }

        function setWalletDuration(duration) {
            walletDuration = duration;
            document.querySelectorAll('.duration-filters .filter-btn').forEach(btn => {
                btn.classList.remove('active');
            });
            document.querySelector(` + "`" + `.duration-filters .filter-btn[onclick="setWalletDuration('${duration}')"]` + "`" + `).classList.add('active');
        }

        function selectWalletByAddress(address) {
            selectedWallet = {
                address: address,
                name: null,
                pseudonym: null,
                profileImage: null
            };
            updateSelectedWalletUI();
            document.getElementById('walletAddressInput').value = '';
        }

        function removeSelectedWallet() {
            selectedWallet = null;
            updateSelectedWalletUI();
        }

        function updateSelectedWalletUI() {
            const container = document.getElementById('selectedWallet');
            const runBtn = document.getElementById('runWalletTaskBtn');

            if (!selectedWallet) {
                container.style.display = 'none';
                runBtn.disabled = true;
                return;
            }

            container.style.display = 'flex';
            container.innerHTML = ` + "`" + `
                ${selectedWallet.profileImage ? '<img src="' + selectedWallet.profileImage + '" alt="">' : '<div style="width:40px;height:40px;background:var(--bg-primary);border-radius:50%;display:flex;align-items:center;justify-content:center;font-size:18px;">&#128100;</div>'}
                <div class="selected-wallet-info">
                    <div class="selected-wallet-name">${escapeHtml(selectedWallet.name || selectedWallet.pseudonym || 'Unknown User')}</div>
                    <div class="selected-wallet-address">${selectedWallet.address}</div>
                </div>
                <span class="remove" onclick="removeSelectedWallet()">&times;</span>
            ` + "`" + `;
            runBtn.disabled = false;
        }

        function runWalletActivityTask() {
            if (!selectedWallet) {
                showToast('Select a wallet first', 'error');
                return;
            }

            const taskId = ++taskIdCounter;
            const durationLabels = {
                '1d': '24 hours',
                '1w': '1 week',
                '2w': '2 weeks',
                '1m': '1 month',
                '3m': '3 months',
                '6m': '6 months',
                '1y': '1 year'
            };

            const task = {
                id: taskId,
                type: 'wallet-activity',
                name: 'Wallet Activity',
                description: (selectedWallet.name || selectedWallet.address.substring(0, 10) + '...') + ' - ' + durationLabels[walletDuration],
                status: 'running',
                startTime: new Date(),
                walletAddress: selectedWallet.address,
                duration: walletDuration,
                walletActivityResult: null,
                error: null
            };

            runningTasks.push(task);
            updateRunningTasksUI();
            showToast('Task started', 'success');

            executeWalletActivityTask(task);
        }

        async function executeWalletActivityTask(task) {
            try {
                const response = await fetch('/api/tasks/wallet-activity', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({
                        walletAddress: task.walletAddress,
                        duration: task.duration
                    })
                });

                if (!response.ok) {
                    const err = await response.json();
                    throw new Error(err.error || 'Task failed');
                }

                const data = await response.json();
                task.status = 'completed';
                task.walletActivityResult = data;
                task.endTime = new Date();
                showToast('Task completed: ' + data.totalMarkets + ' markets analyzed', 'success');
            } catch (err) {
                task.status = 'failed';
                task.error = err.message;
                task.endTime = new Date();
                showToast('Task failed: ' + err.message, 'error');
            }

            updateRunningTasksUI();
            saveTaskHistory();
        }

        // Update openTaskModal to handle wallet activity results
        const originalOpenTaskModal = openTaskModal;
        openTaskModal = function(taskId) {
            const task = runningTasks.find(t => t.id === taskId);
            if (!task) return;

            // Check if it's a wallet activity task
            if (task.type === 'wallet-activity') {
                openWalletActivityModal(task);
                return;
            }

            // For other tasks, call the original
            originalOpenTaskModal(taskId);
        };

        function openWalletActivityModal(task) {
            currentModalTask = task;

            const modal = document.getElementById('taskModal');
            const title = document.getElementById('modalTitle');
            const body = document.getElementById('modalBody');
            const exportBtn = document.getElementById('exportCsvBtn');

            title.textContent = task.name + ' #' + task.id;

            // Show export button only for completed tasks with results
            exportBtn.style.display = (task.status === 'completed' && task.walletActivityResult) ? 'inline-block' : 'none';

            let statusClass = task.status;
            let statusText = task.status.charAt(0).toUpperCase() + task.status.slice(1);

            let html = '<div class="modal-status ' + statusClass + '">' + statusText + '</div>';
            html += '<p style="margin-bottom: 16px; color: var(--text-secondary);">' + escapeHtml(task.description) + '</p>';

            if (task.status === 'failed' && task.error) {
                html += '<div style="padding: 12px; background: rgba(248, 81, 73, 0.1); border-radius: 6px; color: var(--error); margin-bottom: 16px;">' + escapeHtml(task.error) + '</div>';
            }

            if (task.walletActivityResult) {
                const r = task.walletActivityResult;
                html += '<div class="results-summary">';
                html += '<div class="summary-stat"><span class="value">$' + formatNumber(r.totalCostBasis) + '</span><span class="label">Total Cost Basis</span></div>';
                html += '<div class="summary-stat"><span class="value">' + r.totalTradeCount + '</span><span class="label">Total Trades</span></div>';
                html += '<div class="summary-stat"><span class="value">' + r.totalMarkets + '</span><span class="label">Markets</span></div>';
                html += '<div class="summary-stat"><span class="value">' + (r.durationMs / 1000).toFixed(1) + 's</span><span class="label">Duration</span></div>';
                html += '</div>';

                if (r.markets && r.markets.length > 0) {
                    html += '<table class="results-table" style="margin-top: 16px;"><thead><tr><th>Market</th><th>Cost Basis</th><th>Trades</th></tr></thead><tbody>';
                    r.markets.forEach(market => {
                        html += '<tr class="market-cost-row">';
                        html += '<td>';
                        html += '<div>' + escapeHtml(market.title || market.conditionId.substring(0, 12) + '...') + '</div>';
                        if (market.outcomes && Object.keys(market.outcomes).length > 0) {
                            html += '<div class="outcome-breakdown">';
                            for (const [outcome, data] of Object.entries(market.outcomes)) {
                                html += '<span>' + escapeHtml(outcome) + ': $' + formatNumber(data.costBasis) + '</span>';
                            }
                            html += '</div>';
                        }
                        html += '</td>';
                        html += '<td class="cost-basis-value">$' + formatNumber(market.totalCostBasis) + '</td>';
                        html += '<td>' + market.tradeCount + '</td>';
                        html += '</tr>';
                    });
                    html += '</tbody></table>';
                } else {
                    html += '<p style="color: var(--text-secondary); margin-top: 16px;">No activity found in the selected time period.</p>';
                }

                if (r.errors && r.errors.length > 0) {
                    html += '<div style="margin-top: 16px;"><h4 style="font-size: 14px; margin-bottom: 8px; color: var(--warning);">Warnings</h4>';
                    r.errors.forEach(err => {
                        html += '<div style="font-size: 12px; color: var(--text-secondary); margin-bottom: 4px;">' + escapeHtml(err) + '</div>';
                    });
                    html += '</div>';
                }
            } else if (task.status === 'running') {
                html += '<div style="display: flex; align-items: center; gap: 12px; color: var(--text-secondary);"><div class="spinner"></div>Analyzing wallet activity...</div>';
            }

            body.innerHTML = html;
            modal.classList.add('show');
        }

        function formatNumber(num) {
            if (num >= 1000000) {
                return (num / 1000000).toFixed(2) + 'M';
            } else if (num >= 1000) {
                return (num / 1000).toFixed(2) + 'K';
            } else {
                return num.toFixed(2);
            }
        }

        // CSV Export functions
        function exportTaskToCsv() {
            if (!currentModalTask) return;

            let csv = '';
            let filename = '';

            if (currentModalTask.type === 'wallet-activity' && currentModalTask.walletActivityResult) {
                csv = generateWalletActivityCsv(currentModalTask);
                filename = 'wallet-activity-' + currentModalTask.walletActivityResult.walletAddress.substring(0, 10) + '.csv';
            } else if (currentModalTask.result) {
                csv = generateMultiMarketWinnersCsv(currentModalTask);
                filename = 'multi-market-winners-' + currentModalTask.id + '.csv';
            } else {
                showToast('No data to export', 'error');
                return;
            }

            downloadCsv(csv, filename);
            showToast('CSV exported', 'success');
        }

        function generateMultiMarketWinnersCsv(task) {
            const r = task.result;
            let csv = 'Wallet Address,Profile URL,Markets Won,Market Titles,Outcomes\n';

            r.results.forEach(wallet => {
                const titles = wallet.markets.map(m => '"' + (m.title || '').replace(/"/g, '""') + '"').join('; ');
                const outcomes = wallet.markets.map(m => m.outcome).join('; ');
                csv += wallet.address + ',' + wallet.profileUrl + ',' + wallet.marketsWon + ',"' + titles + '","' + outcomes + '"\n';
            });

            return csv;
        }

        function generateWalletActivityCsv(task) {
            const r = task.walletActivityResult;
            let csv = 'Market Title,Condition ID,Total Cost Basis,Trade Count,Outcomes\n';

            r.markets.forEach(market => {
                const title = (market.title || market.conditionId).replace(/"/g, '""');
                let outcomes = '';
                if (market.outcomes) {
                    outcomes = Object.entries(market.outcomes)
                        .map(([outcome, data]) => outcome + ': $' + data.costBasis.toFixed(2))
                        .join('; ');
                }
                csv += '"' + title + '",' + market.conditionId + ',' + market.totalCostBasis.toFixed(2) + ',' + market.tradeCount + ',"' + outcomes + '"\n';
            });

            // Add summary row
            csv += '\n';
            csv += 'SUMMARY\n';
            csv += 'Wallet Address,' + r.walletAddress + '\n';
            csv += 'Time Period,' + task.duration + '\n';
            csv += 'Total Cost Basis,$' + r.totalCostBasis.toFixed(2) + '\n';
            csv += 'Total Trades,' + r.totalTradeCount + '\n';
            csv += 'Total Markets,' + r.totalMarkets + '\n';

            return csv;
        }

        function downloadCsv(csv, filename) {
            const blob = new Blob([csv], { type: 'text/csv;charset=utf-8;' });
            const link = document.createElement('a');
            const url = URL.createObjectURL(blob);
            link.setAttribute('href', url);
            link.setAttribute('download', filename);
            link.style.visibility = 'hidden';
            document.body.appendChild(link);
            link.click();
            document.body.removeChild(link);
        }

        // Market Holders Task
        let selectedHoldersMarket = null;

        function setupHoldersMarketSearch() {
            const input = document.getElementById('holdersMarketSearchInput');
            const resultsDiv = document.getElementById('holdersSearchResults');

            input.addEventListener('keydown', (e) => {
                if (e.key === 'Enter') {
                    e.preventDefault();
                    searchMarketsForHolders(input.value);
                }
            });

            input.addEventListener('focus', () => {
                if (resultsDiv.children.length > 0) {
                    resultsDiv.classList.add('show');
                }
            });

            document.addEventListener('click', (e) => {
                if (!e.target.closest('#market-holders .market-search')) {
                    resultsDiv.classList.remove('show');
                }
            });
        }

        async function searchMarketsForHolders(query) {
            const input = document.getElementById('holdersMarketSearchInput');
            const resultsDiv = document.getElementById('holdersSearchResults');
            const loading = document.getElementById('holdersSearchLoading');

            if (query.length < 2) {
                showToast('Enter at least 2 characters to search', 'error');
                return;
            }

            input.disabled = true;
            loading.classList.add('show');
            resultsDiv.classList.remove('show');

            try {
                // Search all markets (open and closed)
                const response = await fetch('/api/tasks/markets/search-all?q=' + encodeURIComponent(query));
                if (!response.ok) {
                    const err = await response.json();
                    throw new Error(err.error || 'Search failed');
                }

                const data = await response.json();
                displayHoldersSearchResults(data.markets);
            } catch (err) {
                showToast('Search failed: ' + err.message, 'error');
            } finally {
                input.disabled = false;
                loading.classList.remove('show');
                input.focus();
            }
        }

        function displayHoldersSearchResults(markets) {
            const resultsDiv = document.getElementById('holdersSearchResults');
            resultsDiv.innerHTML = '';

            if (markets.length === 0) {
                resultsDiv.innerHTML = '<div style="padding: 12px; color: var(--text-secondary);">No markets found</div>';
                resultsDiv.classList.add('show');
                return;
            }

            markets.forEach(market => {
                const item = document.createElement('div');
                item.className = 'search-result-item';
                const statusBadge = market.active
                    ? '<span style="background: rgba(63, 185, 80, 0.2); color: var(--success); padding: 2px 6px; border-radius: 4px; font-size: 10px; margin-left: 6px;">OPEN</span>'
                    : '<span style="background: rgba(139, 148, 158, 0.2); color: var(--text-secondary); padding: 2px 6px; border-radius: 4px; font-size: 10px; margin-left: 6px;">CLOSED</span>';
                item.innerHTML = ` + "`" + `
                    ${market.image ? '<img src="' + market.image + '" alt="">' : ''}
                    <div class="search-result-info">
                        <div class="search-result-title">${escapeHtml(market.title)}${statusBadge}</div>
                        <div class="search-result-outcome" style="color: var(--text-secondary);">${market.conditionId.substring(0, 12)}...</div>
                    </div>
                ` + "`" + `;
                item.onclick = () => selectHoldersMarket(market);
                resultsDiv.appendChild(item);
            });

            resultsDiv.classList.add('show');
        }

        function selectHoldersMarket(market) {
            selectedHoldersMarket = market;
            updateSelectedHoldersMarketUI();
            document.getElementById('holdersSearchResults').classList.remove('show');
            document.getElementById('holdersMarketSearchInput').value = '';
        }

        function removeSelectedHoldersMarket() {
            selectedHoldersMarket = null;
            updateSelectedHoldersMarketUI();
        }

        function updateSelectedHoldersMarketUI() {
            const container = document.getElementById('selectedHoldersMarket');
            const runBtn = document.getElementById('runHoldersTaskBtn');

            if (!selectedHoldersMarket) {
                container.style.display = 'none';
                runBtn.disabled = true;
                return;
            }

            container.style.display = 'flex';
            container.innerHTML = ` + "`" + `
                ${selectedHoldersMarket.image ? '<img src="' + selectedHoldersMarket.image + '" alt="" style="width:40px;height:40px;border-radius:8px;object-fit:cover;">' : '<div style="width:40px;height:40px;background:var(--bg-primary);border-radius:8px;display:flex;align-items:center;justify-content:center;font-size:18px;">&#128200;</div>'}
                <div class="selected-wallet-info">
                    <div class="selected-wallet-name">${escapeHtml(selectedHoldersMarket.title.substring(0, 50))}${selectedHoldersMarket.title.length > 50 ? '...' : ''}</div>
                    <div class="selected-wallet-address">${selectedHoldersMarket.conditionId}</div>
                </div>
                <span class="remove" onclick="removeSelectedHoldersMarket()">&times;</span>
            ` + "`" + `;
            runBtn.disabled = false;
        }

        function runMarketHoldersTask() {
            if (!selectedHoldersMarket) {
                showToast('Select a market first', 'error');
                return;
            }

            const taskId = ++taskIdCounter;
            const topN = parseInt(document.getElementById('topHoldersCount').value) || 50;

            const task = {
                id: taskId,
                type: 'market-holders',
                name: 'Market Holders',
                description: selectedHoldersMarket.title.substring(0, 40) + (selectedHoldersMarket.title.length > 40 ? '...' : ''),
                status: 'running',
                startTime: new Date(),
                conditionId: selectedHoldersMarket.conditionId,
                topN: topN,
                marketHoldersResult: null,
                error: null
            };

            runningTasks.push(task);
            updateRunningTasksUI();
            showToast('Task started', 'success');

            executeMarketHoldersTask(task);
        }

        async function executeMarketHoldersTask(task) {
            try {
                const response = await fetch('/api/tasks/market-holders', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({
                        conditionId: task.conditionId,
                        topN: task.topN
                    })
                });

                if (!response.ok) {
                    const err = await response.json();
                    throw new Error(err.error || 'Task failed');
                }

                const data = await response.json();
                task.status = 'completed';
                task.marketHoldersResult = data;
                task.endTime = new Date();

                const totalHolders = data.outcomes.reduce((sum, o) => sum + o.totalHolders, 0);
                showToast('Task completed: ' + totalHolders + ' holders found', 'success');
            } catch (err) {
                task.status = 'failed';
                task.error = err.message;
                task.endTime = new Date();
                showToast('Task failed: ' + err.message, 'error');
            }

            updateRunningTasksUI();
            saveTaskHistory();
        }

        function openMarketHoldersModal(task) {
            currentModalTask = task;

            const modal = document.getElementById('taskModal');
            const title = document.getElementById('modalTitle');
            const body = document.getElementById('modalBody');
            const exportBtn = document.getElementById('exportCsvBtn');

            title.textContent = task.name + ' #' + task.id;
            exportBtn.style.display = (task.status === 'completed' && task.marketHoldersResult) ? 'inline-block' : 'none';

            let statusClass = task.status;
            let statusText = task.status.charAt(0).toUpperCase() + task.status.slice(1);

            let html = '<div class="modal-status ' + statusClass + '">' + statusText + '</div>';
            html += '<p style="margin-bottom: 16px; color: var(--text-secondary);">' + escapeHtml(task.description) + '</p>';

            if (task.status === 'failed' && task.error) {
                html += '<div style="padding: 12px; background: rgba(248, 81, 73, 0.1); border-radius: 6px; color: var(--error); margin-bottom: 16px;">' + escapeHtml(task.error) + '</div>';
            }

            if (task.marketHoldersResult) {
                const r = task.marketHoldersResult;
                html += '<div class="results-summary">';
                html += '<div class="summary-stat"><span class="value">' + r.totalTraders + '</span><span class="label">Total Traders</span></div>';
                html += '<div class="summary-stat"><span class="value">' + r.tradesProcessed + '</span><span class="label">Trades Processed</span></div>';
                html += '<div class="summary-stat"><span class="value">' + r.outcomes.length + '</span><span class="label">Outcomes</span></div>';
                html += '<div class="summary-stat"><span class="value">' + (r.durationMs / 1000).toFixed(1) + 's</span><span class="label">Duration</span></div>';
                html += '</div>';

                if (r.title) {
                    html += '<h4 style="font-size: 14px; margin: 16px 0 8px 0;">' + escapeHtml(r.title) + '</h4>';
                }

                r.outcomes.forEach(outcome => {
                    html += '<div style="margin-top: 20px; padding: 16px; background: var(--bg-tertiary); border-radius: 8px;">';
                    html += '<h4 style="font-size: 14px; margin-bottom: 12px; color: var(--accent);">' + escapeHtml(outcome.outcome) + ' <span style="color: var(--text-secondary); font-weight: normal;">(' + outcome.totalHolders + ' holders)</span></h4>';

                    if (outcome.topHolders && outcome.topHolders.length > 0) {
                        html += '<table class="results-table"><thead><tr><th>Wallet</th><th>Shares</th><th>Avg Price</th><th>Cost Basis</th></tr></thead><tbody>';
                        outcome.topHolders.forEach(holder => {
                            html += '<tr>';
                            html += '<td class="wallet-address"><a href="' + holder.profileUrl + '" target="_blank">' + holder.wallet.substring(0, 6) + '...' + holder.wallet.substring(holder.wallet.length - 4) + '</a></td>';
                            html += '<td>' + formatNumber(holder.size) + '</td>';
                            html += '<td>$' + holder.avgPrice.toFixed(3) + '</td>';
                            html += '<td class="cost-basis-value">$' + formatNumber(holder.totalBought) + '</td>';
                            html += '</tr>';
                        });
                        html += '</tbody></table>';
                    } else {
                        html += '<p style="color: var(--text-secondary);">No holders found for this outcome.</p>';
                    }
                    html += '</div>';
                });

                if (r.errors && r.errors.length > 0) {
                    html += '<div style="margin-top: 16px;"><h4 style="font-size: 14px; margin-bottom: 8px; color: var(--warning);">Warnings</h4>';
                    r.errors.forEach(err => {
                        html += '<div style="font-size: 12px; color: var(--text-secondary); margin-bottom: 4px;">' + escapeHtml(err) + '</div>';
                    });
                    html += '</div>';
                }
            } else if (task.status === 'running') {
                html += '<div style="display: flex; align-items: center; gap: 12px; color: var(--text-secondary);"><div class="spinner"></div>Processing trades...</div>';
            }

            body.innerHTML = html;
            modal.classList.add('show');
        }

        // Update openTaskModal to handle market holders
        const originalOpenTaskModalBase = openTaskModal;
        openTaskModal = function(taskId) {
            const task = runningTasks.find(t => t.id === taskId);
            if (!task) return;

            if (task.type === 'market-holders') {
                openMarketHoldersModal(task);
                return;
            }

            originalOpenTaskModalBase(taskId);
        };

        // Update the running tasks UI to show market holders results
        const originalUpdateRunningTasksUI = updateRunningTasksUI;
        updateRunningTasksUI = function() {
            originalUpdateRunningTasksUI();

            // Update market holders task display in finished list
            runningTasks.filter(t => t.type === 'market-holders' && t.status !== 'running').forEach(task => {
                const item = document.querySelector(` + "`" + `.finished-task-item .running-task-info[onclick="openTaskModal(${task.id})"]` + "`" + `);
                if (item && task.marketHoldersResult) {
                    const meta = item.querySelector('.running-task-meta');
                    if (meta) {
                        const duration = ((task.endTime - task.startTime) / 1000).toFixed(1);
                        const totalHolders = task.marketHoldersResult.outcomes.reduce((sum, o) => sum + o.totalHolders, 0);
                        meta.textContent = duration + 's - ' + totalHolders + ' holders';
                    }
                }
            });
        };

        // Generate CSV for market holders
        function generateMarketHoldersCsv(task) {
            const r = task.marketHoldersResult;
            let csv = 'Outcome,Wallet Address,Profile URL,Shares,Avg Price,Total Bought,Total Sold,Trade Count\n';

            r.outcomes.forEach(outcome => {
                outcome.topHolders.forEach(holder => {
                    csv += '"' + outcome.outcome + '",' + holder.wallet + ',' + holder.profileUrl + ',' + holder.size.toFixed(2) + ',' + holder.avgPrice.toFixed(4) + ',' + holder.totalBought.toFixed(2) + ',' + holder.totalSold.toFixed(2) + ',' + holder.tradeCount + '\n';
                });
            });

            csv += '\nSUMMARY\n';
            csv += 'Market,' + (r.title || r.conditionId) + '\n';
            csv += 'Condition ID,' + r.conditionId + '\n';
            csv += 'Total Traders,' + r.totalTraders + '\n';
            csv += 'Trades Processed,' + r.tradesProcessed + '\n';

            return csv;
        }

        // Update CSV export to handle market holders
        const originalExportTaskToCsv = exportTaskToCsv;
        exportTaskToCsv = function() {
            if (!currentModalTask) return;

            if (currentModalTask.type === 'market-holders' && currentModalTask.marketHoldersResult) {
                const csv = generateMarketHoldersCsv(currentModalTask);
                const filename = 'market-holders-' + currentModalTask.conditionId.substring(0, 10) + '.csv';
                downloadCsv(csv, filename);
                showToast('CSV exported', 'success');
                return;
            }

            originalExportTaskToCsv();
        };

        // Initialize holders search on page load
        document.addEventListener('DOMContentLoaded', () => {
            setupHoldersMarketSearch();
        });
    </script>
</body>
</html>`
