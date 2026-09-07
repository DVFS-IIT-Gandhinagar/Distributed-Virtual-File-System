//go:build !use_google_auth

package fileserver

import "google.golang.org/grpc"

// GetServerAuthInterceptor returns nil when compiled without Google Auth enforcement.
func GetServerAuthInterceptor() grpc.UnaryServerInterceptor {
	return nil
}
