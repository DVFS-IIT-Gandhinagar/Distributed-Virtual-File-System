package client

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cbpb "github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/api/callback"
	fspb "github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/api/fileserver"
	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/domain"
)

// =============================================================================
// 1. Path & Validation Unit Tests (Pure functions, Edge Cases)
// =============================================================================

func TestRobustness_PathContainsTrashSegment_EdgeCases(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		expected bool
	}{
		{"exact match", ".trash", true},
		{"nested under root", "docs/.trash", true},
		{"nested subfolder", "docs/.trash/sub", true},
		{"leading slash", "/.trash", true},
		{"trailing slash", ".trash/", true},
		{"windows backslash", "docs\\.trash\\sub", true},
		{"normal folder named trash without dot", "trash", false},
		{"dot in filename but not segment", "my.trash.txt", false},
		{"empty string", "", false},
		{"normal nested path", "docs/work/file.txt", false},
		{"root only", "/", false},
		{"deeply nested trash", "a/b/c/d/.trash/e/f", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			actual := pathContainsTrashSegment(tc.input)
			if actual != tc.expected {
				t.Fatalf("pathContainsTrashSegment(%q) = %v; want %v", tc.input, actual, tc.expected)
			}
		})
	}
}

// =============================================================================
// 2. Client Notification Hook Tests
// =============================================================================

func TestRobustness_ClientNotifyWriter(t *testing.T) {
	c := NewClient("alice", false, "")

	// 1. Notify without notifyWriter set (should not panic)
	c.Notify("test message %d", 42)

	// 2. Notify with custom buffer
	var buf bytes.Buffer
	c.SetNotifyWriter(&buf)
	c.Notify("hello %s, code %d", "world", 200)

	expected := "hello world, code 200"
	if buf.String() != expected {
		t.Fatalf("Notify output = %q; want %q", buf.String(), expected)
	}
}

// =============================================================================
// 3. Callback Server Invalidate Edge & Handling Tests
// =============================================================================

func TestRobustness_CallbackServerInvalidate_NilRequest(t *testing.T) {
	s := &callbackServer{client: nil}

	// Nil request
	resp, err := s.Invalidate(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil || resp.Success {
		t.Fatalf("expected Success=false for nil request, got %+v", resp)
	}

	// Request with nil FID
	resp, err = s.Invalidate(context.Background(), &cbpb.InvalidateRequest{Fid: nil})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil || resp.Success {
		t.Fatalf("expected Success=false for request with nil FID, got %+v", resp)
	}
}

func TestRobustness_CallbackServerInvalidate_EventTypesAndNotification(t *testing.T) {
	cleanupWD := withTempWorkingDir(t)
	defer cleanupWD()

	h, c, cleanup := setupCacheHandlerTest(t)
	defer cleanup()

	var notifyBuf bytes.Buffer
	c.SetNotifyWriter(&notifyBuf)
	c.AttachCacheHandler(h)

	s := &callbackServer{client: c}

	fidProto := &fspb.FID{
		FileServerId:     "fs-test",
		InodeId:          100,
		GenerationNumber: 1,
	}

	// Test EventType = DirNewFile (2)
	notifyBuf.Reset()
	resp, err := s.Invalidate(context.Background(), &cbpb.InvalidateRequest{
		Fid:        fidProto,
		NewVersion: callbackEventDirNewFile,
	})
	if err != nil || resp == nil || !resp.Success {
		t.Fatalf("Invalidate DirNewFile failed: resp=%+v, err=%v", resp, err)
	}
	if !strings.Contains(notifyBuf.String(), "[NOTIFY]") || !strings.Contains(notifyBuf.String(), "New file uploaded") {
		t.Fatalf("unexpected notify output: %s", notifyBuf.String())
	}

	// Test EventType = FileDeleted (3)
	notifyBuf.Reset()
	resp, err = s.Invalidate(context.Background(), &cbpb.InvalidateRequest{
		Fid:        fidProto,
		NewVersion: callbackEventFileDeleted,
	})
	if err != nil || resp == nil || !resp.Success {
		t.Fatalf("Invalidate FileDeleted failed: resp=%+v, err=%v", resp, err)
	}
	if !strings.Contains(notifyBuf.String(), "[NOTIFY]") || !strings.Contains(notifyBuf.String(), "deleted") {
		t.Fatalf("unexpected notify output: %s", notifyBuf.String())
	}

	// Test EventType = FileUpdated (1 or 0)
	notifyBuf.Reset()
	resp, err = s.Invalidate(context.Background(), &cbpb.InvalidateRequest{
		Fid:        fidProto,
		NewVersion: callbackEventFileUpdated,
	})
	if err != nil || resp == nil || !resp.Success {
		t.Fatalf("Invalidate FileUpdated failed: resp=%+v, err=%v", resp, err)
	}
	if !strings.Contains(notifyBuf.String(), "[NOTIFY]") || !strings.Contains(notifyBuf.String(), "File updated") {
		t.Fatalf("unexpected notify output: %s", notifyBuf.String())
	}

	// Test EventType = 0 (defaults to FileUpdated)
	notifyBuf.Reset()
	resp, err = s.Invalidate(context.Background(), &cbpb.InvalidateRequest{
		Fid:        fidProto,
		NewVersion: 0,
	})
	if err != nil || resp == nil || !resp.Success {
		t.Fatalf("Invalidate NewVersion=0 failed: resp=%+v, err=%v", resp, err)
	}
	if !strings.Contains(notifyBuf.String(), "[NOTIFY]") || !strings.Contains(notifyBuf.String(), "File updated") {
		t.Fatalf("unexpected notify output: %s", notifyBuf.String())
	}
}

func TestRobustness_CallbackServerInvalidate_NilCacheHandler(t *testing.T) {
	c := NewClient("bob", false, "")
	var notifyBuf bytes.Buffer
	c.SetNotifyWriter(&notifyBuf)
	// client without cache handler
	s := &callbackServer{client: c}

	fidProto := &fspb.FID{
		FileServerId:     "fs-test",
		InodeId:          1,
		GenerationNumber: 1,
	}

	// All 3 event types must gracefully handle nil cacheHandler without panic
	eventTypes := []uint64{callbackEventDirNewFile, callbackEventFileDeleted, callbackEventFileUpdated}
	for _, ev := range eventTypes {
		notifyBuf.Reset()
		resp, err := s.Invalidate(context.Background(), &cbpb.InvalidateRequest{
			Fid:        fidProto,
			NewVersion: ev,
		})
		if err != nil || resp == nil || !resp.Success {
			t.Fatalf("Invalidate event %d failed with nil cacheHandler: resp=%+v, err=%v", ev, resp, err)
		}
		if notifyBuf.Len() == 0 {
			t.Fatalf("expected notification message for event %d", ev)
		}
	}
}

// =============================================================================
// 4. Cache Visualization & Inspection Tests
// =============================================================================

func TestRobustness_CacheHandlerVisualizeCache(t *testing.T) {
	cleanupWD := withTempWorkingDir(t)
	defer cleanupWD()

	h, _, cleanup := setupCacheHandlerTest(t)
	defer cleanup()

	// Populate additional cached and uncached nodes
	h.root.children["cached_file.txt"] = &CNode{
		Name:          "cached_file.txt",
		Type:          domain.InodeTypeFile,
		fid:           &domain.FID{FileServerID: "fs-test", InodeID: 99, GenerationNumber: 1},
		Size:          1024,
		contentCached: true,
		contentUID:    "uid-123",
		parent:        h.root,
	}
	h.root.children["subfolder"] = &CNode{
		Name:     "subfolder",
		Type:     domain.InodeTypeDirectory,
		fid:      &domain.FID{FileServerID: "fs-test", InodeID: 100, GenerationNumber: 1},
		children: make(map[string]*CNode),
		parent:   h.root,
	}

	// Call VisualizeCache (should execute without error or panic)
	h.VisualizeCache("")

	// Also verify GetFileInfo returns root info
	info, err := h.GetFileInfo()
	if err != nil {
		t.Fatalf("GetFileInfo failed: %v", err)
	}
	if info == nil || info.Name != "alice" {
		t.Fatalf("expected root name 'alice', got %+v", info)
	}
}

// =============================================================================
// 5. Recursive Directory Upload & Download Tests
// =============================================================================

func TestRobustness_ClientUploadAndDownloadRecursiveDirectory(t *testing.T) {
	fsAddr, _, cleanupFS := startTestFileServerGRPC(t, "")
	defer cleanupFS()

	c := connectTestClient(t, "alice", "alice", fsAddr)

	// Create local nested directory tree
	srcDir := filepath.Join(t.TempDir(), "project_src")
	if err := os.MkdirAll(filepath.Join(srcDir, "pkg", "sub"), 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "README.md"), []byte("# Project Root"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "pkg", "lib.go"), []byte("package pkg"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "pkg", "sub", "util.go"), []byte("package sub"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	// Upload recursive directory tree to fileserver
	uploadedFID, err := c.Upload(srcDir)
	if err != nil {
		t.Fatalf("Upload directory failed: %v", err)
	}
	if uploadedFID == nil {
		t.Fatalf("expected non-nil FID for uploaded directory")
	}

	// Verify the uploaded folder exists on fileserver
	files, err := c.ListFiles()
	if err != nil {
		t.Fatalf("ListFiles failed: %v", err)
	}
	foundDir := false
	for _, f := range files {
		if f.Name == "project_src" && f.Type == domain.InodeTypeDirectory {
			foundDir = true
			break
		}
	}
	if !foundDir {
		t.Fatalf("expected 'project_src' directory in fileserver root listing")
	}

	// Download the recursive directory tree to a new local directory
	dstParentDir := t.TempDir()
	origWD, _ := os.Getwd()
	if err := os.Chdir(dstParentDir); err != nil {
		t.Fatalf("Chdir to dstParentDir failed: %v", err)
	}
	defer func() { _ = os.Chdir(origWD) }()

	if err := c.Download("project_src"); err != nil {
		t.Fatalf("Download directory failed: %v", err)
	}

	// Verify all files and contents downloaded properly under Download/project_src
	downloadedReadme := filepath.Join(dstParentDir, "Download", "project_src", "README.md")
	content, err := os.ReadFile(downloadedReadme)
	if err != nil {
		t.Fatalf("failed to read downloaded README.md: %v", err)
	}
	if string(content) != "# Project Root" {
		t.Fatalf("downloaded content mismatch: %q", string(content))
	}

	downloadedUtil := filepath.Join(dstParentDir, "Download", "project_src", "pkg", "sub", "util.go")
	contentUtil, err := os.ReadFile(downloadedUtil)
	if err != nil {
		t.Fatalf("failed to read downloaded util.go: %v", err)
	}
	if string(contentUtil) != "package sub" {
		t.Fatalf("downloaded util.go content mismatch: %q", string(contentUtil))
	}
}

// =============================================================================
// 6. CacheHandler Directory Upload Tests
// =============================================================================

func TestRobustness_CacheHandlerUploadDirectory(t *testing.T) {
	cleanupWD := withTempWorkingDir(t)
	defer cleanupWD()

	h, _, cleanup := setupCacheHandlerTest(t)
	defer cleanup()

	// Create local folder with a file
	localDir := filepath.Join(t.TempDir(), "local_folder")
	if err := os.MkdirAll(localDir, 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(localDir, "data.csv"), []byte("a,b,c"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	// Upload directory through CacheHandler
	if err := h.Upload(localDir); err != nil {
		t.Fatalf("CacheHandler.Upload directory failed: %v", err)
	}

	// Verify cache contains the uploaded folder
	child, exists := h.root.children["local_folder"]
	if !exists || child.Type != domain.InodeTypeDirectory {
		t.Fatalf("expected 'local_folder' directory node in cache, got exists=%v", exists)
	}
}

// =============================================================================
// 7. Client Disconnect and DownloadFile Helper Tests
// =============================================================================

func TestRobustness_ClientDisconnectAndDownloadFile(t *testing.T) {
	fsAddr, _, cleanupFS := startTestFileServerGRPC(t, "")
	defer cleanupFS()

	c := connectTestClient(t, "alice", "alice", fsAddr)

	// Create a test file
	if _, err := c.CreateFile("report.txt"); err != nil {
		t.Fatalf("CreateFile failed: %v", err)
	}
	if err := c.WriteFile("report.txt", []byte("annual report data")); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	tempWD := t.TempDir()
	origWD, _ := os.Getwd()
	if err := os.Chdir(tempWD); err != nil {
		t.Fatalf("Chdir failed: %v", err)
	}
	defer func() { _ = os.Chdir(origWD) }()

	// Test DownloadFile wrapper
	if err := c.DownloadFile("report.txt"); err != nil {
		t.Fatalf("DownloadFile failed: %v", err)
	}

	downloaded := filepath.Join(tempWD, "Download", "report.txt")
	content, err := os.ReadFile(downloaded)
	if err != nil {
		t.Fatalf("failed to read downloaded report: %v", err)
	}
	if string(content) != "annual report data" {
		t.Fatalf("unexpected content: %q", string(content))
	}

	// Test Disconnect gracefully closes connection and stops callback server
	c.Disconnect()
	if c.grpcConn != nil {
		t.Fatalf("expected grpcConn to be nil after Disconnect")
	}
}

// =============================================================================
// 8. Cobra Command Execution Suite
// =============================================================================

func TestRobustness_CobraHandlerCommandExecution(t *testing.T) {
	cleanupWD := withTempWorkingDir(t)
	defer cleanupWD()

	h, _, cleanup := setupCacheHandlerTest(t)
	defer cleanup()

	ch := NewCobraHandler(h)
	if ch == nil || ch.rootCmd == nil {
		t.Fatalf("NewCobraHandler returned nil or missing rootCmd")
	}

	// Helper to execute commands on the rootCmd
	executeCmd := func(args ...string) error {
		ch.rootCmd.SetArgs(args)
		return ch.rootCmd.Execute()
	}

	// 1. pwd
	if err := executeCmd("pwd"); err != nil {
		t.Fatalf("cmd 'pwd' failed: %v", err)
	}

	// 2. ls
	if err := executeCmd("ls"); err != nil {
		t.Fatalf("cmd 'ls' failed: %v", err)
	}

	// 3. mkdir
	if err := executeCmd("mkdir", "newdir"); err != nil {
		t.Fatalf("cmd 'mkdir' failed: %v", err)
	}

	// 4. cd into newdir
	if err := executeCmd("cd", "newdir"); err != nil {
		t.Fatalf("cmd 'cd' failed: %v", err)
	}

	// 5. create file in newdir
	if err := executeCmd("create", "nested.txt"); err != nil {
		t.Fatalf("cmd 'create' failed: %v", err)
	}

	// 6. info in newdir
	if err := executeCmd("info"); err != nil {
		t.Fatalf("cmd 'info' failed: %v", err)
	}

	// 7. cd back to root
	if err := executeCmd("cd", ".."); err != nil {
		t.Fatalf("cmd 'cd ..' failed: %v", err)
	}

	// 8. refresh
	if err := executeCmd("refresh"); err != nil {
		t.Fatalf("cmd 'refresh' failed: %v", err)
	}

	// 9. viscache
	if err := executeCmd("viscache"); err != nil {
		t.Fatalf("cmd 'viscache' failed: %v", err)
	}

	// 10. clear
	if err := executeCmd("clear"); err != nil {
		t.Fatalf("cmd 'clear' failed: %v", err)
	}

	// 11. show_trash (empty)
	if err := executeCmd("show_trash"); err != nil {
		t.Fatalf("cmd 'show_trash' failed: %v", err)
	}

	// 12. trash file
	if err := executeCmd("trash", "notes.txt"); err != nil {
		t.Fatalf("cmd 'trash notes.txt' failed: %v", err)
	}

	// 13. show_trash (contains notes.txt)
	if err := executeCmd("show_trash"); err != nil {
		t.Fatalf("cmd 'show_trash' failed: %v", err)
	}

	// 14. restore file
	if err := executeCmd("restore", "notes.txt"); err != nil {
		t.Fatalf("cmd 'restore notes.txt' failed: %v", err)
	}

	// 15. delete file
	if err := executeCmd("delete", "notes.txt"); err != nil {
		t.Fatalf("cmd 'delete notes.txt' failed: %v", err)
	}

	// 16. clear_trash
	if err := executeCmd("clear_trash"); err != nil {
		t.Fatalf("cmd 'clear_trash' failed: %v", err)
	}
}

// =============================================================================
// 9. Metaserver Client Edge Cases (Empty Address, TLS Invalidation)
// =============================================================================

func TestRobustness_MSClientEdgeCases(t *testing.T) {
	c := NewClient("alice", false, "")

	// Empty msAddr
	roots, err := c.GetRoots("")
	if err != nil || len(roots) != 0 {
		t.Fatalf("expected empty roots and nil err for empty msAddr, got roots=%v, err=%v", roots, err)
	}

	addr, err := c.NavigateToFileServer("")
	if err != nil || addr != "" {
		t.Fatalf("expected empty addr and nil err for empty msAddr, got addr=%q, err=%v", addr, err)
	}

	// Non-existent CA cert path with useTLS=true
	cTLS := NewClient("alice", true, filepath.Join(t.TempDir(), "nonexistent.crt"))
	_, err = cTLS.GetRoots("127.0.0.1:50051")
	if err == nil {
		t.Fatalf("expected error dialing with non-existent CA cert, got nil")
	}

	_, err = cTLS.NavigateToFileServer("127.0.0.1:50051")
	if err == nil {
		t.Fatalf("expected error navigating with non-existent CA cert, got nil")
	}
}

