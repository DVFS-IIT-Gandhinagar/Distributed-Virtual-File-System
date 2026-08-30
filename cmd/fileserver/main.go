package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"net"
	"time"

	pb "github.com/umangshikarvar/dvfs/api/fileserver"
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
	tlsCertPath := flag.String("tls_cert", "certs/server.crt", "Path to TLS certificate")
	tlsKeyPath := flag.String("tls_key", "certs/server.key", "Path to TLS private key")
	caCertPath := flag.String("ca_cert", "certs/ca.crt", "Path to CA certificate")
	flag.Parse()

	listenAddr := fmt.Sprintf("0.0.0.0:%d", *port)

	// Create file server
	server, err := fileserver.NewFileServer(*serverID, *rootDir, *useTLS, *msAddr, *caCertPath)
	if err != nil {
		log.Fatalf("Failed to create file server: %v", err)
	}

	// Create gRPC handler
	handler := fileserver.NewGRPCHandler(server)

	// TLS configuration
	var opts []grpc.ServerOption
	// Increase max receive message size to 64MB to support file upload chunks
	// The default 4MB limit is too small once proto field overhead is added to a 4MB chunk.
	opts = append(opts, grpc.MaxRecvMsgSize(64*1024*1024))
	if *useTLS {
		log.Println("TLS enabled")
		tlsCert, err := tls.LoadX509KeyPair(*tlsCertPath, *tlsKeyPath)
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
