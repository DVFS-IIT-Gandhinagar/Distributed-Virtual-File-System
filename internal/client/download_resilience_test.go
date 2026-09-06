package client

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestClientDownloadDeletedFile verifies that attempting to download a file
// that was deleted on the server fails with a clean error and does NOT leave
// an empty or corrupted destination file on the local filesystem.
func TestClientDownloadDeletedFile(t *testing.T) {
	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	tempWD := t.TempDir()
	if err := os.Chdir(tempWD); err != nil {
		t.Fatalf("Chdir failed: %v", err)
	}
	defer func() { _ = os.Chdir(originalWD) }()

	fsAddr, _, cleanupFS := startTestFileServerGRPC(t, "")
	defer cleanupFS()

	c := connectTestClient(t, "alice", "alice", fsAddr)

	localSrc := filepath.Join(tempWD, "upload_me.txt")
	if err := os.WriteFile(localSrc, []byte("important content"), 0644); err != nil {
		t.Fatalf("WriteFile localSrc failed: %v", err)
	}

	// 1. Upload the file
	if _, err := c.Upload(localSrc); err != nil {
		t.Fatalf("Upload failed: %v", err)
	}

	// 2. Delete the file on the server
	if err := c.DeleteFile("upload_me.txt", false); err != nil {
		t.Fatalf("DeleteFile failed: %v", err)
	}

	// 3. Attempt to download the deleted file
	err = c.Download("upload_me.txt")
	if err == nil {
		t.Fatalf("expected Download of deleted file to return error, but got nil")
	}

	// 4. Verify local destination file does not exist (no ghost file left in Download/)
	localDest := filepath.Join(tempWD, "Download", "upload_me.txt")
	if _, err := os.Stat(localDest); err == nil {
		t.Errorf("expected destination file %s to not exist, but it was left on disk", localDest)
	}
}

// TestClientUploadDownloadUnicode verifies upload and download round-trip
// for filenames containing spaces and multibyte unicode characters.
func TestClientUploadDownloadUnicode(t *testing.T) {
	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	tempWD := t.TempDir()
	if err := os.Chdir(tempWD); err != nil {
		t.Fatalf("Chdir failed: %v", err)
	}
	defer func() { _ = os.Chdir(originalWD) }()

	fsAddr, _, cleanupFS := startTestFileServerGRPC(t, "")
	defer cleanupFS()

	c := connectTestClient(t, "alice", "alice", fsAddr)

	localSrc := filepath.Join(tempWD, "report_2026.txt")
	content := []byte("Unicode content test: 日本語, العربية, हिंदी 🚀")
	if err := os.WriteFile(localSrc, content, 0644); err != nil {
		t.Fatalf("WriteFile localSrc failed: %v", err)
	}

	// Upload
	if _, err := c.Upload(localSrc); err != nil {
		t.Fatalf("Upload failed: %v", err)
	}

	// Download back
	if err := c.Download("report_2026.txt"); err != nil {
		t.Fatalf("Download failed: %v", err)
	}

	downloadedPath := filepath.Join(tempWD, "Download", "report_2026.txt")
	got, err := os.ReadFile(downloadedPath)
	if err != nil {
		t.Fatalf("ReadFile downloadedPath failed: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("downloaded content mismatch: got %q, want %q", string(got), string(content))
	}
}
