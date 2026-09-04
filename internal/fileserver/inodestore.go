package fileserver

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const inodeIndexFilename = ".dvfs_inodes_index.json"

// InodeIndexData represents the on-disk format of the persistent inode mapping.
type InodeIndexData struct {
	NextInodeID uint64            `json:"next_inode_id"`
	PathToID    map[string]uint64 `json:"path_to_id"`
}

// InodeStore provides thread-safe persistent mapping between filesystem relative paths
// and deterministic Inode IDs.
type InodeStore struct {
	rootDir   string
	indexPath string
	data      InodeIndexData
	mu        sync.Mutex
}

// normalizePath converts OS-specific path separators to forward slashes
// and cleans up relative prefixes.
func normalizePath(p string) string {
	clean := filepath.ToSlash(filepath.Clean(p))
	clean = strings.TrimPrefix(clean, "./")
	clean = strings.TrimPrefix(clean, "/")
	return clean
}

// NewInodeStore creates or loads an InodeStore for rootDir.
func NewInodeStore(rootDir string) (*InodeStore, error) {
	store := &InodeStore{
		rootDir:   rootDir,
		indexPath: filepath.Join(rootDir, inodeIndexFilename),
		data: InodeIndexData{
			NextInodeID: 0,
			PathToID:    make(map[string]uint64),
		},
	}

	if err := store.load(); err != nil {
		return nil, err
	}

	return store, nil
}

// load reads the index file from disk if it exists.
func (s *InodeStore) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.indexPath)
	if err != nil {
		if os.IsNotExist(err) {
			// No existing index, start fresh
			return nil
		}
		return fmt.Errorf("failed to read inode index %s: %w", s.indexPath, err)
	}

	if len(strings.TrimSpace(string(data))) == 0 {
		return nil
	}

	var indexData InodeIndexData
	if err := json.Unmarshal(data, &indexData); err != nil {
		log.Printf("Warning: failed to parse inode index %s (%v), starting fresh", s.indexPath, err)
		return nil
	}

	if indexData.PathToID == nil {
		indexData.PathToID = make(map[string]uint64)
	}

	s.data = indexData
	log.Printf("[INODESTORE] Loaded persistent inode index from %s with %d entries (nextInodeID: %d)",
		s.indexPath, len(s.data.PathToID), s.data.NextInodeID)
	return nil
}

// GetOrAssign retrieves an existing InodeID for the relative path,
// or allocates and returns a new InodeID.
func (s *InodeStore) GetOrAssign(relPath string) uint64 {
	norm := normalizePath(relPath)

	s.mu.Lock()
	defer s.mu.Unlock()

	if id, exists := s.data.PathToID[norm]; exists {
		return id
	}

	id := s.data.NextInodeID
	s.data.NextInodeID++
	s.data.PathToID[norm] = id
	return id
}

// Get retrieves an existing InodeID without allocating a new one.
func (s *InodeStore) Get(relPath string) (uint64, bool) {
	norm := normalizePath(relPath)

	s.mu.Lock()
	defer s.mu.Unlock()

	id, exists := s.data.PathToID[norm]
	return id, exists
}

// Remove removes the relative path from the inode store (used upon permanent deletion).
// It does not decrement NextInodeID, ensuring old IDs are never recycled.
func (s *InodeStore) Remove(relPath string) {
	norm := normalizePath(relPath)

	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.data.PathToID, norm)
}

// RenamePrefix renames all path keys matching oldPrefix to newPrefix.
// Used when moving or restoring subtrees (e.g. into or out of .trash).
func (s *InodeStore) RenamePrefix(oldPrefix, newPrefix string) {
	normOld := normalizePath(oldPrefix)
	normNew := normalizePath(newPrefix)

	if normOld == "" || normNew == "" || normOld == normNew {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	oldPrefixSlash := normOld + "/"

	for path, id := range s.data.PathToID {
		if path == normOld {
			delete(s.data.PathToID, path)
			s.data.PathToID[normNew] = id
		} else if strings.HasPrefix(path, oldPrefixSlash) {
			suffix := strings.TrimPrefix(path, oldPrefixSlash)
			delete(s.data.PathToID, path)
			s.data.PathToID[normNew+"/"+suffix] = id
		}
	}
}

// NextInodeID returns the current nextInodeID value.
func (s *InodeStore) NextInodeID() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.NextInodeID
}

// Save writes the current inode index to disk atomically.
func (s *InodeStore) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal inode index: %w", err)
	}

	tmpPath := s.indexPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write tmp inode index: %w", err)
	}

	// Windows requires removing destination before rename if it already exists
	_ = os.Remove(s.indexPath)

	if err := os.Rename(tmpPath, s.indexPath); err != nil {
		return fmt.Errorf("failed to atomic-rename inode index: %w", err)
	}

	return nil
}
