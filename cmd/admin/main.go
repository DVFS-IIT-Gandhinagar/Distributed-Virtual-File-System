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
	flag.Parse()

	log.Printf("[ADMIN] Starting Admin Console on port %d...", *port)
	log.Printf("[ADMIN] State file: %s", *stateFile)
	log.Printf("[ADMIN] Static directory: %s", *staticDir)

	server := admin.NewAdminServer(*stateFile, *staticDir)
	if err := server.Run(*port); err != nil {
		log.Fatalf("[ADMIN] Server failed: %v", err)
	}
}
