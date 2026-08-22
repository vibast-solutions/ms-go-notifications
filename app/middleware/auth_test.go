package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vibast-solutions/ms-go-notifications/app/entity"
	"github.com/vibast-solutions/ms-go-notifications/app/service"
)

const testToken = "ntf_abcd1234_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdef"

// fakeKeys implements service.APIKeys for middleware tests.
type fakeKeys struct {
	key        *entity.APIKey
	authErr    error
	touchCalls int
}

func (f *fakeKeys) Generate(context.Context, string, []string, []string) (string, *entity.APIKey, error) {
	panic("not used")
}

func (f *fakeKeys) Authenticate(_ context.Context, plaintext string) (*entity.APIKey, error) {
	if f.authErr != nil {
		return nil, f.authErr
	}
	if plaintext != testToken {
		return nil, service.ErrInvalidAPIKey
	}
	return f.key, nil
}

func (f *fakeKeys) List(context.Context) ([]*entity.APIKey, error) { return nil, nil }
func (f *fakeKeys) Revoke(context.Context, string) error           { return nil }
func (f *fakeKeys) ReplaceDomains(context.Context, string, []string) error {
	return nil
}
func (f *fakeKeys) TouchLastUsed(context.Context, string) error {
	f.touchCalls++
	return nil
}

func sendKey() *entity.APIKey {
	return &entity.APIKey{
		ID:      "01TESTKEYID",
		Name:    "test",
		Scopes:  []string{entity.ScopeSend},
		Status:  entity.APIKeyStatusActive,
		Domains: []string{"vibast.com"},
	}
}

// newAuthServer wires a minimal Echo app with the real error handler so
// responses carry the standard envelope.
func newAuthServer(keys service.APIKeys, scope string) (*echo.Echo, *Auth) {
	e := echo.New()
	e.HTTPErrorHandler = ErrorHandler
	auth := NewAuth(keys)
	e.GET("/protected", func(c *echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"key": GetAPIKey(c).ID})
	}, auth.Require(scope))
	return e, auth
}

func doRequest(e *echo.Echo, authorization string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func TestRequireHappyPath(t *testing.T) {
	keys := &fakeKeys{key: sendKey()}
	e, _ := newAuthServer(keys, entity.ScopeSend)

	rec := doRequest(e, "Bearer "+testToken)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"key":"01TESTKEYID"}`, rec.Body.String())
	assert.Equal(t, 1, keys.touchCalls)
}

func TestRequireMissingHeader401(t *testing.T) {
	e, _ := newAuthServer(&fakeKeys{key: sendKey()}, entity.ScopeSend)

	rec := doRequest(e, "")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.JSONEq(t, `{"error":"unauthorized","message":"missing or malformed Authorization header"}`, rec.Body.String())
}

func TestRequireBadToken401(t *testing.T) {
	e, _ := newAuthServer(&fakeKeys{key: sendKey()}, entity.ScopeSend)

	rec := doRequest(e, "Bearer wrong-token")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.JSONEq(t, `{"error":"unauthorized","message":"invalid API key"}`, rec.Body.String())
}

func TestRequireRevokedKey401(t *testing.T) {
	e, _ := newAuthServer(&fakeKeys{authErr: service.ErrAPIKeyRevoked}, entity.ScopeSend)

	rec := doRequest(e, "Bearer "+testToken)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.JSONEq(t, `{"error":"unauthorized","message":"invalid API key"}`, rec.Body.String())
}

func TestRequireMissingScope403(t *testing.T) {
	e, _ := newAuthServer(&fakeKeys{key: sendKey()}, entity.ScopeAdmin)

	rec := doRequest(e, "Bearer "+testToken)
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.JSONEq(t, `{"error":"forbidden","message":"API key lacks the required scope: admin"}`, rec.Body.String())
}

func TestRequireEmptyScopeAuthenticatesOnly(t *testing.T) {
	e, _ := newAuthServer(&fakeKeys{key: sendKey()}, "")

	rec := doRequest(e, "Bearer "+testToken)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestLastUsedThrottling(t *testing.T) {
	keys := &fakeKeys{key: sendKey()}
	e, auth := newAuthServer(keys, entity.ScopeSend)

	now := time.Now()
	auth.now = func() time.Time { return now }

	for i := 0; i < 3; i++ {
		rec := doRequest(e, "Bearer "+testToken)
		require.Equal(t, http.StatusOK, rec.Code)
	}
	assert.Equal(t, 1, keys.touchCalls, "repeat requests within a minute must not touch again")

	now = now.Add(61 * time.Second)
	rec := doRequest(e, "Bearer "+testToken)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, 2, keys.touchCalls, "a minute later the key is touched again")
}

func TestBearerToken(t *testing.T) {
	cases := []struct {
		header string
		token  string
		ok     bool
	}{
		{"Bearer abc", "abc", true},
		{"bearer abc", "abc", true},
		{"Bearer   abc  ", "abc", true},
		{"Bearer ", "", false},
		{"Basic abc", "", false},
		{"abc", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		token, ok := bearerToken(tc.header)
		assert.Equal(t, tc.ok, ok, "header %q", tc.header)
		assert.Equal(t, tc.token, token, "header %q", tc.header)
	}
}
