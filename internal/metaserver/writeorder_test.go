package metaserver

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	pb "github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/api/metaserver"
	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/storage/memory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// hookStore is the memory store with a few seams for driving the handlers
// into the interleavings the write-ordering rules exist for.
type hookStore struct {
	*memory.Store
	mu sync.Mutex

	onAssignUser func() // runs before the write; may block

	failOnce map[string]error // method name -> error returned on its next call
}

func newHookStore() *hookStore {
	return &hookStore{Store: memory.New(), failOnce: map[string]error{}}
}

func (s *hookStore) takeFailure(method string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.failOnce[method]
	delete(s.failOnce, method)
	return err
}

func (s *hookStore) AssignUser(ctx context.Context, username, nodeID string) error {
	if s.onAssignUser != nil {
		s.onAssignUser()
	}
	return s.Store.AssignUser(ctx, username, nodeID)
}

func (s *hookStore) RemoveFileServer(ctx context.Context, nodeID string) error {
	if err := s.takeFailure("RemoveFileServer"); err != nil {
		return err
	}
	return s.Store.RemoveFileServer(ctx, nodeID)
}

func (s *hookStore) RemoveUsers(ctx context.Context, usernames []string) error {
	if err := s.takeFailure("RemoveUsers"); err != nil {
		return err
	}
	return s.Store.RemoveUsers(ctx, usernames)
}

func (s *hookStore) RemoveSharesInvolving(ctx context.Context, usernames ...string) error {
	if err := s.takeFailure("RemoveSharesInvolving"); err != nil {
		return err
	}
	return s.Store.RemoveSharesInvolving(ctx, usernames...)
}

// A first login places the user in memory, releases the data lock, then
// writes the assignment. If an admin deregisters that node in between, the
// deregistration's RemoveUsers must not be overtaken by the login's
// AssignUser: the store would then hold a user homed on a node it no longer
// has, and after a restart that user is orphaned on a node that never
// returns. Structural writes serialise on writeOrderMu, and a first login is
// a structural write.
func TestWriteOrder_LoginRacingDeregisterCannotStrandUser(t *testing.T) {
	store := newHookStore()
	ctx := context.Background()
	ms, err := NewMetaServer(ctx, store)
	require.NoError(t, err)
	h := NewGRPCHandler(ms)

	require.True(t, register(t, h, "fs1", "10.0.0.1:50052").Success)
	require.True(t, register(t, h, "fs2", "10.0.0.2:50052").Success)

	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	store.onAssignUser = func() {
		once.Do(func() { close(entered) })
		<-release
	}

	loginDone := make(chan *pb.GetRootsResponse, 1)
	go func() {
		resp, _ := h.GetRoots(ctx, &pb.GetRootsRequest{Username: "alice"})
		loginDone <- resp
	}()
	<-entered // alice is placed in memory; her assignment write is in flight

	ms.mu.RLock()
	homeNumeric := ms.users["alice"]
	home := ms.fileservers[homeNumeric].NodeID
	ms.mu.RUnlock()

	deregDone := make(chan *pb.DeregisterFileServerResponse, 1)
	go func() {
		resp, _ := h.DeregisterFileServer(ctx, &pb.DeregisterFileServerRequest{FsId: home})
		deregDone <- resp
	}()

	select {
	case <-deregDone:
		t.Fatal("deregistration completed while a login's assignment write was still in flight")
	case <-time.After(150 * time.Millisecond):
	}

	close(release)
	login := <-loginDone
	dereg := <-deregDone
	require.True(t, login.Success)
	require.True(t, dereg.Success)

	homes := storeHomes(t, store)
	nodes := storeNodes(t, store)
	if h, ok := homes["alice"]; ok {
		assert.Contains(t, nodes, h, "store homes alice on a node it no longer has")
	}
	ms.mu.RLock()
	_, inMemory := ms.users["alice"]
	ms.mu.RUnlock()
	_, inStore := homes["alice"]
	assert.Equal(t, inMemory, inStore, "memory and store disagree about whether alice is assigned")

	ms2, err := NewMetaServer(ctx, store)
	require.NoError(t, err)
	assert.Empty(t, ms2.orphanedUsers, "a restart orphaned a user who was deregistered")
}

// Deregistration is three store writes. Whichever one fails, a restart from
// the half-written store must not orphan anyone, and retrying the
// deregistration must converge.
func TestWriteOrder_DeregisterRetryConvergesAfterAnyStoreFailure(t *testing.T) {
	for _, failing := range []string{"RemoveUsers", "RemoveSharesInvolving", "RemoveFileServer"} {
		t.Run(failing, func(t *testing.T) {
			store := newHookStore()
			ctx := context.Background()
			ms, err := NewMetaServer(ctx, store)
			require.NoError(t, err)
			h := NewGRPCHandler(ms)

			require.True(t, register(t, h, "fs1", "10.0.0.1:50052", "alice").Success)
			require.True(t, register(t, h, "fs2", "10.0.0.2:50052", "bob").Success)
			share, err := h.RootShare(ctx, &pb.RootShareRequest{Owner: "alice", RootPath: "alice/proj", ShareWith: "bob", Name: "proj"})
			require.NoError(t, err)
			require.True(t, share.Success)

			store.failOnce[failing] = errors.New("injected " + failing + " failure")
			first, err := h.DeregisterFileServer(ctx, &pb.DeregisterFileServerRequest{FsId: "fs1", Address: "10.0.0.1:50052"})
			require.NoError(t, err)
			require.False(t, first.Success, "the failed write must be reported")

			// The in-memory picture is rolled back so a retry re-issues everything.
			mem := memNodes(ms)
			assert.Contains(t, mem, "fs1")
			ms.mu.RLock()
			_, aliceMapped := ms.users["alice"]
			bobShares := len(ms.shared["bob"])
			ms.mu.RUnlock()
			assert.True(t, aliceMapped)
			assert.Equal(t, 1, bobShares, "bob's grant from alice must be restored")

			// Whatever got through, a restart must not produce an orphan.
			ms2, err := NewMetaServer(ctx, store)
			require.NoError(t, err)
			assert.Empty(t, ms2.orphanedUsers, "partial deregistration orphaned a user (failed write: %s)", failing)

			second, err := h.DeregisterFileServer(ctx, &pb.DeregisterFileServerRequest{FsId: "fs1", Address: "10.0.0.1:50052"})
			require.NoError(t, err)
			require.True(t, second.Success)

			assert.NotContains(t, storeNodes(t, store), "fs1")
			assert.Equal(t, map[string]string{"bob": "fs2"}, storeHomes(t, store))
			snap, _ := store.LoadSnapshot(ctx)
			assert.Empty(t, snap.Shares)
			assert.NotContains(t, memNodes(ms), "fs1")
		})
	}
}
