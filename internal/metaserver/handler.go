package metaserver

import (
	"context"

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

	user := req.Users
	count := len(user)

	// Create new file server info
	fsInfo := &domain.FileServerInfo{
		Address:  req.Address,
		UserCount: count,
	}

	// Register file server and users
	h.MetaServer.fileservers[h.MetaServer.nextFsID] = fsInfo
	for _, u := range user {
		h.MetaServer.users[u] = h.MetaServer.nextFsID
	}
	h.MetaServer.nextFsID++

	return &pb.RegisterFileServerResponse{
		Success: true,
	}, nil
}