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
	"github.com/stretchr/testify/require"

	dtov1 "github.com/vibast-solutions/ms-go-notifications/app/dto/v1"
	"github.com/vibast-solutions/ms-go-notifications/app/entity"
	"github.com/vibast-solutions/ms-go-notifications/app/middleware"
	"github.com/vibast-solutions/ms-go-notifications/app/service"
)

// fakeEmails is a configurable EmailAccepter for controller tests.
type fakeEmails struct {
	acceptFn func(*entity.APIKey, *dtov1.SendEmailRequest) (*entity.Message, bool, error)
	getFn    func(id, apiKeyID string) (*entity.Message, []*entity.MessageRecipient, []*entity.MessageEvent, error)
}

func (f *fakeEmails) Accept(_ context.Context, key *entity.APIKey, req *dtov1.SendEmailRequest) (*entity.Message, bool, error) {
	return f.acceptFn(key, req)
}

func (f *fakeEmails) GetForAPIKey(_ context.Context, id, apiKeyID string) (*entity.Message, []*entity.MessageRecipient, []*entity.MessageEvent, error) {
	return f.getFn(id, apiKeyID)
}

func sendableKey() *entity.APIKey {
	return &entity.APIKey{ID: "01APIKEY", Name: "app", Scopes: []string{"send"}, Domains: []string{"vibast.ro"},
		Status: entity.APIKeyStatusActive}
}

// newEmailsServer wires /v1/emails exactly like newServer: behind
// auth.Require("send") with the standard error handler.
func newEmailsServer(key *entity.APIKey, emails EmailAccepter) *echo.Echo {
	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	auth := middleware.NewAuth(&fakeKeys{key: key})
	ctl := NewEmailsController(emails)
	g := e.Group("/v1")
	g.POST("/emails", ctl.Send, auth.Require("send"))
	g.GET("/emails/:id", ctl.Get, auth.Require("send"))
	return e
}

func postEmails(e *echo.Echo, body string, auth bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/emails", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	if auth {
		req.Header.Set("Authorization", "Bearer "+whoamiToken)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

const validBody = `{"from":"no-reply@vibast.ro","to":["jane@example.com"],"subject":"Hi","text":"hello"}`

func TestSend202OnCreate(t *testing.T) {
	created := &entity.Message{ID: "01NEW", Status: entity.MessageStatusQueued,
		CreatedAt: time.Date(2026, 8, 22, 10, 0, 0, 0, time.UTC)}
	e := newEmailsServer(sendableKey(), &fakeEmails{
		acceptFn: func(*entity.APIKey, *dtov1.SendEmailRequest) (*entity.Message, bool, error) {
			return created, true, nil
		},
	})

	rec := postEmails(e, validBody, true)
	assert.Equal(t, http.StatusAccepted, rec.Code)
	assert.JSONEq(t, `{"id":"01NEW","status":"queued","createdAt":"2026-08-22T10:00:00Z"}`, rec.Body.String())
}

func TestSend200OnIdempotentReplay(t *testing.T) {
	existing := &entity.Message{ID: "01OLD", Status: entity.MessageStatusQueued,
		CreatedAt: time.Date(2026, 8, 22, 10, 0, 0, 0, time.UTC)}
	e := newEmailsServer(sendableKey(), &fakeEmails{
		acceptFn: func(*entity.APIKey, *dtov1.SendEmailRequest) (*entity.Message, bool, error) {
			return existing, false, nil
		},
	})

	rec := postEmails(e, validBody, true)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"id":"01OLD","status":"queued","createdAt":"2026-08-22T10:00:00Z"}`, rec.Body.String())
}

func TestSend400ValidationError(t *testing.T) {
	e := newEmailsServer(sendableKey(), &fakeEmails{
		acceptFn: func(*entity.APIKey, *dtov1.SendEmailRequest) (*entity.Message, bool, error) {
			return nil, false, service.NewValidationError(map[string]string{"subject": "is required"})
		},
	})

	rec := postEmails(e, validBody, true)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.JSONEq(t,
		`{"error":"validation_error","message":"one or more fields are invalid","fields":{"subject":"is required"}}`,
		rec.Body.String())
}

func TestSend403DomainNotAllowed(t *testing.T) {
	e := newEmailsServer(sendableKey(), &fakeEmails{
		acceptFn: func(*entity.APIKey, *dtov1.SendEmailRequest) (*entity.Message, bool, error) {
			return nil, false, service.ErrDomainNotAllowed
		},
	})

	rec := postEmails(e, validBody, true)
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), `"error":"domain_not_allowed"`)
}

func TestSend413PayloadTooLarge(t *testing.T) {
	e := newEmailsServer(sendableKey(), &fakeEmails{
		acceptFn: func(*entity.APIKey, *dtov1.SendEmailRequest) (*entity.Message, bool, error) {
			return nil, false, service.ErrPayloadTooLarge
		},
	})

	rec := postEmails(e, validBody, true)
	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	assert.Contains(t, rec.Body.String(), `"error":"payload_too_large"`)
}

func TestSend401WithoutKey(t *testing.T) {
	e := newEmailsServer(sendableKey(), &fakeEmails{
		acceptFn: func(*entity.APIKey, *dtov1.SendEmailRequest) (*entity.Message, bool, error) {
			return nil, false, nil
		},
	})

	rec := postEmails(e, validBody, false)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestGet200HidesBodies(t *testing.T) {
	msg := &entity.Message{
		ID: "01MSG", Status: entity.MessageStatusSent, Channel: entity.ChannelEmail,
		FromEmail: "no-reply@vibast.ro", Subject: "Hi",
		TextBody: strptr("secret text"), HTMLBody: strptr("<p>secret</p>"),
		CreatedAt: time.Date(2026, 8, 22, 10, 0, 0, 0, time.UTC),
	}
	recipients := []*entity.MessageRecipient{{Type: "to", Email: "jane@example.com", Status: "delivered"}}
	events := []*entity.MessageEvent{{Type: entity.EventTypeQueued, OccurredAt: msg.CreatedAt}}

	e := newEmailsServer(sendableKey(), &fakeEmails{
		getFn: func(id, apiKeyID string) (*entity.Message, []*entity.MessageRecipient, []*entity.MessageEvent, error) {
			return msg, recipients, events, nil
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/emails/01MSG", nil)
	req.Header.Set("Authorization", "Bearer "+whoamiToken)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, `"id":"01MSG"`)
	assert.Contains(t, body, `"recipients"`)
	assert.Contains(t, body, `"events"`)
	assert.NotContains(t, body, "secret text", "text body must never be returned")
	assert.NotContains(t, body, "secret</p>", "html body must never be returned")
}

func TestGet404NotFound(t *testing.T) {
	e := newEmailsServer(sendableKey(), &fakeEmails{
		getFn: func(id, apiKeyID string) (*entity.Message, []*entity.MessageRecipient, []*entity.MessageEvent, error) {
			return nil, nil, nil, service.ErrNotFound
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/emails/01MISSING", nil)
	req.Header.Set("Authorization", "Bearer "+whoamiToken)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), `"error":"not_found"`)
}

func strptr(s string) *string { return &s }
