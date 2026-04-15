package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"time"

	pb "github.com/umangshikarvar/dvfs/api/fileserver"
	"github.com/umangshikarvar/dvfs/internal/certs"
	"github.com/umangshikarvar/dvfs/internal/fileserver"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

func main() {
	// Server configuration
	serverID := flag.String("id", "fs1", "Server ID")
	port := flag.Int("port", 50052, "Port to listen on")
	rootDir := flag.String("data", "./fileserver_data", "Data directory")
	// metaserver is the meta server's host:port; leave empty to skip registration.
	msAddr := flag.String("meta_addr", "127.0.0.1:50051", "Meta server address (e.g. 127.0.0.1:50052)")
	ownIp := flag.String("own_ip", "127.0.0.1", "Own IP to advertise to meta server (e.g. 127.0.0.1)")
	msRetry := flag.Duration("meta_retry_interval", 3*time.Second, "Retry interval for metaserver registration")
	msHeartbeat := flag.Duration("meta_heartbeat_interval", 5*time.Second, "Heartbeat interval for metaserver liveness")
	useTLS := flag.Bool("tls", false, "Enable TLS (default: false)")
	hasTLS := flag.Bool("has_tls", false, "Enable TLS (alias of -tls)")
	hasTLSDash := flag.Bool("has-tls", false, "Enable TLS (alias of -tls)")
	haslTLS := flag.Bool("hasl_tls", false, "Enable TLS (typo-compatible alias of -tls)")
	haslTLSDash := flag.Bool("hasl-tls", false, "Enable TLS (typo-compatible alias of -tls)")
	tlsCertFile := flag.String("tls_cert_file", "", "Path to TLS server certificate (overrides DVFS_SERVER_CERT_FILE)")
	tlsKeyFile := flag.String("tls_key_file", "", "Path to TLS server key (overrides DVFS_SERVER_KEY_FILE)")
	tlsCAFile := flag.String("tls_ca_file", "", "Path to CA certificate for outbound TLS to metaserver (overrides DVFS_CA_CERT_FILE)")
	flag.Parse()
	tlsEnabled := *useTLS || *hasTLS || *hasTLSDash || *haslTLS || *haslTLSDash

	if *tlsCertFile != "" {
		_ = os.Setenv("DVFS_SERVER_CERT_FILE", *tlsCertFile)
	}
	if *tlsKeyFile != "" {
		_ = os.Setenv("DVFS_SERVER_KEY_FILE", *tlsKeyFile)
	}
	if *tlsCAFile != "" {
		_ = os.Setenv("DVFS_CA_CERT_FILE", *tlsCAFile)
	}

	listenAddr := fmt.Sprintf("0.0.0.0:%d", *port)

	// Create file server
	server, err := fileserver.NewFileServer(*serverID, *rootDir, tlsEnabled, *msAddr)
	if err != nil {
		log.Fatalf("Failed to create file server: %v", err)
	}

	// Create gRPC handler
	handler := fileserver.NewGRPCHandler(server)

	// TLS configuration
	var opts []grpc.ServerOption
	if tlsEnabled {
		log.Println("TLS enabled")
		tlsCert, err := certs.LoadServerTLSCert()
		if err != nil {
			log.Fatalf("Failed to load key pair: %v", err)
		}
		creds := credentials.NewServerTLSFromCert(&tlsCert)
		opts = append(opts, grpc.Creds(creds))
	}

	// Start gRPC server
	grpcServer := grpc.NewServer(opts...)
	pb.RegisterFileServerServer(grpcServer, handler)

	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	// Start background sync with metaserver if an address was provided.
	selfAddr := fmt.Sprintf("%s:%d", *ownIp, *port)
	stopSync := server.StartMetaServerSync(*msAddr, selfAddr, *msRetry, *msHeartbeat)
	defer stopSync()
	if *msAddr != "" {
		log.Printf("Started metaserver sync loop for %s", *msAddr)
	}

	log.Printf("File server starting on %s", listenAddr)
	log.Printf("Advertised address: %s", selfAddr)
	log.Printf("Data directory: %s", *rootDir)

	if err := grpcServer.Serve(listener); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}
}
