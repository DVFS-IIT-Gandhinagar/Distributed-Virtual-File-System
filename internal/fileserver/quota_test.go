package fileserver

import (
	"context"
	"testing"

	pb "github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/api/fileserver"
)

func TestGetUserQuotaDefault(t *testing.T) {
	fs := newTestFileServer(t)

	quota := fs.GetUserQuota("unknown_user")
	if quota != defaultStorageQuota {
		t.Errorf("expected default quota %d, got %d", defaultStorageQuota, quota)
	}
}

func TestSetUserQuotaAndPersistence(t *testing.T) {
	root := t.TempDir()
	fs, err := NewFileServer("fs-test", root, false, "", "")
	if err != nil {
		t.Fatalf("NewFileServer failed: %v", err)
	}

	customQuota := uint64(2 * 1024 * 1024 * 1024) // 2 GB
	if err := fs.SetUserQuota("alice", customQuota); err != nil {
		t.Fatalf("SetUserQuota failed: %v", err)
	}

	if q := fs.GetUserQuota("alice"); q != customQuota {
		t.Errorf("expected quota %d, got %d", customQuota, q)
	}

	// Create a new FileServer instance on the same directory to verify persistence
	fs2, err := NewFileServer("fs-test", root, false, "", "")
	if err != nil {
		t.Fatalf("NewFileServer (restart) failed: %v", err)
	}

	if q := fs2.GetUserQuota("alice"); q != customQuota {
		t.Errorf("expected recovered quota %d, got %d", customQuota, q)
	}
}

func TestCheckStorageQuotaDynamic(t *testing.T) {
	fs := newTestFileServer(t)

	fid, err := fs.GetUserRoot("bob", "bob")
	if err != nil {
		t.Fatalf("GetUserRoot failed: %v", err)
	}

	rootInode, err := fs.GetInode(fid)
	if err != nil {
		t.Fatalf("GetInode failed: %v", err)
	}

	// Restrict quota to 500 bytes
	if err := fs.SetUserQuota("bob", 500); err != nil {
		t.Fatalf("SetUserQuota failed: %v", err)
	}

	// Simulate root inode size = 600 bytes
	fs.mu.Lock()
	rootInode.Size = 600
	fs.mu.Unlock()

	// Should exceed quota
	if err := fs.checkStorageQuota("bob"); err == nil {
		t.Errorf("expected error when size > quota, got nil")
	}

	// Expand quota to 1000 bytes
	if err := fs.SetUserQuota("bob", 1000); err != nil {
		t.Fatalf("SetUserQuota failed: %v", err)
	}

	// Should pass now
	if err := fs.checkStorageQuota("bob"); err != nil {
		t.Errorf("expected success after expanding quota, got: %v", err)
	}
}

func TestGRPCHandlerSetQuota(t *testing.T) {
	fs := newTestFileServer(t)
	handler := NewGRPCHandler(fs)
	ctx := context.Background()

	// Valid request
	resp, err := handler.SetQuota(ctx, &pb.SetQuotaRequest{
		Username:   "charlie",
		QuotaBytes: 5000,
	})
	if err != nil {
		t.Fatalf("SetQuota gRPC call error: %v", err)
	}
	if !resp.Success {
		t.Errorf("expected success=true, got false, error: %s", resp.Error)
	}
	if q := fs.GetUserQuota("charlie"); q != 5000 {
		t.Errorf("expected quota 5000, got %d", q)
	}

	// Missing username
	respInvalid, err := handler.SetQuota(ctx, &pb.SetQuotaRequest{
		Username:   "",
		QuotaBytes: 5000,
	})
	if err != nil || respInvalid.Success {
		t.Errorf("expected failure on empty username, got resp=%v, err=%v", respInvalid, err)
	}

	// Zero quota bytes
	respZero, err := handler.SetQuota(ctx, &pb.SetQuotaRequest{
		Username:   "charlie",
		QuotaBytes: 0,
	})
	if err != nil || respZero.Success {
		t.Errorf("expected failure on zero quota, got resp=%v, err=%v", respZero, err)
	}
}
