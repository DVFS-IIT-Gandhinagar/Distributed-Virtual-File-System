package session

import (
	"context"
)

type sessionContextKey struct{}

// WithSession injects verified session pointer into context.
func WithSession(ctx context.Context, s *Session) context.Context {
	return context.WithValue(ctx, sessionContextKey{}, s)
}

// FromContext extracts the Session pointer from context.
func FromContext(ctx context.Context) (*Session, bool) {
	s, ok := ctx.Value(sessionContextKey{}).(*Session)
	return s, ok && s != nil
}

// UsernameFromContext returns the authenticated user from context.
func UsernameFromContext(ctx context.Context) (string, bool) {
	if s, ok := FromContext(ctx); ok {
		return s.Username, true
	}
	return "", false
}
