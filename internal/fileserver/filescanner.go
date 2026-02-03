package fileserver

import (
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"github.com/umangshikarvar/dvfs/internal/domain"
)

type FileScanner struct{
	rootDir string
	serverID string
}

// loadExistingData scans the root directory and creates inodes for existing files and directories
func (scanner *FileScanner) loadExistingData(nextInodeID *uint64, inodes *map[string]*domain.Inode, users *map[string]*domain.FID) error {
	// Scan user directories (first level under rootDir)
	entries, err := os.ReadDir(scanner.rootDir)
	if err != nil {
		return fmt.Errorf("failed to read root directory: %w", err)
	}

	// user volumes are at first level
	for _, entry := range entries {
		if entry.IsDir() {
			username := entry.Name()
			userDir := filepath.Join(scanner.rootDir, username)

			// Create root inode for this user (always inode ID 0)
			userRootFID := &domain.FID{
				FileServerID:     scanner.serverID,
				InodeID:          *nextInodeID,
				GenerationNumber: 1,
			}
			atomic.AddUint64(nextInodeID, 1)
			(*users)[username] = userRootFID
			
			// Create root inode
			userRootInode := &domain.Inode{
				FID:      userRootFID,
				Type:     domain.InodeTypeDirectory,
				Name:     username,
				OSPath:   userDir,
				Owner:    username,
				Children: make([]*domain.FID, 0),
			}

			(*inodes)[userRootFID.String()] = userRootInode

			// Scan user's files and directories (first level only)
			if err := scanner.scanUserDirectory(userDir, userRootInode, nextInodeID, inodes); err != nil {
				return fmt.Errorf("failed to scan user directory %s: %w", username, err)
			}
		}
	}

	return nil
}

// scanUserDirectory scans a user's directory and creates inodes for first-level files and directories
func (scanner *FileScanner) scanUserDirectory(userDir string, parentInode *domain.Inode, nextInodeID *uint64, inodes *map[string]*domain.Inode) error {

	type bfsItem struct {
		dirPath string
		inode   *domain.Inode
	}

	queue := []*bfsItem{}
	queue = append(queue, &bfsItem{dirPath: userDir, inode: parentInode})

	for len(queue) > 0 {
		userDir = queue[0].dirPath
		parentInode = queue[0].inode
		queue = queue[1:]
		entries, err := os.ReadDir(userDir)
		if err != nil {
			return fmt.Errorf("failed to read user directory: %w", err)
		}
		for _, entry := range entries {
			// Generate new FID for this item
			newFID := &domain.FID{
				FileServerID:     scanner.serverID,
				InodeID:          *nextInodeID,
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
				Parent: parentInode,
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
			(*inodes)[newFID.String()] = newInode

			// Add to parent's children
			parentInode.Children = append(parentInode.Children, newFID)

			// Increment inode ID counter
			atomic.AddUint64(nextInodeID, 1)

			// If dir then add to queue
			if inodeType == domain.InodeTypeDirectory {
				queue = append(queue, &bfsItem{dirPath: itemPath, inode: newInode})
			}
		}
	}

	return nil
}
