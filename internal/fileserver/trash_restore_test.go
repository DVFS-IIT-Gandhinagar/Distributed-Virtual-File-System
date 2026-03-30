package fileserver

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/umangshikarvar/dvfs/internal/domain"
)

func TestTrashAndRestoreFile(t *testing.T) {
	rootDir := t.TempDir()
	fs, err := NewFileServer("fs1", rootDir, false)
	if err != nil {
		t.Fatalf("NewFileServer: %v", err)
	}

	rootFID, err := fs.GetUserRoot("alice")
	if err != nil {
		t.Fatalf("GetUserRoot: %v", err)
	}

	fileFID, err := fs.CreateFile(rootFID, "hello.txt", "alice", domain.InodeTypeFile)
	if err != nil {
		t.Fatalf("CreateFile: %v", err)
	}

	inode, err := fs.GetInode(fileFID)
	if err != nil {
		t.Fatalf("GetInode: %v", err)
	}
	originalPath := inode.OSPath

	trashedName, err := fs.TrashFile(fileFID, "alice", false)
	if err != nil {
		t.Fatalf("TrashFile: %v", err)
	}
	if trashedName == "" {
		t.Fatalf("TrashFile: expected trashed name")
	}

	if _, err := os.Stat(originalPath); err == nil {
		t.Fatalf("expected original path to be gone: %s", originalPath)
	}

	inodeAfterTrash, err := fs.GetInode(fileFID)
	if err != nil {
		t.Fatalf("GetInode after trash: %v", err)
	}
	trashPath := filepath.Join(rootDir, "alice", trashDirName, trashedName)
	if inodeAfterTrash.OSPath != trashPath {
		t.Fatalf("expected trashed OSPath %s, got %s", trashPath, inodeAfterTrash.OSPath)
	}
	if _, err := os.Stat(trashPath); err != nil {
		t.Fatalf("expected trashed path to exist: %v", err)
	}

	restoredName, err := fs.RestoreFile(fileFID, "alice")
	if err != nil {
		t.Fatalf("RestoreFile: %v", err)
	}
	if restoredName == "" {
		t.Fatalf("RestoreFile: expected restored name")
	}

	inodeAfterRestore, err := fs.GetInode(fileFID)
	if err != nil {
		t.Fatalf("GetInode after restore: %v", err)
	}
	restoredPath := filepath.Join(rootDir, "alice", restoredName)
	if inodeAfterRestore.OSPath != restoredPath {
		t.Fatalf("expected restored OSPath %s, got %s", restoredPath, inodeAfterRestore.OSPath)
	}
	if _, err := os.Stat(restoredPath); err != nil {
		t.Fatalf("expected restored path to exist: %v", err)
	}
}

func TestTrashNonEmptyDirRequiresRecursive(t *testing.T) {
	rootDir := t.TempDir()
	fs, err := NewFileServer("fs1", rootDir, false)
	if err != nil {
		t.Fatalf("NewFileServer: %v", err)
	}

	rootFID, err := fs.GetUserRoot("alice")
	if err != nil {
		t.Fatalf("GetUserRoot: %v", err)
	}

	dirFID, err := fs.CreateFile(rootFID, "d", "alice", domain.InodeTypeDirectory)
	if err != nil {
		t.Fatalf("CreateFile dir: %v", err)
	}
	if _, err := fs.CreateFile(dirFID, "f", "alice", domain.InodeTypeFile); err != nil {
		t.Fatalf("CreateFile in dir: %v", err)
	}

	if _, err := fs.TrashFile(dirFID, "alice", false); err == nil {
		t.Fatalf("expected error trashing non-empty dir without recursive")
	}

	if _, err := fs.TrashFile(dirFID, "alice", true); err != nil {
		t.Fatalf("expected recursive trash to succeed, got %v", err)
	}
}

func TestCannotDeleteTrashDir(t *testing.T) {
	rootDir := t.TempDir()
	fs, err := NewFileServer("fs1", rootDir, false)
	if err != nil {
		t.Fatalf("NewFileServer: %v", err)
	}

	rootFID, err := fs.GetUserRoot("alice")
	if err != nil {
		t.Fatalf("GetUserRoot: %v", err)
	}

	rootInode, err := fs.GetInode(rootFID)
	if err != nil {
		t.Fatalf("GetInode root: %v", err)
	}

	trashInode, err := fs.GetChildInodeByName(rootInode, trashDirName)
	if err != nil {
		t.Fatalf("expected trash dir to exist: %v", err)
	}

	if err := fs.DeleteFile(trashInode.FID, "alice", true); err == nil {
		t.Fatalf("expected error deleting trash directory")
	}
}
