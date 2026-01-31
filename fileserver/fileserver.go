package fileserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	cbpb "github.com/umangshikarvar/dvfs/proto/callback"
	pb "github.com/umangshikarvar/dvfs/proto/fileserver"
	"google.golang.org/grpc"
)

// FIDKey is a string representation of FID for map keys
type FIDKey string

// Inode represents a file or directory
type Inode struct {
	FID      *pb.FID
	Type     pb.InodeType
	Name     string
	OSPath   string
	ACL      *pb.ACL
	Children []*pb.FID // for directories
	Version  uint64    // for cache validation
	mu       sync.RWMutex
}

// FileServer represents the file server
type FileServer struct {
	pb.UnimplementedFileServerServer

	serverID    string
	rootDir     string // OS filesystem root for this server
	inodeDB     map[FIDKey]*Inode
	nextInodeID uint64
	mu          sync.RWMutex

	// Client callback management
	clients   map[string]string // client_id -> callback_address
	clientsMu sync.RWMutex

	// Track which clients have which files open (for callbacks)
	openFiles   map[FIDKey]map[string]bool // FID -> set of client_ids
	openFilesMu sync.RWMutex
}

// NewFileServer creates a new file server instance
func NewFileServer(serverID, rootDir string) (*FileServer, error) {
	// Create root directory if it doesn't exist
	if err := os.MkdirAll(rootDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create root dir: %v", err)
	}

	fs := &FileServer{
		serverID:    serverID,
		rootDir:     rootDir,
		inodeDB:     make(map[FIDKey]*Inode),
		nextInodeID: 1,
		clients:     make(map[string]string),
		openFiles:   make(map[FIDKey]map[string]bool),
	}

	// Create root inode
	rootFID := &pb.FID{
		FileServerId:     serverID,
		InodeId:          0,
		GenerationNumber: 1,
	}
	rootInode := &Inode{
		FID:      rootFID,
		Type:     pb.InodeType_DIRECTORY,
		Name:     "/",
		OSPath:   rootDir,
		ACL:      &pb.ACL{ReadUsers: []string{"*"}, WriteUsers: []string{"*"}},
		Children: []*pb.FID{},
		Version:  1,
	}
	fs.inodeDB[fs.fidToKey(rootFID)] = rootInode

	return fs, nil
}

// fidToKey converts FID to a string key
func (fs *FileServer) fidToKey(fid *pb.FID) FIDKey {
	return FIDKey(fmt.Sprintf("%s_%d_%d", fid.FileServerId, fid.InodeId, fid.GenerationNumber))
}

// allocateFID creates a new FID
func (fs *FileServer) allocateFID() *pb.FID {
	inodeID := atomic.AddUint64(&fs.nextInodeID, 1)
	return &pb.FID{
		FileServerId:     fs.serverID,
		InodeId:          inodeID,
		GenerationNumber: 1,
	}
}

// checkACL verifies if user has permission
func (fs *FileServer) checkACL(acl *pb.ACL, user string, operation string) bool {
	if acl == nil {
		return false
	}

	var allowedUsers []string
	switch operation {
	case "read":
		allowedUsers = acl.ReadUsers
	case "write":
		allowedUsers = acl.WriteUsers
	default:
		return false
	}

	for _, u := range allowedUsers {
		if u == "*" || u == user {
			return true
		}
	}
	return false
}

// getInode retrieves an inode by FID
func (fs *FileServer) getInode(fid *pb.FID) (*Inode, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	inode, ok := fs.inodeDB[fs.fidToKey(fid)]
	if !ok {
		return nil, errors.New("inode not found")
	}
	return inode, nil
}

// RegisterClient registers a client for callbacks
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

// invalidateCache sends invalidation callbacks to other clients
func (fs *FileServer) invalidateCache(fid *pb.FID, writingClientID string, newVersion uint64) {
	fs.openFilesMu.RLock()
	key := fs.fidToKey(fid)
	clients := make([]string, 0)
	for clientID := range fs.openFiles[key] {
		if clientID != writingClientID {
			clients = append(clients, clientID)
		}
	}
	fs.openFilesMu.RUnlock()

	fs.clientsMu.RLock()
	defer fs.clientsMu.RUnlock()

	for _, clientID := range clients {
		address, ok := fs.clients[clientID]
		if !ok {
			continue
		}

		go func(addr string, cID string) {
			conn, err := grpc.Dial(addr, grpc.WithInsecure())
			if err != nil {
				fmt.Printf("Failed to connect to client %s: %v\n", cID, err)
				return
			}
			defer conn.Close()

			client := cbpb.NewClientCallbackClient(conn)
			_, err = client.Invalidate(context.Background(), &cbpb.InvalidateRequest{
				Fid:        fid,
				NewVersion: newVersion,
			})
			if err != nil {
				fmt.Printf("Failed to invalidate cache on client %s: %v\n", cID, err)
			} else {
				fmt.Printf("Invalidated cache on client %s\n", cID)
			}
		}(address, clientID)
	}
}
