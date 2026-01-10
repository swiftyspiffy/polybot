package app

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"polybot/clients/gist"
	"strings"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"go.uber.org/zap"
)

const (
	passkeysFileName       = "passkeys.json"
	cookieName             = "polybot_session"
	cookieMaxAge           = 7 * 24 * time.Hour
	registrationTimeout    = 10 * time.Minute
	sessionDataExpiry      = 5 * time.Minute
)

// PasskeyCredential stores a registered passkey.
type PasskeyCredential struct {
	ID              []byte    `json:"id"`
	PublicKey       []byte    `json:"public_key"`
	AttestationType string    `json:"attestation_type"`
	AAGUID          []byte    `json:"aaguid"`
	SignCount       uint32    `json:"sign_count"`
	BackupEligible  bool      `json:"backup_eligible"`
	BackupState     bool      `json:"backup_state"`
	RegisteredAt    time.Time `json:"registered_at"`
	Country         string    `json:"country,omitempty"`
	Username        string    `json:"username"`
	DisplayName     string    `json:"display_name"`
	IsAdmin         bool      `json:"is_admin"`
}

// IPGeoInfo holds geolocation info from ip-api.com.
type IPGeoInfo struct {
	Status  string `json:"status"`
	Country string `json:"country"`
}

// PasskeyStore is the passkeys.json structure.
type PasskeyStore struct {
	Version     int                 `json:"version"`
	UpdatedAt   time.Time           `json:"updated_at"`
	Credentials []PasskeyCredential `json:"credentials"`
}

// SessionCookie represents the signed session cookie data.
type SessionCookie struct {
	CredentialID []byte    `json:"cid"`
	Username     string    `json:"user"`
	IsAdmin      bool      `json:"admin"`
	ExpiresAt    time.Time `json:"exp"`
}

// passkeyUser implements webauthn.User interface.
type passkeyUser struct {
	id          []byte
	credentials []webauthn.Credential
}

func (u *passkeyUser) WebAuthnID() []byte                         { return u.id }
func (u *passkeyUser) WebAuthnName() string                       { return "user" }
func (u *passkeyUser) WebAuthnDisplayName() string                { return "Polybot User" }
func (u *passkeyUser) WebAuthnCredentials() []webauthn.Credential { return u.credentials }
func (u *passkeyUser) WebAuthnIcon() string                       { return "" }

// pendingSession holds WebAuthn session data during registration/login.
type pendingSession struct {
	sessionData *webauthn.SessionData
	createdAt   time.Time
	isRegister  bool
	username    string
	displayName string
}

// AuthHandler handles passkey authentication.
type AuthHandler struct {
	logger     *zap.Logger
	gistClient *gist.Client
	gistID     string
	webauthn   *webauthn.WebAuthn

	mu                      sync.RWMutex
	credentials             []PasskeyCredential
	loaded                  bool
	loadedCh                chan struct{}
	registrationEnabled     bool
	registrationExpiry      time.Time
	registrationUsesLeft    int // 0 means unlimited (time-based only)
	pendingSessions         map[string]*pendingSession
	cookieKey               []byte
}

// NewAuthHandler creates a new AuthHandler.
func NewAuthHandler(logger *zap.Logger, gistClient *gist.Client, gistID string, rpID string, rpOrigins []string) (*AuthHandler, error) {
	if logger == nil {
		logger = zap.NewNop()
	}

	// Generate random cookie signing key
	cookieKey := make([]byte, 32)
	if _, err := rand.Read(cookieKey); err != nil {
		return nil, fmt.Errorf("generate cookie key: %w", err)
	}

	// Configure WebAuthn
	wconfig := &webauthn.Config{
		RPDisplayName: "Polybot",
		RPID:          rpID,
		RPOrigins:     rpOrigins,
	}

	w, err := webauthn.New(wconfig)
	if err != nil {
		return nil, fmt.Errorf("create webauthn: %w", err)
	}

	return &AuthHandler{
		logger:          logger,
		gistClient:      gistClient,
		gistID:          gistID,
		webauthn:        w,
		loadedCh:        make(chan struct{}),
		pendingSessions: make(map[string]*pendingSession),
		cookieKey:       cookieKey,
	}, nil
}

// IsEnabled returns true if passkey auth is configured.
func (h *AuthHandler) IsEnabled() bool {
	return h.gistClient != nil && h.gistID != ""
}

// RegisterRoutes registers auth HTTP routes.
func (h *AuthHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/auth/status", h.handleAuthStatus)
	mux.HandleFunc("/api/auth/register/begin", h.handleBeginRegister)
	mux.HandleFunc("/api/auth/register/finish", h.handleFinishRegister)
	mux.HandleFunc("/api/auth/login/begin", h.handleBeginLogin)
	mux.HandleFunc("/api/auth/login/finish", h.handleFinishLogin)
	mux.HandleFunc("/api/auth/logout", h.handleLogout)
	mux.HandleFunc("/api/auth/enable-registration", h.handleEnableRegistration)
	mux.HandleFunc("/api/auth/credentials", h.handleCredentials)
}

// LoadCredentials loads passkeys from gist.
func (h *AuthHandler) LoadCredentials(ctx context.Context) error {
	if !h.IsEnabled() {
		h.mu.Lock()
		h.loaded = true
		close(h.loadedCh)
		h.mu.Unlock()
		return nil
	}

	content, err := h.gistClient.Load(ctx, passkeysFileName, h.gistID)
	if err != nil {
		// File not found is normal for first-time use
		if strings.Contains(err.Error(), "not found") {
			h.logger.Debug("no passkeys file found, starting fresh")
			h.mu.Lock()
			h.loaded = true
			close(h.loadedCh)
			h.mu.Unlock()
			return nil
		}
		h.mu.Lock()
		h.loaded = true
		close(h.loadedCh)
		h.mu.Unlock()
		return fmt.Errorf("load passkeys from gist: %w", err)
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if content == "" {
		h.logger.Debug("no passkeys file found, starting fresh")
		h.loaded = true
		close(h.loadedCh)
		return nil
	}

	var store PasskeyStore
	if err := json.Unmarshal([]byte(content), &store); err != nil {
		h.loaded = true
		close(h.loadedCh)
		return fmt.Errorf("parse passkeys: %w", err)
	}

	h.credentials = store.Credentials
	h.loaded = true
	close(h.loadedCh)

	h.logger.Info("loaded passkeys from gist",
		zap.Int("count", len(h.credentials)),
	)

	return nil
}

// WaitForLoad blocks until credentials are loaded.
func (h *AuthHandler) WaitForLoad(ctx context.Context) error {
	select {
	case <-h.loadedCh:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// HasCredentials returns true if any passkeys are registered.
func (h *AuthHandler) HasCredentials() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.credentials) > 0
}

// IsAuthenticated checks if the request has a valid session.
func (h *AuthHandler) IsAuthenticated(r *http.Request) bool {
	session := h.GetSession(r)
	return session != nil
}

// GetSession returns the session from the request cookie, or nil if invalid.
func (h *AuthHandler) GetSession(r *http.Request) *SessionCookie {
	cookie, err := r.Cookie(cookieName)
	if err != nil {
		return nil
	}

	session, err := h.verifyCookie(cookie.Value)
	if err != nil {
		return nil
	}

	if time.Now().After(session.ExpiresAt) {
		return nil
	}

	return session
}

// saveCredentials saves passkeys to gist.
func (h *AuthHandler) saveCredentials(ctx context.Context) error {
	store := PasskeyStore{
		Version:     1,
		UpdatedAt:   time.Now(),
		Credentials: h.credentials,
	}

	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal passkeys: %w", err)
	}

	if err := h.gistClient.Save(ctx, passkeysFileName, string(data), h.gistID); err != nil {
		return fmt.Errorf("save passkeys to gist: %w", err)
	}

	return nil
}

// signCookie creates an HMAC-signed cookie value.
func (h *AuthHandler) signCookie(session *SessionCookie) (string, error) {
	data, err := json.Marshal(session)
	if err != nil {
		return "", err
	}

	payload := base64.RawURLEncoding.EncodeToString(data)
	mac := hmac.New(sha256.New, h.cookieKey)
	mac.Write([]byte(payload))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	return payload + "." + sig, nil
}

// verifyCookie validates and parses a signed cookie.
func (h *AuthHandler) verifyCookie(value string) (*SessionCookie, error) {
	parts := strings.SplitN(value, ".", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid cookie format")
	}

	payload, sigStr := parts[0], parts[1]

	sig, err := base64.RawURLEncoding.DecodeString(sigStr)
	if err != nil {
		return nil, fmt.Errorf("decode signature: %w", err)
	}

	mac := hmac.New(sha256.New, h.cookieKey)
	mac.Write([]byte(payload))
	expectedSig := mac.Sum(nil)

	if !hmac.Equal(sig, expectedSig) {
		return nil, fmt.Errorf("invalid signature")
	}

	data, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return nil, fmt.Errorf("decode payload: %w", err)
	}

	var session SessionCookie
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, fmt.Errorf("parse session: %w", err)
	}

	return &session, nil
}

// setSessionCookie sets the session cookie on the response.
func (h *AuthHandler) setSessionCookie(w http.ResponseWriter, session *SessionCookie) error {
	value, err := h.signCookie(session)
	if err != nil {
		return err
	}

	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    value,
		Path:     "/",
		MaxAge:   int(cookieMaxAge.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   true,
	})

	return nil
}

// clearSessionCookie clears the session cookie.
func (h *AuthHandler) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   true,
	})
}

// getClientIP extracts the client IP from the request.
func getClientIP(r *http.Request) string {
	// Check X-Forwarded-For header first (for reverse proxies)
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}

	// Check X-Real-IP header
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}

	// Fall back to RemoteAddr
	addr := r.RemoteAddr
	if idx := strings.LastIndex(addr, ":"); idx != -1 {
		return addr[:idx]
	}
	return addr
}

// lookupIPGeo fetches geolocation info for an IP address from ip-api.com.
func lookupIPGeo(ctx context.Context, ip string) *IPGeoInfo {
	// Skip private/local IPs
	if strings.HasPrefix(ip, "10.") || strings.HasPrefix(ip, "192.168.") ||
		strings.HasPrefix(ip, "172.") || strings.HasPrefix(ip, "127.") ||
		ip == "::1" || ip == "localhost" {
		return nil
	}

	url := fmt.Sprintf("http://ip-api.com/json/%s?fields=status,country,city,isp", ip)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil
	}

	var geo IPGeoInfo
	if err := json.NewDecoder(resp.Body).Decode(&geo); err != nil {
		return nil
	}

	if geo.Status != "success" {
		return nil
	}

	return &geo
}

// canRegister checks if registration is allowed.
func (h *AuthHandler) canRegister(r *http.Request) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()

	// If no credentials, anyone can register
	if len(h.credentials) == 0 {
		return true
	}

	// Check if registration is enabled
	if !h.registrationEnabled {
		return false
	}

	// Check time-based expiry
	if time.Now().After(h.registrationExpiry) {
		return false
	}

	// Check usage-based limit (0 means unlimited)
	if h.registrationUsesLeft < 0 {
		return false
	}

	return true
}

// cleanupPendingSessions removes expired pending sessions.
func (h *AuthHandler) cleanupPendingSessions() {
	now := time.Now()
	for id, session := range h.pendingSessions {
		if now.Sub(session.createdAt) > sessionDataExpiry {
			delete(h.pendingSessions, id)
		}
	}
}

// handleAuthStatus returns the current auth status.
func (h *AuthHandler) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	session := h.GetSession(r)

	h.mu.RLock()
	hasCredentials := len(h.credentials) > 0
	registrationEnabled := h.registrationEnabled && time.Now().Before(h.registrationExpiry)
	registrationExpiry := h.registrationExpiry
	registrationUsesLeft := h.registrationUsesLeft
	h.mu.RUnlock()

	// Check if uses are exhausted
	if registrationEnabled && registrationUsesLeft < 0 {
		registrationEnabled = false
	}

	status := map[string]interface{}{
		"enabled":           h.IsEnabled(),
		"has_credentials":   hasCredentials,
		"authenticated":     session != nil,
		"is_admin":          session != nil && session.IsAdmin,
		"registration_open": registrationEnabled || !hasCredentials,
	}
	if session != nil {
		status["username"] = session.Username
	}

	// Include registration details for admins
	if session != nil && session.IsAdmin && hasCredentials {
		regInfo := map[string]interface{}{
			"enabled": registrationEnabled,
		}
		if registrationEnabled {
			regInfo["expires_at"] = registrationExpiry
			if registrationUsesLeft > 0 {
				regInfo["uses_left"] = registrationUsesLeft
			}
		}
		status["registration"] = regInfo
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

// handleBeginRegister starts the WebAuthn registration ceremony.
func (h *AuthHandler) handleBeginRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !h.canRegister(r) {
		http.Error(w, "Registration not allowed", http.StatusForbidden)
		return
	}

	// Parse request body
	var req struct {
		Username    string `json:"username"`
		DisplayName string `json:"display_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validate username
	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Username is required"})
		return
	}
	if len(req.Username) > 50 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Username must be 50 characters or less"})
		return
	}

	if req.DisplayName == "" {
		req.DisplayName = "My Device"
	}

	// Use fixed user ID for consistent authentication
	// (all passkeys belong to the same logical "polybot" user)
	userID := []byte("polybot-user")

	user := &passkeyUser{
		id:          userID,
		credentials: []webauthn.Credential{},
	}

	options, sessionData, err := h.webauthn.BeginRegistration(user,
		webauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{
			ResidentKey:      protocol.ResidentKeyRequirementPreferred,
			UserVerification: protocol.VerificationPreferred,
		}),
	)
	if err != nil {
		h.logger.Error("begin registration failed", zap.Error(err))
		http.Error(w, "Failed to begin registration", http.StatusInternalServerError)
		return
	}

	// Generate random session ID for tracking
	sessionIDBytes := make([]byte, 32)
	rand.Read(sessionIDBytes)
	sessionID := base64.RawURLEncoding.EncodeToString(sessionIDBytes)

	// Store session data with username and display name
	h.mu.Lock()
	h.cleanupPendingSessions()
	h.pendingSessions[sessionID] = &pendingSession{
		sessionData: sessionData,
		createdAt:   time.Now(),
		isRegister:  true,
		username:    req.Username,
		displayName: req.DisplayName,
	}
	h.mu.Unlock()

	// Return options with session ID
	response := map[string]interface{}{
		"sessionId": sessionID,
		"options":   options,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleFinishRegister completes the WebAuthn registration ceremony.
func (h *AuthHandler) handleFinishRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !h.canRegister(r) {
		http.Error(w, "Registration not allowed", http.StatusForbidden)
		return
	}

	// Parse the JSON body to get sessionId and credential
	var wrapper struct {
		SessionID  string          `json:"sessionId"`
		Credential json.RawMessage `json:"credential"`
	}
	if err := json.NewDecoder(r.Body).Decode(&wrapper); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	sessionID := wrapper.SessionID
	credentialData := wrapper.Credential

	h.mu.Lock()
	pending, ok := h.pendingSessions[sessionID]
	if !ok {
		h.mu.Unlock()
		http.Error(w, "Invalid or expired session", http.StatusBadRequest)
		return
	}
	delete(h.pendingSessions, sessionID)
	isFirstUser := len(h.credentials) == 0
	h.mu.Unlock()

	// Use fixed user ID (same as registration begin)
	user := &passkeyUser{
		id:          []byte("polybot-user"),
		credentials: []webauthn.Credential{},
	}

	// Create a new request with the credential JSON for WebAuthn library
	h.logger.Debug("credential data for WebAuthn", zap.String("data", string(credentialData)))
	newReq, _ := http.NewRequest(r.Method, r.URL.String(), bytes.NewReader(credentialData))
	newReq.Header = r.Header

	credential, err := h.webauthn.FinishRegistration(user, *pending.sessionData, newReq)
	if err != nil {
		h.logger.Error("finish registration failed",
			zap.Error(err),
			zap.String("credentialData", string(credentialData)),
		)
		http.Error(w, "Failed to complete registration: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Create new passkey credential
	clientIP := getClientIP(r)

	// Lookup country for the IP (non-blocking, best-effort)
	geoCtx, geoCancel := context.WithTimeout(r.Context(), 5*time.Second)
	geo := lookupIPGeo(geoCtx, clientIP)
	geoCancel()

	// Use username and displayName from pending session
	username := pending.username
	displayName := pending.displayName
	if displayName == "" {
		displayName = "My Device"
	}

	newCred := PasskeyCredential{
		ID:              credential.ID,
		PublicKey:       credential.PublicKey,
		AttestationType: string(credential.AttestationType),
		AAGUID:          credential.Authenticator.AAGUID,
		SignCount:       credential.Authenticator.SignCount,
		BackupEligible:  credential.Flags.BackupEligible,
		BackupState:     credential.Flags.BackupState,
		RegisteredAt:    time.Now(),
		Username:        username,
		DisplayName:     displayName,
		IsAdmin:         isFirstUser,
	}

	if geo != nil {
		newCred.Country = geo.Country
	}

	if newCred.DisplayName == "" {
		newCred.DisplayName = "Passkey " + time.Now().Format("2006-01-02 15:04")
	}

	h.mu.Lock()
	h.credentials = append(h.credentials, newCred)
	// Decrement usage counter if usage-based limit is set
	if h.registrationUsesLeft > 0 {
		h.registrationUsesLeft--
		if h.registrationUsesLeft == 0 {
			h.registrationEnabled = false
		}
	}
	h.mu.Unlock()

	// Save to gist
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	h.mu.RLock()
	err = h.saveCredentials(ctx)
	h.mu.RUnlock()

	if err != nil {
		h.logger.Error("failed to save credentials", zap.Error(err))
		// Don't fail the registration, just log
	}

	// Set session cookie
	session := &SessionCookie{
		CredentialID: credential.ID,
		Username:     username,
		IsAdmin:      isFirstUser,
		ExpiresAt:    time.Now().Add(cookieMaxAge),
	}

	if err := h.setSessionCookie(w, session); err != nil {
		h.logger.Error("failed to set session cookie", zap.Error(err))
	}

	h.logger.Info("passkey registered",
		zap.String("username", newCred.Username),
		zap.String("displayName", newCred.DisplayName),
		zap.Bool("isAdmin", newCred.IsAdmin),
	)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"isAdmin": isFirstUser,
	})
}

// handleBeginLogin starts the WebAuthn login ceremony.
func (h *AuthHandler) handleBeginLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	h.mu.RLock()
	if len(h.credentials) == 0 {
		h.mu.RUnlock()
		http.Error(w, "No credentials registered", http.StatusBadRequest)
		return
	}

	// Build allowed credentials list
	allowedCreds := make([]webauthn.Credential, len(h.credentials))
	for i, cred := range h.credentials {
		allowedCreds[i] = webauthn.Credential{
			ID:              cred.ID,
			PublicKey:       cred.PublicKey,
			AttestationType: cred.AttestationType,
			Authenticator: webauthn.Authenticator{
				AAGUID:    cred.AAGUID,
				SignCount: cred.SignCount,
			},
			Flags: webauthn.CredentialFlags{
				BackupEligible: cred.BackupEligible,
				BackupState:    cred.BackupState,
			},
		}
	}
	h.mu.RUnlock()

	user := &passkeyUser{
		id:          []byte("polybot-user"),
		credentials: allowedCreds,
	}

	options, sessionData, err := h.webauthn.BeginLogin(user)
	if err != nil {
		h.logger.Error("begin login failed", zap.Error(err))
		http.Error(w, "Failed to begin login", http.StatusInternalServerError)
		return
	}

	// Store session data (Challenge is already base64url encoded)
	sessionID := string(sessionData.Challenge)
	h.mu.Lock()
	h.cleanupPendingSessions()
	h.pendingSessions[sessionID] = &pendingSession{
		sessionData: sessionData,
		createdAt:   time.Now(),
		isRegister:  false,
	}
	h.mu.Unlock()

	response := map[string]interface{}{
		"sessionId": sessionID,
		"options":   options,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleFinishLogin completes the WebAuthn login ceremony.
func (h *AuthHandler) handleFinishLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse session ID from request
	var wrapper struct {
		SessionID  string          `json:"sessionId"`
		Credential json.RawMessage `json:"credential"`
	}
	if err := json.NewDecoder(r.Body).Decode(&wrapper); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	h.mu.Lock()
	pending, ok := h.pendingSessions[wrapper.SessionID]
	if !ok {
		h.mu.Unlock()
		http.Error(w, "Invalid or expired session", http.StatusBadRequest)
		return
	}
	delete(h.pendingSessions, wrapper.SessionID)

	// Build allowed credentials
	allowedCreds := make([]webauthn.Credential, len(h.credentials))
	for i, cred := range h.credentials {
		allowedCreds[i] = webauthn.Credential{
			ID:              cred.ID,
			PublicKey:       cred.PublicKey,
			AttestationType: cred.AttestationType,
			Authenticator: webauthn.Authenticator{
				AAGUID:    cred.AAGUID,
				SignCount: cred.SignCount,
			},
			Flags: webauthn.CredentialFlags{
				BackupEligible: cred.BackupEligible,
				BackupState:    cred.BackupState,
			},
		}
	}
	h.mu.Unlock()

	user := &passkeyUser{
		id:          []byte("polybot-user"),
		credentials: allowedCreds,
	}

	// Create a new request with the credential JSON
	newReq, _ := http.NewRequest(r.Method, r.URL.String(), bytes.NewReader(wrapper.Credential))
	newReq.Header = r.Header

	credential, err := h.webauthn.FinishLogin(user, *pending.sessionData, newReq)
	if err != nil {
		h.logger.Error("finish login failed", zap.Error(err))
		http.Error(w, "Failed to complete login: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Find the matching credential and get user info
	var isAdmin bool
	var username string
	h.mu.Lock()
	for i, cred := range h.credentials {
		if string(cred.ID) == string(credential.ID) {
			isAdmin = cred.IsAdmin
			username = cred.Username
			// Update sign count
			h.credentials[i].SignCount = credential.Authenticator.SignCount
			break
		}
	}
	h.mu.Unlock()

	// Set session cookie
	session := &SessionCookie{
		CredentialID: credential.ID,
		Username:     username,
		IsAdmin:      isAdmin,
		ExpiresAt:    time.Now().Add(cookieMaxAge),
	}

	if err := h.setSessionCookie(w, session); err != nil {
		h.logger.Error("failed to set session cookie", zap.Error(err))
	}

	h.logger.Info("passkey login successful",
		zap.String("ip", getClientIP(r)),
		zap.Bool("isAdmin", isAdmin),
	)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"isAdmin": isAdmin,
	})
}

// handleLogout clears the session cookie.
func (h *AuthHandler) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	h.clearSessionCookie(w)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// handleEnableRegistration enables or disables registration (admin only).
// POST: Enable registration with optional minutes/uses parameters
// DELETE: Disable registration
func (h *AuthHandler) handleEnableRegistration(w http.ResponseWriter, r *http.Request) {
	session := h.GetSession(r)
	if session == nil || !session.IsAdmin {
		http.Error(w, "Admin access required", http.StatusForbidden)
		return
	}

	switch r.Method {
	case http.MethodPost:
		h.enableRegistration(w, r)
	case http.MethodDelete:
		h.disableRegistration(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// enableRegistration enables registration with optional time/usage limits.
func (h *AuthHandler) enableRegistration(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Minutes int `json:"minutes"` // Time limit in minutes (default: 10)
		Uses    int `json:"uses"`    // Usage limit (0 = unlimited, time-based only)
	}

	// Parse request body (optional)
	if r.Body != nil && r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}
	}

	// Default to 10 minutes if not specified
	if req.Minutes <= 0 {
		req.Minutes = 10
	}

	// Cap at 60 minutes
	if req.Minutes > 60 {
		req.Minutes = 60
	}

	duration := time.Duration(req.Minutes) * time.Minute

	h.mu.Lock()
	h.registrationEnabled = true
	h.registrationExpiry = time.Now().Add(duration)
	h.registrationUsesLeft = req.Uses
	h.mu.Unlock()

	h.logger.Info("registration enabled by admin",
		zap.Int("minutes", req.Minutes),
		zap.Int("uses", req.Uses),
	)

	response := map[string]interface{}{
		"success":   true,
		"expiresAt": h.registrationExpiry,
		"minutes":   req.Minutes,
	}
	if req.Uses > 0 {
		response["uses"] = req.Uses
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// disableRegistration disables registration immediately.
func (h *AuthHandler) disableRegistration(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	h.registrationEnabled = false
	h.registrationExpiry = time.Time{}
	h.registrationUsesLeft = 0
	h.mu.Unlock()

	h.logger.Info("registration disabled by admin")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// handleCredentials handles listing and removing credentials (admin only).
func (h *AuthHandler) handleCredentials(w http.ResponseWriter, r *http.Request) {
	session := h.GetSession(r)
	if session == nil || !session.IsAdmin {
		http.Error(w, "Admin access required", http.StatusForbidden)
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.listCredentials(w, r)
	case http.MethodDelete:
		h.removeCredential(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// listCredentials returns all registered credentials.
func (h *AuthHandler) listCredentials(w http.ResponseWriter, r *http.Request) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	// Count admins to determine if credential can be deleted
	adminCount := 0
	for _, cred := range h.credentials {
		if cred.IsAdmin {
			adminCount++
		}
	}

	// Return credentials without sensitive data
	creds := make([]map[string]interface{}, len(h.credentials))
	for i, cred := range h.credentials {
		// Can delete if not the last admin
		canDelete := !cred.IsAdmin || adminCount > 1

		creds[i] = map[string]interface{}{
			"id":            base64.RawURLEncoding.EncodeToString(cred.ID),
			"username":      cred.Username,
			"display_name":  cred.DisplayName,
			"registered_at": cred.RegisteredAt,
			"country":       cred.Country,
			"is_admin":      cred.IsAdmin,
			"can_delete":    canDelete,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"credentials": creds,
	})
}

// removeCredential removes a credential by ID.
func (h *AuthHandler) removeCredential(w http.ResponseWriter, r *http.Request) {
	credIDStr := r.URL.Query().Get("id")
	if credIDStr == "" {
		http.Error(w, "Missing credential ID", http.StatusBadRequest)
		return
	}

	credID, err := base64.RawURLEncoding.DecodeString(credIDStr)
	if err != nil {
		http.Error(w, "Invalid credential ID", http.StatusBadRequest)
		return
	}

	h.mu.Lock()

	// Find and remove the credential
	var found bool
	var isLastAdmin bool
	adminCount := 0

	for _, cred := range h.credentials {
		if cred.IsAdmin {
			adminCount++
		}
	}

	newCreds := make([]PasskeyCredential, 0, len(h.credentials))
	for _, cred := range h.credentials {
		if string(cred.ID) == string(credID) {
			found = true
			if cred.IsAdmin && adminCount <= 1 {
				isLastAdmin = true
			}
			continue
		}
		newCreds = append(newCreds, cred)
	}

	if !found {
		h.mu.Unlock()
		http.Error(w, "Credential not found", http.StatusNotFound)
		return
	}

	if isLastAdmin {
		h.mu.Unlock()
		http.Error(w, "Cannot remove the last admin credential", http.StatusBadRequest)
		return
	}

	h.credentials = newCreds
	h.mu.Unlock()

	// Save to gist
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	h.mu.RLock()
	err = h.saveCredentials(ctx)
	h.mu.RUnlock()

	if err != nil {
		h.logger.Error("failed to save credentials after removal", zap.Error(err))
	}

	h.logger.Info("passkey removed",
		zap.String("credentialID", credIDStr),
	)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// jsonReader wraps json.RawMessage to implement io.ReadCloser.
type jsonReader struct {
	data   []byte
	offset int
}

func newJSONReader(data json.RawMessage) *jsonReader {
	return &jsonReader{data: data}
}

func (r *jsonReader) Read(p []byte) (n int, err error) {
	if r.offset >= len(r.data) {
		return 0, io.EOF
	}
	n = copy(p, r.data[r.offset:])
	r.offset += n
	return n, nil
}

func (r *jsonReader) Close() error {
	return nil
}
