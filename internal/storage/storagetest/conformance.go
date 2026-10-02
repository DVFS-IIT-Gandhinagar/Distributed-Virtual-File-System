// Package storagetest holds the behavioural contract every storage backend must
// satisfy. Both the in-memory double and the Mongo backend run this same suite,
// so the double cannot silently drift from production semantics.
package storagetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/storage"
)

// Factory returns a fresh, empty MetaStore for one subtest.
type Factory func(t *testing.T) storage.MetaStore

// RunMetaStoreConformance exercises the MetaStore contract against one backend.
func RunMetaStoreConformance(t *testing.T, newStore Factory) {
	t.Helper()

	tests := []struct {
		name string
		fn   func(t *testing.T, s storage.MetaStore)
	}{
		{"EmptySnapshot", testEmptySnapshot},
		{"NumericIDAllocation", testNumericIDAllocation},
		{"NodeIdentitySurvivesAddressChange", testNodeIdentitySurvivesAddressChange},
		{"RemoveFileServer", testRemoveFileServer},
		{"Heartbeat", testHeartbeat},
		{"HeartbeatUnknownNode", testHeartbeatUnknownNode},
		{"StatusAndUserCount", testStatusAndUserCount},
		{"UserAssignment", testUserAssignment},
		{"ShareIdempotent", testShareIdempotent},
		{"MultipleSharesFromSameOwner", testMultipleSharesFromSameOwner},
		{"ShareRemovalIgnoresPathFormatting", testShareRemovalIgnoresPathFormatting},
		{"RemoveSharesInvolving", testRemoveSharesInvolving},
		{"RemoveSharesByOwner", testRemoveSharesByOwner},
		{"SnapshotRoundTrip", testSnapshotRoundTrip},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore(t)
			tc.fn(t, s)
		})
	}
}

func ctx(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return c
}

func testEmptySnapshot(t *testing.T, s storage.MetaStore) {
	snap, err := s.LoadSnapshot(ctx(t))
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	if len(snap.FileServers) != 0 || len(snap.Users) != 0 || len(snap.Shares) != 0 {
		t.Fatalf("expected empty snapshot, got %d nodes / %d users / %d shares",
			len(snap.FileServers), len(snap.Users), len(snap.Shares))
	}
}

func testNumericIDAllocation(t *testing.T, s storage.MetaStore) {
	c := ctx(t)
	first, err := s.UpsertFileServer(c, storage.FileServerRecord{NodeID: "fs1", Address: "10.0.0.1:50052", Status: "healthy"})
	if err != nil {
		t.Fatalf("UpsertFileServer fs1: %v", err)
	}
	second, err := s.UpsertFileServer(c, storage.FileServerRecord{NodeID: "fs2", Address: "10.0.0.2:50052", Status: "healthy"})
	if err != nil {
		t.Fatalf("UpsertFileServer fs2: %v", err)
	}
	if first == second {
		t.Fatalf("distinct nodes shared numeric id %d", first)
	}
	// Re-upserting must not burn a new id.
	again, err := s.UpsertFileServer(c, storage.FileServerRecord{NodeID: "fs1", Address: "10.0.0.1:50052", Status: "healthy"})
	if err != nil {
		t.Fatalf("re-upsert fs1: %v", err)
	}
	if again != first {
		t.Fatalf("numeric id changed on re-upsert: got %d want %d", again, first)
	}
}

// testNodeIdentitySurvivesAddressChange checks that a node is keyed on its
// stable id, not its address: re-registering with a new address updates the
// record in place and keeps its numeric id instead of creating a second node.
func testNodeIdentitySurvivesAddressChange(t *testing.T, s storage.MetaStore) {
	c := ctx(t)
	original, err := s.UpsertFileServer(c, storage.FileServerRecord{
		NodeID: "fs1", Address: "10.7.52.85:50052", UserCount: 42, Status: "healthy",
	})
	if err != nil {
		t.Fatalf("initial upsert: %v", err)
	}

	moved, err := s.UpsertFileServer(c, storage.FileServerRecord{
		NodeID: "fs1", Address: "10.0.171.38:50052", UserCount: 42, Status: "healthy",
	})
	if err != nil {
		t.Fatalf("upsert after address change: %v", err)
	}
	if moved != original {
		t.Fatalf("address change allocated a new numeric id: got %d want %d", moved, original)
	}

	snap, err := s.LoadSnapshot(c)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	if len(snap.FileServers) != 1 {
		t.Fatalf("address change created a phantom node: %d nodes present", len(snap.FileServers))
	}
	if got := snap.FileServers[0].Address; got != "10.0.171.38:50052" {
		t.Fatalf("address not updated in place: got %q", got)
	}
}

func testRemoveFileServer(t *testing.T, s storage.MetaStore) {
	c := ctx(t)
	fs1ID, err := s.UpsertFileServer(c, storage.FileServerRecord{
		NodeID: "fs1", Address: "10.0.0.1:50052", Status: "healthy",
	})
	if err != nil {
		t.Fatalf("upsert fs1: %v", err)
	}
	fs2ID, err := s.UpsertFileServer(c, storage.FileServerRecord{
		NodeID: "fs2", Address: "10.0.0.2:50052", Status: "healthy",
	})
	if err != nil {
		t.Fatalf("upsert fs2: %v", err)
	}
	if err := s.AssignUser(c, "alice", "fs1"); err != nil {
		t.Fatalf("assign alice: %v", err)
	}

	if err := s.RemoveFileServer(c, "fs1"); err != nil {
		t.Fatalf("remove fs1: %v", err)
	}

	snap, err := s.LoadSnapshot(c)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	if len(snap.FileServers) != 1 || snap.FileServers[0].NodeID != "fs2" {
		t.Fatalf("expected only fs2 to remain, got %+v", snap.FileServers)
	}

	// The contract is deliberately narrow: the node document goes, nothing
	// else does. Cascading to users here would hide the ordering the caller
	// relies on for a safe partial failure.
	if len(snap.Users) != 1 || snap.Users[0].Username != "alice" || snap.Users[0].HomeNodeID != "fs1" {
		t.Fatalf("RemoveFileServer must not touch user records, got %+v", snap.Users)
	}

	// Removing an absent node is a no-op, so a decommission can be retried.
	if err := s.RemoveFileServer(c, "fs1"); err != nil {
		t.Fatalf("second remove of fs1 must be idempotent: %v", err)
	}
	if err := s.RemoveFileServer(c, "never-registered"); err != nil {
		t.Fatalf("remove of unknown node must be idempotent: %v", err)
	}

	// Numeric ids are never recycled. The admin console keys its removal
	// tombstones on the numeric id, so a recycled id would let a brand-new
	// node inherit a tombstone and be hidden from the console.
	fs1Again, err := s.UpsertFileServer(c, storage.FileServerRecord{
		NodeID: "fs1", Address: "10.0.0.1:50052", Status: "healthy",
	})
	if err != nil {
		t.Fatalf("re-upsert fs1: %v", err)
	}
	if fs1Again == fs1ID || fs1Again == fs2ID {
		t.Fatalf("numeric id recycled after removal: got %d (old fs1=%d, fs2=%d)", fs1Again, fs1ID, fs2ID)
	}
}

func testHeartbeat(t *testing.T, s storage.MetaStore) {
	c := ctx(t)
	if _, err := s.UpsertFileServer(c, storage.FileServerRecord{NodeID: "fs1", Address: "a:1", Status: "stale"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	at := time.Unix(1776323757, 0)
	if err := s.RecordHeartbeat(c, "fs1", at, "healthy"); err != nil {
		t.Fatalf("RecordHeartbeat: %v", err)
	}
	snap, err := s.LoadSnapshot(c)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	rec := snap.FileServers[0]
	if rec.LastHeartbeatUnix != at.Unix() {
		t.Fatalf("heartbeat not recorded: got %d want %d", rec.LastHeartbeatUnix, at.Unix())
	}
	if rec.Status != "healthy" {
		t.Fatalf("status not updated: got %q", rec.Status)
	}
}

func testHeartbeatUnknownNode(t *testing.T, s storage.MetaStore) {
	err := s.RecordHeartbeat(ctx(t), "nope", time.Now(), "healthy")
	if !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for unknown node, got %v", err)
	}
}

func testStatusAndUserCount(t *testing.T, s storage.MetaStore) {
	c := ctx(t)
	if _, err := s.UpsertFileServer(c, storage.FileServerRecord{NodeID: "fs1", Address: "a:1", Status: "healthy"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := s.SetFileServerStatus(c, "fs1", "stale"); err != nil {
		t.Fatalf("SetFileServerStatus: %v", err)
	}
	if err := s.SetUserCount(c, "fs1", 7); err != nil {
		t.Fatalf("SetUserCount: %v", err)
	}
	snap, _ := s.LoadSnapshot(c)
	if snap.FileServers[0].Status != "stale" {
		t.Fatalf("status: got %q want stale", snap.FileServers[0].Status)
	}
	if snap.FileServers[0].UserCount != 7 {
		t.Fatalf("user count: got %d want 7", snap.FileServers[0].UserCount)
	}
}

func testUserAssignment(t *testing.T, s storage.MetaStore) {
	c := ctx(t)
	if err := s.AssignUser(c, "alice@x.edu", "fs1"); err != nil {
		t.Fatalf("AssignUser alice: %v", err)
	}
	if err := s.AssignUser(c, "bob@x.edu", "fs1"); err != nil {
		t.Fatalf("AssignUser bob: %v", err)
	}
	// Reassignment overwrites rather than duplicating.
	if err := s.AssignUser(c, "alice@x.edu", "fs2"); err != nil {
		t.Fatalf("reassign alice: %v", err)
	}

	snap, _ := s.LoadSnapshot(c)
	if len(snap.Users) != 2 {
		t.Fatalf("expected 2 users, got %d", len(snap.Users))
	}
	homes := map[string]string{}
	for _, u := range snap.Users {
		homes[u.Username] = u.HomeNodeID
	}
	if homes["alice@x.edu"] != "fs2" {
		t.Fatalf("alice not reassigned: %q", homes["alice@x.edu"])
	}

	if err := s.RemoveUsers(c, []string{"bob@x.edu"}); err != nil {
		t.Fatalf("RemoveUsers: %v", err)
	}
	snap, _ = s.LoadSnapshot(c)
	if len(snap.Users) != 1 || snap.Users[0].Username != "alice@x.edu" {
		t.Fatalf("RemoveUsers left %+v", snap.Users)
	}
}

func testShareIdempotent(t *testing.T, s storage.MetaStore) {
	c := ctx(t)
	sh := storage.ShareRecord{Grantee: "bob", Owner: "alice", Path: "alice/proj", DisplayName: "proj"}
	for i := 0; i < 3; i++ {
		if err := s.AddShare(c, sh); err != nil {
			t.Fatalf("AddShare #%d: %v", i, err)
		}
	}
	snap, _ := s.LoadSnapshot(c)
	if len(snap.Shares) != 1 {
		t.Fatalf("AddShare not idempotent: %d shares", len(snap.Shares))
	}
}

// testMultipleSharesFromSameOwner checks that shares are keyed on
// (grantee, owner, path), so one owner can share several directories with the
// same grantee and each is stored as its own grant.
func testMultipleSharesFromSameOwner(t *testing.T, s storage.MetaStore) {
	c := ctx(t)
	if err := s.AddShare(c, storage.ShareRecord{Grantee: "bob", Owner: "alice", Path: "alice/proj1", DisplayName: "proj1"}); err != nil {
		t.Fatalf("AddShare proj1: %v", err)
	}
	if err := s.AddShare(c, storage.ShareRecord{Grantee: "bob", Owner: "alice", Path: "alice/proj2", DisplayName: "proj2"}); err != nil {
		t.Fatalf("AddShare proj2: %v", err)
	}
	snap, _ := s.LoadSnapshot(c)
	if len(snap.Shares) != 2 {
		t.Fatalf("second share from same owner was dropped: %d shares, %+v", len(snap.Shares), snap.Shares)
	}
}

// testShareRemovalIgnoresPathFormatting checks that a share stored with one
// path spelling ("/alice/proj") is revoked by a request using another
// ("alice/proj"), because every path is normalised before it reaches the key.
func testShareRemovalIgnoresPathFormatting(t *testing.T, s storage.MetaStore) {
	c := ctx(t)
	if err := s.AddShare(c, storage.ShareRecord{Grantee: "bob", Owner: "alice", Path: "/alice/proj", DisplayName: "proj"}); err != nil {
		t.Fatalf("AddShare: %v", err)
	}
	if err := s.RemoveShare(c, "bob", "alice", "alice/proj"); err != nil {
		t.Fatalf("RemoveShare: %v", err)
	}
	snap, _ := s.LoadSnapshot(c)
	if len(snap.Shares) != 0 {
		t.Fatalf("unshare did not revoke access: %+v", snap.Shares)
	}
}

func testRemoveSharesInvolving(t *testing.T, s storage.MetaStore) {
	c := ctx(t)
	shares := []storage.ShareRecord{
		{Grantee: "bob", Owner: "alice", Path: "alice/a", DisplayName: "a"},
		{Grantee: "carol", Owner: "alice", Path: "alice/b", DisplayName: "b"},
		{Grantee: "alice", Owner: "dave", Path: "dave/c", DisplayName: "c"},
		{Grantee: "carol", Owner: "dave", Path: "dave/d", DisplayName: "d"},
	}
	for _, sh := range shares {
		if err := s.AddShare(c, sh); err != nil {
			t.Fatalf("AddShare %+v: %v", sh, err)
		}
	}
	if err := s.RemoveSharesInvolving(c, "alice"); err != nil {
		t.Fatalf("RemoveSharesInvolving: %v", err)
	}
	snap, _ := s.LoadSnapshot(c)
	if len(snap.Shares) != 1 {
		t.Fatalf("expected only dave->carol to survive, got %+v", snap.Shares)
	}
	if snap.Shares[0].Owner != "dave" || snap.Shares[0].Grantee != "carol" {
		t.Fatalf("wrong survivor: %+v", snap.Shares[0])
	}
}

// testRemoveSharesByOwner covers the registration rebuild: a node republishes
// the shares its users own, so only those may be cleared. Grants those same
// users merely receive are owned by other nodes and must survive.
func testRemoveSharesByOwner(t *testing.T, s storage.MetaStore) {
	c := ctx(t)
	shares := []storage.ShareRecord{
		{Grantee: "bob", Owner: "alice", Path: "alice/a", DisplayName: "a"},
		{Grantee: "carol", Owner: "alice", Path: "alice/b", DisplayName: "b"},
		{Grantee: "alice", Owner: "dave", Path: "dave/c", DisplayName: "c"},
	}
	for _, sh := range shares {
		if err := s.AddShare(c, sh); err != nil {
			t.Fatalf("AddShare %+v: %v", sh, err)
		}
	}
	if err := s.RemoveSharesByOwner(c, "alice"); err != nil {
		t.Fatalf("RemoveSharesByOwner: %v", err)
	}
	snap, _ := s.LoadSnapshot(c)
	if len(snap.Shares) != 1 {
		t.Fatalf("expected dave->alice to survive, got %+v", snap.Shares)
	}
	if snap.Shares[0].Owner != "dave" || snap.Shares[0].Grantee != "alice" {
		t.Fatalf("wrong survivor: %+v", snap.Shares[0])
	}
}

func testSnapshotRoundTrip(t *testing.T, s storage.MetaStore) {
	c := ctx(t)
	if _, err := s.UpsertFileServer(c, storage.FileServerRecord{
		NodeID: "fs1", Address: "10.0.0.1:50052", UserCount: 2,
		LastHeartbeatUnix: 1776323757, Status: "healthy",
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := s.AssignUser(c, "alice", "fs1"); err != nil {
		t.Fatalf("AssignUser: %v", err)
	}
	if err := s.AddShare(c, storage.ShareRecord{
		Grantee: "bob", Owner: "alice", Path: "alice/shared", DisplayName: "shared",
	}); err != nil {
		t.Fatalf("AddShare: %v", err)
	}

	snap, err := s.LoadSnapshot(c)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	if len(snap.FileServers) != 1 || len(snap.Users) != 1 || len(snap.Shares) != 1 {
		t.Fatalf("unexpected snapshot: %+v", snap)
	}
	fs := snap.FileServers[0]
	if fs.NodeID != "fs1" || fs.Address != "10.0.0.1:50052" || fs.UserCount != 2 ||
		fs.LastHeartbeatUnix != 1776323757 || fs.Status != "healthy" {
		t.Fatalf("fileserver round-trip lost data: %+v", fs)
	}
	sh := snap.Shares[0]
	if sh.Grantee != "bob" || sh.Owner != "alice" || sh.Path != "alice/shared" || sh.DisplayName != "shared" {
		t.Fatalf("share round-trip lost data: %+v", sh)
	}
}
