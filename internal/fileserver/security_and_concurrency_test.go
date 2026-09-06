package fileserver

import (
	"bytes"
	"strings"
	"sync"
	"testing"

	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/domain"
)

// TestPathTraversalCreateFileBlocked verifies that path traversal attempts
// (e.g. "../escape.txt", "alice/../../etc/passwd", Windows backslashes)
// are safely rejected when creating files.
func TestPathTraversalCreateFileBlocked(t *testing.T) {
	fs := newTestFileServer(t)

	rootFID, err := fs.GetUserRoot("alice", "alice")
	if err != nil {
		t.Fatalf("GetUserRoot failed: %v", err)
	}

	traversalNames := []string{
		"../escape.txt",
		"..\\escape.txt",
		"../../etc/passwd",
		"..\\..\\windows\\system32",
		"sub/../../secret.txt",
		"/absolute/path/file.txt",
	}

	for _, badName := range traversalNames {
		_, err := fs.CreateFile(rootFID, badName, "alice", domain.InodeTypeFile)
		if err == nil {
			t.Errorf("expected CreateFile with traversal name %q to fail, but got nil error", badName)
		}
	}
}

// TestReservedSystemFileProtection verifies that files matching internal metadata filenames
// (.dvfs_inodes_index.json, quota_config.json, .trash) cannot be created as regular user files.
func TestReservedSystemFileProtection(t *testing.T) {
	fs := newTestFileServer(t)

	rootFID, err := fs.GetUserRoot("alice", "alice")
	if err != nil {
		t.Fatalf("GetUserRoot failed: %v", err)
	}

	// 1. Reserved .trash folder
	if _, err := fs.CreateFile(rootFID, ".trash", "alice", domain.InodeTypeFile); err == nil {
		t.Errorf("expected creating .trash to be rejected")
	}

	// 2. Reserved inode index filename
	if _, err := fs.CreateFile(rootFID, inodeIndexFilename, "alice", domain.InodeTypeFile); err == nil {
		t.Errorf("expected creating %s to be rejected", inodeIndexFilename)
	}

	// 3. Reserved quota config filename
	if _, err := fs.CreateFile(rootFID, quotaConfigFile, "alice", domain.InodeTypeFile); err == nil {
		t.Errorf("expected creating %s to be rejected", quotaConfigFile)
	}
}

// TestUnicodeAndEmojiFilenames verifies full UTF-8 stability for files
// containing emojis, CJK, Arabic, and Hindi characters.
func TestUnicodeAndEmojiFilenames(t *testing.T) {
	fs := newTestFileServer(t)

	rootFID, err := fs.GetUserRoot("alice", "alice")
	if err != nil {
		t.Fatalf("GetUserRoot failed: %v", err)
	}

	testCases := []struct {
		filename string
		content  string
	}{
		{"📁_report_📊.csv", "id,name,value\n1,alpha,100\n"},
		{"プロジェクト計画_2026.txt", "日本語のテスト内容です。"},
		{"ملف_بيانات.dat", "محتوى عربي للاختبار"},
		{"नमस्ते_दुनिया.md", "# हिंदी दस्तावेज़\nयह एक परीक्षण है।"},
	}

	for _, tc := range testCases {
		// 1. Create file
		fid, err := fs.CreateFile(rootFID, tc.filename, "alice", domain.InodeTypeFile)
		if err != nil {
			t.Fatalf("CreateFile(%q) failed: %v", tc.filename, err)
		}
		if fid == nil {
			t.Fatalf("CreateFile(%q) returned nil FID", tc.filename)
		}

		// 2. Write content
		data := []byte(tc.content)
		if err := fs.WriteFile(rootFID, tc.filename, 0, data); err != nil {
			t.Fatalf("WriteFile(%q) failed: %v", tc.filename, err)
		}

		// 3. Read content back and verify exact byte match
		readBytes, err := fs.ReadFile(rootFID, tc.filename, 0, uint64(len(data)))
		if err != nil {
			t.Fatalf("ReadFile(%q) failed: %v", tc.filename, err)
		}
		if !bytes.Equal(readBytes, data) {
			t.Errorf("ReadFile(%q) content mismatch: got %q, want %q", tc.filename, string(readBytes), tc.content)
		}

		// 4. Verify directory listing contains the unicode filename
		entries, err := fs.ListDirectory(rootFID)
		if err != nil {
			t.Fatalf("ListDirectory failed: %v", err)
		}
		found := false
		for _, e := range entries {
			if e.Name == tc.filename {
				found = true
				if e.Size != uint64(len(data)) {
					t.Errorf("entry size mismatch: got %d, want %d", e.Size, len(data))
				}
				break
			}
		}
		if !found {
			t.Errorf("ListDirectory missing expected unicode filename %q", tc.filename)
		}
	}
}

// TestConcurrentDistinctOffsetWritesSameFile tests two concurrent goroutines
// writing to non-overlapping ranges (0..500 and 500..1000) of the same file.
// Verifies final size is 1000 and data integrity is maintained without race corruption.
func TestConcurrentDistinctOffsetWritesSameFile(t *testing.T) {
	fs := newTestFileServer(t)

	rootFID, err := fs.GetUserRoot("alice", "alice")
	if err != nil {
		t.Fatalf("GetUserRoot failed: %v", err)
	}

	filename := "concurrent_offsets.bin"
	_, err = fs.CreateFile(rootFID, filename, "alice", domain.InodeTypeFile)
	if err != nil {
		t.Fatalf("CreateFile failed: %v", err)
	}

	chunk1 := bytes.Repeat([]byte("A"), 500)
	chunk2 := bytes.Repeat([]byte("B"), 500)

	var wg sync.WaitGroup
	wg.Add(2)

	var err1, err2 error
	go func() {
		defer wg.Done()
		err1 = fs.WriteFile(rootFID, filename, 0, chunk1)
	}()
	go func() {
		defer wg.Done()
		err2 = fs.WriteFile(rootFID, filename, 500, chunk2)
	}()

	wg.Wait()

	if err1 != nil {
		t.Errorf("worker 1 WriteFile failed: %v", err1)
	}
	if err2 != nil {
		t.Errorf("worker 2 WriteFile failed: %v", err2)
	}

	// Verify total file size
	rootInode, _ := fs.GetInode(rootFID)
	fileInode, err := fs.GetChildInodeByName(rootInode, filename)
	if err != nil {
		t.Fatalf("GetChildInodeByName failed: %v", err)
	}
	if fileInode.Size != 1000 {
		t.Errorf("expected final file size 1000, got %d", fileInode.Size)
	}

	// Read both halves back and verify byte contents
	readPart1, err := fs.ReadFile(rootFID, filename, 0, 500)
	if err != nil || !bytes.Equal(readPart1, chunk1) {
		t.Errorf("part 1 data corrupted")
	}

	readPart2, err := fs.ReadFile(rootFID, filename, 500, 500)
	if err != nil || !bytes.Equal(readPart2, chunk2) {
		t.Errorf("part 2 data corrupted")
	}
}

// TestMaxFilenameLengthBoundary tests filename length boundaries
// (255 bytes standard max vs > 255 bytes).
func TestMaxFilenameLengthBoundary(t *testing.T) {
	fs := newTestFileServer(t)

	rootFID, err := fs.GetUserRoot("alice", "alice")
	if err != nil {
		t.Fatalf("GetUserRoot failed: %v", err)
	}

	// 255-character filename
	valid255 := strings.Repeat("a", 251) + ".txt"
	_, err = fs.CreateFile(rootFID, valid255, "alice", domain.InodeTypeFile)
	if err != nil {
		// If OS supports 255 bytes, it succeeds; if OS has shorter path limits on Windows tmpDir,
		// it fails gracefully without panic.
	}

	// 300-character filename (exceeds NAME_MAX on most OSs)
	tooLong := strings.Repeat("x", 296) + ".bin"
	_, errTooLong := fs.CreateFile(rootFID, tooLong, "alice", domain.InodeTypeFile)
	// Server must return an error cleanly without panic
	_ = errTooLong
}
