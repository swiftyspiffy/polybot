package polymarketapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"polybot/config"
	"testing"
)

func TestNewPolymarketApiClient(t *testing.T) {
	cfg := &config.Config{
		Polymarket: config.PolymarketConfig{
			GammaAPIURL: "https://gamma.example.com",
			DataAPIURL:  "https://data.example.com",
		},
	}

	client := NewPolymarketApiClient(nil, cfg)

	if client.logger == nil {
		t.Error("expected logger to be set")
	}
	if client.gammaBaseURL != "https://gamma.example.com" {
		t.Errorf("unexpected gamma URL: %s", client.gammaBaseURL)
	}
	if client.dataBaseURL != "https://data.example.com" {
		t.Errorf("unexpected data URL: %s", client.dataBaseURL)
	}
}

func TestGetTopMarketsByVolume(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/markets" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		q := r.URL.Query()
		if q.Get("limit") != "10" {
			t.Errorf("unexpected limit: %s", q.Get("limit"))
		}
		if q.Get("order") != "volume24hr" {
			t.Errorf("unexpected order: %s", q.Get("order"))
		}
		if q.Get("ascending") != "false" {
			t.Errorf("unexpected ascending: %s", q.Get("ascending"))
		}
		if q.Get("active") != "true" {
			t.Errorf("unexpected active: %s", q.Get("active"))
		}

		markets := []GammaMarket{
			{ID: "1", Question: "Market 1", ConditionID: "cond1", Volume24hr: 1000, Active: true},
			{ID: "2", Question: "Market 2", ConditionID: "cond2", Volume24hr: 500, Active: true},
		}
		json.NewEncoder(w).Encode(markets)
	}))
	defer server.Close()

	cfg := &config.Config{
		Polymarket: config.PolymarketConfig{
			GammaAPIURL: server.URL,
			DataAPIURL:  server.URL,
		},
	}
	client := NewPolymarketApiClient(nil, cfg)

	markets, err := client.GetTopMarketsByVolume(context.Background(), 10)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(markets) != 2 {
		t.Errorf("expected 2 markets, got %d", len(markets))
	}
	if markets[0].Volume24hr != 1000 {
		t.Errorf("unexpected volume: %f", markets[0].Volume24hr)
	}
}

func TestGetTopMarketsByVolume_DefaultLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("limit") != "20" {
			t.Errorf("expected default limit 20, got: %s", q.Get("limit"))
		}
		json.NewEncoder(w).Encode([]GammaMarket{})
	}))
	defer server.Close()

	cfg := &config.Config{
		Polymarket: config.PolymarketConfig{GammaAPIURL: server.URL},
	}
	client := NewPolymarketApiClient(nil, cfg)

	_, err := client.GetTopMarketsByVolume(context.Background(), 0)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestGetTopMarketsByVolume_Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("server error"))
	}))
	defer server.Close()

	cfg := &config.Config{
		Polymarket: config.PolymarketConfig{GammaAPIURL: server.URL},
	}
	client := NewPolymarketApiClient(nil, cfg)

	_, err := client.GetTopMarketsByVolume(context.Background(), 10)
	if err == nil {
		t.Error("expected error on server error")
	}
}

func TestGetEventBySlug(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/events/slug/test-event" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		event := GammaEvent{
			ID:          "event1",
			Slug:        "test-event",
			Title:       "Test Event",
			Description: "A test event",
			Markets: []GammaMarket{
				{ID: "m1", Question: "Question 1"},
			},
		}
		json.NewEncoder(w).Encode(event)
	}))
	defer server.Close()

	cfg := &config.Config{
		Polymarket: config.PolymarketConfig{GammaAPIURL: server.URL},
	}
	client := NewPolymarketApiClient(nil, cfg)

	event, err := client.GetEventBySlug(context.Background(), "test-event")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if event.Title != "Test Event" {
		t.Errorf("unexpected title: %s", event.Title)
	}
	if len(event.Markets) != 1 {
		t.Errorf("expected 1 market, got %d", len(event.Markets))
	}
}

func TestGetEventBySlug_EmptySlug(t *testing.T) {
	cfg := &config.Config{
		Polymarket: config.PolymarketConfig{GammaAPIURL: "http://example.com"},
	}
	client := NewPolymarketApiClient(nil, cfg)

	_, err := client.GetEventBySlug(context.Background(), "")
	if err == nil {
		t.Error("expected error for empty slug")
	}

	_, err = client.GetEventBySlug(context.Background(), "   ")
	if err == nil {
		t.Error("expected error for whitespace slug")
	}
}

func TestGetEventBySlug_Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("not found"))
	}))
	defer server.Close()

	cfg := &config.Config{
		Polymarket: config.PolymarketConfig{GammaAPIURL: server.URL},
	}
	client := NewPolymarketApiClient(nil, cfg)

	_, err := client.GetEventBySlug(context.Background(), "nonexistent")
	if err == nil {
		t.Error("expected error on not found")
	}
}

func TestGetTrades(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/trades" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		q := r.URL.Query()
		if q.Get("market") != "cond1,cond2" {
			t.Errorf("unexpected market param: %s", q.Get("market"))
		}
		if q.Get("limit") != "50" {
			t.Errorf("unexpected limit: %s", q.Get("limit"))
		}

		trades := []Trade{
			{
				ID:          "t1",
				ProxyWallet: "0x123",
				Side:        "BUY",
				Size:        100,
				Price:       0.5,
				Timestamp:   1234567890000,
				ConditionID: "cond1",
				Title:       "Test Market",
			},
		}
		json.NewEncoder(w).Encode(trades)
	}))
	defer server.Close()

	cfg := &config.Config{
		Polymarket: config.PolymarketConfig{DataAPIURL: server.URL},
	}
	client := NewPolymarketApiClient(nil, cfg)

	trades, err := client.GetTrades(context.Background(), []string{"cond1", "cond2"}, 50)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(trades) != 1 {
		t.Errorf("expected 1 trade, got %d", len(trades))
	}
	if trades[0].Side != "BUY" {
		t.Errorf("unexpected side: %s", trades[0].Side)
	}
}

func TestGetTrades_NoMarkets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("market") != "" {
			t.Errorf("expected no market param, got: %s", q.Get("market"))
		}
		json.NewEncoder(w).Encode([]Trade{})
	}))
	defer server.Close()

	cfg := &config.Config{
		Polymarket: config.PolymarketConfig{DataAPIURL: server.URL},
	}
	client := NewPolymarketApiClient(nil, cfg)

	_, err := client.GetTrades(context.Background(), nil, 0)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestGetUserActivity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/activity" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		q := r.URL.Query()
		if q.Get("user") != "0x123abc" {
			t.Errorf("unexpected user param: %s", q.Get("user"))
		}
		if q.Get("limit") != "100" {
			t.Errorf("unexpected limit: %s", q.Get("limit"))
		}

		activity := []Activity{
			{
				ProxyWallet: "0x123abc",
				Type:        "TRADE",
				Size:        50,
				ConditionID: "cond1",
				Title:       "Test Market",
			},
		}
		json.NewEncoder(w).Encode(activity)
	}))
	defer server.Close()

	cfg := &config.Config{
		Polymarket: config.PolymarketConfig{DataAPIURL: server.URL},
	}
	client := NewPolymarketApiClient(nil, cfg)

	activity, err := client.GetUserActivity(context.Background(), "0x123abc", 100)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(activity) != 1 {
		t.Errorf("expected 1 activity, got %d", len(activity))
	}
	if activity[0].Type != "TRADE" {
		t.Errorf("unexpected type: %s", activity[0].Type)
	}
}

func TestGetUserActivity_EmptyWallet(t *testing.T) {
	cfg := &config.Config{
		Polymarket: config.PolymarketConfig{DataAPIURL: "http://example.com"},
	}
	client := NewPolymarketApiClient(nil, cfg)

	_, err := client.GetUserActivity(context.Background(), "", 100)
	if err == nil {
		t.Error("expected error for empty wallet")
	}

	_, err = client.GetUserActivity(context.Background(), "   ", 100)
	if err == nil {
		t.Error("expected error for whitespace wallet")
	}
}

func TestGetClosedPositions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/closed-positions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		q := r.URL.Query()
		if q.Get("user") != "0xwallet" {
			t.Errorf("unexpected user param: %s", q.Get("user"))
		}

		positions := []ClosedPosition{
			{
				ProxyWallet: "0xwallet",
				ConditionID: "cond1",
				RealizedPnl: 100.5,
				Title:       "Won Trade",
			},
			{
				ProxyWallet: "0xwallet",
				ConditionID: "cond2",
				RealizedPnl: -50.0,
				Title:       "Lost Trade",
			},
		}
		json.NewEncoder(w).Encode(positions)
	}))
	defer server.Close()

	cfg := &config.Config{
		Polymarket: config.PolymarketConfig{DataAPIURL: server.URL},
	}
	client := NewPolymarketApiClient(nil, cfg)

	positions, err := client.GetClosedPositions(context.Background(), "0xwallet", 50, 0)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(positions) != 2 {
		t.Errorf("expected 2 positions, got %d", len(positions))
	}
	if positions[0].RealizedPnl != 100.5 {
		t.Errorf("unexpected pnl: %f", positions[0].RealizedPnl)
	}
}

func TestGetClosedPositions_EmptyWallet(t *testing.T) {
	cfg := &config.Config{
		Polymarket: config.PolymarketConfig{DataAPIURL: "http://example.com"},
	}
	client := NewPolymarketApiClient(nil, cfg)

	_, err := client.GetClosedPositions(context.Background(), "", 50, 0)
	if err == nil {
		t.Error("expected error for empty wallet")
	}
}

func TestDoGet_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not valid json"))
	}))
	defer server.Close()

	cfg := &config.Config{
		Polymarket: config.PolymarketConfig{DataAPIURL: server.URL},
	}
	client := NewPolymarketApiClient(nil, cfg)

	_, err := client.GetTrades(context.Background(), nil, 10)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestGammaMarketFields(t *testing.T) {
	market := GammaMarket{
		ID:           "m1",
		Slug:         "test-slug",
		Question:     "Test question?",
		ConditionID:  "cond1",
		Volume24hr:   1000.5,
		VolumeNum:    50000,
		Active:       true,
		Closed:       false,
		Image:        "https://example.com/image.png",
	}

	if market.ID != "m1" {
		t.Errorf("unexpected ID: %s", market.ID)
	}
	if !market.Active {
		t.Error("expected market to be active")
	}
	if market.Closed {
		t.Error("expected market to not be closed")
	}
}

func TestTradeFields(t *testing.T) {
	trade := Trade{
		ID:              "t1",
		ProxyWallet:     "0x123",
		Side:            "SELL",
		Size:            200,
		Price:           0.75,
		Timestamp:       1234567890000,
		ConditionID:     "cond1",
		Asset:           "asset1",
		TransactionHash: "0xhash",
		Title:           "Test Trade",
		Slug:            "test-slug",
		Icon:            "https://example.com/icon.png",
		Outcome:         "Yes",
		OutcomeIndex:    0,
		Name:            "Trader Name",
		Pseudonym:       "TraderPseudo",
		ProfileImage:    "https://example.com/profile.png",
	}

	if trade.Side != "SELL" {
		t.Errorf("unexpected side: %s", trade.Side)
	}
	if trade.Size != 200 {
		t.Errorf("unexpected size: %f", trade.Size)
	}
}

func TestActivityFields(t *testing.T) {
	activity := Activity{
		ProxyWallet:     "0x123",
		Timestamp:       1234567890000,
		ConditionID:     "cond1",
		Type:            "TRADE",
		Size:            100,
		UsdcSize:        50.5,
		Price:           0.5,
		Side:            "BUY",
		TransactionHash: "0xhash",
		Title:           "Test Activity",
		Slug:            "test-slug",
		Outcome:         "No",
	}

	if activity.Type != "TRADE" {
		t.Errorf("unexpected type: %s", activity.Type)
	}
}

func TestClosedPositionFields(t *testing.T) {
	position := ClosedPosition{
		ProxyWallet:  "0x123",
		Asset:        "asset1",
		ConditionID:  "cond1",
		AvgPrice:     0.6,
		TotalBought:  1000,
		RealizedPnl:  150.5,
		Timestamp:    1234567890000,
		Title:        "Closed Position",
		Outcome:      "Yes",
		OutcomeIndex: 0,
	}

	if position.RealizedPnl != 150.5 {
		t.Errorf("unexpected pnl: %f", position.RealizedPnl)
	}
}

func TestGetTokenIDs(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		expected []string
	}{
		{
			name:     "direct array",
			raw:      `["token1", "token2"]`,
			expected: []string{"token1", "token2"},
		},
		{
			name:     "json string containing array",
			raw:      `"[\"token1\", \"token2\"]"`,
			expected: []string{"token1", "token2"},
		},
		{
			name:     "array containing json string (Gamma API format)",
			raw:      `["[\"token1\", \"token2\"]"]`,
			expected: []string{"token1", "token2"},
		},
		{
			name:     "empty",
			raw:      ``,
			expected: nil,
		},
		{
			name:     "null",
			raw:      `null`,
			expected: nil,
		},
		{
			name:     "single token",
			raw:      `["token1"]`,
			expected: []string{"token1"},
		},
		{
			name:     "multiple nested arrays to flatten",
			raw:      `["[\"t1\", \"t2\"]", "[\"t3\", \"t4\"]"]`,
			expected: []string{"t1", "t2", "t3", "t4"},
		},
		{
			name:     "mixed (should not flatten)",
			raw:      `["token1", "[\"t2\", \"t3\"]"]`,
			expected: []string{"token1", "[\"t2\", \"t3\"]"},
		},
		{
			name:     "invalid json string",
			raw:      `"invalid"`,
			expected: nil,
		},
		{
			name:     "empty string in json",
			raw:      `""`,
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			market := GammaMarket{
				ClobTokenIDs: json.RawMessage(tt.raw),
			}
			result := market.GetTokenIDs()
			if len(result) != len(tt.expected) {
				t.Errorf("expected %d tokens, got %d: %v", len(tt.expected), len(result), result)
				return
			}
			for i, tok := range result {
				if tok != tt.expected[i] {
					t.Errorf("token %d: expected %s, got %s", i, tt.expected[i], tok)
				}
			}
		})
	}
}

func TestGetEventBySlug_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not valid json"))
	}))
	defer server.Close()

	cfg := &config.Config{
		Polymarket: config.PolymarketConfig{GammaAPIURL: server.URL},
	}
	client := NewPolymarketApiClient(nil, cfg)

	_, err := client.GetEventBySlug(context.Background(), "test")
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestGetTrades_Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	cfg := &config.Config{
		Polymarket: config.PolymarketConfig{DataAPIURL: server.URL},
	}
	client := NewPolymarketApiClient(nil, cfg)

	_, err := client.GetTrades(context.Background(), []string{"cond1"}, 10)
	if err == nil {
		t.Error("expected error on server error")
	}
}

func TestGetUserActivity_Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	cfg := &config.Config{
		Polymarket: config.PolymarketConfig{DataAPIURL: server.URL},
	}
	client := NewPolymarketApiClient(nil, cfg)

	_, err := client.GetUserActivity(context.Background(), "0x123", 100)
	if err == nil {
		t.Error("expected error on server error")
	}
}

func TestGetClosedPositions_Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	cfg := &config.Config{
		Polymarket: config.PolymarketConfig{DataAPIURL: server.URL},
	}
	client := NewPolymarketApiClient(nil, cfg)

	_, err := client.GetClosedPositions(context.Background(), "0x123", 50, 0)
	if err == nil {
		t.Error("expected error on server error")
	}
}

func TestGammaEvent_Fields(t *testing.T) {
	event := GammaEvent{
		ID:          "e1",
		Slug:        "test-event",
		Title:       "Test Event",
		Description: "Description",
		Markets: []GammaMarket{
			{ID: "m1"},
		},
	}

	if event.ID != "e1" {
		t.Errorf("unexpected ID: %s", event.ID)
	}
	if len(event.Markets) != 1 {
		t.Errorf("expected 1 market, got %d", len(event.Markets))
	}
}

func TestGetPositions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/positions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		q := r.URL.Query()
		if q.Get("user") != "0xwallet" {
			t.Errorf("unexpected user param: %s", q.Get("user"))
		}
		if q.Get("market") != "cond123" {
			t.Errorf("unexpected market param: %s", q.Get("market"))
		}
		if q.Get("sizeThreshold") != "0" {
			t.Errorf("unexpected sizeThreshold param: %s", q.Get("sizeThreshold"))
		}

		positions := []Position{
			{
				ProxyWallet:  "0xwallet",
				ConditionID:  "cond123",
				Size:         100.5,
				AvgPrice:     0.65,
				CurrentValue: 75.0,
				Outcome:      "Yes",
				Title:        "Test Market",
			},
			{
				ProxyWallet:  "0xwallet",
				ConditionID:  "cond123",
				Size:         50.0,
				AvgPrice:     0.35,
				CurrentValue: 17.5,
				Outcome:      "No",
				Title:        "Test Market",
			},
		}
		json.NewEncoder(w).Encode(positions)
	}))
	defer server.Close()

	cfg := &config.Config{
		Polymarket: config.PolymarketConfig{DataAPIURL: server.URL},
	}
	client := NewPolymarketApiClient(nil, cfg)

	positions, err := client.GetPositions(context.Background(), "0xwallet", "cond123", 10)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(positions) != 2 {
		t.Errorf("expected 2 positions, got %d", len(positions))
	}
	if positions[0].Size != 100.5 {
		t.Errorf("unexpected size: %f", positions[0].Size)
	}
	if positions[0].Outcome != "Yes" {
		t.Errorf("unexpected outcome: %s", positions[0].Outcome)
	}
}

func TestGetPositions_EmptyWallet(t *testing.T) {
	cfg := &config.Config{
		Polymarket: config.PolymarketConfig{DataAPIURL: "http://example.com"},
	}
	client := NewPolymarketApiClient(nil, cfg)

	_, err := client.GetPositions(context.Background(), "", "cond123", 10)
	if err == nil {
		t.Error("expected error for empty wallet")
	}

	_, err = client.GetPositions(context.Background(), "   ", "cond123", 10)
	if err == nil {
		t.Error("expected error for whitespace wallet")
	}
}

func TestGetPositions_NoMarketFilter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("market") != "" {
			t.Errorf("expected no market param, got: %s", q.Get("market"))
		}
		json.NewEncoder(w).Encode([]Position{})
	}))
	defer server.Close()

	cfg := &config.Config{
		Polymarket: config.PolymarketConfig{DataAPIURL: server.URL},
	}
	client := NewPolymarketApiClient(nil, cfg)

	_, err := client.GetPositions(context.Background(), "0xwallet", "", 0)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestGetPositions_Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	cfg := &config.Config{
		Polymarket: config.PolymarketConfig{DataAPIURL: server.URL},
	}
	client := NewPolymarketApiClient(nil, cfg)

	_, err := client.GetPositions(context.Background(), "0x123", "cond1", 10)
	if err == nil {
		t.Error("expected error on server error")
	}
}

func TestPositionFields(t *testing.T) {
	position := Position{
		ProxyWallet:        "0x123",
		Asset:              "asset1",
		ConditionID:        "cond1",
		Size:               500.0,
		AvgPrice:           0.72,
		InitialValue:       360.0,
		CurrentValue:       400.0,
		CashPnl:            40.0,
		PercentPnl:         11.1,
		TotalBought:        500.0,
		RealizedPnl:        0,
		PercentRealizedPnl: 0,
		CurPrice:           0.80,
		Redeemable:         false,
		Mergeable:          true,
		Title:              "Test Position",
		Slug:               "test-slug",
		Icon:               "https://example.com/icon.png",
		EventSlug:          "event-slug",
		Outcome:            "Yes",
		OutcomeIndex:       0,
		OppositeOutcome:    "No",
		OppositeAsset:      "asset2",
		EndDate:            "2025-12-31",
		NegativeRisk:       false,
	}

	if position.Size != 500.0 {
		t.Errorf("unexpected size: %f", position.Size)
	}
	if position.CurrentValue != 400.0 {
		t.Errorf("unexpected current value: %f", position.CurrentValue)
	}
	if position.Outcome != "Yes" {
		t.Errorf("unexpected outcome: %s", position.Outcome)
	}
}

func TestGetMarketByConditionID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/markets" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("condition_id") != "cond123" {
			t.Errorf("unexpected condition_id param: %s", q.Get("condition_id"))
		}
		if q.Get("limit") != "1" {
			t.Errorf("unexpected limit param: %s", q.Get("limit"))
		}

		// Return array of markets (API returns array)
		markets := []GammaMarket{
			{
				ID:          "m1",
				Question:    "Test Market?",
				ConditionID: "cond123",
				Volume24hr:  5000,
				Active:      true,
			},
		}
		json.NewEncoder(w).Encode(markets)
	}))
	defer server.Close()

	cfg := &config.Config{
		Polymarket: config.PolymarketConfig{GammaAPIURL: server.URL},
	}
	client := NewPolymarketApiClient(nil, cfg)

	market, err := client.GetMarketByConditionID(context.Background(), "cond123")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if market.Question != "Test Market?" {
		t.Errorf("unexpected question: %s", market.Question)
	}
	if market.ConditionID != "cond123" {
		t.Errorf("unexpected condition ID: %s", market.ConditionID)
	}
}

func TestGetMarketByConditionID_EmptyConditionID(t *testing.T) {
	cfg := &config.Config{
		Polymarket: config.PolymarketConfig{GammaAPIURL: "http://example.com"},
	}
	client := NewPolymarketApiClient(nil, cfg)

	_, err := client.GetMarketByConditionID(context.Background(), "")
	if err == nil {
		t.Error("expected error for empty condition ID")
	}

	_, err = client.GetMarketByConditionID(context.Background(), "   ")
	if err == nil {
		t.Error("expected error for whitespace condition ID")
	}
}

func TestGetMarketByConditionID_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return empty array - market not found
		json.NewEncoder(w).Encode([]GammaMarket{})
	}))
	defer server.Close()

	cfg := &config.Config{
		Polymarket: config.PolymarketConfig{GammaAPIURL: server.URL},
	}
	client := NewPolymarketApiClient(nil, cfg)

	_, err := client.GetMarketByConditionID(context.Background(), "nonexistent")
	if err == nil {
		t.Error("expected error on not found")
	}
}

func TestGetMarketByConditionID_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	cfg := &config.Config{
		Polymarket: config.PolymarketConfig{GammaAPIURL: server.URL},
	}
	client := NewPolymarketApiClient(nil, cfg)

	_, err := client.GetMarketByConditionID(context.Background(), "cond123")
	if err == nil {
		t.Error("expected error on server error")
	}
}

func TestGetMarketByConditionID_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not valid json"))
	}))
	defer server.Close()

	cfg := &config.Config{
		Polymarket: config.PolymarketConfig{GammaAPIURL: server.URL},
	}
	client := NewPolymarketApiClient(nil, cfg)

	_, err := client.GetMarketByConditionID(context.Background(), "cond123")
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}
