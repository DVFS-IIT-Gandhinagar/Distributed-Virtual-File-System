package fileserver

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/umangshikarvar/dvfs/internal/domain"
)

type FileScanner struct {
	rootDir  string
	serverID string
	fs       *FileServer // Reference to FileServer for ACL loading
}

// loadExistingData scans the root directory and creates inodes for existing files and directories
func (scanner *FileScanner) loadExistingData(nextInodeID *uint64, inodes *map[string]*domain.Inode, users *map[string]*domain.FID) error {
	// Scan user directories (first level under rootDir)
	entries, err := os.ReadDir(scanner.rootDir)
	if err != nil {
		return fmt.Errorf("failed to read root directory: %w", err)
	}

	var userWaitGroup sync.WaitGroup

	// user volumes are at first level, process each user directory in parallel using goroutines
	for _, entry := range entries {
		if entry.IsDir() {
			userWaitGroup.Add(1)

			go func() error {
				defer userWaitGroup.Done()

				username := entry.Name()
				userDir := filepath.Join(scanner.rootDir, username)

				// Load ACL from disk (or use default if not found)
				var ACL domain.ACL
				if scanner.fs != nil {
					loadedACL, err := scanner.fs.LoadACL(username, username)
					if err != nil {
						fmt.Printf("Warning: failed to load ACL for user %s: %v, using default ACL\n", username, err)
						ACL = domain.ACL{
							Owner:  username,
							Shared: []string{},
						}
					} else {
						ACL = loadedACL
						fmt.Printf("Loaded ACL for user %s: owner=%s, shared=%v\n", username, ACL.Owner, ACL.Shared)
					}
				} else {
					// Fallback if fs is nil
					ACL = domain.ACL{
						Owner:  username,
						Shared: []string{},
					}
				}

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
					ACL:      ACL,
					Children: make([]*domain.FID, 0),
				}

				(*inodes)[userRootFID.String()] = userRootInode

				// Scan user's files and directories (first level only)
				if err := scanner.scanUserDirectory(username, userDir, userRootInode, nextInodeID, inodes); err != nil {
					return fmt.Errorf("failed to scan user directory %s: %w", username, err)
				}
				userDirSize, err := scanner.calculateDirectorySizes(userRootInode, inodes) // calculate and store sizes of all directories under this user
				if err != nil {
					return fmt.Errorf("failed to calculate directory sizes for user %s: %w", username, err)
				}

				fmt.Printf("Scanned user directory: %s, total size: %d bytes\n", username, userDirSize)
				return nil
			}()
		}
	}
	userWaitGroup.Wait() // wait for all user scanning goroutines to finish before returning

	return nil
}

// scanUserDirectory scans a user's directory and creates inodes for all files and directories using BFS
func (scanner *FileScanner) scanUserDirectory(username string, userDir string, parentInode *domain.Inode, nextInodeID *uint64, inodes *map[string]*domain.Inode) error {

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

			// Calculate relative path from rootDir for ACL loading
			relPath, err := filepath.Rel(scanner.rootDir, itemPath)
			if err != nil {
				return fmt.Errorf("failed to calculate relative path: %w", err)
			}

			// Load ACL from disk (or use default if not found)
			var ACL domain.ACL
			if scanner.fs != nil {
				loadedACL, err := scanner.fs.LoadACL(username, relPath)
				if err != nil {
					// No ACL file found, use default (inherit from parent or create new)
					ACL = domain.ACL{
						Owner:  username,
						Shared: []string{},
					}
				} else {
					ACL = loadedACL
					// Only log when ACL has shared users (reduces log spam)
					if len(ACL.Shared) > 0 {
						fmt.Printf("[%s] Loaded ACL for %s: owner=%s, shared=%v\n", username, relPath, ACL.Owner, ACL.Shared)
					}
				}
			} else {
				// Fallback if fs is nil
				ACL = domain.ACL{
					Owner:  username,
					Shared: []string{},
				}
			}

			// CRITICAL: Check if this child's ACL would incorrectly modify parent
			// This should NEVER happen - child ACLs should not affect parent ACLs
			if len(ACL.Shared) > 0 && parentInode != nil {
				fmt.Printf("[SCANNER DEBUG] Child %s has ACL shared=%v, parent=%s has ACL shared=%v\n",
					relPath, ACL.Shared, parentInode.Name, parentInode.ACL.Shared)
			}
			newInode := &domain.Inode{
				FID:    newFID,
				Type:   inodeType,
				Name:   entry.Name(),
				OSPath: itemPath,
				ACL:    ACL,
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

// calculate total size of each user directory inside rootInode by summing sizes of all files and directories under it
func (scanner *FileScanner) calculateDirectorySizes(rootInode *domain.Inode, inodes *map[string]*domain.Inode) (uint64, error) {
	var calculateSize func(inode *domain.Inode) (uint64, error) // recursive function to calculate size of a directory by summing sizes of its children

	calculateSize = func(inode *domain.Inode) (uint64, error) {
		if inode.Type == domain.InodeTypeFile {
			return inode.Size, nil
		}
		var totalSize uint64 = 0
		for _, childFID := range inode.Children {
			childInode, exists := (*inodes)[childFID.String()]
			if !exists {
				return 0, fmt.Errorf("child inode not found for FID: %s", childFID.String())
			}
			childSize, err := calculateSize(childInode)
			if err != nil {
				return 0, err
			}
			totalSize += childSize
		}
		inode.Size = totalSize // update size of this directory inode
		return totalSize, nil
	}

	totalUserDirSize, err := calculateSize(rootInode)
	return totalUserDirSize, err
}
