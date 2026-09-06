package admin

import (
	"bufio"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	// Default session duration: 12 hours
	sessionTTL = 12 * time.Hour
	// Cookie name for admin session
	adminCookieName = "dvfs_admin_token"
)

// LoadEnv loads environment variables from .env files if not already set in the process.
func LoadEnv(paths ...string) {
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				key := strings.TrimSpace(parts[0])
				val := strings.TrimSpace(parts[1])
				val = strings.Trim(val, `"'`)
				if os.Getenv(key) == "" {
					_ = os.Setenv(key, val)
				}
			}
		}
		_ = f.Close()
	}
}

// AuthManager handles password hash verification, session token generation, and route protection.
type AuthManager struct {
	hash     string               // Target SHA-256 hash in lowercase hex
	sessions map[string]time.Time // token -> expiry time
	mu       sync.RWMutex
}

// NewAuthManager initializes the authentication manager by loading .env if available.
func NewAuthManager(envPaths ...string) *AuthManager {
	if len(envPaths) == 0 {
		envPaths = []string{".env", "../.env", "../../.env"}
	}
	LoadEnv(envPaths...)

	hash := strings.TrimSpace(strings.ToLower(os.Getenv("ADMIN_PASSWORD_HASH")))
	if hash == "" {
		log.Printf("[ADMIN AUTH] Warning: ADMIN_PASSWORD_HASH is not set. Admin actions will be inaccessible.")
	} else {
		log.Printf("[ADMIN AUTH] Initialized with password hash: %s...%s", hash[:4], hash[len(hash)-4:])
	}

	return &AuthManager{
		hash:     hash,
		sessions: make(map[string]time.Time),
	}
}

// SetHash manually updates the password hash (primarily for tests).
func (am *AuthManager) SetHash(hash string) {
	am.mu.Lock()
	defer am.mu.Unlock()
	am.hash = strings.TrimSpace(strings.ToLower(hash))
}

// GetHash returns the configured password hash.
func (am *AuthManager) GetHash() string {
	am.mu.RLock()
	defer am.mu.RUnlock()
	return am.hash
}

// VerifyPassword hashes the candidate password using SHA-256 and compares it constant-time with am.hash.
// Strictly NO plaintext verification is supported.
func (am *AuthManager) VerifyPassword(candidate string) bool {
	am.mu.RLock()
	expectedHash := am.hash
	am.mu.RUnlock()

	if expectedHash == "" {
		return false
	}

	sum := sha256.Sum256([]byte(candidate))
	candidateHash := hex.EncodeToString(sum[:])

	return subtle.ConstantTimeCompare([]byte(candidateHash), []byte(expectedHash)) == 1
}

// CreateSession generates a 32-byte cryptographically secure session token, stores it with sessionTTL,
// and returns the hex token string.
func (am *AuthManager) CreateSession() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	token := hex.EncodeToString(b)

	am.mu.Lock()
	defer am.mu.Unlock()

	// Opportunistic cleanup of expired sessions
	now := time.Now()
	for k, exp := range am.sessions {
		if now.After(exp) {
			delete(am.sessions, k)
		}
	}

	am.sessions[token] = now.Add(sessionTTL)
	return token
}

// RevokeSession invalidates a session token immediately.
func (am *AuthManager) RevokeSession(token string) {
	if token == "" {
		return
	}
	am.mu.Lock()
	defer am.mu.Unlock()
	delete(am.sessions, token)
}

// ExtractToken retrieves the auth token from:
// 1. Authorization: Bearer <token>
// 2. Cookie: dvfs_admin_token=<token>
// 3. Query parameter: ?token=<token>
func (am *AuthManager) ExtractToken(r *http.Request) string {
	// 1. Bearer header
	authHeader := r.Header.Get("Authorization")
	if strings.HasPrefix(authHeader, "Bearer ") {
		token := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
		if token != "" {
			return token
		}
	}

	// 2. Cookie
	if cookie, err := r.Cookie(adminCookieName); err == nil && cookie.Value != "" {
		return cookie.Value
	}

	// 3. Query param (for WebSocket handshake)
	if qToken := r.URL.Query().Get("token"); qToken != "" {
		return qToken
	}

	return ""
}

// IsAuthenticated verifies whether the request carries a valid, non-expired session token.
func (am *AuthManager) IsAuthenticated(r *http.Request) bool {
	token := am.ExtractToken(r)
	if token == "" {
		return false
	}

	am.mu.RLock()
	expiry, exists := am.sessions[token]
	am.mu.RUnlock()

	if !exists {
		return false
	}

	if time.Now().After(expiry) {
		am.RevokeSession(token)
		return false
	}

	return true
}

// RequireAuth wraps an http.HandlerFunc, returning 401 Unauthorized if the request is unauthenticated.
func (am *AuthManager) RequireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !am.IsAuthenticated(r) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"error":   "admin authentication required",
			})
			return
		}
		next(w, r)
	}
}
