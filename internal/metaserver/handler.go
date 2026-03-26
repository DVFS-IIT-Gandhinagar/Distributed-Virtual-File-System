package metaserver

import (
	"context"
	"log"

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
	
	h.MetaServer.mu.Lock()
	defer h.MetaServer.mu.Unlock()

	users := req.Users
	count := len(users)

	// Create new file server info
	fsInfo := &domain.FileServerInfo{
		Address:  req.Address,
		UserCount: count,
	}

	// Register file server and users
	h.MetaServer.fileservers[h.MetaServer.nextFsID] = fsInfo
	for _, u := range users {
		fs, exists := h.MetaServer.users[u]
		if exists {
			log.Printf("[METASERVER] ERROR: User %s already exists in FS %s", u, h.MetaServer.fileservers[fs].Address)
			return &pb.RegisterFileServerResponse{
				Success: false,
				Error: "User " + u + " already exists in file server: " + h.MetaServer.fileservers[fs].Address,
			}, nil
		} 
	}

	for _, u := range users {
		h.MetaServer.users[u] = h.MetaServer.nextFsID
	}
	h.MetaServer.nextFsID++

	log.Printf("[METASERVER] FS registered successfully: ID=%d, Address=%s, Users=%d", h.MetaServer.nextFsID-1, req.Address, count)
	return &pb.RegisterFileServerResponse{
		Success: true,
	}, nil
}

// Navigate client to the appropriate file server based on user
func (h *GRPCHandler) Navigate(ctx context.Context, req *pb.NavigateRequest) (*pb.NavigateResponse, error) {
	log.Printf("[METASERVER] Navigate request for user: %s", req.RootUser)
	
	h.MetaServer.mu.Lock()
	defer h.MetaServer.mu.Unlock()

	user := req.RootUser
	fs, exists := h.MetaServer.users[user]
	if !exists {
		log.Printf("[METASERVER] New user %s, assigning to least loaded FS", user)
		min_fs := uint64(0)
		min_users := h.MetaServer.fileservers[min_fs].UserCount
		for fs_no, fsInfo := range h.MetaServer.fileservers {
			if fsInfo.UserCount < min_users {
				min_fs = fs_no
				min_users = fsInfo.UserCount
			}
		}
		fs = min_fs
		h.MetaServer.users[user] = fs
		h.MetaServer.fileservers[fs].UserCount++
		log.Printf("[METASERVER] Assigned user %s to FS %s (users: %d)", user, h.MetaServer.fileservers[fs].Address, h.MetaServer.fileservers[fs].UserCount)
	}

	log.Printf("[METASERVER] Routing user %s to FS %s", user, h.MetaServer.fileservers[fs].Address)
	return &pb.NavigateResponse{
		Success: true,
		Address: h.MetaServer.fileservers[fs].Address,
	}, nil
}