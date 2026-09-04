package fileserver

import (
	"path/filepath"
	"testing"
)

func TestInodeStorePersistence(t *testing.T) {
	dir := t.TempDir()

	store1, err := NewInodeStore(dir)
	if err != nil {
		t.Fatalf("NewInodeStore failed: %v", err)
	}

	id0 := store1.GetOrAssign("alice")
	id1 := store1.GetOrAssign("alice/.trash")
	id2 := store1.GetOrAssign("alice/docs")
	id3 := store1.GetOrAssign("alice/docs/report.pdf")
	id4 := store1.GetOrAssign("bob")

	if err := store1.Save(); err != nil {
		t.Fatalf("store1.Save() failed: %v", err)
	}

	// Create a second store instance on the same directory (simulating restart)
	store2, err := NewInodeStore(dir)
	if err != nil {
		t.Fatalf("NewInodeStore (reloaded) failed: %v", err)
	}

	if got := store2.GetOrAssign("alice"); got != id0 {
		t.Errorf("alice ID mismatch: got %d, want %d", got, id0)
	}
	if got := store2.GetOrAssign("alice/.trash"); got != id1 {
		t.Errorf("alice/.trash ID mismatch: got %d, want %d", got, id1)
	}
	if got := store2.GetOrAssign("alice/docs"); got != id2 {
		t.Errorf("alice/docs ID mismatch: got %d, want %d", got, id2)
	}
	if got := store2.GetOrAssign("alice/docs/report.pdf"); got != id3 {
		t.Errorf("alice/docs/report.pdf ID mismatch: got %d, want %d", got, id3)
	}
	if got := store2.GetOrAssign("bob"); got != id4 {
		t.Errorf("bob ID mismatch: got %d, want %d", got, id4)
	}

	// Verify next allocation starts from nextInodeID
	newID := store2.GetOrAssign("charlie")
	if newID <= id4 {
		t.Errorf("expected newID > id4 (%d), got %d", id4, newID)
	}
}

func TestInodeStoreDeterministicID(t *testing.T) {
	dir := t.TempDir()
	store, err := NewInodeStore(dir)
	if err != nil {
		t.Fatalf("NewInodeStore failed: %v", err)
	}

	idFirst := store.GetOrAssign("jassi/proj.c3p")
	for i := 0; i < 10; i++ {
		idSubsequent := store.GetOrAssign("jassi/proj.c3p")
		if idSubsequent != idFirst {
			t.Fatalf("iteration %d: expected deterministic ID %d, got %d", i, idFirst, idSubsequent)
		}
	}
}

func TestInodeStoreRenamePrefix(t *testing.T) {
	dir := t.TempDir()
	store, err := NewInodeStore(dir)
	if err != nil {
		t.Fatalf("NewInodeStore failed: %v", err)
	}

	dirID := store.GetOrAssign("jassi/folder")
	f1ID := store.GetOrAssign("jassi/folder/file1.txt")
	f2ID := store.GetOrAssign("jassi/folder/sub/file2.txt")
	otherID := store.GetOrAssign("jassi/other.txt")

	// Move jassi/folder -> jassi/.trash/folder
	store.RenamePrefix("jassi/folder", "jassi/.trash/folder")

	// Old paths should no longer exist in index
	if _, ok := store.Get("jassi/folder"); ok {
		t.Errorf("expected old dir path to be removed")
	}
	if _, ok := store.Get("jassi/folder/file1.txt"); ok {
		t.Errorf("expected old file1 path to be removed")
	}
	if _, ok := store.Get("jassi/folder/sub/file2.txt"); ok {
		t.Errorf("expected old file2 path to be removed")
	}

	// New paths should retain exact same IDs
	if id, ok := store.Get("jassi/.trash/folder"); !ok || id != dirID {
		t.Errorf("trashed folder ID mismatch: got %d, want %d", id, dirID)
	}
	if id, ok := store.Get("jassi/.trash/folder/file1.txt"); !ok || id != f1ID {
		t.Errorf("trashed file1 ID mismatch: got %d, want %d", id, f1ID)
	}
	if id, ok := store.Get("jassi/.trash/folder/sub/file2.txt"); !ok || id != f2ID {
		t.Errorf("trashed file2 ID mismatch: got %d, want %d", id, f2ID)
	}

	// Unrelated path untouched
	if id, ok := store.Get("jassi/other.txt"); !ok || id != otherID {
		t.Errorf("other path ID mismatch: got %d, want %d", id, otherID)
	}
}

func TestInodeStoreRemove(t *testing.T) {
	dir := t.TempDir()
	store, err := NewInodeStore(dir)
	if err != nil {
		t.Fatalf("NewInodeStore failed: %v", err)
	}

	id := store.GetOrAssign("temp.txt")
	store.Remove("temp.txt")

	if _, exists := store.Get("temp.txt"); exists {
		t.Errorf("expected temp.txt to be removed")
	}

	// Re-assigning should allocate a NEW ID, not reuse the removed one
	newID := store.GetOrAssign("temp.txt")
	if newID == id {
		t.Errorf("expected new Inode ID for re-created file, got recycled ID %d", id)
	}
}

func TestInodeStorePathNormalization(t *testing.T) {
	dir := t.TempDir()
	store, err := NewInodeStore(dir)
	if err != nil {
		t.Fatalf("NewInodeStore failed: %v", err)
	}

	id1 := store.GetOrAssign("alice/photos/cat.jpg")
	// Windows-style backslashes and redundant dots
	id2 := store.GetOrAssign(filepath.Join("alice", "photos", "cat.jpg"))
	id3 := store.GetOrAssign("./alice/photos/cat.jpg")

	if id1 != id2 || id1 != id3 {
		t.Errorf("path normalization failed: id1=%d, id2=%d, id3=%d", id1, id2, id3)
	}
}
