package metaserver

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	pb "github.com/umangshikarvar/dvfs/api/metaserver"
	"github.com/umangshikarvar/dvfs/internal/domain"
)

func TestMetaServerPersistsAndRecoversState(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "metaserver_state.json")

	ms, err := NewMetaServer(stateFile)
	if err != nil {
		t.Fatalf("NewMetaServer failed: %v", err)
	}

	h := NewGRPCHandler(ms)
	resp, err := h.RegisterFileServer(context.Background(), &pb.RegisterFileServerRequest{
		Address: "127.0.0.1:6001",
		Users:   []string{"alice"},
	})
	if err != nil {
		t.Fatalf("RegisterFileServer returned error: %v", err)
	}
	if !resp.Success {
		t.Fatalf("RegisterFileServer rejected request: %s", resp.Error)
	}

	msRecovered, err := NewMetaServer(stateFile)
	if err != nil {
		t.Fatalf("NewMetaServer(recovered) failed: %v", err)
	}

	if got := len(msRecovered.fileservers); got != 1 {
		t.Fatalf("expected 1 fileserver after recovery, got %d", got)
	}
	if got := len(msRecovered.users); got != 1 {
		t.Fatalf("expected 1 user mapping after recovery, got %d", got)
	}
	if fsID, ok := msRecovered.users["alice"]; !ok {
		t.Fatalf("expected recovered mapping for alice")
	} else if msRecovered.fileservers[fsID] == nil {
		t.Fatalf("expected fileserver for recovered fsID %d", fsID)
	}
}

func TestRegisterFileServerIsIdempotentByAddress(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "metaserver_state.json")

	ms, err := NewMetaServer(stateFile)
	if err != nil {
		t.Fatalf("NewMetaServer failed: %v", err)
	}

	h := NewGRPCHandler(ms)
	_, err = h.RegisterFileServer(context.Background(), &pb.RegisterFileServerRequest{
		Address: "127.0.0.1:6002",
		Users:   []string{"alice"},
	})
	if err != nil {
		t.Fatalf("first RegisterFileServer returned error: %v", err)
	}

	_, err = h.RegisterFileServer(context.Background(), &pb.RegisterFileServerRequest{
		Address: "127.0.0.1:6002",
		Users:   []string{"alice", "bob"},
	})
	if err != nil {
		t.Fatalf("second RegisterFileServer returned error: %v", err)
	}

	if got := len(ms.fileservers); got != 1 {
		t.Fatalf("expected 1 fileserver after registration refresh, got %d", got)
	}
	if got := len(ms.users); got != 2 {
		t.Fatalf("expected 2 users mapped after registration refresh, got %d", got)
	}
}

func TestNavigateFailsGracefullyWithoutAnyFileServer(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "metaserver_state.json")

	ms, err := NewMetaServer(stateFile)
	if err != nil {
		t.Fatalf("NewMetaServer failed: %v", err)
	}

	h := NewGRPCHandler(ms)
	resp, err := h.Navigate(context.Background(), &pb.NavigateRequest{Username: "alice", RootUser: "alice"})
	if err != nil {
		t.Fatalf("Navigate returned unexpected error: %v", err)
	}
	if resp.Success {
		t.Fatalf("expected Navigate to fail when no fileservers are registered")
	}
	if resp.Error == "" {
		t.Fatalf("expected a descriptive error when no fileservers are registered")
	}
}

func TestHeartbeatRefreshesStaleServer(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "metaserver_state.json")

	ms, err := NewMetaServer(stateFile)
	if err != nil {
		t.Fatalf("NewMetaServer failed: %v", err)
	}

	h := NewGRPCHandler(ms)
	_, err = h.RegisterFileServer(context.Background(), &pb.RegisterFileServerRequest{
		Address: "127.0.0.1:7001",
		Users:   []string{"alice"},
	})
	if err != nil {
		t.Fatalf("RegisterFileServer returned error: %v", err)
	}

	ms.mu.Lock()
	fsID, ok := ms.findFileServerByAddressLocked("127.0.0.1:7001")
	if !ok {
		ms.mu.Unlock()
		t.Fatalf("expected fileserver to be present")
	}
	ms.fileservers[fsID].Status = domain.FileServerStatusStale
	ms.fileservers[fsID].LastHeartbeatUnix = time.Now().Add(-time.Minute).Unix()
	ms.mu.Unlock()

	hbResp, err := h.Heartbeat(context.Background(), &pb.HeartbeatRequest{Address: "127.0.0.1:7001"})
	if err != nil {
		t.Fatalf("Heartbeat returned error: %v", err)
	}
	if !hbResp.Success {
		t.Fatalf("Heartbeat failed: %s", hbResp.Error)
	}

	ms.mu.RLock()
	defer ms.mu.RUnlock()
	if ms.fileservers[fsID].Status != domain.FileServerStatusHealthy {
		t.Fatalf("expected status to become healthy, got %s", ms.fileservers[fsID].Status)
	}
}

func TestNavigateFailsWhenRootServerIsStale(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "metaserver_state.json")

	ms, err := NewMetaServer(stateFile)
	if err != nil {
		t.Fatalf("NewMetaServer failed: %v", err)
	}

	ms.SetHeartbeatConfig(2*time.Second, 500*time.Millisecond)
	h := NewGRPCHandler(ms)

	resp1, err := h.RegisterFileServer(context.Background(), &pb.RegisterFileServerRequest{
		Address: "127.0.0.1:7101",
		Users:   []string{"alice"},
	})
	if err != nil {
		t.Fatalf("first register error: %v", err)
	}
	if !resp1.Success {
		t.Fatalf("first register failed: %s", resp1.Error)
	}

	ms.mu.Lock()
	fs1, ok := ms.findFileServerByAddressLocked("127.0.0.1:7101")
	if !ok {
		ms.mu.Unlock()
		t.Fatalf("expected first fileserver to be present")
	}
	ms.fileservers[fs1].Status = domain.FileServerStatusStale
	ms.fileservers[fs1].LastHeartbeatUnix = time.Now().Add(-time.Minute).Unix()
	ms.mu.Unlock()

	navResp, err := h.Navigate(context.Background(), &pb.NavigateRequest{Username: "alice", RootUser: "alice"})
	if err != nil {
		t.Fatalf("Navigate returned error: %v", err)
	}
	if navResp.Success {
		t.Fatalf("expected Navigate to fail when root user's server is stale")
	}
	if navResp.Error == "" {
		t.Fatalf("expected descriptive error when root user's server is stale")
	}
}
