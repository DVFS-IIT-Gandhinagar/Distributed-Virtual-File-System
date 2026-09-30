package admin

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
)

// FSInfo holds the state of a single fileserver as recorded by the metaserver.
type FSInfo struct {
	Address           string `json:"address"`
	UserCount         int    `json:"user_count"`
	LastHeartbeatUnix int64  `json:"last_heartbeat_unix"`
	Status            string `json:"status"`
}

// MetaState is the top-level shape of the metaserver_state.json file.
type MetaState struct {
	FileServers map[string]FSInfo  `json:"fileservers"` // string key (numeric fsID)
	Users       map[string]uint64  `json:"users"`       // username -> fsID
	NextFsID    uint64             `json:"next_fs_id"`
}

// LoadMetaState reads and parses the metaserver state JSON file.
func LoadMetaState(path string) (*MetaState, error) {
	f, err := os.Open(path)
	if err != nil && os.IsNotExist(err) {
		// Try sensible fallbacks if the configured path doesn't exist
		fallbacks := []string{
			"./metaserver_state.json",
			"./bin/metaserver_state.json",
			"../metaserver_state.json",
		}
		for _, fb := range fallbacks {
			if fb == path {
				continue
			}
			if fbFile, fbErr := os.Open(fb); fbErr == nil {
				f = fbFile
				err = nil
				break
			}
		}
	}
	if err != nil {
		return nil, fmt.Errorf("LoadMetaState: open %s: %w", path, err)
	}
	defer f.Close()

	var state MetaState
	if err := json.NewDecoder(f).Decode(&state); err != nil {
		return nil, fmt.Errorf("LoadMetaState: decode %s: %w", path, err)
	}
	return &state, nil
}

// RemoveNodeFromMetaStateFile removes a fileserver and any associated user mappings
// from the metaserver state JSON file on disk.
func RemoveNodeFromMetaStateFile(path string, targetFsID string) error {
	if path == "" {
		return fmt.Errorf("empty path")
	}

	actualPath := path
	data, err := os.ReadFile(actualPath)
	if err != nil && os.IsNotExist(err) {
		fallbacks := []string{
			"./metaserver_state.json",
			"./bin/metaserver_state.json",
			"../metaserver_state.json",
		}
		for _, fb := range fallbacks {
			if fb == path {
				continue
			}
			if fbData, fbErr := os.ReadFile(fb); fbErr == nil {
				actualPath = fb
				data = fbData
				err = nil
				break
			}
		}
	}
	if err != nil {
		return fmt.Errorf("RemoveNodeFromMetaStateFile read %s: %w", path, err)
	}

	var rawState map[string]interface{}
	if err := json.Unmarshal(data, &rawState); err != nil {
		return fmt.Errorf("RemoveNodeFromMetaStateFile decode %s: %w", actualPath, err)
	}

	// Remove from fileservers
	if fsMap, ok := rawState["fileservers"].(map[string]interface{}); ok {
		delete(fsMap, targetFsID)
	}

	// Remove from users if mapped to this fsID
	var numericID uint64
	hasNum := false
	if n, parseErr := strconv.ParseUint(targetFsID, 10, 64); parseErr == nil {
		numericID = n
		hasNum = true
	}

	if usersMap, ok := rawState["users"].(map[string]interface{}); ok && hasNum {
		for u, v := range usersMap {
			var id uint64
			switch val := v.(type) {
			case float64:
				id = uint64(val)
			case int:
				id = uint64(val)
			case json.Number:
				if n, numErr := val.Int64(); numErr == nil {
					id = uint64(n)
				}
			}
			if id == numericID {
				delete(usersMap, u)
			}
		}
	}

	tmpFile := fmt.Sprintf("%s.tmp.%d", actualPath, os.Getpid())
	encoded, err := json.MarshalIndent(rawState, "", "  ")
	if err != nil {
		return fmt.Errorf("RemoveNodeFromMetaStateFile encode: %w", err)
	}
	if err := os.WriteFile(tmpFile, encoded, 0644); err != nil {
		return fmt.Errorf("RemoveNodeFromMetaStateFile write tmp: %w", err)
	}
	if err := os.Rename(tmpFile, actualPath); err != nil {
		_ = os.Remove(tmpFile)
		return fmt.Errorf("RemoveNodeFromMetaStateFile rename: %w", err)
	}
	return nil
}

