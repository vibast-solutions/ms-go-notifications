package v1

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vibast-solutions/ms-go-notifications/app/middleware"
	"github.com/vibast-solutions/ms-go-notifications/pkg/provider/ses"
	"github.com/vibast-solutions/ms-go-notifications/pkg/sns"
)

// --- fakes -----------------------------------------------------------------

type fakeVerifier struct {
	verifyErr  error
	confirmErr error
	confirmed  bool
}

func (f *fakeVerifier) Verify(context.Context, *sns.Envelope) error { return f.verifyErr }
func (f *fakeVerifier) ConfirmSubscription(context.Context, *sns.Envelope) error {
	f.confirmed = true
	return f.confirmErr
}

type fakeTopics struct{ allow bool }

func (f fakeTopics) Allows(string) bool { return f.allow }

type fakeHandler struct {
	handled bool
	gotSNS  string
	gotType string
	err     error
}

func (f *fakeHandler) HandleSESEvent(_ context.Context, snsMessageID string, ev *ses.Event) error {
	f.handled = true
	f.gotSNS = snsMessageID
	f.gotType = ev.Type
	return f.err
}

// --- helpers ---------------------------------------------------------------

func newWebhookServer(v SNSVerifier, topics TopicAllowList, h SESEventHandler) *echo.Echo {
	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	logger := log.New()
	logger.SetOutput(io.Discard)
	ctl := NewWebhooksController(v, topics, h, logger)
	e.POST("/v1/webhooks/ses", ctl.SES)
	return e
}

func postWebhook(e *echo.Echo, msgType, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/ses", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	if msgType != "" {
		req.Header.Set("x-amz-sns-message-type", msgType)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

const notificationBody = `{"Type":"Notification","MessageId":"sns-abc","TopicArn":"arn:aws:sns:us-east-1:1:ses",
  "Message":"{\"eventType\":\"Delivery\",\"mail\":{\"messageId\":\"p1\",\"destination\":[\"j@example.com\"]},\"delivery\":{\"recipients\":[\"j@example.com\"]}}"}`

const subConfirmBody = `{"Type":"SubscriptionConfirmation","MessageId":"sns-sub","TopicArn":"arn:aws:sns:us-east-1:1:ses",
  "SubscribeURL":"https://sns.us-east-1.amazonaws.com/?Action=ConfirmSubscription","Token":"tok"}`

// --- tests -----------------------------------------------------------------

func TestWebhookSubscriptionConfirmation(t *testing.T) {
	v := &fakeVerifier{}
	h := &fakeHandler{}
	e := newWebhookServer(v, fakeTopics{allow: true}, h)

	rec := postWebhook(e, sns.TypeSubscriptionConfirmation, subConfirmBody)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, v.confirmed, "subscription must be auto-confirmed")
	assert.False(t, h.handled)
}

func TestWebhookNotification(t *testing.T) {
	v := &fakeVerifier{}
	h := &fakeHandler{}
	e := newWebhookServer(v, fakeTopics{allow: true}, h)

	rec := postWebhook(e, sns.TypeNotification, notificationBody)

	assert.Equal(t, http.StatusOK, rec.Code)
	require.True(t, h.handled, "notification must be handled")
	assert.Equal(t, "sns-abc", h.gotSNS)
	assert.Equal(t, ses.EventDelivery, h.gotType)
}

func TestWebhookUnsubscribeConfirmation(t *testing.T) {
	e := newWebhookServer(&fakeVerifier{}, fakeTopics{allow: true}, &fakeHandler{})

	body := `{"Type":"UnsubscribeConfirmation","MessageId":"u","TopicArn":"arn:aws:sns:us-east-1:1:ses",
	  "SubscribeURL":"https://sns.us-east-1.amazonaws.com/?Action=ConfirmSubscription","Token":"t"}`
	rec := postWebhook(e, sns.TypeUnsubscribeConfirmation, body)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestWebhookDisallowedTopicForbidden(t *testing.T) {
	v := &fakeVerifier{}
	e := newWebhookServer(v, fakeTopics{allow: false}, &fakeHandler{})

	rec := postWebhook(e, sns.TypeNotification, notificationBody)

	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "forbidden")
}

func TestWebhookBadSignatureForbidden(t *testing.T) {
	v := &fakeVerifier{verifyErr: sns.ErrBadSignature}
	h := &fakeHandler{}
	e := newWebhookServer(v, fakeTopics{allow: true}, h)

	rec := postWebhook(e, sns.TypeNotification, notificationBody)

	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.False(t, h.handled, "an unverified message must not be handled")
}

func TestWebhookMalformedBadRequest(t *testing.T) {
	e := newWebhookServer(&fakeVerifier{}, fakeTopics{allow: true}, &fakeHandler{})

	rec := postWebhook(e, sns.TypeNotification, `{not json`)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestWebhookUnknownTypeBadRequest(t *testing.T) {
	// A syntactically valid envelope whose Type is not one of the three known
	// values is rejected at parse time (400).
	e := newWebhookServer(&fakeVerifier{}, fakeTopics{allow: true}, &fakeHandler{})

	body := `{"Type":"Nonsense","MessageId":"m","TopicArn":"arn:aws:sns:us-east-1:1:ses"}`
	rec := postWebhook(e, "Nonsense", body)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestWebhookBodyTooLarge(t *testing.T) {
	e := newWebhookServer(&fakeVerifier{}, fakeTopics{allow: true}, &fakeHandler{})

	big := `{"Type":"Notification","MessageId":"m","TopicArn":"arn","Message":"` +
		strings.Repeat("a", maxSNSBodyBytes+10) + `"}`
	rec := postWebhook(e, sns.TypeNotification, big)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestWebhookUnknownMessageIDStill200(t *testing.T) {
	// The handler returns nil for an unknown provider message id (logged, dropped).
	v := &fakeVerifier{}
	h := &fakeHandler{} // err == nil
	e := newWebhookServer(v, fakeTopics{allow: true}, h)

	rec := postWebhook(e, sns.TypeNotification, notificationBody)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, h.handled)
}

func TestWebhookHandlerErrorReturns500(t *testing.T) {
	// An infrastructure failure while handling a verified notification must
	// surface as 5xx so SNS retries.
	v := &fakeVerifier{}
	h := &fakeHandler{err: errors.New("db down")}
	e := newWebhookServer(v, fakeTopics{allow: true}, h)

	rec := postWebhook(e, sns.TypeNotification, notificationBody)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestWebhookConfirmFailureReturns500(t *testing.T) {
	v := &fakeVerifier{confirmErr: errors.New("subscribe url unreachable")}
	e := newWebhookServer(v, fakeTopics{allow: true}, &fakeHandler{})

	rec := postWebhook(e, sns.TypeSubscriptionConfirmation, subConfirmBody)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestWebhookUnparseableSESEventStill200(t *testing.T) {
	// Verified notification whose inner Message is not a valid SES event: logged
	// and dropped with 200 (retrying would never help).
	v := &fakeVerifier{}
	h := &fakeHandler{}
	e := newWebhookServer(v, fakeTopics{allow: true}, h)

	body := `{"Type":"Notification","MessageId":"m","TopicArn":"arn:aws:sns:us-east-1:1:ses","Message":"{not-json"}`
	rec := postWebhook(e, sns.TypeNotification, body)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.False(t, h.handled)
}
