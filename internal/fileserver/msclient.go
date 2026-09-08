package fileserver

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"time"

	mspb "github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/api/metaserver"
	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/domain"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// dialMetaServer dials the configured metaserver. If certs/ca.crt exists, it verifies TLS
// and presents mutual TLS (mTLS) client certificates; otherwise it falls back to plaintext (e.g. in mock unit tests).
func (fs *FileServer) dialMetaServer() (*grpc.ClientConn, error) {
	if fs.msAddr == "" {
		return nil, fmt.Errorf("no metaserver address configured")
	}

	var opts []grpc.DialOption
	caPath := fs.caCertPath
	if caPath == "" {
		caPath = "certs/ca.crt"
	}
	if _, err := os.Stat(caPath); os.IsNotExist(err) {
		if _, errDev := os.Stat("deploy_certs/ca.crt"); errDev == nil {
			caPath = "deploy_certs/ca.crt"
		}
	}
	if _, err := os.Stat(caPath); err == nil {
		caBytes, err := os.ReadFile(caPath)
		if err == nil {
			cp := x509.NewCertPool()
			if cp.AppendCertsFromPEM(caBytes) {
				host, _, err := net.SplitHostPort(fs.msAddr)
				if err != nil {
					host = fs.msAddr
				}
				serverName := host
				if ip := net.ParseIP(host); ip != nil {
					if ip.IsLoopback() {
						serverName = "localhost"
					} else {
						serverName = "dvfs1"
					}
				}

				tlsConfig := &tls.Config{
					RootCAs:    cp,
					ServerName: serverName,
				}

				// Load client certificate for mTLS if available
				certPath := fs.tlsCertPath
				keyPath := fs.tlsKeyPath
				if certPath == "" {
					certPath = "certs/server.crt"
				}
				if keyPath == "" {
					keyPath = "certs/server.key"
				}
				if _, err := os.Stat(certPath); os.IsNotExist(err) {
					if _, errDev := os.Stat(filepath.Join("deploy_certs", fs.serverID, "server.crt")); errDev == nil {
						certPath = filepath.Join("deploy_certs", fs.serverID, "server.crt")
						keyPath = filepath.Join("deploy_certs", fs.serverID, "server.key")
					}
				}

				if clientCert, err := tls.LoadX509KeyPair(certPath, keyPath); err == nil {
					tlsConfig.Certificates = []tls.Certificate{clientCert}
				} else {
					log.Printf("[FILESERVER] Warning: could not load mTLS client certificate (%s, %s): %v", certPath, keyPath, err)
				}

				creds := credentials.NewTLS(tlsConfig)
				opts = append(opts, grpc.WithTransportCredentials(creds))
			}
		}
	}
	if len(opts) == 0 {
		opts = append(opts, grpc.WithInsecure())
	}

	return grpc.NewClient(fs.msAddr, opts...)
}

// RegisterWithMetaServer dials the meta server and registers this file
// server along with all users it currently knows about.
// selfAddr is the host:port that the meta server should store as this FS's address.
// If msAddr is empty this is a no-op.
func (fs *FileServer) RegisterWithMetaServer(selfAddr string) error {
	if fs.msAddr == "" {
		return nil
	}

	conn, err := fs.dialMetaServer()
	if err != nil {
		return fmt.Errorf("failed to connect to meta server: %w", err)
	}
	defer conn.Close()

	// Collect known users and their explicit directory shares under the read lock
	fs.mu.RLock()
	users := make([]string, 0, len(fs.users))
	acls := make([]*mspb.SharedDir, 0)

	// Collect all users
	for username := range fs.users {
		users = append(users, username)
	}

	// Build SharedDir messages from fs.Shared (dirShares)
	// fs.Shared maps path -> []users
	for dirPath, sharedUsers := range fs.Shared {
		// Find the inode for this path
		fullPath := filepath.Join(fs.rootDir, dirPath)

		// Clean both paths for comparison (resolve . and .. components)
		cleanFullPath := filepath.Clean(fullPath)

		var foundInode *domain.Inode
		for _, inode := range fs.inodes {
			cleanInodePath := filepath.Clean(inode.OSPath)
			if cleanInodePath == cleanFullPath {
				foundInode = inode
				break
			}
		}

		if foundInode == nil {
			log.Printf("[FILESERVER] Warning: path '%s' in Shared map but inode not found (looking for OSPath='%s')",
				dirPath, cleanFullPath)
			// Debug: print all directory inodes to help diagnose
			log.Printf("[FILESERVER] Debug: All directory inodes:")
			for _, inode := range fs.inodes {
				if inode.Type == domain.InodeTypeDirectory {
					log.Printf("[FILESERVER] Debug:   OSPath='%s', Name='%s'", inode.OSPath, inode.Name)
				}
			}
			continue
		}

		// Create SharedDir message with explicit shares
		sharedDir := &mspb.SharedDir{
			Owner: foundInode.ACL.Owner,
			Name:  foundInode.Name,
			Path:  dirPath,
			Users: sharedUsers,
		}
		acls = append(acls, sharedDir)
		log.Printf("[FILESERVER] Registration: dir=%s (owner=%s, path=%s) shared with %v",
			foundInode.Name, foundInode.ACL.Owner, dirPath, sharedUsers)
	}
	fs.mu.RUnlock()

	log.Printf("[FILESERVER] Registering with metaserver: %d users, %d explicit shares", len(users), len(acls))

	client := mspb.NewMetaServerClient(conn)
	resp, err := client.RegisterFileServer(context.Background(), &mspb.RegisterFileServerRequest{
		Address: selfAddr,
		Users:   users,
		Shared:  acls,
	})
	if err != nil {
		return fmt.Errorf("RegisterFileServer RPC failed: %w", err)
	}
	if !resp.Success {
		return fmt.Errorf("meta server rejected registration: %s", resp.Error)
	}

	return nil
}

func (fs *FileServer) RootShare(owner, name, path, share_with string) error {
	if fs.msAddr == "" {
		return nil
	}

	conn, err := fs.dialMetaServer()
	if err != nil {
		return fmt.Errorf("failed to connect to meta server: %w", err)
	}
	defer conn.Close()

	client := mspb.NewMetaServerClient(conn)
	resp, err := client.RootShare(context.Background(), &mspb.RootShareRequest{
		Owner:     owner,
		RootPath:  path,
		ShareWith: share_with,
		Name:      name,
	})
	if err != nil {
		return fmt.Errorf("RootShare RPC failed: %w", err)
	}
	if !resp.Success {
		return fmt.Errorf("meta server rejected sharing: %s", resp.Error)
	}

	return nil
}

func (fs *FileServer) RootUnshare(owner, name, path, unshare_with string) error {
	if fs.msAddr == "" {
		return nil
	}

	conn, err := fs.dialMetaServer()
	if err != nil {
		return fmt.Errorf("failed to connect to meta server: %w", err)
	}
	defer conn.Close()

	client := mspb.NewMetaServerClient(conn)
	resp, err := client.RootUnshare(context.Background(), &mspb.RootUnshareRequest{
		Owner:       owner,
		RootPath:    path,
		UnshareWith: unshare_with,
		Name:        name,
	})
	if err != nil {
		return fmt.Errorf("RootShare RPC failed: %w", err)
	}
	if !resp.Success {
		return fmt.Errorf("meta server rejected sharing: %s", resp.Error)
	}

	return nil
}

// HeartbeatWithMetaServer sends a lightweight liveness signal to metaserver.
func (fs *FileServer) HeartbeatWithMetaServer(selfAddr string) error {
	if fs.msAddr == "" {
		return nil
	}

	conn, err := fs.dialMetaServer()
	if err != nil {
		return fmt.Errorf("failed to connect to meta server: %w", err)
	}
	defer conn.Close()

	client := mspb.NewMetaServerClient(conn)
	resp, err := client.Heartbeat(context.Background(), &mspb.HeartbeatRequest{Address: selfAddr})
	if err != nil {
		return fmt.Errorf("Heartbeat RPC failed: %w", err)
	}
	if !resp.Success {
		return fmt.Errorf("meta server heartbeat rejected: %s", resp.Error)
	}

	return nil
}

// StartMetaServerSync keeps trying registration in the background so this file
// server can automatically re-attach when the metaserver restarts.
func (fs *FileServer) StartMetaServerSync(msAddr, selfAddr string, retryInterval, heartbeatInterval time.Duration) func() {
	if msAddr == "" {
		return func() {}
	}

	if retryInterval <= 0 {
		retryInterval = 3 * time.Second
	}
	if heartbeatInterval <= 0 {
		heartbeatInterval = 5 * time.Second
	}

	stopCh := make(chan struct{})

	go func() {
		registered := false
		lastHeartbeatAt := time.Time{}

		attemptRegister := func(reason string) {
			err := fs.RegisterWithMetaServer(selfAddr)
			if err != nil {
				registered = false
				log.Printf("[FILESERVER] MetaServer sync (%s) failed: %v", reason, err)
				return
			}

			registered = true
			lastHeartbeatAt = time.Now()
			log.Printf("[FILESERVER] MetaServer sync (%s) succeeded", reason)
		}

		attemptHeartbeat := func(reason string) {
			err := fs.HeartbeatWithMetaServer(selfAddr)
			if err != nil {
				registered = false
				log.Printf("[FILESERVER] MetaServer heartbeat (%s) failed: %v", reason, err)
				return
			}

			lastHeartbeatAt = time.Now()
			log.Printf("[FILESERVER] MetaServer heartbeat (%s) succeeded", reason)
		}

		attemptRegister("startup")
		ticker := time.NewTicker(retryInterval)
		defer ticker.Stop()

		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				if !registered {
					attemptRegister("retry")
					continue
				}

				if time.Since(lastHeartbeatAt) >= heartbeatInterval {
					attemptHeartbeat("periodic")
					if !registered {
						continue
					}
				}
			}
		}
	}()

	return func() {
		close(stopCh)
	}
}
