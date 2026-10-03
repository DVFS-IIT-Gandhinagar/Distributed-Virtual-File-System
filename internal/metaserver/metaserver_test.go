package metaserver

import (
	"context"
	"strings"
	"testing"
	"time"

	pb "github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/api/metaserver"
	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/domain"
	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/storage"
	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/storage/memory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestMetaServer(t *testing.T) *MetaServer {
	t.Helper()

	ms, err := NewMetaServer(context.Background(), memory.New())
	if err != nil {
		t.Fatalf("NewMetaServer failed: %v", err)
	}
	return ms
}

// seedFileServer registers a node in both the store and the in-memory table,
// leaving the same state a real registration would.
func seedFileServer(t *testing.T, ms *MetaServer, nodeID, addr string, userCount int, lastHeartbeat int64, status string) uint64 {
	t.Helper()

	numericID, err := ms.store.UpsertFileServer(context.Background(), storage.FileServerRecord{
		NodeID:            nodeID,
		Address:           addr,
		LastHeartbeatUnix: lastHeartbeat,
		Status:            status,
	})
	if err != nil {
		t.Fatalf("seed fileserver %s: %v", nodeID, err)
	}

	ms.mu.Lock()
	ms.fileservers[numericID] = &domain.FileServerInfo{
		NodeID:            nodeID,
		Address:           addr,
		UserCount:         userCount,
		LastHeartbeatUnix: lastHeartbeat,
		Status:            status,
	}
	ms.mu.Unlock()
	return numericID
}

// TestMetaServerStateSurvivesRestart: state written through the handlers must be
// recovered verbatim by a fresh MetaServer sharing the same store.
func TestMetaServerStateSurvivesRestart(t *testing.T) {
	store := memory.New()

	ms, err := NewMetaServer(context.Background(), store)
	if err != nil {
		t.Fatalf("NewMetaServer failed: %v", err)
	}
	h := NewGRPCHandler(ms)

	resp, err := h.RegisterFileServer(context.Background(), &pb.RegisterFileServerRequest{
		FsId:    "fs1",
		Address: "10.0.0.1:5001",
		Users:   []string{"alice", "bob"},
		Shared: []*pb.SharedDir{
			{Owner: "alice", Name: "docs", Path: "alice/docs", Users: []string{"bob"}},
		},
	})
	if err != nil || !resp.Success {
		t.Fatalf("RegisterFileServer failed: err=%v resp=%+v", err, resp)
	}

	reloaded, err := NewMetaServer(context.Background(), store)
	if err != nil {
		t.Fatalf("NewMetaServer (reload) failed: %v", err)
	}

	reloaded.mu.RLock()
	defer reloaded.mu.RUnlock()

	if len(reloaded.fileservers) != 1 {
		t.Fatalf("expected 1 fileserver after reload, got %d", len(reloaded.fileservers))
	}
	var info *domain.FileServerInfo
	for _, v := range reloaded.fileservers {
		info = v
	}
	if info.NodeID != "fs1" || info.Address != "10.0.0.1:5001" || info.UserCount != 2 {
		t.Fatalf("reloaded fileserver mismatch: %+v", info)
	}

	if len(reloaded.users) != 2 {
		t.Fatalf("users mismatch after reload: %v", reloaded.users)
	}
	if _, ok := reloaded.users["alice"]; !ok {
		t.Fatalf("alice missing after reload")
	}

	sharedForBob := reloaded.shared["bob"]
	if len(sharedForBob) != 1 {
		t.Fatalf("shared entries for bob mismatch: got=%d want=1 (%+v)", len(sharedForBob), sharedForBob)
	}
	if sharedForBob[0].Owner != "alice" || sharedForBob[0].Path != "alice/docs" {
		t.Fatalf("unexpected shared entry after reload: %+v", sharedForBob[0])
	}
}

func TestMarkStaleFileServersLocked(t *testing.T) {
	ms := newTestMetaServer(t)
	ms.heartbeatTimeout = 10 * time.Second

	now := time.Now().Unix()
	seedFileServer(t, ms, "fs1", "fs1:1", 1, now, domain.FileServerStatusHealthy)
	fs2 := seedFileServer(t, ms, "fs2", "fs2:1", 1, now-100, domain.FileServerStatusHealthy)

	transitioned := ms.markStaleFileServersLocked(now)
	if len(transitioned) != 1 || transitioned[0] != "fs2" {
		t.Fatalf("expected fs2 to transition, got %v", transitioned)
	}

	if got := ms.fileservers[fs2].Status; got != domain.FileServerStatusStale {
		t.Fatalf("fs2 status mismatch: got=%s want=%s", got, domain.FileServerStatusStale)
	}

	if again := ms.markStaleFileServersLocked(now); len(again) != 0 {
		t.Fatalf("expected second stale pass to be a no-op, got %v", again)
	}
}

func TestGetLeastLoadedHealthyFileServerLocked(t *testing.T) {
	ms := newTestMetaServer(t)
	now := time.Now().Unix()

	seedFileServer(t, ms, "fs0", "fs0:1", 10, now, domain.FileServerStatusHealthy)
	seedFileServer(t, ms, "fs1", "fs1:1", 1, now, domain.FileServerStatusStale)
	want := seedFileServer(t, ms, "fs2", "fs2:1", 3, now, domain.FileServerStatusHealthy)

	gotID, ok := ms.getLeastLoadedHealthyFileServerLocked(now)
	if !ok {
		t.Fatalf("expected to find at least one healthy file server")
	}
	if gotID != want {
		t.Fatalf("least-loaded healthy server mismatch: got=%d want=%d", gotID, want)
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
		FsId:    "fs1",
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

	ms.mu.RLock()
	defer ms.mu.RUnlock()

	fsID, ok := ms.findFileServerByNodeIDLocked("fs1")
	if !ok {
		t.Fatalf("node fs1 not registered")
	}
	if got := ms.users["alice"]; got != fsID {
		t.Fatalf("alice mapping mismatch: got=%d want=%d", got, fsID)
	}
	if got := ms.users["bob"]; got != fsID {
		t.Fatalf("bob mapping mismatch: got=%d want=%d", got, fsID)
	}

	sharedForBob := ms.shared["bob"]
	if len(sharedForBob) != 1 {
		t.Fatalf("shared entries for bob mismatch: got=%d want=1", len(sharedForBob))
	}
	// Paths are normalized on the way in, so the leading slash the old code
	// added during registration is gone.
	if sharedForBob[0].Owner != "alice" || sharedForBob[0].Path != "alice/docs" || sharedForBob[0].DisplayName != "docs" {
		t.Fatalf("unexpected shared entry: %+v", sharedForBob[0])
	}

	if ms.fileservers[fsID].UserCount != 2 {
		t.Fatalf("user count mismatch: got=%d want=2", ms.fileservers[fsID].UserCount)
	}
}

func TestHandlerRegisterFileServerRejectsUserConflict(t *testing.T) {
	ms := newTestMetaServer(t)
	h := NewGRPCHandler(ms)

	first, err := h.RegisterFileServer(context.Background(), &pb.RegisterFileServerRequest{
		FsId: "fs1", Address: "127.0.0.1:5001", Users: []string{"alice"},
	})
	if err != nil || !first.Success {
		t.Fatalf("first registration failed: err=%v resp=%+v", err, first)
	}

	second, err := h.RegisterFileServer(context.Background(), &pb.RegisterFileServerRequest{
		FsId: "fs2", Address: "127.0.0.1:5002", Users: []string{"alice"},
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

// TestRegisterFileServerAddressChangeKeepsIdentity is the regression test for
// the phantom-node bug: a node that gets a new DHCP lease must update in place
// rather than appear as a second, empty, permanently-healthy node that then
// captures every newly-enrolling user.
func TestRegisterFileServerAddressChangeKeepsIdentity(t *testing.T) {
	ms := newTestMetaServer(t)
	h := NewGRPCHandler(ms)

	if resp, err := h.RegisterFileServer(context.Background(), &pb.RegisterFileServerRequest{
		FsId: "fs1", Address: "10.7.52.85:50052", Users: []string{"alice", "bob"},
	}); err != nil || !resp.Success {
		t.Fatalf("initial registration failed: err=%v resp=%+v", err, resp)
	}

	// Same node, new lease. The process that held the old address is gone, so
	// its registration has expired by the time the restarted one arrives; a
	// live holder at another address would be a second machine with this id.
	forceStale(ms, "fs1")
	if resp, err := h.RegisterFileServer(context.Background(), &pb.RegisterFileServerRequest{
		FsId: "fs1", Address: "10.0.171.38:50052", Users: []string{"alice", "bob"},
	}); err != nil || !resp.Success {
		t.Fatalf("re-registration after address change failed: err=%v resp=%+v", err, resp)
	}

	ms.mu.RLock()
	defer ms.mu.RUnlock()

	if len(ms.fileservers) != 1 {
		t.Fatalf("address change created a phantom node: %d nodes registered", len(ms.fileservers))
	}
	fsID, _ := ms.findFileServerByNodeIDLocked("fs1")
	if got := ms.fileservers[fsID].Address; got != "10.0.171.38:50052" {
		t.Fatalf("address not updated in place: got %q", got)
	}
	if got := ms.users["alice"]; got != fsID {
		t.Fatalf("alice stranded after address change: mapped to %d, node is %d", got, fsID)
	}
}

// TestRegisterFileServerDoesNotPurgeUsersByDefault covers the data-loss path:
// req.Users comes from a disk scan, so a data directory that failed to mount
// registers as "this node has no users". That must not delete the mappings.
func TestRegisterFileServerDoesNotPurgeUsersByDefault(t *testing.T) {
	ms := newTestMetaServer(t)
	h := NewGRPCHandler(ms)

	if resp, _ := h.RegisterFileServer(context.Background(), &pb.RegisterFileServerRequest{
		FsId: "fs1", Address: "127.0.0.1:5001", Users: []string{"alice", "bob", "carol"},
	}); !resp.Success {
		t.Fatalf("initial registration failed: %s", resp.Error)
	}

	// Same node comes back reporting nothing, as an unmounted disk would.
	if resp, _ := h.RegisterFileServer(context.Background(), &pb.RegisterFileServerRequest{
		FsId: "fs1", Address: "127.0.0.1:5001", Users: nil,
	}); !resp.Success {
		t.Fatalf("empty re-registration failed: %s", resp.Error)
	}

	ms.mu.RLock()
	got := len(ms.users)
	ms.mu.RUnlock()
	if got != 3 {
		t.Fatalf("empty registration purged user mappings: %d users remain, want 3", got)
	}

	snap, err := ms.store.LoadSnapshot(context.Background())
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	if len(snap.Users) != 3 {
		t.Fatalf("empty registration purged users from the store: %+v", snap.Users)
	}
}

func TestRegisterFileServerPurgesUsersWhenExplicitlyAllowed(t *testing.T) {
	ms := newTestMetaServer(t)
	ms.SetAllowUserPurge(true)
	h := NewGRPCHandler(ms)

	if resp, _ := h.RegisterFileServer(context.Background(), &pb.RegisterFileServerRequest{
		FsId: "fs1", Address: "127.0.0.1:5001", Users: []string{"alice", "bob"},
	}); !resp.Success {
		t.Fatalf("initial registration failed: %s", resp.Error)
	}

	if resp, _ := h.RegisterFileServer(context.Background(), &pb.RegisterFileServerRequest{
		FsId: "fs1", Address: "127.0.0.1:5001", Users: []string{"alice"},
	}); !resp.Success {
		t.Fatalf("re-registration failed: %s", resp.Error)
	}

	ms.mu.RLock()
	_, bobStillMapped := ms.users["bob"]
	ms.mu.RUnlock()
	if bobStillMapped {
		t.Fatalf("expected bob to be purged when -allow_user_purge is set")
	}
}

func TestHandlerHeartbeat(t *testing.T) {
	ms := newTestMetaServer(t)
	h := NewGRPCHandler(ms)

	_, _ = h.RegisterFileServer(context.Background(), &pb.RegisterFileServerRequest{
		FsId: "fs1", Address: "127.0.0.1:5001", Users: []string{"alice"},
	})

	if resp, _ := h.Heartbeat(context.Background(), &pb.HeartbeatRequest{Address: ""}); resp.Success {
		t.Fatalf("expected empty-address heartbeat to fail")
	}

	if resp, _ := h.Heartbeat(context.Background(), &pb.HeartbeatRequest{FsId: "nope", Address: "unknown:5001"}); resp.Success {
		t.Fatalf("expected unknown-node heartbeat to fail")
	}

	resp, err := h.Heartbeat(context.Background(), &pb.HeartbeatRequest{FsId: "fs1", Address: "127.0.0.1:5001"})
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
		FsId:    "fs1",
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

	seedFileServer(t, ms, "fs0", "fs0:5001", 3, now, domain.FileServerStatusHealthy)
	want := seedFileServer(t, ms, "fs1", "fs1:5001", 1, now, domain.FileServerStatusHealthy)

	resp, err := h.GetRoots(context.Background(), &pb.GetRootsRequest{Username: "dave"})
	if err != nil {
		t.Fatalf("GetRoots returned error: %v", err)
	}
	if !resp.Success {
		t.Fatalf("GetRoots failed: %s", resp.Error)
	}

	ms.mu.RLock()
	assigned := ms.users["dave"]
	count := ms.fileservers[want].UserCount
	ms.mu.RUnlock()

	if assigned != want {
		t.Fatalf("user assignment mismatch: got=%d want=%d", assigned, want)
	}
	if count != 2 {
		t.Fatalf("updated user count mismatch: got=%d want=2", count)
	}

	if len(resp.Roots) != 1 {
		t.Fatalf("roots size mismatch: got=%d want=1", len(resp.Roots))
	}
	if resp.Roots[0].DisplayName != "mydrive" || resp.Roots[0].Owner != "dave" || resp.Roots[0].Path != "dave" {
		t.Fatalf("roots mismatch: got=%+v", resp.Roots[0])
	}
}

func TestHandlerRootShareAndUnshareLifecycle(t *testing.T) {
	ms := newTestMetaServer(t)
	h := NewGRPCHandler(ms)

	ms.users["alice"] = 0
	ms.users["bob"] = 0
	ms.shared["bob"] = []SharedDirEntry{}

	shareResp, err := h.RootShare(context.Background(), &pb.RootShareRequest{
		Owner: "alice", RootPath: "/alice/project", ShareWith: "bob", Name: "project",
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
		Owner: "alice", RootPath: "/alice/project", ShareWith: "bob", Name: "project",
	})
	if !dupResp.Success {
		t.Fatalf("duplicate RootShare should be idempotent success: %s", dupResp.Error)
	}
	if got := len(ms.shared["bob"]); got != 1 {
		t.Fatalf("duplicate share should not create extra entries: got=%d", got)
	}

	unshareResp, err := h.RootUnshare(context.Background(), &pb.RootUnshareRequest{
		Owner: "alice", RootPath: "/alice/project", UnshareWith: "bob", Name: "project",
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

// TestRootShareSecondDirectoryFromSameOwner is the regression test for the
// duplicate-key bug: deduplicating on owner alone silently dropped every
// directory after the first that an owner shared with the same person, while
// still reporting success.
func TestRootShareSecondDirectoryFromSameOwner(t *testing.T) {
	ms := newTestMetaServer(t)
	h := NewGRPCHandler(ms)

	ms.users["alice"] = 0
	ms.users["bob"] = 0
	ms.shared["bob"] = []SharedDirEntry{}

	for _, dir := range []string{"proj1", "proj2"} {
		resp, err := h.RootShare(context.Background(), &pb.RootShareRequest{
			Owner: "alice", RootPath: "alice/" + dir, ShareWith: "bob", Name: dir,
		})
		if err != nil || !resp.Success {
			t.Fatalf("RootShare %s failed: err=%v resp=%+v", dir, err, resp)
		}
	}

	if got := len(ms.shared["bob"]); got != 2 {
		t.Fatalf("second share from the same owner was dropped: %d entries (%+v)", got, ms.shared["bob"])
	}

	roots, _ := h.GetRoots(context.Background(), &pb.GetRootsRequest{Username: "bob"})
	// mydrive plus both shared directories.
	if len(roots.Roots) != 3 {
		t.Fatalf("bob should see mydrive + 2 shared roots, got %d: %+v", len(roots.Roots), roots.Roots)
	}
}

// TestRootUnshareAfterRegistrationRebuild is the regression test for the
// revocation failure: registration used to store slash-prefixed paths while
// unshare compared unprefixed ones, so the exact match never fired and the
// grantee silently kept access.
func TestRootUnshareAfterRegistrationRebuild(t *testing.T) {
	ms := newTestMetaServer(t)
	h := NewGRPCHandler(ms)

	if resp, _ := h.RegisterFileServer(context.Background(), &pb.RegisterFileServerRequest{
		FsId:    "fs1",
		Address: "127.0.0.1:5001",
		Users:   []string{"alice", "bob"},
		Shared: []*pb.SharedDir{
			{Owner: "alice", Name: "proj", Path: "alice/proj", Users: []string{"bob"}},
		},
	}); !resp.Success {
		t.Fatalf("registration failed: %s", resp.Error)
	}

	if got := len(ms.shared["bob"]); got != 1 {
		t.Fatalf("expected bob to see the share after registration, got %d", got)
	}

	// The fileserver sends the unprefixed relative path on unshare.
	resp, err := h.RootUnshare(context.Background(), &pb.RootUnshareRequest{
		Owner: "alice", RootPath: "alice/proj", UnshareWith: "bob", Name: "proj",
	})
	if err != nil || !resp.Success {
		t.Fatalf("RootUnshare failed: err=%v resp=%+v", err, resp)
	}

	ms.mu.RLock()
	remaining := len(ms.shared["bob"])
	ms.mu.RUnlock()
	if remaining != 0 {
		t.Fatalf("unshare did not revoke access: %+v", ms.shared["bob"])
	}

	snap, err := ms.store.LoadSnapshot(context.Background())
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	if len(snap.Shares) != 0 {
		t.Fatalf("share still present in the store after unshare: %+v", snap.Shares)
	}

	nav, _ := h.Navigate(context.Background(), &pb.NavigateRequest{Username: "bob", RootUser: "alice"})
	if nav.Success {
		t.Fatalf("bob still has routing access to alice's root after unshare")
	}
}

func TestHandlerDeregisterFileServer(t *testing.T) {
	ms := newTestMetaServer(t)
	h := NewGRPCHandler(ms)
	ctx := context.Background()

	// 1. A request naming no node must fail.
	resp, err := h.DeregisterFileServer(ctx, &pb.DeregisterFileServerRequest{})
	require.NoError(t, err)
	assert.False(t, resp.Success)

	// 2. An unknown node is an idempotent success, so a decommission can be retried.
	resp, err = h.DeregisterFileServer(ctx, &pb.DeregisterFileServerRequest{Address: "192.168.1.100:50052"})
	require.NoError(t, err)
	assert.True(t, resp.Success, resp.Error)

	// 3. fs1 hosts alice and bob (alice shares with bob); fs2 hosts charlie.
	_, err = h.RegisterFileServer(ctx, &pb.RegisterFileServerRequest{
		FsId: "fs1", Address: "10.0.0.1:50052", Users: []string{"alice", "bob"},
		Shared: []*pb.SharedDir{{Owner: "alice", Path: "alice/docs", Name: "docs", Users: []string{"bob"}}},
	})
	require.NoError(t, err)
	_, err = h.RegisterFileServer(ctx, &pb.RegisterFileServerRequest{
		FsId: "fs2", Address: "10.0.0.2:50052", Users: []string{"charlie"},
	})
	require.NoError(t, err)
	require.Len(t, ms.fileservers, 2)
	require.Len(t, ms.users, 3)

	// Deregister fs1 by address, as a caller that predates fs_id would.
	resp, err = h.DeregisterFileServer(ctx, &pb.DeregisterFileServerRequest{Address: "10.0.0.1:50052"})
	require.NoError(t, err)
	require.True(t, resp.Success, resp.Error)

	assert.Len(t, ms.fileservers, 1)
	assert.NotContains(t, ms.users, "alice")
	assert.NotContains(t, ms.users, "bob")
	assert.Contains(t, ms.users, "charlie", "a user on another node must be untouched")
	assert.Empty(t, ms.shared["bob"], "grants to a released user must be gone")

	// The store must agree: a fresh metaserver hydrated from it sees the same,
	// and the released users must not come back as orphans.
	reloaded, err := NewMetaServer(ctx, ms.store)
	require.NoError(t, err)
	assert.Len(t, reloaded.fileservers, 1)
	assert.NotContains(t, reloaded.users, "alice")
	assert.NotContains(t, reloaded.orphanedUsers, "alice")
	assert.Contains(t, reloaded.users, "charlie")
	snap, err := ms.store.LoadSnapshot(ctx)
	require.NoError(t, err)
	assert.Empty(t, snap.Shares, "shares involving released users must be gone from the store")

	// 4. Deregistering again is still an idempotent success.
	resp, err = h.DeregisterFileServer(ctx, &pb.DeregisterFileServerRequest{Address: "10.0.0.1:50052"})
	require.NoError(t, err)
	assert.True(t, resp.Success)
}

// Deregistering by fs_id also releases users orphaned on that node, so a node
// that was already gone when the metaserver started can still be decommissioned.
// Only fs_id can name such a node, since it has no registered address.
func TestHandlerDeregisterFileServerReleasesOrphanedUsers(t *testing.T) {
	store := memory.New()
	ctx := context.Background()
	_, err := store.UpsertFileServer(ctx, storage.FileServerRecord{
		NodeID: "fs1", Address: "10.0.0.1:50052", Status: domain.FileServerStatusHealthy,
		LastHeartbeatUnix: time.Now().Unix(),
	})
	require.NoError(t, err)
	require.NoError(t, store.AssignUser(ctx, "dave", "fs-dead"))

	ms, err := NewMetaServer(ctx, store)
	require.NoError(t, err)
	require.Contains(t, ms.orphanedUsers, "dave")
	h := NewGRPCHandler(ms)

	// While orphaned, dave is refused rather than reassigned.
	roots, err := h.GetRoots(ctx, &pb.GetRootsRequest{Username: "dave"})
	require.NoError(t, err)
	require.False(t, roots.Success)

	resp, err := h.DeregisterFileServer(ctx, &pb.DeregisterFileServerRequest{FsId: "fs-dead"})
	require.NoError(t, err)
	require.True(t, resp.Success, resp.Error)

	assert.NotContains(t, ms.orphanedUsers, "dave")
	snap, err := store.LoadSnapshot(ctx)
	require.NoError(t, err)
	for _, u := range snap.Users {
		assert.NotEqual(t, "dave", u.Username, "a released user must be gone from the store")
	}

	// Released means placeable again: dave now gets a home on the healthy node.
	roots, err = h.GetRoots(ctx, &pb.GetRootsRequest{Username: "dave"})
	require.NoError(t, err)
	assert.True(t, roots.Success, "a released user must be assignable again: %s", roots.Error)
}
