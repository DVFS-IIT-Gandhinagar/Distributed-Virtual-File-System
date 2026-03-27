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
	useTLS      bool
	trashMeta   map[string]trashEntry // trashed inode FID string -> metadata (best-effort, in-memory)
	msAddr		string
}

type trashEntry struct {
	originalParentFID string
	originalName      string
}

const trashDirName = ".trash"
const storageQuota uint64 = 1 * 1024 // 1 MB per user, for demonstration

// NewFileServer creates a new file server object, either blank or loading from existing data
func NewFileServer(serverID, rootDir string, useTLS bool, msAddr string) (*FileServer, error) {
	fs := &FileServer{
		serverID:    serverID,
		rootDir:     rootDir,
		inodes:      make(map[string]*domain.Inode),
		users:       make(map[string]*domain.FID),
		nextInodeID: 0,
		useTLS:      useTLS,
		trashMeta:   make(map[string]trashEntry),
		msAddr: 	msAddr,
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
func (fs *FileServer) GetUserRoot(root_user string) (*domain.FID, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	
	// If user already exists, ensure trash directory exists and return.
	if rootFID := fs.users[root_user]; rootFID != nil {
		if _, err := fs.getOrCreateTrashDirLocked(root_user); err != nil {
			return nil, err
		}
		return rootFID, nil
	}

	// Create user directory if it doesn't exist
	userDir := filepath.Join(fs.rootDir, root_user)
	if err := os.MkdirAll(userDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create user dir: %w", err)
	}
	
	// Create root FID for user
	rootFID := &domain.FID{
		FileServerID:     fs.serverID,
		InodeID:          fs.nextInodeID,
		GenerationNumber: 1,
	}
	
	ACL := domain.ACL{
		Owner:  root_user,
		Shared: []string{},
	}
	
	// Create root inode
	rootInode := &domain.Inode{
		FID:      rootFID,
		Type:     domain.InodeTypeDirectory,
		Name:     root_user,
		OSPath:   userDir,
		ACL:      ACL,
		Children: make([]*domain.FID, 0),
	}
	
	rootInode.Parent = rootInode
	
	fs.inodes[rootFID.String()] = rootInode
	fs.users[root_user] = rootFID

	atomic.AddUint64(&fs.nextInodeID, 1)

	// Ensure per-user trash exists.
	if _, err := fs.getOrCreateTrashDirLocked(root_user); err != nil {
		return nil, err
	}
	
	return rootFID, nil
}

func (fs *FileServer) getOrCreateTrashDirLocked(root_user string) (*domain.Inode, error) {
	rootFID := fs.users[root_user]
	if rootFID == nil {
		return nil, fmt.Errorf("user not registered")
	}
	rootInode, ok := fs.inodes[rootFID.String()]
	if !ok {
		return nil, fmt.Errorf("internal error: root inode not found")
	}
	
	if child, err := fs.GetChildInodeByName(rootInode, trashDirName); err == nil {
		if child.Type != domain.InodeTypeDirectory {
			return nil, fmt.Errorf("internal error: %s exists but is not a directory", trashDirName)
		}
		return child, nil
	}
	
	trashPath := filepath.Join(rootInode.OSPath, trashDirName)
	if err := os.MkdirAll(trashPath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create trash directory: %w", err)
	}

	trashFID := &domain.FID{
		FileServerID:     fs.serverID,
		InodeID:          fs.nextInodeID,
		GenerationNumber: 1,
	}
	atomic.AddUint64(&fs.nextInodeID, 1)
	
	ACL := domain.ACL{
		Owner:  root_user,
		Shared: []string{},
	}
	
	trashInode := &domain.Inode{
		FID:      trashFID,
		Type:     domain.InodeTypeDirectory,
		Name:     trashDirName,
		OSPath:   trashPath,
		ACL:      ACL,
		Children: make([]*domain.FID, 0),
		Parent:   rootInode,
	}
	
	fs.inodes[trashFID.String()] = trashInode
	rootInode.Children = append(rootInode.Children, trashFID)
	return trashInode, nil
}

func (fs *FileServer) isTrashDirForUser(inode *domain.Inode, root_user string) bool {
	if inode == nil {
		return false
	}
	if inode.Name != trashDirName {
		return false
	}
	rootFID := fs.users[root_user]
	if rootFID == nil {
		return false
	}
	rootInode, ok := fs.inodes[rootFID.String()]
	if !ok {
		return false
	}
	return inode.Parent != nil && inode.Parent.FID.String() == rootInode.FID.String()
}

func (fs *FileServer) nameExistsInDirLocked(dir *domain.Inode, name string) bool {
	if dir == nil {
		return false
	}
	for _, childFID := range dir.Children {
		childInode, ok := fs.inodes[childFID.String()]
		if ok && childInode.Name == name {
			return true
		}
	}
	return false
}

func (fs *FileServer) uniqueNameInDirLocked(dir *domain.Inode, desired string, inodeID uint64) string {
	if !fs.nameExistsInDirLocked(dir, desired) {
		return desired
	}
	// Deterministic, collision-resistant name.
	return fmt.Sprintf("%s__%d", desired, inodeID)
}

func (fs *FileServer) isUnderTrashLocked(inode *domain.Inode, root_user string) bool {
	trashInode, err := fs.getOrCreateTrashDirLocked(root_user)
	if err != nil {
		return false
	}
	
	for n := inode; n != nil; n = n.Parent {
		if n.FID != nil && n.FID.String() == trashInode.FID.String() {
			return true
		}
		if n.Parent == nil || (n.Parent.FID != nil && n.FID != nil && n.Parent.FID.String() == n.FID.String()) {
			break
		}
	}
	return false
}

func (fs *FileServer) updateSubtreePathsLocked(inode *domain.Inode, newOSPath string) {
	inode.OSPath = newOSPath
	if inode.Type != domain.InodeTypeDirectory {
		return
	}
	for _, childFID := range inode.Children {
		childInode, ok := fs.inodes[childFID.String()]
		if !ok {
			continue
		}
		childNewPath := filepath.Join(newOSPath, childInode.Name)
		fs.updateSubtreePathsLocked(childInode, childNewPath)
	}
}

// TrashFile moves a file or directory into the user's trash directory (soft delete).
// NOTE: This stores restore metadata in-memory only.
func (fs *FileServer) TrashFile(fid *domain.FID, root_user string, recursive bool) (string, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	
	if fid == nil {
		return "", fmt.Errorf("invalid FID: cannot be nil")
	}
	
	inode, err := fs.GetInode(fid)
	if err != nil {
		return "", fmt.Errorf("file not found: %s", fid.String())
	}
	
	// Prevent trashing root and the trash directory itself.
	if rootFID, exists := fs.users[root_user]; exists && rootFID.String() == fid.String() {
		return "", fmt.Errorf("cannot trash user root directory")
	}
	if fs.isTrashDirForUser(inode, root_user) {
		return "", fmt.Errorf("cannot trash the trash directory")
	}
	
	if err := fs.validateDeletePermissions(inode, root_user, recursive); err != nil {
		return "", err
	}
	
	trashInode, err := fs.getOrCreateTrashDirLocked(root_user)
	if err != nil {
		return "", err
	}
	if inode.Parent != nil && inode.Parent.FID.String() == trashInode.FID.String() {
		return "", fmt.Errorf("inode is already in trash")
	}
	
	finalName := fs.uniqueNameInDirLocked(trashInode, inode.Name, inode.FID.InodeID)
	newPath := filepath.Join(trashInode.OSPath, finalName)
	
	// OS move first; on failure, keep memory untouched.
	if err := os.Rename(inode.OSPath, newPath); err != nil {
		return "", fmt.Errorf("failed to move to trash: %w", err)
	}

	// Memory updates.
	origParent := ""
	if inode.Parent != nil {
		origParent = inode.Parent.FID.String()
	}
	fs.trashMeta[fid.String()] = trashEntry{originalParentFID: origParent, originalName: inode.Name}
	
	if err := fs.removeFromParent(inode); err != nil {
		log.Printf("Warning: failed to unlink from parent during trash: %v", err)
	}
	
	inode.Parent = trashInode
	inode.Name = finalName
	trashInode.Children = append(trashInode.Children, inode.FID)
	fs.updateSubtreePathsLocked(inode, newPath)
	
	return finalName, nil
}

// RestoreFile moves an inode out of trash back to its original parent (best-effort).
func (fs *FileServer) RestoreFile(fid *domain.FID, root_user string) (string, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	
	if fid == nil {
		return "", fmt.Errorf("invalid FID: cannot be nil")
	}

	inode, err := fs.GetInode(fid)
	if err != nil {
		return "", fmt.Errorf("file not found: %s", fid.String())
	}
	if inode.ACL.Owner != root_user {
		return "", fmt.Errorf("permission denied: user '%s' does not own '%s'", root_user, inode.Name)
	}
	
	trashInode, err := fs.getOrCreateTrashDirLocked(root_user)
	if err != nil {
		return "", err
	}
	if inode.Parent == nil || inode.Parent.FID.String() != trashInode.FID.String() {
		return "", fmt.Errorf("inode is not in trash")
	}
	
	meta, ok := fs.trashMeta[fid.String()]
	if !ok {
		return "", fmt.Errorf("restore metadata not available (try restoring before restarting the server)")
	}

	// Determine original parent; if missing, restore to user root.
	var targetParent *domain.Inode
	if meta.originalParentFID != "" {
		if parentInode, ok := fs.inodes[meta.originalParentFID]; ok {
			targetParent = parentInode
		}
	}
	if targetParent == nil {
		rootFID := fs.users[root_user]
		if rootFID == nil {
			return "", fmt.Errorf("internal error: user root not found")
		}
		rootInode, ok := fs.inodes[rootFID.String()]
		if !ok {
			return "", fmt.Errorf("internal error: user root inode not found")
		}
		targetParent = rootInode
	}
	if targetParent.Type != domain.InodeTypeDirectory {
		return "", fmt.Errorf("internal error: restore target is not a directory")
	}
	
	finalName := fs.uniqueNameInDirLocked(targetParent, meta.originalName, inode.FID.InodeID)
	newPath := filepath.Join(targetParent.OSPath, finalName)
	
	if err := os.Rename(inode.OSPath, newPath); err != nil {
		return "", fmt.Errorf("failed to restore from trash: %w", err)
	}
	
	if err := fs.removeFromParent(inode); err != nil {
		log.Printf("Warning: failed to unlink from trash during restore: %v", err)
	}
	
	inode.Parent = targetParent
	inode.Name = finalName
	targetParent.Children = append(targetParent.Children, inode.FID)
	fs.updateSubtreePathsLocked(inode, newPath)
	
	delete(fs.trashMeta, fid.String())
	return finalName, nil
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

// checkStorageQuota checks if the user has exceeded their storage quota and returns an error if so. This is a best-effort check that is called before allowing uploads, but it does not roll back uploads that have already happened.
func (fs *FileServer) checkStorageQuota(username string) error {
	rootFID, exists := fs.users[username]
	if !exists {
		return fmt.Errorf("user not found: %s", username)
	}
	rootInode, exists := fs.inodes[rootFID.String()] 
	if !exists {
		return fmt.Errorf("internal error: root inode not found for user %s", username)
	}
	if rootInode.Size > storageQuota {
		return fmt.Errorf("storage quota exceeded: user '%s' has used %d bytes, exceeding the quota of %d bytes", username, rootInode.Size, storageQuota)
	}

	return nil
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

// Share another user the root dir only if current user is owner
func (fs *FileServer) Share(username string, root_user string, share_with string) error {
	rootInodeFID, err := fs.GetUserRoot(root_user)
	if err != nil {
		return err
	}

	rootInode, err := fs.GetInode(rootInodeFID)
	if err != nil {
		return err
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()

	// Sharing is allowed only if current user is the owner
	if rootInode.ACL.Owner != username {
		return fmt.Errorf("Only owner can share")
	}

	if share_with == root_user {
		return fmt.Errorf("Cannot share with self")
	}

	// if not already share append share_with in shared ACL
	for _, u := range rootInode.ACL.Shared {
		if u == share_with {
			return nil
		}
	}

	rootInode.ACL.Shared = append(rootInode.ACL.Shared, share_with)
	fs.RootShare(username, share_with)
	return nil
}

// Unshare removes a user from the shared list of the root dir
func (fs *FileServer) Unshare(username string, root_user string, unshare_with string) error {

	rootInodeFID, err := fs.GetUserRoot(root_user)
	if err != nil {
		return err
	}

	rootInode, err := fs.GetInode(rootInodeFID)
	if err != nil {
		return err
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()

	// Sharing is allowed only if current user is the owner
	if rootInode.ACL.Owner != username {
		return fmt.Errorf("Only owner can unshare")
	}

	if unshare_with == root_user {
		return fmt.Errorf("Cannot unshare with self")
	}

	// remove unshare_with from shared list
	newShared := []string{}

	// if user is present in shared ACL, remove it
	found := false
	for _, u := range rootInode.ACL.Shared {
		if u == unshare_with {
			found = true
			continue
		}
		newShared = append(newShared, u)
	}

	if !found {
		return fmt.Errorf("user not in shared list")
	}

	rootInode.ACL.Shared = newShared
	fs.RootUnshare(username, unshare_with)
	return nil
}

// Return path as pwd
func (fs *FileServer) Path(dirFID *domain.FID) (string, error) {

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
func (fs *FileServer) CreateFile(parentFID *domain.FID, name, root_user string, fileType domain.InodeType) (*domain.FID, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	log.Printf("Creating %s with name %s under parent FID %s", fileType.String(), name, parentFID.String())
	if name == trashDirName {
		return nil, fmt.Errorf("'%s' is a reserved name", trashDirName)
	}
	// Get parent inode
	parent, err := fs.GetInode(parentFID)
	if err != nil {
		return nil, fmt.Errorf("parent directory not found, %s", parentFID.String())
	}

	// Check if parent is directory
	if parent.Type != domain.InodeTypeDirectory {
		return nil, fmt.Errorf("parent is not a directory, %s", parentFID.String())
	}

	// Disallow creating inside trash.
	if fs.isUnderTrashLocked(parent, root_user) {
		return nil, fmt.Errorf("cannot create files/directories inside trash")
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

	ACL := domain.ACL{
		Owner:  root_user,
		Shared: []string{},
	}

	// Create inode
	newInode := &domain.Inode{
		FID:    newFID,
		Type:   fileType,
		Name:   name,
		OSPath: osPath,
		ACL:    ACL,
		Parent: parent,
		Size:   0,
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
	f, err := os.OpenFile(inode.OSPath, os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open failed: %w", err)
	}
	defer f.Close()

	// track size changes to update inode size after write
	var newSize uint64
	if offset+uint64(len(data)) > inode.Size {
		newSize = offset + uint64(len(data))
	} else {
		newSize = inode.Size
	}

	_, err = f.WriteAt(data, int64(offset))
	if err != nil {
		return fmt.Errorf("write failed: %w", err)
	}

	sizeDiff := int64(newSize) - int64(inode.Size)
	if sizeDiff != 0 {
		node := inode
		for {
			node.Size += uint64(sizeDiff)
			if node.Parent != nil {
				node = node.Parent
			} else {
				// Reached root
				if node.Size > storageQuota {
					// throw error and prevent any more writes that would exceed quota. Note: this is a best-effort check and does not roll back the write that just happened, but prevents future writes.
					return fmt.Errorf("storage quota exceeded: cannot write data")
				}
				break
			}
		}
	}

	return nil
}

// DeleteFile deletes a file or directory with comprehensive error handling
// Parameters:
//   - fid: File identifier to delete
//   - root_user: User attempting the deletion (for permission checking)
//   - recursive: If true, allows deletion of non-empty directories
//
// Implementation notes:
// - Uses two-phase approach: validate first, then delete
// - Deletes from OS filesystem first, then updates in-memory structures
// - For directories, uses post-order DFS traversal (children before parents)
// - Maintains atomicity: if OS deletion fails, memory state unchanged
func (fs *FileServer) DeleteFile(fid *domain.FID, root_user string, recursive bool) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	log.Printf("DeleteFile called: FID=%s, user=%s, recursive=%v", fid.String(), root_user, recursive)

	// Phase 1: Validation
	if fid == nil {
		return fmt.Errorf("invalid FID: cannot be nil")
	}

	inode, err := fs.GetInode(fid)
	if err != nil {
		return fmt.Errorf("file not found: %s", fid.String())
	}

	log.Printf("DeleteFile: target inode: name=%s, type=%s, children=%d",
		inode.Name, inode.Type.String(), len(inode.Children))

	// Prevent deletion of root directory
	if rootFID, exists := fs.users[root_user]; exists && rootFID.String() == fid.String() {
		return fmt.Errorf("cannot delete user root directory")
	}
	if fs.isTrashDirForUser(inode, root_user) {
		return fmt.Errorf("cannot delete the trash directory")
	}

	// Validate ownership/permissions for entire subtree
	if err := fs.validateDeletePermissions(inode, root_user, recursive); err != nil {
		return err
	}

	// Phase 2: Collect all inodes to delete (post-order traversal)
	var toDelete []*domain.Inode
	if inode.Type == domain.InodeTypeDirectory && len(inode.Children) > 0 {
		if err := fs.collectInodesToDelete(inode, &toDelete); err != nil {
			return err
		}
	}
	// Add the target inode itself (deleted last in post-order)
	toDelete = append(toDelete, inode)

	// Phase 3: Delete from OS filesystem (children first, then parent)
	// If any deletion fails, we stop and return error before modifying memory
	for _, inodeToDelete := range toDelete {
		if err := os.RemoveAll(inodeToDelete.OSPath); err != nil {
			return fmt.Errorf("failed to delete '%s' from filesystem: %w", inodeToDelete.Name, err)
		}
	}

	// Phase 4: Update in-memory structures (only after successful OS deletions)
	// Remove all deleted inodes from the map
	for _, deletedInode := range toDelete {
		delete(fs.inodes, deletedInode.FID.String())
		delete(fs.trashMeta, deletedInode.FID.String())
	}

	// Remove main inode from parent's children list
	if err := fs.removeFromParent(inode); err != nil {
		log.Printf("Warning: failed to remove from parent: %v", err)
	}

	log.Printf("Successfully deleted %s: %s (FID: %s) with %d total items",
		inode.Type.String(), inode.Name, fid.String(), len(toDelete))
	return nil
}

// validateDeletePermissions validates that user can delete the inode and all its children
func (fs *FileServer) validateDeletePermissions(inode *domain.Inode, root_user string, recursive bool) error {
	// Check ownership of the target
	if inode.ACL.Owner != root_user {
		return fmt.Errorf("permission denied: user '%s' does not own '%s'", root_user, inode.Name)
	}

	// For directories with children
	if inode.Type == domain.InodeTypeDirectory && len(inode.Children) > 0 {
		log.Printf("validateDeletePermissions: directory '%s' has %d children, recursive=%v",
			inode.Name, len(inode.Children), recursive)

		if !recursive {
			return fmt.Errorf("directory '%s' not empty: use recursive deletion (-r flag)", inode.Name)
		}

		// Recursively validate permissions for all children
		for _, childFID := range inode.Children {
			childInode, exists := fs.inodes[childFID.String()]
			if !exists {
				log.Printf("Warning: child inode %s not found during validation", childFID.String())
				continue
			}

			if err := fs.validateDeletePermissions(childInode, root_user, recursive); err != nil {
				return err
			}
		}
	}

	return nil
}

// collectInodesToDelete performs post-order DFS to collect all inodes to delete
// Post-order ensures children are deleted before parents
// Note: Does NOT include the root inode passed in (caller adds it)
func (fs *FileServer) collectInodesToDelete(parentInode *domain.Inode, result *[]*domain.Inode) error {
	// Create a copy to avoid modification during traversal
	childrenCopy := make([]*domain.FID, len(parentInode.Children))
	copy(childrenCopy, parentInode.Children)

	for _, childFID := range childrenCopy {
		childInode, exists := fs.inodes[childFID.String()]
		if !exists {
			log.Printf("Warning: child inode %s not found, skipping", childFID.String())
			continue
		}

		// Recursively collect children first (post-order)
		if childInode.Type == domain.InodeTypeDirectory && len(childInode.Children) > 0 {
			if err := fs.collectInodesToDelete(childInode, result); err != nil {
				return err
			}
		}

		// Add this child to deletion list
		*result = append(*result, childInode)
	}

	return nil
}

// removeFromParent removes an inode from its parent's children list
// Note: This function assumes the parent lock is already held
func (fs *FileServer) removeFromParent(inode *domain.Inode) error {
	if inode == nil || inode.FID == nil {
		return fmt.Errorf("invalid inode")
	}

	// Preferred path: use parent pointer.
	if inode.Parent != nil {
		// Don't remove root from itself
		if inode.Parent.FID.String() == inode.FID.String() {
			return nil
		}

		parent := inode.Parent
		newChildren := make([]*domain.FID, 0, len(parent.Children))
		removed := false
		for _, childFID := range parent.Children {
			if childFID.String() != inode.FID.String() {
				newChildren = append(newChildren, childFID)
			} else {
				removed = true
			}
		}
		parent.Children = newChildren
		if removed {
			return nil
		}
	}

	// Fallback: scan all directories to find a parent that references this inode.
	for _, candidate := range fs.inodes {
		if candidate == nil || candidate.Type != domain.InodeTypeDirectory {
			continue
		}
		if candidate.FID != nil && candidate.FID.String() == inode.FID.String() {
			continue
		}

		newChildren := make([]*domain.FID, 0, len(candidate.Children))
		removed := false
		for _, childFID := range candidate.Children {
			if childFID.String() != inode.FID.String() {
				newChildren = append(newChildren, childFID)
			} else {
				removed = true
			}
		}
		if removed {
			candidate.Children = newChildren
			return nil
		}
	}

	return fmt.Errorf("inode has no parent")
}
