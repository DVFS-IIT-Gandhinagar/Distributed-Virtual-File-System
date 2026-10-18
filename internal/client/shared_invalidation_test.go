package client

import (
	"os"
	"path/filepath"
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

// setupSharedPair connects alice (owner) and bob (shared user, with a cache
// handler) to alice's root. expect runs action and waits until bob receives a
// new notification containing want.
func setupSharedPair(t *testing.T) (alice, bob *Client, bobCache *CacheHandler, expect func(what, want string, action func() error)) {
	t.Helper()
	fsAddr, _, cleanupFS := startTestFileServerGRPC(t, "")
	t.Cleanup(cleanupFS)

	alice = connectTestClient(t, "alice", "alice", fsAddr)
	t.Cleanup(alice.Disconnect)
	if err := alice.Share("bob"); err != nil {
		t.Fatalf("Share failed: %v", err)
	}

	bob = connectTestClient(t, "bob", "alice", fsAddr)
	t.Cleanup(bob.Disconnect)
	rec := &notifyRecorder{ch: make(chan struct{}, 64)}
	bob.SetNotifyWriter(rec)
	bobCache = NewCacheHandler(bob, bob.rootFID)
	if bobCache == nil {
		t.Fatalf("NewCacheHandler returned nil")
	}
	bob.AttachCacheHandler(bobCache)

	expect = func(what, want string, action func() error) {
		t.Helper()
		// Let callbacks from earlier steps land so they are not mistaken for this one.
		time.Sleep(100 * time.Millisecond)
		rec.mu.Lock()
		mark := len(rec.msgs)
		rec.mu.Unlock()

		if err := action(); err != nil {
			t.Fatalf("%s failed: %v", what, err)
		}
		deadline := time.After(5 * time.Second)
		for {
			rec.mu.Lock()
			for _, m := range rec.msgs[mark:] {
				if strings.Contains(m, want) {
					rec.mu.Unlock()
					return
				}
			}
			rec.mu.Unlock()
			select {
			case <-rec.ch:
			case <-deadline:
				t.Fatalf("bob never received a %q notification for %s", want, what)
			}
		}
	}
	return alice, bob, bobCache, expect
}

func TestSharedUserNotifiedOnMkdirTrashRestore(t *testing.T) {
	defer withTempWorkingDir(t)()
	alice, _, bobCache, expect := setupSharedPair(t)

	if files, _ := bobCache.ListFiles(); hasFile(files, "share_test") {
		t.Fatalf("share_test should not exist yet")
	}

	expect("mkdir", "added", func() error { _, err := alice.CreateDirectory("share_test"); return err })
	if files, _ := bobCache.ListFiles(); !hasFile(files, "share_test") {
		t.Fatalf("bob does not see share_test after mkdir: %+v", files)
	}

	expect("trash", "deleted", func() error { _, err := alice.TrashFile("share_test", true); return err })
	if files, _ := bobCache.ListFiles(); hasFile(files, "share_test") {
		t.Fatalf("bob still sees share_test after trash: %+v", files)
	}

	expect("restore", "added", func() error {
		trash, err := alice.ShowTrash()
		if err != nil || len(trash) == 0 {
			t.Fatalf("alice ShowTrash failed: err=%v entries=%d", err, len(trash))
		}
		_, err = alice.RestoreFile(trash[0].Name)
		return err
	})
	if files, _ := bobCache.ListFiles(); !hasFile(files, "share_test") {
		t.Fatalf("bob does not see share_test after restore: %+v", files)
	}
}

func TestSharedUserNotifiedOnUploadFileAndFolder(t *testing.T) {
	defer withTempWorkingDir(t)()
	alice, _, bobCache, expect := setupSharedPair(t)

	local := t.TempDir()
	filePath := filepath.Join(local, "notes.txt")
	if err := os.WriteFile(filePath, []byte("hello from alice"), 0o644); err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(local, "photos")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "a.jpg"), []byte("jpg"), 0o644); err != nil {
		t.Fatal(err)
	}

	expect("file upload", "added", func() error { _, err := alice.Upload(filePath); return err })
	if files, _ := bobCache.ListFiles(); !hasFile(files, "notes.txt") {
		t.Fatalf("bob does not see notes.txt after upload: %+v", files)
	}

	expect("folder upload", "added", func() error { _, err := alice.Upload(folder); return err })
	if files, _ := bobCache.ListFiles(); !hasFile(files, "photos") {
		t.Fatalf("bob does not see photos/ after folder upload: %+v", files)
	}
}
