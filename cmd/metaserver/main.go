package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"net"
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
	flag.Parse()

	listenAddr := fmt.Sprintf("0.0.0.0:%d", *port)

	// Create meta server
	server, err := metaserver.NewMetaServer(*stateFile)
	if err != nil {
		log.Fatalf("Failed to create meta server: %v", err)
	}
	server.SetHeartbeatConfig(*heartbeatTimeout, *heartbeatCheckInterval)
	stopMonitor := server.StartHeartbeatMonitor()
	defer stopMonitor()

	// Create gRPC handler
	handler := metaserver.NewGRPCHandler(server)

	// TLS configuration
	var opts []grpc.ServerOption
	if *useTLS {
		log.Println("TLS enabled")
		tlsCert, err := tls.X509KeyPair(certs.ServerCert, certs.ServerKey)
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
