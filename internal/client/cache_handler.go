package client

import (
	"fmt"
	"log"

	"github.com/umangshikarvar/dvfs/internal/domain"
)

// CNode represents a cached file or directory in the client, and are made to mirror the structure of the remote file system as it gets accessed. They are stored in a tree structure, with the root node representing the root directory of the remote file system. Each CNode has a name, type (file or directory), a map of children nodes (for directories), and a reference to its parent node. The client uses CNodes to cache metadata about files and directories, allowing for faster access and reduced network calls when navigating the file system or performing operations on files and directories.
type CNode struct {
	Name     string
	Type     domain.InodeType // 0 for file, 1 for directory
	children map[string]*CNode
	parent   *CNode
}

type CacheHandler struct {
	root *CNode
}

func NewCacheHandler(c *Client) *CacheHandler {
	// Initialize root node representing the root directory of the remote file system
	root := &CNode{
		Name:     "mydrive",
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
			children: make(map[string]*CNode),
			parent:   root,
		}
	}

	return &CacheHandler{
		root: root,
	}
}

func (c *CacheHandler) VisualizeCache(indent string) {
	node := c.root
	fmt.Println("Cache Structure:")
	fmt.Printf("%s- %s (%s)\n", indent, node.Name, func() string {
		if node.Type == domain.InodeTypeDirectory {
			return "directory"
		}
		return "file"
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
		return "file"
	}())
	for _, child := range node.children {
		c.visualizeCacheHelper(child, indent+"  ")
	}
}

func (c *CacheHandler) GetFileInfo() (*FileInfo, error) {
	panic("unimplemented")
}

func (c *CacheHandler) WriteFile(filename string, b []byte) error {
	panic("unimplemented")
}

func (c *CacheHandler) ReadFile(s string) ([]byte, error) {
	panic("unimplemented")
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
