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

// Node identity is the fileserver's -id. These tests pin down what happens
// when two processes claim the same id, when an id shows up at an address
// another node used to have, and when a pre-fs_id node upgrades.

func register(t *testing.T, h *GRPCHandler, fsID, addr string, users ...string) *pb.RegisterFileServerResponse {
	t.Helper()
	resp, err := h.RegisterFileServer(context.Background(), &pb.RegisterFileServerRequest{
		FsId: fsID, Address: addr, Users: users,
	})
	require.NoError(t, err)
	return resp
}

func storeNodes(t *testing.T, s storage.MetaStore) map[string]storage.FileServerRecord {
	t.Helper()
	snap, err := s.LoadSnapshot(context.Background())
	require.NoError(t, err)
	out := map[string]storage.FileServerRecord{}
	for _, rec := range snap.FileServers {
		out[rec.NodeID] = rec
	}
	return out
}

func storeHomes(t *testing.T, s storage.MetaStore) map[string]string {
	t.Helper()
	snap, err := s.LoadSnapshot(context.Background())
	require.NoError(t, err)
	out := map[string]string{}
	for _, u := range snap.Users {
		out[u.Username] = u.HomeNodeID
	}
	return out
}

func memNodes(ms *MetaServer) map[string]domain.FileServerInfo {
	ms.mu.RLock()
	defer ms.mu.RUnlock()
	out := map[string]domain.FileServerInfo{}
	for _, info := range ms.fileservers {
		out[info.NodeID] = *info
	}
	return out
}

// forceStale is what the heartbeat monitor does to a node that stopped
// reporting: memory first, then the status persisted.
func forceStale(ms *MetaServer, nodeID string) {
	ms.mu.Lock()
	id, _ := ms.findFileServerByNodeIDLocked(nodeID)
	ms.fileservers[id].LastHeartbeatUnix = time.Now().Unix() - 3600
	ms.fileservers[id].Status = domain.FileServerStatusStale
	ms.mu.Unlock()
	_ = ms.store.SetFileServerStatus(context.Background(), nodeID, domain.FileServerStatusStale)
}

// Two machines both started with -id=fs1 (the flag's default). The second
// must be refused while the first is alive; otherwise they share one record
// and every heartbeat flips its address, routing users to the wrong disk.
func TestIdentity_DuplicateFsIDFromDifferentAddressIsRejected(t *testing.T) {
	store := memory.New()
	ms, err := NewMetaServer(context.Background(), store)
	require.NoError(t, err)
	h := NewGRPCHandler(ms)

	require.True(t, register(t, h, "fs1", "10.0.0.1:50052", "alice").Success)

	resp := register(t, h, "fs1", "10.0.0.2:50052", "bob")
	require.False(t, resp.Success, "a second live claimant of fs1 must be refused")
	assert.Contains(t, resp.Error, "fs1")
	assert.Contains(t, resp.Error, "10.0.0.1:50052")

	nodes := memNodes(ms)
	require.Len(t, nodes, 1)
	assert.Equal(t, "10.0.0.1:50052", nodes["fs1"].Address, "the first registration keeps the identity")
	assert.Equal(t, map[string]string{"alice": "fs1"}, storeHomes(t, store), "bob must not have been assigned anywhere")
	assert.Len(t, storeNodes(t, store), 1)

	// The impostor's heartbeats must not refresh or re-address the real node.
	hb, err := h.Heartbeat(context.Background(), &pb.HeartbeatRequest{FsId: "fs1", Address: "10.0.0.2:50052"})
	require.NoError(t, err)
	require.False(t, hb.Success)
	assert.Equal(t, "10.0.0.1:50052", memNodes(ms)["fs1"].Address)
	assert.Equal(t, "10.0.0.1:50052", storeNodes(t, store)["fs1"].Address)

	nav, err := h.Navigate(context.Background(), &pb.NavigateRequest{Username: "alice", RootUser: "alice"})
	require.NoError(t, err)
	require.True(t, nav.Success)
	assert.Equal(t, "10.0.0.1:50052", nav.Address, "alice is still routed to the machine holding her data")
}

// A genuine move: the node restarted on a new DHCP lease. Once its old
// registration has gone stale the same id re-registers from the new address,
// keeps its numeric id and keeps its users.
func TestIdentity_StaleNodeCanReRegisterFromNewAddress(t *testing.T) {
	store := memory.New()
	ms, err := NewMetaServer(context.Background(), store)
	require.NoError(t, err)
	h := NewGRPCHandler(ms)

	require.True(t, register(t, h, "fs1", "10.0.0.1:50052", "alice", "bob").Success)
	before := storeNodes(t, store)["fs1"]
	forceStale(ms, "fs1")

	require.True(t, register(t, h, "fs1", "10.0.0.7:50052", "alice", "bob").Success)

	nodes := storeNodes(t, store)
	require.Len(t, nodes, 1, "an address change must not create a second node")
	assert.Equal(t, "10.0.0.7:50052", nodes["fs1"].Address)
	assert.Equal(t, before.NumericID, nodes["fs1"].NumericID)
	assert.Equal(t, map[string]string{"alice": "fs1", "bob": "fs1"}, storeHomes(t, store))
	assert.Equal(t, 2, memNodes(ms)["fs1"].UserCount)
}

// fs2 comes up on the ip:port fs1 used to have. It is a different machine
// with a different disk, so it must become a second node; alice stays on fs1
// and is simply unavailable until fs1 returns.
func TestIdentity_NewFsIDAtReusedAddressIsANewNode(t *testing.T) {
	store := memory.New()
	ms, err := NewMetaServer(context.Background(), store)
	require.NoError(t, err)
	h := NewGRPCHandler(ms)

	require.True(t, register(t, h, "fs1", "10.0.0.5:50052", "alice").Success)
	fs1Numeric := storeNodes(t, store)["fs1"].NumericID
	forceStale(ms, "fs1")

	require.True(t, register(t, h, "fs2", "10.0.0.5:50052", "bob").Success)

	mem := memNodes(ms)
	require.Len(t, mem, 2, "fs2 must not adopt fs1's entry")
	assert.Equal(t, "fs1", mem["fs1"].NodeID)
	assert.Equal(t, 1, mem["fs1"].UserCount)
	assert.Equal(t, 1, mem["fs2"].UserCount)

	nodes := storeNodes(t, store)
	require.Len(t, nodes, 2)
	assert.Equal(t, fs1Numeric, nodes["fs1"].NumericID)
	assert.NotEqual(t, fs1Numeric, nodes["fs2"].NumericID)
	assert.Equal(t, map[string]string{"alice": "fs1", "bob": "fs2"}, storeHomes(t, store))

	// Memory and store agree, so a restart reproduces the same picture.
	ms2, err := NewMetaServer(context.Background(), store)
	require.NoError(t, err)
	assert.Len(t, ms2.orphanedUsers, 0)
	after := memNodes(ms2)
	require.Len(t, after, 2)
	assert.Equal(t, 1, after["fs1"].UserCount)
	assert.Equal(t, 1, after["fs2"].UserCount)
	assert.Equal(t, domain.FileServerStatusStale, after["fs1"].Status)
	assert.Equal(t, domain.FileServerStatusHealthy, after["fs2"].Status)
}

// A fileserver built before fs_id existed registered as "addr:<address>".
// When it is upgraded and reports -id=fs1 from the same address, the record
// is re-keyed in memory and in the store, with the same numeric id and users.
func TestIdentity_LegacyAdoptionIsPersisted(t *testing.T) {
	store := memory.New()
	ms, err := NewMetaServer(context.Background(), store)
	require.NoError(t, err)
	h := NewGRPCHandler(ms)

	require.True(t, register(t, h, "", "10.0.0.5:50052", "alice").Success)
	legacy := storeNodes(t, store)["addr:10.0.0.5:50052"]
	require.NotEmpty(t, legacy.NodeID)

	require.True(t, register(t, h, "fs1", "10.0.0.5:50052", "alice").Success)

	mem := memNodes(ms)
	require.Len(t, mem, 1)
	assert.Equal(t, 1, mem["fs1"].UserCount)

	nodes := storeNodes(t, store)
	require.Len(t, nodes, 1, "the addr:-keyed record must be replaced, not left as a ghost")
	assert.Equal(t, legacy.NumericID, nodes["fs1"].NumericID)
	assert.Equal(t, map[string]string{"alice": "fs1"}, storeHomes(t, store))

	ms2, err := NewMetaServer(context.Background(), store)
	require.NoError(t, err)
	assert.Len(t, ms2.orphanedUsers, 0)
	assert.Len(t, memNodes(ms2), 1)
	assert.Equal(t, 1, memNodes(ms2)["fs1"].UserCount)
}

// Validation runs before adoption: a rejected upgrade registration must leave
// the legacy entry exactly as it was, in memory and in the store.
func TestIdentity_RejectedAdoptionLeavesLegacyNodeUntouched(t *testing.T) {
	store := memory.New()
	ms, err := NewMetaServer(context.Background(), store)
	require.NoError(t, err)
	h := NewGRPCHandler(ms)

	require.True(t, register(t, h, "", "10.0.0.5:50052", "alice").Success)
	require.True(t, register(t, h, "fs2", "10.0.0.6:50052", "bob").Success)

	// fs1 upgrades at the legacy address but wrongly claims bob, who lives on fs2.
	resp := register(t, h, "fs1", "10.0.0.5:50052", "alice", "bob")
	require.False(t, resp.Success)

	mem := memNodes(ms)
	_, renamed := mem["fs1"]
	assert.False(t, renamed, "a rejected registration renamed the legacy node in memory")
	assert.Contains(t, mem, "addr:10.0.0.5:50052")
	assert.Contains(t, storeNodes(t, store), "addr:10.0.0.5:50052")
	assert.Equal(t, map[string]string{"alice": "addr:10.0.0.5:50052", "bob": "fs2"}, storeHomes(t, store))

	// Heartbeats from the still-running legacy process keep working.
	hb, err := h.Heartbeat(context.Background(), &pb.HeartbeatRequest{Address: "10.0.0.5:50052"})
	require.NoError(t, err)
	assert.True(t, hb.Success)
}

// The admin always sends both the stable id and the address. An id the
// metaserver does not know must not fall through to whichever live node now
// holds that address.
func TestIdentity_DeregisterIgnoresAddressWhenFsIDUnknown(t *testing.T) {
	store := memory.New()
	ms, err := NewMetaServer(context.Background(), store)
	require.NoError(t, err)
	h := NewGRPCHandler(ms)

	require.True(t, register(t, h, "fs1", "10.0.0.5:50052", "alice").Success)

	resp, err := h.DeregisterFileServer(context.Background(), &pb.DeregisterFileServerRequest{
		FsId: "fs-gone", Address: "10.0.0.5:50052",
	})
	require.NoError(t, err)
	assert.True(t, resp.Success, "deregistering an unknown node is idempotent")
	assert.Len(t, memNodes(ms), 1, "fs1 must survive a deregister aimed at another id")
	assert.Equal(t, map[string]string{"alice": "fs1"}, storeHomes(t, store))

	// Address-only is still honoured for callers that predate fs_id.
	resp, err = h.DeregisterFileServer(context.Background(), &pb.DeregisterFileServerRequest{Address: "10.0.0.5:50052"})
	require.NoError(t, err)
	assert.True(t, resp.Success)
	assert.Len(t, memNodes(ms), 0)
	assert.Empty(t, storeHomes(t, store))
}

// A heartbeat carrying an unknown id must not refresh the node that happens
// to share its address; the sender has to register first.
func TestIdentity_HeartbeatFromUnknownFsIDIsRejected(t *testing.T) {
	store := memory.New()
	ms, err := NewMetaServer(context.Background(), store)
	require.NoError(t, err)
	h := NewGRPCHandler(ms)

	require.True(t, register(t, h, "fs1", "10.0.0.5:50052", "alice").Success)
	forceStale(ms, "fs1")

	hb, err := h.Heartbeat(context.Background(), &pb.HeartbeatRequest{FsId: "fs2", Address: "10.0.0.5:50052"})
	require.NoError(t, err)
	assert.False(t, hb.Success)
	assert.Equal(t, domain.FileServerStatusStale, memNodes(ms)["fs1"].Status, "fs1 must not have been revived by fs2's heartbeat")
	assert.Equal(t, domain.FileServerStatusStale, storeNodes(t, store)["fs1"].Status)
}
