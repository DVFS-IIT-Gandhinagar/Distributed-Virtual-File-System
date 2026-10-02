package metaserver

import (
	"context"
	"fmt"
	"log"
	"time"

	pb "github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/api/metaserver"
	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/domain"
	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/storage"
)

// GRPCHandler implements the gRPC meta server interface.
type GRPCHandler struct {
	pb.UnimplementedMetaServerServer
	MetaServer *MetaServer
}

// NewGRPCHandler creates a new gRPC handler.
func NewGRPCHandler(metaServer *MetaServer) *GRPCHandler {
	return &GRPCHandler{MetaServer: metaServer}
}

// removeSharesByOwnerLocked drops every visibility entry published by rootUser.
func (h *GRPCHandler) removeSharesByOwnerLocked(rootUser string) {
	for grantee, roots := range h.MetaServer.shared {
		out := make([]SharedDirEntry, 0, len(roots))
		for _, s := range roots {
			if s.Owner != rootUser {
				out = append(out, s)
			}
		}
		h.MetaServer.shared[grantee] = out
	}
}

// resolveNodeIDLocked determines the stable node identity for a request,
// falling back to the address for fileservers predating the fs_id field.
func resolveNodeID(fsID, address string) string {
	if fsID != "" {
		return fsID
	}
	return "addr:" + address
}

// registrationConflictLocked returns an error message for the first user this
// node may not claim, or "" if none. A user already on another node is a
// conflict, and so is a user orphaned on another node: their data is still
// there, so only that node may reclaim them. Anything else has to deregister
// the old node first.
func (ms *MetaServer) registrationConflictLocked(nodeID string, fsID uint64, exists bool, users []string) string {
	for _, username := range users {
		if mappedID, ok := ms.users[username]; ok && (!exists || mappedID != fsID) {
			addr := "unknown"
			if info := ms.fileservers[mappedID]; info != nil {
				addr = info.Address
			}
			return "User " + username + " already exists in file server: " + addr
		}
		if home, ok := ms.orphanedUsers[username]; ok && home != nodeID {
			return "User " + username + " is assigned to file server " + home +
				", which is currently unregistered; deregister that node before another claims the user"
		}
	}
	return ""
}

// RegisterFileServer records a storage node, the users it hosts, and the
// directories those users share.
func (h *GRPCHandler) RegisterFileServer(ctx context.Context, req *pb.RegisterFileServerRequest) (*pb.RegisterFileServerResponse, error) {
	log.Printf("[METASERVER] Registering FS id=%q addr=%s with %d users: %v", req.FsId, req.Address, len(req.Users), req.Users)

	if req.Address == "" {
		return &pb.RegisterFileServerResponse{Success: false, Error: "empty file server address"}, nil
	}
	nodeID := resolveNodeID(req.FsId, req.Address)
	if req.FsId == "" {
		log.Printf("[METASERVER] WARN: fileserver at %s did not send fs_id; falling back to address-derived identity %q", req.Address, nodeID)
	}

	ms := h.MetaServer
	var writes deferredWrites

	// Structural writes below must reach the store in memory order; see
	// writeOrderMu.
	ms.writeOrderMu.Lock()
	defer ms.writeOrderMu.Unlock()

	ms.mu.Lock()

	fsID, exists := ms.findFileServerByNodeIDLocked(nodeID)
	if !exists {
		// Legacy fallback: adopt an existing address-keyed entry so upgrading a
		// fileserver does not orphan its users.
		if legacyID, legacyFound := ms.findFileServerByAddressLocked(req.Address); legacyFound {
			fsID = legacyID
			exists = true
			if info := ms.fileservers[fsID]; info != nil {
				info.NodeID = nodeID
			}
		}
	}

	incomingUsers := make(map[string]struct{}, len(req.Users))
	for _, username := range req.Users {
		incomingUsers[username] = struct{}{}
	}

	// Validate before anything is allocated or mutated, so a rejected
	// registration leaves neither memory nor the store changed.
	if msg := ms.registrationConflictLocked(nodeID, fsID, exists, req.Users); msg != "" {
		ms.mu.Unlock()
		log.Printf("[METASERVER] ERROR: %s", msg)
		return &pb.RegisterFileServerResponse{Success: false, Error: msg}, nil
	}

	if !exists {
		// Allocate through the store so the numeric id is durable and unique.
		ms.mu.Unlock()
		allocCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		numericID, err := ms.store.UpsertFileServer(allocCtx, storage.FileServerRecord{
			NodeID:            nodeID,
			Address:           req.Address,
			LastHeartbeatUnix: time.Now().Unix(),
			Status:            domain.FileServerStatusHealthy,
		})
		cancel()
		if err != nil {
			log.Printf("[METASERVER] ERROR: could not allocate node %s: %v", nodeID, err)
			return &pb.RegisterFileServerResponse{Success: false, Error: "failed to persist metaserver state"}, nil
		}

		ms.mu.Lock()
		fsID = numericID
		created := false
		if ms.fileservers[fsID] == nil {
			ms.fileservers[fsID] = &domain.FileServerInfo{NodeID: nodeID}
			created = true
		}

		// The lock was released for the allocation, so another registration may
		// have claimed one of these users meanwhile. The node document already
		// exists, so undo it rather than leave a node that never finished
		// registering and would look healthy to placement after a restart.
		if msg := ms.registrationConflictLocked(nodeID, fsID, true, req.Users); msg != "" {
			if created {
				delete(ms.fileservers, fsID)
			}
			ms.mu.Unlock()
			cleanupCtx, cancelCleanup := context.WithTimeout(ctx, 10*time.Second)
			if rmErr := ms.store.RemoveFileServer(cleanupCtx, nodeID); rmErr != nil {
				log.Printf("[METASERVER] WARN: could not remove provisional node %s after rejected registration: %v", nodeID, rmErr)
			}
			cancelCleanup()
			log.Printf("[METASERVER] ERROR: %s", msg)
			return &pb.RegisterFileServerResponse{Success: false, Error: msg}, nil
		}
	}

	// Past this point the registration commits: no path below returns early.
	fsInfo := ms.fileservers[fsID]
	if fsInfo == nil {
		fsInfo = &domain.FileServerInfo{NodeID: nodeID}
		ms.fileservers[fsID] = fsInfo
	}
	fsInfo.NodeID = nodeID
	fsInfo.Address = req.Address
	fsInfo.LastHeartbeatUnix = time.Now().Unix()
	fsInfo.Status = domain.FileServerStatusHealthy

	// Users this node previously hosted but no longer reports.
	var missing []string
	for username, mappedID := range ms.users {
		if mappedID == fsID {
			if _, ok := incomingUsers[username]; !ok {
				missing = append(missing, username)
			}
		}
	}

	if len(missing) > 0 && !ms.allowUserPurge {
		// A disk scan is the only source of req.Users, so an unmounted or
		// mistyped data directory presents as "this node has no users". Deleting
		// on that signal is unrecoverable, so the default is to keep the
		// mappings and make the discrepancy loud instead.
		log.Printf("[METASERVER] WARN: node %s reported %d users but %d known mappings are absent (%v); retaining them. "+
			"Start the metaserver with -allow_user_purge to permit deletion.",
			nodeID, len(req.Users), len(missing), missing)
		missing = nil
	}

	for _, username := range missing {
		delete(ms.users, username)
		delete(ms.shared, username)
		h.removeSharesByOwnerLocked(username)
	}
	if len(missing) > 0 {
		purged := append([]string(nil), missing...)
		writes = append(writes, func(c context.Context) error {
			if err := ms.store.RemoveUsers(c, purged); err != nil {
				return err
			}
			for _, u := range purged {
				if err := ms.store.RemoveSharesInvolving(c, u); err != nil {
					return err
				}
			}
			return nil
		})
	}

	// Clear orphan markers pointing at this node, whether or not it reported the users by name.
	for username, homeNode := range ms.orphanedUsers {
		if homeNode == nodeID {
			delete(ms.orphanedUsers, username)
			ms.users[username] = fsID
			if ms.shared[username] == nil {
				ms.shared[username] = []SharedDirEntry{}
			}
		}
	}

	for username := range incomingUsers {
		ms.users[username] = fsID
		if ms.shared[username] == nil {
			ms.shared[username] = []SharedDirEntry{}
		}

		u := username
		writes = append(writes, func(c context.Context) error {
			return ms.store.AssignUser(c, u, nodeID)
		})
	}

	// This node is authoritative for the shares its users publish, so clear and
	// rebuild exactly those. Grants those users merely receive belong to other
	// nodes and are left alone.
	for username := range incomingUsers {
		h.removeSharesByOwnerLocked(username)
		owner := username
		writes = append(writes, func(c context.Context) error {
			return ms.store.RemoveSharesByOwner(c, owner)
		})
	}

	log.Printf("[METASERVER] Processing %d shared directory entries from registration", len(req.Shared))
	for _, sharedDir := range req.Shared {
		owner := sharedDir.Owner
		dirPath := storage.NormalizeSharePath(sharedDir.Path)
		dirName := sharedDir.Name

		if _, ownedByThisFS := incomingUsers[owner]; !ownedByThisFS {
			log.Printf("[METASERVER] Skipping owner %s (not on this FS)", owner)
			continue
		}

		for _, sharedWith := range sharedDir.Users {
			if sharedWith == owner {
				continue
			}
			if ms.shared[sharedWith] == nil {
				ms.shared[sharedWith] = []SharedDirEntry{}
			}
			if !containsShare(ms.shared[sharedWith], owner, dirPath) {
				ms.shared[sharedWith] = append(ms.shared[sharedWith], SharedDirEntry{
					Owner:       owner,
					Path:        dirPath,
					DisplayName: dirName,
				})
			}

			rec := storage.ShareRecord{Grantee: sharedWith, Owner: owner, Path: dirPath, DisplayName: dirName}
			writes = append(writes, func(c context.Context) error {
				return ms.store.AddShare(c, rec)
			})
		}
	}

	fsInfo.UserCount = ms.countUsersForFileServerLocked(fsID)
	userCount := fsInfo.UserCount
	address := fsInfo.Address
	heartbeat := fsInfo.LastHeartbeatUnix

	ms.mu.Unlock()

	// The node record itself is written first so later share and user writes
	// never reference a node the store has not seen.
	writes = append(deferredWrites{func(c context.Context) error {
		_, err := ms.store.UpsertFileServer(c, storage.FileServerRecord{
			NodeID:            nodeID,
			Address:           address,
			UserCount:         userCount,
			LastHeartbeatUnix: heartbeat,
			Status:            domain.FileServerStatusHealthy,
		})
		return err
	}}, writes...)

	writeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// No rollback here, deliberately. The fileserver treats any failure as
	// "not registered" and retries within seconds, and every write above is
	// idempotent, so the retry re-applies exactly this state. Memory being
	// briefly ahead of the store is harmless: memory is what serves requests.
	if err := writes.run(writeCtx); err != nil {
		log.Printf("[METASERVER] ERROR: failed to persist state after registration: %v", err)
		return &pb.RegisterFileServerResponse{Success: false, Error: "failed to persist metaserver state"}, nil
	}

	log.Printf("[METASERVER] FS registered successfully: ID=%d, Node=%s, Address=%s, Users=%d", fsID, nodeID, req.Address, userCount)
	return &pb.RegisterFileServerResponse{Success: true}, nil
}

func containsShare(entries []SharedDirEntry, owner, path string) bool {
	for _, e := range entries {
		if e.Owner == owner && e.Path == path {
			return true
		}
	}
	return false
}

// Heartbeat refreshes liveness for an already-registered fileserver.
func (h *GRPCHandler) Heartbeat(ctx context.Context, req *pb.HeartbeatRequest) (*pb.HeartbeatResponse, error) {
	if req.Address == "" {
		log.Printf("[METASERVER] WARN: heartbeat rejected due to empty file server address")
		return &pb.HeartbeatResponse{Success: false, Error: "empty file server address"}, nil
	}
	nodeID := resolveNodeID(req.FsId, req.Address)

	ms := h.MetaServer
	ms.mu.Lock()

	fsID, exists := ms.findFileServerByNodeIDLocked(nodeID)
	if !exists {
		fsID, exists = ms.findFileServerByAddressLocked(req.Address)
	}
	if !exists {
		ms.mu.Unlock()
		log.Printf("[METASERVER] WARN: heartbeat from unknown file server node=%s address=%s", nodeID, req.Address)
		return &pb.HeartbeatResponse{Success: false, Error: "unknown file server"}, nil
	}

	fsInfo := ms.fileservers[fsID]
	if fsInfo == nil {
		ms.mu.Unlock()
		log.Printf("[METASERVER] WARN: heartbeat received for missing file server entry id=%d address=%s", fsID, req.Address)
		return &pb.HeartbeatResponse{Success: false, Error: "file server entry missing"}, nil
	}

	prevStatus := fsInfo.Status
	now := time.Now()
	fsInfo.LastHeartbeatUnix = now.Unix()
	fsInfo.Status = domain.FileServerStatusHealthy
	// An address change on an existing identity is a new DHCP lease, not a new
	// machine; update in place.
	addressChanged := fsInfo.Address != req.Address
	if addressChanged {
		log.Printf("[METASERVER] Node %s changed address: %s -> %s", nodeID, fsInfo.Address, req.Address)
		fsInfo.Address = req.Address
	}
	storedNodeID := fsInfo.NodeID
	ms.mu.Unlock()

	if prevStatus != domain.FileServerStatusHealthy {
		log.Printf("[METASERVER] File server recovered: id=%d node=%s status=%s->%s", fsID, storedNodeID, prevStatus, domain.FileServerStatusHealthy)
	}

	// Liveness is best-effort by design: the in-memory table already reflects
	// it, and a missed write is corrected by the next heartbeat one interval
	// later.
	writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if addressChanged {
		// Targeted update, not a whole-record upsert.
		if err := ms.store.SetAddress(writeCtx, storedNodeID, req.Address); err != nil {
			log.Printf("[METASERVER] WARN: could not persist address change for %s: %v", storedNodeID, err)
		}
	}
	if err := ms.store.RecordHeartbeat(writeCtx, storedNodeID, now, domain.FileServerStatusHealthy); err != nil {
		log.Printf("[METASERVER] WARN: could not persist heartbeat for %s: %v", storedNodeID, err)
	}

	return &pb.HeartbeatResponse{Success: true}, nil
}

// Navigate resolves which fileserver hosts a given root user.
func (h *GRPCHandler) Navigate(ctx context.Context, req *pb.NavigateRequest) (*pb.NavigateResponse, error) {
	log.Printf("[METASERVER] Navigate request for user: %s", req.RootUser)

	user := req.Username
	rootUser := req.RootUser
	if user == "" || rootUser == "" {
		return &pb.NavigateResponse{Success: false, Error: "username and root_user are required"}, nil
	}

	ms := h.MetaServer
	// Read-only: isHealthyLocked evaluates the heartbeat deadline directly, so
	// routing is correct without mutating Status here.
	ms.mu.RLock()
	defer ms.mu.RUnlock()

	nowUnix := time.Now().Unix()

	if _, exists := ms.users[user]; !exists {
		log.Printf("[METASERVER] Navigate failed: username '%s' does not exist", req.Username)
		return &pb.NavigateResponse{Success: false, Error: "username '" + req.Username + "' does not exist"}, nil
	}

	fs, exists := ms.users[rootUser]
	if !exists {
		log.Printf("[METASERVER] Navigate failed: root user '%s' does not exist", req.RootUser)
		return &pb.NavigateResponse{Success: false, Error: "root user '" + req.RootUser + "' does not exist"}, nil
	}

	rootFS, present := ms.fileservers[fs]
	if !present || !ms.isHealthyLocked(rootFS, nowUnix) {
		log.Printf("[METASERVER] Navigate failed: root user '%s' is on unavailable file server", rootUser)
		return &pb.NavigateResponse{Success: false, Error: "root user '" + rootUser + "' is currently unavailable"}, nil
	}

	allowed := user == rootUser
	if !allowed {
		for _, s := range ms.shared[user] {
			if s.Owner == rootUser {
				allowed = true
				break
			}
		}
	}
	if !allowed {
		log.Printf("[METASERVER] Navigate failed: user '%s' does not have access to root '%s'", user, rootUser)
		return &pb.NavigateResponse{
			Success: false,
			Error:   "user '" + user + "' does not have access to root '" + rootUser + "'",
		}, nil
	}

	log.Printf("[METASERVER] Routing user %s to FS %s", user, rootFS.Address)
	return &pb.NavigateResponse{Success: true, Address: rootFS.Address}, nil
}

// GetRoots returns the personal and shared roots visible to a user, assigning
// them a home fileserver on first contact.
func (h *GRPCHandler) GetRoots(ctx context.Context, req *pb.GetRootsRequest) (*pb.GetRootsResponse, error) {
	log.Printf("[METASERVER] Get roots request for user: %s", req.Username)

	user := req.Username
	if user == "" {
		return &pb.GetRootsResponse{Success: false, Error: "username is required"}, nil
	}

	ms := h.MetaServer

	// Fast path: an already-assigned user needs no write and no exclusive lock.
	ms.mu.RLock()
	if _, exists := ms.users[user]; exists {
		roots := buildRootsLocked(ms, user)
		ms.mu.RUnlock()
		return &pb.GetRootsResponse{Success: true, Roots: roots}, nil
	}
	ms.mu.RUnlock()

	ms.mu.Lock()
	// Re-check: another request may have assigned this user while we upgraded.
	if _, exists := ms.users[user]; exists {
		roots := buildRootsLocked(ms, user)
		ms.mu.Unlock()
		return &pb.GetRootsResponse{Success: true, Roots: roots}, nil
	}

	// An orphaned user is assigned, just not routable right now: their home node
	// is not registered. Refuse rather than assigning them a new one, because
	// reassignment would overwrite the stored assignment and strand whatever
	// data is still sitting on the original node.
	if homeNode, orphaned := ms.orphanedUsers[user]; orphaned {
		ms.mu.Unlock()
		log.Printf("[METASERVER] GetRoots for %s deferred: home node %q is not registered", user, homeNode)
		return &pb.GetRootsResponse{
			Success: false,
			Error:   "your home file server (" + homeNode + ") is currently unavailable; please try again once it is back online",
		}, nil
	}

	nowUnix := time.Now().Unix()
	transitioned := ms.markStaleFileServersLocked(nowUnix)

	minFS, ok := ms.getLeastLoadedHealthyFileServerLocked(nowUnix)
	if !ok {
		ms.mu.Unlock()
		h.persistStaleTransitions(ctx, transitioned)
		return &pb.GetRootsResponse{Success: false, Error: "no healthy file server registered"}, nil
	}

	ms.users[user] = minFS
	ms.fileservers[minFS].UserCount++
	ms.shared[user] = []SharedDirEntry{}

	nodeID := ms.fileservers[minFS].NodeID
	userCount := ms.fileservers[minFS].UserCount
	address := ms.fileservers[minFS].Address
	roots := buildRootsLocked(ms, user)
	ms.mu.Unlock()

	h.persistStaleTransitions(ctx, transitioned)

	// Assignment is durable state: if it is lost, the user is re-assigned on
	// their next login and may land on a different node from their data.
	writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := ms.store.AssignUser(writeCtx, user, nodeID); err != nil {
		log.Printf("[METASERVER] ERROR: failed to persist user assignment: %v", err)
		// Roll the in-memory assignment back so memory and store agree.
		ms.mu.Lock()
		delete(ms.users, user)
		delete(ms.shared, user)
		if info := ms.fileservers[minFS]; info != nil && info.UserCount > 0 {
			info.UserCount--
		}
		ms.mu.Unlock()
		return &pb.GetRootsResponse{Success: false, Error: "failed to persist metaserver state"}, nil
	}
	if err := ms.store.SetUserCount(writeCtx, nodeID, userCount); err != nil {
		log.Printf("[METASERVER] WARN: could not persist user count for %s: %v", nodeID, err)
	}

	log.Printf("[METASERVER] Assigned user %s to FS %s (users: %d)", user, address, userCount)
	return &pb.GetRootsResponse{Success: true, Roots: roots}, nil
}

func (h *GRPCHandler) persistStaleTransitions(ctx context.Context, nodeIDs []string) {
	for _, nodeID := range nodeIDs {
		c, cancel := context.WithTimeout(ctx, 5*time.Second)
		if err := h.MetaServer.store.SetFileServerStatus(c, nodeID, domain.FileServerStatusStale); err != nil {
			log.Printf("[METASERVER] WARN: could not persist stale status for %s: %v", nodeID, err)
		}
		cancel()
	}
}

func buildRootsLocked(ms *MetaServer, user string) []*pb.SharedRoot {
	roots := []*pb.SharedRoot{
		{Owner: user, Path: user, DisplayName: "mydrive"},
	}
	for _, sharedRoot := range ms.shared[user] {
		roots = append(roots, &pb.SharedRoot{
			Owner:       sharedRoot.Owner,
			Path:        sharedRoot.Path,
			DisplayName: sharedRoot.DisplayName,
		})
	}
	return roots
}

// RootShare records that a directory is visible to another user.
func (h *GRPCHandler) RootShare(ctx context.Context, req *pb.RootShareRequest) (*pb.RootShareResponse, error) {
	log.Printf("[METASERVER] Root share request for dir: %s to share with: %s", req.RootPath, req.ShareWith)

	ms := h.MetaServer
	ms.writeOrderMu.Lock()
	defer ms.writeOrderMu.Unlock()
	path := storage.NormalizeSharePath(req.RootPath)

	ms.mu.Lock()

	if _, exists := ms.users[req.Owner]; !exists {
		ms.mu.Unlock()
		log.Printf("[METASERVER] Share failed: root user '%s' does not exist", req.Owner)
		return &pb.RootShareResponse{Success: false, Error: fmt.Sprintf("root user '%s' does not exist", req.Owner)}, nil
	}
	if _, exists := ms.users[req.ShareWith]; !exists {
		ms.mu.Unlock()
		log.Printf("[METASERVER] Share failed: target user '%s' does not exist", req.ShareWith)
		return &pb.RootShareResponse{Success: false, Error: fmt.Sprintf("target user '%s' does not exist", req.ShareWith)}, nil
	}

	// Deduplicate on (owner, path). Matching on owner alone previously dropped
	// every directory after the first that an owner shared with the same person.
	if containsShare(ms.shared[req.ShareWith], req.Owner, path) {
		ms.mu.Unlock()
		log.Printf("[METASERVER] Share skipped: '%s' already shared with '%s'", path, req.ShareWith)
		return &pb.RootShareResponse{Success: true}, nil
	}

	ms.shared[req.ShareWith] = append(ms.shared[req.ShareWith], SharedDirEntry{
		Owner:       req.Owner,
		DisplayName: req.Name,
		Path:        path,
	})
	ms.mu.Unlock()

	writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := ms.store.AddShare(writeCtx, storage.ShareRecord{
		Grantee:     req.ShareWith,
		Owner:       req.Owner,
		Path:        path,
		DisplayName: req.Name,
	}); err != nil {
		log.Printf("[METASERVER] ERROR: failed to persist state after share: %v", err)
		ms.mu.Lock()
		ms.shared[req.ShareWith] = removeShareEntry(ms.shared[req.ShareWith], req.Owner, path)
		ms.mu.Unlock()
		return &pb.RootShareResponse{Success: false, Error: "failed to persist metaserver state"}, nil
	}

	log.Printf("[METASERVER] Dir %s successfully shared with %s", path, req.ShareWith)
	return &pb.RootShareResponse{Success: true}, nil
}

// RootUnshare revokes a directory's visibility.
func (h *GRPCHandler) RootUnshare(ctx context.Context, req *pb.RootUnshareRequest) (*pb.RootUnshareResponse, error) {
	log.Printf("[METASERVER] Root unshare request for dir: %s to unshare with: %s", req.RootPath, req.UnshareWith)

	ms := h.MetaServer
	ms.writeOrderMu.Lock()
	defer ms.writeOrderMu.Unlock()
	path := storage.NormalizeSharePath(req.RootPath)

	ms.mu.Lock()

	if _, exists := ms.users[req.Owner]; !exists {
		ms.mu.Unlock()
		log.Printf("[METASERVER] Unshare failed: root user '%s' does not exist", req.Owner)
		return &pb.RootUnshareResponse{Success: false, Error: fmt.Sprintf("root user '%s' does not exist", req.Owner)}, nil
	}
	if _, exists := ms.users[req.UnshareWith]; !exists {
		ms.mu.Unlock()
		log.Printf("[METASERVER] Unshare failed: target user '%s' does not exist", req.UnshareWith)
		return &pb.RootUnshareResponse{Success: false, Error: fmt.Sprintf("target user '%s' does not exist", req.UnshareWith)}, nil
	}

	before := len(ms.shared[req.UnshareWith])
	ms.shared[req.UnshareWith] = removeShareEntry(ms.shared[req.UnshareWith], req.Owner, path)
	removed := len(ms.shared[req.UnshareWith]) != before
	ms.mu.Unlock()

	// Always issue the delete, even when memory held no entry: the store is the
	// authority for revocation and a keyed delete is idempotent.
	writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := ms.store.RemoveShare(writeCtx, req.UnshareWith, req.Owner, path); err != nil {
		log.Printf("[METASERVER] ERROR: failed to persist state after unshare: %v", err)
		return &pb.RootUnshareResponse{Success: false, Error: "failed to persist metaserver state"}, nil
	}

	if !removed {
		log.Printf("[METASERVER] Unshare: dir '%s' was not present for '%s'; store delete issued anyway", path, req.UnshareWith)
	} else {
		log.Printf("[METASERVER] Dir '%s' successfully unshared from '%s'", path, req.UnshareWith)
	}
	return &pb.RootUnshareResponse{Success: true}, nil
}

func removeShareEntry(entries []SharedDirEntry, owner, path string) []SharedDirEntry {
	out := make([]SharedDirEntry, 0, len(entries))
	for _, e := range entries {
		if e.Owner == owner && e.Path == path {
			continue
		}
		out = append(out, e)
	}
	return out
}

// DeregisterFileServer removes a node and releases every user assigned to it,
// so they are placed afresh on their next login.
func (h *GRPCHandler) DeregisterFileServer(ctx context.Context, req *pb.DeregisterFileServerRequest) (*pb.DeregisterFileServerResponse, error) {
	if req.FsId == "" && req.Address == "" {
		return &pb.DeregisterFileServerResponse{Success: false, Error: "fs_id or address is required"}, nil
	}

	ms := h.MetaServer
	ms.writeOrderMu.Lock()
	defer ms.writeOrderMu.Unlock()
	ms.mu.Lock()

	// Resolve by stable identity first; address is the fallback for callers
	// that predate fs_id.
	nodeID := req.FsId
	var (
		fsID   uint64
		exists bool
	)
	if nodeID != "" {
		fsID, exists = ms.findFileServerByNodeIDLocked(nodeID)
	}
	if !exists && req.Address != "" {
		if fsID, exists = ms.findFileServerByAddressLocked(req.Address); exists {
			if info := ms.fileservers[fsID]; info != nil {
				nodeID = info.NodeID
			}
		}
	}

	// Everyone routed to this node, plus anyone orphaned on it.
	var removed []string
	if exists {
		for username, mappedID := range ms.users {
			if mappedID == fsID {
				removed = append(removed, username)
			}
		}
	}
	if nodeID != "" {
		for username, home := range ms.orphanedUsers {
			if home == nodeID {
				removed = append(removed, username)
			}
		}
	}

	if !exists && len(removed) == 0 {
		ms.mu.Unlock()
		log.Printf("[METASERVER] DeregisterFileServer: node=%q address=%q not found (idempotent)", req.FsId, req.Address)
		return &pb.DeregisterFileServerResponse{Success: true}, nil
	}

	// Snapshot enough to undo the in-memory change if the store rejects it
	// to maintain consistency between memory and the store.
	var prevNode *domain.FileServerInfo
	if exists {
		prevNode = ms.fileservers[fsID]
	}
	prevUsers := make(map[string]uint64, len(removed))
	prevOrphans := make(map[string]string, len(removed))
	for _, u := range removed {
		if id, ok := ms.users[u]; ok {
			prevUsers[u] = id
		}
		if home, ok := ms.orphanedUsers[u]; ok {
			prevOrphans[u] = home
		}
	}
	prevShared := cloneShared(ms.shared)

	for _, u := range removed {
		delete(ms.users, u)
		delete(ms.orphanedUsers, u)
		delete(ms.shared, u)
		h.removeSharesByOwnerLocked(u)
	}
	if exists {
		delete(ms.fileservers, fsID)
	}
	ms.mu.Unlock()

	// Node first, then users, then shares.
	writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	err := func() error {
		if exists {
			if err := ms.store.RemoveFileServer(writeCtx, nodeID); err != nil {
				return err
			}
		}
		if len(removed) == 0 {
			return nil
		}
		if err := ms.store.RemoveUsers(writeCtx, removed); err != nil {
			return err
		}
		for _, u := range removed {
			if err := ms.store.RemoveSharesInvolving(writeCtx, u); err != nil {
				return err
			}
		}
		return nil
	}()
	if err != nil {
		log.Printf("[METASERVER] ERROR: failed to persist state after deregister of %s: %v", nodeID, err)
		ms.mu.Lock()
		if prevNode != nil {
			ms.fileservers[fsID] = prevNode
		}
		for u, id := range prevUsers {
			ms.users[u] = id
		}
		for u, home := range prevOrphans {
			ms.orphanedUsers[u] = home
		}
		ms.shared = prevShared
		ms.mu.Unlock()
		return &pb.DeregisterFileServerResponse{Success: false, Error: "failed to persist metaserver state: " + err.Error()}, nil
	}

	log.Printf("[METASERVER] DeregisterFileServer: removed node=%s id=%d address=%s, released users=%v", nodeID, fsID, req.Address, removed)
	return &pb.DeregisterFileServerResponse{Success: true}, nil
}

// cloneShared deep-copies the visibility index so a failed store write can
// restore it exactly.
func cloneShared(m map[string][]SharedDirEntry) map[string][]SharedDirEntry {
	out := make(map[string][]SharedDirEntry, len(m))
	for k, v := range m {
		out[k] = append([]SharedDirEntry(nil), v...)
	}
	return out
}
