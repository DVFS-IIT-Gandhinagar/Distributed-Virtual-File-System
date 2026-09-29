package metaserver

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	pb "github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/api/metaserver"
	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/domain"
)

func newTestMetaServer(t *testing.T) *MetaServer {
	t.Helper()

	statePath := filepath.Join(t.TempDir(), "state", "metaserver_state.json")
	ms, err := NewMetaServer(statePath)
	if err != nil {
		t.Fatalf("NewMetaServer failed: %v", err)
	}
	return ms
}

func TestMetaServerSaveAndLoadStateRoundTrip(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "nested", "ms_state.json")

	ms, err := NewMetaServer(statePath)
	if err != nil {
		t.Fatalf("NewMetaServer failed: %v", err)
	}

	now := time.Now().Unix()
	ms.fileservers[1] = &domain.FileServerInfo{
		Address:           "10.0.0.1:5001",
		UserCount:         2,
		LastHeartbeatUnix: now,
		Status:            domain.FileServerStatusHealthy,
	}
	ms.users["alice"] = 1
	ms.users["bob"] = 1
	ms.shared["bob"] = []SharedDirEntry{{Owner: "alice", Path: "/alice/docs", DisplayName: "docs"}}
	ms.nextFsID = 2

	if err := ms.SaveState(); err != nil {
		t.Fatalf("SaveState failed: %v", err)
	}

	reloaded, err := NewMetaServer(statePath)
	if err != nil {
		t.Fatalf("NewMetaServer (reload) failed: %v", err)
	}

	if reloaded.nextFsID != 2 {
		t.Fatalf("nextFsID mismatch: got=%d want=2", reloaded.nextFsID)
	}

	if got := reloaded.fileservers[1]; got == nil || got.Address != "10.0.0.1:5001" || got.UserCount != 2 {
		t.Fatalf("reloaded fileserver mismatch: got=%+v", got)
	}

	if !reflect.DeepEqual(reloaded.users, ms.users) {
		t.Fatalf("users mismatch: got=%v want=%v", reloaded.users, ms.users)
	}

	if !reflect.DeepEqual(reloaded.shared, ms.shared) {
		t.Fatalf("shared mismatch: got=%v want=%v", reloaded.shared, ms.shared)
	}
}

func TestMarkStaleFileServersLocked(t *testing.T) {
	ms := newTestMetaServer(t)
	ms.heartbeatTimeout = 10 * time.Second

	now := time.Now().Unix()
	ms.fileservers[1] = &domain.FileServerInfo{Address: "fs1", UserCount: 1, LastHeartbeatUnix: now, Status: domain.FileServerStatusHealthy}
	ms.fileservers[2] = &domain.FileServerInfo{Address: "fs2", UserCount: 1, LastHeartbeatUnix: now - 100, Status: domain.FileServerStatusHealthy}

	changed := ms.markStaleFileServersLocked(now)
	if !changed {
		t.Fatalf("expected stale transition to report changed=true")
	}

	if got := ms.fileservers[1].Status; got != domain.FileServerStatusHealthy {
		t.Fatalf("fs1 status mismatch: got=%s want=%s", got, domain.FileServerStatusHealthy)
	}

	if got := ms.fileservers[2].Status; got != domain.FileServerStatusStale {
		t.Fatalf("fs2 status mismatch: got=%s want=%s", got, domain.FileServerStatusStale)
	}

	if changedAgain := ms.markStaleFileServersLocked(now); changedAgain {
		t.Fatalf("expected second stale pass to be no-op")
	}
}

func TestGetLeastLoadedHealthyFileServerLocked(t *testing.T) {
	ms := newTestMetaServer(t)
	now := time.Now().Unix()

	ms.fileservers[0] = &domain.FileServerInfo{Address: "fs0", UserCount: 10, LastHeartbeatUnix: now, Status: domain.FileServerStatusHealthy}
	ms.fileservers[1] = &domain.FileServerInfo{Address: "fs1", UserCount: 1, LastHeartbeatUnix: now, Status: domain.FileServerStatusStale}
	ms.fileservers[2] = &domain.FileServerInfo{Address: "fs2", UserCount: 3, LastHeartbeatUnix: now, Status: domain.FileServerStatusHealthy}

	gotID, ok := ms.getLeastLoadedHealthyFileServerLocked(now)
	if !ok {
		t.Fatalf("expected to find at least one healthy file server")
	}

	if gotID != 2 {
		t.Fatalf("least-loaded healthy server mismatch: got=%d want=2", gotID)
	}
}

func TestSetHeartbeatConfig(t *testing.T) {
	ms := newTestMetaServer(t)

	ms.SetHeartbeatConfig(60*time.Second, 8*time.Second)

	if ms.heartbeatTimeout != 60*time.Second {
		t.Fatalf("heartbeatTimeout mismatch: got=%v want=60s", ms.heartbeatTimeout)
	}
	if ms.heartbeatCheckInterval != 8*time.Second {
		t.Fatalf("heartbeatCheckInterval mismatch: got=%v want=8s", ms.heartbeatCheckInterval)
	}
}

func TestHandlerRegisterFileServerSuccessAndSharedMapping(t *testing.T) {
	ms := newTestMetaServer(t)
	h := NewGRPCHandler(ms)

	resp, err := h.RegisterFileServer(context.Background(), &pb.RegisterFileServerRequest{
		Address: "127.0.0.1:5001",
		Users:   []string{"alice", "bob"},
		Shared: []*pb.SharedDir{
			{Owner: "alice", Name: "docs", Path: "alice/docs", Users: []string{"bob", "alice"}},
		},
	})
	if err != nil {
		t.Fatalf("RegisterFileServer returned error: %v", err)
	}
	if !resp.Success {
		t.Fatalf("RegisterFileServer failed: %s", resp.Error)
	}

	if ms.nextFsID != 1 {
		t.Fatalf("nextFsID mismatch: got=%d want=1", ms.nextFsID)
	}

	if got := ms.users["alice"]; got != 0 {
		t.Fatalf("alice mapping mismatch: got=%d want=0", got)
	}
	if got := ms.users["bob"]; got != 0 {
		t.Fatalf("bob mapping mismatch: got=%d want=0", got)
	}

	sharedForBob := ms.shared["bob"]
	if len(sharedForBob) != 1 {
		t.Fatalf("shared entries for bob mismatch: got=%d want=1", len(sharedForBob))
	}
	if sharedForBob[0].Owner != "alice" || sharedForBob[0].Path != "/alice/docs" || sharedForBob[0].DisplayName != "docs" {
		t.Fatalf("unexpected shared entry: %+v", sharedForBob[0])
	}

	if ms.fileservers[0].UserCount != 2 {
		t.Fatalf("user count mismatch: got=%d want=2", ms.fileservers[0].UserCount)
	}
}

func TestHandlerRegisterFileServerRejectsUserConflict(t *testing.T) {
	ms := newTestMetaServer(t)
	h := NewGRPCHandler(ms)

	first, err := h.RegisterFileServer(context.Background(), &pb.RegisterFileServerRequest{
		Address: "127.0.0.1:5001",
		Users:   []string{"alice"},
	})
	if err != nil || !first.Success {
		t.Fatalf("first registration failed: err=%v resp=%+v", err, first)
	}

	second, err := h.RegisterFileServer(context.Background(), &pb.RegisterFileServerRequest{
		Address: "127.0.0.1:5002",
		Users:   []string{"alice"},
	})
	if err != nil {
		t.Fatalf("second registration returned RPC error: %v", err)
	}
	if second.Success {
		t.Fatalf("expected conflict registration to fail")
	}
	if !strings.Contains(second.Error, "already exists") {
		t.Fatalf("unexpected conflict error: %q", second.Error)
	}
}

func TestHandlerHeartbeat(t *testing.T) {
	ms := newTestMetaServer(t)
	h := NewGRPCHandler(ms)

	_, _ = h.RegisterFileServer(context.Background(), &pb.RegisterFileServerRequest{
		Address: "127.0.0.1:5001",
		Users:   []string{"alice"},
	})

	if resp, _ := h.Heartbeat(context.Background(), &pb.HeartbeatRequest{Address: ""}); resp.Success {
		t.Fatalf("expected empty-address heartbeat to fail")
	}

	if resp, _ := h.Heartbeat(context.Background(), &pb.HeartbeatRequest{Address: "unknown:5001"}); resp.Success {
		t.Fatalf("expected unknown-address heartbeat to fail")
	}

	resp, err := h.Heartbeat(context.Background(), &pb.HeartbeatRequest{Address: "127.0.0.1:5001"})
	if err != nil {
		t.Fatalf("heartbeat returned error: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected heartbeat success, got error=%q", resp.Error)
	}
}

func TestHandlerNavigateAuthorizationMatrix(t *testing.T) {
	ms := newTestMetaServer(t)
	h := NewGRPCHandler(ms)

	_, _ = h.RegisterFileServer(context.Background(), &pb.RegisterFileServerRequest{
		Address: "127.0.0.1:5001",
		Users:   []string{"alice", "bob", "charlie"},
		Shared: []*pb.SharedDir{
			{Owner: "alice", Name: "docs", Path: "alice/docs", Users: []string{"bob"}},
		},
	})

	ownerResp, _ := h.Navigate(context.Background(), &pb.NavigateRequest{Username: "alice", RootUser: "alice"})
	if !ownerResp.Success || ownerResp.Address != "127.0.0.1:5001" {
		t.Fatalf("owner navigate mismatch: %+v", ownerResp)
	}

	sharedResp, _ := h.Navigate(context.Background(), &pb.NavigateRequest{Username: "bob", RootUser: "alice"})
	if !sharedResp.Success || sharedResp.Address != "127.0.0.1:5001" {
		t.Fatalf("shared navigate mismatch: %+v", sharedResp)
	}

	deniedResp, _ := h.Navigate(context.Background(), &pb.NavigateRequest{Username: "charlie", RootUser: "alice"})
	if deniedResp.Success {
		t.Fatalf("expected unauthorized navigate to fail")
	}
	if !strings.Contains(deniedResp.Error, "does not have access") {
		t.Fatalf("unexpected unauthorized error: %q", deniedResp.Error)
	}
}

func TestHandlerGetRootsAssignsLeastLoadedHealthyServer(t *testing.T) {
	ms := newTestMetaServer(t)
	h := NewGRPCHandler(ms)
	now := time.Now().Unix()

	ms.fileservers[0] = &domain.FileServerInfo{Address: "fs0:5001", UserCount: 3, LastHeartbeatUnix: now, Status: domain.FileServerStatusHealthy}
	ms.fileservers[1] = &domain.FileServerInfo{Address: "fs1:5001", UserCount: 1, LastHeartbeatUnix: now, Status: domain.FileServerStatusHealthy}
	ms.nextFsID = 2

	resp, err := h.GetRoots(context.Background(), &pb.GetRootsRequest{Username: "dave"})
	if err != nil {
		t.Fatalf("GetRoots returned error: %v", err)
	}
	if !resp.Success {
		t.Fatalf("GetRoots failed: %s", resp.Error)
	}

	if got := ms.users["dave"]; got != 1 {
		t.Fatalf("user assignment mismatch: got=%d want=1", got)
	}

	if got := ms.fileservers[1].UserCount; got != 2 {
		t.Fatalf("updated user count mismatch: got=%d want=2", got)
	}

	if len(resp.Roots) != 1 {
		t.Fatalf("roots size mismatch: got=%d want=1", len(resp.Roots))
	}
	if resp.Roots[0].DisplayName != "mydrive" || resp.Roots[0].Owner != "dave" || resp.Roots[0].Path != "dave" {
		t.Fatalf("roots mismatch: got=%+v want={DisplayName:mydrive Owner:dave Path:dave}", resp.Roots[0])
	}
}

func TestHandlerRootShareAndUnshareLifecycle(t *testing.T) {
	ms := newTestMetaServer(t)
	h := NewGRPCHandler(ms)

	ms.users["alice"] = 0
	ms.users["bob"] = 0
	ms.shared["bob"] = []SharedDirEntry{}

	shareResp, err := h.RootShare(context.Background(), &pb.RootShareRequest{
		Owner:     "alice",
		RootPath:  "/alice/project",
		ShareWith: "bob",
		Name:      "project",
	})
	if err != nil {
		t.Fatalf("RootShare returned error: %v", err)
	}
	if !shareResp.Success {
		t.Fatalf("RootShare failed: %s", shareResp.Error)
	}

	if got := len(ms.shared["bob"]); got != 1 {
		t.Fatalf("shared list length mismatch: got=%d want=1", got)
	}

	dupResp, _ := h.RootShare(context.Background(), &pb.RootShareRequest{
		Owner:     "alice",
		RootPath:  "/alice/project",
		ShareWith: "bob",
		Name:      "project",
	})
	if !dupResp.Success {
		t.Fatalf("duplicate RootShare should be idempotent success: %s", dupResp.Error)
	}
	if got := len(ms.shared["bob"]); got != 1 {
		t.Fatalf("duplicate share should not create extra entries: got=%d", got)
	}

	unshareResp, err := h.RootUnshare(context.Background(), &pb.RootUnshareRequest{
		Owner:       "alice",
		RootPath:    "/alice/project",
		UnshareWith: "bob",
		Name:        "project",
	})
	if err != nil {
		t.Fatalf("RootUnshare returned error: %v", err)
	}
	if !unshareResp.Success {
		t.Fatalf("RootUnshare failed: %s", unshareResp.Error)
	}

	if got := len(ms.shared["bob"]); got != 0 {
		t.Fatalf("shared list length after unshare mismatch: got=%d want=0", got)
	}
}

func TestHandlerDeregisterFileServer(t *testing.T) {
	ms := newTestMetaServer(t)
	h := NewGRPCHandler(ms)

	// 1. Empty address
	resp, err := h.DeregisterFileServer(context.Background(), &pb.DeregisterFileServerRequest{
		Address: "",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Success {
		t.Fatalf("expected failure for empty address")
	}

	// 2. Non-existent address (should be idempotent success)
	resp, err = h.DeregisterFileServer(context.Background(), &pb.DeregisterFileServerRequest{
		Address: "192.168.1.100:50052",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected idempotent success for non-existent server: %v", resp.Error)
	}

	// 3. Register FS 1 with users alice and bob, and FS 2 with user charlie
	_, err = h.RegisterFileServer(context.Background(), &pb.RegisterFileServerRequest{
		Address: "10.0.0.1:50052",
		Users:   []string{"alice", "bob"},
		Shared: []*pb.SharedDir{
			{Owner: "alice", Path: "alice/docs", Name: "docs", Users: []string{"bob"}},
		},
	})
	if err != nil {
		t.Fatalf("register FS 1 failed: %v", err)
	}

	_, err = h.RegisterFileServer(context.Background(), &pb.RegisterFileServerRequest{
		Address: "10.0.0.2:50052",
		Users:   []string{"charlie"},
	})
	if err != nil {
		t.Fatalf("register FS 2 failed: %v", err)
	}

	// Verify initial state
	if len(ms.fileservers) != 2 {
		t.Fatalf("expected 2 fileservers, got %d", len(ms.fileservers))
	}
	if len(ms.users) != 3 {
		t.Fatalf("expected 3 users, got %d", len(ms.users))
	}

	// Deregister FS 1
	resp, err = h.DeregisterFileServer(context.Background(), &pb.DeregisterFileServerRequest{
		Address: "10.0.0.1:50052",
	})
	if err != nil {
		t.Fatalf("deregister FS 1 error: %v", err)
	}
	if !resp.Success {
		t.Fatalf("deregister FS 1 failed: %s", resp.Error)
	}

	// Verify FS 1 and its users (alice, bob) are removed, charlie remains
	if len(ms.fileservers) != 1 {
		t.Fatalf("expected 1 fileserver remaining, got %d", len(ms.fileservers))
	}
	if _, exists := ms.users["alice"]; exists {
		t.Errorf("alice should have been removed")
	}
	if _, exists := ms.users["bob"]; exists {
		t.Errorf("bob should have been removed")
	}
	if _, exists := ms.users["charlie"]; !exists {
		t.Errorf("charlie on FS 2 should still exist")
	}

	// Reload state from disk to ensure persistence
	reloaded, err := NewMetaServer(ms.stateFile)
	if err != nil {
		t.Fatalf("failed to reload state: %v", err)
	}
	if len(reloaded.fileservers) != 1 {
		t.Fatalf("reloaded fileservers mismatch: got %d, want 1", len(reloaded.fileservers))
	}

	// Calling deregister again on FS 1 should be idempotent success
	resp, err = h.DeregisterFileServer(context.Background(), &pb.DeregisterFileServerRequest{
		Address: "10.0.0.1:50052",
	})
	if err != nil || !resp.Success {
		t.Fatalf("subsequent deregister failed: err=%v, resp=%+v", err, resp)
	}
}

