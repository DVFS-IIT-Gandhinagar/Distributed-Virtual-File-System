package fileserver

import (
	"context"
	"os"
	"path/filepath"

	pb "github.com/umangshikarvar/dvfs/api/fileserver"
)

// GetAttr gets file attributes
func (fs *FileServer) GetAttr(ctx context.Context, req *pb.GetAttrRequest) (*pb.GetAttrResponse, error) {
	inode, err := fs.getInode(req.Fid)
	if err != nil {
		return &pb.GetAttrResponse{Success: false, Error: err.Error()}, nil
	}

	inode.mu.RLock()
	defer inode.mu.RUnlock()

	// Check ACL
	if !fs.checkACL(inode.ACL, req.User, "read") {
		return &pb.GetAttrResponse{Success: false, Error: "permission denied"}, nil
	}

	var size uint64
	if inode.Type == pb.InodeType_FILE {
		info, err := os.Stat(inode.OSPath)
		if err != nil {
			return &pb.GetAttrResponse{Success: false, Error: err.Error()}, nil
		}
		size = uint64(info.Size())
	}

	return &pb.GetAttrResponse{
		Success: true,
		Name:    inode.Name,
		Type:    inode.Type,
		Size:    size,
		Version: inode.Version,
	}, nil
}

// ListDir lists directory contents
func (fs *FileServer) ListDir(ctx context.Context, req *pb.ListDirRequest) (*pb.ListDirResponse, error) {
	inode, err := fs.getInode(req.Fid)
	if err != nil {
		return &pb.ListDirResponse{Success: false, Error: err.Error()}, nil
	}

	inode.mu.RLock()
	defer inode.mu.RUnlock()

	// Check ACL
	if !fs.checkACL(inode.ACL, req.User, "read") {
		return &pb.ListDirResponse{Success: false, Error: "permission denied"}, nil
	}

	if inode.Type != pb.InodeType_DIRECTORY {
		return &pb.ListDirResponse{Success: false, Error: "not a directory"}, nil
	}

	// Read directory from OS
	entries, err := os.ReadDir(inode.OSPath)
	if err != nil {
		return &pb.ListDirResponse{Success: false, Error: err.Error()}, nil
	}

	var dirEntries []*pb.DirEntry
	for _, entry := range entries {
		// For simplicity, create temporary FIDs for listing
		// In a real implementation, we'd look up existing inodes
		entryType := pb.InodeType_FILE
		if entry.IsDir() {
			entryType = pb.InodeType_DIRECTORY
		}

		dirEntries = append(dirEntries, &pb.DirEntry{
			Name: entry.Name(),
			Type: entryType,
		})
	}

	return &pb.ListDirResponse{
		Success: true,
		Entries: dirEntries,
	}, nil
}

// Lookup finds a file by name in a directory
func (fs *FileServer) Lookup(ctx context.Context, req *pb.LookupRequest) (*pb.LookupResponse, error) {
	parentInode, err := fs.getInode(req.ParentFid)
	if err != nil {
		return &pb.LookupResponse{Success: false, Error: err.Error()}, nil
	}

	parentInode.mu.RLock()
	defer parentInode.mu.RUnlock()

	// Check ACL
	if !fs.checkACL(parentInode.ACL, req.User, "read") {
		return &pb.LookupResponse{Success: false, Error: "permission denied"}, nil
	}

	// Search for child by name
	targetPath := filepath.Join(parentInode.OSPath, req.Name)

	// Check if file exists on OS
	info, err := os.Stat(targetPath)
	if err != nil {
		return &pb.LookupResponse{Success: false, Error: "file not found"}, nil
	}

	// Look for existing inode or create new one
	fs.mu.Lock()
	var foundFID *pb.FID
	for _, inode := range fs.inodeDB {
		if inode.OSPath == targetPath {
			foundFID = inode.FID
			break
		}
	}

	// If not found, create new inode entry
	if foundFID == nil {
		foundFID = fs.allocateFID()
		inodeType := pb.InodeType_FILE
		if info.IsDir() {
			inodeType = pb.InodeType_DIRECTORY
		}
		newInode := &Inode{
			FID:      foundFID,
			Type:     inodeType,
			Name:     req.Name,
			OSPath:   targetPath,
			ACL:      &pb.ACL{ReadUsers: []string{"*"}, WriteUsers: []string{"*"}},
			Children: []*pb.FID{},
			Version:  1,
		}
		fs.inodeDB[fs.fidToKey(foundFID)] = newInode
	}
	fs.mu.Unlock()

	return &pb.LookupResponse{
		Success: true,
		Fid:     foundFID,
	}, nil
}
