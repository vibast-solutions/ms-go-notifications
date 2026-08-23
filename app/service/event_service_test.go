package service

import (
	"context"
	"database/sql"
	"io"
	"testing"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vibast-solutions/ms-go-notifications/app/entity"
	"github.com/vibast-solutions/ms-go-notifications/app/repository"
	"github.com/vibast-solutions/ms-go-notifications/pkg/provider/ses"
)

// --- fakes -----------------------------------------------------------------

type recStatusCall struct {
	messageID, email, status              string
	bounceType, bounceSubtype, diagnostic *string
}

type fakeEventMessages struct {
	msg     *entity.Message
	findErr error

	statusCalls []recStatusCall
	lastError   *string
}

func (f *fakeEventMessages) FindByProviderMessageID(_ context.Context, _ string) (*entity.Message, error) {
	if f.findErr != nil {
		return nil, f.findErr
	}
	return f.msg, nil
}

func (f *fakeEventMessages) SetRecipientStatus(_ context.Context, messageID, email, status string,
	bounceType, bounceSubtype, diagnostic *string, _ time.Time) error {
	f.statusCalls = append(f.statusCalls, recStatusCall{messageID, email, status, bounceType, bounceSubtype, diagnostic})
	return nil
}

func (f *fakeEventMessages) SetLastError(_ context.Context, _, lastError string, _ time.Time) error {
	f.lastError = &lastError
	return nil
}

// fakeEventStore simulates the (provider_event_id, recipient_email) unique index.
type fakeEventStore struct {
	inserted []*entity.MessageEvent
	seen     map[string]bool
}

func newFakeEventStore() *fakeEventStore { return &fakeEventStore{seen: map[string]bool{}} }

func (f *fakeEventStore) Insert(_ context.Context, ev *entity.MessageEvent) error {
	rcpt := ""
	if ev.RecipientEmail != nil {
		rcpt = *ev.RecipientEmail
	}
	pid := ""
	if ev.ProviderEventID != nil {
		pid = *ev.ProviderEventID
	}
	// NULL recipient_email never collides in MySQL; mirror that here.
	if rcpt != "" {
		key := pid + "\x00" + rcpt
		if f.seen[key] {
			return repository.ErrDuplicateKey
		}
		f.seen[key] = true
	}
	f.inserted = append(f.inserted, ev)
	return nil
}

type fakeSuppressionWriter struct {
	upserts []*entity.Suppression
}

func (f *fakeSuppressionWriter) Upsert(_ context.Context, s *entity.Suppression) error {
	f.upserts = append(f.upserts, s)
	return nil
}

// --- helpers ---------------------------------------------------------------

func newEventService() (*EventService, *fakeEventMessages, *fakeEventStore, *fakeSuppressionWriter) {
	msgs := &fakeEventMessages{msg: &entity.Message{ID: "01MSG", Status: entity.MessageStatusSent}}
	store := newFakeEventStore()
	sup := &fakeSuppressionWriter{}
	logger := log.New()
	logger.SetOutput(io.Discard)
	svc := NewEventService(msgs, store, sup, logger)
	return svc, msgs, store, sup
}

func mustEvent(t *testing.T, raw string) *ses.Event {
	t.Helper()
	ev, err := ses.ParseEvent([]byte(raw))
	require.NoError(t, err)
	return ev
}

// --- tests -----------------------------------------------------------------

func TestHandleDelivery(t *testing.T) {
	svc, msgs, store, sup := newEventService()
	ev := mustEvent(t, `{"eventType":"Delivery","mail":{"messageId":"p1","destination":["jane@example.com"]},
	  "delivery":{"recipients":["jane@example.com"],"smtpResponse":"250 OK"}}`)

	require.NoError(t, svc.HandleSESEvent(context.Background(), "sns-1", ev))

	require.Len(t, msgs.statusCalls, 1)
	assert.Equal(t, entity.RecipientStatusDelivered, msgs.statusCalls[0].status)
	assert.Equal(t, "jane@example.com", msgs.statusCalls[0].email)
	require.Len(t, store.inserted, 1)
	assert.Equal(t, entity.EventTypeDelivery, store.inserted[0].Type)
	assert.Empty(t, sup.upserts)
}

func TestHandlePermanentBounceSuppresses(t *testing.T) {
	svc, msgs, store, sup := newEventService()
	ev := mustEvent(t, `{"eventType":"Bounce","mail":{"messageId":"p1","destination":["bad@example.com"]},
	  "bounce":{"bounceType":"Permanent","bounceSubType":"General","bouncedRecipients":[
	    {"emailAddress":"bad@example.com","status":"5.1.1","diagnosticCode":"smtp; 550 user unknown"}]}}`)

	require.NoError(t, svc.HandleSESEvent(context.Background(), "sns-2", ev))

	require.Len(t, msgs.statusCalls, 1)
	assert.Equal(t, entity.RecipientStatusBounced, msgs.statusCalls[0].status)
	require.NotNil(t, msgs.statusCalls[0].bounceType)
	assert.Equal(t, "Permanent", *msgs.statusCalls[0].bounceType)
	assert.Equal(t, "General", *msgs.statusCalls[0].bounceSubtype)
	assert.Equal(t, "smtp; 550 user unknown", *msgs.statusCalls[0].diagnostic)

	require.Len(t, sup.upserts, 1)
	assert.Equal(t, "bad@example.com", sup.upserts[0].Email)
	assert.Equal(t, entity.SuppressionReasonHardBounce, sup.upserts[0].Reason)
	require.NotNil(t, sup.upserts[0].SourceMessageID)
	assert.Equal(t, "01MSG", *sup.upserts[0].SourceMessageID)

	require.Len(t, store.inserted, 1)
	assert.Equal(t, entity.EventTypeBounce, store.inserted[0].Type)
}

func TestHandleTransientBounceNoSuppression(t *testing.T) {
	svc, msgs, _, sup := newEventService()
	ev := mustEvent(t, `{"eventType":"Bounce","mail":{"messageId":"p1","destination":["slow@example.com"]},
	  "bounce":{"bounceType":"Transient","bounceSubType":"MailboxFull","bouncedRecipients":[
	    {"emailAddress":"slow@example.com"}]}}`)

	require.NoError(t, svc.HandleSESEvent(context.Background(), "sns-3", ev))

	require.Len(t, msgs.statusCalls, 1)
	assert.Equal(t, entity.RecipientStatusBounced, msgs.statusCalls[0].status)
	assert.Empty(t, sup.upserts, "transient bounces must not suppress")
}

func TestHandleComplaintSuppresses(t *testing.T) {
	svc, msgs, _, sup := newEventService()
	ev := mustEvent(t, `{"eventType":"Complaint","mail":{"messageId":"p1","destination":["angry@example.com"]},
	  "complaint":{"complaintFeedbackType":"abuse","complainedRecipients":[{"emailAddress":"angry@example.com"}]}}`)

	require.NoError(t, svc.HandleSESEvent(context.Background(), "sns-4", ev))

	require.Len(t, msgs.statusCalls, 1)
	assert.Equal(t, entity.RecipientStatusComplained, msgs.statusCalls[0].status)
	require.Len(t, sup.upserts, 1)
	assert.Equal(t, entity.SuppressionReasonComplaint, sup.upserts[0].Reason)
	assert.Equal(t, "angry@example.com", sup.upserts[0].Email)
}

func TestHandleDeliveryDelay(t *testing.T) {
	svc, msgs, store, sup := newEventService()
	ev := mustEvent(t, `{"eventType":"DeliveryDelay","mail":{"messageId":"p1","destination":["wait@example.com"]},
	  "deliveryDelay":{"delayType":"TransientCommunicationFailure","delayedRecipients":[
	    {"emailAddress":"wait@example.com","status":"4.4.1"}]}}`)

	require.NoError(t, svc.HandleSESEvent(context.Background(), "sns-5", ev))

	require.Len(t, msgs.statusCalls, 1)
	assert.Equal(t, entity.RecipientStatusDelayed, msgs.statusCalls[0].status)
	assert.Empty(t, sup.upserts)
	require.Len(t, store.inserted, 1)
	assert.Equal(t, entity.EventTypeDeliveryDelay, store.inserted[0].Type)
}

func TestHandleRejectSetsLastError(t *testing.T) {
	svc, msgs, store, sup := newEventService()
	ev := mustEvent(t, `{"eventType":"Reject","mail":{"messageId":"p1","destination":["a@example.com"]},
	  "reject":{"reason":"Bad content"}}`)

	require.NoError(t, svc.HandleSESEvent(context.Background(), "sns-6", ev))

	require.NotNil(t, msgs.lastError)
	assert.Equal(t, "Bad content", *msgs.lastError)
	assert.Empty(t, msgs.statusCalls, "reject changes no recipient status")
	assert.Empty(t, sup.upserts)
	require.Len(t, store.inserted, 1)
	assert.Equal(t, entity.EventTypeReject, store.inserted[0].Type)
}

func TestHandleUnknownEventTypeStoresOnly(t *testing.T) {
	svc, msgs, store, sup := newEventService()
	ev := mustEvent(t, `{"eventType":"Open","mail":{"messageId":"p1","destination":["jane@example.com"]}}`)

	require.NoError(t, svc.HandleSESEvent(context.Background(), "sns-7", ev))

	assert.Empty(t, msgs.statusCalls)
	assert.Empty(t, sup.upserts)
	require.Len(t, store.inserted, 1)
	assert.Equal(t, entity.EventTypeOpen, store.inserted[0].Type)
}

func TestHandleUnknownMessageIDDropped(t *testing.T) {
	svc, msgs, store, sup := newEventService()
	msgs.msg = nil
	msgs.findErr = sql.ErrNoRows
	ev := mustEvent(t, `{"eventType":"Delivery","mail":{"messageId":"nope","destination":["jane@example.com"]},
	  "delivery":{"recipients":["jane@example.com"]}}`)

	// Unknown id is logged and dropped: nil error (caller answers 200), nothing written.
	require.NoError(t, svc.HandleSESEvent(context.Background(), "sns-8", ev))
	assert.Empty(t, store.inserted)
	assert.Empty(t, msgs.statusCalls)
	assert.Empty(t, sup.upserts)
}

func TestHandleDuplicateSNSMessageIgnored(t *testing.T) {
	svc, msgs, store, sup := newEventService()
	ev := mustEvent(t, `{"eventType":"Bounce","mail":{"messageId":"p1","destination":["bad@example.com"]},
	  "bounce":{"bounceType":"Permanent","bounceSubType":"General","bouncedRecipients":[
	    {"emailAddress":"bad@example.com"}]}}`)

	require.NoError(t, svc.HandleSESEvent(context.Background(), "dup-sns", ev))
	// Redeliver the exact same SNS message id.
	require.NoError(t, svc.HandleSESEvent(context.Background(), "dup-sns", ev))

	assert.Len(t, store.inserted, 1, "duplicate SNS delivery must insert one event")
	assert.Len(t, msgs.statusCalls, 1, "recipient status applied once")
	assert.Len(t, sup.upserts, 1, "recipient suppressed once")
}

func TestHandleMultipleRecipients(t *testing.T) {
	svc, msgs, store, _ := newEventService()
	ev := mustEvent(t, `{"eventType":"Delivery","mail":{"messageId":"p1","destination":["a@example.com","b@example.com"]},
	  "delivery":{"recipients":["a@example.com","b@example.com"]}}`)

	require.NoError(t, svc.HandleSESEvent(context.Background(), "sns-multi", ev))
	assert.Len(t, store.inserted, 2)
	assert.Len(t, msgs.statusCalls, 2)
}

func TestHandleFindErrorPropagates(t *testing.T) {
	svc, msgs, _, _ := newEventService()
	msgs.msg = nil
	msgs.findErr = context.DeadlineExceeded
	ev := mustEvent(t, `{"eventType":"Delivery","mail":{"messageId":"p1","destination":["a@example.com"]},
	  "delivery":{"recipients":["a@example.com"]}}`)

	err := svc.HandleSESEvent(context.Background(), "sns-9", ev)
	assert.Error(t, err, "infrastructure errors must propagate so the caller answers 5xx")
}
