package fileserver

import (
	"context"
	"strings"
	"testing"

	pb "github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/api/fileserver"
	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/domain"
)

func TestMultiUserQuotaEnforcement(t *testing.T) {
	fs := newTestFileServer(t)
	handler := NewGRPCHandler(fs)
	ctx := context.Background()

	const quotaUser10 = uint64(10 * 1024 * 1024) // 10 MB
	const quotaUser20 = uint64(20 * 1024 * 1024) // 20 MB

	// 1. Configure quotas: 10 MB for user10, 20 MB for user20
	if err := fs.SetUserQuota("user10", quotaUser10); err != nil {
		t.Fatalf("failed to set user10 quota: %v", err)
	}
	if err := fs.SetUserQuota("user20", quotaUser20); err != nil {
		t.Fatalf("failed to set user20 quota: %v", err)
	}

	// 2. Register user roots
	reg1, err := handler.RegisterClient(ctx, &pb.RegisterClientRequest{
		Username: "user10", RootUser: "user10", RootPath: "user10",
	})
	if err != nil || !reg1.Success {
		t.Fatalf("failed to register user10: err=%v, resp=%+v", err, reg1)
	}
	rootFID10 := domain.FIDFromProto(reg1.UserRootFid)

	reg2, err := handler.RegisterClient(ctx, &pb.RegisterClientRequest{
		Username: "user20", RootUser: "user20", RootPath: "user20",
	})
	if err != nil || !reg2.Success {
		t.Fatalf("failed to register user20: err=%v, resp=%+v", err, reg2)
	}
	rootFID20 := domain.FIDFromProto(reg2.UserRootFid)

	// ==========================================
	// Test Scenario A: User 10 (10 MB quota)
	// ==========================================

	// 3. User 10 creates and uploads a 5 MB file -> MUST SUCCEED (5 MB <= 10 MB)
	createResp, err := handler.CreateFile(ctx, &pb.CreateFileRequest{
		Name:     "file5mb.dat",
		RootUser: "user10",
		Fid:      reg1.UserRootFid,
		Type:     pb.InodeType_FILE,
		Size:     5 * 1024 * 1024,
	})
	if err != nil || !createResp.Success {
		t.Fatalf("user10 CreateFile(5mb) failed: err=%v, resp=%+v", err, createResp)
	}

	data5MB := make([]byte, 5*1024*1024)
	if err := fs.WriteFile(rootFID10, "file5mb.dat", 0, data5MB); err != nil {
		t.Fatalf("user10 WriteFile(5mb) failed: %v", err)
	}

	rootInode10, _ := fs.GetInode(rootFID10)
	if rootInode10.Size != 5*1024*1024 {
		t.Fatalf("expected user10 storage to be 5 MB, got %d bytes", rootInode10.Size)
	}

	// 4. User 10 tries to create a 6 MB file with pre-check size -> MUST FAIL BEFORE UPLOAD
	// Because remaining free space is 5 MB, and 6 MB > 5 MB
	blockedPrecheck, err := handler.CreateFile(ctx, &pb.CreateFileRequest{
		Name:     "file6mb.dat",
		RootUser: "user10",
		Fid:      reg1.UserRootFid,
		Type:     pb.InodeType_FILE,
		Size:     6 * 1024 * 1024,
	})
	if err != nil {
		t.Fatalf("unexpected RPC error on CreateFile precheck: %v", err)
	}
	if blockedPrecheck.Success {
		t.Fatalf("expected CreateFile with size > free space to be rejected, but it succeeded")
	}
	if !strings.Contains(blockedPrecheck.Error, "storage quota exceeded") {
		t.Errorf("expected quota exceeded error, got: %s", blockedPrecheck.Error)
	}

	// 5. If size is unknown (Size: 0), CreateFile succeeds, but WriteFile blocks write when exceeding free space
	createUnknown, err := handler.CreateFile(ctx, &pb.CreateFileRequest{
		Name:     "file6mb_unknown.dat",
		RootUser: "user10",
		Fid:      reg1.UserRootFid,
		Type:     pb.InodeType_FILE,
		Size:     0,
	})
	if err != nil || !createUnknown.Success {
		t.Fatalf("CreateFile(size=0) should pass initial check: %+v", createUnknown)
	}

	data6MB := make([]byte, 6*1024*1024)
	writeErr := fs.WriteFile(rootFID10, "file6mb_unknown.dat", 0, data6MB)
	if writeErr == nil {
		t.Fatalf("expected WriteFile to fail when write exceeds remaining free space, but succeeded")
	}
	if !strings.Contains(writeErr.Error(), "storage quota exceeded") {
		t.Errorf("expected 'storage quota exceeded' error, got: %v", writeErr)
	}

	// Verify that user10 root size was NOT increased by the rejected write (still 5 MB)
	rootInode10Check, _ := fs.GetInode(rootFID10)
	if rootInode10Check.Size != 5*1024*1024 {
		t.Errorf("rejected write must not corrupt inode size: expected 5 MB, got %d", rootInode10Check.Size)
	}

	// 6. Admin increases User 10 quota to 15 MB via SetQuota
	setResp, err := handler.SetQuota(ctx, &pb.SetQuotaRequest{
		Username:   "user10",
		QuotaBytes: 15 * 1024 * 1024,
	})
	if err != nil || !setResp.Success {
		t.Fatalf("SetQuota(15MB) failed: %v", err)
	}

	// Now CreateFile with Size: 6 MB MUST SUCCEED (5 MB used + 6 MB = 11 MB <= 15 MB quota)
	unblockedCreate, err := handler.CreateFile(ctx, &pb.CreateFileRequest{
		Name:     "allowed_after_expansion.dat",
		RootUser: "user10",
		Fid:      reg1.UserRootFid,
		Type:     pb.InodeType_FILE,
		Size:     6 * 1024 * 1024,
	})
	if err != nil || !unblockedCreate.Success {
		t.Fatalf("expected CreateFile to succeed after quota increase, got: %+v, err=%v", unblockedCreate, err)
	}

	// ==========================================
	// Test Scenario B: User 20 (20 MB quota)
	// ==========================================

	// 7. User 20 is independent: uploads 15 MB file -> MUST SUCCEED
	createResp20, err := handler.CreateFile(ctx, &pb.CreateFileRequest{
		Name:     "big15mb.dat",
		RootUser: "user20",
		Fid:      reg2.UserRootFid,
		Type:     pb.InodeType_FILE,
		Size:     15 * 1024 * 1024,
	})
	if err != nil || !createResp20.Success {
		t.Fatalf("user20 CreateFile(15mb) failed: err=%v, resp=%+v", err, createResp20)
	}

	data15MB := make([]byte, 15*1024*1024)
	if err := fs.WriteFile(rootFID20, "big15mb.dat", 0, data15MB); err != nil {
		t.Fatalf("user20 WriteFile(15mb) failed: %v", err)
	}

	rootInode20, _ := fs.GetInode(rootFID20)
	if rootInode20.Size != 15*1024*1024 {
		t.Fatalf("expected user20 storage to be 15 MB, got %d bytes", rootInode20.Size)
	}

	// 8. User 20 attempts to write another 6 MB -> Total would be 21 MB > 20 MB quota -> MUST FAIL
	createResp20Extra, err := handler.CreateFile(ctx, &pb.CreateFileRequest{
		Name:     "extra6mb.dat",
		RootUser: "user20",
		Fid:      reg2.UserRootFid,
		Type:     pb.InodeType_FILE,
		Size:     6 * 1024 * 1024,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if createResp20Extra.Success {
		t.Fatalf("expected user20 CreateFile(6mb) to be rejected when 5 MB remains")
	}

	// 9. Admin lowers User 20 quota below current usage (from 20 MB down to 10 MB while used is 15 MB)
	setLowerResp, err := handler.SetQuota(ctx, &pb.SetQuotaRequest{
		Username:   "user20",
		QuotaBytes: 10 * 1024 * 1024,
	})
	if err != nil || !setLowerResp.Success {
		t.Fatalf("SetQuota to lower value failed: %v", err)
	}

	// Verify that user20 cannot create new files even with size 0
	blockedCreate20, err := handler.CreateFile(ctx, &pb.CreateFileRequest{
		Name:     "should-be-blocked.txt",
		RootUser: "user20",
		Fid:      reg2.UserRootFid,
		Type:     pb.InodeType_FILE,
	})
	if err != nil {
		t.Fatalf("unexpected RPC error: %v", err)
	}
	if blockedCreate20.Success {
		t.Fatalf("expected CreateFile to be blocked when quota is reduced below current usage")
	}

	// ==========================================
	// Test Scenario C: Telemetry / Metrics Verification
	// ==========================================

	metrics := fs.CollectMetrics()
	if metrics.PerUserQuota["user10"] != 15*1024*1024 {
		t.Errorf("expected user10 quota in metrics to be 15 MB, got %d", metrics.PerUserQuota["user10"])
	}
	if metrics.PerUserQuota["user20"] != 10*1024*1024 {
		t.Errorf("expected user20 quota in metrics to be 10 MB, got %d", metrics.PerUserQuota["user20"])
	}
}

// TestScrapPartialUploadOnQuotaExceeded tests that when a multi-chunk upload
// exceeds quota mid-stream, the partial file is scrapped (deleted) and user storage is restored.
func TestScrapPartialUploadOnQuotaExceeded(t *testing.T) {
	fs := newTestFileServer(t)
	handler := NewGRPCHandler(fs)
	ctx := context.Background()

	// 10 MB quota
	const quota = uint64(10 * 1024 * 1024)
	_ = fs.SetUserQuota("alice", quota)

	reg, _ := handler.RegisterClient(ctx, &pb.RegisterClientRequest{
		Username: "alice", RootUser: "alice", RootPath: "alice",
	})
	rootFID := domain.FIDFromProto(reg.UserRootFid)

	// Upload base file: 9 MB -> Leaves 1 MB free space
	data9MB := make([]byte, 9*1024*1024)
	cResp, err := handler.CreateFile(ctx, &pb.CreateFileRequest{
		Name: "base9mb.dat", RootUser: "alice", Fid: reg.UserRootFid, Type: pb.InodeType_FILE, Size: 9 * 1024 * 1024,
	})
	if err != nil || !cResp.Success {
		t.Fatalf("base create failed: %+v", cResp)
	}
	if err := fs.WriteFile(rootFID, "base9mb.dat", 0, data9MB); err != nil {
		t.Fatalf("base write failed: %v", err)
	}

	rootInode, _ := fs.GetInode(rootFID)
	if rootInode.Size != 9*1024*1024 {
		t.Fatalf("expected 9 MB, got %d", rootInode.Size)
	}

	// Now user starts uploading a new file without known size (e.g. streaming chunks)
	// Chunk size = 512 KB
	chunk512KB := make([]byte, 512*1024)
	cResp2, err := handler.CreateFile(ctx, &pb.CreateFileRequest{
		Name: "stream_file.dat", RootUser: "alice", Fid: reg.UserRootFid, Type: pb.InodeType_FILE, Size: 0,
	})
	if err != nil || !cResp2.Success {
		t.Fatalf("file create failed: %+v", cResp2)
	}

	// Chunk 1 (offset 0): 512 KB -> Total = 9.5 MB <= 10 MB -> SUCCEEDS
	if err := fs.WriteFile(rootFID, "stream_file.dat", 0, chunk512KB); err != nil {
		t.Fatalf("chunk 1 write failed: %v", err)
	}

	// Chunk 2 (offset 512KB): 512 KB -> Total = 10.0 MB <= 10 MB -> SUCCEEDS
	if err := fs.WriteFile(rootFID, "stream_file.dat", 512*1024, chunk512KB); err != nil {
		t.Fatalf("chunk 2 write failed: %v", err)
	}

	// Chunk 3 (offset 1024KB): 512 KB -> Total = 10.5 MB > 10 MB -> FAILS with quota exceeded
	chunk3Err := fs.WriteFile(rootFID, "stream_file.dat", 1024*1024, chunk512KB)
	if chunk3Err == nil {
		t.Fatalf("expected chunk 3 to fail when quota is reached")
	}

	// When chunk write fails, handler scraps the incomplete file
	handler.cleanupFailedUpload(rootFID, "stream_file.dat", "alice")

	// Verify 1: The partially uploaded file is deleted from fs.inodes
	rootInodeAfter, _ := fs.GetInode(rootFID)
	child, err := fs.GetChildInodeByName(rootInodeAfter, "stream_file.dat")
	if err == nil && child != nil {
		t.Errorf("partially uploaded file should have been deleted/scrapped, but still exists")
	}

	// Verify 2: Root inode storage has rolled back from 10.0 MB to 9.0 MB!
	if rootInodeAfter.Size != 9*1024*1024 {
		t.Errorf("expected storage to be rolled back to 9 MB (9437184 bytes), but got %d bytes", rootInodeAfter.Size)
	}
}

// TestDeleteFileFreesStorageQuota tests that deleting files deducts their size
// up to root, freeing storage quota for future uploads.
func TestDeleteFileFreesStorageQuota(t *testing.T) {
	fs := newTestFileServer(t)
	handler := NewGRPCHandler(fs)
	ctx := context.Background()

	const quota = uint64(10 * 1024 * 1024) // 10 MB
	_ = fs.SetUserQuota("bob", quota)

	reg, _ := handler.RegisterClient(ctx, &pb.RegisterClientRequest{
		Username: "bob", RootUser: "bob", RootPath: "bob",
	})
	rootFID := domain.FIDFromProto(reg.UserRootFid)

	// Upload 8 MB file
	data8MB := make([]byte, 8*1024*1024)
	cResp, _ := handler.CreateFile(ctx, &pb.CreateFileRequest{
		Name: "doc8mb.dat", RootUser: "bob", Fid: reg.UserRootFid, Type: pb.InodeType_FILE, Size: 8 * 1024 * 1024,
	})
	_ = fs.WriteFile(rootFID, "doc8mb.dat", 0, data8MB)

	rootInode, _ := fs.GetInode(rootFID)
	if rootInode.Size != 8*1024*1024 {
		t.Fatalf("expected 8 MB, got %d", rootInode.Size)
	}

	// Now try to upload another 5 MB file -> 8 + 5 = 13 MB > 10 MB quota -> MUST FAIL
	failResp, err := handler.CreateFile(ctx, &pb.CreateFileRequest{
		Name: "doc5mb.dat", RootUser: "bob", Fid: reg.UserRootFid, Type: pb.InodeType_FILE, Size: 5 * 1024 * 1024,
	})
	if err != nil || failResp.Success {
		t.Fatalf("expected 5 MB file creation to fail because 8 + 5 > 10: %+v", failResp)
	}

	// Delete the 8 MB file
	docFID := domain.FIDFromProto(cResp.Fid)
	delErr := fs.DeleteFile(docFID, "bob", false)
	if delErr != nil {
		t.Fatalf("DeleteFile failed: %v", delErr)
	}

	// Verify root inode size dropped back to 0 bytes
	rootAfterDelete, _ := fs.GetInode(rootFID)
	if rootAfterDelete.Size != 0 {
		t.Errorf("expected storage to drop to 0 bytes after deleting 8 MB file, got %d", rootAfterDelete.Size)
	}

	// Now upload the 5 MB file -> MUST NOW SUCCEED because space was freed!
	successResp, err := handler.CreateFile(ctx, &pb.CreateFileRequest{
		Name: "doc5mb.dat", RootUser: "bob", Fid: reg.UserRootFid, Type: pb.InodeType_FILE, Size: 5 * 1024 * 1024,
	})
	if err != nil || !successResp.Success {
		t.Fatalf("expected 5 MB file creation to succeed after space freed: %+v, err=%v", successResp, err)
	}

	data5MB := make([]byte, 5*1024*1024)
	if err := fs.WriteFile(rootFID, "doc5mb.dat", 0, data5MB); err != nil {
		t.Fatalf("expected 5 MB write to succeed: %v", err)
	}

	rootFinal, _ := fs.GetInode(rootFID)
	if rootFinal.Size != 5*1024*1024 {
		t.Errorf("expected final storage to be 5 MB, got %d", rootFinal.Size)
	}
}
