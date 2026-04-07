package client

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/umangshikarvar/dvfs/internal/domain"
)

func setupCacheHandlerTest(t *testing.T) (*CacheHandler, *Client, func()) {
	t.Helper()

	fsAddr, _, cleanupFS := startTestFileServerGRPC(t, "")
	c := connectTestClient(t, "alice", "alice", fsAddr)

	if _, err := c.CreateDirectory("docs"); err != nil {
		cleanupFS()
		t.Fatalf("CreateDirectory docs failed: %v", err)
	}
	if _, err := c.CreateFile("notes.txt"); err != nil {
		cleanupFS()
		t.Fatalf("CreateFile notes.txt failed: %v", err)
	}
	if err := c.WriteFile("notes.txt", []byte("original payload")); err != nil {
		cleanupFS()
		t.Fatalf("WriteFile notes.txt failed: %v", err)
	}

	h := NewCacheHandler(c, c.rootFID)
	if h == nil {
		cleanupFS()
		t.Fatalf("NewCacheHandler returned nil")
	}

	cleanup := func() {
		h.ClearCache()
		cleanupFS()
	}

	return h, c, cleanup
}

func withTempWorkingDir(t *testing.T) func() {
	t.Helper()

	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	tempWD := t.TempDir()
	if err := os.Chdir(tempWD); err != nil {
		t.Fatalf("Chdir failed: %v", err)
	}
	if err := os.MkdirAll(CacheDir, 0755); err != nil {
		t.Fatalf("failed to create cache dir: %v", err)
	}

	return func() {
		_ = os.Chdir(originalWD)
	}
}

func TestNewCacheHandlerPopulatesRootChildren(t *testing.T) {
	h, _, cleanup := setupCacheHandlerTest(t)
	defer cleanup()

	if h.root == nil || h.curr == nil {
		t.Fatalf("expected root and current nodes to be initialized")
	}
	if h.root.Name != "mydrive" {
		t.Fatalf("unexpected root name: got=%q", h.root.Name)
	}

	if _, ok := h.root.children["docs"]; !ok {
		t.Fatalf("expected docs directory in cache root children")
	}
	if _, ok := h.root.children["notes.txt"]; !ok {
		t.Fatalf("expected notes.txt in cache root children")
	}
}

func TestCacheHandlerReadFileMissThenHit(t *testing.T) {
	restoreWD := withTempWorkingDir(t)
	defer restoreWD()

	h, c, cleanup := setupCacheHandlerTest(t)
	defer cleanup()

	firstRead, err := h.ReadFile("notes.txt")
	if err != nil {
		t.Fatalf("ReadFile miss failed: %v", err)
	}
	if got, want := string(firstRead), "original payload"; got != want {
		t.Fatalf("first read mismatch: got=%q want=%q", got, want)
	}

	node := h.curr.children["notes.txt"]
	if node == nil || !node.contentCached || node.contentUID == "" {
		t.Fatalf("expected notes.txt node to be cached with contentUID")
	}

	if err := c.WriteFile("notes.txt", []byte("new remote payload")); err != nil {
		t.Fatalf("WriteFile to mutate remote content failed: %v", err)
	}

	secondRead, err := h.ReadFile("notes.txt")
	if err != nil {
		t.Fatalf("ReadFile hit failed: %v", err)
	}
	if got, want := string(secondRead), "original payload"; got != want {
		t.Fatalf("expected cached read to return original cached content: got=%q want=%q", got, want)
	}
}

func TestCacheHandlerNavigationAndPath(t *testing.T) {
	h, _, cleanup := setupCacheHandlerTest(t)
	defer cleanup()

	if err := h.ChangeDirectory("docs"); err != nil {
		t.Fatalf("ChangeDirectory docs failed: %v", err)
	}
	if h.curr.Name != "docs" {
		t.Fatalf("unexpected current dir after cd docs: got=%q", h.curr.Name)
	}

	path, err := h.Path()
	if err != nil {
		t.Fatalf("Path failed: %v", err)
	}
	if !strings.Contains(path, "/docs") {
		t.Fatalf("unexpected cache path: %q", path)
	}

	if err := h.ChangeDirectory(".."); err != nil {
		t.Fatalf("ChangeDirectory .. failed: %v", err)
	}
	if h.curr != h.root {
		t.Fatalf("expected to return to root after cd ..")
	}

	if err := h.ChangeDirectory("/"); err != nil {
		t.Fatalf("ChangeDirectory / failed: %v", err)
	}

	if err := h.ChangeDirectory("missing"); err == nil {
		t.Fatalf("expected cd into missing directory to fail")
	}
}

func TestCacheHandlerUploadTrashRestoreDeleteFlow(t *testing.T) {
	restoreWD := withTempWorkingDir(t)
	defer restoreWD()

	h, _, cleanup := setupCacheHandlerTest(t)
	defer cleanup()

	localPath := filepath.Join(".", "upload.txt")
	if err := os.WriteFile(localPath, []byte("from local disk"), 0644); err != nil {
		t.Fatalf("failed to create local upload file: %v", err)
	}

	if err := h.Upload(localPath); err != nil {
		t.Fatalf("Upload failed: %v", err)
	}

	uploaded := h.curr.children["upload.txt"]
	if uploaded == nil || uploaded.fid == nil || uploaded.Type != domain.InodeTypeFile {
		t.Fatalf("uploaded file not reflected properly in cache: %+v", uploaded)
	}

	if _, err := h.TrashFile("upload.txt", false); err != nil {
		t.Fatalf("TrashFile failed: %v", err)
	}
	if _, ok := h.curr.children["upload.txt"]; ok {
		t.Fatalf("expected upload.txt removed from current cache after trash")
	}

	if _, err := h.RestoreFile("upload.txt"); err != nil {
		t.Fatalf("RestoreFile failed: %v", err)
	}
	if _, ok := h.root.children["upload.txt"]; !ok {
		t.Fatalf("expected upload.txt to reappear in root cache after restore")
	}

	if err := h.DeleteFile("upload.txt", false); err != nil {
		t.Fatalf("DeleteFile failed: %v", err)
	}
	if _, ok := h.root.children["upload.txt"]; ok {
		t.Fatalf("expected upload.txt removed from cache after delete")
	}
}

func TestCacheHandlerClearCacheRemovesCachedFiles(t *testing.T) {
	restoreWD := withTempWorkingDir(t)
	defer restoreWD()

	h, _, cleanup := setupCacheHandlerTest(t)
	defer cleanup()

	f1 := filepath.Join(CacheDir, "a.bin")
	f2 := filepath.Join(CacheDir, "b.bin")
	_ = os.WriteFile(f1, []byte("a"), 0644)
	_ = os.WriteFile(f2, []byte("b"), 0644)

	h.ClearCache()

	entries, err := os.ReadDir(CacheDir)
	if err != nil {
		t.Fatalf("ReadDir(cache) failed: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected cache directory to be empty after ClearCache, got=%d entries", len(entries))
	}
}
