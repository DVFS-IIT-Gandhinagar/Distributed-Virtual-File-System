package admin

import (
	"encoding/json"
	"fmt"
	"os"
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
