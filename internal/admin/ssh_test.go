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

func TestFormatCommandBinaryRestartUsesMongoURI(t *testing.T) {
	// A realistic replica-set URI: "&" would background the command and "?"
	// is a glob character unless the whole value is quoted.
	const uri = "mongodb://dvfs:s3cret@db1:27017,db2:27017/dvfs?replicaSet=rs0&authSource=admin"
	orchestrator := &Orchestrator{defaultRepoPath: "/home/ubuntu/repo", mongoURI: uri, mongoDB: "dvfs"}
	params := &NodeRestartParams{
		FsID: "0", Address: "10.7.52.85:50052", Host: "10.7.52.85", Port: 50052,
		MetaAddr: "10.7.52.85:50051", OwnIP: "10.7.52.85", DataDir: "./fileserver_data",
	}

	for _, service := range []string{"metaserver", "admin", "all"} {
		t.Run(service, func(t *testing.T) {
			cmd := orchestrator.FormatCommand(&ActionRequest{
				ActionType:    ActionRestart,
				RestartMode:   "binary",
				TargetService: service,
			}, "0", params)

			if strings.Contains(cmd, "-state_file") {
				t.Errorf("%s restart still passes the removed -state_file flag: %s", service, cmd)
			}
			if !strings.Contains(cmd, "-mongo_uri='"+uri+"'") {
				t.Errorf("%s restart must pass the URI single-quoted: %s", service, cmd)
			}
			if !strings.Contains(cmd, "-mongo_db='dvfs'") {
				t.Errorf("%s restart must carry the database override: %s", service, cmd)
			}
			if strings.Contains(cmd, "-mongo_uri="+uri) {
				t.Errorf("%s restart has an unquoted URI, so the shell would split it at '&': %s", service, cmd)
			}
		})
	}
}

// The console's own target wins over the environment; the environment is only
// the fallback for a console that was never told what it connected to.
func TestFormatCommandPrefersConfiguredMongoTarget(t *testing.T) {
	t.Setenv("MONGO_URI", "mongodb://env-host:27017/other")
	configured := &Orchestrator{mongoURI: "mongodb://real-host:27017/dvfs"}
	fallback := &Orchestrator{}
	req := &ActionRequest{ActionType: ActionRestart, RestartMode: "binary", TargetService: "metaserver"}

	if cmd := configured.FormatCommand(req, "0", &NodeRestartParams{}); !strings.Contains(cmd, "real-host") || strings.Contains(cmd, "env-host") {
		t.Errorf("configured target must win: %s", cmd)
	}
	if cmd := fallback.FormatCommand(req, "0", &NodeRestartParams{}); !strings.Contains(cmd, "env-host") {
		t.Errorf("environment must be the fallback: %s", cmd)
	}
}

func TestShellQuote(t *testing.T) {
	cases := map[string]string{
		"plain":                 "'plain'",
		"a b":                   "'a b'",
		"x?y&z":                 "'x?y&z'",
		"it's":                  `'it'\''s'`,
		"$(rm -rf /)":           "'$(rm -rf /)'",
		"mongodb://u:p@h/d?a=1": "'mongodb://u:p@h/d?a=1'",
	}
	for in, want := range cases {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestRedactMongoURI(t *testing.T) {
	cases := map[string]string{
		"nohup ./bin/metaserver -mongo_uri='mongodb://dvfs:s3cret@db1:27017/dvfs?replicaSet=rs0' > log": "nohup ./bin/metaserver -mongo_uri='mongodb://dvfs:***@db1:27017/dvfs?replicaSet=rs0' > log",
		"-mongo_uri='mongodb+srv://admin:p%40ss@cluster0.example.net/dvfs'":                             "-mongo_uri='mongodb+srv://admin:***@cluster0.example.net/dvfs'",
		"-mongo_uri='mongodb://127.0.0.1:27017/dvfs'":                                                   "-mongo_uri='mongodb://127.0.0.1:27017/dvfs'",
		"git -C ~/repo pull origin main":                                                                "git -C ~/repo pull origin main",
	}
	for in, want := range cases {
		if got := redactMongoURI(in); got != want {
			t.Errorf("redactMongoURI(%q)\n got %q\nwant %q", in, got, want)
		}
	}
}

func TestResolveMongoURIPrefersEnvironment(t *testing.T) {
	t.Setenv("MONGO_URI", "mongodb://db1:27017,db2:27017/dvfs?replicaSet=rs0")
	if got := resolveMongoURI(); got != "mongodb://db1:27017,db2:27017/dvfs?replicaSet=rs0" {
		t.Errorf("a restarted binary must inherit the console's cluster, got %q", got)
	}

	t.Setenv("MONGO_URI", "")
	if got := resolveMongoURI(); got != defaultMongoURI {
		t.Errorf("expected the local default, got %q", got)
	}
}
