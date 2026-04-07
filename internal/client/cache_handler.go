package client

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/umangshikarvar/dvfs/internal/domain"
)

func generateUniqueCacheID() string {
	return uuid.New().String()
}

// CNode represents a cached file or directory in the client, and are made to mirror the structure of the remote file system as it gets accessed. They are stored in a tree structure, with the root node representing the root directory of the remote file system. Each CNode has a name, type (file or directory), a map of children nodes (for directories), and a reference to its parent node. The client uses CNodes to cache metadata about files and directories, allowing for faster access and reduced network calls when navigating the file system or performing operations on files and directories.
type CNode struct {
	Name          string
	Type          domain.InodeType  // 0 for file, 1 for directory
	fid           *domain.FID       // FID of the file/directory represented by this node
	Size          uint64            // size of the file (for files)
	children      map[string]*CNode // child names -> child nodes (for directories)
	contentCached bool              // indicates if file content is cached (for files)
	contentUID    string            // unique identifier for cached content (for files)
	parent        *CNode
}

type CacheHandler struct {
	root   *CNode
	curr   *CNode
	client *Client
}

const CacheDir = "./.cache"

func NewCacheHandler(c *Client, rootFID *domain.FID) *CacheHandler {
	// Initialize root node representing the root directory of the remote file system
	root := &CNode{
		Name:     "mydrive",
		fid:      rootFID,
		Type:     domain.InodeTypeDirectory,
		children: make(map[string]*CNode),
		parent:   nil, // root's parent is nil,
	}

	// get user root and files/dir in the root from server and populate the cache
	files, err := c.ListFiles()
	if err != nil {
		log.Printf("Error fetching root directory contents: %v", err)
		return nil
	}
	for _, file := range files {
		root.children[file.Name] = &CNode{
			Name:     file.Name,
			Type:     file.Type,
			fid:      file.FID,
			Size:     file.Size,
			children: make(map[string]*CNode),
			parent:   root,
		}
	}

	return &CacheHandler{
		root:   root,
		curr:   root,
		client: c,
	}
}

func (c *CacheHandler) VisualizeCache(indent string) {
	node := c.curr // will always be the current directory node
	fmt.Println("Cache Structure:")
	fmt.Printf("%s- %s (%s)\n", indent, node.Name, func() string {
		if node.Type == domain.InodeTypeDirectory {
			return "directory"
		}
		return fmt.Sprintf("file (cached: %v)", node.contentCached)
	}())
	for _, child := range node.children {
		c.visualizeCacheHelper(child, indent+"  ")
	}
	fmt.Println()
}

func (c *CacheHandler) visualizeCacheHelper(node *CNode, indent string) {
	fmt.Printf("%s- %s (%s)\n", indent, node.Name, func() string {
		if node.Type == domain.InodeTypeDirectory {
			return "directory"
		}
		return fmt.Sprintf("file (cached: %v)", node.contentCached)
	}())
	for _, child := range node.children {
		c.visualizeCacheHelper(child, indent+"  ")
	}
}

func (c *CacheHandler) GetFileInfo() (*FileInfo, error) {
	return c.client.GetFileInfo()
}

// check for cache hit, if not then download file from server and update cache
func (c *CacheHandler) ReadFile(s string) ([]byte, error) {
	// check if file exists in cache of current directory
	fileNode, exists := c.curr.children[s]
	if exists && fileNode.Type == domain.InodeTypeFile && fileNode.contentCached {
		// cache hit, read file from cached file
		data, err := os.ReadFile(CacheDir + "/" + fileNode.contentUID)
		if err != nil {
			return nil, fmt.Errorf("error reading cached file: %v", err)
		}
		return data, nil
	} else if !exists || fileNode.Type != domain.InodeTypeFile {
		// file not found in cache
		return nil, fmt.Errorf("file '%s' not found in current directory", s)
	}

	// cache miss, read file from server and update cache
	fileNode.contentUID = generateUniqueCacheID()                                        // generate a UUID for the cached file
	err := c.client.downloadFileInternalAs(c.curr.fid, s, CacheDir, fileNode.contentUID) // download file content to a local cache file
	if err != nil {
		return nil, err
	}
	// update cache node to indicate content is cached and store unique identifier for cached content
	fileNode.contentCached = true
	// read file content from local cache file and return
	data, err := os.ReadFile(CacheDir + "/" + fileNode.contentUID)
	if err != nil {
		return nil, fmt.Errorf("error reading cached file: %v", err)
	}
	return data, nil
}

func (c *CacheHandler) CreateDirectory(s string) (*FileInfo, error) {
	info, err := c.client.CreateDirectory(s)
	if err != nil {
		return nil, err
	}
	// update cache to reflect new directory creation
	c.curr.children[s] = &CNode{
		Name:     s,
		Type:     domain.InodeTypeDirectory,
		fid:      info.FID,
		Size:     info.Size,
		children: make(map[string]*CNode),
		parent:   c.curr,
	}
	return info, nil
}

func (c *CacheHandler) CreateFile(s string) (*FileInfo, error) {
	info, err := c.client.CreateFile(s)
	if err != nil {
		return nil, err
	}
	// update cache to reflect new file creation
	c.curr.children[s] = &CNode{
		Name:   s,
		Type:   domain.InodeTypeFile,
		fid:    info.FID,
		Size:   info.Size,
		parent: c.curr,
	}
	return info, nil
}

func (c *CacheHandler) Share(s string) error {
	return c.client.Share(s)
}

func (c *CacheHandler) Unshare(s string) error {
	return c.client.Unshare(s)
}

func (c *CacheHandler) Download(s string) error {
	return c.client.Download(s)
}

func (c *CacheHandler) Upload(s string) error {
	// create a new file node in the cache for the uploaded file
	// extract file name from path
	fileName := filepath.Base(s)
	// exctract file size from local file info
	fi, err := os.Stat(s)
	if err != nil {
		return fmt.Errorf("error getting local file info: %v", err)
	}
	fileSize := uint64(fi.Size())

	c.curr.children[fileName] = &CNode{
		Name:   fileName,
		Type:   domain.InodeTypeFile,
		fid:    nil, // FID will be updated after successful upload when we get the file info from server
		Size:   fileSize,
		parent: c.curr,
	}
	fid, err := c.client.Upload(s)
	if err != nil {
		return err
	}
	// after successful upload, set the FID and size of the file node in cache to reflect the new file on server
	c.curr.children[fileName].fid = fid
	// update parents sizes up the cache tree to reflect the new file size
	sizeDiff := int64(fileSize)
	node := c.curr
	for node != nil {
		node.Size += uint64(sizeDiff)
		node = node.parent
	}
	return nil
}

func (c *CacheHandler) ChangeDirectory(s string) error {
	switch s {
	case "/":
		c.curr = c.root
		c.client.ChangeCurrentFID(c.curr.fid)
		return c.populateCurrentDirCache()
	case "..":
		// Check if we're at the root (either own root or shared directory root)
		if c.curr == c.root {
			// User is at root, return special error to trigger metaserver screen
			return fmt.Errorf("RETURN_TO_METASERVER")
		}

		c.curr = c.curr.parent
		if c.curr == nil { // if parent is nil, we're at root, so stay at root
			c.curr = c.root
		}
		c.client.ChangeCurrentFID(c.curr.fid)
		return c.populateCurrentDirCache()
	default:
		// check if directory exists in cache of current directory
		dirNode, exists := c.curr.children[s]
		if exists && dirNode.Type == domain.InodeTypeDirectory {
			c.curr = dirNode
			c.client.ChangeCurrentFID(c.curr.fid) // change FID in client to reflect new current directory
			_ = c.populateCurrentDirCache()       // refresh cache each time you cd into dir
			return nil
		} else {
			// directory not found in cache
			return fmt.Errorf("directory '%s' not found in current directory", s)
		}
	}
}

func (c *CacheHandler) ListFiles() ([]*FileInfo, error) {
	// read from cache of current directory
	files := make([]*FileInfo, 0)
	for _, child := range c.curr.children {
		files = append(files, &FileInfo{
			Name: child.Name,
			Type: child.Type,
			Size: child.Size,
			FID:  child.fid,
		})
	}
	return files, nil
}

func (c *CacheHandler) Path() (string, error) {
	// If we're at root, return the display name (e.g., "proj" for shared dirs)
	if c.curr == c.root {
		if c.client.display_name != "" {
			return c.client.display_name, nil
		}
		return c.root.Name, nil
	}

	// construct path by traversing up the cache tree from current node to root
	path := ""
	node := c.curr
	for node != c.root {
		path = node.Name + "/" + path
		node = node.parent
	}

	// Use display_name as the root prefix if available
	rootPrefix := c.root.Name
	if c.client.display_name != "" {
		rootPrefix = c.client.display_name
	}

	return rootPrefix + "/" + path, nil
}

// get files/dir in the current dir from server and populate the cache
func (c *CacheHandler) populateCurrentDirCache() error {
	files, err := c.client.ListFilesAt(c.curr.fid)
	if err != nil {
		log.Printf("Error fetching %s directory contents: %v", c.curr.Name, err)
		return err
	}

	// Replace children map from authoritative server listing.
	// Preserve already-known subtrees when the name matches.
	oldChildren := c.curr.children
	newChildren := make(map[string]*CNode, len(files))
	for _, file := range files {
		if existing, ok := oldChildren[file.Name]; ok {
			existing.Name = file.Name
			existing.Type = file.Type
			existing.fid = file.FID
			existing.Size = file.Size
			existing.parent = c.curr
			if existing.children == nil {
				existing.children = make(map[string]*CNode)
			}
			newChildren[file.Name] = existing
			continue
		}

		newChildren[file.Name] = &CNode{
			Name:     file.Name,
			Type:     file.Type,
			fid:      file.FID,
			Size:     file.Size,
			children: make(map[string]*CNode),
			parent:   c.curr,
		}
	}
	c.curr.children = newChildren
	return nil
}

// delete file/dir from server and update cache accordingly. If recursive is true, delete all contents of the directory as well
func (c *CacheHandler) DeleteFile(s string, recursive bool) error {
	err := c.client.DeleteFile(s, recursive) // delete file/dir from server
	if err != nil {
		return err
	}
	// Update cache to reflect deletion.
	// Cache can be stale; guard against missing entry to avoid panics.
	if node, ok := c.curr.children[s]; ok {
		if recursive && node.Type == domain.InodeTypeDirectory {
			recursiveDelete(node)
		}
		delete(c.curr.children, s) // delete file/dir node from cache of current directory
	}
	return nil
}

// TrashFile moves a file/dir to server-side trash and updates the current cache view.
func (c *CacheHandler) TrashFile(name string, recursive bool) (string, error) {
	trashedName, err := c.client.TrashFile(name, recursive)
	if err != nil {
		return "", err
	}
	// Remove from current directory cache (it no longer lives here).
	if _, ok := c.curr.children[name]; ok {
		delete(c.curr.children, name)
	}
	return trashedName, nil
}

// RestoreFile restores an item from server-side trash and updates cache if currently viewing trash.
func (c *CacheHandler) RestoreFile(name string) (string, error) {
	restoredName, err := c.client.RestoreFile(name)
	if err != nil {
		return "", err
	}
	// If we're currently in trash, remove it from this cache view.
	if c.curr != nil && c.curr.Name == ".trash" {
		delete(c.curr.children, name)
	}
	// Refresh root so the restored entry is immediately visible via `ls`/`cd`.
	_ = c.populateNodeCache(c.root)
	return restoredName, nil
}

func (c *CacheHandler) populateNodeCache(node *CNode) error {
	if node == nil {
		return nil
	}
	files, err := c.client.ListFilesAt(node.fid)
	if err != nil {
		return err
	}
	oldChildren := node.children
	newChildren := make(map[string]*CNode, len(files))
	for _, file := range files {
		if existing, ok := oldChildren[file.Name]; ok {
			existing.Name = file.Name
			existing.Type = file.Type
			existing.fid = file.FID
			existing.Size = file.Size
			existing.parent = node
			if existing.children == nil {
				existing.children = make(map[string]*CNode)
			}
			newChildren[file.Name] = existing
			continue
		}
		newChildren[file.Name] = &CNode{
			Name:     file.Name,
			Type:     file.Type,
			fid:      file.FID,
			Size:     file.Size,
			children: make(map[string]*CNode),
			parent:   node,
		}
	}
	node.children = newChildren
	return nil
}

func recursiveDelete(node *CNode) {
	for _, child := range node.children {
		delete(node.children, child.Name)
		if child.Type == domain.InodeTypeFile && child.contentCached {
			// if it's a file and content is cached, remove the cached file from local cache directory
			err := os.Remove(CacheDir + "/" + child.contentUID)
			if err != nil {
				log.Printf("Error removing cached file %s: %v", child.contentUID, err)
			}
		} else if child.Type == domain.InodeTypeDirectory {
			recursiveDelete(child)
		}
	}
}

func (c *CacheHandler) ClearCache() {
	// Check if cache directory exists before attempting to read it
	if _, err := os.Stat(CacheDir); os.IsNotExist(err) {
		return // silently return if cache directory doesn't exist
	}

	// Existing logic continues unchanged
	files, err := os.ReadDir(CacheDir)
	if err != nil {
		log.Printf("Error reading cache directory: %v", err)
		return
	}
	for _, file := range files {
		err := os.Remove(CacheDir + "/" + file.Name())
		if err != nil {
			log.Printf("Error removing cached file %s: %v", file.Name(), err)
		}
	}
}
