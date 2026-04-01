package fileserver

import (
	"context"
	"crypto/x509"
	"fmt"
	"log"
	"net"
	"time"

	mspb "github.com/umangshikarvar/dvfs/api/metaserver"
	"github.com/umangshikarvar/dvfs/internal/certs"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// RegisterWithMetaServer dials the meta server over TLS and registers this file
// server along with all users it currently knows about.
// selfAddr is the host:port that the meta server should store as this FS's address.
// If msAddr is empty this is a no-op.
func (fs *FileServer) RegisterWithMetaServer(selfAddr string) error {
	if fs.msAddr == "" {
		return nil
	}

	// Build the same CA-backed TLS config the client uses when talking to FS.
	var opts []grpc.DialOption
	if fs.useTLS {
		cp := x509.NewCertPool()
		if !cp.AppendCertsFromPEM(certs.CACert) {
			return fmt.Errorf("failed to append CA certificate")
		}

		host, _, err := net.SplitHostPort(fs.msAddr)
		if err != nil {
			host = fs.msAddr
		}
		creds := credentials.NewClientTLSFromCert(cp, host)
		opts = append(opts, grpc.WithTransportCredentials(creds))
	} else {
		opts = append(opts, grpc.WithInsecure())
	}

	conn, err := grpc.NewClient(fs.msAddr, opts...)
	if err != nil {
		return fmt.Errorf("failed to connect to meta server: %w", err)
	}
	defer conn.Close()

	// Collect known users and their ACLs under the read lock
	fs.mu.RLock()
	users := make([]string, 0, len(fs.users))
	acls := make([]*mspb.SharedDir, 0, len(fs.users))

	for username, rootFID := range fs.users {
		users = append(users, username)

		// Get root inode to access ACL
		rootInode := fs.inodes[rootFID.String()]

		path, err := fs.Path(rootFID)
		if err != nil {
			log.Printf("[FILESERVER] Failed to resolve path for user %s: %v", username, err)
			continue
		}

		// Create SharedDir message
		sharedDir := &mspb.SharedDir{
			Owner: username,
			Name: username,
			Path: path,
			Users:   rootInode.ACL.Shared,
		}
		acls = append(acls, sharedDir)
	}
	fs.mu.RUnlock()

	client := mspb.NewMetaServerClient(conn)
	resp, err := client.RegisterFileServer(context.Background(), &mspb.RegisterFileServerRequest{
		Address: selfAddr,
		Users:   users,
		Shared:    acls,
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

	// Build the same CA-backed TLS config the client uses when talking to FS.
	var opts []grpc.DialOption
	if fs.useTLS {
		cp := x509.NewCertPool()
		if !cp.AppendCertsFromPEM(certs.CACert) {
			return fmt.Errorf("failed to append CA certificate")
		}

		host, _, err := net.SplitHostPort(fs.msAddr)
		if err != nil {
			host = fs.msAddr
		}
		creds := credentials.NewClientTLSFromCert(cp, host)
		opts = append(opts, grpc.WithTransportCredentials(creds))
	} else {
		opts = append(opts, grpc.WithInsecure())
	}

	conn, err := grpc.NewClient(fs.msAddr, opts...)
	if err != nil {
		return fmt.Errorf("failed to connect to meta server: %w", err)
	}
	defer conn.Close()

	client := mspb.NewMetaServerClient(conn)
	resp, err := client.RootShare(context.Background(), &mspb.RootShareRequest{
		Owner: owner,
		RootPath:  path,
		ShareWith: share_with,
		Name: 	name,
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

	// Build the same CA-backed TLS config the client uses when talking to FS.
	var opts []grpc.DialOption
	if fs.useTLS {
		cp := x509.NewCertPool()
		if !cp.AppendCertsFromPEM(certs.CACert) {
			return fmt.Errorf("failed to append CA certificate")
		}

		host, _, err := net.SplitHostPort(fs.msAddr)
		if err != nil {
			host = fs.msAddr
		}
		creds := credentials.NewClientTLSFromCert(cp, host)
		opts = append(opts, grpc.WithTransportCredentials(creds))
	} else {
		opts = append(opts, grpc.WithInsecure())
	}

	conn, err := grpc.NewClient(fs.msAddr, opts...)
	if err != nil {
		return fmt.Errorf("failed to connect to meta server: %w", err)
	}
	defer conn.Close()

	client := mspb.NewMetaServerClient(conn)
	resp, err := client.RootUnshare(context.Background(), &mspb.RootUnshareRequest{
		Owner: owner,
		RootPath:  path,
		UnshareWith: unshare_with,
		Name: 	name,
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

	var opts []grpc.DialOption
	if fs.useTLS {
		cp := x509.NewCertPool()
		if !cp.AppendCertsFromPEM(certs.CACert) {
			return fmt.Errorf("failed to append CA certificate")
		}

		host, _, err := net.SplitHostPort(fs.msAddr)
		if err != nil {
			host = fs.msAddr
		}
		creds := credentials.NewClientTLSFromCert(cp, host)
		opts = append(opts, grpc.WithTransportCredentials(creds))
	} else {
		opts = append(opts, grpc.WithInsecure())
	}

	conn, err := grpc.NewClient(fs.msAddr, opts...)
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
