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
		h.MetaServer.shared[u] = []string{}
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
			Error: "root user '%s' does not exist" + req.RootUser,
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
			Error: "target user '%s' does not exist" + req.ShareWith,
		}, nil
	}

	// Consistency check 3: avoid duplicate sharing entries
	for _, existing := range h.MetaServer.shared[req.ShareWith] {
		if existing == req.RootUser {
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
	h.MetaServer.shared[req.ShareWith] = append(h.MetaServer.shared[req.ShareWith], req.RootUser)
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
			Error: "root user '%s' does not exist" + req.RootUser,
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
			Error: "target user '%s' does not exist" + req.UnshareWith,
		}, nil
	}

	// Check 3: verify sharing actually exists
	sharedRoots := h.MetaServer.shared[req.UnshareWith]
	index := -1
	for i, root := range sharedRoots {
		if root == req.RootUser {
			index = i
			break
		}
	}

	if index == -1 {
		log.Printf(
			"[METASERVER] Unshare skipped: root '%s' was not shared with '%s'",req.RootUser,req.UnshareWith)

		return &pb.RootUnshareResponse{
			Success: true,
		}, nil
	}

	// Remove entry from slice
	h.MetaServer.shared[req.UnshareWith] = append(
		sharedRoots[:index],
		sharedRoots[index+1:]...,
	)

	log.Printf("[METASERVER] Root '%s' successfully unshared from '%s'",req.RootUser,req.UnshareWith)
	return &pb.RootUnshareResponse{
		Success: true,
	}, nil
}