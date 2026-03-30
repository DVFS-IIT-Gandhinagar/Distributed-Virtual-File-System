package metaserver

import (
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/umangshikarvar/dvfs/internal/domain"
)

type MetaServer struct {
	fileservers map[uint64]*domain.FileServerInfo // fs_id -> fs
	users       map[string]uint64 // username -> fs_id
	shared 		map[string][]string // username -> accessible root
	nextFsID    uint64
	stateFile   string

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

type persistedState struct {
	FileServers map[uint64]*domain.FileServerInfo `json:"fileservers"`
	Users       map[string]uint64                 `json:"users"`
	NextFsID    uint64                            `json:"next_fs_id"`
}

// NewFileServer creates a new file server object, either blank or loading from existing data
func NewMetaServer(stateFile string) (*MetaServer, error) {
	if stateFile == "" {
		stateFile = "./metaserver_state.json"
	}

	ms := &MetaServer{
		fileservers: make(map[uint64]*domain.FileServerInfo),
		users:       make(map[string]uint64),
		shared:      make(map[string][]string),
		nextFsID:    0,
		stateFile:   stateFile,

		heartbeatTimeout:       defaultHeartbeatTimeout,
		heartbeatCheckInterval: defaultHeartbeatCheckInterval,
	}

	if err := ms.loadState(); err != nil {
		log.Printf("[METASERVER] Warning: failed to load state from %s: %v", ms.stateFile, err)
	}
	
	return ms, nil
}


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
				changed := ms.markStaleFileServersLocked(time.Now().Unix())
				if changed {
					if err := ms.saveStateLocked(); err != nil {
						log.Printf("[METASERVER] ERROR: failed to persist heartbeat monitor updates: %v", err)
					}
				}
				ms.mu.Unlock()
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

func (ms *MetaServer) findFileServerByAddressLocked(address string) (uint64, bool) {
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

func (ms *MetaServer) SaveState() error {
	ms.mu.RLock()
	defer ms.mu.RUnlock()
	return ms.saveStateLocked()
}

func (ms *MetaServer) saveStateLocked() error {
	if ms.stateFile == "" {
		return nil
	}

	state := persistedState{
		FileServers: ms.fileservers,
		Users:       ms.users,
		NextFsID:    ms.nextFsID,
	}

	dir := filepath.Dir(ms.stateFile)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	tmpFile, err := os.CreateTemp(dir, "mds-state-*.tmp")
	if err != nil {
		return err
	}

	enc := json.NewEncoder(tmpFile)
	enc.SetIndent("", "  ")
	if err := enc.Encode(state); err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		return err
	}

	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpFile.Name())
		return err
	}

	if err := os.Rename(tmpFile.Name(), ms.stateFile); err != nil {
		os.Remove(tmpFile.Name())
		return err
	}

	return nil
}

func (ms *MetaServer) loadState() error {
	if ms.stateFile == "" {
		return nil
	}

	data, err := os.ReadFile(ms.stateFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}

	var state persistedState
	if err := json.Unmarshal(data, &state); err != nil {
		return err
	}

	if state.FileServers == nil {
		state.FileServers = make(map[uint64]*domain.FileServerInfo)
	}
	if state.Users == nil {
		state.Users = make(map[string]uint64)
	}

	ms.fileservers = state.FileServers
	ms.users = state.Users
	ms.nextFsID = state.NextFsID

	now := time.Now().Unix()
	for id, info := range ms.fileservers {
		if info == nil {
			delete(ms.fileservers, id)
			continue
		}
		if info.LastHeartbeatUnix == 0 {
			info.LastHeartbeatUnix = now
		}
		if info.Status == "" {
			info.Status = domain.FileServerStatusHealthy
		}
	}

	log.Printf("[METASERVER] Recovered state: fileservers=%d users=%d nextFsID=%d", len(ms.fileservers), len(ms.users), ms.nextFsID)
	return nil
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

func (ms *MetaServer) markStaleFileServersLocked(nowUnix int64) bool {
	changed := false
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
			log.Printf("[METASERVER] File server marked stale: id=%d address=%s last_heartbeat_ago=%ds", fsID, info.Address, lastSeenAgo)
			info.Status = domain.FileServerStatusStale
			changed = true
		}
	}
	return changed
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