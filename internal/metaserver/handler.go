package metaserver

import (
	"context"
	"fmt"

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
			// If user already exists, we can log an error
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

	return &pb.RegisterFileServerResponse{
		Success: true,
	}, nil
}

// Navigate client to the appropriate file server based on user
func (h *GRPCHandler) Navigate(ctx context.Context, req *pb.NavigateRequest) (*pb.NavigateResponse, error) {
	h.MetaServer.mu.Lock()
	defer h.MetaServer.mu.Unlock()

	user := req.User
	fs, exists := h.MetaServer.users[user]
	if !exists {
		fmt.Printf("User %s not found in any file server, assigning to least loaded server\n", user)
		min_fs := uint64(0)
		min_users := h.MetaServer.fileservers[min_fs].UserCount
		for fs_no, fsInfo := range h.MetaServer.fileservers {
			if fsInfo.UserCount < min_users {
				min_fs = fs_no
				min_users = fsInfo.UserCount
			}
		}
		fs = min_fs
	}

	return &pb.NavigateResponse{
		Success: true,
		Address: h.MetaServer.fileservers[fs].Address,
	}, nil
}