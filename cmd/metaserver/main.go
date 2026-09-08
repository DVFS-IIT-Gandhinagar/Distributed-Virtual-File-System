package main

import (
	"crypto/tls"
	"crypto/x509"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"time"

	pb "github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/api/metaserver"
	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/metaserver"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

func main() {
	// Server configuration
	port := flag.Int("port", 50051, "Port to listen on")
	stateFile := flag.String("state_file", "./metaserver_state.json", "Path to metaserver state snapshot file")
	heartbeatTimeout := flag.Duration("heartbeat_timeout", 30*time.Second, "Timeout after which fileserver is marked stale")
	heartbeatCheckInterval := flag.Duration("heartbeat_check_interval", 5*time.Second, "Interval to evaluate fileserver liveness")
	tlsCertPath := flag.String("tls_cert", "certs/server.crt", "Path to TLS certificate")
	tlsKeyPath := flag.String("tls_key", "certs/server.key", "Path to TLS private key")
	caCertPath := flag.String("ca_cert", "certs/ca.crt", "Path to Root CA certificate for mTLS client verification")
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
	if *tlsCertPath != "" && *tlsKeyPath != "" {
		if _, errCert := os.Stat(*tlsCertPath); errCert == nil {
			if _, errKey := os.Stat(*tlsKeyPath); errKey == nil {
				tlsCert, err := tls.LoadX509KeyPair(*tlsCertPath, *tlsKeyPath)
				if err != nil {
					log.Fatalf("Failed to load key pair: %v", err)
				}
				tlsConfig := &tls.Config{
					Certificates: []tls.Certificate{tlsCert},
					ClientAuth:   tls.RequestClientCert,
				}

				caPath := *caCertPath
				if _, err := os.Stat(caPath); os.IsNotExist(err) {
					if _, errDev := os.Stat("deploy_certs/ca.crt"); errDev == nil {
						caPath = "deploy_certs/ca.crt"
					}
				}
				if caBytes, err := os.ReadFile(caPath); err == nil {
					cp := x509.NewCertPool()
					if cp.AppendCertsFromPEM(caBytes) {
						tlsConfig.ClientCAs = cp
						tlsConfig.ClientAuth = tls.VerifyClientCertIfGiven
						log.Printf("mTLS client verification enabled with CA from %s", caPath)
					}
				}

				creds := credentials.NewTLS(tlsConfig)
				opts = append(opts, grpc.Creds(creds))
				log.Println("TLS enabled with server certificate")
			}
		}
	}

	if interceptor := metaserver.GetServerAuthInterceptor(); interceptor != nil {
		opts = append(opts, grpc.UnaryInterceptor(interceptor))
		log.Println("[AUTH] Google Authentication enforcement enabled on metaserver")
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
