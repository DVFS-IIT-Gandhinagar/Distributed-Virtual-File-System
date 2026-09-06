package metaserver

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	pb "github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/api/metaserver"
	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRobustness_Contains(t *testing.T) {
	entries := []SharedDirEntry{
		{Owner: "alice"},
		{Owner: "bob"},
	}

	assert.True(t, contains(entries, "alice"))
	assert.True(t, contains(entries, "bob"))
	assert.False(t, contains(entries, "charlie"))
}

func TestRobustness_RemoveValue(t *testing.T) {
	entries := []SharedDirEntry{
		{Owner: "alice", Path: "a"},
		{Owner: "bob", Path: "b"},
		{Owner: "alice", Path: "a2"},
	}

	// Remove existing
	res := removeValue(entries, "alice")
	require.Len(t, res, 1)
	assert.Equal(t, "bob", res[0].Owner)

	// Remove non-existing
	res2 := removeValue(entries, "charlie")
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

func TestRobustness_LoadState_CorruptedJSON(t *testing.T) {
	tmpDir := t.TempDir()
	stateFile := filepath.Join(tmpDir, "corrupted.json")
	err := os.WriteFile(stateFile, []byte("{corrupt json"), 0644)
	require.NoError(t, err)

	ms := &MetaServer{stateFile: stateFile}
	err = ms.loadState()
	assert.Error(t, err) // logs warning and returns error which we check
}

func TestRobustness_LoadState_NilEntriesCleanup(t *testing.T) {
	tmpDir := t.TempDir()
	stateFile := filepath.Join(tmpDir, "state.json")

	// Create JSON with a null entry in fileservers
	data := []byte(`{"fileservers":{"1":null,"2":{"address":"127.0.0.1:8080"}},"users":null,"shared":null,"next_fs_id":0}`)
	err := os.WriteFile(stateFile, data, 0644)
	require.NoError(t, err)

	ms := &MetaServer{stateFile: stateFile}
	err = ms.loadState()
	require.NoError(t, err)

	// verify nil entry was removed
	assert.NotContains(t, ms.fileservers, uint64(1))
	assert.Contains(t, ms.fileservers, uint64(2))
	assert.NotNil(t, ms.users)
	assert.NotNil(t, ms.shared)
}

func TestRobustness_SaveLoadState_RoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	stateFile := filepath.Join(tmpDir, "state.json")

	ms1 := &MetaServer{
		stateFile: stateFile,
		fileservers: map[uint64]*domain.FileServerInfo{
			1: {Address: "a", UserCount: 1, LastHeartbeatUnix: 100, Status: domain.FileServerStatusHealthy},
		},
		users: map[string]uint64{"user1": 1},
		shared: map[string][]SharedDirEntry{
			"user1": {{Owner: "user2", Path: "/user2/dir", DisplayName: "dir"}},
		},
		nextFsID: 2,
	}

	err := ms1.SaveState()
	require.NoError(t, err)

	ms2 := &MetaServer{stateFile: stateFile}
	err = ms2.loadState()
	require.NoError(t, err)

	assert.Equal(t, ms1.fileservers[1].Address, ms2.fileservers[1].Address)
	assert.Equal(t, ms1.users["user1"], ms2.users["user1"])
	assert.Equal(t, ms1.shared["user1"][0].Owner, ms2.shared["user1"][0].Owner)
	assert.Equal(t, ms1.nextFsID, ms2.nextFsID)
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

