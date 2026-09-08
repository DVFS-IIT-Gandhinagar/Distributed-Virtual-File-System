package session

import (
	"strings"
	"testing"
	"time"
)

func TestGenerateSST(t *testing.T) {
	sst1, err := GenerateSST()
	if err != nil {
		t.Fatalf("GenerateSST failed: %v", err)
	}
	if !strings.HasPrefix(sst1, SSTPrefix) {
		t.Fatalf("Expected prefix %s, got: %s", SSTPrefix, sst1)
	}
	if len(sst1) < 40 {
		t.Fatalf("Expected high-entropy token length, got: %d", len(sst1))
	}

	sst2, err := GenerateSST()
	if err != nil {
		t.Fatalf("GenerateSST failed: %v", err)
	}
	if sst1 == sst2 {
		t.Fatalf("Expected unique tokens, got identical: %s", sst1)
	}
}

func TestHashSST(t *testing.T) {
	sst, _ := GenerateSST()
	h1 := HashSST(sst)
	h2 := HashSST(sst)
	if h1 != h2 {
		t.Fatalf("HashSST must be deterministic: %s != %s", h1, h2)
	}
	if len(h1) != 64 {
		t.Fatalf("Expected 64-char hex SHA256, got %d", len(h1))
	}
}

func TestSessionStore_CreateAndValidate(t *testing.T) {
	store := NewStore(10*time.Minute, 10*time.Second)
	defer store.Close()

	sst, _ := GenerateSST()
	user := "alice@gmail.com"
	clientID := "client-123"
	peerIP := "192.168.1.50:54321"
	absExp := time.Now().Add(1 * time.Hour)

	sess, err := store.CreateSession(sst, user, clientID, peerIP, "127.0.0.1:4000", "root-fid-1", absExp)
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	if sess.Username != user {
		t.Fatalf("Expected username %s, got %s", user, sess.Username)
	}

	// Validate with same IP
	validated, err := store.ValidateSession(sst, "192.168.1.50:59999")
	if err != nil {
		t.Fatalf("ValidateSession failed: %v", err)
	}
	if validated.Username != user {
		t.Fatalf("Expected %s, got %s", user, validated.Username)
	}

	// Validate with different IP -> Expect IP Mismatch error
	_, err = store.ValidateSession(sst, "10.0.0.99:12345")
	if err != ErrIPMismatch {
		t.Fatalf("Expected ErrIPMismatch, got: %v", err)
	}

	// Validate with non-existent token
	_, err = store.ValidateSession("sst_nonexistenttoken", "192.168.1.50:5000")
	if err != ErrSessionNotFound {
		t.Fatalf("Expected ErrSessionNotFound, got: %v", err)
	}
}

func TestSessionStore_IdleTimeout(t *testing.T) {
	// 50ms idle TTL
	store := NewStore(50*time.Millisecond, 1*time.Second)
	defer store.Close()

	sst, _ := GenerateSST()
	_, err := store.CreateSession(sst, "bob@gmail.com", "bob-1", "127.0.0.1:1111", "", "fid-1", time.Now().Add(1*time.Hour))
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	// Sleep 70ms to trigger idle timeout
	time.Sleep(70 * time.Millisecond)

	_, err = store.ValidateSession(sst, "127.0.0.1:1111")
	if err != ErrSessionExpired {
		t.Fatalf("Expected ErrSessionExpired, got: %v", err)
	}
}

func TestSessionStore_AbsoluteExpiryCeiling(t *testing.T) {
	// Long idle TTL (1 hour), but short absolute expiry (50ms)
	store := NewStore(1*time.Hour, 1*time.Second)
	defer store.Close()

	sst, _ := GenerateSST()
	absExp := time.Now().Add(50 * time.Millisecond)
	_, err := store.CreateSession(sst, "carol@gmail.com", "carol-1", "127.0.0.1:2222", "", "fid-1", absExp)
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	// Actively touch session
	time.Sleep(20 * time.Millisecond)
	_, err = store.ValidateSession(sst, "127.0.0.1:2222")
	if err != nil {
		t.Fatalf("Active session should validate: %v", err)
	}

	// Wait past absolute expiry
	time.Sleep(40 * time.Millisecond)
	_, err = store.ValidateSession(sst, "127.0.0.1:2222")
	if err != ErrSessionExpired {
		t.Fatalf("Expected absolute expiration to trigger ErrSessionExpired, got: %v", err)
	}
}

func TestSessionStore_PerUserLimitAndEviction(t *testing.T) {
	store := NewStore(10*time.Minute, 1*time.Second)
	defer store.Close()

	user := "dave@gmail.com"
	tokens := make([]string, 6)

	// Create 6 sessions (limit is 5)
	for i := 0; i < 6; i++ {
		sst, _ := GenerateSST()
		tokens[i] = sst
		_, err := store.CreateSession(sst, user, "", "127.0.0.1:3333", "", "fid-1", time.Now().Add(1*time.Hour))
		if err != nil {
			t.Fatalf("CreateSession %d failed: %v", i, err)
		}
	}

	// Oldest token (tokens[0]) should have been evicted
	_, err := store.ValidateSession(tokens[0], "127.0.0.1:3333")
	if err != ErrSessionNotFound {
		t.Fatalf("Oldest session should be evicted, got: %v", err)
	}

	// Newest 5 tokens should still be valid
	for i := 1; i < 6; i++ {
		_, err := store.ValidateSession(tokens[i], "127.0.0.1:3333")
		if err != nil {
			t.Fatalf("Token %d should be valid: %v", i, err)
		}
	}
}

func TestSessionStore_Revocation(t *testing.T) {
	store := NewStore(10*time.Minute, 1*time.Second)
	defer store.Close()

	sst, _ := GenerateSST()
	clientID := "client-xyz"
	_, err := store.CreateSession(sst, "eve@gmail.com", clientID, "127.0.0.1:4444", "", "fid-1", time.Now().Add(1*time.Hour))
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	// Revoke by Client ID
	err = store.RevokeByClientID(clientID)
	if err != nil {
		t.Fatalf("RevokeByClientID failed: %v", err)
	}

	_, err = store.ValidateSession(sst, "127.0.0.1:4444")
	if err != ErrSessionNotFound {
		t.Fatalf("Expected ErrSessionNotFound after revocation, got: %v", err)
	}
}
