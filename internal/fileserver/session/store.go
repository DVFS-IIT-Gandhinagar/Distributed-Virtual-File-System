package session

import (
	"errors"
	"log"
	"net"
	"strings"
	"sync"
	"time"
)

var (
	ErrSessionNotFound     = errors.New("session not found")
	ErrSessionExpired      = errors.New("session expired")
	ErrIPMismatch          = errors.New("session transport binding mismatch (IP address differs)")
	ErrUserLimitExceeded   = errors.New("per-user active session limit exceeded")
	ErrGlobalLimitExceeded = errors.New("global session capacity reached (system saturated)")
)

const (
	DefaultIdleTTL       = 15 * time.Minute
	DefaultSweepInterval = 60 * time.Second
	MaxSessionsPerUser   = 5
	MaxGlobalSessions    = 10000
)

// Store provides a decoupled, thread-safe, memory-bounded session store.
type Store struct {
	mu           sync.RWMutex
	sessions     map[string]*Session  // hashID -> *Session
	userSessions map[string][]string // username -> slice of hashIDs (oldest to newest)
	clientIndex  map[string]string   // clientID -> hashID
	idleTTL      time.Duration
	perUserLimit int
	globalLimit  int
	stopChan     chan struct{}
	sweepTicker  *time.Ticker
}

// NewStore initializes a new SessionStore and launches the background sweeper.
func NewStore(idleTTL time.Duration, sweepInterval time.Duration) *Store {
	if idleTTL <= 0 {
		idleTTL = DefaultIdleTTL
	}
	if sweepInterval <= 0 {
		sweepInterval = DefaultSweepInterval
	}

	s := &Store{
		sessions:     make(map[string]*Session),
		userSessions: make(map[string][]string),
		clientIndex:  make(map[string]string),
		idleTTL:      idleTTL,
		perUserLimit: MaxSessionsPerUser,
		globalLimit:  MaxGlobalSessions,
		stopChan:     make(chan struct{}),
		sweepTicker:  time.NewTicker(sweepInterval),
	}

	go s.sweeperLoop()
	return s
}

// CreateSession establishes a new session under strict per-user and global capacity controls.
func (s *Store) CreateSession(
	rawSST string,
	username string,
	clientID string,
	peerAddr string,
	callbackAddr string,
	rootFID string,
	absoluteExp time.Time,
) (*Session, error) {
	if rawSST == "" || username == "" {
		return nil, errors.New("invalid session creation arguments")
	}

	hashID := HashSST(rawSST)
	peerIP := extractIP(peerAddr)
	now := time.Now()

	s.mu.Lock()
	defer s.mu.Unlock()

	// 1. Global Capacity Cap (DoS Prevention)
	if len(s.sessions) >= s.globalLimit {
		s.purgeExpiredLocked(now)
		if len(s.sessions) >= s.globalLimit {
			return nil, ErrGlobalLimitExceeded
		}
	}

	// 2. Per-User Limit Enforcement (FIFO Eviction of Oldest Session)
	userHashes := s.userSessions[username]
	if len(userHashes) >= s.perUserLimit {
		oldestHash := userHashes[0]
		if oldSess, ok := s.sessions[oldestHash]; ok {
			delete(s.clientIndex, oldSess.ClientID)
		}
		delete(s.sessions, oldestHash)
		s.userSessions[username] = userHashes[1:]
		if len(oldestHash) >= 8 {
			log.Printf("[SESSION] Evicted oldest session %s for user %s (cap=%d reached)", oldestHash[:8], username, s.perUserLimit)
		}
	}

	// Remove previous session for same clientID if any
	if prevHash, exists := s.clientIndex[clientID]; exists {
		delete(s.sessions, prevHash)
		s.removeFromUserSliceLocked(username, prevHash)
	}

	sess := &Session{
		HashID:       hashID,
		Username:     username,
		ClientID:     clientID,
		ClientIP:     peerIP,
		CallbackAddr: callbackAddr,
		RootFID:      rootFID,
		CreatedAt:    now,
		LastSeenAt:   now,
		AbsoluteExp:  absoluteExp,
	}

	s.sessions[hashID] = sess
	s.userSessions[username] = append(s.userSessions[username], hashID)
	if clientID != "" {
		s.clientIndex[clientID] = hashID
	}

	return sess, nil
}

// ValidateSession verifies SST authenticity, idle TTL, absolute expiry, and client IP binding.
func (s *Store) ValidateSession(rawSST string, currentPeerAddr string) (*Session, error) {
	if rawSST == "" {
		return nil, ErrSessionNotFound
	}

	hashID := HashSST(rawSST)
	currentIP := extractIP(currentPeerAddr)
	now := time.Now()

	s.mu.RLock()
	sess, exists := s.sessions[hashID]
	if !exists {
		s.mu.RUnlock()
		return nil, ErrSessionNotFound
	}

	// Check Expiration
	if sess.IsExpired(now, s.idleTTL) {
		s.mu.RUnlock()
		go s.RevokeSession(rawSST)
		return nil, ErrSessionExpired
	}

	// Check IP Binding (anti-session hijacking)
	if sess.ClientIP != "" && currentIP != "" && sess.ClientIP != currentIP {
		s.mu.RUnlock()
		log.Printf("[SECURITY ALERT] Session hijacking attempt detected! Bound IP: %s, Current IP: %s (User: %s)", sess.ClientIP, currentIP, sess.Username)
		return nil, ErrIPMismatch
	}
	s.mu.RUnlock()

	// Update sliding window under write lock (throttled check could be done, here we touch safely)
	s.mu.Lock()
	if validSess, ok := s.sessions[hashID]; ok {
		validSess.Touch(now)
	}
	s.mu.Unlock()

	return sess, nil
}

// GetSessionByClientID retrieves an active session for the specified clientID without touching expiry.
func (s *Store) GetSessionByClientID(clientID string) (*Session, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	hashID, exists := s.clientIndex[clientID]
	if !exists {
		return nil, false
	}
	sess, exists := s.sessions[hashID]
	if !exists || sess.IsExpired(time.Now(), s.idleTTL) {
		return nil, false
	}
	return sess, true
}

// RevokeSession explicitly terminates an active session given its raw SST.
func (s *Store) RevokeSession(rawSST string) error {
	hashID := HashSST(rawSST)
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, exists := s.sessions[hashID]
	if !exists {
		return nil
	}

	delete(s.sessions, hashID)
	delete(s.clientIndex, sess.ClientID)
	s.removeFromUserSliceLocked(sess.Username, hashID)
	return nil
}

// RevokeByClientID revokes a session using the client ID.
func (s *Store) RevokeByClientID(clientID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	hashID, exists := s.clientIndex[clientID]
	if !exists {
		return nil
	}

	sess, hasSess := s.sessions[hashID]
	delete(s.clientIndex, clientID)
	delete(s.sessions, hashID)
	if hasSess {
		s.removeFromUserSliceLocked(sess.Username, hashID)
	}
	return nil
}

// RevokeAllForUser terminates all active sessions for a given user.
func (s *Store) RevokeAllForUser(username string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	hashes, ok := s.userSessions[username]
	if !ok {
		return
	}

	for _, h := range hashes {
		if sess, exists := s.sessions[h]; exists {
			delete(s.clientIndex, sess.ClientID)
		}
		delete(s.sessions, h)
	}
	delete(s.userSessions, username)
}

// ActiveCount returns current active session count.
func (s *Store) ActiveCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.sessions)
}

// Close shuts down the background cleaner.
func (s *Store) Close() {
	s.sweepTicker.Stop()
	close(s.stopChan)
}

func (s *Store) sweeperLoop() {
	for {
		select {
		case <-s.stopChan:
			return
		case <-s.sweepTicker.C:
			s.Sweep()
		}
	}
}

// Sweep clears idle and absolutely expired sessions.
func (s *Store) Sweep() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purgeExpiredLocked(time.Now())
}

func (s *Store) purgeExpiredLocked(now time.Time) {
	var expiredHashes []string
	for hashID, sess := range s.sessions {
		if sess.IsExpired(now, s.idleTTL) {
			expiredHashes = append(expiredHashes, hashID)
		}
	}

	for _, hashID := range expiredHashes {
		sess := s.sessions[hashID]
		delete(s.sessions, hashID)
		if sess != nil {
			delete(s.clientIndex, sess.ClientID)
			s.removeFromUserSliceLocked(sess.Username, hashID)
		}
	}

	if len(expiredHashes) > 0 {
		log.Printf("[SESSION SWEEPER] Cleaned %d expired sessions; %d active remaining", len(expiredHashes), len(s.sessions))
	}
}

func (s *Store) removeFromUserSliceLocked(username, hashID string) {
	hashes, ok := s.userSessions[username]
	if !ok {
		return
	}
	newHashes := make([]string, 0, len(hashes))
	for _, h := range hashes {
		if h != hashID {
			newHashes = append(newHashes, h)
		}
	}
	if len(newHashes) == 0 {
		delete(s.userSessions, username)
	} else {
		s.userSessions[username] = newHashes
	}
}

func extractIP(addr string) string {
	if addr == "" {
		return ""
	}
	host, _, err := net.SplitHostPort(addr)
	if err == nil {
		return host
	}
	return strings.Trim(addr, "[]")
}
