package main

import (
	"context"
	"flag"
	"log"
	"os"
	"time"

	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/admin"
	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/client"
	mongostore "github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/storage/mongo"
)

func main() {
	mongoURI := flag.String("mongo_uri", "", "MongoDB connection URI (overrides MONGO_URI env)")
	mongoDB := flag.String("mongo_db", "", "MongoDB database name; overrides the database in -mongo_uri (default: the URI's database, else \"dvfs\")")
	port := flag.Int("port", 8080, "Admin server port")
	staticDir := flag.String("static", "./cmd/admin/static", "Path to static frontend files")
	sshUser := flag.String("ssh_user", "", "Default SSH username for cluster nodes (optional)")
	sshKey := flag.String("ssh_key", "~/.ssh/id_ed25519", "Path to default SSH private key for cluster node access")
	sshPort := flag.Int("ssh_port", 22, "Default SSH port for cluster nodes")
	repoPath := flag.String("repo_path", "~/Distributed-Virtual-File-System", "Path to cloned DVFS repository on remote nodes")
	historyFile := flag.String("history_file", "./command_history.json", "Path to persistent command history JSON")
	historyLimit := flag.Int("history_limit", 100, "Maximum number of command execution records to retain in ring buffer")
	tlsCert := flag.String("tls_cert", "certs/server.crt", "Path to TLS server certificate")
	tlsKey := flag.String("tls_key", "certs/server.key", "Path to TLS private key")
	tlsEnabled := flag.Bool("tls", false, "Force enable TLS (auto-enabled if cert and key exist)")
	gistURL := flag.String("gist_url", "", "Custom GitHub Gist URL for machines discovery (optional)")
	metaserverAddr := flag.String("metaserver_addr", "", "gRPC address of the metaserver (e.g. 10.0.171.40:50051 or dvfs1:50051)")
	flag.Parse()

	log.Printf("[ADMIN] Starting Admin Console on port %d...", *port)
	log.Printf("[ADMIN] Static directory: %s", *staticDir)
	log.Printf("[ADMIN] SSH User: '%s', SSH Key: '%s', Port: %d, Repo Path: '%s'", *sshUser, *sshKey, *sshPort, *repoPath)

	uri := *mongoURI
	if uri == "" {
		uri = os.Getenv("MONGO_URI")
	}
	if uri == "" {
		log.Fatalf("MongoDB is required: pass -mongo_uri or set MONGO_URI. " +
			"The admin console reads cluster membership from the shared metadata store, " +
			"so it no longer needs to run on the metaserver host.")
	}

	connectCtx, connectCancel := context.WithTimeout(context.Background(), 30*time.Second)
	store, err := mongostore.Open(connectCtx, mongostore.Config{
		URI:      uri,
		Database: *mongoDB,
		AppName:  "dvfs-admin",
	})
	connectCancel()
	if err != nil {
		log.Fatalf("[ADMIN] Failed to connect to MongoDB: %v", err)
	}
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer closeCancel()
		_ = store.Close(closeCtx)
	}()
	log.Printf("[ADMIN] Connected to MongoDB database %q", store.DatabaseName())

	server := admin.NewAdminServer(store, *staticDir)
	msAddr := *metaserverAddr
	if msAddr == "" {
		msAddr = os.Getenv("DVFS_METASERVER_ADDR")
	}
	if msAddr != "" {
		server.SetMetaServerAddr(msAddr)
		log.Printf("[ADMIN] MetaServer address configured: %s", msAddr)
	}
	if *gistURL != "" {
		server.SetDiscoveryResolver(client.NewDiscoveryResolver(client.WithGistURL(*gistURL)))
	}
	history := admin.NewCommandHistory(*historyLimit, *historyFile)
	server.SetHistory(history)
	orchestrator := admin.NewOrchestrator(server, admin.NewRemoteSSHExecutor(), history, *sshUser, *sshKey, *repoPath, *sshPort)
	server.SetOrchestrator(orchestrator)

	if *tlsCert != "" && *tlsKey != "" {
		certPath := *tlsCert
		keyPath := *tlsKey
		if _, err := os.Stat(certPath); os.IsNotExist(err) {
			if _, errDev := os.Stat("deploy_certs/localhost/server.crt"); errDev == nil {
				certPath = "deploy_certs/localhost/server.crt"
				keyPath = "deploy_certs/localhost/server.key"
			}
		}

		if _, errCert := os.Stat(certPath); errCert == nil {
			if _, errKey := os.Stat(keyPath); errKey == nil {
				server.SetTLS(certPath, keyPath)
				log.Printf("[ADMIN] Direct TLS configured (cert: %s, key: %s)", certPath, keyPath)
			} else if *tlsEnabled {
				log.Fatalf("[ADMIN] TLS private key not found: %s", keyPath)
			}
		} else if *tlsEnabled {
			log.Fatalf("[ADMIN] TLS certificate not found: %s", certPath)
		}
	}

	if err := server.Run(*port); err != nil {
		log.Fatalf("[ADMIN] Server failed: %v", err)
	}
}
