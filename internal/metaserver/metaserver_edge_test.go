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

	fsID := seedFileServer(t, ms, "fs1", "127.0.0.1:5001", 1, now, domain.FileServerStatusStale)
	ms.users["alice"] = fsID
	ms.users["bob"] = fsID

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

	fsID := seedFileServer(t, ms, "fs1", "127.0.0.1:5001", 0, now-5, domain.FileServerStatusHealthy)

	ms.SetHeartbeatConfig(1*time.Second, 50*time.Millisecond)
	stop := ms.StartHeartbeatMonitor()
	defer stop()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		ms.mu.RLock()
		status := ms.fileservers[fsID].Status
		ms.mu.RUnlock()
		if status == domain.FileServerStatusStale {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("heartbeat monitor did not mark stale before deadline")
}
