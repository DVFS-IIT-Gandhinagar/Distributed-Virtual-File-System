//go:build use_google_auth

package fileserver

import (
	"context"
	"os"
	"strings"
	"sync"
	"time"

	pb "github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/api/fileserver"
	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/auth"
	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/fileserver/session"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

type authContextKey struct{}

// AuthenticatedServerStream wraps grpc.ServerStream to attach verified identity context.
type AuthenticatedServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *AuthenticatedServerStream) Context() context.Context {
	return s.ctx
}

var (
	globalSessionStore   *session.Store
	globalSessionStoreMu sync.RWMutex
)

// SetGlobalSessionStore sets the session store used by auth interceptors.
func SetGlobalSessionStore(s *session.Store) {
	globalSessionStoreMu.Lock()
	globalSessionStore = s
	globalSessionStoreMu.Unlock()
}

func getGlobalSessionStore() *session.Store {
	globalSessionStoreMu.RLock()
	defer globalSessionStoreMu.RUnlock()
	return globalSessionStore
}

// GetServerAuthInterceptor enforces Google OAuth handshake on RegisterClient and SST on subsequent RPCs.
func GetServerAuthInterceptor() grpc.UnaryServerInterceptor {
	cfg := auth.LoadDesktopConfig()

	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return nil, status.Errorf(codes.Unauthenticated, "missing authorization metadata in request")
		}

		// Allow administrative password-authenticated calls to SetQuota to proceed to handler
		// where GRPCHandler.SetQuota performs constant-time ADMIN_PASSWORD_HASH verification.
		if info.FullMethod == "/fileserver.FileServer/SetQuota" {
			if len(md.Get("x-admin-password-hash")) > 0 || len(md.Get("x-admin-password")) > 0 {
				return handler(ctx, req)
			}
		}

		authHeaders := md.Get("authorization")
		if len(authHeaders) == 0 || !strings.HasPrefix(authHeaders[0], "Bearer ") {
			return nil, status.Errorf(codes.Unauthenticated, "missing or malformed Bearer authorization header")
		}

		rawToken := strings.TrimPrefix(authHeaders[0], "Bearer ")

		// 1. One-Time Handshake: RegisterClient verifies Google ID token
		if info.FullMethod == "/fileserver.FileServer/RegisterClient" {
			expectedUser := ""
			if r, ok := req.(*pb.RegisterClientRequest); ok {
				expectedUser = r.Username
			}
			claims, err := auth.VerifyToken(ctx, rawToken, expectedUser, cfg.ClientID)
			if err != nil {
				return nil, status.Errorf(codes.Unauthenticated, "google handshake authentication failed: %v", err)
			}
			ctx = context.WithValue(ctx, authContextKey{}, claims.Email)
			return handler(ctx, req)
		}

		// 2. High-Speed Session Check on all other RPCs
		store := getGlobalSessionStore()
		if store == nil {
			return handler(ctx, req)
		}

		peerIP := ""
		if p, ok := peer.FromContext(ctx); ok && p.Addr != nil {
			peerIP = p.Addr.String()
		}

		sess, err := store.ValidateSession(rawToken, peerIP)
		if err != nil {
			switch err {
			case session.ErrSessionExpired:
				return nil, status.Errorf(codes.Unauthenticated, "session has expired; please reconnect")
			case session.ErrIPMismatch:
				return nil, status.Errorf(codes.PermissionDenied, "session transport violation: client IP mismatch")
			default:
				// Fallback only allowed if explicitly enabled for legacy test suites
				if os.Getenv("DVFS_ALLOW_LEGACY_TOKEN_FALLBACK") == "true" || os.Getenv("DVFS_ALLOW_LEGACY_TOKEN_FALLBACK") == "1" {
					claims, gErr := auth.VerifyToken(ctx, rawToken, "", cfg.ClientID)
					if gErr == nil && claims != nil {
						ctx = context.WithValue(ctx, authContextKey{}, claims.Email)
						newSess, _ := store.CreateSession(rawToken, claims.Email, "", peerIP, "", "", time.Now().Add(1*time.Hour))
						if newSess != nil {
							ctx = session.WithSession(ctx, newSess)
						}
						return handler(ctx, req)
					}
				}
				return nil, status.Errorf(codes.Unauthenticated, "invalid session token: %v", err)
			}
		}

		ctx = session.WithSession(ctx, sess)
		ctx = context.WithValue(ctx, authContextKey{}, sess.Username)
		return handler(ctx, req)
	}
}

// GetServerStreamAuthInterceptor enforces SST verification on streaming RPCs (UploadFile, DownloadFile).
func GetServerStreamAuthInterceptor() grpc.StreamServerInterceptor {
	cfg := auth.LoadDesktopConfig()

	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx := ss.Context()
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return status.Errorf(codes.Unauthenticated, "missing authorization metadata in stream request")
		}

		authHeaders := md.Get("authorization")
		if len(authHeaders) == 0 || !strings.HasPrefix(authHeaders[0], "Bearer ") {
			return status.Errorf(codes.Unauthenticated, "missing or malformed Bearer authorization header in stream")
		}

		rawToken := strings.TrimPrefix(authHeaders[0], "Bearer ")
		store := getGlobalSessionStore()
		if store == nil {
			return handler(srv, ss)
		}

		peerIP := ""
		if p, ok := peer.FromContext(ctx); ok && p.Addr != nil {
			peerIP = p.Addr.String()
		}

		sess, err := store.ValidateSession(rawToken, peerIP)
		if err != nil {
			switch err {
			case session.ErrSessionExpired:
				return status.Errorf(codes.Unauthenticated, "stream session has expired; please reconnect")
			case session.ErrIPMismatch:
				return status.Errorf(codes.PermissionDenied, "stream transport violation: client IP mismatch")
			default:
				// Fallback only allowed if explicitly enabled for legacy test suites
				if os.Getenv("DVFS_ALLOW_LEGACY_TOKEN_FALLBACK") == "true" || os.Getenv("DVFS_ALLOW_LEGACY_TOKEN_FALLBACK") == "1" {
					claims, gErr := auth.VerifyToken(ctx, rawToken, "", cfg.ClientID)
					if gErr == nil && claims != nil {
						ctx = context.WithValue(ctx, authContextKey{}, claims.Email)
						newSess, _ := store.CreateSession(rawToken, claims.Email, "", peerIP, "", "", time.Now().Add(1*time.Hour))
						if newSess != nil {
							ctx = session.WithSession(ctx, newSess)
						}
						wrappedStream := &AuthenticatedServerStream{
							ServerStream: ss,
							ctx:          ctx,
						}
						return handler(srv, wrappedStream)
					}
				}
				return status.Errorf(codes.Unauthenticated, "stream session validation failed: %v", err)
			}
		}

		ctx = session.WithSession(ctx, sess)
		ctx = context.WithValue(ctx, authContextKey{}, sess.Username)
		wrappedStream := &AuthenticatedServerStream{
			ServerStream: ss,
			ctx:          ctx,
		}
		return handler(srv, wrappedStream)
	}
}
