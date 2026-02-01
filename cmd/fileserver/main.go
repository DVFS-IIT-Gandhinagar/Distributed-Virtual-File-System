package main

import (
	"log"
	"net"

	pb "github.com/umangshikarvar/dvfs/api/fileserver"
	"github.com/umangshikarvar/dvfs/internal/fileserver"
	"google.golang.org/grpc"
)

func main() {
	// Server configuration
	serverID := "fs1"
	rootDir := "./fileserver_data"
	listenAddr := "0.0.0.0:50051" // Listen on all interfaces

	// Create file server
	server, err := fileserver.NewFileServer(serverID, rootDir)
	if err != nil {
		log.Fatalf("Failed to create file server: %v", err)
	}

	// Create gRPC handler
	handler := fileserver.NewGRPCHandler(server)

	// Start gRPC server
	grpcServer := grpc.NewServer()
	pb.RegisterFileServerServer(grpcServer, handler)

	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	log.Printf("File server starting on %s", listenAddr)
	log.Printf("Data directory: %s", rootDir)

	if err := grpcServer.Serve(listener); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}
}