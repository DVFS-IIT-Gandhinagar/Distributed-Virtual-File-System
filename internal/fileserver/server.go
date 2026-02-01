package fileserver

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/umangshikarvar/dvfs/internal/domain"
)

// FileServer provides basic file operations
type FileServer struct {
	serverID    string
	rootDir     string
	inodes      map[string]*domain.Inode // FID string -> Inode
	nextInodeID uint64
	mu          sync.RWMutex
}

// NewFileServer creates a new file server
func NewFileServer(serverID, rootDir string) (*FileServer, error) {
	if err := os.MkdirAll(rootDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create root dir: %w", err)
	}

	return &FileServer{
		serverID:    serverID,
		rootDir:     rootDir,
		inodes:      make(map[string]*domain.Inode),
		nextInodeID: 1,
	}, nil
}

// GetUserRoot returns the root FID for a user, creating it if necessary
func (fs *FileServer) GetUserRoot(username string) (*domain.FID, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	// Create user directory if it doesn't exist
	userDir := filepath.Join(fs.rootDir, username)
	if err := os.MkdirAll(userDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create user dir: %w", err)
	}

	// Create root FID for user (always inode ID 0 for root)
	rootFID := &domain.FID{
		FileServerID:     fs.serverID,
		InodeID:          0,
		GenerationNumber: 1,
	}

	// Check if root inode already exists
	if inode := fs.inodes[rootFID.String()]; inode != nil {
		return rootFID, nil
	}

	// Create root inode
	rootInode := &domain.Inode{
		FID:      rootFID,
		Type:     domain.InodeTypeDirectory,
		Name:     username,
		OSPath:   userDir,
		Owner:    username,
		Children: make([]*domain.FID, 0),
	}

	fs.inodes[rootFID.String()] = rootInode
	return rootFID, nil
}

// CreateFile creates a new file or directory
func (fs *FileServer) CreateFile(parentFID *domain.FID, name, username string, fileType domain.InodeType) (*domain.FID, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	// Get parent inode
	parent, exists := fs.inodes[parentFID.String()]
	if !exists {
		return nil, fmt.Errorf("parent directory not found")
	}

	// Check if parent is directory
	if parent.Type != domain.InodeTypeDirectory {
		return nil, fmt.Errorf("parent is not a directory")
	}

	// Generate new FID
	inodeID := atomic.AddUint64(&fs.nextInodeID, 1)
	newFID := &domain.FID{
		FileServerID:     fs.serverID,
		InodeID:          inodeID,
		GenerationNumber: 1,
	}

	// Create OS path
	osPath := filepath.Join(parent.OSPath, name)

	// Create on filesystem
	switch fileType {
	case domain.InodeTypeFile:
		file, err := os.Create(osPath)
		if err != nil {
			return nil, fmt.Errorf("failed to create file: %w", err)
		}
		file.Close()
	case domain.InodeTypeDirectory:
		if err := os.Mkdir(osPath, 0755); err != nil {
			return nil, fmt.Errorf("failed to create directory: %w", err)
		}
	}

	// Create inode
	newInode := &domain.Inode{
		FID:    newFID,
		Type:   fileType,
		Name:   name,
		OSPath: osPath,
		Owner:  username,
	}

	if fileType == domain.InodeTypeDirectory {
		newInode.Children = make([]*domain.FID, 0)
	}

	// Store inode
	fs.inodes[newFID.String()] = newInode

	// Add to parent's children
	parent.Children = append(parent.Children, newFID)

	return newFID, nil
}

// GetInode retrieves an inode by FID
func (fs *FileServer) GetInode(fid *domain.FID) (*domain.Inode, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	inode, exists := fs.inodes[fid.String()]
	if !exists {
		return nil, fmt.Errorf("inode not found")
	}

	// Update size for files
	if inode.Type == domain.InodeTypeFile {
		if info, err := os.Stat(inode.OSPath); err == nil {
			inode.Size = uint64(info.Size())
		}
	}

	return inode, nil
}

// ListDirectory lists contents of a directory
func (fs *FileServer) ListDirectory(dirFID *domain.FID) ([]*domain.Inode, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	dirInode, exists := fs.inodes[dirFID.String()]
	if !exists {
		return nil, fmt.Errorf("directory not found")
	}

	if dirInode.Type != domain.InodeTypeDirectory {
		return nil, fmt.Errorf("not a directory")
	}

	children := make([]*domain.Inode, 0, len(dirInode.Children))
	for _, childFID := range dirInode.Children {
		if childInode, exists := fs.inodes[childFID.String()]; exists {
			// Update file size
			if childInode.Type == domain.InodeTypeFile {
				if info, err := os.Stat(childInode.OSPath); err == nil {
					childInode.Size = uint64(info.Size())
				}
			}
			children = append(children, childInode)
		}
	}

	return children, nil
}

// DeleteFile deletes a file or directory
func (fs *FileServer) DeleteFile(fid *domain.FID) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	inode, exists := fs.inodes[fid.String()]
	if !exists {
		return fmt.Errorf("file not found")
	}

	// For directories, ensure they're empty
	if inode.Type == domain.InodeTypeDirectory && len(inode.Children) > 0 {
		return fmt.Errorf("directory not empty")
	}

	// Delete from filesystem
	if err := os.RemoveAll(inode.OSPath); err != nil {
		return fmt.Errorf("failed to delete from filesystem: %w", err)
	}

	// Remove from storage
	delete(fs.inodes, fid.String())

	return nil
}