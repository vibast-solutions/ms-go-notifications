//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"

	authpb "github.com/vibast-solutions/ms-go-auth/app/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	defaultNotificationsCallerAPIKey   = "notifications-caller-key"
	defaultNotificationsNoAccessAPIKey = "notifications-no-access-key"
	defaultNotificationsAppAPIKey      = "notifications-app-api-key"
	notificationsAuthMockAddr          = "0.0.0.0:38082"
)

func notificationsCallerAPIKey() string {
	if value := strings.TrimSpace(os.Getenv("NOTIFICATIONS_CALLER_API_KEY")); value != "" {
		return value
	}
	return defaultNotificationsCallerAPIKey
}

func notificationsNoAccessAPIKey() string {
	if value := strings.TrimSpace(os.Getenv("NOTIFICATIONS_NO_ACCESS_API_KEY")); value != "" {
		return value
	}
	return defaultNotificationsNoAccessAPIKey
}

func notificationsAppAPIKey() string {
	if value := strings.TrimSpace(os.Getenv("NOTIFICATIONS_APP_API_KEY")); value != "" {
		return value
	}
	return defaultNotificationsAppAPIKey
}

type notificationsAuthGRPCServer struct {
	authpb.UnimplementedAuthServiceServer
}

func (s *notificationsAuthGRPCServer) ValidateInternalAccess(ctx context.Context, req *authpb.ValidateInternalAccessRequest) (*authpb.ValidateInternalAccessResponse, error) {
	if incomingNotificationsAPIKey(ctx) != notificationsAppAPIKey() {
		return nil, status.Error(codes.Unauthenticated, "unauthorized caller")
	}

	apiKey := strings.TrimSpace(req.GetApiKey())
	switch apiKey {
	case notificationsCallerAPIKey():
		return &authpb.ValidateInternalAccessResponse{
			ServiceName:   "notifications-gateway",
			AllowedAccess: []string{"notifications-service", "profile-service"},
		}, nil
	case notificationsNoAccessAPIKey():
		return &authpb.ValidateInternalAccessResponse{
			ServiceName:   "notifications-gateway",
			AllowedAccess: []string{"profile-service"},
		}, nil
	default:
		return nil, status.Error(codes.Unauthenticated, "invalid api key")
	}
}

func incomingNotificationsAPIKey(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	values := md.Get("x-api-key")
	if len(values) == 0 {
		return ""
	}
	return strings.TrimSpace(values[0])
}

func TestMain(m *testing.M) {
	if os.Getenv("NOTIFICATIONS_CALLER_API_KEY") == "" {
		_ = os.Setenv("NOTIFICATIONS_CALLER_API_KEY", defaultNotificationsCallerAPIKey)
	}
	if os.Getenv("NOTIFICATIONS_NO_ACCESS_API_KEY") == "" {
		_ = os.Setenv("NOTIFICATIONS_NO_ACCESS_API_KEY", defaultNotificationsNoAccessAPIKey)
	}
	if os.Getenv("NOTIFICATIONS_APP_API_KEY") == "" {
		_ = os.Setenv("NOTIFICATIONS_APP_API_KEY", defaultNotificationsAppAPIKey)
	}

	listener, err := net.Listen("tcp", notificationsAuthMockAddr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to start notifications auth grpc mock: %v\n", err)
		os.Exit(1)
	}

	grpcServer := grpc.NewServer()
	authpb.RegisterAuthServiceServer(grpcServer, &notificationsAuthGRPCServer{})

	go func() {
		_ = grpcServer.Serve(listener)
	}()

	exitCode := m.Run()

	grpcServer.GracefulStop()
	_ = listener.Close()

	os.Exit(exitCode)
}
