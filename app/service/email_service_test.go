package service

import (
	"context"
	"database/sql"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dtov1 "github.com/vibast-solutions/ms-go-notifications/app/dto/v1"
	"github.com/vibast-solutions/ms-go-notifications/app/entity"
	"github.com/vibast-solutions/ms-go-notifications/app/repository"
)

// fakeMessages is an in-memory MessageRepository. The transaction boundary is
// backed by sqlmock so BeginTx/Commit/Rollback are real *sql.Tx operations the
// test asserts on; the actual row writes are captured in memory.
type fakeMessages struct {
	db   *sql.DB
	mock sqlmock.Sqlmock

	createErr error // e.g. repository.ErrDuplicateKey to simulate a race

	created           *entity.Message
	createdRecipients []*entity.MessageRecipient

	findIdem      func(apiKeyID, key string) (*entity.Message, error)
	findForKey    func(id, apiKeyID string) (*entity.Message, error)
	recipientsOut []*entity.MessageRecipient
	eventsOut     []*entity.MessageEvent
}

func newFakeMessages(t *testing.T) *fakeMessages {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return &fakeMessages{
		db:   db,
		mock: mock,
		findIdem: func(string, string) (*entity.Message, error) {
			return nil, sql.ErrNoRows
		},
	}
}

func (f *fakeMessages) BeginTx(ctx context.Context) (*sql.Tx, error) {
	return f.db.BeginTx(ctx, nil)
}

func (f *fakeMessages) CreateWithChildren(_ context.Context, _ *sql.Tx, msg *entity.Message,
	recipients []*entity.MessageRecipient, _ []*entity.MessageAttachment, _ []*entity.MessageEvent) error {
	if f.createErr != nil {
		return f.createErr
	}
	f.created = msg
	f.createdRecipients = recipients
	return nil
}

func (f *fakeMessages) FindByIdempotency(_ context.Context, apiKeyID, key string) (*entity.Message, error) {
	return f.findIdem(apiKeyID, key)
}

func (f *fakeMessages) FindByIDForAPIKey(_ context.Context, id, apiKeyID string) (*entity.Message, error) {
	if f.findForKey != nil {
		return f.findForKey(id, apiKeyID)
	}
	return nil, sql.ErrNoRows
}

func (f *fakeMessages) LoadRecipients(context.Context, string) ([]*entity.MessageRecipient, error) {
	return f.recipientsOut, nil
}

func (f *fakeMessages) LoadEvents(context.Context, string) ([]*entity.MessageEvent, error) {
	return f.eventsOut, nil
}

// fakeSuppressions returns a fixed set of suppressed addresses.
type fakeSuppressions struct {
	suppressed []string
}

func (f *fakeSuppressions) FindByEmails(_ context.Context, emails []string) ([]*entity.Suppression, error) {
	want := map[string]bool{}
	for _, e := range f.suppressed {
		want[e] = true
	}
	var out []*entity.Suppression
	for _, e := range emails {
		if want[e] {
			out = append(out, &entity.Suppression{Email: e, Reason: entity.SuppressionReasonManual})
		}
	}
	return out, nil
}

// fakeDispatcher records enqueued ids.
type fakeDispatcher struct{ ids []string }

func (f *fakeDispatcher) Enqueue(id string) { f.ids = append(f.ids, id) }

func sendKey() *entity.APIKey {
	return &entity.APIKey{ID: "01APIKEY", Scopes: []string{"send"}, Domains: []string{"vibast.ro"}}
}

func baseRequest() *dtov1.SendEmailRequest {
	return &dtov1.SendEmailRequest{
		From:    dtov1.Address{Email: "no-reply@vibast.ro"},
		To:      []dtov1.Address{{Email: "jane@example.com"}},
		Subject: "Hi",
		Text:    "hello",
	}
}

func newService(msgs *fakeMessages, supp *fakeSuppressions, disp Dispatcher) *EmailService {
	return NewEmailService(msgs, supp, disp,
		dtov1.Limits{MaxRecipients: 50, MaxPayloadBytes: 10 << 20})
}

func TestAcceptNewMessageQueuedAndDispatched(t *testing.T) {
	msgs := newFakeMessages(t)
	msgs.mock.ExpectBegin()
	msgs.mock.ExpectCommit()
	disp := &fakeDispatcher{}
	svc := newService(msgs, &fakeSuppressions{}, disp)

	msg, created, err := svc.Accept(context.Background(), sendKey(), baseRequest())
	require.NoError(t, err)
	assert.True(t, created)
	assert.Equal(t, entity.MessageStatusQueued, msg.Status)
	assert.Equal(t, "vibast.ro", msg.FromDomain)
	assert.Equal(t, 1, msg.RecipientCount)
	assert.Equal(t, []string{msg.ID}, disp.ids, "queued message must be dispatched with its id")
	assert.NoError(t, msgs.mock.ExpectationsWereMet())
}

func TestAcceptIdempotentReplayReturnsExistingNoDispatch(t *testing.T) {
	existing := &entity.Message{ID: "01EXISTING", Status: entity.MessageStatusQueued}
	msgs := newFakeMessages(t)
	msgs.findIdem = func(string, string) (*entity.Message, error) { return existing, nil }
	disp := &fakeDispatcher{}
	svc := newService(msgs, &fakeSuppressions{}, disp)

	req := baseRequest()
	req.IdempotencyKey = "contact-123"

	msg, created, err := svc.Accept(context.Background(), sendKey(), req)
	require.NoError(t, err)
	assert.False(t, created)
	assert.Equal(t, "01EXISTING", msg.ID)
	assert.Empty(t, disp.ids, "idempotent replay must not dispatch")
	assert.Nil(t, msgs.created, "idempotent replay must not insert")
	assert.NoError(t, msgs.mock.ExpectationsWereMet(), "no transaction should be opened")
}

func TestAcceptPartialSuppression(t *testing.T) {
	msgs := newFakeMessages(t)
	msgs.mock.ExpectBegin()
	msgs.mock.ExpectCommit()
	disp := &fakeDispatcher{}
	svc := newService(msgs, &fakeSuppressions{suppressed: []string{"blocked@example.com"}}, disp)

	req := baseRequest()
	req.To = []dtov1.Address{{Email: "jane@example.com"}}
	req.Cc = []dtov1.Address{{Email: "blocked@example.com"}}

	msg, created, err := svc.Accept(context.Background(), sendKey(), req)
	require.NoError(t, err)
	assert.True(t, created)
	assert.Equal(t, entity.MessageStatusQueued, msg.Status, "some recipients are sendable")
	assert.Equal(t, []string{msg.ID}, disp.ids)

	byEmail := map[string]string{}
	for _, r := range msgs.createdRecipients {
		byEmail[r.Email] = r.Status
	}
	assert.Equal(t, entity.RecipientStatusPending, byEmail["jane@example.com"])
	assert.Equal(t, entity.RecipientStatusSuppressed, byEmail["blocked@example.com"])
}

func TestAcceptAllSuppressed(t *testing.T) {
	msgs := newFakeMessages(t)
	msgs.mock.ExpectBegin()
	msgs.mock.ExpectCommit()
	disp := &fakeDispatcher{}
	svc := newService(msgs, &fakeSuppressions{suppressed: []string{"jane@example.com"}}, disp)

	msg, created, err := svc.Accept(context.Background(), sendKey(), baseRequest())
	require.NoError(t, err)
	assert.True(t, created)
	assert.Equal(t, entity.MessageStatusSuppressed, msg.Status)
	assert.NotNil(t, msg.FinishedAt, "suppressed message is terminal at accept time")
	assert.Empty(t, disp.ids, "fully suppressed message must not be dispatched")
	assert.NoError(t, msgs.mock.ExpectationsWereMet())
}

func TestAcceptDomainNotAllowed(t *testing.T) {
	msgs := newFakeMessages(t)
	disp := &fakeDispatcher{}
	svc := newService(msgs, &fakeSuppressions{}, disp)

	req := baseRequest()
	req.From = dtov1.Address{Email: "no-reply@evil.com"}

	_, _, err := svc.Accept(context.Background(), sendKey(), req)
	assert.ErrorIs(t, err, ErrDomainNotAllowed)
	assert.Empty(t, disp.ids)
	assert.Nil(t, msgs.created)
}

func TestAcceptValidationError(t *testing.T) {
	msgs := newFakeMessages(t)
	svc := newService(msgs, &fakeSuppressions{}, &fakeDispatcher{})

	req := baseRequest()
	req.Subject = "" // required

	_, _, err := svc.Accept(context.Background(), sendKey(), req)
	var ve *ValidationError
	require.ErrorAs(t, err, &ve)
	assert.Contains(t, ve.Fields, "subject")
}

func TestAcceptPayloadTooLarge(t *testing.T) {
	msgs := newFakeMessages(t)
	svc := NewEmailService(msgs, &fakeSuppressions{}, &fakeDispatcher{},
		dtov1.Limits{MaxRecipients: 50, MaxPayloadBytes: 4})

	req := baseRequest()
	req.Text = "this is definitely longer than four bytes"

	_, _, err := svc.Accept(context.Background(), sendKey(), req)
	assert.ErrorIs(t, err, ErrPayloadTooLarge)
}

func TestAcceptIdempotencyRaceReReads(t *testing.T) {
	existing := &entity.Message{ID: "01WINNER", Status: entity.MessageStatusQueued}
	msgs := newFakeMessages(t)
	msgs.createErr = repository.ErrDuplicateKey
	// First lookup (pre-insert) misses; second (after the duplicate) hits.
	calls := 0
	msgs.findIdem = func(string, string) (*entity.Message, error) {
		calls++
		if calls == 1 {
			return nil, sql.ErrNoRows
		}
		return existing, nil
	}
	msgs.mock.ExpectBegin()
	msgs.mock.ExpectRollback()
	disp := &fakeDispatcher{}
	svc := newService(msgs, &fakeSuppressions{}, disp)

	req := baseRequest()
	req.IdempotencyKey = "contact-123"

	msg, created, err := svc.Accept(context.Background(), sendKey(), req)
	require.NoError(t, err)
	assert.False(t, created)
	assert.Equal(t, "01WINNER", msg.ID)
	assert.Empty(t, disp.ids, "the losing writer must not dispatch")
	assert.NoError(t, msgs.mock.ExpectationsWereMet())
}

func TestGetForAPIKey(t *testing.T) {
	msg := &entity.Message{ID: "01MSG", APIKeyID: "01APIKEY", Status: entity.MessageStatusSent}
	msgs := newFakeMessages(t)
	msgs.findForKey = func(id, apiKeyID string) (*entity.Message, error) {
		if id == "01MSG" && apiKeyID == "01APIKEY" {
			return msg, nil
		}
		return nil, sql.ErrNoRows
	}
	msgs.recipientsOut = []*entity.MessageRecipient{{ID: "r1", Email: "jane@example.com", Status: "delivered"}}
	msgs.eventsOut = []*entity.MessageEvent{{ID: "e1", Type: entity.EventTypeQueued}}
	svc := newService(msgs, &fakeSuppressions{}, &fakeDispatcher{})

	t.Run("found", func(t *testing.T) {
		got, recipients, events, err := svc.GetForAPIKey(context.Background(), "01MSG", "01APIKEY")
		require.NoError(t, err)
		assert.Equal(t, "01MSG", got.ID)
		assert.Len(t, recipients, 1)
		assert.Len(t, events, 1)
	})
	t.Run("other key is not found", func(t *testing.T) {
		_, _, _, err := svc.GetForAPIKey(context.Background(), "01MSG", "01OTHERKEY")
		assert.ErrorIs(t, err, ErrNotFound)
	})
}
