package admin

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Supported orchestration action types.
const (
	ActionPull    = "pull"
	ActionBuild   = "build"
	ActionRestart = "restart"
	ActionLogs    = "logs"
	ActionCustom  = "custom"
)

// NodeRestartParams holds configurable parameters for fileserver process launch.
type NodeRestartParams struct {
	FsID     string `json:"fs_id"`
	Address  string `json:"address"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	SSHPort  int    `json:"ssh_port,omitempty"`
	SSHUser  string `json:"ssh_user,omitempty"`
	MetaAddr string `json:"meta_addr"`
	OwnIP    string `json:"own_ip"`
	DataDir  string `json:"data_dir"`
}

// ActionRequest specifies an orchestration operation to execute across cluster nodes.
type ActionRequest struct {
	ActionType     string                        `json:"action_type"`
	TargetNodeIDs  []string                      `json:"target_node_ids"`
	CustomCommand  string                        `json:"custom_command,omitempty"`
	RepoPath       string                        `json:"repo_path,omitempty"`
	GitBranch      string                        `json:"git_branch,omitempty"`
	MakeTarget     string                        `json:"make_target,omitempty"`
	TimeoutSeconds int                           `json:"timeout_seconds,omitempty"`
	SSHPort        int                           `json:"ssh_port,omitempty"`
	LogLines       int                           `json:"log_lines,omitempty"`
	LogPath        string                        `json:"log_path,omitempty"`
	RestartMode    string                        `json:"restart_mode,omitempty"` // "systemctl" (default) or "binary"
	LogMode        string                        `json:"log_mode,omitempty"`     // "journalctl" (default) or "tail"
	RestartParams  map[string]*NodeRestartParams `json:"restart_params,omitempty"`
	SSHUser        string                        `json:"ssh_user,omitempty"`
	SSHKeyPath     string                        `json:"ssh_key_path,omitempty"`
}

// ActionEvent represents a real-time event emitted during action execution.
type ActionEvent struct {
	Type       string `json:"type"` // "action_started", "node_started", "node_output", "node_finished", "action_finished"
	ActionID   string `json:"action_id"`
	Command    string `json:"command,omitempty"`
	NodeID     string `json:"node_id,omitempty"`
	Address    string `json:"address,omitempty"`
	Stream     string `json:"stream,omitempty"` // "stdout" or "stderr"
	Chunk      string `json:"chunk,omitempty"`
	ExitCode   int    `json:"exit_code"`
	DurationMs int64  `json:"duration_ms,omitempty"`
	Status     string `json:"status,omitempty"` // "running", "success", "failed"
	Error      string `json:"error,omitempty"`
}

// Orchestrator coordinates SSH command execution across fileserver nodes.
type Orchestrator struct {
	server          *AdminServer
	ssh             SSHExecutor
	history         *CommandHistory
	defaultSSHUser  string
	defaultSSHKey   string
	defaultRepoPath string
	defaultSSHPort  int
	activeNodes     sync.Map // nodeID (string) -> actionID (string)
}

// NewOrchestrator creates a new Orchestrator instance.
func NewOrchestrator(server *AdminServer, ssh SSHExecutor, history *CommandHistory, defaultSSHUser, defaultSSHKey, defaultRepoPath string, defaultSSHPort ...int) *Orchestrator {
	port := 22
	if len(defaultSSHPort) > 0 && defaultSSHPort[0] > 0 {
		port = defaultSSHPort[0]
	}
	return &Orchestrator{
		server:          server,
		ssh:             ssh,
		history:         history,
		defaultSSHUser:  defaultSSHUser,
		defaultSSHKey:   defaultSSHKey,
		defaultRepoPath: defaultRepoPath,
		defaultSSHPort:  port,
	}
}

// GetPresets generates pre-filled configuration parameters for all discovered nodes.
func (o *Orchestrator) GetPresets() map[string]*NodeRestartParams {
	o.server.mu.RLock()
	defer o.server.mu.RUnlock()

	presets := make(map[string]*NodeRestartParams)
	for fsID, node := range o.server.nodes {
		host, portStr, err := net.SplitHostPort(node.Address)
		port := 50052
		if err == nil {
			if p, parseErr := strconv.Atoi(portStr); parseErr == nil {
				port = p
			}
		} else {
			host = node.Address
		}

		nodeSSHUser := o.defaultSSHUser
		if nodeSSHUser == "" || strings.HasPrefix(nodeSSHUser, "dvfs") {
			if num, parseErr := strconv.Atoi(fsID); parseErr == nil {
				nodeSSHUser = fmt.Sprintf("dvfs%d", num+1)
			}
		}

		presets[fsID] = &NodeRestartParams{
			FsID:     fsID,
			Address:  node.Address,
			Host:     host,
			Port:     port,
			SSHPort:  o.defaultSSHPort,
			SSHUser:  nodeSSHUser,
			MetaAddr: "127.0.0.1:50051",
			OwnIP:    host,
			DataDir:  fmt.Sprintf("./fileserver_data_%s", fsID),
		}
	}
	return presets
}

// FormatCommand generates the exact bash command string for a specific node.
func (o *Orchestrator) FormatCommand(req *ActionRequest, nodeID string, params *NodeRestartParams) string {
	repoPath := req.RepoPath
	if repoPath == "" {
		repoPath = o.defaultRepoPath
	}
	if repoPath == "" {
		repoPath = "~/Distributed-Virtual-File-System"
	}

	switch req.ActionType {
	case ActionPull:
		if req.CustomCommand != "" {
			return req.CustomCommand
		}
		branch := req.GitBranch
		if branch == "" {
			branch = "main"
		}
		return fmt.Sprintf("git -C %s pull origin %s", repoPath, branch)

	case ActionBuild:
		target := req.MakeTarget
		if target != "" {
			target = " " + target
		}
		return fmt.Sprintf(". ~/.profile 2>/dev/null; . ~/.bashrc 2>/dev/null; export PATH=\"$PATH:/usr/local/go/bin:/snap/bin:$HOME/go/bin\"; make -C %s%s", repoPath, target)

	case ActionRestart:
		if req.CustomCommand != "" {
			return req.CustomCommand
		}
		if req.RestartMode == "binary" {
			dataDir := params.DataDir
			if dataDir == "" {
				dataDir = "./fileserver_data"
			}
			metaAddr := params.MetaAddr
			if metaAddr == "" {
				metaAddr = "127.0.0.1:50051"
			}
			ownIP := params.OwnIP
			if ownIP == "" {
				ownIP = params.Host
			}
			return fmt.Sprintf(
				"fuser -k %d/tcp 2>/dev/null || pkill -f 'fileserver -id=%s' || true; sleep 1; nohup %s/bin/fileserver -id=%s -port=%d -data=%s -meta_addr=%s -own_ip=%s > %s/fileserver.log 2>&1 < /dev/null &",
				params.Port, params.FsID, repoPath, params.FsID, params.Port, dataDir, metaAddr, ownIP, repoPath,
			)
		}
		// Default systemd restart (using -n for non-interactive sudo safety)
		return "sudo -n systemctl restart dvfs-fileserver"

	case ActionLogs:
		if req.CustomCommand != "" {
			return req.CustomCommand
		}
		lines := req.LogLines
		if lines <= 0 {
			lines = 50
		}
		if req.LogMode == "tail" {
			logPath := req.LogPath
			if logPath == "" {
				logPath = fmt.Sprintf("%s/fileserver.log", repoPath)
			}
			return fmt.Sprintf("tail -n %d %s", lines, logPath)
		}
		// Default journalctl
		return fmt.Sprintf("journalctl -u dvfs-fileserver -n %d --no-pager", lines)

	case ActionCustom:
		return req.CustomCommand

	default:
		return req.CustomCommand
	}
}

// eventWriter wraps an output stream to emit real-time WebSocket events as data arrives.
type eventWriter struct {
	nodeID   string
	stream   string
	actionID string
	onEvent  func(ActionEvent)
	buf      bytes.Buffer
}

func (w *eventWriter) Write(p []byte) (n int, err error) {
	n = len(p)
	w.buf.Write(p)
	if w.onEvent != nil {
		w.onEvent(ActionEvent{
			Type:     "node_output",
			ActionID: w.actionID,
			NodeID:   w.nodeID,
			Stream:   w.stream,
			Chunk:    string(p),
		})
	}
	return n, nil
}

// Execute orchestrates command execution across selected nodes in parallel.
func (o *Orchestrator) Execute(ctx context.Context, req ActionRequest, onEvent func(ActionEvent)) (*CommandRecord, error) {
	actionID := fmt.Sprintf("act-%s", uuid.New().String()[:8])
	startTime := time.Now()

	// Resolve SSH user and key
	sshUser := req.SSHUser
	if sshUser == "" {
		sshUser = o.defaultSSHUser
	}
	if sshUser == "" {
		sshUser = "ubuntu"
	}

	sshKeyPath := req.SSHKeyPath
	if sshKeyPath == "" {
		sshKeyPath = o.defaultSSHKey
	}
	if sshKeyPath == "" {
		sshKeyPath = "~/.ssh/id_rsa"
	}

	// Validate target nodes
	if len(req.TargetNodeIDs) == 0 {
		return nil, fmt.Errorf("no target nodes specified")
	}

	o.server.mu.RLock()
	targetNodes := make(map[string]*NodeState)
	for _, nodeID := range req.TargetNodeIDs {
		if node, exists := o.server.nodes[nodeID]; exists {
			targetNodes[nodeID] = node
		}
	}
	o.server.mu.RUnlock()

	if len(targetNodes) == 0 {
		return nil, fmt.Errorf("none of the specified target nodes were found in active cluster")
	}

	// Concurrency lock: ensure none of the target nodes are currently running an action
	for _, nID := range req.TargetNodeIDs {
		if activeAct, busy := o.activeNodes.Load(nID); busy {
			return nil, fmt.Errorf("node FS-%s is currently executing action %s", nID, activeAct)
		}
	}
	for _, nID := range req.TargetNodeIDs {
		o.activeNodes.Store(nID, actionID)
	}
	defer func() {
		for _, nID := range req.TargetNodeIDs {
			o.activeNodes.Delete(nID)
		}
	}()

	// Execution timeout (default 300s / 5 minutes)
	timeout := time.Duration(req.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 300 * time.Second
	}
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	presets := o.GetPresets()

	// Formatted command string representation for history and notifications
	displayCmd := req.CustomCommand
	if displayCmd == "" {
		for nID := range targetNodes {
			displayCmd = o.FormatCommand(&req, nID, presets[nID])
			break
		}
	}
	if displayCmd == "" {
		displayCmd = req.ActionType
	}

	// Initial record in running state
	record := CommandRecord{
		ID:          actionID,
		Timestamp:   startTime.Unix(),
		ActionType:  req.ActionType,
		Command:     displayCmd,
		TargetNodes: req.TargetNodeIDs,
		Status:      "running",
		NodeResults: make(map[string]*NodeResult),
	}

	if onEvent != nil {
		onEvent(ActionEvent{
			Type:     "action_started",
			ActionID: actionID,
			Command:  displayCmd,
			Status:   "running",
		})
	}

	var wg sync.WaitGroup
	var resultsMu sync.Mutex
	overallFailed := false

	for nodeID, node := range targetNodes {
		wg.Add(1)

		go func(nID string, nState *NodeState) {
			defer wg.Done()
			nodeStart := time.Now()

			host, _, _ := net.SplitHostPort(nState.Address)
			if host == "" {
				host = nState.Address
			}

			params := presets[nID]
			if req.RestartParams != nil && req.RestartParams[nID] != nil {
				params = req.RestartParams[nID]
			}

			sshPort := req.SSHPort
			if sshPort <= 0 && params != nil && params.SSHPort > 0 {
				sshPort = params.SSHPort
			}
			if sshPort <= 0 && o.defaultSSHPort > 0 {
				sshPort = o.defaultSSHPort
			}
			if sshPort <= 0 {
				sshPort = 22
			}

			cmd := o.FormatCommand(&req, nID, params)

			if onEvent != nil {
				onEvent(ActionEvent{
					Type:     "node_started",
					ActionID: actionID,
					NodeID:   nID,
					Address:  nState.Address,
					Command:  cmd,
				})
			}

			stdoutWriter := &eventWriter{nodeID: nID, stream: "stdout", actionID: actionID, onEvent: onEvent}
			stderrWriter := &eventWriter{nodeID: nID, stream: "stderr", actionID: actionID, onEvent: onEvent}

			nodeUser := sshUser
			if params != nil && params.SSHUser != "" {
				nodeUser = params.SSHUser
			} else if sshUser == "" || strings.HasPrefix(sshUser, "dvfs") {
				if num, parseErr := strconv.Atoi(nID); parseErr == nil {
					nodeUser = fmt.Sprintf("dvfs%d", num+1)
				}
			}

			exitCode, err := o.ssh.Run(execCtx, host, sshPort, nodeUser, sshKeyPath, cmd, stdoutWriter, stderrWriter)
			nodeDuration := time.Since(nodeStart).Milliseconds()

			nodeOutput := stdoutWriter.buf.String()
			if stderrWriter.buf.Len() > 0 {
				if nodeOutput != "" {
					nodeOutput += "\n"
				}
				nodeOutput += "[STDERR]\n" + stderrWriter.buf.String()
			}

			errMsg := ""
			if err != nil {
				if execCtx.Err() == context.DeadlineExceeded {
					errMsg = fmt.Sprintf("command timed out after %v", timeout)
				} else {
					errMsg = err.Error()
				}
				overallFailed = true
			} else if exitCode != 0 {
				overallFailed = true
			}

			if onEvent != nil {
				onEvent(ActionEvent{
					Type:       "node_finished",
					ActionID:   actionID,
					NodeID:     nID,
					Address:    nState.Address,
					ExitCode:   exitCode,
					DurationMs: nodeDuration,
					Error:      errMsg,
				})
			}

			resultsMu.Lock()
			record.NodeResults[nID] = &NodeResult{
				NodeID:     nID,
				Address:    nState.Address,
				ExitCode:   exitCode,
				Output:     nodeOutput,
				Error:      errMsg,
				DurationMs: nodeDuration,
			}
			resultsMu.Unlock()
		}(nodeID, node)
	}

	wg.Wait()

	totalDuration := time.Since(startTime).Milliseconds()
	record.DurationMs = totalDuration
	if overallFailed {
		record.Status = "failed"
	} else {
		record.Status = "success"
	}

	// Push to bounded ring buffer and disk
	o.history.Push(record)

	if onEvent != nil {
		onEvent(ActionEvent{
			Type:       "action_finished",
			ActionID:   actionID,
			Status:     record.Status,
			DurationMs: totalDuration,
		})
	}

	return &record, nil
}
