package admin

import (
	"context"
	"strings"
	"testing"
)

func TestFormatCommand(t *testing.T) {
	orchestrator := &Orchestrator{
		defaultRepoPath: "/home/ubuntu/repo",
	}

	params := &NodeRestartParams{
		FsID:     "0",
		Address:  "10.7.52.85:50052",
		Host:     "10.7.52.85",
		Port:     50052,
		MetaAddr: "10.7.52.85:50051",
		OwnIP:    "10.7.52.85",
		DataDir:  "./fileserver_data",
	}

	// 1. Pull default
	cmdPull := orchestrator.FormatCommand(&ActionRequest{ActionType: ActionPull}, "0", params)
	if cmdPull != "git -C /home/ubuntu/repo pull origin main" {
		t.Errorf("unexpected pull command: %s", cmdPull)
	}

	// 1b. Pull with custom branch
	cmdPullBranch := orchestrator.FormatCommand(&ActionRequest{ActionType: ActionPull, GitBranch: "feature-test"}, "0", params)
	if cmdPullBranch != "git -C /home/ubuntu/repo pull origin feature-test" {
		t.Errorf("unexpected pull branch command: %s", cmdPullBranch)
	}

	// 2. Build default
	cmdBuild := orchestrator.FormatCommand(&ActionRequest{ActionType: ActionBuild}, "0", params)
	if !strings.Contains(cmdBuild, "make -C /home/ubuntu/repo") || !strings.Contains(cmdBuild, "PATH") {
		t.Errorf("unexpected build command: %s", cmdBuild)
	}

	// 2b. Build with target
	cmdBuildTarget := orchestrator.FormatCommand(&ActionRequest{ActionType: ActionBuild, MakeTarget: "fileserver"}, "0", params)
	if !strings.Contains(cmdBuildTarget, "make -C /home/ubuntu/repo fileserver") {
		t.Errorf("unexpected build target command: %s", cmdBuildTarget)
	}

	// 3. Restart (systemctl default with -n)
	cmdRestartSys := orchestrator.FormatCommand(&ActionRequest{ActionType: ActionRestart}, "0", params)
	if cmdRestartSys != "sudo -n systemctl restart dvfs-fileserver" {
		t.Errorf("unexpected systemctl restart command: %s", cmdRestartSys)
	}

	// 3b. Restart metaserver
	cmdRestartMeta := orchestrator.FormatCommand(&ActionRequest{ActionType: ActionRestart, TargetService: "metaserver"}, "0", params)
	if cmdRestartMeta != "sudo -n systemctl restart dvfs-metaserver" {
		t.Errorf("unexpected systemctl metaserver restart command: %s", cmdRestartMeta)
	}

	// 3c. Restart admin
	cmdRestartAdmin := orchestrator.FormatCommand(&ActionRequest{ActionType: ActionRestart, TargetService: "admin"}, "0", params)
	if cmdRestartAdmin != "sudo -n systemctl restart dvfs-admin" {
		t.Errorf("unexpected systemctl admin restart command: %s", cmdRestartAdmin)
	}

	// 3d. Restart all
	cmdRestartAll := orchestrator.FormatCommand(&ActionRequest{ActionType: ActionRestart, TargetService: "all"}, "0", params)
	if cmdRestartAll != "sudo -n systemctl restart dvfs-metaserver dvfs-fileserver dvfs-admin" {
		t.Errorf("unexpected systemctl all restart command: %s", cmdRestartAll)
	}

	// 3e. APT Update & Upgrade (default)
	cmdApt := orchestrator.FormatCommand(&ActionRequest{ActionType: ActionApt}, "0", params)
	if !strings.Contains(cmdApt, "apt-get update") || !strings.Contains(cmdApt, "apt-get upgrade -y") {
		t.Errorf("unexpected apt command: %s", cmdApt)
	}

	// 3f. APT Update Only
	cmdAptUpdate := orchestrator.FormatCommand(&ActionRequest{ActionType: ActionApt, AptMode: "update_only"}, "0", params)
	if !strings.Contains(cmdAptUpdate, "apt-get update") || strings.Contains(cmdAptUpdate, "upgrade") {
		t.Errorf("unexpected apt update only command: %s", cmdAptUpdate)
	}

	// 4. Restart (binary mode with fuser and < /dev/null)
	cmdRestartBin := orchestrator.FormatCommand(&ActionRequest{ActionType: ActionRestart, RestartMode: "binary"}, "0", params)
	if !strings.Contains(cmdRestartBin, "fuser -k") || !strings.Contains(cmdRestartBin, "< /dev/null") {
		t.Errorf("unexpected binary restart command: %s", cmdRestartBin)
	}

	// 5. Logs (journalctl default)
	cmdLogsJ := orchestrator.FormatCommand(&ActionRequest{ActionType: ActionLogs, LogLines: 75}, "0", params)
	if cmdLogsJ != "journalctl -u dvfs-fileserver -n 75 --no-pager" {
		t.Errorf("unexpected journalctl command: %s", cmdLogsJ)
	}

	// 6. Logs (tail mode)
	cmdLogsTail := orchestrator.FormatCommand(&ActionRequest{ActionType: ActionLogs, LogMode: "tail", LogLines: 100}, "0", params)
	if cmdLogsTail != "tail -n 100 /home/ubuntu/repo/fileserver.log" {
		t.Errorf("unexpected tail command: %s", cmdLogsTail)
	}

	// 7. Reboot
	cmdReboot := orchestrator.FormatCommand(&ActionRequest{ActionType: ActionReboot}, "0", params)
	if !strings.Contains(cmdReboot, "sudo -n reboot") || !strings.Contains(cmdReboot, "sleep 2") {
		t.Errorf("unexpected reboot command: %s", cmdReboot)
	}

	// 8. Custom
	cmdCustom := orchestrator.FormatCommand(&ActionRequest{ActionType: ActionCustom, CustomCommand: "df -h"}, "0", params)
	if cmdCustom != "df -h" {
		t.Errorf("unexpected custom command: %s", cmdCustom)
	}
}

func TestMockSSHExecutor(t *testing.T) {
	mock := NewMockSSHExecutor()
	mock.Responses["uptime"] = MockSSHResponse{
		Stdout:   "load average: 0.15, 0.20, 0.10\n",
		ExitCode: 0,
	}
	mock.Responses["bad_cmd"] = MockSSHResponse{
		Stderr:   "command not found\n",
		ExitCode: 127,
	}

	ctx := context.Background()

	var stdout, stderr strings.Builder
	code, err := mock.Run(ctx, "10.0.0.1", 22, "ubuntu", "~/.ssh/id_rsa", "uptime", &stdout, &stderr)
	if err != nil || code != 0 {
		t.Fatalf("expected code 0, got %d, err=%v", code, err)
	}
	if !strings.Contains(stdout.String(), "load average") {
		t.Errorf("expected stdout with load average, got %s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	code, err = mock.Run(ctx, "10.0.0.1", 22, "ubuntu", "~/.ssh/id_rsa", "bad_cmd", &stdout, &stderr)
	if code != 127 {
		t.Errorf("expected exit code 127, got %d", code)
	}
	if !strings.Contains(stderr.String(), "command not found") {
		t.Errorf("expected stderr, got %s", stderr.String())
	}
}
