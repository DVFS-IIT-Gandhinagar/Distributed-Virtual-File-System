package fileserver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	pb "github.com/umangshikarvar/dvfs/api/fileserver"
)

// RegisterClient registers a client for callbacks and creates user directory
func (fs *FileServer) RegisterClient(ctx context.Context, req *pb.RegisterClientRequest) (*pb.RegisterClientResponse, error) {
	fs.clientsMu.Lock()
	fs.clients[req.ClientId] = req.CallbackAddress
	fs.clientsMu.Unlock()

	fmt.Printf("Registered client %s (user: %s) at %s\n", req.ClientId, req.Username, req.CallbackAddress)

	// Create user directory if it doesn't exist
	userDir := filepath.Join(fs.rootDir, req.Username)
	if err := os.MkdirAll(userDir, 0755); err != nil {
		return &pb.RegisterClientResponse{
			Success: false,
			Error:   fmt.Sprintf("failed to create user directory: %v", err),
		}, nil
	}

	// Find or create user root FID
	fs.mu.Lock()
	var userRootFID *pb.FID
	for _, inode := range fs.inodeDB {
		if inode.OSPath == userDir {
			userRootFID = inode.FID
			break
		}
	}

	if userRootFID == nil {
		// Create new inode for user directory
		userRootFID = fs.allocateFID()
		userInode := &Inode{
			FID:      userRootFID,
			Type:     pb.InodeType_DIRECTORY,
			Name:     req.Username,
			OSPath:   userDir,
			ACL:      &pb.ACL{ReadUsers: []string{req.Username}, WriteUsers: []string{req.Username}},
			Children: []*pb.FID{},
			Version:  1,
		}
		fs.inodeDB[fs.fidToKey(userRootFID)] = userInode
		fmt.Printf("Created user directory for %s (FID: %s)\n", req.Username, fs.fidToKey(userRootFID))
	}
	fs.mu.Unlock()

	return &pb.RegisterClientResponse{
		Success:     true,
		UserRootFid: userRootFID,
	}, nil
}
