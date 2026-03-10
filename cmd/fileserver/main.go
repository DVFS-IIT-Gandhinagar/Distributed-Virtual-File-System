package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"net"

	pb "github.com/umangshikarvar/dvfs/api/fileserver"
	"github.com/umangshikarvar/dvfs/internal/certs"
	"github.com/umangshikarvar/dvfs/internal/fileserver"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

func main() {
	// Server configuration
	serverID := flag.String("id", "fs1", "Server ID")
	port := flag.Int("port", 50051, "Port to listen on")
	rootDir := flag.String("data", "./fileserver_data", "Data directory")
	// metaserver is the meta server's host:port; leave empty to skip registration.
	msAddr := flag.String("meta_addr", "", "Meta server address (e.g. 127.0.0.1:50052)")
	ownAddr := flag.String("own_addr", "", "Own address to advertise to meta server (e.g. 127.0.0.1:50051)")
	useTLS := flag.Bool("tls", false, "Enable TLS (default: true)")
	flag.Parse()

	listenAddr := fmt.Sprintf("0.0.0.0:%d", *port)

	// Create file server
	server, err := fileserver.NewFileServer(*serverID, *rootDir, *useTLS)
	if err != nil {
		log.Fatalf("Failed to create file server: %v", err)
	}

	// Create gRPC handler
	handler := fileserver.NewGRPCHandler(server)

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
	pb.RegisterFileServerServer(grpcServer, handler)

	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	// Register with meta server if address was provided.
	selfAddr := fmt.Sprintf("%s:%d", *ownAddr, *port)
	if err := server.RegisterWithMetaServer(*msAddr, selfAddr); err != nil {
		log.Printf("Warning: failed to register with meta server: %v", err)
	} else if *msAddr != "" {
		log.Printf("Registered with meta server at %s", *msAddr)
	}

	log.Printf("File server starting on %s", listenAddr)
	log.Printf("Advertised address: %s", selfAddr)
	log.Printf("Data directory: %s", *rootDir)

	if err := grpcServer.Serve(listener); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}
}
