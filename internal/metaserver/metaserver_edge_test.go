package metaserver

import (
	"context"
	"testing"
	"time"

	pb "github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/api/metaserver"
	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/domain"
)

func TestGetRootsFailsWithoutHealthyFileServer(t *testing.T) {
	ms := newTestMetaServer(t)
	h := NewGRPCHandler(ms)

	resp, err := h.GetRoots(context.Background(), &pb.GetRootsRequest{Username: "alice"})
	if err != nil {
		t.Fatalf("GetRoots returned RPC error: %v", err)
	}
	if resp.Success {
		t.Fatalf("expected GetRoots to fail when no healthy file server exists")
	}
	if resp.Error == "" {
		t.Fatalf("expected GetRoots to include a failure reason")
	}
}

func TestNavigateValidationAndUnavailableRoot(t *testing.T) {
	ms := newTestMetaServer(t)
	h := NewGRPCHandler(ms)
	now := time.Now().Unix()

	ms.fileservers[0] = &domain.FileServerInfo{
		Address:           "127.0.0.1:5001",
		UserCount:         1,
		LastHeartbeatUnix: now,
		Status:            domain.FileServerStatusStale,
	}
	ms.users["alice"] = 0
	ms.users["bob"] = 0

	missingFields, _ := h.Navigate(context.Background(), &pb.NavigateRequest{Username: "", RootUser: ""})
	if missingFields.Success {
		t.Fatalf("expected missing-fields Navigate to fail")
	}

	unavailable, _ := h.Navigate(context.Background(), &pb.NavigateRequest{Username: "bob", RootUser: "alice"})
	if unavailable.Success {
		t.Fatalf("expected Navigate to unavailable root to fail")
	}
	if unavailable.Error == "" {
		t.Fatalf("expected unavailable Navigate to include error message")
	}
}

func TestRegisterFileServerRejectsEmptyAddress(t *testing.T) {
	ms := newTestMetaServer(t)
	h := NewGRPCHandler(ms)

	resp, err := h.RegisterFileServer(context.Background(), &pb.RegisterFileServerRequest{
		Address: "",
		Users:   []string{"alice"},
	})
	if err != nil {
		t.Fatalf("RegisterFileServer returned RPC error: %v", err)
	}
	if resp.Success {
		t.Fatalf("expected empty-address registration to fail")
	}
}

func TestStartHeartbeatMonitorMarksServerStale(t *testing.T) {
	ms := newTestMetaServer(t)
	now := time.Now().Unix()

	ms.fileservers[0] = &domain.FileServerInfo{
		Address:           "127.0.0.1:5001",
		UserCount:         0,
		LastHeartbeatUnix: now - 5,
		Status:            domain.FileServerStatusHealthy,
	}

	ms.SetHeartbeatConfig(1*time.Second, 50*time.Millisecond)
	stop := ms.StartHeartbeatMonitor()
	defer stop()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		ms.mu.RLock()
		status := ms.fileservers[0].Status
		ms.mu.RUnlock()
		if status == domain.FileServerStatusStale {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("heartbeat monitor did not mark stale before deadline")
}
