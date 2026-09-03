package main

import (
	"flag"
	"log"

	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/admin"
)

func main() {
	stateFile := flag.String("state_file", "./bin/metaserver_state.json", "Path to metaserver state JSON")
	port := flag.Int("port", 8080, "Admin server port")
	staticDir := flag.String("static", "./cmd/admin/static", "Path to static frontend files")
	sshUser := flag.String("ssh_user", "", "Default SSH username for cluster nodes (optional)")
	sshKey := flag.String("ssh_key", "~/.ssh/id_ed25519", "Path to default SSH private key for cluster node access")
	sshPort := flag.Int("ssh_port", 22, "Default SSH port for cluster nodes")
	repoPath := flag.String("repo_path", "~/Distributed-Virtual-File-System", "Path to cloned DVFS repository on remote nodes")
	historyFile := flag.String("history_file", "./command_history.json", "Path to persistent command history JSON")
	historyLimit := flag.Int("history_limit", 100, "Maximum number of command execution records to retain in ring buffer")
	flag.Parse()

	log.Printf("[ADMIN] Starting Admin Console on port %d...", *port)
	log.Printf("[ADMIN] State file: %s", *stateFile)
	log.Printf("[ADMIN] Static directory: %s", *staticDir)
	log.Printf("[ADMIN] SSH User: '%s', SSH Key: '%s', Port: %d, Repo Path: '%s'", *sshUser, *sshKey, *sshPort, *repoPath)

	server := admin.NewAdminServer(*stateFile, *staticDir)
	history := admin.NewCommandHistory(*historyLimit, *historyFile)
	server.SetHistory(history)
	server.SetOrchestrator(admin.NewOrchestrator(server, admin.NewRemoteSSHExecutor(), history, *sshUser, *sshKey, *repoPath, *sshPort))

	if err := server.Run(*port); err != nil {
		log.Fatalf("[ADMIN] Server failed: %v", err)
	}
}
