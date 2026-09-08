package session

import (
	"time"
)

// Session represents an in-memory authenticated client state.
type Session struct {
	HashID       string    // SHA-256 hash of the SST (primary lookup key)
	Username     string    // Verified identity (Google email)
	ClientID     string    // Client instance identifier
	ClientIP     string    // Bound transport peer IP address
	CallbackAddr string    // Callback endpoint address for file updates
	RootFID      string    // User root directory FID
	CreatedAt    time.Time // Initial registration timestamp
	LastSeenAt   time.Time // Last activity timestamp (idle timeout marker)
	AbsoluteExp  time.Time // Hard expiry ceiling (bounded by Google ID token exp)
}

// IsExpired checks both the sliding idle timeout and the absolute lifetime ceiling.
func (s *Session) IsExpired(now time.Time, idleTTL time.Duration) bool {
	// 1. Check absolute expiration ceiling
	if !s.AbsoluteExp.IsZero() && now.After(s.AbsoluteExp) {
		return true
	}
	// 2. Check sliding idle timeout
	if now.Sub(s.LastSeenAt) > idleTTL {
		return true
	}
	return false
}

// Touch refreshes the sliding last seen timestamp.
func (s *Session) Touch(now time.Time) {
	s.LastSeenAt = now
}
