package config

import (
	"sync"
	"time"
)

// ConfigObserver is an interface for components that need to be notified of config changes.
type ConfigObserver interface {
	OnConfigUpdate(cfg *Config)
}

// LiveConfig is a thread-safe wrapper around Config that supports hot-reload.
type LiveConfig struct {
	mu        sync.RWMutex
	config    *Config
	observers []ConfigObserver
	obsMu     sync.RWMutex

	// Track when config was last updated
	lastUpdated time.Time
}

// NewLiveConfig creates a new LiveConfig with the given initial config.
func NewLiveConfig(initial *Config) *LiveConfig {
	if initial == nil {
		initial = Defaults()
	}
	return &LiveConfig{
		config:      initial.Clone(),
		observers:   make([]ConfigObserver, 0),
		lastUpdated: time.Now(),
	}
}

// Get returns a copy of the current config.
// This is safe to call from multiple goroutines.
func (lc *LiveConfig) Get() *Config {
	lc.mu.RLock()
	defer lc.mu.RUnlock()
	return lc.config.Clone()
}

// GetDirect returns a pointer to the current config without cloning.
// WARNING: This is faster but the caller must NOT modify the returned config.
// Use this only for read-only access in hot paths.
func (lc *LiveConfig) GetDirect() *Config {
	lc.mu.RLock()
	defer lc.mu.RUnlock()
	return lc.config
}

// Update atomically updates the config after validation.
// Returns an error if validation fails.
// Notifies all observers of the change.
func (lc *LiveConfig) Update(newConfig *Config) error {
	if newConfig == nil {
		return nil
	}

	// Validate the new config
	result := newConfig.Validate()
	if !result.Valid {
		return &ConfigValidationError{Errors: result.Errors}
	}

	// Clone to ensure we own the data
	cloned := newConfig.Clone()

	// Update the config
	lc.mu.Lock()
	lc.config = cloned
	lc.lastUpdated = time.Now()
	lc.mu.Unlock()

	// Notify observers (outside of lock to avoid deadlocks)
	lc.notifyObservers(cloned)

	return nil
}

// UpdatePartial updates specific fields of the config.
// Takes a function that modifies the config in place.
func (lc *LiveConfig) UpdatePartial(updateFn func(*Config)) error {
	lc.mu.Lock()
	newConfig := lc.config.Clone()
	lc.mu.Unlock()

	// Apply the update
	updateFn(newConfig)

	// Validate and set
	return lc.Update(newConfig)
}

// AddObserver registers an observer to be notified of config changes.
func (lc *LiveConfig) AddObserver(obs ConfigObserver) {
	if obs == nil {
		return
	}
	lc.obsMu.Lock()
	defer lc.obsMu.Unlock()
	lc.observers = append(lc.observers, obs)
}

// RemoveObserver removes an observer from the notification list.
func (lc *LiveConfig) RemoveObserver(obs ConfigObserver) {
	if obs == nil {
		return
	}
	lc.obsMu.Lock()
	defer lc.obsMu.Unlock()
	for i, o := range lc.observers {
		if o == obs {
			lc.observers = append(lc.observers[:i], lc.observers[i+1:]...)
			return
		}
	}
}

// notifyObservers notifies all registered observers of a config change.
func (lc *LiveConfig) notifyObservers(cfg *Config) {
	lc.obsMu.RLock()
	observers := make([]ConfigObserver, len(lc.observers))
	copy(observers, lc.observers)
	lc.obsMu.RUnlock()

	for _, obs := range observers {
		// Clone for each observer to prevent mutations
		obs.OnConfigUpdate(cfg.Clone())
	}
}

// LastUpdated returns when the config was last updated.
func (lc *LiveConfig) LastUpdated() time.Time {
	lc.mu.RLock()
	defer lc.mu.RUnlock()
	return lc.lastUpdated
}

// ConfigValidationError is returned when config validation fails.
type ConfigValidationError struct {
	Errors []ValidationError
}

func (e *ConfigValidationError) Error() string {
	if len(e.Errors) == 0 {
		return "config validation failed"
	}
	return "config validation failed: " + e.Errors[0].Field + ": " + e.Errors[0].Message
}
