package v1

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/assert"

	dtov1 "github.com/vibast-solutions/ms-go-notifications/app/dto/v1"
	"github.com/vibast-solutions/ms-go-notifications/app/entity"
	"github.com/vibast-solutions/ms-go-notifications/app/middleware"
	"github.com/vibast-solutions/ms-go-notifications/app/service"
)

type fakeAdminSuppressions struct {
	listResp  *dtov1.SuppressionListResponse
	addResp   *dtov1.SuppressionResponse
	addErr    error
	deleteErr error
	deleted   string
}

func (f *fakeAdminSuppressions) ListSuppressions(context.Context, service.SuppressionListParams) (*dtov1.SuppressionListResponse, error) {
	return f.listResp, nil
}
func (f *fakeAdminSuppressions) AddSuppression(context.Context, *dtov1.CreateSuppressionRequest) (*dtov1.SuppressionResponse, error) {
	return f.addResp, f.addErr
}
func (f *fakeAdminSuppressions) DeleteSuppression(_ context.Context, email string) error {
	f.deleted = email
	return f.deleteErr
}

func newAdminSuppressionsServer(svc AdminSuppressions, key *entity.APIKey) *echo.Echo {
	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	auth := middleware.NewAuth(&fakeKeys{key: key})
	ctl := NewAdminSuppressionsController(svc)
	admin := e.Group("/v1/admin")
	admin.GET("/suppressions", ctl.List, auth.Require("admin"))
	admin.POST("/suppressions", ctl.Create, auth.Require("admin"))
	admin.DELETE("/suppressions/:email", ctl.Delete, auth.Require("admin"))
	return e
}

func TestAdminSuppressionsListHappy(t *testing.T) {
	svc := &fakeAdminSuppressions{listResp: &dtov1.SuppressionListResponse{
		Items: []dtov1.SuppressionResponse{{Email: "a@example.com", Reason: "manual", CreatedAt: time.Now().UTC()}},
	}}
	e := newAdminSuppressionsServer(svc, adminKey())
	rec := doAdmin(e, http.MethodGet, "/v1/admin/suppressions", "", true)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"a@example.com"`)
}

func TestAdminSuppressionsCreateHappy(t *testing.T) {
	svc := &fakeAdminSuppressions{addResp: &dtov1.SuppressionResponse{Email: "a@example.com", Reason: "manual"}}
	e := newAdminSuppressionsServer(svc, adminKey())
	rec := doAdmin(e, http.MethodPost, "/v1/admin/suppressions", `{"email":"a@example.com"}`, true)

	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.Contains(t, rec.Body.String(), `"reason":"manual"`)
}

func TestAdminSuppressionsCreateConflict(t *testing.T) {
	svc := &fakeAdminSuppressions{addErr: service.ErrAlreadyExists}
	e := newAdminSuppressionsServer(svc, adminKey())
	rec := doAdmin(e, http.MethodPost, "/v1/admin/suppressions", `{"email":"a@example.com"}`, true)

	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), `"conflict"`)
}

func TestAdminSuppressionsCreateValidationError(t *testing.T) {
	svc := &fakeAdminSuppressions{addErr: service.NewValidationError(map[string]string{"email": "must be a valid email address"})}
	e := newAdminSuppressionsServer(svc, adminKey())
	rec := doAdmin(e, http.MethodPost, "/v1/admin/suppressions", `{"email":"bad"}`, true)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), `"validation_error"`)
}

func TestAdminSuppressionsDeleteHappy(t *testing.T) {
	svc := &fakeAdminSuppressions{}
	e := newAdminSuppressionsServer(svc, adminKey())
	rec := doAdmin(e, http.MethodDelete, "/v1/admin/suppressions/a@example.com", "", true)

	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, "a@example.com", svc.deleted)
}

func TestAdminSuppressionsDeleteNotFound(t *testing.T) {
	svc := &fakeAdminSuppressions{deleteErr: service.ErrNotFound}
	e := newAdminSuppressionsServer(svc, adminKey())
	rec := doAdmin(e, http.MethodDelete, "/v1/admin/suppressions/missing@example.com", "", true)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestAdminSuppressionsRequireAdminScope(t *testing.T) {
	e := newAdminSuppressionsServer(&fakeAdminSuppressions{}, sendKey())
	rec := doAdmin(e, http.MethodGet, "/v1/admin/suppressions", "", true)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}
