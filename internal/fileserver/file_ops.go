package fileserver

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	pb "github.com/umangshikarvar/dvfs/api/fileserver"
)

// CreateFile creates a new file or directory
func (fs *FileServer) CreateFile(ctx context.Context, req *pb.CreateFileRequest) (*pb.CreateFileResponse, error) {
	// Allocate new FID
	fid := fs.allocateFID()

	// Construct OS path under user's directory
	var osPath string
	if req.ParentPath == "" {
		// Create in user's root directory
		osPath = filepath.Join(fs.rootDir, req.User, req.Name)
	} else {
		// Create in subdirectory under user's root
		osPath = filepath.Join(fs.rootDir, req.User, req.ParentPath, req.Name)
	}

	// Create on OS filesystem
	var err error
	if req.Type == pb.InodeType_DIRECTORY {
		err = os.Mkdir(osPath, 0755)
	} else {
		var f *os.File
		f, err = os.Create(osPath)
		if f != nil {
			f.Close()
		}
	}

	if err != nil {
		return &pb.CreateFileResponse{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	// Create inode
	inode := &Inode{
		FID:      fid,
		Type:     req.Type,
		Name:     req.Name,
		OSPath:   osPath,
		ACL:      &pb.ACL{ReadUsers: []string{req.User}, WriteUsers: []string{req.User}},
		Children: []*pb.FID{},
		Version:  1,
	}

	fs.mu.Lock()
	fs.inodeDB[fs.fidToKey(fid)] = inode
	fs.mu.Unlock()

	fmt.Printf("Created %s: %s -> %s\n", req.Type, req.Name, osPath)

	return &pb.CreateFileResponse{
		Fid:     fid,
		Success: true,
	}, nil
}

// OpenFile opens a file for reading/writing
func (fs *FileServer) OpenFile(ctx context.Context, req *pb.OpenFileRequest) (*pb.OpenFileResponse, error) {
	inode, err := fs.getInode(req.Fid)
	if err != nil {
		return &pb.OpenFileResponse{Success: false, Error: err.Error()}, nil
	}

	inode.mu.RLock()
	defer inode.mu.RUnlock()

	// Check ACL
	if !fs.checkACL(inode.ACL, req.User, "read") {
		return &pb.OpenFileResponse{Success: false, Error: "permission denied"}, nil
	}

	// Get file size
	var fileSize uint64
	if inode.Type == pb.InodeType_FILE {
		info, err := os.Stat(inode.OSPath)
		if err != nil {
			return &pb.OpenFileResponse{Success: false, Error: err.Error()}, nil
		}
		fileSize = uint64(info.Size())
	}

	// Track that this client has this file open
	fs.openFilesMu.Lock()
	key := fs.fidToKey(req.Fid)
	if fs.openFiles[key] == nil {
		fs.openFiles[key] = make(map[string]bool)
	}
	fs.openFiles[key][req.ClientId] = true
	fs.openFilesMu.Unlock()

	fmt.Printf("Client %s opened file %s (version %d)\n", req.ClientId, inode.Name, inode.Version)

	return &pb.OpenFileResponse{
		Success:  true,
		FileSize: fileSize,
		Version:  inode.Version,
	}, nil
}

// ReadFile reads data from a file
func (fs *FileServer) ReadFile(ctx context.Context, req *pb.ReadFileRequest) (*pb.ReadFileResponse, error) {
	inode, err := fs.getInode(req.Fid)
	if err != nil {
		return &pb.ReadFileResponse{Success: false, Error: err.Error()}, nil
	}

	inode.mu.RLock()
	osPath := inode.OSPath
	acl := inode.ACL
	inode.mu.RUnlock()

	// Check ACL
	if !fs.checkACL(acl, req.User, "read") {
		return &pb.ReadFileResponse{Success: false, Error: "permission denied"}, nil
	}

	// Read from OS file
	file, err := os.Open(osPath)
	if err != nil {
		return &pb.ReadFileResponse{Success: false, Error: err.Error()}, nil
	}
	defer file.Close()

	// Seek to offset
	if _, err := file.Seek(int64(req.Offset), 0); err != nil {
		return &pb.ReadFileResponse{Success: false, Error: err.Error()}, nil
	}

	// Read data
	data := make([]byte, req.Length)
	n, err := file.Read(data)
	if err != nil && err != io.EOF {
		return &pb.ReadFileResponse{Success: false, Error: err.Error()}, nil
	}

	return &pb.ReadFileResponse{
		Success: true,
		Data:    data[:n],
	}, nil
}

// WriteFile writes data to a file
func (fs *FileServer) WriteFile(ctx context.Context, req *pb.WriteFileRequest) (*pb.WriteFileResponse, error) {
	inode, err := fs.getInode(req.Fid)
	if err != nil {
		return &pb.WriteFileResponse{Success: false, Error: err.Error()}, nil
	}

	inode.mu.Lock()

	// Check ACL
	if !fs.checkACL(inode.ACL, req.User, "write") {
		inode.mu.Unlock()
		return &pb.WriteFileResponse{Success: false, Error: "permission denied"}, nil
	}

	osPath := inode.OSPath
	inode.mu.Unlock()

	// Write to OS file
	file, err := os.OpenFile(osPath, os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		return &pb.WriteFileResponse{Success: false, Error: err.Error()}, nil
	}
	defer file.Close()

	// Seek to offset
	if _, err := file.Seek(int64(req.Offset), 0); err != nil {
		return &pb.WriteFileResponse{Success: false, Error: err.Error()}, nil
	}

	// Write data
	if _, err := file.Write(req.Data); err != nil {
		return &pb.WriteFileResponse{Success: false, Error: err.Error()}, nil
	}

	// Increment version
	inode.mu.Lock()
	inode.Version++
	newVersion := inode.Version
	inode.mu.Unlock()

	// Invalidate cache on other clients
	go fs.invalidateCache(req.Fid, req.ClientId, newVersion)

	fmt.Printf("Write to %s, new version: %d\n", inode.Name, newVersion)

	return &pb.WriteFileResponse{
		Success: true,
		Version: newVersion,
	}, nil
}

// CloseFile closes a file
func (fs *FileServer) CloseFile(ctx context.Context, req *pb.CloseFileRequest) (*pb.CloseFileResponse, error) {
	// Remove from open files tracking
	fs.openFilesMu.Lock()
	key := fs.fidToKey(req.Fid)
	if clients, ok := fs.openFiles[key]; ok {
		delete(clients, req.ClientId)
		if len(clients) == 0 {
			delete(fs.openFiles, key)
		}
	}
	fs.openFilesMu.Unlock()

	inode, _ := fs.getInode(req.Fid)
	if inode != nil {
		fmt.Printf("Client %s closed file %s\n", req.ClientId, inode.Name)
	}

	return &pb.CloseFileResponse{Success: true}, nil
}

// DeleteFile deletes a file or directory
func (fs *FileServer) DeleteFile(ctx context.Context, req *pb.DeleteFileRequest) (*pb.DeleteFileResponse, error) {
	inode, err := fs.getInode(req.Fid)
	if err != nil {
		return &pb.DeleteFileResponse{Success: false, Error: err.Error()}, nil
	}

	inode.mu.Lock()
	defer inode.mu.Unlock()

	// Check ACL
	if !fs.checkACL(inode.ACL, req.User, "write") {
		return &pb.DeleteFileResponse{Success: false, Error: "permission denied"}, nil
	}

	// Delete from OS
	if err := os.RemoveAll(inode.OSPath); err != nil {
		return &pb.DeleteFileResponse{Success: false, Error: err.Error()}, nil
	}

	// Remove from inodeDB
	fs.mu.Lock()
	delete(fs.inodeDB, fs.fidToKey(req.Fid))
	fs.mu.Unlock()

	fmt.Printf("Deleted %s\n", inode.Name)

	return &pb.DeleteFileResponse{Success: true}, nil
}
