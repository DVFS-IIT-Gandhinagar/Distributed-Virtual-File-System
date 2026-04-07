package fileserver

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/umangshikarvar/dvfs/internal/domain"
)

func TestFileScannerLoadExistingDataScansNestedTree(t *testing.T) {
	root := t.TempDir()

	userDir := filepath.Join(root, "alice")
	docsDir := filepath.Join(userDir, "docs")
	if err := os.MkdirAll(docsDir, 0755); err != nil {
		t.Fatalf("failed to create test dirs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(userDir, "a.txt"), []byte("abc"), 0644); err != nil {
		t.Fatalf("failed to write a.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(docsDir, "b.txt"), []byte("hello"), 0644); err != nil {
		t.Fatalf("failed to write b.txt: %v", err)
	}

	fs := &FileServer{rootDir: root}
	scanner := &FileScanner{rootDir: root, serverID: "fs-scan", fs: fs}

	nextID := uint64(0)
	inodes := map[string]*domain.Inode{}
	users := map[string]*domain.FID{}

	if err := scanner.loadExistingData(&nextID, &inodes, &users); err != nil {
		t.Fatalf("loadExistingData failed: %v", err)
	}

	rootFID, ok := users["alice"]
	if !ok || rootFID == nil {
		t.Fatalf("expected alice user root to be discovered")
	}

	rootInode, ok := inodes[rootFID.String()]
	if !ok {
		t.Fatalf("expected root inode for alice to exist")
	}

	if rootInode.Size != uint64(len("abc")+len("hello")) {
		t.Fatalf("unexpected root size: got=%d want=%d", rootInode.Size, len("abc")+len("hello"))
	}

	foundDocs := false
	foundBFile := false
	for _, inode := range inodes {
		if inode.Name == "docs" && inode.Type == domain.InodeTypeDirectory {
			foundDocs = true
		}
		if inode.Name == "b.txt" && inode.Type == domain.InodeTypeFile {
			foundBFile = true
		}
	}

	if !foundDocs || !foundBFile {
		t.Fatalf("expected scanner to find nested directory and file (docs=%v b.txt=%v)", foundDocs, foundBFile)
	}
}

func TestFileScannerCalculateDirectorySizesMissingChild(t *testing.T) {
	scanner := &FileScanner{}

	rootFID := &domain.FID{FileServerID: "fs", InodeID: 1, GenerationNumber: 1}
	missingChildFID := &domain.FID{FileServerID: "fs", InodeID: 2, GenerationNumber: 1}

	rootInode := &domain.Inode{
		FID:      rootFID,
		Type:     domain.InodeTypeDirectory,
		Name:     "root",
		Children: []*domain.FID{missingChildFID},
	}

	inodes := map[string]*domain.Inode{
		rootFID.String(): rootInode,
	}

	if _, err := scanner.calculateDirectorySizes(rootInode, &inodes); err == nil {
		t.Fatalf("expected calculateDirectorySizes to fail when child inode is missing")
	}
}

func TestFileScannerScanUserDirectoryBuildsParentChildLinks(t *testing.T) {
	root := t.TempDir()
	userDir := filepath.Join(root, "alice")
	childDir := filepath.Join(userDir, "child")
	if err := os.MkdirAll(childDir, 0755); err != nil {
		t.Fatalf("failed to create directories: %v", err)
	}

	fs := &FileServer{rootDir: root}
	scanner := &FileScanner{rootDir: root, serverID: "fs-scan", fs: fs}

	nextID := uint64(1)
	inodes := map[string]*domain.Inode{}
	rootFID := &domain.FID{FileServerID: "fs-scan", InodeID: 0, GenerationNumber: 1}
	rootInode := &domain.Inode{
		FID:      rootFID,
		Type:     domain.InodeTypeDirectory,
		Name:     "alice",
		OSPath:   userDir,
		ACL:      domain.ACL{Owner: "alice", Shared: []string{}},
		Children: []*domain.FID{},
	}
	inodes[rootFID.String()] = rootInode

	if err := scanner.scanUserDirectory("alice", userDir, rootInode, &nextID, &inodes); err != nil {
		t.Fatalf("scanUserDirectory failed: %v", err)
	}

	if len(rootInode.Children) == 0 {
		t.Fatalf("expected root inode to have discovered child entries")
	}

	foundChild := false
	for _, childFID := range rootInode.Children {
		inode := inodes[childFID.String()]
		if inode != nil && inode.Name == "child" && inode.Parent == rootInode {
			foundChild = true
			break
		}
	}
	if !foundChild {
		t.Fatalf("expected discovered child directory with correct parent link")
	}
}
