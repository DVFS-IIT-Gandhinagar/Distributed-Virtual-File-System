package fileserver

import (
	"fmt"
	"os"
	"sync"

	pb "github.com/umangshikarvar/dvfs/api/fileserver"
)

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
