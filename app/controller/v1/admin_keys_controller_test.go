package v1

import (
	"context"
	"net/http"
	"testing"

	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/assert"

	"github.com/vibast-solutions/ms-go-notifications/app/entity"
	"github.com/vibast-solutions/ms-go-notifications/app/middleware"
	"github.com/vibast-solutions/ms-go-notifications/app/service"
)

// fakeAdminKeys is a programmable AdminKeys.
type fakeAdminKeys struct {
	generated  *entity.APIKey
	plaintext  string
	generalErr error
	list       []*entity.APIKey
	getKey     *entity.APIKey
	getErr     error
	replaceErr error
	replaced   []string
	revokeErr  error
	revokedID  string
}

func (f *fakeAdminKeys) Generate(_ context.Context, name string, scopes, domains []string) (string, *entity.APIKey, error) {
	if f.generalErr != nil {
		return "", nil, f.generalErr
	}
	return f.plaintext, f.generated, nil
}
func (f *fakeAdminKeys) List(context.Context) ([]*entity.APIKey, error) { return f.list, nil }
func (f *fakeAdminKeys) Get(context.Context, string) (*entity.APIKey, error) {
	return f.getKey, f.getErr
}
func (f *fakeAdminKeys) ReplaceDomains(_ context.Context, _ string, domains []string) error {
	f.replaced = domains
	return f.replaceErr
}
func (f *fakeAdminKeys) Revoke(_ context.Context, id string) error {
	f.revokedID = id
	return f.revokeErr
}

func newAdminKeysServer(svc AdminKeys, key *entity.APIKey) *echo.Echo {
	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	auth := middleware.NewAuth(&fakeKeys{key: key})
	ctl := NewAdminKeysController(svc)
	admin := e.Group("/v1/admin")
	admin.GET("/api-keys", ctl.List, auth.Require("admin"))
	admin.POST("/api-keys", ctl.Create, auth.Require("admin"))
	admin.PUT("/api-keys/:id/domains", ctl.ReplaceDomains, auth.Require("admin"))
	admin.DELETE("/api-keys/:id", ctl.Revoke, auth.Require("admin"))
	return e
}

func TestAdminKeysListNeverLeaksSecret(t *testing.T) {
	svc := &fakeAdminKeys{list: []*entity.APIKey{{
		ID: "01KEY", Name: "app", KeyPrefix: "abcd1234", KeyHash: "SECRETHASH",
		Scopes: []string{"send"}, Domains: []string{"vibast.ro"}, Status: "active",
	}}}
	e := newAdminKeysServer(svc, adminKey())
	rec := doAdmin(e, http.MethodGet, "/v1/admin/api-keys", "", true)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"prefix":"abcd1234"`)
	assert.NotContains(t, rec.Body.String(), "SECRETHASH")
}

func TestAdminKeysCreateReturnsPlaintextOnce(t *testing.T) {
	svc := &fakeAdminKeys{
		plaintext: "ntf_abcd1234_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdef",
		generated: &entity.APIKey{ID: "01NEW", Name: "app", KeyPrefix: "abcd1234", Scopes: []string{"send"}, Domains: []string{"vibast.ro"}, Status: "active"},
	}
	e := newAdminKeysServer(svc, adminKey())
	rec := doAdmin(e, http.MethodPost, "/v1/admin/api-keys", `{"name":"app","scopes":["send"],"domains":["vibast.ro"]}`, true)

	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.Contains(t, rec.Body.String(), `"key":"ntf_abcd1234_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdef"`)
}

func TestAdminKeysCreateDomainValidationError(t *testing.T) {
	svc := &fakeAdminKeys{generalErr: service.NewValidationError(map[string]string{"domains": "at least one domain is required for the send scope"})}
	e := newAdminKeysServer(svc, adminKey())
	rec := doAdmin(e, http.MethodPost, "/v1/admin/api-keys", `{"name":"app","scopes":["send"]}`, true)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), `"validation_error"`)
	assert.Contains(t, rec.Body.String(), "domains")
}

func TestAdminKeysReplaceDomainsHappy(t *testing.T) {
	svc := &fakeAdminKeys{getKey: &entity.APIKey{ID: "01KEY", Name: "app", Scopes: []string{"send"}, Domains: []string{"alextools.ro"}, Status: "active"}}
	e := newAdminKeysServer(svc, adminKey())
	rec := doAdmin(e, http.MethodPut, "/v1/admin/api-keys/01KEY/domains", `{"domains":["alextools.ro"]}`, true)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, []string{"alextools.ro"}, svc.replaced)
	assert.Contains(t, rec.Body.String(), `"alextools.ro"`)
}

func TestAdminKeysReplaceDomainsNotFound(t *testing.T) {
	svc := &fakeAdminKeys{replaceErr: service.ErrNotFound}
	e := newAdminKeysServer(svc, adminKey())
	rec := doAdmin(e, http.MethodPut, "/v1/admin/api-keys/01KEY/domains", `{"domains":["vibast.ro"]}`, true)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestAdminKeysRevokeHappy(t *testing.T) {
	svc := &fakeAdminKeys{}
	e := newAdminKeysServer(svc, adminKey())
	rec := doAdmin(e, http.MethodDelete, "/v1/admin/api-keys/01OTHER", "", true)

	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, "01OTHER", svc.revokedID)
}

func TestAdminKeysCannotRevokeItself(t *testing.T) {
	svc := &fakeAdminKeys{}
	// adminKey() has ID "01ADMIN"; revoking that id is a self-revoke.
	e := newAdminKeysServer(svc, adminKey())
	rec := doAdmin(e, http.MethodDelete, "/v1/admin/api-keys/01ADMIN", "", true)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), `"invalid_request"`)
	assert.Empty(t, svc.revokedID, "the service is never asked to revoke on a self-revoke")
}

func TestAdminKeysRevokeNotFound(t *testing.T) {
	svc := &fakeAdminKeys{revokeErr: service.ErrNotFound}
	e := newAdminKeysServer(svc, adminKey())
	rec := doAdmin(e, http.MethodDelete, "/v1/admin/api-keys/01OTHER", "", true)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestAdminKeysRequireAdminScope(t *testing.T) {
	e := newAdminKeysServer(&fakeAdminKeys{}, sendKey())
	rec := doAdmin(e, http.MethodGet, "/v1/admin/api-keys", "", true)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}
