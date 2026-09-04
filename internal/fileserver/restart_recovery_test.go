package fileserver

import (
	"testing"

	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/domain"
)

func TestFileserverRestartInodeStability(t *testing.T) {
	rootDir := t.TempDir()

	// 1. Boot fileserver instance 1
	fs1, err := NewFileServer("fs-1", rootDir, false, "", "")
	if err != nil {
		t.Fatalf("Failed to create fs1: %v", err)
	}

	// Create user roots
	aliceRoot1, err := fs1.GetUserRoot("alice", "alice")
	if err != nil {
		t.Fatalf("GetUserRoot alice failed: %v", err)
	}
	jassiRoot1, err := fs1.GetUserRoot("jassi", "jassi")
	if err != nil {
		t.Fatalf("GetUserRoot jassi failed: %v", err)
	}

	// Create files and subdirectories
	dirFID1, err := fs1.CreateFile(jassiRoot1, "workspace", "jassi", domain.InodeTypeDirectory)
	if err != nil {
		t.Fatalf("CreateFile workspace failed: %v", err)
	}
	fileFID1, err := fs1.CreateFile(dirFID1, "proj.c3p", "jassi", domain.InodeTypeFile)
	if err != nil {
		t.Fatalf("CreateFile proj.c3p failed: %v", err)
	}
	aliceFileFID1, err := fs1.CreateFile(aliceRoot1, "notes.txt", "alice", domain.InodeTypeFile)
	if err != nil {
		t.Fatalf("CreateFile notes.txt failed: %v", err)
	}

	// Record FIDs
	aliceRootStr1 := aliceRoot1.String()
	jassiRootStr1 := jassiRoot1.String()
	dirStr1 := dirFID1.String()
	fileStr1 := fileFID1.String()
	aliceFileStr1 := aliceFileFID1.String()

	// 2. Simulate server restart: create a new FileServer instance on the exact same rootDir
	fs2, err := NewFileServer("fs-1", rootDir, false, "", "")
	if err != nil {
		t.Fatalf("Failed to create fs2 (restart): %v", err)
	}

	// 3. Verify user roots match exactly
	aliceRoot2, err := fs2.GetUserRoot("alice", "alice")
	if err != nil {
		t.Fatalf("GetUserRoot alice post-restart failed: %v", err)
	}
	if aliceRoot2.String() != aliceRootStr1 {
		t.Fatalf("Alice root FID changed after restart: got %s, want %s", aliceRoot2.String(), aliceRootStr1)
	}

	jassiRoot2, err := fs2.GetUserRoot("jassi", "jassi")
	if err != nil {
		t.Fatalf("GetUserRoot jassi post-restart failed: %v", err)
	}
	if jassiRoot2.String() != jassiRootStr1 {
		t.Fatalf("Jassi root FID changed after restart: got %s, want %s", jassiRoot2.String(), jassiRootStr1)
	}

	// 4. Verify directory and file FIDs match exactly
	jassiRootInode2, err := fs2.GetInode(jassiRoot2)
	if err != nil {
		t.Fatalf("GetInode jassi root post-restart failed: %v", err)
	}

	wsInode2, err := fs2.GetChildInodeByName(jassiRootInode2, "workspace")
	if err != nil {
		t.Fatalf("workspace dir not found post-restart: %v", err)
	}
	if wsInode2.FID.String() != dirStr1 {
		t.Fatalf("workspace FID changed after restart: got %s, want %s", wsInode2.FID.String(), dirStr1)
	}

	projInode2, err := fs2.GetChildInodeByName(wsInode2, "proj.c3p")
	if err != nil {
		t.Fatalf("proj.c3p not found post-restart: %v", err)
	}
	if projInode2.FID.String() != fileStr1 {
		t.Fatalf("proj.c3p FID changed after restart: got %s, want %s", projInode2.FID.String(), fileStr1)
	}

	// 5. Verify Alice's file matches
	aliceRootInode2, err := fs2.GetInode(aliceRoot2)
	if err != nil {
		t.Fatalf("GetInode alice root post-restart failed: %v", err)
	}
	notesInode2, err := fs2.GetChildInodeByName(aliceRootInode2, "notes.txt")
	if err != nil {
		t.Fatalf("notes.txt not found post-restart: %v", err)
	}
	if notesInode2.FID.String() != aliceFileStr1 {
		t.Fatalf("notes.txt FID changed after restart: got %s, want %s", notesInode2.FID.String(), aliceFileStr1)
	}
}
