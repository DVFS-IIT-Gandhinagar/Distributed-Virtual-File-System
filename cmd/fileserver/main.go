package main

import (
	"flag"
	"fmt"
	"log"
	"net"

	pb "github.com/umangshikarvar/dvfs/api/fileserver"
	"github.com/umangshikarvar/dvfs/internal/fileserver"
	"google.golang.org/grpc"
)

func main() {
	// Server configuration
	serverID := flag.String("id", "fs1", "Server ID")
	port := flag.Int("port", 50051, "Port to listen on")
	rootDir := flag.String("data", "./fileserver_data", "Data directory")
	flag.Parse()

	listenAddr := fmt.Sprintf("0.0.0.0:%d", *port)

	// Create file server
	server, err := fileserver.NewFileServer(*serverID, *rootDir)
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