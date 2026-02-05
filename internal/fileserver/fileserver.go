package fileserver

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
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
		// Root directory exists, scan and load existing data using FileScanner
		fileScanner := &FileScanner{rootDir: rootDir, serverID: serverID}

		if err := fileScanner.loadExistingData(&fs.nextInodeID, &fs.inodes, &fs.users); err != nil {
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

	rootInode.Parent = rootInode

	fs.inodes[rootFID.String()] = rootInode
	fs.users[username] = rootFID

	atomic.AddUint64(&fs.nextInodeID, 1)

	return rootFID, nil
}

// GetInode retrieves an inode by FID from the file server (assumes lock is held)
func (fs *FileServer) GetInode(fid *domain.FID) (*domain.Inode, error) {
	inode, exists := fs.inodes[fid.String()]
	if !exists {
		return nil, fmt.Errorf("inode not found, %s", fid.String())
	}

	// Update size for files
	if inode.Type == domain.InodeTypeFile {
		if info, err := os.Stat(inode.OSPath); err == nil {
			inode.Size = uint64(info.Size())
		}
	}

	return inode, nil
}

// find the inode in parent's children
func (fs *FileServer) GetChildInodeByName(parentInode *domain.Inode, name string) (*domain.Inode, error) {	
	var inode *domain.Inode
	found := false
	for _, childFID := range parentInode.Children {
		childInode, exists := fs.inodes[childFID.String()]
		if !exists {
			continue
		}
		if childInode.Name == name {
			inode = childInode
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("inode not found, %s", name)
	}
	return inode, nil
}

// Return path as pwd
func (fs *FileServer) Path(dirFID *domain.FID) (string, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	dirInode, err := fs.GetInode(dirFID)
	if err != nil {
		return "", fmt.Errorf("directory not found, %s", dirFID.String())
	}

	if dirInode.Type != domain.InodeTypeDirectory {
		return "", fmt.Errorf("not a directory")
	}

	path, err := filepath.Rel(fs.rootDir, dirInode.OSPath)
	if err != nil {
    	return "", fmt.Errorf("internal error can't compute path")
	}
	return path, nil
}

func (fs *FileServer) ChangeDir(CurrentFID *domain.FID, path string, RootFID *domain.FID) (*domain.FID, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	CurrentInode, err := fs.GetInode(CurrentFID)
	if err != nil {
		return CurrentFID, fmt.Errorf("directory not found, %s", CurrentFID.String())
	}

	if CurrentInode.Type != domain.InodeTypeDirectory {
		return CurrentFID, fmt.Errorf("not a directory, %s", CurrentFID.String())
	}

	if path == "/" {
		return RootFID, nil
	}

	if path == ".." {
		return CurrentInode.Parent.FID, nil
	}

	parts := strings.Split(path, "/")
	log.Println("Parts:", parts)
	for _, part := range parts {

		childInode, found := fs.GetChildInodeByName(CurrentInode, part)

		if found != nil {
			return CurrentFID, fmt.Errorf("incorrect path: %s", path)
		}

		CurrentInode = childInode
		CurrentFID = childInode.FID
	}

	return CurrentFID, nil
}

// ListDirectory lists contents of a directory
func (fs *FileServer) ListDirectory(dirFID *domain.FID) ([]*domain.Inode, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	dirInode, err := fs.GetInode(dirFID)
	if err != nil {
		return nil, fmt.Errorf("directory not found, %s", dirFID.String())
	}

	if dirInode.Type != domain.InodeTypeDirectory {
		return nil, fmt.Errorf("not a directory, %s", dirFID.String())
	}

	children := make([]*domain.Inode, 0, len(dirInode.Children))
	for _, childFID := range dirInode.Children {
		if childInode, err := fs.GetInode(childFID); err == nil {
			children = append(children, childInode)
		}
	}

	return children, nil
}

// CreateFile creates a new file or directory
func (fs *FileServer) CreateFile(parentFID *domain.FID, name, username string, fileType domain.InodeType) (*domain.FID, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	log.Printf("Creating %s with name %s under parent FID %s", fileType.String(), name, parentFID.String())
	// Get parent inode
	parent, err := fs.GetInode(parentFID)
	if err != nil {
		return nil, fmt.Errorf("parent directory not found, %s", parentFID.String())
	}

	// Check if parent is directory
	if parent.Type != domain.InodeTypeDirectory {
		return nil, fmt.Errorf("parent is not a directory, %s", parentFID.String())
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
		Parent: parent,
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

// ReadFile reads data from a file
func (fs *FileServer) ReadFile(parentFID *domain.FID, name string, offset, length uint64) ([]byte, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	// Get parent inode
	parentInode, err := fs.GetInode(parentFID)
	if err != nil {
		return nil, fmt.Errorf("parent directory not found, %s", parentFID.String())
	}
	
	inode, err := fs.GetChildInodeByName(parentInode, name)
	if err != nil {
		return nil, fmt.Errorf("file not found, %s", name)
	}
	// check if file
	if inode.Type != domain.InodeTypeFile {
		return nil, fmt.Errorf("not a file, %s", name)
	}

	// read the whole file for now
	fmt.Println(inode.OSPath)
	data, err := os.ReadFile(inode.OSPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}
	return data, nil
}

// WriteFile writes given data to a file
func (fs *FileServer) WriteFile(parentFID *domain.FID, name string, offset uint64, data []byte) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	// Get parent inode
	parentInode, err := fs.GetInode(parentFID)
	if err != nil {
		return fmt.Errorf("parent directory not found, %s", parentFID.String())
	}

	inode, err := fs.GetChildInodeByName(parentInode, name)
	if err != nil {
		return fmt.Errorf("file not found, %s", name)
	}
	// check if file
	if inode.Type != domain.InodeTypeFile {
		return fmt.Errorf("not a file, %s", name)
	}

	// write data to file
	if err := os.WriteFile(inode.OSPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	return nil
}

// DeleteFile deletes a file or directory
func (fs *FileServer) DeleteFile(fid *domain.FID) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	inode, err := fs.GetInode(fid)
	if err != nil {
		return fmt.Errorf("file not found, %s", fid.String())
	}

	// For directories, ensure they're empty
	if inode.Type == domain.InodeTypeDirectory && len(inode.Children) > 0 {
		return fmt.Errorf("directory not empty, %s", fid.String())
	}

	// Delete from filesystem
	if err := os.RemoveAll(inode.OSPath); err != nil {
		return fmt.Errorf("failed to delete from filesystem: %w", err)
	}

	// Remove from storage
	delete(fs.inodes, fid.String())

	return nil
}