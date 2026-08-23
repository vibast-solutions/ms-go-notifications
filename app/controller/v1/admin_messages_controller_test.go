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

// fakeAdminMessages is a programmable AdminMessages for controller tests.
type fakeAdminMessages struct {
	listResp   *dtov1.MessageListResponse
	listErr    error
	detail     *dtov1.AdminMessageResponse
	detailErr  error
	attachment *entity.MessageAttachment
	attachErr  error
	retryErr   error
	retryBy    string
}

func (f *fakeAdminMessages) ListMessages(context.Context, service.MessageListParams) (*dtov1.MessageListResponse, error) {
	return f.listResp, f.listErr
}
func (f *fakeAdminMessages) MessageDetail(context.Context, string) (*dtov1.AdminMessageResponse, error) {
	return f.detail, f.detailErr
}
func (f *fakeAdminMessages) Attachment(context.Context, string, string) (*entity.MessageAttachment, error) {
	return f.attachment, f.attachErr
}
func (f *fakeAdminMessages) Retry(_ context.Context, _, byName string) error {
	f.retryBy = byName
	return f.retryErr
}

func newAdminMessagesServer(svc AdminMessages, key *entity.APIKey) *echo.Echo {
	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	auth := middleware.NewAuth(&fakeKeys{key: key})
	ctl := NewAdminMessagesController(svc)
	admin := e.Group("/v1/admin")
	admin.GET("/messages", ctl.List, auth.Require("admin"))
	admin.GET("/messages/:id", ctl.Get, auth.Require("admin"))
	admin.GET("/messages/:id/attachments/:attachmentId", ctl.DownloadAttachment, auth.Require("admin"))
	admin.POST("/messages/:id/retry", ctl.Retry, auth.Require("admin"))
	return e
}

func TestAdminMessagesListHappy(t *testing.T) {
	svc := &fakeAdminMessages{listResp: &dtov1.MessageListResponse{
		Items:      []dtov1.MessageSummary{{ID: "01M", Status: "sent", From: dtov1.Address{Email: "x@vibast.ro"}, Subject: "Hi"}},
		NextCursor: "abc",
	}}
	e := newAdminMessagesServer(svc, adminKey())
	rec := doAdmin(e, http.MethodGet, "/v1/admin/messages", "", true)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"nextCursor":"abc"`)
	assert.Contains(t, rec.Body.String(), `"id":"01M"`)
}

func TestAdminMessagesListValidationError(t *testing.T) {
	svc := &fakeAdminMessages{listErr: service.NewValidationError(map[string]string{"status": "unknown status"})}
	e := newAdminMessagesServer(svc, adminKey())
	rec := doAdmin(e, http.MethodGet, "/v1/admin/messages?status=bogus", "", true)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), `"validation_error"`)
}

func TestAdminMessagesDetailHappy(t *testing.T) {
	msg := &entity.Message{
		ID: "01M", Status: "sent", Channel: "email", FromEmail: "x@vibast.ro", APIKeyID: "01KEY",
		Subject: "Hi", TextBody: strPtr("body"), CreatedAt: time.Now().UTC(),
	}
	detail := dtov1.NewAdminMessageResponse(msg, nil, nil, nil, "web")
	svc := &fakeAdminMessages{detail: &detail}
	e := newAdminMessagesServer(svc, adminKey())
	rec := doAdmin(e, http.MethodGet, "/v1/admin/messages/01M", "", true)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"text":"body"`)
	assert.Contains(t, rec.Body.String(), `"apiKey":{"id":"01KEY","name":"web"}`)
}

func TestAdminMessagesDetailNotFound(t *testing.T) {
	svc := &fakeAdminMessages{detailErr: service.ErrNotFound}
	e := newAdminMessagesServer(svc, adminKey())
	rec := doAdmin(e, http.MethodGet, "/v1/admin/messages/01M", "", true)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), `"not_found"`)
}

func TestAdminMessagesDownloadStreamsBytes(t *testing.T) {
	svc := &fakeAdminMessages{attachment: &entity.MessageAttachment{
		ID: "01A", Filename: "invoice.pdf", ContentType: "application/pdf", Content: []byte("PDFDATA"),
	}}
	e := newAdminMessagesServer(svc, adminKey())
	rec := doAdmin(e, http.MethodGet, "/v1/admin/messages/01M/attachments/01A", "", true)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "PDFDATA", rec.Body.String())
	assert.Equal(t, "application/pdf", rec.Header().Get("Content-Type"))
	assert.Equal(t, `attachment; filename="invoice.pdf"`, rec.Header().Get("Content-Disposition"))
}

func TestAdminMessagesDownloadPurgedGone(t *testing.T) {
	purgedAt := time.Now().UTC()
	svc := &fakeAdminMessages{attachment: &entity.MessageAttachment{
		ID: "01A", Filename: "old.pdf", ContentType: "application/pdf", Content: nil, PurgedAt: &purgedAt,
	}}
	e := newAdminMessagesServer(svc, adminKey())
	rec := doAdmin(e, http.MethodGet, "/v1/admin/messages/01M/attachments/01A", "", true)

	assert.Equal(t, http.StatusGone, rec.Code)
	assert.Contains(t, rec.Body.String(), `"gone"`)
}

func TestAdminMessagesDownloadNotFound(t *testing.T) {
	svc := &fakeAdminMessages{attachErr: service.ErrNotFound}
	e := newAdminMessagesServer(svc, adminKey())
	rec := doAdmin(e, http.MethodGet, "/v1/admin/messages/01M/attachments/01A", "", true)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestAdminMessagesRetryHappy(t *testing.T) {
	svc := &fakeAdminMessages{}
	e := newAdminMessagesServer(svc, adminKey())
	rec := doAdmin(e, http.MethodPost, "/v1/admin/messages/01M/retry", "", true)

	assert.Equal(t, http.StatusAccepted, rec.Code)
	assert.Equal(t, "ops", svc.retryBy, "retry is attributed to the calling key name")
}

func TestAdminMessagesRetryInvalidState(t *testing.T) {
	svc := &fakeAdminMessages{retryErr: service.ErrInvalidState}
	e := newAdminMessagesServer(svc, adminKey())
	rec := doAdmin(e, http.MethodPost, "/v1/admin/messages/01M/retry", "", true)

	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), `"invalid_state"`)
}

func TestAdminMessagesRetryNotFound(t *testing.T) {
	svc := &fakeAdminMessages{retryErr: service.ErrNotFound}
	e := newAdminMessagesServer(svc, adminKey())
	rec := doAdmin(e, http.MethodPost, "/v1/admin/messages/01M/retry", "", true)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestAdminMessagesRequireAdminScope(t *testing.T) {
	svc := &fakeAdminMessages{}
	e := newAdminMessagesServer(svc, sendKey())
	rec := doAdmin(e, http.MethodGet, "/v1/admin/messages", "", true)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestAdminMessagesRequireAuth(t *testing.T) {
	svc := &fakeAdminMessages{}
	e := newAdminMessagesServer(svc, adminKey())
	rec := doAdmin(e, http.MethodGet, "/v1/admin/messages", "", false)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func strPtr(s string) *string { return &s }
