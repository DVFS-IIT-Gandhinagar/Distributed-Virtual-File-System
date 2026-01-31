package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	pb "github.com/umangshikarvar/dvfs/api/fileserver"
	"github.com/umangshikarvar/dvfs/internal/fileserver"
	"google.golang.org/grpc"
)

func main() {
	// Configuration
	serverID := "fs1"
	port := "50051"
	rootDir := "./fileserver_data"

	// Parse command line args
	if len(os.Args) > 1 {
		serverID = os.Args[1]
	}
	if len(os.Args) > 2 {
		port = os.Args[2]
	}
	if len(os.Args) > 3 {
		rootDir = os.Args[3]
	}

	// Create file server
	fs, err := fileserver.NewFileServer(serverID, rootDir)
	if err != nil {
		log.Fatalf("Failed to create file server: %v", err)
	}

	// Start gRPC server
	lis, err := net.Listen("tcp", ":"+port)
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	grpcServer := grpc.NewServer()
	pb.RegisterFileServerServer(grpcServer, fs)

	// Handle shutdown gracefully
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		fmt.Println("\nShutting down file server...")
		grpcServer.GracefulStop()
		os.Exit(0)
	}()

	fmt.Printf("File Server '%s' started on port %s\n", serverID, port)
	fmt.Printf("Root directory: %s\n", rootDir)
	fmt.Println("Press Ctrl+C to stop")

	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}
}
