package main

import (
	"context"
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
	mongostore "github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/storage/mongo"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

func main() {
	// Server configuration
	port := flag.Int("port", 50051, "Port to listen on")
	mongoURI := flag.String("mongo_uri", "", "MongoDB connection URI (overrides MONGO_URI env)")
	mongoDB := flag.String("mongo_db", "", "MongoDB database name; overrides the database in -mongo_uri (default: the URI's database, else \"dvfs\")")
	allowUserPurge := flag.Bool("allow_user_purge", false,
		"Permit a fileserver registration to delete user mappings it did not report. "+
			"Off by default: an unmounted data directory registers as having no users.")
	heartbeatTimeout := flag.Duration("heartbeat_timeout", 30*time.Second, "Timeout after which fileserver is marked stale")
	heartbeatCheckInterval := flag.Duration("heartbeat_check_interval", 5*time.Second, "Interval to evaluate fileserver liveness")
	tlsCertPath := flag.String("tls_cert", "certs/server.crt", "Path to TLS certificate")
	tlsKeyPath := flag.String("tls_key", "certs/server.key", "Path to TLS private key")
	caCertPath := flag.String("ca_cert", "certs/ca.crt", "Path to Root CA certificate for mTLS client verification")
	flag.Parse()

	listenAddr := fmt.Sprintf("0.0.0.0:%d", *port)

	uri := *mongoURI
	if uri == "" {
		uri = os.Getenv("MONGO_URI")
	}
	if uri == "" {
		log.Fatalf("MongoDB is required: pass -mongo_uri or set MONGO_URI " +
			"(e.g. mongodb://dvfs1:27017,dvfs2:27017,dvfs3:27017/dvfs?replicaSet=rs0)")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	store, err := mongostore.Open(ctx, mongostore.Config{
		URI:      uri,
		Database: *mongoDB,
		AppName:  "dvfs-metaserver",
	})
	if err != nil {
		cancel()
		log.Fatalf("Failed to connect to MongoDB: %v", err)
	}
	if err := store.EnsureIndexes(ctx); err != nil {
		cancel()
		log.Fatalf("Failed to create MongoDB indexes: %v", err)
	}

	// Create meta server
	server, err := metaserver.NewMetaServer(ctx, store)
	cancel()
	if err != nil {
		log.Fatalf("Failed to create meta server: %v", err)
	}
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer closeCancel()
		_ = store.Close(closeCtx)
	}()
	server.SetHeartbeatConfig(*heartbeatTimeout, *heartbeatCheckInterval)
	server.SetAllowUserPurge(*allowUserPurge)
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
