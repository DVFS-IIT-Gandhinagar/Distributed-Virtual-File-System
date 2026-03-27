package fileserver

import (
	"context"
	"crypto/x509"
	"fmt"
	"net"

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
    acls := make([]*mspb.UserACL, 0, len(fs.users))
    
    for username, rootFID := range fs.users {
        users = append(users, username)
        
        // Get root inode to access ACL
        rootInode := fs.inodes[rootFID.String()]
        
        // Create UserACL message
        userACL := &mspb.UserACL{
            Username: username,
            Shared:   rootInode.ACL.Shared,
        }
        acls = append(acls, userACL)
    }
    fs.mu.RUnlock()

	client := mspb.NewMetaServerClient(conn)
	resp, err := client.RegisterFileServer(context.Background(), &mspb.RegisterFileServerRequest{
		Address: selfAddr,
		Users:   users,
		Acls:    acls,
	})
	if err != nil {
		return fmt.Errorf("RegisterFileServer RPC failed: %w", err)
	}
	if !resp.Success {
		return fmt.Errorf("meta server rejected registration: %s", resp.Error)
	}

	return nil
}

func (fs *FileServer) RootShare(root_user, share_with string) error {
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
		RootUser: root_user,
		ShareWith: share_with,  
	})
	if err != nil {
		return fmt.Errorf("RootShare RPC failed: %w", err)
	}
	if !resp.Success {
		return fmt.Errorf("meta server rejected sharing: %s", resp.Error)
	}

	return nil
}

func (fs *FileServer) RootUnshare(root_user, unshare_with string) error {
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
		RootUser: root_user,
		UnshareWith: unshare_with,  
	})
	if err != nil {
		return fmt.Errorf("RootShare RPC failed: %w", err)
	}
	if !resp.Success {
		return fmt.Errorf("meta server rejected sharing: %s", resp.Error)
	}

	return nil
}