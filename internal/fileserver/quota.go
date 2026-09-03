package fileserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

const (
	quotaConfigFile     = "quota_config.json"
	defaultStorageQuota = 1024 * 1024 * 1024 // 1 GB default per user
)

// getUserQuotaLocked returns the configured quota for a user in bytes.
// Caller must hold fs.mu (either read or write lock).
func (fs *FileServer) getUserQuotaLocked(username string) uint64 {
	if q, exists := fs.quotas[username]; exists && q > 0 {
		return q
	}
	return defaultStorageQuota
}

// GetUserQuota returns the configured quota for a user in bytes.
// If no custom quota is configured, it returns the default quota (1 GB).
func (fs *FileServer) GetUserQuota(username string) uint64 {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	return fs.getUserQuotaLocked(username)
}

// SetUserQuota updates the quota for a user and persists it to quota_config.json.
func (fs *FileServer) SetUserQuota(username string, quotaBytes uint64) error {
	if username == "" {
		return errors.New("username cannot be empty")
	}
	if quotaBytes == 0 {
		return errors.New("quota must be greater than 0")
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()

	if fs.quotas == nil {
		fs.quotas = make(map[string]uint64)
	}

	fs.quotas[username] = quotaBytes

	if err := fs.saveQuotasLocked(); err != nil {
		log.Printf("[FILESERVER] Error saving quotas: %v", err)
		return fmt.Errorf("failed to persist quota: %w", err)
	}

	log.Printf("[FILESERVER] Quota updated for user '%s': %d bytes", username, quotaBytes)
	return nil
}

// loadQuotas reads quota_config.json from fs.rootDir into fs.quotas.
func (fs *FileServer) loadQuotas() error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	if fs.quotas == nil {
		fs.quotas = make(map[string]uint64)
	}

	configPath := filepath.Join(fs.rootDir, quotaConfigFile)
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			// No config file yet, will use defaults
			return nil
		}
		return fmt.Errorf("failed to read quota config: %w", err)
	}

	var loaded map[string]uint64
	if err := json.Unmarshal(data, &loaded); err != nil {
		return fmt.Errorf("failed to parse quota config: %w", err)
	}

	for u, q := range loaded {
		fs.quotas[u] = q
	}

	log.Printf("[FILESERVER] Loaded %d custom user quotas from %s", len(fs.quotas), configPath)
	return nil
}

// saveQuotasLocked writes fs.quotas to quota_config.json using an atomic temp file rename.
// Caller must hold fs.mu.
func (fs *FileServer) saveQuotasLocked() error {
	configPath := filepath.Join(fs.rootDir, quotaConfigFile)

	tmpFile, err := os.CreateTemp(fs.rootDir, "quota_config_*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpName := tmpFile.Name()

	enc := json.NewEncoder(tmpFile)
	enc.SetIndent("", "  ")
	if err := enc.Encode(fs.quotas); err != nil {
		tmpFile.Close()
		os.Remove(tmpName)
		return fmt.Errorf("failed to encode quotas: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	if err := os.Rename(tmpName, configPath); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("failed to rename temp file to %s: %w", configPath, err)
	}

	return nil
}
