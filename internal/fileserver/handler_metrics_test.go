package fileserver

import (
	"context"
	"testing"

	pb "github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/api/fileserver"
)

// TestGRPCHandlerMetricsTracking verifies that successful RPC operations via GRPCHandler
// record operation metrics, byte transfers, and durations in opMetrics.
func TestGRPCHandlerMetricsTracking(t *testing.T) {
	h, fs := setupHandlerTest(t)
	ctx := context.Background()

	regResp, err := h.RegisterClient(ctx, &pb.RegisterClientRequest{
		Username: "alice",
		RootUser: "alice",
		RootPath: "alice",
	})
	if err != nil || !regResp.Success {
		t.Fatalf("RegisterClient failed: %v", err)
	}
	rootFID := regResp.UserRootFid

	initialSnap := fs.OpMetricsSnapshot()

	// 1. Create a file (write operation)
	createResp, err := h.CreateFile(ctx, &pb.CreateFileRequest{
		Fid:      rootFID,
		Name:     "telemetry_test.txt",
		RootUser: "alice",
		Type:     pb.InodeType_FILE,
	})
	if err != nil || !createResp.Success {
		t.Fatalf("CreateFile failed: %v", err)
	}

	snapAfterCreate := fs.OpMetricsSnapshot()
	if snapAfterCreate.WriteOpsTotal <= initialSnap.WriteOpsTotal {
		t.Errorf("expected WriteOpsTotal to increment after CreateFile: before=%d, after=%d",
			initialSnap.WriteOpsTotal, snapAfterCreate.WriteOpsTotal)
	}

	// 2. Write data to the file (50 bytes)
	testData := []byte("12345678901234567890123456789012345678901234567890") // 50 bytes
	writeResp, err := h.WriteFile(ctx, &pb.WriteFileRequest{
		ParentFid: rootFID,
		Name:      "telemetry_test.txt",
		Offset:    0,
		Data:      testData,
	})
	if err != nil || !writeResp.Success {
		t.Fatalf("WriteFile failed: %v", err)
	}

	snapAfterWrite := fs.OpMetricsSnapshot()
	if snapAfterWrite.WriteOpsTotal <= snapAfterCreate.WriteOpsTotal {
		t.Errorf("expected WriteOpsTotal to increment after WriteFile")
	}
	if snapAfterWrite.BytesWrittenTotal < snapAfterCreate.BytesWrittenTotal+50 {
		t.Errorf("expected BytesWrittenTotal to increase by at least 50 bytes, got before=%d, after=%d",
			snapAfterCreate.BytesWrittenTotal, snapAfterWrite.BytesWrittenTotal)
	}

	// 3. Read data from the file (50 bytes)
	readResp, err := h.ReadFile(ctx, &pb.ReadFileRequest{
		ParentFid: rootFID,
		Name:      "telemetry_test.txt",
	})
	if err != nil || !readResp.Success {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if len(readResp.Data) != 50 {
		t.Fatalf("expected 50 bytes read, got %d", len(readResp.Data))
	}

	snapAfterRead := fs.OpMetricsSnapshot()
	if snapAfterRead.ReadOpsTotal <= snapAfterWrite.ReadOpsTotal {
		t.Errorf("expected ReadOpsTotal to increment after ReadFile: before=%d, after=%d",
			snapAfterWrite.ReadOpsTotal, snapAfterRead.ReadOpsTotal)
	}
	if snapAfterRead.BytesReadTotal < snapAfterWrite.BytesReadTotal+50 {
		t.Errorf("expected BytesReadTotal to increase by at least 50 bytes, got before=%d, after=%d",
			snapAfterWrite.BytesReadTotal, snapAfterRead.BytesReadTotal)
	}

	// 4. GetAttr and ListDir (read operations)
	_, _ = h.GetAttr(ctx, &pb.GetAttrRequest{
		Fid: createResp.Fid,
	})
	_, _ = h.ListDir(ctx, &pb.ListDirRequest{
		Fid: rootFID,
	})

	snapAfterAttrs := fs.OpMetricsSnapshot()
	if snapAfterAttrs.ReadOpsTotal < snapAfterRead.ReadOpsTotal+2 {
		t.Errorf("expected ReadOpsTotal to increment for GetAttr and ListDir: before=%d, after=%d",
			snapAfterRead.ReadOpsTotal, snapAfterAttrs.ReadOpsTotal)
	}

	// 5. Delete file (write operation)
	delResp, err := h.DeleteFile(ctx, &pb.DeleteFileRequest{
		Fid:       createResp.Fid,
		RootUser:  "alice",
		Recursive: false,
	})
	if err != nil || !delResp.Success {
		t.Fatalf("DeleteFile failed: %v", err)
	}

	snapAfterDel := fs.OpMetricsSnapshot()
	if snapAfterDel.WriteOpsTotal <= snapAfterAttrs.WriteOpsTotal {
		t.Errorf("expected WriteOpsTotal to increment after DeleteFile")
	}

	// Verify latency percentiles were recorded and are >= 0
	if snapAfterDel.OpLatencyWriteMsP50 < 0 || snapAfterDel.OpLatencyReadMsP50 < 0 {
		t.Errorf("expected non-negative latencies, got writeP50=%v, readP50=%v",
			snapAfterDel.OpLatencyWriteMsP50, snapAfterDel.OpLatencyReadMsP50)
	}
}

// TestGRPCHandlerErrorMetricsTracking verifies that failed operations increment
// error and failed write counters.
func TestGRPCHandlerErrorMetricsTracking(t *testing.T) {
	h, fs := setupHandlerTest(t)
	ctx := context.Background()

	beforeSnap := fs.OpMetricsSnapshot()

	// Attempt to create a file in a non-existent parent FID
	badFID := &pb.FID{FileServerId: "fs-bad", InodeId: 999999, GenerationNumber: 99}
	createResp, err := h.CreateFile(ctx, &pb.CreateFileRequest{
		Fid:      badFID,
		Name:     "should_fail.txt",
		RootUser: "alice",
		Type:     pb.InodeType_FILE,
	})
	if err != nil {
		t.Fatalf("unexpected RPC error: %v", err)
	}
	if createResp.Success {
		t.Fatalf("expected CreateFile in bad parent FID to fail")
	}

	afterSnap := fs.OpMetricsSnapshot()
	if afterSnap.ErrorsTotal <= beforeSnap.ErrorsTotal {
		t.Errorf("expected ErrorsTotal to increment on failed CreateFile: before=%d, after=%d",
			beforeSnap.ErrorsTotal, afterSnap.ErrorsTotal)
	}
	if afterSnap.FailedWritesTotal <= beforeSnap.FailedWritesTotal {
		t.Errorf("expected FailedWritesTotal to increment on failed CreateFile: before=%d, after=%d",
			beforeSnap.FailedWritesTotal, afterSnap.FailedWritesTotal)
	}

	// Attempt to read a non-existent file
	readResp, err := h.ReadFile(ctx, &pb.ReadFileRequest{
		ParentFid: badFID,
		Name:      "nonexistent.txt",
	})
	if err != nil {
		t.Fatalf("unexpected RPC error: %v", err)
	}
	if readResp.Success {
		t.Fatalf("expected ReadFile on bad parent FID to fail")
	}

	finalSnap := fs.OpMetricsSnapshot()
	if finalSnap.FailedReadsTotal <= afterSnap.FailedReadsTotal {
		t.Errorf("expected FailedReadsTotal to increment on failed ReadFile: before=%d, after=%d",
			afterSnap.FailedReadsTotal, finalSnap.FailedReadsTotal)
	}
}
