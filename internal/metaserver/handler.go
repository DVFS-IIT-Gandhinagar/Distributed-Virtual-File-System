package metaserver

import (
	"context"
	"log"
	"time"

	pb "github.com/umangshikarvar/dvfs/api/metaserver"
	"github.com/umangshikarvar/dvfs/internal/domain"
)

// helper func
func contains(slice []SharedDirEntry, target string) bool {
	for _, s := range slice {
		if s.Owner == target {
			return true
		}
	}
	return false
}

func removeValue(slice []SharedDirEntry, target string) []SharedDirEntry {
	out := make([]SharedDirEntry, 0, len(slice))
	for _, s := range slice {
		if s.Owner != target {
			out = append(out, s)
		}
	}
	return out
}

func (h *GRPCHandler) removeRootFromAllSharedLocked(rootUser string) {
	for username, roots := range h.MetaServer.shared {
		h.MetaServer.shared[username] = removeValue(roots, rootUser)
	}
}

// GRPCHandler implements the gRPC meta server interface
type GRPCHandler struct {
	pb.UnimplementedMetaServerServer
	MetaServer *MetaServer
}

// NewGRPCHandler creates a new gRPC handler
func NewGRPCHandler(metaServer *MetaServer) *GRPCHandler {
	return &GRPCHandler{
		MetaServer: metaServer,
	}
}

// RegisterFileServer handles file server registration
func (h *GRPCHandler) RegisterFileServer(ctx context.Context, req *pb.RegisterFileServerRequest) (*pb.RegisterFileServerResponse, error) {
	log.Printf("[METASERVER] Registering FS %s with %d users: %v", req.Address, len(req.Users), req.Users)

	if req.Address == "" {
		return &pb.RegisterFileServerResponse{
			Success: false,
			Error:   "empty file server address",
		}, nil
	}

	h.MetaServer.mu.Lock()
	defer h.MetaServer.mu.Unlock()

	fsID, exists := h.MetaServer.findFileServerByAddressLocked(req.Address)
	if !exists {
		fsID = h.MetaServer.nextFsID
		h.MetaServer.fileservers[fsID] = &domain.FileServerInfo{
			Address:   req.Address,
			UserCount: 0,
		}
		h.MetaServer.nextFsID++
	}

	fsInfo := h.MetaServer.fileservers[fsID]
	if fsInfo == nil {
		fsInfo = &domain.FileServerInfo{Address: req.Address}
		h.MetaServer.fileservers[fsID] = fsInfo
	}
	fsInfo.Address = req.Address
	fsInfo.LastHeartbeatUnix = time.Now().Unix()
	fsInfo.Status = domain.FileServerStatusHealthy

	incomingUsers := make(map[string]struct{}, len(req.Users))
	for _, username := range req.Users {
		incomingUsers[username] = struct{}{}
	}

	// Remove users that no longer belong to this fileserver.
	for username, mappedID := range h.MetaServer.users {
		if mappedID == fsID {
			if _, ok := incomingUsers[username]; !ok {
				delete(h.MetaServer.users, username)
				delete(h.MetaServer.shared, username)
				h.removeRootFromAllSharedLocked(username)
			}
		}
	}

	for username := range incomingUsers {
		mappedID, alreadyMapped := h.MetaServer.users[username]
		if alreadyMapped && mappedID != fsID {
			mappedFS := h.MetaServer.fileservers[mappedID]
			mappedAddr := "unknown"
			if mappedFS != nil {
				mappedAddr = mappedFS.Address
			}
			log.Printf("[METASERVER] ERROR: User %s already exists in FS %s", username, mappedAddr)
			return &pb.RegisterFileServerResponse{
				Success: false,
				Error:   "User " + username + " already exists in file server: " + mappedAddr,
			}, nil
		}
		h.MetaServer.users[username] = fsID
		if h.MetaServer.shared[username] == nil {
			h.MetaServer.shared[username] = []SharedDirEntry{}
		}
	}

	// Rebuild sharing entries for roots that belong to this fileserver.
	for username := range incomingUsers {
		h.removeRootFromAllSharedLocked(username)
	}

	log.Printf("[METASERVER] Processing %d ACL entries from registration", len(req.Acls))
	for _, userACL := range req.Acls {
		username := userACL.Username
		if _, ownedByThisFS := incomingUsers[username]; !ownedByThisFS {
			continue
		}

		for _, sharedWith := range userACL.Shared {
			if h.MetaServer.shared[sharedWith] == nil {
				h.MetaServer.shared[sharedWith] = []SharedDirEntry{}
			}
			if !contains(h.MetaServer.shared[sharedWith], username) {
				h.MetaServer.shared[sharedWith] = append(h.MetaServer.shared[sharedWith], SharedDirEntry{Owner: username})
			}
		}
	}

	fsInfo.UserCount = h.MetaServer.countUsersForFileServerLocked(fsID)

	if err := h.MetaServer.saveStateLocked(); err != nil {
		log.Printf("[METASERVER] ERROR: failed to persist state after registration: %v", err)
		return &pb.RegisterFileServerResponse{
			Success: false,
			Error:   "failed to persist metaserver state",
		}, nil
	}

	log.Printf("[METASERVER] FS registered successfully: ID=%d, Address=%s, Users=%d", fsID, req.Address, fsInfo.UserCount)
	return &pb.RegisterFileServerResponse{
		Success: true,
	}, nil
}

// Heartbeat updates liveness for an already-registered fileserver.
func (h *GRPCHandler) Heartbeat(ctx context.Context, req *pb.HeartbeatRequest) (*pb.HeartbeatResponse, error) {
	if req.Address == "" {
		log.Printf("[METASERVER] WARN: heartbeat rejected due to empty file server address")
		return &pb.HeartbeatResponse{Success: false, Error: "empty file server address"}, nil
	}

	h.MetaServer.mu.Lock()
	defer h.MetaServer.mu.Unlock()

	fsID, exists := h.MetaServer.findFileServerByAddressLocked(req.Address)
	if !exists {
		log.Printf("[METASERVER] WARN: heartbeat from unknown file server address=%s", req.Address)
		return &pb.HeartbeatResponse{Success: false, Error: "unknown file server"}, nil
	}

	fsInfo := h.MetaServer.fileservers[fsID]
	if fsInfo == nil {
		log.Printf("[METASERVER] WARN: heartbeat received for missing file server entry id=%d address=%s", fsID, req.Address)
		return &pb.HeartbeatResponse{Success: false, Error: "file server entry missing"}, nil
	}

	prevStatus := fsInfo.Status
	fsInfo.LastHeartbeatUnix = time.Now().Unix()
	fsInfo.Status = domain.FileServerStatusHealthy
	if prevStatus != domain.FileServerStatusHealthy {
		log.Printf("[METASERVER] File server recovered: id=%d address=%s status=%s->%s", fsID, fsInfo.Address, prevStatus, domain.FileServerStatusHealthy)
	}

	if err := h.MetaServer.saveStateLocked(); err != nil {
		log.Printf("[METASERVER] ERROR: failed to persist state after heartbeat: %v", err)
		return &pb.HeartbeatResponse{Success: false, Error: "failed to persist metaserver state"}, nil
	}

	return &pb.HeartbeatResponse{Success: true}, nil
}

// Navigate client to the appropriate file server based on user
func (h *GRPCHandler) Navigate(ctx context.Context, req *pb.NavigateRequest) (*pb.NavigateResponse, error) {
	log.Printf("[METASERVER] Navigate request for user: %s", req.RootUser)

	h.MetaServer.mu.Lock()
	defer h.MetaServer.mu.Unlock()

	nowUnix := time.Now().Unix()
	if changed := h.MetaServer.markStaleFileServersLocked(nowUnix); changed {
		if err := h.MetaServer.saveStateLocked(); err != nil {
			log.Printf("[METASERVER] ERROR: failed to persist stale transition: %v", err)
			return &pb.NavigateResponse{Success: false, Error: "failed to persist metaserver state"}, nil
		}
	}

	user := req.Username
	rootUser := req.RootUser
	if user == "" || rootUser == "" {
		return &pb.NavigateResponse{Success: false, Error: "username and root_user are required"}, nil
	}

	_, exists1 := h.MetaServer.users[user]
	if !exists1 {
		log.Printf("[METASERVER] Navigate failed: username '%s' does not exist", req.Username)
		return &pb.NavigateResponse{
			Success: false,
			Error:   "username '" + req.Username + "' does not exist",
		}, nil
	}

	fs, exists2 := h.MetaServer.users[rootUser]
	if !exists2 {
		log.Printf("[METASERVER] Navigate failed: root user '%s' does not exist", req.RootUser)
		return &pb.NavigateResponse{
			Success: false,
			Error:   "root user '" + req.RootUser + "' does not exist",
		}, nil
	}

	rootFS, present := h.MetaServer.fileservers[fs]
	if !present || !h.MetaServer.isHealthyLocked(rootFS, nowUnix) {
		log.Printf("[METASERVER] Navigate failed: root user '%s' is on unavailable file server", rootUser)
		return &pb.NavigateResponse{
			Success: false,
			Error:   "root user '" + rootUser + "' is currently unavailable",
		}, nil
	}

	allowed := false
	if user == rootUser {
		allowed = true
	} else {
		for _, s := range h.MetaServer.shared[user] {
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
	return &pb.NavigateResponse{
		Success: true,
		Address: rootFS.Address,
	}, nil
}

// Navigate client to the appropriate file server based on user
func (h *GRPCHandler) GetRoots(ctx context.Context, req *pb.GetRootsRequest) (*pb.GetRootsResponse, error) {
	log.Printf("[METASERVER] Get roots request for user: %s", req.Username)

	h.MetaServer.mu.Lock()
	defer h.MetaServer.mu.Unlock()

	user := req.Username
	fs, exists := h.MetaServer.users[user]
	if !exists {
		nowUnix := time.Now().Unix()
		if changed := h.MetaServer.markStaleFileServersLocked(nowUnix); changed {
			if err := h.MetaServer.saveStateLocked(); err != nil {
				log.Printf("[METASERVER] ERROR: failed to persist stale transition: %v", err)
				return &pb.GetRootsResponse{Success: false, Error: "failed to persist metaserver state"}, nil
			}
		}

		minFS, ok := h.MetaServer.getLeastLoadedHealthyFileServerLocked(nowUnix)
		if !ok {
			return &pb.GetRootsResponse{Success: false, Error: "no healthy file server registered"}, nil
		}

		fs = minFS
		h.MetaServer.users[user] = fs
		h.MetaServer.fileservers[fs].UserCount++
		h.MetaServer.shared[user] = []SharedDirEntry{}

		if err := h.MetaServer.saveStateLocked(); err != nil {
			log.Printf("[METASERVER] ERROR: failed to persist state after user assignment: %v", err)
			return &pb.GetRootsResponse{Success: false, Error: "failed to persist metaserver state"}, nil
		}

		log.Printf("[METASERVER] Assigned user %s to FS %s (users: %d)", user, h.MetaServer.fileservers[fs].Address, h.MetaServer.fileservers[fs].UserCount)
	}

	roots := []string{}
	roots = append(roots, "mydrive")
	for _, sharedRoot := range h.MetaServer.shared[user] {
		roots = append(roots, sharedRoot.Owner)
	}

	return &pb.GetRootsResponse{
		Success: true,
		Roots:   roots,
	}, nil
}

// Share a root
func (h *GRPCHandler) RootShare(ctx context.Context, req *pb.RootShareRequest) (*pb.RootShareResponse, error) {
	log.Printf("[METASERVER] Root share request for user root: %s to share with: %s", req.RootUser, req.ShareWith)

	h.MetaServer.mu.Lock()
	defer h.MetaServer.mu.Unlock()

	// Consistency check 1: root user must exist
	if _, exists := h.MetaServer.users[req.RootUser]; !exists {
		log.Printf(
			"[METASERVER] Share failed: root user '%s' does not exist",
			req.RootUser,
		)

		return &pb.RootShareResponse{
			Success: false,
			Error:   "root user '%s' does not exist" + req.RootUser,
		}, nil
	}

	// Consistency check 2: shared_with user must exist
	if _, exists := h.MetaServer.users[req.ShareWith]; !exists {
		log.Printf(
			"[METASERVER] Share failed: target user '%s' does not exist",
			req.ShareWith,
		)

		return &pb.RootShareResponse{
			Success: false,
			Error:   "target user '%s' does not exist" + req.ShareWith,
		}, nil
	}

	// Consistency check 3: avoid duplicate sharing entries
	for _, existing := range h.MetaServer.shared[req.ShareWith] {
		if existing.Owner == req.RootUser {
			log.Printf(
				"[METASERVER] Share skipped: root '%s' already shared with '%s'",
				req.RootUser,
				req.ShareWith,
			)

			return &pb.RootShareResponse{
				Success: true,
			}, nil
		}
	}

	// Do sharing
	h.MetaServer.shared[req.ShareWith] = append(h.MetaServer.shared[req.ShareWith], SharedDirEntry{Owner: req.RootUser})
	if err := h.MetaServer.saveStateLocked(); err != nil {
		log.Printf("[METASERVER] ERROR: failed to persist state after share: %v", err)
		return &pb.RootShareResponse{Success: false, Error: "failed to persist metaserver state"}, nil
	}
	log.Printf("[METASERVER] User root %s successfully shared with %s", req.RootUser, req.ShareWith)
	return &pb.RootShareResponse{
		Success: true,
	}, nil
}

// Unshare a root
func (h *GRPCHandler) RootUnshare(ctx context.Context, req *pb.RootUnshareRequest) (*pb.RootUnshareResponse, error) {
	log.Printf("[METASERVER] Root unshare request for user root: %s to unshare with: %s", req.RootUser, req.UnshareWith)

	h.MetaServer.mu.Lock()
	defer h.MetaServer.mu.Unlock()

	// Consistency check 1: root user must exist
	if _, exists := h.MetaServer.users[req.RootUser]; !exists {
		log.Printf(
			"[METASERVER] Share failed: root user '%s' does not exist",
			req.RootUser,
		)

		return &pb.RootUnshareResponse{
			Success: false,
			Error:   "root user '%s' does not exist" + req.RootUser,
		}, nil
	}

	// Consistency check 2: shared_with user must exist
	if _, exists := h.MetaServer.users[req.UnshareWith]; !exists {
		log.Printf(
			"[METASERVER] Share failed: target user '%s' does not exist",
			req.UnshareWith,
		)

		return &pb.RootUnshareResponse{
			Success: false,
			Error:   "target user '%s' does not exist" + req.UnshareWith,
		}, nil
	}

	// Check 3: verify sharing actually exists
	sharedRoots := h.MetaServer.shared[req.UnshareWith]
	index := -1
	for i, root := range sharedRoots {
		if root.Owner == req.RootUser {
			index = i
			break
		}
	}

	if index == -1 {
		log.Printf(
			"[METASERVER] Unshare skipped: root '%s' was not shared with '%s'", req.RootUser, req.UnshareWith)

		return &pb.RootUnshareResponse{
			Success: true,
		}, nil
	}

	// Remove entry from slice
	h.MetaServer.shared[req.UnshareWith] = append(
		sharedRoots[:index],
		sharedRoots[index+1:]...,
	)
	if err := h.MetaServer.saveStateLocked(); err != nil {
		log.Printf("[METASERVER] ERROR: failed to persist state after unshare: %v", err)
		return &pb.RootUnshareResponse{Success: false, Error: "failed to persist metaserver state"}, nil
	}

	log.Printf("[METASERVER] Root '%s' successfully unshared from '%s'", req.RootUser, req.UnshareWith)
	return &pb.RootUnshareResponse{
		Success: true,
	}, nil
}
