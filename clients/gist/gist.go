package gist

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"polybot/config"
	"time"

	"go.uber.org/zap"
)

const (
	apiBaseURL = "https://api.github.com"
)

// Storage is the interface for gist storage operations.
// This allows for easy mocking in tests.
type Storage interface {
	IsEnabled() bool
	Load(ctx context.Context, filename string, gistID ...string) (string, error)
	Save(ctx context.Context, filename, content string, gistID ...string) error
	LoadJSON(ctx context.Context, filename string, dest any) error
	SaveJSON(ctx context.Context, filename string, data any) error
	GetGistID() string
}

// Ensure Client implements Storage interface
var _ Storage = (*Client)(nil)

// Client is a GitHub Gist API client for storing JSON data.
type Client struct {
	logger     *zap.Logger
	httpClient *http.Client
	token      string
	gistID     string // If set, updates this gist; otherwise creates new ones
}

// GistFile represents a file in a gist.
type GistFile struct {
	Filename string `json:"filename,omitempty"`
	Content  string `json:"content"`
}

// Gist represents a GitHub gist.
type Gist struct {
	ID          string              `json:"id"`
	Description string              `json:"description"`
	Public      bool                `json:"public"`
	Files       map[string]GistFile `json:"files"`
	CreatedAt   time.Time           `json:"created_at"`
	UpdatedAt   time.Time           `json:"updated_at"`
}

// createGistRequest is the request body for creating/updating a gist.
type gistRequest struct {
	Description string              `json:"description,omitempty"`
	Public      bool                `json:"public"`
	Files       map[string]GistFile `json:"files"`
}

// NewClient creates a new GitHub Gist client.
func NewClient(logger *zap.Logger, cfg *config.Config) *Client {
	if logger == nil {
		logger = zap.NewNop()
	}

	token := cfg.Gist.Token
	if token == "" {
		logger.Warn("GITHUB_TOKEN not set, gist storage will be disabled")
	}

	return &Client{
		logger: logger,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		token:  token,
		gistID: cfg.Gist.GistID,
	}
}

// IsEnabled returns true if the client has a valid token.
func (c *Client) IsEnabled() bool {
	return c.token != ""
}

// SaveJSON saves JSON data to a gist file.
// If gistID is set, updates the existing gist; otherwise creates a new one.
func (c *Client) SaveJSON(ctx context.Context, filename string, data any) error {
	if !c.IsEnabled() {
		return fmt.Errorf("gist client not configured")
	}

	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal json: %w", err)
	}

	return c.Save(ctx, filename, string(jsonData))
}

// Save saves content to a gist file.
// If gistID is provided (non-empty), it overrides the client's default gist ID.
func (c *Client) Save(ctx context.Context, filename, content string, gistID ...string) error {
	if !c.IsEnabled() {
		return fmt.Errorf("gist client not configured")
	}

	targetGistID := c.gistID
	if len(gistID) > 0 && gistID[0] != "" {
		targetGistID = gistID[0]
	}

	reqBody := gistRequest{
		Description: "polybot cache",
		Public:      false,
		Files: map[string]GistFile{
			filename: {Content: content},
		},
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	var url string
	var method string
	if targetGistID != "" {
		url = fmt.Sprintf("%s/gists/%s", apiBaseURL, targetGistID)
		method = http.MethodPatch
	} else {
		url = fmt.Sprintf("%s/gists", apiBaseURL)
		method = http.MethodPost
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("api error status=%d body=%s", resp.StatusCode, string(body))
	}

	// If we created a new gist, save its ID for future updates
	if c.gistID == "" {
		var gist Gist
		if err := json.NewDecoder(resp.Body).Decode(&gist); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
		c.gistID = gist.ID
		c.logger.Info("created new gist", zap.String("id", gist.ID))
	}

	c.logger.Debug("saved to gist",
		zap.String("filename", filename),
		zap.Int("bytes", len(content)),
	)

	return nil
}

// LoadJSON loads JSON data from a gist file.
func (c *Client) LoadJSON(ctx context.Context, filename string, dest any) error {
	if !c.IsEnabled() {
		return fmt.Errorf("gist client not configured")
	}

	content, err := c.Load(ctx, filename)
	if err != nil {
		return err
	}

	if err := json.Unmarshal([]byte(content), dest); err != nil {
		return fmt.Errorf("unmarshal json: %w", err)
	}

	return nil
}

// Load loads content from a gist file.
// If gistID is provided (non-empty), it overrides the client's default gist ID.
func (c *Client) Load(ctx context.Context, filename string, gistID ...string) (string, error) {
	if !c.IsEnabled() {
		return "", fmt.Errorf("gist client not configured")
	}

	targetGistID := c.gistID
	if len(gistID) > 0 && gistID[0] != "" {
		targetGistID = gistID[0]
	}

	if targetGistID == "" {
		return "", fmt.Errorf("no gist ID configured")
	}

	url := fmt.Sprintf("%s/gists/%s", apiBaseURL, targetGistID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("gist not found")
	}

	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("api error status=%d body=%s", resp.StatusCode, string(body))
	}

	var gist Gist
	if err := json.NewDecoder(resp.Body).Decode(&gist); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	file, ok := gist.Files[filename]
	if !ok {
		return "", fmt.Errorf("file %q not found in gist", filename)
	}

	c.logger.Debug("loaded from gist",
		zap.String("filename", filename),
		zap.Int("bytes", len(file.Content)),
	)

	return file.Content, nil
}

// GetGistID returns the current gist ID.
func (c *Client) GetGistID() string {
	return c.gistID
}
