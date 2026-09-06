package metaserver

import (
	"context"
	"testing"
	"time"

	pb "github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/api/metaserver"
	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/domain"
)

// TestHeartbeatRecoveryFromStale verifies that a fileserver marked stale
// is transitioned back to healthy when it sends a heartbeat.
func TestHeartbeatRecoveryFromStale(t *testing.T) {
	ms := newTestMetaServer(t)
	h := NewGRPCHandler(ms)
	now := time.Now().Unix()

	// 1. Setup a stale fileserver
	addr := "127.0.0.1:50052"
	ms.fileservers[0] = &domain.FileServerInfo{
		Address:           addr,
		UserCount:         0,
		LastHeartbeatUnix: now - 100, // 100s ago
		Status:            domain.FileServerStatusStale,
	}

	// 2. Fileserver sends Heartbeat
	hbResp, err := h.Heartbeat(context.Background(), &pb.HeartbeatRequest{
		Address: addr,
	})
	if err != nil {
		t.Fatalf("Heartbeat returned RPC error: %v", err)
	}
	if !hbResp.Success {
		t.Fatalf("expected Heartbeat to succeed: %v", hbResp.Error)
	}

	// 3. Verify fileserver recovered to Healthy
	ms.mu.RLock()
	fsInfo := ms.fileservers[0]
	status := fsInfo.Status
	lastHB := fsInfo.LastHeartbeatUnix
	ms.mu.RUnlock()

	if status != domain.FileServerStatusHealthy {
		t.Errorf("expected status to recover to Healthy, got: %s", status)
	}
	if lastHB < now {
		t.Errorf("expected LastHeartbeatUnix to be updated to >= %d, got %d", now, lastHB)
	}
}

// TestMetaserverLeastLoadedAssignment verifies that when a new user requests roots,
// the metaserver assigns them to the healthy fileserver with the lowest user count.
func TestMetaserverLeastLoadedAssignment(t *testing.T) {
	ms := newTestMetaServer(t)
	h := NewGRPCHandler(ms)
	now := time.Now().Unix()

	// FS 0: heavily loaded (5 users)
	ms.fileservers[0] = &domain.FileServerInfo{
		Address:           "127.0.0.1:50052",
		UserCount:         5,
		LastHeartbeatUnix: now,
		Status:            domain.FileServerStatusHealthy,
	}

	// FS 1: lightly loaded (1 user)
	ms.fileservers[1] = &domain.FileServerInfo{
		Address:           "127.0.0.1:50053",
		UserCount:         1,
		LastHeartbeatUnix: now,
		Status:            domain.FileServerStatusHealthy,
	}
	ms.nextFsID = 2

	// New user "charlie" requests roots -> should be assigned to FS 1
	resp, err := h.GetRoots(context.Background(), &pb.GetRootsRequest{
		Username: "charlie",
	})
	if err != nil {
		t.Fatalf("GetRoots RPC error: %v", err)
	}
	if !resp.Success {
		t.Fatalf("GetRoots failed: %v", resp.Error)
	}

	// Verify assigned to FS 1 in ms.users
	ms.mu.RLock()
	assignedFS, exists := ms.users["charlie"]
	fs1Count := ms.fileservers[1].UserCount
	ms.mu.RUnlock()

	if !exists {
		t.Fatalf("expected user charlie to be registered in ms.users")
	}
	if assignedFS != 1 {
		t.Errorf("expected new user to be assigned to least-loaded FS 1, got FS %d", assignedFS)
	}
	if fs1Count != 2 {
		t.Errorf("expected FS 1 UserCount to increment to 2, got %d", fs1Count)
	}
}

// TestHeartbeatUnknownFileserver verifies that heartbeats for unknown FS addresses fail cleanly.
func TestHeartbeatUnknownFileserver(t *testing.T) {
	ms := newTestMetaServer(t)
	h := NewGRPCHandler(ms)

	resp, err := h.Heartbeat(context.Background(), &pb.HeartbeatRequest{
		Address: "192.168.99.99:9999",
	})
	if err != nil {
		t.Fatalf("unexpected RPC error: %v", err)
	}
	if resp.Success {
		t.Errorf("expected heartbeat for unknown fileserver to fail")
	}
}
