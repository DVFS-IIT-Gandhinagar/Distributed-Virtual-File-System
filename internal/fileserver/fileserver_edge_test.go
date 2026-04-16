package fileserver

import (
	"testing"

	"github.com/umangshikarvar/dvfs/internal/domain"
)

func TestCreateFileRejectsReservedTrashName(t *testing.T) {
	fs := newTestFileServer(t)
	rootFID, err := fs.GetUserRoot("alice", "alice")
	if err != nil {
		t.Fatalf("GetUserRoot failed: %v", err)
	}

	if _, err := fs.CreateFile(rootFID, ".trash", "alice", domain.InodeTypeDirectory); err == nil {
		t.Fatalf("expected creating reserved trash name to fail")
	}
}

func TestCreateFileInsideTrashIsRejected(t *testing.T) {
	fs := newTestFileServer(t)
	rootFID, err := fs.GetUserRoot("alice", "alice")
	if err != nil {
		t.Fatalf("GetUserRoot failed: %v", err)
	}

	rootInode, err := fs.GetInode(rootFID)
	if err != nil {
		t.Fatalf("GetInode(root) failed: %v", err)
	}
	trashInode, err := fs.GetChildInodeByName(rootInode, trashDirName)
	if err != nil {
		t.Fatalf("GetChildInodeByName(.trash) failed: %v", err)
	}

	if _, err := fs.CreateFile(trashInode.FID, "blocked.txt", "alice", domain.InodeTypeFile); err == nil {
		t.Fatalf("expected creating inside trash to fail")
	}
}

func TestDeleteFilePermissionDenied(t *testing.T) {
	fs := newTestFileServer(t)
	rootFID, err := fs.GetUserRoot("alice", "alice")
	if err != nil {
		t.Fatalf("GetUserRoot failed: %v", err)
	}

	fileFID, err := fs.CreateFile(rootFID, "secret.txt", "alice", domain.InodeTypeFile)
	if err != nil {
		t.Fatalf("CreateFile failed: %v", err)
	}

	if err := fs.DeleteFile(fileFID, "bob", false); err == nil {
		t.Fatalf("expected delete by non-owner to fail")
	}
}

func TestTrashRootAndRestoreMetadataMissing(t *testing.T) {
	fs := newTestFileServer(t)
	rootFID, err := fs.GetUserRoot("alice", "alice")
	if err != nil {
		t.Fatalf("GetUserRoot failed: %v", err)
	}

	if _, err := fs.TrashFile(rootFID, "alice", true); err == nil {
		t.Fatalf("expected trashing root directory to fail")
	}

	fileFID, err := fs.CreateFile(rootFID, "notes.txt", "alice", domain.InodeTypeFile)
	if err != nil {
		t.Fatalf("CreateFile failed: %v", err)
	}
	if _, err := fs.TrashFile(fileFID, "alice", false); err != nil {
		t.Fatalf("TrashFile failed: %v", err)
	}

	delete(fs.trashMeta, fileFID.String())
	if _, err := fs.RestoreFile(fileFID, "alice", "alice"); err == nil {
		t.Fatalf("expected restore without metadata to fail")
	}
}

func TestCheckStorageQuotaEdgeCases(t *testing.T) {
	fs := newTestFileServer(t)

	if err := fs.checkStorageQuota("ghost"); err == nil {
		t.Fatalf("expected unknown user quota check to fail")
	}

	rootFID, err := fs.GetUserRoot("alice", "alice")
	if err != nil {
		t.Fatalf("GetUserRoot failed: %v", err)
	}
	rootInode, err := fs.GetInode(rootFID)
	if err != nil {
		t.Fatalf("GetInode(root) failed: %v", err)
	}

	rootInode.Size = storageQuota + 1
	if err := fs.checkStorageQuota("alice"); err == nil {
		t.Fatalf("expected exceeded quota to fail")
	}
}

func TestChangeDirToTrashIsRejected(t *testing.T) {
	fs := newTestFileServer(t)
	rootFID, err := fs.GetUserRoot("alice", "alice")
	if err != nil {
		t.Fatalf("GetUserRoot failed: %v", err)
	}

	if _, err := fs.ChangeDir(rootFID, ".trash", rootFID); err == nil {
		t.Fatalf("expected cd to .trash to fail")
	}
}

func TestChangeDirToTrashSubPathIsRejected(t *testing.T) {
	fs := newTestFileServer(t)
	rootFID, err := fs.GetUserRoot("alice", "alice")
	if err != nil {
		t.Fatalf("GetUserRoot failed: %v", err)
	}

	dirFID, err := fs.CreateFile(rootFID, "docs", "alice", domain.InodeTypeDirectory)
	if err != nil {
		t.Fatalf("CreateFile docs failed: %v", err)
	}
	if _, err := fs.TrashFile(dirFID, "alice", true); err != nil {
		t.Fatalf("TrashFile docs failed: %v", err)
	}

	if _, err := fs.ChangeDir(rootFID, ".trash/docs", rootFID); err == nil {
		t.Fatalf("expected cd to .trash/docs to fail")
	}
}
