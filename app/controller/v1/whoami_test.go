package v1

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/assert"

	"github.com/vibast-solutions/ms-go-notifications/app/entity"
	"github.com/vibast-solutions/ms-go-notifications/app/middleware"
	"github.com/vibast-solutions/ms-go-notifications/app/service"
)

const whoamiToken = "ntf_abcd1234_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdef"

// fakeKeys authenticates exactly one token to one key.
type fakeKeys struct {
	key *entity.APIKey
}

func (f *fakeKeys) Generate(context.Context, string, []string, []string) (string, *entity.APIKey, error) {
	panic("not used")
}

func (f *fakeKeys) Authenticate(_ context.Context, plaintext string) (*entity.APIKey, error) {
	if plaintext != whoamiToken {
		return nil, service.ErrInvalidAPIKey
	}
	return f.key, nil
}

func (f *fakeKeys) List(context.Context) ([]*entity.APIKey, error)          { return nil, nil }
func (f *fakeKeys) Revoke(context.Context, string) error                    { return nil }
func (f *fakeKeys) ReplaceDomains(context.Context, string, []string) error  { return nil }
func (f *fakeKeys) TouchLastUsed(context.Context, string) error             { return nil }

// newWhoamiServer wires /v1/whoami exactly like newServer does: behind
// auth.Require("") with the standard error handler.
func newWhoamiServer(key *entity.APIKey) *echo.Echo {
	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	auth := middleware.NewAuth(&fakeKeys{key: key})
	e.GET("/v1/whoami", NewWhoamiController().Whoami, auth.Require(""))
	return e
}

func TestWhoamiWithValidKey(t *testing.T) {
	e := newWhoamiServer(&entity.APIKey{
		ID:      "01KEY",
		Name:    "smoke",
		Scopes:  []string{"send"},
		Status:  entity.APIKeyStatusActive,
		Domains: []string{"vibast.com", "alextools.ro"},
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/whoami", nil)
	req.Header.Set("Authorization", "Bearer "+whoamiToken)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t,
		`{"id":"01KEY","name":"smoke","scopes":["send"],"domains":["vibast.com","alextools.ro"]}`,
		rec.Body.String())
}

func TestWhoamiAdminKeyHasEmptyDomains(t *testing.T) {
	e := newWhoamiServer(&entity.APIKey{
		ID:     "01ADMIN",
		Name:   "ops",
		Scopes: []string{"admin"},
		Status: entity.APIKeyStatusActive,
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/whoami", nil)
	req.Header.Set("Authorization", "Bearer "+whoamiToken)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t,
		`{"id":"01ADMIN","name":"ops","scopes":["admin"],"domains":[]}`,
		rec.Body.String())
}

func TestWhoamiWithoutKey401(t *testing.T) {
	e := newWhoamiServer(&entity.APIKey{ID: "01KEY"})

	req := httptest.NewRequest(http.MethodGet, "/v1/whoami", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.JSONEq(t,
		`{"error":"unauthorized","message":"missing or malformed Authorization header"}`,
		rec.Body.String())
}
