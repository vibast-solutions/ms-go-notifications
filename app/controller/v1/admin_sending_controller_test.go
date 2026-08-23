package v1

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/assert"

	"github.com/vibast-solutions/ms-go-notifications/app/entity"
	"github.com/vibast-solutions/ms-go-notifications/app/middleware"
	"github.com/vibast-solutions/ms-go-notifications/app/service"
)

var fixedSince = time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)

// fakeBreaker is a stateful SendingBreaker: Pause/Resume mutate the state the
// controller re-reads.
type fakeBreaker struct {
	state        service.SendingState
	pausedReason string
	pausedBy     string
	resumedBy    string
}

func (f *fakeBreaker) State(context.Context) (service.SendingState, error) { return f.state, nil }

func (f *fakeBreaker) Pause(_ context.Context, reason, by string) error {
	f.pausedReason, f.pausedBy = reason, by
	f.state = service.SendingState{Paused: true, Reason: reason, Since: fixedSince, By: by}
	return nil
}

func (f *fakeBreaker) Resume(_ context.Context, by string) error {
	f.resumedBy = by
	f.state = service.SendingState{Paused: false}
	return nil
}

func newAdminSendingServer(breaker SendingBreaker, key *entity.APIKey) *echo.Echo {
	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	auth := middleware.NewAuth(&fakeKeys{key: key})
	ctl := NewAdminSendingController(breaker)
	admin := e.Group("/v1/admin")
	admin.GET("/sending", ctl.Get, auth.Require("admin"))
	admin.POST("/sending/pause", ctl.Pause, auth.Require("admin"))
	admin.POST("/sending/resume", ctl.Resume, auth.Require("admin"))
	return e
}

func adminKey() *entity.APIKey {
	return &entity.APIKey{ID: "01ADMIN", Name: "ops", Scopes: []string{"admin"}, Status: entity.APIKeyStatusActive}
}

func sendKey() *entity.APIKey {
	return &entity.APIKey{ID: "01SEND", Name: "app", Scopes: []string{"send"}, Status: entity.APIKeyStatusActive, Domains: []string{"vibast.ro"}}
}

func doAdmin(e *echo.Echo, method, path, body string, withAuth bool) *httptest.ResponseRecorder {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	if withAuth {
		req.Header.Set("Authorization", "Bearer "+whoamiToken)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func TestAdminSendingGetRunning(t *testing.T) {
	e := newAdminSendingServer(&fakeBreaker{}, adminKey())
	rec := doAdmin(e, http.MethodGet, "/v1/admin/sending", "", true)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"paused":false,"reason":"","since":null,"by":""}`, rec.Body.String())
}

func TestAdminSendingGetPaused(t *testing.T) {
	breaker := &fakeBreaker{state: service.SendingState{Paused: true, Reason: "auto: bounce 5% >= 4%", Since: fixedSince, By: "auto"}}
	e := newAdminSendingServer(breaker, adminKey())
	rec := doAdmin(e, http.MethodGet, "/v1/admin/sending", "", true)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t,
		`{"paused":true,"reason":"auto: bounce 5% >= 4%","since":"2026-08-23T12:00:00Z","by":"auto"}`,
		rec.Body.String())
}

func TestAdminSendingPauseWithReason(t *testing.T) {
	breaker := &fakeBreaker{}
	e := newAdminSendingServer(breaker, adminKey())
	rec := doAdmin(e, http.MethodPost, "/v1/admin/sending/pause", `{"reason":"maintenance"}`, true)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t,
		`{"paused":true,"reason":"maintenance","since":"2026-08-23T12:00:00Z","by":"ops"}`,
		rec.Body.String())
	assert.Equal(t, "maintenance", breaker.pausedReason)
	assert.Equal(t, "ops", breaker.pausedBy, "by is the calling key name")
}

func TestAdminSendingPauseDefaultsReason(t *testing.T) {
	breaker := &fakeBreaker{}
	e := newAdminSendingServer(breaker, adminKey())
	rec := doAdmin(e, http.MethodPost, "/v1/admin/sending/pause", "", true)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, defaultManualPauseReason, breaker.pausedReason, "missing reason defaults to manual")
}

func TestAdminSendingResume(t *testing.T) {
	breaker := &fakeBreaker{state: service.SendingState{Paused: true, Reason: "manual", Since: fixedSince, By: "ops"}}
	e := newAdminSendingServer(breaker, adminKey())
	rec := doAdmin(e, http.MethodPost, "/v1/admin/sending/resume", "", true)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"paused":false,"reason":"","since":null,"by":""}`, rec.Body.String())
	assert.Equal(t, "ops", breaker.resumedBy)
}

func TestAdminSendingRequiresAdminScope(t *testing.T) {
	e := newAdminSendingServer(&fakeBreaker{}, sendKey())

	for _, tc := range []struct {
		method, path, body string
	}{
		{http.MethodGet, "/v1/admin/sending", ""},
		{http.MethodPost, "/v1/admin/sending/pause", `{"reason":"x"}`},
		{http.MethodPost, "/v1/admin/sending/resume", ""},
	} {
		rec := doAdmin(e, tc.method, tc.path, tc.body, true)
		assert.Equal(t, http.StatusForbidden, rec.Code, "%s %s with a send-only key → 403", tc.method, tc.path)
	}
}

func TestAdminSendingRequiresAuth(t *testing.T) {
	e := newAdminSendingServer(&fakeBreaker{}, adminKey())
	rec := doAdmin(e, http.MethodGet, "/v1/admin/sending", "", false)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}
