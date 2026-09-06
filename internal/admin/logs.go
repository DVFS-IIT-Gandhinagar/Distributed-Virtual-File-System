package admin

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// LogTailResponse captures the output of a log query for a specific fileserver node.
type LogTailResponse struct {
	NodeID    string `json:"node_id"`
	Address   string `json:"address"`
	Service   string `json:"service"`
	Lines     int    `json:"lines"`
	Content   string `json:"content"`
	Timestamp int64  `json:"timestamp"`
}

// FetchNodeLogs queries the remote node via SSH to tail recent system or file logs.
func (a *AdminServer) FetchNodeLogs(ctx context.Context, nodeID string, service string, lines int, mode string, extraOpts ...string) (*LogTailResponse, error) {
	if lines <= 0 {
		lines = 100
	}
	if service == "" {
		service = "fileserver"
	}
	if mode == "" {
		mode = "journalctl"
	}

	a.mu.RLock()
	node, exists := a.nodes[nodeID]
	if !exists {
		// Try resolving by display name
		for _, n := range a.nodes {
			if strings.EqualFold(n.DisplayName, nodeID) || strconv.Itoa(n.DisplayID) == nodeID {
				node = n
				exists = true
				break
			}
		}
	}
	a.mu.RUnlock()

	if !exists || node == nil {
		return nil, fmt.Errorf("node %s not found", nodeID)
	}

	serviceName := "dvfs-fileserver"
	logFileName := "fileserver.log"
	switch service {
	case "metaserver":
		serviceName = "dvfs-metaserver"
		logFileName = "metaserver.log"
	case "admin":
		serviceName = "dvfs-admin"
		logFileName = "admin.log"
	}

	repoPath := "~/Distributed-Virtual-File-System"
	if a.orchestrator != nil && a.orchestrator.defaultRepoPath != "" {
		repoPath = a.orchestrator.defaultRepoPath
	}
	repoLog := fmt.Sprintf("%s/%s", repoPath, logFileName)

	var cmd string
	if mode == "tail" {
		cmd = fmt.Sprintf("tail -n %d %s 2>/dev/null || tail -n %d %s 2>/dev/null || tail -n %d /var/log/%s 2>/dev/null || journalctl -u %s -n %d --no-pager 2>/dev/null",
			lines, repoLog, lines, logFileName, lines, logFileName, serviceName, lines)
	} else {
		cmd = fmt.Sprintf("(journalctl -u %s -n %d --no-pager 2>/dev/null | grep -v '^--' | grep .) || tail -n %d %s 2>/dev/null || tail -n %d %s 2>/dev/null || journalctl -u %s -n %d --no-pager",
			serviceName, lines, lines, repoLog, lines, logFileName, serviceName, lines)
	}

	now := time.Now().Unix()

	// If orchestrator or SSH executor is configured, run via SSH
	if a.orchestrator != nil && a.orchestrator.ssh != nil {
		host, _, err := net.SplitHostPort(node.Address)
		if err != nil {
			host = node.Address
		}

		sshPort := 22
		if a.orchestrator.defaultSSHPort > 0 {
			sshPort = a.orchestrator.defaultSSHPort
		}
		presets := a.orchestrator.GetPresets()
		if presets != nil && presets[node.FsID] != nil && presets[node.FsID].SSHPort > 0 {
			sshPort = presets[node.FsID].SSHPort
		}

		// Resolve SSH User matching Actions logic
		sshUser := a.orchestrator.defaultSSHUser
		if presets != nil && presets[node.FsID] != nil && presets[node.FsID].SSHUser != "" {
			sshUser = presets[node.FsID].SSHUser
		} else if sshUser == "" || strings.HasPrefix(sshUser, "dvfs") {
			if node.MachineName != "" {
				sshUser = node.MachineName
			} else if num, parseErr := strconv.Atoi(node.FsID); parseErr == nil {
				sshUser = fmt.Sprintf("dvfs%d", num+1)
			}
		}
		if sshUser == "" {
			sshUser = "ubuntu"
		}

		sshKey := a.orchestrator.defaultSSHKey
		if sshKey == "" {
			sshKey = "~/.ssh/id_ed25519"
		}

		// Apply optional overrides if provided
		if len(extraOpts) > 0 && extraOpts[0] != "" {
			sshUser = extraOpts[0]
		}
		if len(extraOpts) > 1 && extraOpts[1] != "" {
			sshKey = extraOpts[1]
		}
		if len(extraOpts) > 2 && extraOpts[2] != "" {
			if p, pErr := strconv.Atoi(extraOpts[2]); pErr == nil && p > 0 {
				sshPort = p
			}
		}

		var stdoutBuf, stderrBuf bytes.Buffer
		exitCode, err := a.orchestrator.ssh.Run(ctx, host, sshPort, sshUser, sshKey, cmd, &stdoutBuf, &stderrBuf)
		content := stdoutBuf.String()
		if content == "" && stderrBuf.Len() > 0 {
			content = stderrBuf.String()
		}
		if err != nil && content == "" {
			return &LogTailResponse{
				NodeID:    node.FsID,
				Address:   node.Address,
				Service:   service,
				Lines:     lines,
				Content:   fmt.Sprintf("[ERROR] Failed to query logs from %s (exit code %d): %v\n%s", node.Address, exitCode, err, stderrBuf.String()),
				Timestamp: now,
			}, nil
		}
		return &LogTailResponse{
			NodeID:    node.FsID,
			Address:   node.Address,
			Service:   service,
			Lines:     lines,
			Content:   content,
			Timestamp: now,
		}, nil
	}

	// Fallback diagnostic output for environments without SSH
	return &LogTailResponse{
		NodeID:    node.FsID,
		Address:   node.Address,
		Service:   service,
		Lines:     lines,
		Content:   fmt.Sprintf("[%s] Simulation log stream for %s on %s (SSH not initialized)", time.Now().Format(time.RFC3339), service, node.Address),
		Timestamp: now,
	}, nil
}
