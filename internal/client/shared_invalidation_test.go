package client

import (
	"strings"
	"sync"
	"testing"
	"time"
)

type notifyRecorder struct {
	mu   sync.Mutex
	msgs []string
	ch   chan struct{}
}

func (r *notifyRecorder) Write(p []byte) (int, error) {
	r.mu.Lock()
	r.msgs = append(r.msgs, string(p))
	r.mu.Unlock()
	select {
	case r.ch <- struct{}{}:
	default:
	}
	return len(p), nil
}

// When the owner adds a file to a shared root, the shared user's cached
// listing must be invalidated and show the file on the next ls.
func TestSharedUserCacheInvalidatedOnOwnerChange(t *testing.T) {
	defer withTempWorkingDir(t)()
	fsAddr, _, cleanupFS := startTestFileServerGRPC(t, "")
	defer cleanupFS()

	alice := connectTestClient(t, "alice", "alice", fsAddr)
	defer alice.Disconnect()
	if err := alice.Share("bob"); err != nil {
		t.Fatalf("Share failed: %v", err)
	}

	bob := connectTestClient(t, "bob", "alice", fsAddr)
	defer bob.Disconnect()
	rec := &notifyRecorder{ch: make(chan struct{}, 8)}
	bob.SetNotifyWriter(rec)
	bobCache := NewCacheHandler(bob, bob.rootFID)
	if bobCache == nil {
		t.Fatalf("NewCacheHandler returned nil")
	}
	bob.AttachCacheHandler(bobCache)

	if files, _ := bobCache.ListFiles(); hasFile(files, "report.txt") {
		t.Fatalf("report.txt should not exist yet")
	}

	if _, err := alice.CreateFile("report.txt"); err != nil {
		t.Fatalf("alice CreateFile failed: %v", err)
	}

	select {
	case <-rec.ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("bob never received an invalidation callback")
	}
	rec.mu.Lock()
	got := strings.Join(rec.msgs, "")
	rec.mu.Unlock()
	if !strings.Contains(got, "[NOTIFY]") {
		t.Fatalf("unexpected notification: %q", got)
	}

	files, err := bobCache.ListFiles()
	if err != nil {
		t.Fatalf("bob ListFiles failed: %v", err)
	}
	if !hasFile(files, "report.txt") {
		t.Fatalf("bob's cache still stale after invalidation: %+v", files)
	}
}

// Connect starts the heartbeat and Disconnect must stop it cleanly.
func TestHeartbeatStartsAndStops(t *testing.T) {
	fsAddr, _, cleanupFS := startTestFileServerGRPC(t, "")
	defer cleanupFS()

	c := connectTestClient(t, "alice", "alice", fsAddr)
	if c.heartbeatStop == nil {
		t.Fatalf("expected heartbeat to be running after Connect")
	}
	c.sendHeartbeat(c.serverConn) // must not error/panic
	c.Disconnect()
	if c.heartbeatStop != nil {
		t.Fatalf("expected heartbeat to be stopped after Disconnect")
	}
}

func hasFile(files []*FileInfo, name string) bool {
	for _, f := range files {
		if f.Name == name {
			return true
		}
	}
	return false
}
