package metaserver

import (
	"context"
	"testing"
	"time"

	pb "github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/api/metaserver"
	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/domain"
	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/storage"
	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/storage/memory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRobustness_ContainsShare(t *testing.T) {
	entries := []SharedDirEntry{
		{Owner: "alice", Path: "alice/a"},
		{Owner: "bob", Path: "bob/b"},
	}

	assert.True(t, containsShare(entries, "alice", "alice/a"))
	assert.True(t, containsShare(entries, "bob", "bob/b"))
	assert.False(t, containsShare(entries, "charlie", "charlie/c"))

	// Matching must consider the path, not just the owner: treating an owner
	// as already-shared regardless of directory is what silently dropped a
	// second share from the same owner.
	assert.False(t, containsShare(entries, "alice", "alice/other"))
}

func TestRobustness_RemoveShareEntry(t *testing.T) {
	entries := []SharedDirEntry{
		{Owner: "alice", Path: "alice/a"},
		{Owner: "bob", Path: "bob/b"},
		{Owner: "alice", Path: "alice/a2"},
	}

	// Revoking one directory leaves the owner's other shares intact.
	res := removeShareEntry(entries, "alice", "alice/a")
	require.Len(t, res, 2)
	assert.Equal(t, "bob", res[0].Owner)
	assert.Equal(t, "alice/a2", res[1].Path)

	// Removing something absent is a no-op.
	res2 := removeShareEntry(entries, "charlie", "charlie/c")
	assert.Len(t, res2, 3)
}

func TestRobustness_IsHealthyLocked(t *testing.T) {
	ms := &MetaServer{heartbeatTimeout: 30 * time.Second}
	now := time.Now().Unix()

	// 3. nil fsInfo
	assert.False(t, ms.isHealthyLocked(nil, now))

	// 4. Status != healthy
	staleFS := &domain.FileServerInfo{Status: domain.FileServerStatusStale, LastHeartbeatUnix: now}
	assert.False(t, ms.isHealthyLocked(staleFS, now))

	// 5. LastHeartbeatUnix == 0
	noHeartbeatFS := &domain.FileServerInfo{Status: domain.FileServerStatusHealthy, LastHeartbeatUnix: 0}
	assert.False(t, ms.isHealthyLocked(noHeartbeatFS, now))

	// 6. expired heartbeat
	expiredFS := &domain.FileServerInfo{Status: domain.FileServerStatusHealthy, LastHeartbeatUnix: now - 60}
	assert.False(t, ms.isHealthyLocked(expiredFS, now))

	// 7. all valid
	validFS := &domain.FileServerInfo{Status: domain.FileServerStatusHealthy, LastHeartbeatUnix: now}
	assert.True(t, ms.isHealthyLocked(validFS, now))
}

func TestRobustness_CountUsersForFileServerLocked(t *testing.T) {
	ms := &MetaServer{
		users: map[string]uint64{
			"u1": 1,
			"u2": 1,
			"u3": 2,
		},
	}

	assert.Equal(t, 2, ms.countUsersForFileServerLocked(1))
	assert.Equal(t, 1, ms.countUsersForFileServerLocked(2))
	assert.Equal(t, 0, ms.countUsersForFileServerLocked(3))
}

func TestRobustness_FindFileServerByAddressLocked(t *testing.T) {
	ms := &MetaServer{
		fileservers: map[uint64]*domain.FileServerInfo{
			1: {Address: "10.0.0.1:8080"},
			2: {Address: "10.0.0.2:8080"},
		},
	}

	id, ok := ms.findFileServerByAddressLocked("10.0.0.1:8080")
	assert.True(t, ok)
	assert.Equal(t, uint64(1), id)

	id2, ok2 := ms.findFileServerByAddressLocked("10.0.0.3:8080")
	assert.False(t, ok2)
	assert.Equal(t, uint64(0), id2)
}

func TestRobustness_Hydrate_EmptyStore(t *testing.T) {
	ms, err := NewMetaServer(context.Background(), memory.New())
	require.NoError(t, err)
	assert.Empty(t, ms.fileservers)
	assert.Empty(t, ms.users)
	assert.Empty(t, ms.shared)
}

func TestRobustness_NewMetaServer_RequiresStore(t *testing.T) {
	_, err := NewMetaServer(context.Background(), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "MetaStore is required")
}

func TestRobustness_Hydrate_RetainsUsersWithUnregisteredHomeNode(t *testing.T) {
	store := memory.New()
	ctx := context.Background()

	_, err := store.UpsertFileServer(ctx, storage.FileServerRecord{
		NodeID: "fs1", Address: "10.0.0.1:50052", Status: domain.FileServerStatusHealthy,
		LastHeartbeatUnix: time.Now().Unix(),
	})
	require.NoError(t, err)
	require.NoError(t, store.AssignUser(ctx, "alice", "fs1"))
	require.NoError(t, store.AssignUser(ctx, "ghost", "fs-decommissioned"))

	ms, err := NewMetaServer(ctx, store)
	require.NoError(t, err)

	_, aliceOK := ms.users["alice"]
	assert.True(t, aliceOK, "a user whose home node is registered keeps a live route")

	// A user on an unregistered node must not get a bogus route into whichever
	// node happens to occupy that numeric slot...
	_, ghostRouted := ms.users["ghost"]
	assert.False(t, ghostRouted, "user on an unregistered node must not be given a live route")

	// ...but must not be forgotten either. Forgetting the assignment is what
	// lets GetRoots treat them as a brand-new user and hand them a different
	// home node, stranding their data on the original one.
	homeNode, orphaned := ms.orphanedUsers["ghost"]
	assert.True(t, orphaned, "user on an unregistered node must be retained as orphaned")
	assert.Equal(t, "fs-decommissioned", homeNode, "the original assignment must be preserved")
}

// A user whose home node is offline must be told to wait, never silently moved
// to a different node. This is the regression that orphaned user data.
func TestRobustness_GetRoots_DoesNotReassignOrphanedUser(t *testing.T) {
	store := memory.New()
	ctx := context.Background()

	_, err := store.UpsertFileServer(ctx, storage.FileServerRecord{
		NodeID: "fs1", Address: "10.0.0.1:50052", Status: domain.FileServerStatusHealthy,
		LastHeartbeatUnix: time.Now().Unix(),
	})
	require.NoError(t, err)
	require.NoError(t, store.AssignUser(ctx, "carol", "fs-offline"))

	ms, err := NewMetaServer(ctx, store)
	require.NoError(t, err)
	h := NewGRPCHandler(ms)

	resp, err := h.GetRoots(ctx, &pb.GetRootsRequest{Username: "carol"})
	require.NoError(t, err)
	assert.False(t, resp.Success, "an orphaned user must not be silently reassigned")
	assert.Contains(t, resp.Error, "fs-offline")

	// The stored assignment must be untouched: fs1 is healthy and would have
	// been the reassignment target.
	snap, err := store.LoadSnapshot(ctx)
	require.NoError(t, err)
	for _, u := range snap.Users {
		if u.Username == "carol" {
			assert.Equal(t, "fs-offline", u.HomeNodeID, "the durable assignment must survive")
		}
	}
}

// When the missing node comes back, the account becomes routable again with no
// operator intervention.
func TestRobustness_Registration_ClearsOrphanedUser(t *testing.T) {
	store := memory.New()
	ctx := context.Background()

	require.NoError(t, store.AssignUser(ctx, "carol", "fs-returning"))

	ms, err := NewMetaServer(ctx, store)
	require.NoError(t, err)
	require.Contains(t, ms.orphanedUsers, "carol")

	h := NewGRPCHandler(ms)
	resp, err := h.RegisterFileServer(ctx, &pb.RegisterFileServerRequest{
		FsId: "fs-returning", Address: "10.0.0.9:50052", Users: []string{"carol"},
	})
	require.NoError(t, err)
	require.True(t, resp.Success)

	assert.NotContains(t, ms.orphanedUsers, "carol", "a returning node clears the orphan marker")
	_, routed := ms.users["carol"]
	assert.True(t, routed, "carol is routable again")

	rootsResp, err := h.GetRoots(ctx, &pb.GetRootsRequest{Username: "carol"})
	require.NoError(t, err)
	assert.True(t, rootsResp.Success)
}

func TestRobustness_StateRoundTripThroughStore(t *testing.T) {
	store := memory.New()
	ctx := context.Background()

	ms1, err := NewMetaServer(ctx, store)
	require.NoError(t, err)
	h := NewGRPCHandler(ms1)

	resp, err := h.RegisterFileServer(ctx, &pb.RegisterFileServerRequest{
		FsId: "fs1", Address: "10.0.0.1:5001", Users: []string{"u1", "u2"},
		Shared: []*pb.SharedDir{
			{Owner: "u1", Name: "shared", Path: "u1/shared", Users: []string{"u2"}},
		},
	})
	require.NoError(t, err)
	require.True(t, resp.Success, resp.Error)

	ms2, err := NewMetaServer(ctx, store)
	require.NoError(t, err)

	assert.Equal(t, len(ms1.fileservers), len(ms2.fileservers))
	assert.Equal(t, ms1.users, ms2.users)
	assert.Equal(t, ms1.shared["u2"], ms2.shared["u2"])
}

func TestRobustness_Navigate_Gaps(t *testing.T) {
	ms := &MetaServer{
		users: map[string]uint64{
			"u1": 1,
			"u2": 2, // u2 exists but on fs 2
		},
		fileservers: map[uint64]*domain.FileServerInfo{
			1: {Address: "127.0.0.1", Status: domain.FileServerStatusHealthy, LastHeartbeatUnix: time.Now().Unix()},
			2: {Address: "127.0.0.2", Status: domain.FileServerStatusStale, LastHeartbeatUnix: 0},
		},
		shared:           make(map[string][]SharedDirEntry),
		heartbeatTimeout: 30 * time.Second,
	}
	h := &GRPCHandler{MetaServer: ms}
	ctx := context.Background()

	// 13. empty username
	resp, _ := h.Navigate(ctx, &pb.NavigateRequest{Username: "", RootUser: "u1"})
	assert.False(t, resp.Success)
	assert.Contains(t, resp.Error, "username and root_user are required")

	// 14. empty rootUser
	resp, _ = h.Navigate(ctx, &pb.NavigateRequest{Username: "u1", RootUser: ""})
	assert.False(t, resp.Success)
	assert.Contains(t, resp.Error, "username and root_user are required")

	// 15. navigate to user on unavailable/stale server
	resp, _ = h.Navigate(ctx, &pb.NavigateRequest{Username: "u1", RootUser: "u2"})
	assert.False(t, resp.Success)
	assert.Contains(t, resp.Error, "currently unavailable")
}

func TestRobustness_RootShareUnshare_Gaps(t *testing.T) {
	ms := &MetaServer{
		store: memory.New(),
		users: map[string]uint64{
			"u1": 1,
			"u2": 1,
		},
		shared: make(map[string][]SharedDirEntry),
	}
	h := &GRPCHandler{MetaServer: ms}
	ctx := context.Background()

	// 16. RootShare - owner doesn't exist
	resp1, _ := h.RootShare(ctx, &pb.RootShareRequest{Owner: "nonexist", ShareWith: "u2"})
	assert.False(t, resp1.Success)
	assert.Contains(t, resp1.Error, "does not exist")

	// 17. RootShare - target doesn't exist
	resp2, _ := h.RootShare(ctx, &pb.RootShareRequest{Owner: "u1", ShareWith: "nonexist"})
	assert.False(t, resp2.Success)
	assert.Contains(t, resp2.Error, "does not exist")

	// 18. RootUnshare - owner doesn't exist
	resp3, _ := h.RootUnshare(ctx, &pb.RootUnshareRequest{Owner: "nonexist", UnshareWith: "u2"})
	assert.False(t, resp3.Success)
	assert.Contains(t, resp3.Error, "does not exist")

	// 19. RootUnshare - target doesn't exist
	resp4, _ := h.RootUnshare(ctx, &pb.RootUnshareRequest{Owner: "u1", UnshareWith: "nonexist"})
	assert.False(t, resp4.Success)
	assert.Contains(t, resp4.Error, "does not exist")
}

func TestRobustness_HeartbeatMonitor_Cancel(t *testing.T) {
	ms := &MetaServer{
		store:                  memory.New(),
		heartbeatCheckInterval: 5 * time.Millisecond,
		fileservers:            make(map[uint64]*domain.FileServerInfo),
	}
	cancel := ms.StartHeartbeatMonitor()
	assert.True(t, ms.monitorStarted)
	assert.NotNil(t, ms.stopMonitorCh)

	// Call cancel
	cancel()

	ms.mu.Lock()
	started := ms.monitorStarted
	ms.mu.Unlock()
	assert.False(t, started)

	// Call cancel again should be safe
	cancel()
}

func TestRobustness_GetLeastLoadedHealthyFileServerLocked(t *testing.T) {
	ms := &MetaServer{
		heartbeatTimeout: 30 * time.Second,
	}
	now := time.Now().Unix()

	// 21. no healthy servers
	ms.fileservers = map[uint64]*domain.FileServerInfo{
		1: {UserCount: 0, Status: domain.FileServerStatusStale, LastHeartbeatUnix: now},
	}
	id, ok := ms.getLeastLoadedHealthyFileServerLocked(now)
	assert.False(t, ok)
	assert.Equal(t, uint64(0), id)

	// 22. multiple servers picks lowest userCount
	ms.fileservers = map[uint64]*domain.FileServerInfo{
		1: {UserCount: 10, Status: domain.FileServerStatusHealthy, LastHeartbeatUnix: now},
		2: {UserCount: 2, Status: domain.FileServerStatusHealthy, LastHeartbeatUnix: now},
		3: {UserCount: 5, Status: domain.FileServerStatusHealthy, LastHeartbeatUnix: now},
	}
	id, ok = ms.getLeastLoadedHealthyFileServerLocked(now)
	assert.True(t, ok)
	assert.Equal(t, uint64(2), id) // FS 2 has 2 users
}

// A DHCP lease change must not disturb the node's persisted user count. The
// address-change path used to issue a whole-record upsert whose UserCount
// defaulted to zero, which only became visible after a restart: hydration
// reported the node as empty and the least-loaded picker sent every new user
// to it.
func TestRobustness_Heartbeat_AddressChangePreservesUserCount(t *testing.T) {
	store := memory.New()
	ctx := context.Background()

	ms, err := NewMetaServer(ctx, store)
	require.NoError(t, err)
	h := NewGRPCHandler(ms)

	resp, err := h.RegisterFileServer(ctx, &pb.RegisterFileServerRequest{
		FsId: "fs1", Address: "10.0.0.1:50052", Users: []string{"alice", "bob"},
	})
	require.NoError(t, err)
	require.True(t, resp.Success)

	snap, err := store.LoadSnapshot(ctx)
	require.NoError(t, err)
	require.Len(t, snap.FileServers, 1)
	countBefore := snap.FileServers[0].UserCount
	require.Equal(t, 2, countBefore)

	// Same node, new DHCP lease.
	hbResp, err := h.Heartbeat(ctx, &pb.HeartbeatRequest{FsId: "fs1", Address: "10.0.0.7:50052"})
	require.NoError(t, err)
	require.True(t, hbResp.Success)

	snap, err = store.LoadSnapshot(ctx)
	require.NoError(t, err)
	require.Len(t, snap.FileServers, 1, "an address change must not create a second node")
	assert.Equal(t, "10.0.0.7:50052", snap.FileServers[0].Address, "the new address is persisted")
	assert.Equal(t, countBefore, snap.FileServers[0].UserCount, "the user count must survive an address change")
}

// A registration that conflicts must leave no trace: the old code mutated the
// node record and reassigned an arbitrary prefix of the user list before
// discovering the conflict, then returned without rollback or a store write.
func TestRobustness_Registration_ConflictLeavesNoPartialState(t *testing.T) {
	store := memory.New()
	ctx := context.Background()

	ms, err := NewMetaServer(ctx, store)
	require.NoError(t, err)
	h := NewGRPCHandler(ms)

	// alice lives on fs1.
	resp, err := h.RegisterFileServer(ctx, &pb.RegisterFileServerRequest{
		FsId: "fs1", Address: "10.0.0.1:50052", Users: []string{"alice"},
	})
	require.NoError(t, err)
	require.True(t, resp.Success)

	resp, err = h.RegisterFileServer(ctx, &pb.RegisterFileServerRequest{
		FsId: "fs2", Address: "10.0.0.2:50052", Users: []string{"bob"},
	})
	require.NoError(t, err)
	require.True(t, resp.Success)

	fs1ID, ok := ms.findFileServerByNodeIDLocked("fs1")
	require.True(t, ok)

	// fs2 now claims alice as well. Many users are sent so that, with the old
	// code, map iteration order would reassign some before hitting the conflict.
	resp, err = h.RegisterFileServer(ctx, &pb.RegisterFileServerRequest{
		FsId:    "fs2",
		Address: "10.0.0.2:50052",
		Users:   []string{"u1", "u2", "u3", "u4", "u5", "alice"},
	})
	require.NoError(t, err)
	require.False(t, resp.Success, "a duplicate user must fail the registration")

	// alice must still be on fs1, in memory and in the store.
	assert.Equal(t, fs1ID, ms.users["alice"], "alice must not be moved by a failed registration")

	// None of the other users may have been assigned.
	for _, u := range []string{"u1", "u2", "u3", "u4", "u5"} {
		_, assigned := ms.users[u]
		assert.False(t, assigned, "%s must not be assigned by a failed registration", u)
	}

	snap, err := store.LoadSnapshot(ctx)
	require.NoError(t, err)
	for _, u := range snap.Users {
		assert.NotContains(t, []string{"u1", "u2", "u3", "u4", "u5"}, u.Username,
			"a failed registration must not persist user assignments")
		if u.Username == "alice" {
			assert.Equal(t, "fs1", u.HomeNodeID, "alice's durable assignment must be untouched")
		}
	}
}

// A returning node clears its orphaned accounts even when it reports no users.
//
// req.Users comes from a disk scan, so a node whose data directory was empty or
// not yet mounted at boot registers with an empty user list. Clearing orphans
// only for users named in that list would leave those accounts locked out
// permanently, which is worse than the bug the orphan marker exists to prevent.
func TestRobustness_Registration_ClearsOrphansWhenNodeReportsNoUsers(t *testing.T) {
	store := memory.New()
	ctx := context.Background()

	require.NoError(t, store.AssignUser(ctx, "carol", "fs-returning"))

	ms, err := NewMetaServer(ctx, store)
	require.NoError(t, err)
	require.Contains(t, ms.orphanedUsers, "carol")

	h := NewGRPCHandler(ms)
	// Note: no Users field -- the node reports nothing, as an empty data
	// directory would.
	resp, err := h.RegisterFileServer(ctx, &pb.RegisterFileServerRequest{
		FsId: "fs-returning", Address: "10.0.0.9:50052",
	})
	require.NoError(t, err)
	require.True(t, resp.Success)

	assert.NotContains(t, ms.orphanedUsers, "carol",
		"a returning node must clear its orphans even when it reports no users")
	_, routed := ms.users["carol"]
	assert.True(t, routed, "carol must be routable again")

	rootsResp, err := h.GetRoots(ctx, &pb.GetRootsRequest{Username: "carol"})
	require.NoError(t, err)
	assert.True(t, rootsResp.Success, "carol must be able to log in once her node is back")

	// An unrelated node registering must NOT clear her marker.
	store2 := memory.New()
	require.NoError(t, store2.AssignUser(ctx, "dave", "fs-gone"))
	ms2, err := NewMetaServer(ctx, store2)
	require.NoError(t, err)
	h2 := NewGRPCHandler(ms2)
	_, err = h2.RegisterFileServer(ctx, &pb.RegisterFileServerRequest{
		FsId: "fs-other", Address: "10.0.0.8:50052",
	})
	require.NoError(t, err)
	assert.Contains(t, ms2.orphanedUsers, "dave",
		"an unrelated node must not make dave routable to the wrong host")
}
