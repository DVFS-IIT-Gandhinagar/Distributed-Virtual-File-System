package metaserver

import (
	"context"
	"log"
	"time"

	pb "github.com/umangshikarvar/dvfs/api/metaserver"
	"github.com/umangshikarvar/dvfs/internal/domain"
)

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
	for _, u := range req.Users {
		incomingUsers[u] = struct{}{}
	}

	// On registration refresh, remove stale mappings that no longer belong to this file server.
	for u, mappedID := range h.MetaServer.users {
		if mappedID == fsID {
			if _, ok := incomingUsers[u]; !ok {
				delete(h.MetaServer.users, u)
			}
		}
	}

	for u := range incomingUsers {
		fs, exists := h.MetaServer.users[u]
		if exists && fs != fsID {
			log.Printf("[METASERVER] ERROR: User %s already exists in FS %s", u, h.MetaServer.fileservers[fs].Address)
			return &pb.RegisterFileServerResponse{
				Success: false,
				Error:   "User " + u + " already exists in file server: " + h.MetaServer.fileservers[fs].Address,
			}, nil
		} 
		h.MetaServer.users[u] = fsID
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
	log.Printf("[METASERVER] Navigate request for user: %s", req.User)
	
	h.MetaServer.mu.Lock()
	defer h.MetaServer.mu.Unlock()

	nowUnix := time.Now().Unix()
	if changed := h.MetaServer.markStaleFileServersLocked(nowUnix); changed {
		if err := h.MetaServer.saveStateLocked(); err != nil {
			log.Printf("[METASERVER] ERROR: failed to persist stale transition: %v", err)
			return &pb.NavigateResponse{Success: false, Error: "failed to persist metaserver state"}, nil
		}
	}

	user := req.User
	if len(h.MetaServer.fileservers) == 0 {
		log.Printf("[METASERVER] No registered file servers available for user %s", user)
		return &pb.NavigateResponse{
			Success: false,
			Error:   "no file server registered",
		}, nil
	}

	fs, exists := h.MetaServer.users[user]
	removedStaleMapping := false
	if exists {
		mappedFS, present := h.MetaServer.fileservers[fs]
		if !present || !h.MetaServer.isHealthyLocked(mappedFS, nowUnix) {
			delete(h.MetaServer.users, user)
			removedStaleMapping = true
			exists = false
		}
	}

	if !exists {
		if removedStaleMapping {
			if err := h.MetaServer.saveStateLocked(); err != nil {
				log.Printf("[METASERVER] ERROR: failed to persist stale user remap cleanup: %v", err)
				return &pb.NavigateResponse{Success: false, Error: "failed to persist metaserver state"}, nil
			}
		}

		log.Printf("[METASERVER] New or remapped user %s, assigning to least loaded healthy FS", user)
		minFS, ok := h.MetaServer.getLeastLoadedHealthyFileServerLocked(nowUnix)
		if !ok {
			return &pb.NavigateResponse{
				Success: false,
				Error:   "no healthy file server registered",
			}, nil
		}

		fs = minFS
		h.MetaServer.users[user] = fs
		h.MetaServer.fileservers[fs].UserCount++

		if err := h.MetaServer.saveStateLocked(); err != nil {
			log.Printf("[METASERVER] ERROR: failed to persist state after user assignment: %v", err)
			return &pb.NavigateResponse{
				Success: false,
				Error:   "failed to persist metaserver state",
			}, nil
		}

		log.Printf("[METASERVER] Assigned user %s to FS %s (users: %d)", user, h.MetaServer.fileservers[fs].Address, h.MetaServer.fileservers[fs].UserCount)
	}

	if h.MetaServer.fileservers[fs] == nil {
		return &pb.NavigateResponse{
			Success: false,
			Error:   "file server entry missing",
		}, nil
	}

	log.Printf("[METASERVER] Routing user %s to FS %s", user, h.MetaServer.fileservers[fs].Address)
	return &pb.NavigateResponse{
		Success: true,
		Address: h.MetaServer.fileservers[fs].Address,
	}, nil
}