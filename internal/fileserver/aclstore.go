package fileserver

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/umangshikarvar/dvfs/internal/domain"
)

// ACLMetadata represents the persisted ACL information
type ACLMetadata struct {
	Owner  string   `json:"owner"`
	Shared []string `json:"shared"`
}

const aclFileName = ".acl"

// getACLPath returns the path to the ACL file for a given username
func (fs *FileServer) getACLPath(path string) string {
	return filepath.Join(fs.rootDir, path, aclFileName)
}

// SaveACL persists the ACL to disk in the user's root directory
// Uses atomic write (write to temp file, then rename) to prevent corruption
func (fs *FileServer) SaveACL(path string, acl domain.ACL) error {
	// Convert domain.ACL to ACLMetadata
	metadata := ACLMetadata{
		Owner:  acl.Owner,
		Shared: acl.Shared,
	}

	// Serialize to JSON
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal ACL: %w", err)
	}

	// Get ACL file path
	aclPath := fs.getACLPath(path)
	tmpPath := aclPath + ".tmp"

	// Write to temporary file first
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write ACL to temp file: %w", err)
	}

	// Atomic rename to final location
	if err := os.Rename(tmpPath, aclPath); err != nil {
		// Clean up temp file if rename fails
		os.Remove(tmpPath)
		return fmt.Errorf("failed to rename ACL file: %w", err)
	}

	return nil
}

// LoadACL loads the ACL from disk for a user's root directory
// Returns default ACL (owner only, no shared users) if file doesn't exist
func (fs *FileServer) LoadACL(username, path string) (domain.ACL, error) {
	// Default ACL (owner only, no shared users)
	defaultACL := domain.ACL{
		Owner:  username,
		Shared: []string{},
	}

	// Get ACL file path
	aclPath := fs.getACLPath(path)

	// Check if ACL file exists
	if _, err := os.Stat(aclPath); os.IsNotExist(err) {
		// File doesn't exist, return default ACL (not an error)
		return defaultACL, nil
	}

	// Read ACL file
	data, err := os.ReadFile(aclPath)
	if err != nil {
		return defaultACL, fmt.Errorf("failed to read ACL file: %w", err)
	}

	// Parse JSON
	var metadata ACLMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return defaultACL, fmt.Errorf("failed to parse ACL JSON: %w", err)
	}

	log.Printf("[FILESERVER] LoadACL for path=%s: owner=%s, shared=%v (from file)", path, metadata.Owner, metadata.Shared)

	// Convert ACLMetadata to domain.ACL
	acl := domain.ACL{
		Owner:  metadata.Owner,
		Shared: metadata.Shared,
	}

	// Ensure Shared is not nil (empty slice instead)
	if acl.Shared == nil {
		acl.Shared = []string{}
	}

	log.Printf("[FILESERVER] LoadACL returning: owner=%s, shared=%v", acl.Owner, acl.Shared)

	return acl, nil
}
