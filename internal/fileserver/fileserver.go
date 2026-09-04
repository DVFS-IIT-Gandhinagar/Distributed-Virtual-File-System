package fileserver

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/domain"
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
	caCertPath  string
	trashMeta   map[string]trashEntry // trashed inode FID string -> metadata (best-effort, in-memory)
	msAddr      string
	Shared      map[string][]string       // directory path -> users map (e.g., "umang/proj" -> ["romit"])
	sessions    map[string]*clientSession // username -> last known session metadata
	startTime   time.Time
	quotas      map[string]uint64 // username -> quota in bytes
	inodeStore  *InodeStore
}

type trashEntry struct {
	originalParentFID string
	originalName      string
	originalRelPath   string
	sharedSnapshots   []sharedDirSnapshot
}

type sharedDirSnapshot struct {
	path  string
	users []string
}

const trashDirName = ".trash"
const storageQuota uint64 = defaultStorageQuota // backwards-compatibility alias for tests
const trashNavigationDeniedMsg = "access denied: use show_trash to view trash contents"

// NewFileServer creates a new file server object, either blank or loading from existing data
func NewFileServer(serverID, rootDir string, useTLS bool, msAddr string, caCertPath string) (*FileServer, error) {
	fs := &FileServer{
		serverID:    serverID,
		rootDir:     rootDir,
		inodes:      make(map[string]*domain.Inode),
		users:       make(map[string]*domain.FID),
		nextInodeID: 0,
		useTLS:      useTLS,
		caCertPath:  caCertPath,
		trashMeta:   make(map[string]trashEntry),
		msAddr:      msAddr,
		Shared:      make(map[string][]string),
		sessions:    make(map[string]*clientSession),
		startTime:   time.Now(),
		quotas:      make(map[string]uint64),
	}

	// Load custom quota configuration if present
	_ = fs.loadQuotas()

	// Ensure root directory exists
	if err := os.MkdirAll(rootDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create root dir: %w", err)
	}

	// Initialize persistent inode store before scanning or creating inodes
	inodeStore, err := NewInodeStore(rootDir)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize inode store: %w", err)
	}
	fs.inodeStore = inodeStore
	fs.nextInodeID = inodeStore.NextInodeID()

	// Scan and load existing data using FileScanner
	fileScanner := &FileScanner{rootDir: rootDir, serverID: serverID, fs: fs}
	if err := fileScanner.loadExistingData(&fs.nextInodeID, &fs.inodes, &fs.users); err != nil {
		return nil, fmt.Errorf("failed to load existing data: %w", err)
	}

	log.Printf("Loaded existing data from root directory, current inode count: %d", len(fs.inodes))

	// Load explicit directory shares from disk
	if err := fs.LoadDirShares(); err != nil {
		log.Printf("Warning: failed to load dirShares: %v", err)
		// Continue anyway - start with empty dirShares
		fs.Shared = make(map[string][]string)
	}

	return fs, nil
}

// GetUserRoot returns the root FID for a user, creating it if necessary
func (fs *FileServer) GetUserRoot(root_path, root_user string) (*domain.FID, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	// Get or create the root user's directory
	var rootFID *domain.FID
	if existingRootFID := fs.users[root_user]; existingRootFID != nil {
		rootFID = existingRootFID
	} else {
		// Create user directory if it doesn't exist
		userDir := filepath.Join(fs.rootDir, root_user)
		if err := os.MkdirAll(userDir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create user dir: %w", err)
		}

		var inodeID uint64
		if fs.inodeStore != nil {
			inodeID = fs.inodeStore.GetOrAssign(root_user)
			fs.nextInodeID = fs.inodeStore.NextInodeID()
			_ = fs.inodeStore.Save()
		} else {
			inodeID = fs.nextInodeID
			atomic.AddUint64(&fs.nextInodeID, 1)
		}

		// Create root FID for user
		rootFID = &domain.FID{
			FileServerID:     fs.serverID,
			InodeID:          inodeID,
			GenerationNumber: 1,
		}

		// Load ACL from disk if it exists, otherwise use default
		ACL, err := fs.LoadACL(root_user, root_user)
		if err != nil {
			return nil, fmt.Errorf("failed to load ACL for user %s: %w", root_user, err)
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
	}

	// Ensure per-user trash exists (always use root_user for trash)
	if _, err := fs.getOrCreateTrashDirLocked(root_user); err != nil {
		return nil, err
	}

	// If root_path is same as root_user, return root FID
	if root_path == root_user {
		return rootFID, nil
	}

	// Parse the path - it should be in format "root_user/subdir/..." or "/root_user/subdir/..."
	// Remove leading slash if present
	cleanPath := strings.TrimPrefix(root_path, "/")

	// Split the path
	pathComponents := strings.Split(cleanPath, "/")
	log.Printf("GetUserRoot: root_path=%s, root_user=%s, cleanPath=%s, pathComponents=%v", root_path, root_user, cleanPath, pathComponents)

	// Find where to start traversal
	startIdx := 0
	if len(pathComponents) > 0 && pathComponents[0] == root_user {
		// Path starts with root_user, skip it
		startIdx = 1
	}
	log.Printf("GetUserRoot: startIdx=%d, len(pathComponents)=%d", startIdx, len(pathComponents))

	// If no components to traverse, return root
	if startIdx >= len(pathComponents) {
		return rootFID, nil
	}

	// Traverse from root to the requested path
	currentFID := rootFID
	for i := startIdx; i < len(pathComponents); i++ {
		component := pathComponents[i]
		log.Printf("GetUserRoot: traversing component[%d]=%s", i, component)

		if component == "" || component == "." {
			log.Printf("GetUserRoot: skipping empty/dot component")
			continue
		}

		currentInode, err := fs.GetInode(currentFID)
		if err != nil {
			return nil, fmt.Errorf("failed to get inode during traversal: %w", err)
		}

		if currentInode.Type != domain.InodeTypeDirectory {
			return nil, fmt.Errorf("path component is not a directory: %s", component)
		}

		// Find child with matching name
		childInode, err := fs.GetChildInodeByName(currentInode, component)
		if err != nil {
			return nil, fmt.Errorf("path component not found: %s (looking in %s)", component, currentInode.Name)
		}

		currentFID = childInode.FID
	}

	return currentFID, nil
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

	trashRelPath := filepath.Join(root_user, trashDirName)
	var trashInodeID uint64
	if fs.inodeStore != nil {
		trashInodeID = fs.inodeStore.GetOrAssign(trashRelPath)
		fs.nextInodeID = fs.inodeStore.NextInodeID()
		_ = fs.inodeStore.Save()
	} else {
		trashInodeID = fs.nextInodeID
		atomic.AddUint64(&fs.nextInodeID, 1)
	}

	trashFID := &domain.FID{
		FileServerID:     fs.serverID,
		InodeID:          trashInodeID,
		GenerationNumber: 1,
	}

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

func pathContainsTrashSegment(path string) bool {
	parts := strings.Split(path, "/")
	for _, part := range parts {
		if part == trashDirName {
			return true
		}
	}
	return false
}

func (fs *FileServer) isUnderRootTrashLocked(inode *domain.Inode, rootFID *domain.FID) bool {
	if inode == nil || rootFID == nil {
		return false
	}

	rootInode, ok := fs.inodes[rootFID.String()]
	if !ok {
		return false
	}

	trashInode, err := fs.GetChildInodeByName(rootInode, trashDirName)
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

func userCanAccessInode(inode *domain.Inode, username string) bool {
	if inode == nil || username == "" {
		return false
	}
	if inode.ACL.Owner == username {
		return true
	}
	for _, sharedUser := range inode.ACL.Shared {
		if sharedUser == username {
			return true
		}
	}
	return false
}

// ShowTrash returns all entries from the caller's trash directory.
func (fs *FileServer) ShowTrash(root_user, requester string) ([]*domain.Inode, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	if requester == "" {
		requester = root_user
	}

	trashInode, err := fs.getOrCreateTrashDirLocked(root_user)
	if err != nil {
		return nil, err
	}

	children := make([]*domain.Inode, 0, len(trashInode.Children))
	for _, childFID := range trashInode.Children {
		if childInode, err := fs.GetInode(childFID); err == nil {
			if userCanAccessInode(childInode, requester) {
				children = append(children, childInode)
			}
		}
	}

	sort.Slice(children, func(i, j int) bool {
		return children[i].Name < children[j].Name
	})

	return children, nil
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

func (fs *FileServer) collectSharedSnapshotsForPathLocked(basePath string) []sharedDirSnapshot {
	snapshots := make([]sharedDirSnapshot, 0)
	if basePath == "" || fs.Shared == nil {
		return snapshots
	}

	prefix := basePath + string(os.PathSeparator)
	for sharedPath, users := range fs.Shared {
		if sharedPath != basePath && !strings.HasPrefix(sharedPath, prefix) {
			continue
		}
		usersCopy := make([]string, len(users))
		copy(usersCopy, users)
		snapshots = append(snapshots, sharedDirSnapshot{path: sharedPath, users: usersCopy})
	}

	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].path < snapshots[j].path
	})

	return snapshots
}

func (fs *FileServer) detachSharedSnapshotsLocked(owner string, snapshots []sharedDirSnapshot) {
	if len(snapshots) == 0 {
		return
	}

	for _, snap := range snapshots {
		delete(fs.Shared, snap.path)
		for _, user := range snap.users {
			if err := fs.RootUnshare(owner, filepath.Base(snap.path), snap.path, user); err != nil {
				log.Printf("Warning: failed to notify metaserver unshare for path=%s user=%s: %v", snap.path, user, err)
			}
		}
	}

	if err := fs.SaveDirShares(); err != nil {
		log.Printf("Warning: failed to persist dirShares after detaching shared snapshots: %v", err)
	}
}

func (fs *FileServer) reattachSharedSnapshotsLocked(owner, oldBasePath, newBasePath string, snapshots []sharedDirSnapshot) {
	if len(snapshots) == 0 {
		return
	}

	if fs.Shared == nil {
		fs.Shared = make(map[string][]string)
	}

	oldPrefix := oldBasePath + string(os.PathSeparator)
	for _, snap := range snapshots {
		mappedPath := snap.path
		if snap.path == oldBasePath {
			mappedPath = newBasePath
		} else if strings.HasPrefix(snap.path, oldPrefix) {
			suffix := strings.TrimPrefix(snap.path, oldPrefix)
			mappedPath = filepath.Join(newBasePath, suffix)
		}

		if fs.Shared[mappedPath] == nil {
			fs.Shared[mappedPath] = []string{}
		}

		for _, user := range snap.users {
			alreadyShared := false
			for _, existing := range fs.Shared[mappedPath] {
				if existing == user {
					alreadyShared = true
					break
				}
			}
			if !alreadyShared {
				fs.Shared[mappedPath] = append(fs.Shared[mappedPath], user)
			}

			if err := fs.RootShare(owner, filepath.Base(mappedPath), mappedPath, user); err != nil {
				log.Printf("Warning: failed to notify metaserver share for path=%s user=%s: %v", mappedPath, user, err)
			}
		}
	}

	if err := fs.SaveDirShares(); err != nil {
		log.Printf("Warning: failed to persist dirShares after reattaching shared snapshots: %v", err)
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
	originalRelPath, relErr := filepath.Rel(fs.rootDir, inode.OSPath)
	if relErr != nil {
		originalRelPath = ""
	}

	sharedSnapshots := []sharedDirSnapshot{}
	if originalRelPath != "" {
		sharedSnapshots = fs.collectSharedSnapshotsForPathLocked(originalRelPath)
	}

	// OS move first; on failure, keep memory untouched.
	if err := os.Rename(inode.OSPath, newPath); err != nil {
		return "", fmt.Errorf("failed to move to trash: %w", err)
	}
	if len(sharedSnapshots) > 0 {
		fs.detachSharedSnapshotsLocked(root_user, sharedSnapshots)
	}

	// Memory updates.
	origParent := ""
	if inode.Parent != nil {
		origParent = inode.Parent.FID.String()
	}
	fs.trashMeta[fid.String()] = trashEntry{
		originalParentFID: origParent,
		originalName:      inode.Name,
		originalRelPath:   originalRelPath,
		sharedSnapshots:   sharedSnapshots,
	}

	if err := fs.removeFromParent(inode); err != nil {
		log.Printf("Warning: failed to unlink from parent during trash: %v", err)
	}

	inode.Parent = trashInode
	inode.Name = finalName
	trashInode.Children = append(trashInode.Children, inode.FID)
	fs.updateSubtreePathsLocked(inode, newPath)

	if fs.inodeStore != nil && originalRelPath != "" {
		if newRelPath, err := filepath.Rel(fs.rootDir, newPath); err == nil {
			fs.inodeStore.RenamePrefix(originalRelPath, newRelPath)
			_ = fs.inodeStore.Save()
		}
	}

	return finalName, nil
}

// RestoreFile moves an inode out of trash back to its original parent (best-effort).
func (fs *FileServer) RestoreFile(fid *domain.FID, root_user, requester string) (string, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	if requester == "" {
		requester = root_user
	}

	if fid == nil {
		return "", fmt.Errorf("invalid FID: cannot be nil")
	}

	inode, err := fs.GetInode(fid)
	if err != nil {
		return "", fmt.Errorf("file not found: %s", fid.String())
	}
	if !userCanAccessInode(inode, requester) {
		return "", fmt.Errorf("permission denied: user '%s' cannot restore '%s'", requester, inode.Name)
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

	oldTrashRelPath, _ := filepath.Rel(fs.rootDir, inode.OSPath)

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

	if fs.inodeStore != nil && oldTrashRelPath != "" {
		if newRelPath, err := filepath.Rel(fs.rootDir, newPath); err == nil {
			fs.inodeStore.RenamePrefix(oldTrashRelPath, newRelPath)
			_ = fs.inodeStore.Save()
		}
	}

	if len(meta.sharedSnapshots) > 0 && meta.originalRelPath != "" {
		newRelPath, relErr := filepath.Rel(fs.rootDir, inode.OSPath)
		if relErr != nil {
			newRelPath = meta.originalRelPath
		}
		fs.reattachSharedSnapshotsLocked(root_user, meta.originalRelPath, newRelPath, meta.sharedSnapshots)
	}

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

// checkStorageQuotaWithAdditional checks if the user's storage plus additionalBytes exceeds their quota.
func (fs *FileServer) checkStorageQuotaWithAdditional(username string, additionalBytes uint64) error {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	rootFID, exists := fs.users[username]
	if !exists {
		return fmt.Errorf("user not found: %s", username)
	}
	rootInode, exists := fs.inodes[rootFID.String()]
	if !exists {
		return fmt.Errorf("internal error: root inode not found for user %s", username)
	}
	quota := fs.getUserQuotaLocked(username)
	if rootInode.Size+additionalBytes > quota {
		freeSpace := uint64(0)
		if quota > rootInode.Size {
			freeSpace = quota - rootInode.Size
		}
		return fmt.Errorf("storage quota exceeded: user '%s' has %d bytes remaining, but requested %d bytes (quota: %d bytes)",
			username, freeSpace, additionalBytes, quota)
	}

	return nil
}

// checkStorageQuota checks if the user has exceeded their storage quota.
func (fs *FileServer) checkStorageQuota(username string) error {
	return fs.checkStorageQuotaWithAdditional(username, 0)
}

// find the inode in parent's children (should be existing)
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

// collectSubtreeInodes performs DFS traversal to collect all inodes in a subtree
// Returns a list containing the root inode and all its descendants
func (fs *FileServer) collectSubtreeInodes(rootInode *domain.Inode) []*domain.Inode {
	if rootInode == nil {
		return []*domain.Inode{}
	}

	result := []*domain.Inode{rootInode} // Include root itself

	// If not a directory, return just the root
	if rootInode.Type != domain.InodeTypeDirectory {
		return result
	}

	// DFS traversal of children
	for _, childFID := range rootInode.Children {
		childInode, exists := fs.inodes[childFID.String()]
		if !exists {
			log.Printf("Warning: child inode %s not found during subtree collection", childFID.String())
			continue
		}

		// Recursively collect child's subtree
		childSubtree := fs.collectSubtreeInodes(childInode)
		result = append(result, childSubtree...)
	}

	return result
}

// Share another user the root dir only if current user is owner
// This method now recursively updates ACLs for all inodes in the subtree
func (fs *FileServer) Share(username string, share_with string, dirFID *domain.FID) error {

	dirInode, err := fs.GetInode(dirFID)
	if err != nil {
		return err
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()

	// Only directories can be shared
	if dirInode.Type != domain.InodeTypeDirectory {
		return fmt.Errorf("only directories can be shared")
	}

	// Sharing is allowed only if current user is the owner
	if dirInode.ACL.Owner != username {
		return fmt.Errorf("Only owner can share")
	}

	if share_with == dirInode.ACL.Owner {
		return fmt.Errorf("Cannot share with self")
	}

	// Check if already shared at directory level (idempotent)
	for _, u := range dirInode.ACL.Shared {
		if u == share_with {
			return fmt.Errorf("The given already has the access to this directory")
		}
	}

	// Collect all inodes in the subtree using DFS traversal
	subtreeInodes := fs.collectSubtreeInodes(dirInode)
	log.Printf("Share: collected %d inodes in subtree for directory '%s'", len(subtreeInodes), dirInode.Name)

	// Log all collected inodes for debugging
	log.Printf("Share: Collected inodes:")
	for i, inode := range subtreeInodes {
		log.Printf("  [%d] Name=%s, Type=%s, OSPath=%s", i, inode.Name, inode.Type, inode.OSPath)
	}

	// Update ACL for each inode in the subtree
	for _, inode := range subtreeInodes {
		// Check if share_with is already in the ACL.Shared list (idempotent)
		alreadyShared := false
		for _, u := range inode.ACL.Shared {
			if u == share_with {
				alreadyShared = true
				break
			}
		}

		// If not already shared, append share_with to ACL.Shared
		if !alreadyShared {
			inode.ACL.Shared = append(inode.ACL.Shared, share_with)

			// Persist ACL to disk for this inode
			// Get the relative path from root for ACL storage
			inodePath, err := filepath.Rel(fs.rootDir, inode.OSPath)
			if err != nil {
				log.Printf("Warning: failed to compute relative path for inode '%s': %v", inode.Name, err)
				continue
			}

			if err := fs.SaveACL(inodePath, inode.ACL); err != nil {
				log.Printf("Warning: failed to persist ACL for inode '%s': %v", inode.Name, err)
				// Continue anyway - in-memory ACL is updated
			}
		}
	}

	// Track explicit directory share in fs.Shared map (dirShares)
	// This is CRITICAL: only track the explicit share, not inherited ones
	// Use path as key (stable across restarts), not FID
	path, err := filepath.Rel(fs.rootDir, dirInode.OSPath)
	if err != nil {
		log.Printf("Warning: failed to compute relative path for dirShares tracking: %v", err)
		path = dirInode.Name // Fallback to just the name
	}

	if fs.Shared == nil {
		fs.Shared = make(map[string][]string)
	}
	if fs.Shared[path] == nil {
		fs.Shared[path] = []string{}
	}

	// Check if already in explicit shares (idempotent)
	alreadyInShared := false
	for _, u := range fs.Shared[path] {
		if u == share_with {
			alreadyInShared = true
			break
		}
	}
	if !alreadyInShared {
		fs.Shared[path] = append(fs.Shared[path], share_with)
		log.Printf("Share: Added explicit share tracking: path=%s, user=%s", path, share_with)
	}

	// Persist dirShares to disk
	if saveErr := fs.SaveDirShares(); saveErr != nil {
		log.Printf("Warning: failed to persist dirShares: %v", saveErr)
		// Continue anyway - in-memory state is updated
	}

	if err != nil {
		log.Printf("Warning: failed to compute relative path for inode '%s': %v", dirInode.Name, err)
	}

	// Notify metaserver once after all ACLs are updated
	fs.RootShare(username, dirInode.Name, path, share_with)

	log.Printf("Share: successfully shared directory '%s' with user '%s' (updated %d inodes)",
		dirInode.Name, share_with, len(subtreeInodes))

	return nil
}

// Unshare removes a user from the shared list of the root dir
// This method now recursively updates ACLs for all inodes in the subtree
func (fs *FileServer) Unshare(username string, unshare_with string, dirFID *domain.FID) error {

	dirInode, err := fs.GetInode(dirFID)
	if err != nil {
		return err
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()

	// Sharing is allowed only if current user is the owner
	if dirInode.ACL.Owner != username {
		return fmt.Errorf("Only owner can unshare")
	}

	if unshare_with == dirInode.ACL.Owner {
		return fmt.Errorf("Cannot unshare with self")
	}

	// Check if user is present in root directory's shared ACL
	found := false
	for _, u := range dirInode.ACL.Shared {
		if u == unshare_with {
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("user not in shared list")
	}

	// Collect all inodes in the subtree using DFS traversal
	subtreeInodes := fs.collectSubtreeInodes(dirInode)
	log.Printf("Unshare: collected %d inodes in subtree for directory '%s'", len(subtreeInodes), dirInode.Name)

	// Update ACL for each inode in the subtree
	for _, inode := range subtreeInodes {
		// Remove unshare_with from ACL.Shared list
		newShared := []string{}
		userFound := false

		for _, u := range inode.ACL.Shared {
			if u == unshare_with {
				userFound = true
				continue
			}
			newShared = append(newShared, u)
		}

		// Only update and persist if the user was found in this inode's ACL
		if userFound {
			inode.ACL.Shared = newShared

			// Persist ACL to disk for this inode
			// Get the relative path from root for ACL storage
			inodePath, err := filepath.Rel(fs.rootDir, inode.OSPath)
			if err != nil {
				log.Printf("Warning: failed to compute relative path for inode '%s': %v", inode.Name, err)
				continue
			}

			if err := fs.SaveACL(inodePath, inode.ACL); err != nil {
				log.Printf("Warning: failed to persist ACL for inode '%s': %v", inode.Name, err)
				// Continue anyway - in-memory ACL is updated
			}
		}
	}

	// Remove explicit directory share from fs.Shared map (dirShares)
	// Use path as key (stable across restarts), not FID
	dirPath, pathErr := filepath.Rel(fs.rootDir, dirInode.OSPath)
	if pathErr != nil {
		log.Printf("Warning: failed to compute relative path for dirShares tracking: %v", pathErr)
		dirPath = dirInode.Name // Fallback
	}

	if fs.Shared != nil && fs.Shared[dirPath] != nil {
		newSharedList := []string{}
		for _, u := range fs.Shared[dirPath] {
			if u != unshare_with {
				newSharedList = append(newSharedList, u)
			}
		}
		fs.Shared[dirPath] = newSharedList
		log.Printf("Unshare: Removed explicit share tracking: path=%s, user=%s", dirPath, unshare_with)

		// If no more explicit shares, remove the entry
		if len(fs.Shared[dirPath]) == 0 {
			delete(fs.Shared, dirPath)
		}
	}

	// Persist dirShares to disk
	if saveErr := fs.SaveDirShares(); saveErr != nil {
		log.Printf("Warning: failed to persist dirShares: %v", saveErr)
		// Continue anyway - in-memory state is updated
	}

	path, err := filepath.Rel(fs.rootDir, dirInode.OSPath)
	if err != nil {
		log.Printf("Warning: failed to compute relative path for inode '%s': %v", dirInode.Name, err)
	}

	// Notify metaserver once after all ACLs are updated
	fs.RootUnshare(username, dirInode.Name, path, unshare_with)

	log.Printf("Unshare: successfully unshared directory '%s' with user '%s' (updated %d inodes)",
		dirInode.Name, unshare_with, len(subtreeInodes))

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

	if pathContainsTrashSegment(path) {
		return CurrentFID, fmt.Errorf("%s", trashNavigationDeniedMsg)
	}

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
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			if CurrentInode.Parent != nil {
				CurrentInode = CurrentInode.Parent
				CurrentFID = CurrentInode.FID
			}
			continue
		}

		childInode, found := fs.GetChildInodeByName(CurrentInode, part)

		if found != nil {
			return CurrentFID, fmt.Errorf("incorrect path: %s", path)
		}

		CurrentInode = childInode
		CurrentFID = childInode.FID
	}

	if fs.isUnderRootTrashLocked(CurrentInode, RootFID) {
		return CurrentFID, fmt.Errorf("%s", trashNavigationDeniedMsg)
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
	// check if already exists in parent
	parentInode, err := fs.GetInode(parentFID)
	if err != nil {
		return nil, fmt.Errorf("parent directory not found, %s", parentFID.String())
	}
	if parentInode.Type != domain.InodeTypeDirectory {
		return nil, fmt.Errorf("parent is not a directory, %s", parentFID.String())
	}
	// if same name exists, return existing one's fid instead of error to make it idempotent
	existingInode, err := fs.GetChildInodeByName(parentInode, name)
	if err == nil {
		return existingInode.FID, nil
	}

	// Disallow creating inside trash.
	if fs.isUnderTrashLocked(parentInode, root_user) {
		return nil, fmt.Errorf("cannot create files/directories inside trash")
	}

	// Create OS path
	osPath := filepath.Join(parentInode.OSPath, name)
	path, err := filepath.Rel(fs.rootDir, osPath)
	if err != nil {
		return nil, fmt.Errorf("internal error can't compute path: %w", err)
	}

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

	var inodeID uint64
	if fs.inodeStore != nil {
		inodeID = fs.inodeStore.GetOrAssign(path)
	} else {
		inodeID = fs.nextInodeID
		atomic.AddUint64(&fs.nextInodeID, 1)
	}

	// Generate new FID
	newFID := &domain.FID{
		FileServerID:     fs.serverID,
		InodeID:          inodeID,
		GenerationNumber: 1,
	}

	// Create a deep copy of parent's ACL (not a reference)
	// This ensures child ACL modifications don't affect parent
	ACL := domain.ACL{
		Owner:  parentInode.ACL.Owner,
		Shared: make([]string, len(parentInode.ACL.Shared)),
	}
	copy(ACL.Shared, parentInode.ACL.Shared)

	// Create inode
	newInode := &domain.Inode{
		FID:    newFID,
		Type:   fileType,
		Name:   name,
		OSPath: osPath,
		ACL:    ACL,
		Parent: parentInode,
		Size:   0,
	}

	if fileType == domain.InodeTypeDirectory {
		newInode.Children = make([]*domain.FID, 0)
		fs.SaveACL(path, ACL)
	}

	// Store inode
	fs.inodes[newFID.String()] = newInode

	// Add to parent's children
	parentInode.Children = append(parentInode.Children, newFID)

	if fs.inodeStore != nil {
		fs.nextInodeID = fs.inodeStore.NextInodeID()
		_ = fs.inodeStore.Save()
	}

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

// GetFileHash computes and returns the hash of a file's contents.
func (fs *FileServer) GetFileHash(parentFID *domain.FID, name string) (string, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	parentInode, err := fs.GetInode(parentFID)
	if err != nil {
		return "", fmt.Errorf("parent directory not found, %s", parentFID.String())
	}

	inode, err := fs.GetChildInodeByName(parentInode, name)
	if err != nil {
		return "", fmt.Errorf("file not found, %s", name)
	}
	if inode.Type != domain.InodeTypeFile {
		return "", fmt.Errorf("not a file, %s", name)
	}

	f, err := os.Open(inode.OSPath)
	if err != nil {
		return "", fmt.Errorf("failed to open file for hashing: %w", err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("failed to hash file contents: %w", err)
	}

	return hex.EncodeToString(h.Sum(nil)), nil
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

	sizeDiff := int64(newSize) - int64(inode.Size)
	if sizeDiff > 0 {
		// Pre-flight quota check: find user's root inode and verify write will not exceed quota
		root := inode
		for root.Parent != nil && root.Parent != root {
			root = root.Parent
		}
		quota := fs.getUserQuotaLocked(root.Name)
		if root.Size+uint64(sizeDiff) > quota {
			freeSpace := uint64(0)
			if quota > root.Size {
				freeSpace = quota - root.Size
			}
			return fmt.Errorf("storage quota exceeded: write of %d bytes exceeds available free space (%d bytes remaining of %d quota)",
				len(data), freeSpace, quota)
		}
	}

	_, err = f.WriteAt(data, int64(offset))
	if err != nil {
		return fmt.Errorf("write failed: %w", err)
	}

	if sizeDiff != 0 {
		node := inode
		for {
			node.Size += uint64(sizeDiff)
			if node.Parent != nil && node.Parent != node {
				node = node.Parent
			} else {
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
	// Deduct deleted inode's size from parent directories up to root
	if inode.Size > 0 {
		deletedSize := inode.Size
		p := inode.Parent
		for p != nil && p != inode {
			if p.Size >= deletedSize {
				p.Size -= deletedSize
			} else {
				p.Size = 0
			}
			if p.Parent == nil || p.Parent == p {
				break
			}
			p = p.Parent
		}
	}

	// Remove all deleted inodes from the map and InodeStore
	for _, deletedInode := range toDelete {
		delete(fs.inodes, deletedInode.FID.String())
		delete(fs.trashMeta, deletedInode.FID.String())
		if fs.inodeStore != nil {
			if relPath, err := filepath.Rel(fs.rootDir, deletedInode.OSPath); err == nil {
				fs.inodeStore.Remove(relPath)
			}
		}
	}
	if fs.inodeStore != nil {
		_ = fs.inodeStore.Save()
	}

	// Collect shared snapshots and remove them from fs.Shared *under the lock*,
	// then notify the metaserver *after* releasing it (network calls must not
	// be made while holding the mutex).
	targetRelPath := ""
	if relPath, relErr := filepath.Rel(fs.rootDir, inode.OSPath); relErr == nil {
		targetRelPath = relPath
	}
	var sharedSnapshots []sharedDirSnapshot
	if targetRelPath != "" {
		sharedSnapshots = fs.collectSharedSnapshotsForPathLocked(targetRelPath)
		for _, snap := range sharedSnapshots {
			delete(fs.Shared, snap.path)
		}
		if len(sharedSnapshots) > 0 {
			if err := fs.SaveDirShares(); err != nil {
				log.Printf("Warning: failed to persist dirShares after delete: %v", err)
			}
		}
	}

	// Remove main inode from parent's children list
	if err := fs.removeFromParent(inode); err != nil {
		log.Printf("Warning: failed to remove from parent: %v", err)
	}

	log.Printf("Successfully deleted %s: %s (FID: %s) with %d total items",
		inode.Type.String(), inode.Name, fid.String(), len(toDelete))

	// Release the lock before making outbound network calls to the metaserver.
	fs.mu.Unlock()

	// Notify metaserver to remove shared root entries for the deleted directory.
	for _, snap := range sharedSnapshots {
		for _, user := range snap.users {
			if err := fs.RootUnshare(root_user, filepath.Base(snap.path), snap.path, user); err != nil {
				log.Printf("Warning: failed to notify metaserver unshare for path=%s user=%s: %v", snap.path, user, err)
			}
		}
	}

	// Re-acquire so the deferred Unlock() doesn't double-unlock.
	fs.mu.Lock()
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
