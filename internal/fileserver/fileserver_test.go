package fileserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/umangshikarvar/dvfs/internal/domain"
)

func newTestFileServer(t *testing.T) *FileServer {
	t.Helper()

	root := t.TempDir()
	fs, err := NewFileServer("fs-test", root, false, "")
	if err != nil {
		t.Fatalf("NewFileServer failed: %v", err)
	}
	return fs
}

func TestGetUserRootCreatesTrashAndIsIdempotent(t *testing.T) {
	fs := newTestFileServer(t)

	rootFID1, err := fs.GetUserRoot("alice", "alice")
	if err != nil {
		t.Fatalf("GetUserRoot (first call) failed: %v", err)
	}

	rootInode, err := fs.GetInode(rootFID1)
	if err != nil {
		t.Fatalf("GetInode(root) failed: %v", err)
	}

	trash, err := fs.GetChildInodeByName(rootInode, trashDirName)
	if err != nil {
		t.Fatalf("expected trash directory to exist: %v", err)
	}
	if trash.Type != domain.InodeTypeDirectory {
		t.Fatalf("trash inode type mismatch: got=%v want=%v", trash.Type, domain.InodeTypeDirectory)
	}

	rootFID2, err := fs.GetUserRoot("alice", "alice")
	if err != nil {
		t.Fatalf("GetUserRoot (second call) failed: %v", err)
	}

	if rootFID1.String() != rootFID2.String() {
		t.Fatalf("expected root FID to be stable: first=%s second=%s", rootFID1.String(), rootFID2.String())
	}

	countTrash := 0
	for _, childFID := range rootInode.Children {
		child, _ := fs.GetInode(childFID)
		if child != nil && child.Name == trashDirName {
			countTrash++
		}
	}
	if countTrash != 1 {
		t.Fatalf("expected exactly one trash directory, got=%d", countTrash)
	}
}

func TestCreateListWriteReadAndPath(t *testing.T) {
	fs := newTestFileServer(t)
	rootFID, err := fs.GetUserRoot("alice", "alice")
	if err != nil {
		t.Fatalf("GetUserRoot failed: %v", err)
	}

	docsFID, err := fs.CreateFile(rootFID, "docs", "alice", domain.InodeTypeDirectory)
	if err != nil {
		t.Fatalf("CreateFile docs failed: %v", err)
	}

	_, err = fs.CreateFile(docsFID, "note.txt", "alice", domain.InodeTypeFile)
	if err != nil {
		t.Fatalf("CreateFile note.txt failed: %v", err)
	}

	entries, err := fs.ListDirectory(rootFID)
	if err != nil {
		t.Fatalf("ListDirectory(root) failed: %v", err)
	}

	foundDocs := false
	for _, e := range entries {
		if e.Name == "docs" && e.Type == domain.InodeTypeDirectory {
			foundDocs = true
			break
		}
	}
	if !foundDocs {
		t.Fatalf("expected docs directory in root listing")
	}

	if err := fs.WriteFile(docsFID, "note.txt", 0, []byte("hello")); err != nil {
		t.Fatalf("WriteFile first chunk failed: %v", err)
	}
	if err := fs.WriteFile(docsFID, "note.txt", 5, []byte(" world")); err != nil {
		t.Fatalf("WriteFile second chunk failed: %v", err)
	}

	data, err := fs.ReadFile(docsFID, "note.txt", 0, 11)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if got, want := string(data), "hello world"; got != want {
		t.Fatalf("ReadFile content mismatch: got=%q want=%q", got, want)
	}

	if path, err := fs.Path(docsFID); err != nil {
		t.Fatalf("Path(docs) failed: %v", err)
	} else if !strings.Contains(path, filepath.Join("alice", "docs")) {
		t.Fatalf("unexpected docs path: %q", path)
	}
}

func TestChangeDirScenarios(t *testing.T) {
	fs := newTestFileServer(t)
	rootFID, err := fs.GetUserRoot("alice", "alice")
	if err != nil {
		t.Fatalf("GetUserRoot failed: %v", err)
	}

	aFID, err := fs.CreateFile(rootFID, "a", "alice", domain.InodeTypeDirectory)
	if err != nil {
		t.Fatalf("CreateFile a failed: %v", err)
	}
	bFID, err := fs.CreateFile(aFID, "b", "alice", domain.InodeTypeDirectory)
	if err != nil {
		t.Fatalf("CreateFile b failed: %v", err)
	}

	if got, err := fs.ChangeDir(rootFID, "a", rootFID); err != nil || got.String() != aFID.String() {
		t.Fatalf("ChangeDir to a mismatch: got=%v err=%v want=%s", got, err, aFID.String())
	}

	if got, err := fs.ChangeDir(aFID, "b", rootFID); err != nil || got.String() != bFID.String() {
		t.Fatalf("ChangeDir to b mismatch: got=%v err=%v want=%s", got, err, bFID.String())
	}

	if got, err := fs.ChangeDir(bFID, "..", rootFID); err != nil || got.String() != aFID.String() {
		t.Fatalf("ChangeDir .. mismatch: got=%v err=%v want=%s", got, err, aFID.String())
	}

	if got, err := fs.ChangeDir(bFID, "/", rootFID); err != nil || got.String() != rootFID.String() {
		t.Fatalf("ChangeDir / mismatch: got=%v err=%v want=%s", got, err, rootFID.String())
	}

	if _, err := fs.ChangeDir(rootFID, "does/not/exist", rootFID); err == nil {
		t.Fatalf("expected invalid path ChangeDir to fail")
	}
}

func TestDeleteFileRecursiveAndNonRecursive(t *testing.T) {
	fs := newTestFileServer(t)
	rootFID, err := fs.GetUserRoot("alice", "alice")
	if err != nil {
		t.Fatalf("GetUserRoot failed: %v", err)
	}

	dirFID, err := fs.CreateFile(rootFID, "docs", "alice", domain.InodeTypeDirectory)
	if err != nil {
		t.Fatalf("CreateFile docs failed: %v", err)
	}

	_, err = fs.CreateFile(dirFID, "note.txt", "alice", domain.InodeTypeFile)
	if err != nil {
		t.Fatalf("CreateFile child file failed: %v", err)
	}

	if err := fs.DeleteFile(dirFID, "alice", false); err == nil {
		t.Fatalf("expected non-recursive delete of non-empty directory to fail")
	}

	if err := fs.DeleteFile(dirFID, "alice", true); err != nil {
		t.Fatalf("recursive delete failed: %v", err)
	}

	if _, err := fs.GetInode(dirFID); err == nil {
		t.Fatalf("expected deleted directory inode lookup to fail")
	}
}

func TestTrashAndRestoreRoundTrip(t *testing.T) {
	fs := newTestFileServer(t)
	rootFID, err := fs.GetUserRoot("alice", "alice")
	if err != nil {
		t.Fatalf("GetUserRoot failed: %v", err)
	}

	fileFID, err := fs.CreateFile(rootFID, "notes.txt", "alice", domain.InodeTypeFile)
	if err != nil {
		t.Fatalf("CreateFile notes.txt failed: %v", err)
	}

	if err := fs.WriteFile(rootFID, "notes.txt", 0, []byte("payload")); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	trashedName, err := fs.TrashFile(fileFID, "alice", false)
	if err != nil {
		t.Fatalf("TrashFile failed: %v", err)
	}
	if trashedName == "" {
		t.Fatalf("TrashFile returned empty trashedName")
	}

	rootInode, _ := fs.GetInode(rootFID)
	trashInode, err := fs.GetChildInodeByName(rootInode, trashDirName)
	if err != nil {
		t.Fatalf("trash directory missing: %v", err)
	}

	trashedInode, err := fs.GetInode(fileFID)
	if err != nil {
		t.Fatalf("expected trashed inode to still exist in inode map: %v", err)
	}
	if trashedInode.Parent.FID.String() != trashInode.FID.String() {
		t.Fatalf("trashed inode parent mismatch: got=%s want=%s", trashedInode.Parent.FID.String(), trashInode.FID.String())
	}
	if _, err := os.Stat(trashedInode.OSPath); err != nil {
		t.Fatalf("trashed file not found on disk at %s: %v", trashedInode.OSPath, err)
	}

	restoredName, err := fs.RestoreFile(fileFID, "alice", "alice")
	if err != nil {
		t.Fatalf("RestoreFile failed: %v", err)
	}
	if restoredName == "" {
		t.Fatalf("RestoreFile returned empty restoredName")
	}

	restoredInode, err := fs.GetInode(fileFID)
	if err != nil {
		t.Fatalf("expected restored inode to exist: %v", err)
	}
	if restoredInode.Parent.FID.String() != rootFID.String() {
		t.Fatalf("restored inode parent mismatch: got=%s want=%s", restoredInode.Parent.FID.String(), rootFID.String())
	}
	if _, err := os.Stat(restoredInode.OSPath); err != nil {
		t.Fatalf("restored file not found on disk at %s: %v", restoredInode.OSPath, err)
	}
}

func TestShareAndUnsharePropagateACLAndTrackExplicitShare(t *testing.T) {
	fs := newTestFileServer(t)
	rootFID, err := fs.GetUserRoot("alice", "alice")
	if err != nil {
		t.Fatalf("GetUserRoot failed: %v", err)
	}

	projectFID, err := fs.CreateFile(rootFID, "project", "alice", domain.InodeTypeDirectory)
	if err != nil {
		t.Fatalf("CreateFile project failed: %v", err)
	}

	fileFID, err := fs.CreateFile(projectFID, "readme.txt", "alice", domain.InodeTypeFile)
	if err != nil {
		t.Fatalf("CreateFile readme.txt failed: %v", err)
	}

	if err := fs.Share("alice", "bob", projectFID); err != nil {
		t.Fatalf("Share failed: %v", err)
	}

	projectInode, _ := fs.GetInode(projectFID)
	fileInode, _ := fs.GetInode(fileFID)

	contains := func(list []string, target string) bool {
		for _, v := range list {
			if v == target {
				return true
			}
		}
		return false
	}

	if !contains(projectInode.ACL.Shared, "bob") {
		t.Fatalf("project ACL did not include bob after share: %v", projectInode.ACL.Shared)
	}
	if !contains(fileInode.ACL.Shared, "bob") {
		t.Fatalf("child file ACL did not include bob after share: %v", fileInode.ACL.Shared)
	}

	shareKey := filepath.Join("alice", "project")
	if !contains(fs.Shared[shareKey], "bob") {
		t.Fatalf("explicit dir share map missing bob for key=%s: %v", shareKey, fs.Shared[shareKey])
	}

	if err := fs.Unshare("alice", "bob", projectFID); err != nil {
		t.Fatalf("Unshare failed: %v", err)
	}

	if contains(projectInode.ACL.Shared, "bob") {
		t.Fatalf("project ACL still includes bob after unshare: %v", projectInode.ACL.Shared)
	}
	if contains(fileInode.ACL.Shared, "bob") {
		t.Fatalf("child file ACL still includes bob after unshare: %v", fileInode.ACL.Shared)
	}

	if users := fs.Shared[shareKey]; len(users) != 0 {
		t.Fatalf("explicit dir share map not cleared after unshare: %v", users)
	}
}

func TestTrashSharedDirectoryHidesAndRestoreReaddsShareEntry(t *testing.T) {
	fs := newTestFileServer(t)
	rootFID, err := fs.GetUserRoot("alice", "alice")
	if err != nil {
		t.Fatalf("GetUserRoot failed: %v", err)
	}

	projectFID, err := fs.CreateFile(rootFID, "project", "alice", domain.InodeTypeDirectory)
	if err != nil {
		t.Fatalf("CreateFile project failed: %v", err)
	}

	if err := fs.Share("alice", "bob", projectFID); err != nil {
		t.Fatalf("Share failed: %v", err)
	}

	shareKey := filepath.Join("alice", "project")
	if len(fs.Shared[shareKey]) == 0 {
		t.Fatalf("expected explicit share entry to exist before trash")
	}

	if _, err := fs.TrashFile(projectFID, "alice", true); err != nil {
		t.Fatalf("TrashFile failed: %v", err)
	}
	if _, ok := fs.Shared[shareKey]; ok {
		t.Fatalf("expected explicit share entry to be removed when directory is trashed")
	}

	if _, err := fs.RestoreFile(projectFID, "alice", "alice"); err != nil {
		t.Fatalf("RestoreFile failed: %v", err)
	}
	if len(fs.Shared[shareKey]) == 0 {
		t.Fatalf("expected explicit share entry to be restored after directory restore")
	}
}

func TestDeleteSharedDirectoryRemovesShareEntry(t *testing.T) {
	fs := newTestFileServer(t)
	rootFID, err := fs.GetUserRoot("alice", "alice")
	if err != nil {
		t.Fatalf("GetUserRoot failed: %v", err)
	}

	projectFID, err := fs.CreateFile(rootFID, "project", "alice", domain.InodeTypeDirectory)
	if err != nil {
		t.Fatalf("CreateFile project failed: %v", err)
	}

	if err := fs.Share("alice", "bob", projectFID); err != nil {
		t.Fatalf("Share failed: %v", err)
	}

	shareKey := filepath.Join("alice", "project")
	if len(fs.Shared[shareKey]) == 0 {
		t.Fatalf("expected explicit share entry to exist before delete")
	}

	if err := fs.DeleteFile(projectFID, "alice", true); err != nil {
		t.Fatalf("DeleteFile failed: %v", err)
	}
	if _, ok := fs.Shared[shareKey]; ok {
		t.Fatalf("expected explicit share entry to be removed when directory is permanently deleted")
	}
}

func TestShowTrashFiltersEntriesByRequesterACL(t *testing.T) {
	fs := newTestFileServer(t)
	rootFID, err := fs.GetUserRoot("alice", "alice")
	if err != nil {
		t.Fatalf("GetUserRoot failed: %v", err)
	}

	privateFID, err := fs.CreateFile(rootFID, "private.txt", "alice", domain.InodeTypeFile)
	if err != nil {
		t.Fatalf("CreateFile private.txt failed: %v", err)
	}

	sharedFID, err := fs.CreateFile(rootFID, "shared", "alice", domain.InodeTypeDirectory)
	if err != nil {
		t.Fatalf("CreateFile shared failed: %v", err)
	}
	if err := fs.Share("alice", "bob", sharedFID); err != nil {
		t.Fatalf("Share failed: %v", err)
	}

	if _, err := fs.TrashFile(privateFID, "alice", false); err != nil {
		t.Fatalf("TrashFile private failed: %v", err)
	}
	if _, err := fs.TrashFile(sharedFID, "alice", true); err != nil {
		t.Fatalf("TrashFile shared failed: %v", err)
	}

	bobEntries, err := fs.ShowTrash("alice", "bob")
	if err != nil {
		t.Fatalf("ShowTrash for bob failed: %v", err)
	}
	if len(bobEntries) != 1 {
		t.Fatalf("expected bob to see exactly one trashed entry, got=%d", len(bobEntries))
	}
	if bobEntries[0].Name != "shared" {
		t.Fatalf("expected bob to only see shared entry, got=%s", bobEntries[0].Name)
	}

	aliceEntries, err := fs.ShowTrash("alice", "alice")
	if err != nil {
		t.Fatalf("ShowTrash for alice failed: %v", err)
	}
	if len(aliceEntries) != 2 {
		t.Fatalf("expected alice to see all trashed entries, got=%d", len(aliceEntries))
	}
}

func TestRestoreFromTrashDeniedWhenRequesterLacksACL(t *testing.T) {
	fs := newTestFileServer(t)
	rootFID, err := fs.GetUserRoot("alice", "alice")
	if err != nil {
		t.Fatalf("GetUserRoot failed: %v", err)
	}

	privateFID, err := fs.CreateFile(rootFID, "private.txt", "alice", domain.InodeTypeFile)
	if err != nil {
		t.Fatalf("CreateFile failed: %v", err)
	}
	if _, err := fs.TrashFile(privateFID, "alice", false); err != nil {
		t.Fatalf("TrashFile failed: %v", err)
	}

	if _, err := fs.RestoreFile(privateFID, "alice", "bob"); err == nil {
		t.Fatalf("expected restore by unauthorized requester to fail")
	}
}
