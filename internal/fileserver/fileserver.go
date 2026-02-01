package fileserver

import (
	"fmt"
	"log"
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
	users       map[string]*domain.FID
	nextInodeID uint64
	mu          sync.RWMutex
}

// NewFileServer creates a new file server object, either blank or loading from existing data
func NewFileServer(serverID, rootDir string) (*FileServer, error) {
	fs := &FileServer{
		serverID:    serverID,
		rootDir:     rootDir,
		inodes:      make(map[string]*domain.Inode),
		users:       make(map[string]*domain.FID),
		nextInodeID: 0,
	}

	// Check if rootDir already exists
	if _, err := os.Stat(rootDir); err == nil {
		// Root directory exists, scan and load existing data
		if err := fs.loadExistingData(); err != nil {
			return nil, fmt.Errorf("failed to load existing data: %w", err)
		}
		log.Printf("Loaded existing data from root directory, current inode count: %d", len(fs.inodes))
	} else {
		// Root directory doesn't exist, create it
		if err := os.MkdirAll(rootDir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create root dir: %w", err)
		}
	}

	return fs, nil
}

// loadExistingData scans the root directory and creates inodes for existing files and directories
func (fs *FileServer) loadExistingData() error {
	// Scan user directories (first level under rootDir)
	entries, err := os.ReadDir(fs.rootDir)
	if err != nil {
		return fmt.Errorf("failed to read root directory: %w", err)
	}

	// user volumes are at first level
	for _, entry := range entries {
		if entry.IsDir() {
			username := entry.Name()
			userDir := filepath.Join(fs.rootDir, username)

			// Create root inode for this user (always inode ID 0)
			userRootFID := &domain.FID{
				FileServerID:     fs.serverID,
				InodeID:          fs.nextInodeID,
				GenerationNumber: 1,
			}
			atomic.AddUint64(&fs.nextInodeID, 1)
			fs.users[username] = userRootFID

			// Create root inode
			userRootInode := &domain.Inode{
				FID:      userRootFID,
				Type:     domain.InodeTypeDirectory,
				Name:     username,
				OSPath:   userDir,
				Owner:    username,
				Children: make([]*domain.FID, 0),
			}

			fs.inodes[userRootFID.String()] = userRootInode

			// Scan user's files and directories (first level only)
			if err := fs.scanUserDirectory(userDir, userRootInode); err != nil {
				return fmt.Errorf("failed to scan user directory %s: %w", username, err)
			}
		}
	}

	return nil
}

// scanUserDirectory scans a user's directory and creates inodes for first-level files and directories
func (fs *FileServer) scanUserDirectory(userDir string, parentInode *domain.Inode) error {
	entries, err := os.ReadDir(userDir)
	if err != nil {
		return fmt.Errorf("failed to read user directory: %w", err)
	}

	for _, entry := range entries {
		// Generate new FID for this item
		newFID := &domain.FID{
			FileServerID:     fs.serverID,
			InodeID:          fs.nextInodeID,
			GenerationNumber: 1,
		}

		// Determine type
		var inodeType domain.InodeType
		if entry.IsDir() {
			inodeType = domain.InodeTypeDirectory
		} else {
			inodeType = domain.InodeTypeFile
		}

		// Create inode
		itemPath := filepath.Join(userDir, entry.Name())
		newInode := &domain.Inode{
			FID:    newFID,
			Type:   inodeType,
			Name:   entry.Name(),
			OSPath: itemPath,
			Owner:  parentInode.Owner, // Same as parent (user)
		}

		// Set up directory-specific fields
		if inodeType == domain.InodeTypeDirectory {
			newInode.Children = make([]*domain.FID, 0)
		} else {
			// For files, get the current size
			if info, err := entry.Info(); err == nil {
				newInode.Size = uint64(info.Size())
			}
		}

		// Store inode
		fs.inodes[newFID.String()] = newInode

		// Add to parent's children
		parentInode.Children = append(parentInode.Children, newFID)

		// Increment inode ID counter
		atomic.AddUint64(&fs.nextInodeID, 1)
	}

	return nil
}

// GetUserRoot returns the root FID for a user, creating it if necessary
func (fs *FileServer) GetUserRoot(username string) (*domain.FID, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	// Check if user already exists
	if rootFID := fs.users[username]; rootFID != nil {
		return rootFID, nil
	}

	// Create user directory if it doesn't exist
	userDir := filepath.Join(fs.rootDir, username)
	if err := os.MkdirAll(userDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create user dir: %w", err)
	}

	// Create root FID for user
	rootFID := &domain.FID{
		FileServerID:     fs.serverID,
		InodeID:          fs.nextInodeID,
		GenerationNumber: 1,
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
	fs.users[username] = rootFID

	atomic.AddUint64(&fs.nextInodeID, 1)

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
	newFID := &domain.FID{
		FileServerID:     fs.serverID,
		InodeID:          fs.nextInodeID,
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

	atomic.AddUint64(&fs.nextInodeID, 1)

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