package cmd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	authclient "github.com/vibast-solutions/lib-go-auth/client"
	authmiddleware "github.com/vibast-solutions/lib-go-auth/middleware"
	authservice "github.com/vibast-solutions/lib-go-auth/service"
	"github.com/vibast-solutions/ms-go-notifications/app/controller"
)

type notificationsInternalAuthClientStub struct{}

func (notificationsInternalAuthClientStub) ValidateInternalAccess(_ context.Context, req authclient.InternalAccessRequest) (authclient.InternalAccessResponse, error) {
	switch req.APIKey {
	case "valid-key":
		return authclient.InternalAccessResponse{
			ServiceName:   "caller-service",
			AllowedAccess: []string{"notifications-service"},
		}, nil
	case "no-access-key":
		return authclient.InternalAccessResponse{
			ServiceName:   "caller-service",
			AllowedAccess: []string{"profile-service"},
		}, nil
	default:
		return authclient.InternalAccessResponse{}, &authclient.APIError{StatusCode: http.StatusUnauthorized}
	}
}

func newNotificationsInternalAuthMiddlewareStub() *authmiddleware.EchoInternalAuthMiddleware {
	internalAuth := authservice.NewInternalAuthService(notificationsInternalAuthClientStub{})
	return authmiddleware.NewEchoInternalAuthMiddleware(internalAuth)
}

func newNotificationsTestServer() *http.Server {
	emailController := &controller.EmailController{}
	internalAuthMW := newNotificationsInternalAuthMiddlewareStub()
	e := setupHTTPServer(emailController, internalAuthMW, "notifications-service")
	return &http.Server{Handler: e}
}

func TestSetupHTTPServerHealthRouteUnauthorized(t *testing.T) {
	server := newNotificationsTestServer()

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d", rec.Code)
	}
}

func TestSetupHTTPServerHealthRouteForbidden(t *testing.T) {
	server := newNotificationsTestServer()

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("X-API-Key", "no-access-key")
	rec := httptest.NewRecorder()
	server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d", rec.Code)
	}
}

func TestSetupHTTPServerHealthRouteAuthorized(t *testing.T) {
	server := newNotificationsTestServer()

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("X-API-Key", "valid-key")
	rec := httptest.NewRecorder()
	server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"status":"ok"`) {
		t.Fatalf("unexpected health payload: %s", rec.Body.String())
	}
}
