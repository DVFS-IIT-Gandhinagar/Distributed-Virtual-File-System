//go:build !use_google_auth

package fileserver

import (
	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/fileserver/session"
	"google.golang.org/grpc"
)

// SetGlobalSessionStore is a no-op in non-google-auth builds.
func SetGlobalSessionStore(s *session.Store) {}

// GetServerAuthInterceptor returns nil when compiled without Google Auth enforcement.
func GetServerAuthInterceptor() grpc.UnaryServerInterceptor {
	return nil
}

// GetServerStreamAuthInterceptor returns nil when compiled without Google Auth enforcement.
func GetServerStreamAuthInterceptor() grpc.StreamServerInterceptor {
	return nil
}
