package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
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

	// Raft configuration flags (Phase 1: inactive when raft_enabled=false)
	raftEnabled := flag.Bool("raft_enabled", false, "Enable Raft consensus clustering")
	raftNodeID := flag.String("raft_node_id", "", "Unique Raft node ID (e.g. dvfs1)")
	raftBindAddr := flag.String("raft_bind_addr", "", "Raft TCP transport address (host:port)")
	raftPeers := flag.String("raft_peers", "", "Comma-separated list of Raft peers (nodeID=host:port,...)")
	raftDataDir := flag.String("raft_data_dir", "./bin/raft/", "Directory for Raft logs and snapshots")
	raftBootstrap := flag.Bool("raft_bootstrap", false, "Bootstrap as initial Raft cluster leader")
	raftElectionTimeout := flag.Duration("raft_election_timeout", 1*time.Second, "Raft election timeout")
	raftHeartbeatTimeout := flag.Duration("raft_heartbeat_timeout", 200*time.Millisecond, "Raft leader heartbeat timeout")
	httpPort := flag.Int("http_port", 8090, "HTTP port for unauthenticated status/leader endpoint")
	flag.Parse()

	if *raftEnabled {
		log.Printf("[RAFT] Raft clustering enabled: node=%s bind=%s bootstrap=%v", *raftNodeID, *raftBindAddr, *raftBootstrap)
	} else {
		log.Println("[RAFT] Raft clustering disabled (single-node mode)")
	}
	_ = raftPeers
	_ = raftDataDir
	_ = raftElectionTimeout
	_ = raftHeartbeatTimeout

	listenAddr := fmt.Sprintf("0.0.0.0:%d", *port)

	// Start lightweight HTTP status server for /leader checks
	if *httpPort > 0 {
		httpMux := http.NewServeMux()
		httpMux.HandleFunc("/leader", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			leaderResp := map[string]interface{}{
				"is_leader":   true,
				"leader_id":   "dvfs1",
				"leader_addr": fmt.Sprintf("0.0.0.0:%d", *port),
				"raft_state":  "Leader",
			}
			if *raftNodeID != "" {
				leaderResp["leader_id"] = *raftNodeID
			}
			_ = json.NewEncoder(w).Encode(leaderResp)
		})

		httpAddr := fmt.Sprintf("0.0.0.0:%d", *httpPort)
		go func() {
			log.Printf("[HTTP] Status server listening on %s (endpoint: /leader)", httpAddr)
			if err := http.ListenAndServe(httpAddr, httpMux); err != nil && err != http.ErrServerClosed {
				log.Printf("[HTTP ERROR] Status server error: %v", err)
			}
		}()
	}

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
