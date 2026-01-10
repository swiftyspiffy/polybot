package app

import (
	"context"
	"encoding/json"
	"sync"
)

// MockGistStorage is a mock implementation of gist.Storage for testing.
type MockGistStorage struct {
	mu       sync.RWMutex
	files    map[string]string
	gistID   string
	enabled  bool
	loadErr  error
	saveErr  error
	loadJSON map[string]any
}

// NewMockGistStorage creates a new mock gist storage.
func NewMockGistStorage() *MockGistStorage {
	return &MockGistStorage{
		files:    make(map[string]string),
		gistID:   "mock-gist-id",
		enabled:  true,
		loadJSON: make(map[string]any),
	}
}

// IsEnabled returns whether the mock is enabled.
func (m *MockGistStorage) IsEnabled() bool {
	return m.enabled
}

// SetEnabled sets whether the mock is enabled.
func (m *MockGistStorage) SetEnabled(enabled bool) {
	m.enabled = enabled
}

// Load returns stored content for a filename.
func (m *MockGistStorage) Load(ctx context.Context, filename string, gistID ...string) (string, error) {
	if m.loadErr != nil {
		return "", m.loadErr
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.files[filename], nil
}

// Save stores content for a filename.
func (m *MockGistStorage) Save(ctx context.Context, filename, content string, gistID ...string) error {
	if m.saveErr != nil {
		return m.saveErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.files[filename] = content
	return nil
}

// LoadJSON loads JSON data from a file.
func (m *MockGistStorage) LoadJSON(ctx context.Context, filename string, dest any) error {
	if m.loadErr != nil {
		return m.loadErr
	}
	m.mu.RLock()
	content := m.files[filename]
	m.mu.RUnlock()
	if content == "" {
		return nil
	}
	return json.Unmarshal([]byte(content), dest)
}

// SaveJSON saves JSON data to a file.
func (m *MockGistStorage) SaveJSON(ctx context.Context, filename string, data any) error {
	if m.saveErr != nil {
		return m.saveErr
	}
	jsonData, err := json.Marshal(data)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.files[filename] = string(jsonData)
	return nil
}

// GetGistID returns the mock gist ID.
func (m *MockGistStorage) GetGistID() string {
	return m.gistID
}

// SetLoadError sets an error to be returned on Load calls.
func (m *MockGistStorage) SetLoadError(err error) {
	m.loadErr = err
}

// SetSaveError sets an error to be returned on Save calls.
func (m *MockGistStorage) SetSaveError(err error) {
	m.saveErr = err
}

// SetContent sets the content for a filename.
func (m *MockGistStorage) SetContent(filename, content string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.files[filename] = content
}

// GetContent returns the content for a filename.
func (m *MockGistStorage) GetContent(filename string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.files[filename]
}

// MockHedgeAPIClient is a mock implementation of HedgeAPIClient.
type MockHedgeAPIClient struct {
	positions []struct {
		wallet      string
		conditionID string
		positions   []Position
		err         error
	}
	defaultPositions []Position
	defaultErr       error
}

// Position represents a mock position (simplified from polymarketapi.Position).
type Position struct {
	Outcome    string
	Size       float64
	AvgPrice   float64
	CurPrice   float64
	Redeemable bool
}

// NewMockHedgeAPIClient creates a new mock API client.
func NewMockHedgeAPIClient() *MockHedgeAPIClient {
	return &MockHedgeAPIClient{}
}

// SetPositions sets positions to return for specific wallet/conditionID.
func (m *MockHedgeAPIClient) SetPositions(wallet, conditionID string, positions []Position, err error) {
	m.positions = append(m.positions, struct {
		wallet      string
		conditionID string
		positions   []Position
		err         error
	}{wallet, conditionID, positions, err})
}

// SetDefaultPositions sets default positions to return.
func (m *MockHedgeAPIClient) SetDefaultPositions(positions []Position, err error) {
	m.defaultPositions = positions
	m.defaultErr = err
}
