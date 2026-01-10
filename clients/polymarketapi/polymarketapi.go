package polymarketapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"polybot/config"
	"strings"
	"time"

	"go.uber.org/zap"
)

type PolymarketApiClient struct {
	logger       *zap.Logger
	httpClient   *http.Client
	gammaBaseURL string
	dataBaseURL  string
}

func NewPolymarketApiClient(logger *zap.Logger, cfg *config.Config) *PolymarketApiClient {
	if logger == nil {
		logger = zap.NewNop()
	}

	return &PolymarketApiClient{
		logger: logger,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		gammaBaseURL: cfg.Polymarket.GammaAPIURL,
		dataBaseURL:  cfg.Polymarket.DataAPIURL,
	}
}

// ---- Gamma API types (minimal; add fields as you need) ----

type GammaEvent struct {
	ID          string        `json:"id"`
	Slug        string        `json:"slug"`
	Title       string        `json:"title"`
	Description string        `json:"description"`
	Markets     []GammaMarket `json:"markets"`
}

type GammaMarket struct {
	ID           string          `json:"id"`
	Slug         string          `json:"slug"`
	Question     string          `json:"question"`
	ConditionID  string          `json:"conditionId"`
	ClobTokenIDs json.RawMessage `json:"clobTokenIds"`

	// These are commonly present and very useful for labeling YES/NO.
	Outcomes      json.RawMessage `json:"outcomes"`
	OutcomePrices json.RawMessage `json:"outcomePrices"`

	// Volume metrics
	Volume24hr float64 `json:"volume24hr"`
	VolumeNum  float64 `json:"volumeNum"`

	// Status
	Active bool `json:"active"`
	Closed bool `json:"closed"`

	// Market image
	Image string `json:"image"`

	// Resolution info (for closed markets)
	WinningOutcome string `json:"winningOutcome,omitempty"`
	ClosedTime     string `json:"closedTime,omitempty"`
}

// GetOutcomes parses the Outcomes field and returns the outcome names.
func (m *GammaMarket) GetOutcomes() []string {
	if len(m.Outcomes) == 0 {
		return nil
	}

	// Try parsing as direct array
	var outcomes []string
	if err := json.Unmarshal(m.Outcomes, &outcomes); err == nil {
		return outcomes
	}

	// Try parsing as JSON string containing an array (e.g., "[\"Yes\", \"No\"]")
	var jsonStr string
	if err := json.Unmarshal(m.Outcomes, &jsonStr); err == nil {
		if err := json.Unmarshal([]byte(jsonStr), &outcomes); err == nil {
			return outcomes
		}
	}

	return nil
}

// GetOutcomePrices parses the OutcomePrices field and returns prices.
func (m *GammaMarket) GetOutcomePrices() []float64 {
	if len(m.OutcomePrices) == 0 {
		return nil
	}

	// Helper to parse string array to floats
	parseStrings := func(strs []string) []float64 {
		prices := make([]float64, len(strs))
		for i, s := range strs {
			fmt.Sscanf(s, "%f", &prices[i])
		}
		return prices
	}

	// Try parsing as array of floats
	var prices []float64
	if err := json.Unmarshal(m.OutcomePrices, &prices); err == nil {
		return prices
	}

	// Try parsing as array of strings (sometimes prices are strings)
	var priceStrs []string
	if err := json.Unmarshal(m.OutcomePrices, &priceStrs); err == nil {
		return parseStrings(priceStrs)
	}

	// Try parsing as JSON string containing an array (e.g., "[\"0\", \"1\"]")
	var jsonStr string
	if err := json.Unmarshal(m.OutcomePrices, &jsonStr); err == nil {
		// Try as float array inside string
		if err := json.Unmarshal([]byte(jsonStr), &prices); err == nil {
			return prices
		}
		// Try as string array inside string
		if err := json.Unmarshal([]byte(jsonStr), &priceStrs); err == nil {
			return parseStrings(priceStrs)
		}
	}

	return nil
}

// GetWinningOutcome determines which outcome won based on prices.
// For resolved markets, the winning outcome has price ~1.0.
// Returns the outcome name and its index, or empty string and -1 if not determined.
func (m *GammaMarket) GetWinningOutcome() (string, int) {
	if !m.Closed {
		return "", -1
	}

	// If WinningOutcome is explicitly set, use it
	if m.WinningOutcome != "" {
		outcomes := m.GetOutcomes()
		for i, o := range outcomes {
			if o == m.WinningOutcome {
				return o, i
			}
		}
		return m.WinningOutcome, 0
	}

	// Infer from prices - winning outcome has price near 1.0
	prices := m.GetOutcomePrices()
	outcomes := m.GetOutcomes()
	if len(prices) == 0 || len(outcomes) == 0 || len(prices) != len(outcomes) {
		return "", -1
	}

	winnerIdx := -1
	for i, p := range prices {
		if p >= 0.95 { // Price >= 95% means this outcome won
			winnerIdx = i
			break
		}
	}

	if winnerIdx >= 0 && winnerIdx < len(outcomes) {
		return outcomes[winnerIdx], winnerIdx
	}

	return "", -1
}

// GetTokenIDs parses the ClobTokenIDs field and returns the token IDs.
// Returns nil if parsing fails or no token IDs are present.
// Handles multiple Gamma API formats:
// - Direct array: ["token1", "token2"]
// - Array containing JSON string: ["[\"token1\", \"token2\"]"]
// - JSON string: "[\"token1\", \"token2\"]"
func (m *GammaMarket) GetTokenIDs() []string {
	if len(m.ClobTokenIDs) == 0 {
		return nil
	}

	// Try parsing as array of strings directly
	var tokenIDs []string
	if err := json.Unmarshal(m.ClobTokenIDs, &tokenIDs); err == nil && len(tokenIDs) > 0 {
		// Check if elements are themselves JSON arrays (nested encoding)
		// e.g., ["[\"token1\", \"token2\"]"] -> ["token1", "token2"]
		if len(tokenIDs) == 1 && len(tokenIDs[0]) > 0 && tokenIDs[0][0] == '[' {
			var nested []string
			if err := json.Unmarshal([]byte(tokenIDs[0]), &nested); err == nil && len(nested) > 0 {
				return nested
			}
		}
		// Check if ALL elements look like JSON arrays and flatten them
		var flattened []string
		allNested := true
		for _, t := range tokenIDs {
			if len(t) > 0 && t[0] == '[' {
				var nested []string
				if err := json.Unmarshal([]byte(t), &nested); err == nil {
					flattened = append(flattened, nested...)
					continue
				}
			}
			allNested = false
			break
		}
		if allNested && len(flattened) > 0 {
			return flattened
		}
		return tokenIDs
	}

	// Try parsing as a JSON string containing an array
	var jsonStr string
	if err := json.Unmarshal(m.ClobTokenIDs, &jsonStr); err == nil && jsonStr != "" {
		var innerTokenIDs []string
		if err := json.Unmarshal([]byte(jsonStr), &innerTokenIDs); err == nil && len(innerTokenIDs) > 0 {
			return innerTokenIDs
		}
	}

	return nil
}

// GetMarketByConditionID fetches a specific market by its condition ID.
func (c *PolymarketApiClient) GetMarketByConditionID(
	ctx context.Context,
	conditionID string,
) (*GammaMarket, error) {
	conditionID = strings.TrimSpace(conditionID)
	if conditionID == "" {
		return nil, fmt.Errorf("conditionID is empty")
	}

	u, err := url.Parse(c.gammaBaseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid gammaBaseURL: %w", err)
	}
	u.Path = "/markets"

	q := u.Query()
	q.Set("condition_id", conditionID)
	q.Set("limit", "1")
	u.RawQuery = q.Encode()

	var markets []GammaMarket
	if err := c.doGet(ctx, u.String(), &markets); err != nil {
		return nil, fmt.Errorf("get market by condition: %w", err)
	}

	if len(markets) == 0 {
		return nil, fmt.Errorf("market not found: %s", conditionID)
	}

	return &markets[0], nil
}

// GetTopMarketsByVolume fetches the top markets sorted by 24-hour trading volume.
func (c *PolymarketApiClient) GetTopMarketsByVolume(
	ctx context.Context,
	limit int,
) ([]GammaMarket, error) {
	return c.GetTopMarketsByVolumeFiltered(ctx, limit, nil)
}

// GetTopMarketsByVolumeFiltered fetches top markets filtered by category tag slugs.
// If categories is empty or nil, returns all categories.
// Categories are tag slugs like "sports", "politics", "crypto", etc.
func (c *PolymarketApiClient) GetTopMarketsByVolumeFiltered(
	ctx context.Context,
	limit int,
	categories []string,
) ([]GammaMarket, error) {
	if limit <= 0 {
		limit = 20
	}

	// If no category filter, use the markets endpoint directly
	if len(categories) == 0 {
		u, err := url.Parse(c.gammaBaseURL)
		if err != nil {
			return nil, fmt.Errorf("invalid gammaBaseURL: %w", err)
		}
		u.Path = "/markets"

		q := u.Query()
		q.Set("limit", fmt.Sprintf("%d", limit))
		q.Set("order", "volume24hr")
		q.Set("ascending", "false")
		q.Set("active", "true")
		u.RawQuery = q.Encode()

		var markets []GammaMarket
		if err := c.doGet(ctx, u.String(), &markets); err != nil {
			return nil, fmt.Errorf("get top markets: %w", err)
		}
		return markets, nil
	}

	// Fetch events for each category and extract markets
	marketMap := make(map[string]GammaMarket) // dedupe by condition ID
	for _, category := range categories {
		events, err := c.getEventsByTagSlug(ctx, category, limit*2) // fetch more events to get enough markets
		if err != nil {
			c.logger.Warn("failed to fetch events for category",
				zap.String("category", category),
				zap.Error(err),
			)
			continue
		}
		for _, event := range events {
			for _, market := range event.Markets {
				if market.ConditionID != "" && market.Active && !market.Closed {
					marketMap[market.ConditionID] = market
				}
			}
		}
	}

	// Convert map to slice and sort by volume
	markets := make([]GammaMarket, 0, len(marketMap))
	for _, m := range marketMap {
		markets = append(markets, m)
	}

	// Sort by volume (descending)
	for i := 0; i < len(markets)-1; i++ {
		for j := i + 1; j < len(markets); j++ {
			if markets[j].Volume24hr > markets[i].Volume24hr {
				markets[i], markets[j] = markets[j], markets[i]
			}
		}
	}

	// Limit results
	if len(markets) > limit {
		markets = markets[:limit]
	}

	return markets, nil
}

// SearchActiveMarkets searches for active (open) markets by text query.
func (c *PolymarketApiClient) SearchActiveMarkets(
	ctx context.Context,
	query string,
	limit int,
) ([]GammaMarket, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}

	if limit <= 0 {
		limit = 50
	}

	searchLower := strings.ToLower(query)
	marketMap := make(map[string]GammaMarket)

	// Search markets endpoint
	u, err := url.Parse(c.gammaBaseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid gammaBaseURL: %w", err)
	}
	u.Path = "/markets"

	pageSize := 200
	maxPages := 5

	for page := 0; page < maxPages && len(marketMap) < limit; page++ {
		select {
		case <-ctx.Done():
			break
		default:
		}

		q := u.Query()
		q.Set("limit", fmt.Sprintf("%d", pageSize))
		q.Set("active", "true")
		q.Set("closed", "false")
		q.Set("order", "volume24hr")
		q.Set("ascending", "false")
		q.Set("offset", fmt.Sprintf("%d", page*pageSize))
		u.RawQuery = q.Encode()

		var markets []GammaMarket
		if err := c.doGet(ctx, u.String(), &markets); err != nil {
			c.logger.Warn("failed to fetch active markets page", zap.Int("page", page), zap.Error(err))
			break
		}

		if len(markets) == 0 {
			break
		}

		for _, m := range markets {
			if m.ConditionID == "" {
				continue
			}
			// Text filter
			if strings.Contains(strings.ToLower(m.Question), searchLower) ||
				strings.Contains(strings.ToLower(m.Slug), searchLower) {
				marketMap[m.ConditionID] = m
			}
		}

		if len(markets) < pageSize {
			break
		}
	}

	// Also search events (markets are nested in events)
	u.Path = "/events"
	for page := 0; page < 3 && len(marketMap) < limit; page++ {
		select {
		case <-ctx.Done():
			break
		default:
		}

		q := u.Query()
		q.Set("limit", "100")
		q.Set("active", "true")
		q.Set("order", "volume24hr")
		q.Set("ascending", "false")
		q.Set("offset", fmt.Sprintf("%d", page*100))
		u.RawQuery = q.Encode()

		var events []GammaEvent
		if err := c.doGet(ctx, u.String(), &events); err != nil {
			break
		}

		if len(events) == 0 {
			break
		}

		for _, event := range events {
			eventMatches := strings.Contains(strings.ToLower(event.Title), searchLower) ||
				strings.Contains(strings.ToLower(event.Slug), searchLower)

			for _, m := range event.Markets {
				if m.ConditionID == "" || m.Closed || !m.Active {
					continue
				}
				if eventMatches ||
					strings.Contains(strings.ToLower(m.Question), searchLower) ||
					strings.Contains(strings.ToLower(m.Slug), searchLower) {
					marketMap[m.ConditionID] = m
				}
			}
		}
	}

	// Convert to slice and sort by volume
	markets := make([]GammaMarket, 0, len(marketMap))
	for _, m := range marketMap {
		markets = append(markets, m)
	}

	// Sort by volume descending
	for i := 0; i < len(markets)-1; i++ {
		for j := i + 1; j < len(markets); j++ {
			if markets[j].Volume24hr > markets[i].Volume24hr {
				markets[i], markets[j] = markets[j], markets[i]
			}
		}
	}

	if len(markets) > limit {
		markets = markets[:limit]
	}

	return markets, nil
}

// getEventsByTagSlug fetches events filtered by tag slug (category).
func (c *PolymarketApiClient) getEventsByTagSlug(
	ctx context.Context,
	tagSlug string,
	limit int,
) ([]GammaEvent, error) {
	u, err := url.Parse(c.gammaBaseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid gammaBaseURL: %w", err)
	}
	u.Path = "/events"

	q := u.Query()
	q.Set("tag_slug", tagSlug)
	q.Set("active", "true")
	q.Set("order", "volume24hr")
	q.Set("ascending", "false")
	if limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", limit))
	}
	u.RawQuery = q.Encode()

	var events []GammaEvent
	if err := c.doGet(ctx, u.String(), &events); err != nil {
		return nil, fmt.Errorf("get events by tag: %w", err)
	}

	return events, nil
}

// GetEventBySlug fetches the event metadata for an event slug, e.g.
// "will-the-us-invade-venezuela-in-2025".
func (c *PolymarketApiClient) GetEventBySlug(
	ctx context.Context,
	slug string,
) (*GammaEvent, error) {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return nil, fmt.Errorf("slug is empty")
	}

	u, err := url.Parse(c.gammaBaseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid gammaBaseURL: %w", err)
	}
	u.Path = fmt.Sprintf("/events/slug/%s", url.PathEscape(slug))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gamma request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read gamma response: %w", err)
	}

	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf(
			"gamma status=%d body=%s",
			resp.StatusCode,
			string(body),
		)
	}

	var ev GammaEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		return nil, fmt.Errorf("decode gamma json: %w", err)
	}

	return &ev, nil
}

// ---- Data API types ----

// Trade represents a trade from the data API.
type Trade struct {
	ID              string  `json:"id"`
	ProxyWallet     string  `json:"proxyWallet"`
	Side            string  `json:"side"` // BUY or SELL
	Size            float64 `json:"size"`
	Price           float64 `json:"price"`
	Timestamp       int64   `json:"timestamp"`
	ConditionID     string  `json:"conditionId"`
	Asset           string  `json:"asset"`
	TransactionHash string  `json:"transactionHash"`

	// Market metadata
	Title        string `json:"title"`
	Slug         string `json:"slug"`
	Icon         string `json:"icon"` // Market image URL
	Outcome      string `json:"outcome"`
	OutcomeIndex int    `json:"outcomeIndex"`

	// User profile
	Name         string `json:"name"`
	Pseudonym    string `json:"pseudonym"`
	ProfileImage string `json:"profileImage"`
}

// Activity represents user activity from the data API.
type Activity struct {
	ProxyWallet     string  `json:"proxyWallet"`
	Timestamp       int64   `json:"timestamp"`
	ConditionID     string  `json:"conditionId"`
	Type            string  `json:"type"` // TRADE, SPLIT, MERGE, REDEEM, REWARD, CONVERSION
	Size            float64 `json:"size"`
	UsdcSize        float64 `json:"usdcSize"`
	Price           float64 `json:"price"`
	Side            string  `json:"side"`
	TransactionHash string  `json:"transactionHash"`

	// Market metadata
	Title   string `json:"title"`
	Slug    string `json:"slug"`
	Outcome string `json:"outcome"`
}

// ClosedPosition represents a closed position from the data API.
type ClosedPosition struct {
	ProxyWallet  string  `json:"proxyWallet"`
	Asset        string  `json:"asset"`
	ConditionID  string  `json:"conditionId"`
	AvgPrice     float64 `json:"avgPrice"`
	TotalBought  float64 `json:"totalBought"`
	RealizedPnl  float64 `json:"realizedPnl"`
	Timestamp    int64   `json:"timestamp"`
	Title        string  `json:"title"`
	Outcome      string  `json:"outcome"`
	OutcomeIndex int     `json:"outcomeIndex"`
}

// Position represents an open position from the data API.
type Position struct {
	ProxyWallet        string  `json:"proxyWallet"`
	Asset              string  `json:"asset"`
	ConditionID        string  `json:"conditionId"`
	Size               float64 `json:"size"`
	AvgPrice           float64 `json:"avgPrice"`
	InitialValue       float64 `json:"initialValue"`
	CurrentValue       float64 `json:"currentValue"`
	CashPnl            float64 `json:"cashPnl"`
	PercentPnl         float64 `json:"percentPnl"`
	TotalBought        float64 `json:"totalBought"`
	RealizedPnl        float64 `json:"realizedPnl"`
	PercentRealizedPnl float64 `json:"percentRealizedPnl"`
	CurPrice           float64 `json:"curPrice"`
	Redeemable         bool    `json:"redeemable"`
	Mergeable          bool    `json:"mergeable"`
	Title              string  `json:"title"`
	Slug               string  `json:"slug"`
	Icon               string  `json:"icon"`
	EventSlug          string  `json:"eventSlug"`
	Outcome            string  `json:"outcome"`
	OutcomeIndex       int     `json:"outcomeIndex"`
	OppositeOutcome    string  `json:"oppositeOutcome"`
	OppositeAsset      string  `json:"oppositeAsset"`
	EndDate            string  `json:"endDate"`
	NegativeRisk       bool    `json:"negativeRisk"`
}

// GetTrades fetches recent trades for the given market condition IDs.
func (c *PolymarketApiClient) GetTrades(
	ctx context.Context,
	markets []string,
	limit int,
) ([]Trade, error) {
	u, err := url.Parse(c.dataBaseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid dataBaseURL: %w", err)
	}
	u.Path = "/trades"

	q := u.Query()
	if len(markets) > 0 {
		q.Set("market", strings.Join(markets, ","))
	}
	if limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", limit))
	}
	u.RawQuery = q.Encode()

	var trades []Trade
	if err := c.doGet(ctx, u.String(), &trades); err != nil {
		return nil, fmt.Errorf("get trades: %w", err)
	}

	return trades, nil
}

// GetUserActivity fetches activity for a specific wallet address.
func (c *PolymarketApiClient) GetUserActivity(
	ctx context.Context,
	wallet string,
	limit int,
) ([]Activity, error) {
	wallet = strings.TrimSpace(wallet)
	if wallet == "" {
		return nil, fmt.Errorf("wallet is empty")
	}

	u, err := url.Parse(c.dataBaseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid dataBaseURL: %w", err)
	}
	u.Path = "/activity"

	q := u.Query()
	q.Set("user", wallet)
	if limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", limit))
	}
	u.RawQuery = q.Encode()

	var activity []Activity
	if err := c.doGet(ctx, u.String(), &activity); err != nil {
		return nil, fmt.Errorf("get user activity: %w", err)
	}

	return activity, nil
}

// GetUserActivityPaginated fetches activity for a wallet with pagination and optional time filtering.
// Use cursor for pagination (pass the last activity ID from previous response).
// startTime filters activities after this timestamp (milliseconds).
func (c *PolymarketApiClient) GetUserActivityPaginated(
	ctx context.Context,
	wallet string,
	limit int,
	cursor string,
	startTime int64,
) ([]Activity, error) {
	wallet = strings.TrimSpace(wallet)
	if wallet == "" {
		return nil, fmt.Errorf("wallet is empty")
	}

	if limit <= 0 {
		limit = 1000
	}

	u, err := url.Parse(c.dataBaseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid dataBaseURL: %w", err)
	}
	u.Path = "/activity"

	q := u.Query()
	q.Set("user", wallet)
	q.Set("limit", fmt.Sprintf("%d", limit))
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	if startTime > 0 {
		q.Set("startTime", fmt.Sprintf("%d", startTime))
	}
	u.RawQuery = q.Encode()

	var activity []Activity
	if err := c.doGet(ctx, u.String(), &activity); err != nil {
		return nil, fmt.Errorf("get user activity: %w", err)
	}

	return activity, nil
}

// GetClosedPositions fetches closed positions for a specific wallet address.
func (c *PolymarketApiClient) GetClosedPositions(
	ctx context.Context,
	wallet string,
	limit int,
	offset int,
) ([]ClosedPosition, error) {
	wallet = strings.TrimSpace(wallet)
	if wallet == "" {
		return nil, fmt.Errorf("wallet is empty")
	}

	u, err := url.Parse(c.dataBaseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid dataBaseURL: %w", err)
	}
	u.Path = "/closed-positions"

	q := u.Query()
	q.Set("user", wallet)
	if limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", limit))
	}
	if offset > 0 {
		q.Set("offset", fmt.Sprintf("%d", offset))
	}
	u.RawQuery = q.Encode()

	var positions []ClosedPosition
	if err := c.doGet(ctx, u.String(), &positions); err != nil {
		return nil, fmt.Errorf("get closed positions: %w", err)
	}

	return positions, nil
}

// GetPositions fetches open positions for a specific wallet address.
// Optionally filter by market condition ID.
func (c *PolymarketApiClient) GetPositions(
	ctx context.Context,
	wallet string,
	market string,
	limit int,
) ([]Position, error) {
	wallet = strings.TrimSpace(wallet)
	if wallet == "" {
		return nil, fmt.Errorf("wallet is empty")
	}

	u, err := url.Parse(c.dataBaseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid dataBaseURL: %w", err)
	}
	u.Path = "/positions"

	q := u.Query()
	q.Set("user", wallet)
	if market != "" {
		q.Set("market", market)
	}
	if limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", limit))
	}
	// Set sizeThreshold to 0 to include positions of any size
	q.Set("sizeThreshold", "0")
	u.RawQuery = q.Encode()

	var positions []Position
	if err := c.doGet(ctx, u.String(), &positions); err != nil {
		return nil, fmt.Errorf("get positions: %w", err)
	}

	return positions, nil
}

// MarketSearchOptions contains options for searching closed markets.
type MarketSearchOptions struct {
	Query        string     // Text search query
	ClosedAfter  *time.Time // Filter markets closed after this time
	ClosedBefore *time.Time // Filter markets closed before this time
}

// GetClosedMarkets fetches resolved/closed markets with optional search and date filtering.
func (c *PolymarketApiClient) GetClosedMarkets(
	ctx context.Context,
	limit int,
	offset int,
	searchQuery string,
) ([]GammaMarket, error) {
	return c.GetClosedMarketsWithOptions(ctx, limit, offset, MarketSearchOptions{Query: searchQuery})
}

// GetClosedMarketsWithOptions fetches resolved/closed markets with full search options.
func (c *PolymarketApiClient) GetClosedMarketsWithOptions(
	ctx context.Context,
	limit int,
	offset int,
	opts MarketSearchOptions,
) ([]GammaMarket, error) {
	if limit <= 0 {
		limit = 50
	}

	hasDateFilter := opts.ClosedAfter != nil || opts.ClosedBefore != nil
	hasTextSearch := opts.Query != ""

	// No filters - just fetch one page of markets
	if !hasTextSearch && !hasDateFilter {
		return c.fetchClosedMarketsPage(ctx, limit, offset, "volume24hr", false)
	}

	// With filters, search and filter client-side
	searchLower := strings.ToLower(opts.Query)
	marketMap := make(map[string]GammaMarket) // dedupe by condition ID

	// Determine sort order - use closedTime for date filtering
	orderBy := "volume24hr"
	if hasDateFilter {
		orderBy = "closedTime"
	}

	// 1. Search markets (paginated)
	c.searchClosedMarketsWithOptions(ctx, searchLower, opts, limit*2, marketMap, orderBy)

	// 2. Search events and extract closed markets (if text search)
	if hasTextSearch {
		c.searchEventsForClosedMarketsWithOptions(ctx, searchLower, opts, limit*2, marketMap)
	}

	// Convert map to slice
	results := make([]GammaMarket, 0, len(marketMap))
	for _, m := range marketMap {
		results = append(results, m)
	}

	// Sort by closedTime descending (most recent first) if date filter, else by volume
	if hasDateFilter {
		// Sort by closedTime descending
		for i := 0; i < len(results)-1; i++ {
			for j := i + 1; j < len(results); j++ {
				if results[j].ClosedTime > results[i].ClosedTime {
					results[i], results[j] = results[j], results[i]
				}
			}
		}
	} else {
		// Sort by volume descending
		for i := 0; i < len(results)-1; i++ {
			for j := i + 1; j < len(results); j++ {
				if results[j].VolumeNum > results[i].VolumeNum {
					results[i], results[j] = results[j], results[i]
				}
			}
		}
	}

	// Limit results
	if len(results) > limit {
		results = results[:limit]
	}

	return results, nil
}

// matchesDateFilter checks if a market's closedTime falls within the date range.
func matchesDateFilter(closedTimeStr string, opts MarketSearchOptions) bool {
	if closedTimeStr == "" {
		return false
	}

	// Parse closedTime - format is "2020-11-02 16:31:01+00"
	closedTime, err := time.Parse("2006-01-02 15:04:05-07", closedTimeStr)
	if err != nil {
		// Try alternative format
		closedTime, err = time.Parse("2006-01-02 15:04:05+00", closedTimeStr)
		if err != nil {
			return false
		}
	}

	if opts.ClosedAfter != nil && closedTime.Before(*opts.ClosedAfter) {
		return false
	}
	if opts.ClosedBefore != nil && closedTime.After(*opts.ClosedBefore) {
		return false
	}

	return true
}

// fetchClosedMarketsPage fetches a single page of closed markets.
func (c *PolymarketApiClient) fetchClosedMarketsPage(ctx context.Context, limit, offset int, orderBy string, ascending bool) ([]GammaMarket, error) {
	u, err := url.Parse(c.gammaBaseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid gammaBaseURL: %w", err)
	}
	u.Path = "/markets"

	q := u.Query()
	q.Set("limit", fmt.Sprintf("%d", limit))
	q.Set("closed", "true")
	if orderBy == "" {
		orderBy = "volume24hr"
	}
	q.Set("order", orderBy)
	q.Set("ascending", fmt.Sprintf("%v", ascending))
	if offset > 0 {
		q.Set("offset", fmt.Sprintf("%d", offset))
	}
	u.RawQuery = q.Encode()

	var markets []GammaMarket
	if err := c.doGet(ctx, u.String(), &markets); err != nil {
		return nil, fmt.Errorf("get closed markets: %w", err)
	}

	return markets, nil
}

// searchClosedMarketsWithOptions searches closed markets by text query and date filters.
func (c *PolymarketApiClient) searchClosedMarketsWithOptions(
	ctx context.Context,
	searchLower string,
	opts MarketSearchOptions,
	maxResults int,
	results map[string]GammaMarket,
	orderBy string,
) {
	u, err := url.Parse(c.gammaBaseURL)
	if err != nil {
		return
	}
	u.Path = "/markets"

	pageSize := 500
	maxPages := 10
	hasDateFilter := opts.ClosedAfter != nil || opts.ClosedBefore != nil

	for page := 0; page < maxPages && len(results) < maxResults; page++ {
		select {
		case <-ctx.Done():
			return
		default:
		}

		q := u.Query()
		q.Set("limit", fmt.Sprintf("%d", pageSize))
		q.Set("closed", "true")
		q.Set("order", orderBy)
		q.Set("ascending", "false")
		q.Set("offset", fmt.Sprintf("%d", page*pageSize))
		u.RawQuery = q.Encode()

		var markets []GammaMarket
		if err := c.doGet(ctx, u.String(), &markets); err != nil {
			c.logger.Warn("failed to fetch closed markets page", zap.Int("page", page), zap.Error(err))
			break
		}

		if len(markets) == 0 {
			break
		}

		for _, m := range markets {
			if m.ConditionID == "" {
				continue
			}

			// Apply date filter
			if hasDateFilter && !matchesDateFilter(m.ClosedTime, opts) {
				continue
			}

			// Apply text filter
			if searchLower != "" {
				if !strings.Contains(strings.ToLower(m.Question), searchLower) &&
					!strings.Contains(strings.ToLower(m.Slug), searchLower) {
					continue
				}
			}

			results[m.ConditionID] = m
		}

		if len(markets) < pageSize {
			break
		}
	}
}

// searchEventsForClosedMarketsWithOptions searches events and extracts matching closed markets.
func (c *PolymarketApiClient) searchEventsForClosedMarketsWithOptions(
	ctx context.Context,
	searchLower string,
	opts MarketSearchOptions,
	maxResults int,
	results map[string]GammaMarket,
) {
	u, err := url.Parse(c.gammaBaseURL)
	if err != nil {
		return
	}
	u.Path = "/events"

	pageSize := 100
	maxPages := 5
	hasDateFilter := opts.ClosedAfter != nil || opts.ClosedBefore != nil

	for page := 0; page < maxPages && len(results) < maxResults; page++ {
		select {
		case <-ctx.Done():
			return
		default:
		}

		q := u.Query()
		q.Set("limit", fmt.Sprintf("%d", pageSize))
		q.Set("closed", "true")
		q.Set("order", "volume24hr")
		q.Set("ascending", "false")
		q.Set("offset", fmt.Sprintf("%d", page*pageSize))
		u.RawQuery = q.Encode()

		var events []GammaEvent
		if err := c.doGet(ctx, u.String(), &events); err != nil {
			c.logger.Warn("failed to fetch closed events page", zap.Int("page", page), zap.Error(err))
			break
		}

		if len(events) == 0 {
			break
		}

		for _, event := range events {
			// Check if event title or slug matches
			eventMatches := strings.Contains(strings.ToLower(event.Title), searchLower) ||
				strings.Contains(strings.ToLower(event.Slug), searchLower)

			for _, m := range event.Markets {
				if m.ConditionID == "" || !m.Closed {
					continue
				}

				// Apply date filter
				if hasDateFilter && !matchesDateFilter(m.ClosedTime, opts) {
					continue
				}

				// Include if event matches OR market matches
				if eventMatches ||
					strings.Contains(strings.ToLower(m.Question), searchLower) ||
					strings.Contains(strings.ToLower(m.Slug), searchLower) {
					results[m.ConditionID] = m
				}
			}
		}

		if len(events) < pageSize {
			break
		}
	}
}

// GetMarketTrades fetches trades for a specific market condition ID with pagination.
// Use cursor for pagination (pass the last trade ID from previous response).
func (c *PolymarketApiClient) GetMarketTrades(
	ctx context.Context,
	conditionID string,
	limit int,
	cursor string,
) ([]Trade, error) {
	conditionID = strings.TrimSpace(conditionID)
	if conditionID == "" {
		return nil, fmt.Errorf("conditionID is empty")
	}

	if limit <= 0 {
		limit = 1000
	}

	u, err := url.Parse(c.dataBaseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid dataBaseURL: %w", err)
	}
	u.Path = "/trades"

	q := u.Query()
	q.Set("market", conditionID)
	q.Set("limit", fmt.Sprintf("%d", limit))
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	u.RawQuery = q.Encode()

	var trades []Trade
	if err := c.doGet(ctx, u.String(), &trades); err != nil {
		return nil, fmt.Errorf("get market trades: %w", err)
	}

	return trades, nil
}

// doGet is a helper that performs a GET request and decodes JSON response.
func (c *PolymarketApiClient) doGet(ctx context.Context, url string, dest any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("status=%d body=%s", resp.StatusCode, string(body))
	}

	if err := json.Unmarshal(body, dest); err != nil {
		return fmt.Errorf("decode json: %w", err)
	}

	return nil
}
