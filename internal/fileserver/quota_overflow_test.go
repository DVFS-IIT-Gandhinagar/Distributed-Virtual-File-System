package fileserver

import (
	"context"
	"math"
	"strings"
	"testing"

	pb "github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/api/fileserver"
	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/domain"
)

// TestQuotaArithmeticUint64Overflow tests if requesting additionalBytes close to math.MaxUint64
// causes integer wrap-around that bypasses storage quota enforcement.
func TestQuotaArithmeticUint64Overflow(t *testing.T) {
	fs := newTestFileServer(t)

	rootFID, err := fs.GetUserRoot("alice", "alice")
	if err != nil {
		t.Fatalf("GetUserRoot failed: %v", err)
	}

	rootInode, err := fs.GetInode(rootFID)
	if err != nil {
		t.Fatalf("GetInode failed: %v", err)
	}
	rootInode.Size = 1024 // 1 KiB existing usage

	// Quota is defaultStorageQuota (1 GiB)
	// If additionalBytes wraps around uint64:
	// 1024 + (math.MaxUint64 - 512) = 511 (which is < 1 GiB!)
	hugeAdditional := math.MaxUint64 - uint64(512)

	err = fs.checkStorageQuotaWithAdditional("alice", hugeAdditional)
	if err == nil || !strings.Contains(err.Error(), "storage quota exceeded") {
		t.Fatalf("CRITICAL: integer overflow in quota arithmetic! Request of %d bytes bypassed quota check because of uint64 wrap-around (err: %v)", hugeAdditional, err)
	}

	// Test math.MaxUint64 directly
	err = fs.checkStorageQuotaWithAdditional("alice", math.MaxUint64)
	if err == nil || !strings.Contains(err.Error(), "storage quota exceeded") {
		t.Fatalf("CRITICAL: math.MaxUint64 bypassed quota check due to wrap-around (err: %v)", err)
	}
}

// TestWriteFileOffsetLengthOverflow tests whether write requests with offset + len(data) >= math.MaxUint64
// are safely rejected instead of wrapping around.
func TestWriteFileOffsetLengthOverflow(t *testing.T) {
	fs := newTestFileServer(t)

	rootFID, err := fs.GetUserRoot("alice", "alice")
	if err != nil {
		t.Fatalf("GetUserRoot failed: %v", err)
	}

	fileFID, err := fs.CreateFile(rootFID, "overflow_test.bin", "alice", domain.InodeTypeFile)
	if err != nil {
		t.Fatalf("CreateFile failed: %v", err)
	}

	// Offset near MaxUint64
	offset := math.MaxUint64 - uint64(10)
	data := []byte("payload that causes uint64 overflow")

	err = fs.WriteFile(rootFID, "overflow_test.bin", offset, data)
	if err == nil {
		t.Fatalf("expected WriteFile with overflowing offset+length to return error, but got nil")
	}
	_ = fileFID
}

// TestSetQuotaZeroAndInvalid tests boundary inputs for SetUserQuota via gRPC and direct calls.
func TestSetQuotaZeroAndInvalid(t *testing.T) {
	fs := newTestFileServer(t)
	handler := NewGRPCHandler(fs)
	ctx := context.Background()

	// 1. Zero quota direct
	if err := fs.SetUserQuota("alice", 0); err == nil {
		t.Errorf("expected SetUserQuota with 0 bytes to fail")
	}

	// 2. Empty username direct
	if err := fs.SetUserQuota("", 1024); err == nil {
		t.Errorf("expected SetUserQuota with empty username to fail")
	}

	// 3. gRPC with zero quota
	resp, err := handler.SetQuota(ctx, &pb.SetQuotaRequest{
		Username:   "alice",
		QuotaBytes: 0,
	})
	if err != nil {
		t.Fatalf("unexpected RPC error: %v", err)
	}
	if resp.Success {
		t.Errorf("expected SetQuota RPC with 0 quota to fail")
	}

	// 4. gRPC with nil request
	respNil, err := handler.SetQuota(ctx, nil)
	if err != nil {
		t.Fatalf("unexpected RPC error: %v", err)
	}
	if respNil.Success {
		t.Errorf("expected SetQuota RPC with nil request to fail")
	}
}

// TestQuotaBelowUsageBlocksNewWrites tests that an admin can lower a user's quota below
// their current usage, which safely blocks new writes while preserving existing data.
func TestQuotaBelowUsageBlocksNewWrites(t *testing.T) {
	fs := newTestFileServer(t)

	rootFID, err := fs.GetUserRoot("alice", "alice")
	if err != nil {
		t.Fatalf("GetUserRoot failed: %v", err)
	}

	// Write 500 bytes
	data := make([]byte, 500)
	for i := range data {
		data[i] = byte(i % 256)
	}
	_, err = fs.CreateFile(rootFID, "existing.bin", "alice", domain.InodeTypeFile)
	if err != nil {
		t.Fatalf("CreateFile failed: %v", err)
	}
	if err := fs.WriteFile(rootFID, "existing.bin", 0, data); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	// Set quota below current usage (300 bytes)
	if err := fs.SetUserQuota("alice", 300); err != nil {
		t.Fatalf("SetUserQuota to 300 failed: %v", err)
	}

	// Existing file should still be readable
	readData, err := fs.ReadFile(rootFID, "existing.bin", 0, 500)
	if err != nil {
		t.Fatalf("ReadFile of existing data failed: %v", err)
	}
	if len(readData) != 500 {
		t.Fatalf("expected 500 bytes, got %d", len(readData))
	}

	// Attempting to append or create new file must fail with quota error
	err = fs.WriteFile(rootFID, "existing.bin", 500, []byte("more"))
	if err == nil {
		t.Fatalf("expected WriteFile to fail when usage exceeds quota")
	}

	err = fs.checkStorageQuotaWithAdditional("alice", 1)
	if err == nil {
		t.Fatalf("expected checkStorageQuotaWithAdditional to fail when usage exceeds quota")
	}
}
