package fileserver

import (
	"os"
	"path/filepath"
	"testing"
)

// TestInodeStoreRenamePrefixSubtree tests moving entire subtrees (like moving a directory to .trash)
// and verifies that all nested child paths are updated with the correct new prefix.
func TestInodeStoreRenamePrefixSubtree(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewInodeStore(tmpDir)
	if err != nil {
		t.Fatalf("NewInodeStore failed: %v", err)
	}

	// Register a folder and nested children
	dirID := store.GetOrAssign("alice/workspace")
	f1ID := store.GetOrAssign("alice/workspace/doc.txt")
	f2ID := store.GetOrAssign("alice/workspace/src/main.go")

	// Rename alice/workspace -> .trash/workspace
	store.RenamePrefix("alice/workspace", ".trash/workspace")

	// Old paths must no longer exist
	if _, exists := store.Get("alice/workspace"); exists {
		t.Errorf("old dir path should no longer exist in InodeStore")
	}
	if _, exists := store.Get("alice/workspace/doc.txt"); exists {
		t.Errorf("old f1 path should no longer exist in InodeStore")
	}
	if _, exists := store.Get("alice/workspace/src/main.go"); exists {
		t.Errorf("old f2 path should no longer exist in InodeStore")
	}

	// New paths must have the EXACT same Inode IDs
	newDirID, exists := store.Get(".trash/workspace")
	if !exists || newDirID != dirID {
		t.Errorf("expected new dir ID %d, got %d (exists: %v)", dirID, newDirID, exists)
	}

	newF1ID, exists := store.Get(".trash/workspace/doc.txt")
	if !exists || newF1ID != f1ID {
		t.Errorf("expected new f1 ID %d, got %d (exists: %v)", f1ID, newF1ID, exists)
	}

	newF2ID, exists := store.Get(".trash/workspace/src/main.go")
	if !exists || newF2ID != f2ID {
		t.Errorf("expected new f2 ID %d, got %d (exists: %v)", f2ID, newF2ID, exists)
	}
}

// TestInodeStoreCorruptFileRecovery verifies that a corrupted index file on disk
// causes InodeStore to gracefully log a warning and start fresh rather than crashing.
func TestInodeStoreCorruptFileRecovery(t *testing.T) {
	tmpDir := t.TempDir()
	indexPath := filepath.Join(tmpDir, inodeIndexFilename)

	// Write invalid JSON
	if err := os.WriteFile(indexPath, []byte("{corrupted json content..."), 0644); err != nil {
		t.Fatalf("failed to write corrupted file: %v", err)
	}

	store, err := NewInodeStore(tmpDir)
	if err != nil {
		t.Fatalf("expected NewInodeStore to handle corrupt file gracefully, but returned err: %v", err)
	}

	// Should start fresh with NextInodeID == 0
	if store.NextInodeID() != 0 {
		t.Errorf("expected NextInodeID=0 after corrupt index recovery, got %d", store.NextInodeID())
	}

	// Should be able to assign and save normally
	id := store.GetOrAssign("alice/test.txt")
	if id != 0 {
		t.Errorf("expected first assigned ID to be 0, got %d", id)
	}
	if err := store.Save(); err != nil {
		t.Fatalf("Save after recovery failed: %v", err)
	}
}

// TestInodeStoreDeterministicAllocation verifies deterministic IDs.
func TestInodeStoreDeterministicAllocation(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewInodeStore(tmpDir)
	if err != nil {
		t.Fatalf("NewInodeStore failed: %v", err)
	}

	id1 := store.GetOrAssign("path/a")
	id2 := store.GetOrAssign("path/b")
	id1Repeat := store.GetOrAssign("path/a")

	if id1 == id2 {
		t.Errorf("different paths must receive different IDs, got %d for both", id1)
	}
	if id1 != id1Repeat {
		t.Errorf("same path must receive identical ID, got %d and %d", id1, id1Repeat)
	}
}

// TestInodeStoreRemoveDoesNotRecycleID verifies ID monotonicity.
func TestInodeStoreRemoveDoesNotRecycleID(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewInodeStore(tmpDir)
	if err != nil {
		t.Fatalf("NewInodeStore failed: %v", err)
	}

	id0 := store.GetOrAssign("alice/file0.txt")
	store.Remove("alice/file0.txt")

	id1 := store.GetOrAssign("alice/file1.txt")
	if id1 <= id0 {
		t.Errorf("new ID (%d) must be strictly greater than deleted ID (%d)", id1, id0)
	}
}
