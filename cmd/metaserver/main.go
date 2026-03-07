package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"net"

	pb "github.com/umangshikarvar/dvfs/api/metaserver"
	"github.com/umangshikarvar/dvfs/internal/certs"
	"github.com/umangshikarvar/dvfs/internal/metaserver"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

func main() {
	// Server configuration
	port := flag.Int("port", 50051, "Port to listen on")
	flag.Parse()

	listenAddr := fmt.Sprintf("0.0.0.0:%d", *port)

	// Create meta server
	server, err := metaserver.NewMetaServer()
	if err != nil {
		log.Fatalf("Failed to create meta server: %v", err)
	}

	// Create gRPC handler
	handler := metaserver.NewGRPCHandler(server)

	// TLS configuration
	tlsCert, err := tls.X509KeyPair(certs.ServerCert, certs.ServerKey)
	if err != nil {
		log.Fatalf("Failed to load key pair: %v", err)
	}
	creds := credentials.NewServerTLSFromCert(&tlsCert)

	// Start gRPC server
	grpcServer := grpc.NewServer(grpc.Creds(creds))
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