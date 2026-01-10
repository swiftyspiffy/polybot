package gist

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"polybot/config"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestNewClient(t *testing.T) {
	cfg := &config.Config{
		Gist: config.GistConfig{
			Token:  "test-token",
			GistID: "test-gist-id",
		},
	}

	client := NewClient(nil, cfg)

	if client.logger == nil {
		t.Error("expected logger to be set")
	}
	if client.token != "test-token" {
		t.Errorf("expected token 'test-token', got '%s'", client.token)
	}
	if client.gistID != "test-gist-id" {
		t.Errorf("expected gistID 'test-gist-id', got '%s'", client.gistID)
	}
}

func TestNewClient_NoToken(t *testing.T) {
	cfg := &config.Config{
		Gist: config.GistConfig{
			Token:  "",
			GistID: "",
		},
	}

	client := NewClient(zap.NewNop(), cfg)

	if client.IsEnabled() {
		t.Error("expected client to be disabled without token")
	}
}

func TestIsEnabled(t *testing.T) {
	tests := []struct {
		name     string
		token    string
		expected bool
	}{
		{"with token", "test-token", true},
		{"empty token", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				Gist: config.GistConfig{Token: tt.token},
			}
			client := NewClient(nil, cfg)
			if client.IsEnabled() != tt.expected {
				t.Errorf("expected IsEnabled() = %v", tt.expected)
			}
		})
	}
}

func TestGetGistID(t *testing.T) {
	cfg := &config.Config{
		Gist: config.GistConfig{
			Token:  "token",
			GistID: "my-gist-id",
		},
	}
	client := NewClient(nil, cfg)

	if id := client.GetGistID(); id != "my-gist-id" {
		t.Errorf("expected 'my-gist-id', got '%s'", id)
	}
}

func TestSave_Disabled(t *testing.T) {
	cfg := &config.Config{
		Gist: config.GistConfig{Token: ""},
	}
	client := NewClient(nil, cfg)

	err := client.Save(context.Background(), "test.json", "content")
	if err == nil {
		t.Error("expected error when client is disabled")
	}
}

func TestSave_UpdateExisting(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Errorf("expected PATCH, got %s", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("missing or invalid authorization header")
		}
		if r.Header.Get("X-GitHub-Api-Version") != "2022-11-28" {
			t.Error("missing or invalid API version header")
		}

		var req gistRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("failed to decode request: %v", err)
		}

		if req.Description != "polybot cache" {
			t.Errorf("unexpected description: %s", req.Description)
		}
		if req.Public {
			t.Error("expected public to be false")
		}
		if file, ok := req.Files["test.json"]; !ok || file.Content != "test content" {
			t.Error("unexpected file content")
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(Gist{ID: "existing-id"})
	}))
	defer server.Close()

	client := &Client{
		logger:     zap.NewNop(),
		httpClient: server.Client(),
		token:      "test-token",
		gistID:     "existing-id",
	}
	// Override the API base URL by modifying the client's behavior
	// We need to use the test server URL
	origClient := client.httpClient
	client.httpClient = &http.Client{
		Transport: &testTransport{
			baseURL:   server.URL,
			transport: origClient.Transport,
		},
	}

	err := client.Save(context.Background(), "test.json", "test content")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSave_CreateNew(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(Gist{ID: "new-gist-id"})
	}))
	defer server.Close()

	client := &Client{
		logger:     zap.NewNop(),
		httpClient: server.Client(),
		token:      "test-token",
		gistID:     "", // No existing gist
	}
	client.httpClient = &http.Client{
		Transport: &testTransport{
			baseURL:   server.URL,
			transport: http.DefaultTransport,
		},
	}

	err := client.Save(context.Background(), "test.json", "test content")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if client.gistID != "new-gist-id" {
		t.Errorf("expected gistID to be updated to 'new-gist-id', got '%s'", client.gistID)
	}
}

func TestLoad_Disabled(t *testing.T) {
	cfg := &config.Config{
		Gist: config.GistConfig{Token: ""},
	}
	client := NewClient(nil, cfg)

	_, err := client.Load(context.Background(), "test.json")
	if err == nil {
		t.Error("expected error when client is disabled")
	}
}

func TestLoad_NoGistID(t *testing.T) {
	cfg := &config.Config{
		Gist: config.GistConfig{
			Token:  "test-token",
			GistID: "",
		},
	}
	client := NewClient(nil, cfg)

	_, err := client.Load(context.Background(), "test.json")
	if err == nil {
		t.Error("expected error when no gist ID configured")
	}
}

func TestLoad_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}

		gist := Gist{
			ID: "test-gist-id",
			Files: map[string]GistFile{
				"test.json": {Content: `{"key": "value"}`},
			},
		}
		json.NewEncoder(w).Encode(gist)
	}))
	defer server.Close()

	client := &Client{
		logger:     zap.NewNop(),
		httpClient: server.Client(),
		token:      "test-token",
		gistID:     "test-gist-id",
	}
	client.httpClient = &http.Client{
		Transport: &testTransport{
			baseURL:   server.URL,
			transport: http.DefaultTransport,
		},
	}

	content, err := client.Load(context.Background(), "test.json")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if content != `{"key": "value"}` {
		t.Errorf("unexpected content: %s", content)
	}
}

func TestLoad_FileNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gist := Gist{
			ID:    "test-gist-id",
			Files: map[string]GistFile{},
		}
		json.NewEncoder(w).Encode(gist)
	}))
	defer server.Close()

	client := &Client{
		logger:     zap.NewNop(),
		httpClient: server.Client(),
		token:      "test-token",
		gistID:     "test-gist-id",
	}
	client.httpClient = &http.Client{
		Transport: &testTransport{
			baseURL:   server.URL,
			transport: http.DefaultTransport,
		},
	}

	_, err := client.Load(context.Background(), "nonexistent.json")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestLoad_GistNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := &Client{
		logger:     zap.NewNop(),
		httpClient: server.Client(),
		token:      "test-token",
		gistID:     "nonexistent-id",
	}
	client.httpClient = &http.Client{
		Transport: &testTransport{
			baseURL:   server.URL,
			transport: http.DefaultTransport,
		},
	}

	_, err := client.Load(context.Background(), "test.json")
	if err == nil {
		t.Error("expected error for nonexistent gist")
	}
}

func TestSaveJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req gistRequest
		json.NewDecoder(r.Body).Decode(&req)

		file := req.Files["data.json"]
		var data map[string]string
		if err := json.Unmarshal([]byte(file.Content), &data); err != nil {
			t.Errorf("failed to parse JSON content: %v", err)
		}
		if data["key"] != "value" {
			t.Errorf("unexpected JSON data: %v", data)
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(Gist{ID: "test-id"})
	}))
	defer server.Close()

	client := &Client{
		logger:     zap.NewNop(),
		httpClient: server.Client(),
		token:      "test-token",
		gistID:     "test-id",
	}
	client.httpClient = &http.Client{
		Transport: &testTransport{
			baseURL:   server.URL,
			transport: http.DefaultTransport,
		},
	}

	err := client.SaveJSON(context.Background(), "data.json", map[string]string{"key": "value"})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestLoadJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gist := Gist{
			ID: "test-id",
			Files: map[string]GistFile{
				"data.json": {Content: `{"name": "test", "count": 42}`},
			},
		}
		json.NewEncoder(w).Encode(gist)
	}))
	defer server.Close()

	client := &Client{
		logger:     zap.NewNop(),
		httpClient: server.Client(),
		token:      "test-token",
		gistID:     "test-id",
	}
	client.httpClient = &http.Client{
		Transport: &testTransport{
			baseURL:   server.URL,
			transport: http.DefaultTransport,
		},
	}

	var dest struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	err := client.LoadJSON(context.Background(), "data.json", &dest)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if dest.Name != "test" || dest.Count != 42 {
		t.Errorf("unexpected data: %+v", dest)
	}
}

func TestSave_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error": "internal server error"}`))
	}))
	defer server.Close()

	client := &Client{
		logger:     zap.NewNop(),
		httpClient: server.Client(),
		token:      "test-token",
		gistID:     "test-id",
	}
	client.httpClient = &http.Client{
		Transport: &testTransport{
			baseURL:   server.URL,
			transport: http.DefaultTransport,
		},
	}

	err := client.Save(context.Background(), "test.json", "content")
	if err == nil {
		t.Error("expected error on API error")
	}
}

func TestLoad_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error": "internal server error"}`))
	}))
	defer server.Close()

	client := &Client{
		logger:     zap.NewNop(),
		httpClient: server.Client(),
		token:      "test-token",
		gistID:     "test-id",
	}
	client.httpClient = &http.Client{
		Transport: &testTransport{
			baseURL:   server.URL,
			transport: http.DefaultTransport,
		},
	}

	_, err := client.Load(context.Background(), "test.json")
	if err == nil {
		t.Error("expected error on API error")
	}
}

// testTransport rewrites requests to go to the test server
type testTransport struct {
	baseURL   string
	transport http.RoundTripper
}

func (t *testTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Rewrite the URL to point to the test server
	req.URL.Scheme = "http"
	req.URL.Host = t.baseURL[7:] // Strip "http://"

	if t.transport == nil {
		t.transport = http.DefaultTransport
	}
	return t.transport.RoundTrip(req)
}

func TestGistTypes(t *testing.T) {
	// Test Gist struct
	gist := Gist{
		ID:          "test-id",
		Description: "test description",
		Public:      true,
		Files: map[string]GistFile{
			"file.txt": {Filename: "file.txt", Content: "content"},
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if gist.ID != "test-id" {
		t.Error("unexpected gist ID")
	}

	// Test GistFile struct
	file := GistFile{
		Filename: "test.json",
		Content:  "test content",
	}

	if file.Filename != "test.json" {
		t.Error("unexpected filename")
	}
}

func TestSaveJSON_Disabled(t *testing.T) {
	cfg := &config.Config{
		Gist: config.GistConfig{Token: ""},
	}
	client := NewClient(nil, cfg)

	err := client.SaveJSON(context.Background(), "test.json", map[string]string{"key": "value"})
	if err == nil {
		t.Error("expected error when client is disabled")
	}
}

func TestSaveJSON_MarshalError(t *testing.T) {
	client := &Client{
		logger: zap.NewNop(),
		token:  "test-token",
		gistID: "test-id",
	}

	// Channel cannot be marshaled to JSON
	err := client.SaveJSON(context.Background(), "test.json", make(chan int))
	if err == nil {
		t.Error("expected error for unmarshalable data")
	}
}

func TestLoadJSON_Disabled(t *testing.T) {
	cfg := &config.Config{
		Gist: config.GistConfig{Token: ""},
	}
	client := NewClient(nil, cfg)

	var dest map[string]string
	err := client.LoadJSON(context.Background(), "test.json", &dest)
	if err == nil {
		t.Error("expected error when client is disabled")
	}
}

func TestLoadJSON_UnmarshalError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gist := Gist{
			ID: "test-id",
			Files: map[string]GistFile{
				"test.json": {Content: `not valid json`},
			},
		}
		json.NewEncoder(w).Encode(gist)
	}))
	defer server.Close()

	client := &Client{
		logger:     zap.NewNop(),
		httpClient: server.Client(),
		token:      "test-token",
		gistID:     "test-id",
	}
	client.httpClient = &http.Client{
		Transport: &testTransport{
			baseURL:   server.URL,
			transport: http.DefaultTransport,
		},
	}

	var dest map[string]string
	err := client.LoadJSON(context.Background(), "test.json", &dest)
	if err == nil {
		t.Error("expected error for invalid JSON content")
	}
}

func TestLoadJSON_LoadError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := &Client{
		logger:     zap.NewNop(),
		httpClient: server.Client(),
		token:      "test-token",
		gistID:     "test-id",
	}
	client.httpClient = &http.Client{
		Transport: &testTransport{
			baseURL:   server.URL,
			transport: http.DefaultTransport,
		},
	}

	var dest map[string]string
	err := client.LoadJSON(context.Background(), "test.json", &dest)
	if err == nil {
		t.Error("expected error when load fails")
	}
}
