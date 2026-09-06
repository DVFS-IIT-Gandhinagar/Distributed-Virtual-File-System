package client

import (
	"testing"
	"time"

	pb "github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/api/fileserver"
	"google.golang.org/protobuf/proto"
)

func TestUnregisterClientProtoSerialization(t *testing.T) {
	req := &pb.UnregisterClientRequest{
		ClientId: "test-client-123",
		Username: "alice",
	}

	// 1. Proto size calculation (which previously panicked when ProtoReflect was nil)
	size := proto.Size(req)
	if size <= 0 {
		t.Fatalf("expected positive proto size for UnregisterClientRequest, got %d", size)
	}

	// 2. Proto Marshal
	data, err := proto.Marshal(req)
	if err != nil {
		t.Fatalf("failed to marshal UnregisterClientRequest: %v", err)
	}

	// 3. Proto Unmarshal
	var unmarshaledReq pb.UnregisterClientRequest
	if err := proto.Unmarshal(data, &unmarshaledReq); err != nil {
		t.Fatalf("failed to unmarshal UnregisterClientRequest: %v", err)
	}

	if unmarshaledReq.GetClientId() != req.ClientId {
		t.Errorf("expected ClientId %s, got %s", req.ClientId, unmarshaledReq.GetClientId())
	}
	if unmarshaledReq.GetUsername() != req.Username {
		t.Errorf("expected Username %s, got %s", req.Username, unmarshaledReq.GetUsername())
	}

	// 4. Response serialization
	resp := &pb.UnregisterClientResponse{
		Success: true,
		Error:   "",
	}
	respSize := proto.Size(resp)
	if respSize <= 0 {
		t.Fatalf("expected positive proto size for UnregisterClientResponse, got %d", respSize)
	}

	respData, err := proto.Marshal(resp)
	if err != nil {
		t.Fatalf("failed to marshal UnregisterClientResponse: %v", err)
	}

	var unmarshaledResp pb.UnregisterClientResponse
	if err := proto.Unmarshal(respData, &unmarshaledResp); err != nil {
		t.Fatalf("failed to unmarshal UnregisterClientResponse: %v", err)
	}
	if !unmarshaledResp.GetSuccess() {
		t.Errorf("expected Success=true, got %v", unmarshaledResp.GetSuccess())
	}
}

func TestClientDisconnect_IdempotentAndNilSafe(t *testing.T) {
	// 1. Nil client Disconnect should not panic
	var nilClient *Client
	nilClient.Disconnect()

	// 2. Disconnected client Disconnect should not panic and be idempotent
	c := &Client{
		username: "testuser",
		clientID: "testclient",
	}
	c.Disconnect()
	c.Disconnect()
}

func TestClientDisconnect_FullGRPCTeardown(t *testing.T) {
	fsAddr, fs, cleanupFS := startTestFileServerGRPC(t, "")
	defer cleanupFS()

	c := NewClient("alice", false, "")
	c.SetRootUser("alice")
	c.SetRootPath("alice", "alice")

	if _, err := c.Connect(fsAddr); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	// Verify session exists
	metrics1 := fs.CollectMetrics()
	if metrics1.ActiveConnections != 1 {
		t.Fatalf("expected 1 active connection, got %d", metrics1.ActiveConnections)
	}

	// Disconnect should unregister cleanly over gRPC without panics
	c.Disconnect()

	// Verify connections and references are cleared on client
	if c.serverConn != nil {
		t.Errorf("expected serverConn to be nil")
	}
	if c.grpcConn != nil {
		t.Errorf("expected grpcConn to be nil")
	}

	// Verify fileserver session is immediately removed
	time.Sleep(50 * time.Millisecond)
	metrics2 := fs.CollectMetrics()
	if metrics2.ActiveConnections != 0 {
		t.Errorf("expected 0 active connections after Disconnect(), got %d", metrics2.ActiveConnections)
	}

	// Double disconnect should be safe
	c.Disconnect()
}
