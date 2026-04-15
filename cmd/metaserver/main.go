package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"time"

	pb "github.com/umangshikarvar/dvfs/api/metaserver"
	"github.com/umangshikarvar/dvfs/internal/certs"
	"github.com/umangshikarvar/dvfs/internal/metaserver"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

func main() {
	// Server configuration
	port := flag.Int("port", 50051, "Port to listen on")
	stateFile := flag.String("state_file", "./metaserver_state.json", "Path to metaserver state snapshot file")
	heartbeatTimeout := flag.Duration("heartbeat_timeout", 30*time.Second, "Timeout after which fileserver is marked stale")
	heartbeatCheckInterval := flag.Duration("heartbeat_check_interval", 5*time.Second, "Interval to evaluate fileserver liveness")
	useTLS := flag.Bool("tls", false, "Enable TLS (default: false)")
	hasTLS := flag.Bool("has_tls", false, "Enable TLS (alias of -tls)")
	hasTLSDash := flag.Bool("has-tls", false, "Enable TLS (alias of -tls)")
	haslTLS := flag.Bool("hasl_tls", false, "Enable TLS (typo-compatible alias of -tls)")
	haslTLSDash := flag.Bool("hasl-tls", false, "Enable TLS (typo-compatible alias of -tls)")
	publicIP := flag.String("public_ip", "", "Public IP used for strict TLS identity and SAN (required when TLS is enabled)")
	tlsCertFile := flag.String("tls_cert_file", "", "Path to TLS server certificate (overrides DVFS_SERVER_CERT_FILE)")
	tlsKeyFile := flag.String("tls_key_file", "", "Path to TLS server key (overrides DVFS_SERVER_KEY_FILE)")
	tlsCAFile := flag.String("tls_ca_file", "", "Path to CA certificate (overrides DVFS_CA_CERT_FILE)")
	tlsCAKeyFile := flag.String("tls_ca_key_file", "", "Path to CA private key (overrides DVFS_CA_KEY_FILE)")
	revokedFSFingerprintsFile := flag.String("revoked_fs_fingerprints_file", "", "Path to newline-separated revoked fileserver cert SHA-256 fingerprints")
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
	if *tlsCAKeyFile != "" {
		_ = os.Setenv("DVFS_CA_KEY_FILE", *tlsCAKeyFile)
	}

	if tlsEnabled {
		if *publicIP == "" {
			log.Fatalf("TLS requires -public_ip for strict IP identity")
		}
		if err := certs.EnsureMetaServerPKI(*publicIP); err != nil {
			log.Fatalf("Failed to initialize MetaServer PKI: %v", err)
		}
	}

	listenAddr := fmt.Sprintf("0.0.0.0:%d", *port)

	// Create meta server
	server, err := metaserver.NewMetaServer(*stateFile)
	if err != nil {
		log.Fatalf("Failed to create meta server: %v", err)
	}
	if *revokedFSFingerprintsFile != "" {
		added, err := server.LoadRevokedFileServerFingerprintsFromFile(*revokedFSFingerprintsFile)
		if err != nil {
			log.Fatalf("Failed to load revoked fileserver fingerprints: %v", err)
		}
		log.Printf("Loaded %d revoked fileserver certificate fingerprint(s) from %s", added, *revokedFSFingerprintsFile)
	}
	server.SetHeartbeatConfig(*heartbeatTimeout, *heartbeatCheckInterval)
	stopMonitor := server.StartHeartbeatMonitor()
	defer stopMonitor()

	// Create gRPC handler
	handler := metaserver.NewGRPCHandler(server)

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
	pb.RegisterMetaServerServer(grpcServer, handler)

	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	log.Printf("Meta server starting on %s", listenAddr)

	if err := grpcServer.Serve(listener); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}
}
