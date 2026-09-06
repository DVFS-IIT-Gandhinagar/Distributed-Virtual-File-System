package fileserver

import (
	"context"
	"io"
	"testing"

	pb "github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/api/fileserver"
	"google.golang.org/grpc/metadata"
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

// mockUploadServer implements pb.FileServer_UploadFileServer for testing streaming uploads
type mockUploadServer struct {
	ctx      context.Context
	requests []*pb.UploadFileRequest
	idx      int
	response *pb.UploadFileResponse
	onRecv   func(idx int)
}

func (m *mockUploadServer) Context() context.Context {
	if m.ctx != nil {
		return m.ctx
	}
	return context.Background()
}
func (m *mockUploadServer) Recv() (*pb.UploadFileRequest, error) {
	if m.onRecv != nil {
		m.onRecv(m.idx)
	}
	if m.idx >= len(m.requests) {
		return nil, io.EOF
	}
	req := m.requests[m.idx]
	m.idx++
	return req, nil
}
func (m *mockUploadServer) SendAndClose(res *pb.UploadFileResponse) error {
	m.response = res
	return nil
}
func (m *mockUploadServer) SetHeader(metadata.MD) error  { return nil }
func (m *mockUploadServer) SendHeader(metadata.MD) error { return nil }
func (m *mockUploadServer) SetTrailer(metadata.MD)       {}
func (m *mockUploadServer) SendMsg(msg any) error        { return nil }
func (m *mockUploadServer) RecvMsg(msg any) error        { return nil }

// mockDownloadServer implements pb.FileServer_DownloadFileServer for testing streaming downloads
type mockDownloadServer struct {
	ctx       context.Context
	responses []*pb.DownloadFileResponse
	onChunk   func(res *pb.DownloadFileResponse)
}

func (m *mockDownloadServer) Context() context.Context {
	if m.ctx != nil {
		return m.ctx
	}
	return context.Background()
}
func (m *mockDownloadServer) Send(res *pb.DownloadFileResponse) error {
	m.responses = append(m.responses, res)
	if m.onChunk != nil {
		m.onChunk(res)
	}
	return nil
}
func (m *mockDownloadServer) SetHeader(metadata.MD) error  { return nil }
func (m *mockDownloadServer) SendHeader(metadata.MD) error { return nil }
func (m *mockDownloadServer) SetTrailer(metadata.MD)       {}
func (m *mockDownloadServer) SendMsg(msg any) error        { return nil }
func (m *mockDownloadServer) RecvMsg(msg any) error        { return nil }

// TestGRPCHandlerStreamingMetricsTracking verifies that UploadFile and DownloadFile
// update BytesWrittenTotal and BytesReadTotal in real time as chunks stream across the wire.
func TestGRPCHandlerStreamingMetricsTracking(t *testing.T) {
	h, fs := setupHandlerTest(t)
	ctx := context.Background()

	regResp, err := h.RegisterClient(ctx, &pb.RegisterClientRequest{
		Username: "streamer",
		RootUser: "streamer",
		RootPath: "streamer",
	})
	if err != nil || !regResp.Success {
		t.Fatalf("RegisterClient failed: %v", err)
	}
	rootFID := regResp.UserRootFid

	fileName := "streaming_sample.bin"
	chunk1 := []byte("AAAAABBBBBCCCCCDDDDD")                               // 20 bytes
	chunk2 := []byte("EEEEEFFFFFGGGGGHHHHH")                               // 20 bytes
	chunk3 := []byte("IIIIIJJJJJKKKKKLLLLL")                               // 20 bytes
	totalExpectedBytes := uint64(len(chunk1) + len(chunk2) + len(chunk3)) // 60 bytes

	// 1. Create file first
	createResp, err := h.CreateFile(ctx, &pb.CreateFileRequest{
		Fid:      rootFID,
		Name:     fileName,
		RootUser: "streamer",
		Type:     pb.InodeType_FILE,
		Size:     totalExpectedBytes,
	})
	if err != nil || !createResp.Success {
		t.Fatalf("CreateFile failed: %v", err)
	}

	snapBeforeUpload := fs.OpMetricsSnapshot()

	// 2. Prepare streaming upload with 3 chunks
	uploadStream := &mockUploadServer{
		requests: []*pb.UploadFileRequest{
			{ParentFid: rootFID, Name: fileName, User: "streamer", Offset: 0, Chunk: chunk1},
			{ParentFid: rootFID, Name: fileName, User: "streamer", Offset: 20, Chunk: chunk2},
			{ParentFid: rootFID, Name: fileName, User: "streamer", Offset: 40, Chunk: chunk3},
		},
	}

	// Capture live byte counter during streaming.
	// When idx > 0, chunk (idx-1) has been received and written to disk.
	var bytesObservedDuringStreaming []uint64
	uploadStream.onRecv = func(idx int) {
		snap := fs.OpMetricsSnapshot()
		bytesObservedDuringStreaming = append(bytesObservedDuringStreaming, snap.BytesWrittenTotal)
	}

	err = h.UploadFile(uploadStream)
	if err != nil {
		t.Fatalf("UploadFile failed: %v", err)
	}
	if uploadStream.response == nil || !uploadStream.response.Success {
		t.Fatalf("UploadFile response was not successful: %+v", uploadStream.response)
	}

	snapAfterUpload := fs.OpMetricsSnapshot()

	// Verify write bytes increased by 60
	diffWritten := snapAfterUpload.BytesWrittenTotal - snapBeforeUpload.BytesWrittenTotal
	if diffWritten != totalExpectedBytes {
		t.Errorf("expected %d bytes written, got %d", totalExpectedBytes, diffWritten)
	}

	// Verify writeOpsTotal incremented by 1 (the single upload stream operation)
	if snapAfterUpload.WriteOpsTotal != snapBeforeUpload.WriteOpsTotal+1 {
		t.Errorf("expected WriteOpsTotal to increment by 1: before=%d, after=%d",
			snapBeforeUpload.WriteOpsTotal, snapAfterUpload.WriteOpsTotal)
	}

	// Verify real-time intermediate observations showed incremental bytes
	// idx=0: 0 bytes written, idx=1: 20 bytes written, idx=2: 40 bytes written, idx=3 (EOF): 60 bytes written
	if len(bytesObservedDuringStreaming) != 4 {
		t.Fatalf("expected 4 intermediate streaming snapshots (0, 1, 2, EOF), got %d", len(bytesObservedDuringStreaming))
	}
	if bytesObservedDuringStreaming[1]-snapBeforeUpload.BytesWrittenTotal != 20 {
		t.Errorf("expected 20 bytes after chunk 1, got %d", bytesObservedDuringStreaming[1]-snapBeforeUpload.BytesWrittenTotal)
	}
	if bytesObservedDuringStreaming[2]-snapBeforeUpload.BytesWrittenTotal != 40 {
		t.Errorf("expected 40 bytes after chunk 2, got %d", bytesObservedDuringStreaming[2]-snapBeforeUpload.BytesWrittenTotal)
	}
	if bytesObservedDuringStreaming[3]-snapBeforeUpload.BytesWrittenTotal != 60 {
		t.Errorf("expected 60 bytes after chunk 3, got %d", bytesObservedDuringStreaming[3]-snapBeforeUpload.BytesWrittenTotal)
	}

	// 3. Test DownloadFile streaming
	snapBeforeDownload := fs.OpMetricsSnapshot()
	var downloadBytesObserved []uint64
	downloadStream := &mockDownloadServer{
		onChunk: func(res *pb.DownloadFileResponse) {
			snap := fs.OpMetricsSnapshot()
			downloadBytesObserved = append(downloadBytesObserved, snap.BytesReadTotal)
		},
	}

	err = h.DownloadFile(&pb.DownloadFileRequest{
		ParentFid: rootFID,
		Name:      fileName,
	}, downloadStream)
	if err != nil {
		t.Fatalf("DownloadFile failed: %v", err)
	}

	snapAfterDownload := fs.OpMetricsSnapshot()

	diffRead := snapAfterDownload.BytesReadTotal - snapBeforeDownload.BytesReadTotal
	if diffRead != totalExpectedBytes {
		t.Errorf("expected %d bytes read, got %d", totalExpectedBytes, diffRead)
	}

	if snapAfterDownload.ReadOpsTotal != snapBeforeDownload.ReadOpsTotal+1 {
		t.Errorf("expected ReadOpsTotal to increment by 1: before=%d, after=%d",
			snapBeforeDownload.ReadOpsTotal, snapAfterDownload.ReadOpsTotal)
	}

	if len(downloadBytesObserved) == 0 {
		t.Errorf("expected at least 1 chunk observed during download")
	}
}
