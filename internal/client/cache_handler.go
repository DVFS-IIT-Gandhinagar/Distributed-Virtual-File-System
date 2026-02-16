package client

import (
	"fmt"
	"log"
	"os"

	"github.com/google/uuid"
	"github.com/umangshikarvar/dvfs/internal/domain"
)

func generateUniqueCacheID() string {
	return uuid.New().String()
}

// CNode represents a cached file or directory in the client, and are made to mirror the structure of the remote file system as it gets accessed. They are stored in a tree structure, with the root node representing the root directory of the remote file system. Each CNode has a name, type (file or directory), a map of children nodes (for directories), and a reference to its parent node. The client uses CNodes to cache metadata about files and directories, allowing for faster access and reduced network calls when navigating the file system or performing operations on files and directories.
type CNode struct {
	Name     string
	Type     domain.InodeType // 0 for file, 1 for directory
	fid	  	 *domain.FID       // FID of the file/directory represented by this node
	children map[string]*CNode // child names -> child nodes (for directories)
	contentCached bool // indicates if file content is cached (for files)
	contentUID string // unique identifier for cached content (for files)
	parent   *CNode
}

type CacheHandler struct {
	root *CNode
	curr *CNode
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
		parent:   nil, // root's parent is itself,
	}
	root.parent = root

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
			children: make(map[string]*CNode),
			parent:   root,
		}
	}

	return &CacheHandler{
		root: root,
		curr: root,
		client: c,
	}
}

func (c *CacheHandler) VisualizeCache(indent string) {
	node := c.curr                          // will always be the current directory node
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

// no caching mechanism for file info yet, so directly call client method to get file info from server
func (c *CacheHandler) GetFileInfo() (*FileInfo, error) {
	return c.client.GetFileInfo()
}

// check for cache hit, if not then download file from server and update cache
func (c *CacheHandler) ReadFile(s string) ([]byte, error) {
	// check if file exists in cache of current directory
	if node, exists := c.curr.children[s]; exists && node.Type == domain.InodeTypeFile && node.contentCached {
		// cache hit, read file from cached file
		data, err := os.ReadFile(CacheDir + "/" + node.contentUID)
		if err != nil {
			return nil, fmt.Errorf("error reading cached file: %v", err)
		}
		return data, nil
	}
	// cache miss, read file from server and update cache
	c.curr.children[s].contentUID = generateUniqueCacheID() // generate a UUID for the cached file
	err := c.client.downloadFileInternalAs(c.curr.fid, s, CacheDir, c.curr.children[s].contentUID) // download file content to a local cache file
	if err != nil {
		return nil, err
	}
	// update cache node to indicate content is cached and store unique identifier for cached content
	c.curr.children[s].contentCached = true
	// read file content from local cache file and return
	data, err := os.ReadFile(CacheDir + "/" + c.curr.children[s].contentUID)
	if err != nil {
		return nil, fmt.Errorf("error reading cached file: %v", err)
	}
	return data, nil
}

func (c *CacheHandler) CreateDirectory(s string) (*FileInfo, error) {
	panic("unimplemented")
}

func (c *CacheHandler) CreateFile(s string) (*FileInfo, error) {
	panic("unimplemented")
}

func (c *CacheHandler) Download(s string) error {
	panic("unimplemented")
}

func (c *CacheHandler) Upload(s string) error {
	panic("unimplemented")
}

func (c *CacheHandler) ChangeDirectory(s string) error {
	panic("unimplemented")
}

func (c *CacheHandler) Path() (string, error) {
	panic("unimplemented")
}

func (c *CacheHandler) ListFiles() ([]*FileInfo, error) {
	panic("unimplemented")
}

func (c *CacheHandler) ClearCache() {
	// remove all cached files in the cache directory
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
