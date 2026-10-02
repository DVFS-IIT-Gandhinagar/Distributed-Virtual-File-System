package metaserver

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/domain"
	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/storage"
)

// SharedDirEntry is one directory visible to a user in their root menu.
type SharedDirEntry struct {
	Owner       string // Directory owner username
	Path        string // Normalized path including owner namespace, e.g. "alice/proj"
	DisplayName string // Directory name to display
}

// MetaServer coordinates user-to-fileserver routing and shared-root visibility.
type MetaServer struct {
	store storage.MetaStore

	fileservers map[uint64]*domain.FileServerInfo // numeric fs id -> node
	users       map[string]uint64                 // username -> numeric fs id
	shared      map[string][]SharedDirEntry       // grantee -> visible roots

	// orphanedUsers holds accounts whose home node is not currently registered
	// (username -> the node id they are assigned to).
	orphanedUsers map[string]string

	// allowUserPurge permits a registration to delete user mappings it did not
	// report. Off by default: req.Users is built from a disk scan, so a data
	// directory that was not mounted at boot arrives as an empty list and would
	// otherwise erase every mapping for that node.
	allowUserPurge bool

	heartbeatTimeout       time.Duration
	heartbeatCheckInterval time.Duration
	stopMonitorCh          chan struct{}
	monitorStarted         bool
	mu                     sync.RWMutex
}

const (
	defaultHeartbeatTimeout       = 30 * time.Second
	defaultHeartbeatCheckInterval = 5 * time.Second
)

// deferredWrites collects store mutations to be run once ms.mu is released.
type deferredWrites []func(context.Context) error

func (d deferredWrites) run(ctx context.Context) error {
	for _, fn := range d {
		if err := fn(ctx); err != nil {
			return err
		}
	}
	return nil
}

// NewMetaServer builds a MetaServer and hydrates it from the store.
func NewMetaServer(ctx context.Context, store storage.MetaStore) (*MetaServer, error) {
	if store == nil {
		return nil, fmt.Errorf("metaserver: a MetaStore is required")
	}

	ms := &MetaServer{
		store:         store,
		fileservers:   make(map[uint64]*domain.FileServerInfo),
		users:         make(map[string]uint64),
		shared:        make(map[string][]SharedDirEntry),
		orphanedUsers: make(map[string]string),

		heartbeatTimeout:       defaultHeartbeatTimeout,
		heartbeatCheckInterval: defaultHeartbeatCheckInterval,
	}

	if err := ms.hydrate(ctx); err != nil {
		return nil, err
	}
	return ms, nil
}

// hydrate loads the durable state into the in-memory read caches.
func (ms *MetaServer) hydrate(ctx context.Context) error {
	snap, err := ms.store.LoadSnapshot(ctx)
	if err != nil {
		return fmt.Errorf("metaserver: load state: %w", err)
	}

	ms.mu.Lock()
	defer ms.mu.Unlock()

	ms.fileservers = make(map[uint64]*domain.FileServerInfo, len(snap.FileServers))
	nodeToNumeric := make(map[string]uint64, len(snap.FileServers))
	now := time.Now().Unix()
	for _, rec := range snap.FileServers {
		info := &domain.FileServerInfo{
			NodeID:            rec.NodeID,
			Address:           rec.Address,
			UserCount:         rec.UserCount,
			LastHeartbeatUnix: rec.LastHeartbeatUnix,
			Status:            rec.Status,
		}
		if info.LastHeartbeatUnix == 0 {
			info.LastHeartbeatUnix = now
		}
		if info.Status == "" {
			info.Status = domain.FileServerStatusHealthy
		}
		ms.fileservers[rec.NumericID] = info
		nodeToNumeric[rec.NodeID] = rec.NumericID
	}

	ms.users = make(map[string]uint64, len(snap.Users))
	ms.orphanedUsers = make(map[string]string)
	for _, u := range snap.Users {
		numericID, ok := nodeToNumeric[u.HomeNodeID]
		if !ok {
			// The user's home node is not currently registered -- it may be
			// down, renamed, or decommissioned. Record the account as orphaned
			// rather than dropping it.
			log.Printf("[METASERVER] User %s references unregistered node %q; retaining assignment without a live route", u.Username, u.HomeNodeID)
			ms.orphanedUsers[u.Username] = u.HomeNodeID
			continue
		}
		ms.users[u.Username] = numericID
	}

	ms.shared = make(map[string][]SharedDirEntry, len(snap.Shares))
	for _, sh := range snap.Shares {
		ms.shared[sh.Grantee] = append(ms.shared[sh.Grantee], SharedDirEntry{
			Owner:       sh.Owner,
			Path:        sh.Path,
			DisplayName: sh.DisplayName,
		})
	}
	// Every known user gets an entry so GetRoots does not have to distinguish
	// "no shares" from "unknown user".
	for username := range ms.users {
		if ms.shared[username] == nil {
			ms.shared[username] = []SharedDirEntry{}
		}
	}

	log.Printf("[METASERVER] Recovered state: fileservers=%d users=%d shares=%d orphaned_users=%d",
		len(ms.fileservers), len(ms.users), len(snap.Shares), len(ms.orphanedUsers))
	return nil
}

// SetHeartbeatConfig adjusts liveness thresholds.
func (ms *MetaServer) SetHeartbeatConfig(timeout, checkInterval time.Duration) {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	if timeout > 0 {
		ms.heartbeatTimeout = timeout
	}
	if checkInterval > 0 {
		ms.heartbeatCheckInterval = checkInterval
	}
}

// SetAllowUserPurge controls whether a registration may delete user mappings it
// did not report. See the field comment for why this defaults to false.
func (ms *MetaServer) SetAllowUserPurge(allow bool) {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	ms.allowUserPurge = allow
}

// StartHeartbeatMonitor sweeps for nodes that have stopped reporting and marks
// them stale. Returns a stop function.
func (ms *MetaServer) StartHeartbeatMonitor() func() {
	ms.mu.Lock()
	if ms.monitorStarted {
		ms.mu.Unlock()
		return func() {}
	}
	ms.monitorStarted = true
	ms.stopMonitorCh = make(chan struct{})
	checkInterval := ms.heartbeatCheckInterval
	if checkInterval <= 0 {
		checkInterval = defaultHeartbeatCheckInterval
	}
	stopCh := ms.stopMonitorCh
	ms.mu.Unlock()

	go func() {
		ticker := time.NewTicker(checkInterval)
		defer ticker.Stop()

		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				ms.mu.Lock()
				transitioned := ms.markStaleFileServersLocked(time.Now().Unix())
				ms.mu.Unlock()

				for _, nodeID := range transitioned {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					if err := ms.store.SetFileServerStatus(ctx, nodeID, domain.FileServerStatusStale); err != nil {
						log.Printf("[METASERVER] WARN: could not persist stale status for %s: %v", nodeID, err)
					}
					cancel()
				}
			}
		}
	}()

	return func() {
		ms.mu.Lock()
		defer ms.mu.Unlock()
		if ms.monitorStarted {
			close(ms.stopMonitorCh)
			ms.stopMonitorCh = nil
			ms.monitorStarted = false
		}
	}
}

// findFileServerByNodeIDLocked resolves the stable node identity to its dense
// numeric id. Keying on identity rather than address is what stops a DHCP lease
// change from registering the same machine twice.
func (ms *MetaServer) findFileServerByNodeIDLocked(nodeID string) (uint64, bool) {
	if nodeID == "" {
		return 0, false
	}
	for id, info := range ms.fileservers {
		if info != nil && info.NodeID == nodeID {
			return id, true
		}
	}
	return 0, false
}

// findFileServerByAddressLocked is the fallback for fileservers built before
// fs_id existed on the wire.
func (ms *MetaServer) findFileServerByAddressLocked(address string) (uint64, bool) {
	if address == "" {
		return 0, false
	}
	for id, info := range ms.fileservers {
		if info != nil && info.Address == address {
			return id, true
		}
	}
	return 0, false
}

func (ms *MetaServer) countUsersForFileServerLocked(fsID uint64) int {
	count := 0
	for _, mappedID := range ms.users {
		if mappedID == fsID {
			count++
		}
	}
	return count
}

func (ms *MetaServer) isHealthyLocked(fsInfo *domain.FileServerInfo, nowUnix int64) bool {
	if fsInfo == nil {
		return false
	}
	if fsInfo.Status != domain.FileServerStatusHealthy {
		return false
	}
	if fsInfo.LastHeartbeatUnix == 0 {
		return false
	}
	if ms.heartbeatTimeout <= 0 {
		return true
	}
	return nowUnix-fsInfo.LastHeartbeatUnix <= int64(ms.heartbeatTimeout/time.Second)
}

// markStaleFileServersLocked flips newly-silent nodes to stale and returns the
// node IDs that changed, for the caller to persist after unlocking.
func (ms *MetaServer) markStaleFileServersLocked(nowUnix int64) []string {
	var transitioned []string
	for fsID, info := range ms.fileservers {
		if info == nil {
			continue
		}
		if ms.isHealthyLocked(info, nowUnix) {
			continue
		}
		if info.Status != domain.FileServerStatusStale {
			lastSeenAgo := int64(0)
			if info.LastHeartbeatUnix > 0 {
				lastSeenAgo = nowUnix - info.LastHeartbeatUnix
			}
			log.Printf("[METASERVER] File server marked stale: id=%d node=%s address=%s last_heartbeat_ago=%ds",
				fsID, info.NodeID, info.Address, lastSeenAgo)
			info.Status = domain.FileServerStatusStale
			transitioned = append(transitioned, info.NodeID)
		}
	}
	return transitioned
}

func (ms *MetaServer) getLeastLoadedHealthyFileServerLocked(nowUnix int64) (uint64, bool) {
	var minFS uint64
	minUsers := 0
	first := true

	for fsID, fsInfo := range ms.fileservers {
		if !ms.isHealthyLocked(fsInfo, nowUnix) {
			continue
		}
		if first || fsInfo.UserCount < minUsers {
			minFS = fsID
			minUsers = fsInfo.UserCount
			first = false
		}
	}

	if first {
		return 0, false
	}
	return minFS, true
}
