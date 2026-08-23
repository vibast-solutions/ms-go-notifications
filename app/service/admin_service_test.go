package service

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dtov1 "github.com/vibast-solutions/ms-go-notifications/app/dto/v1"
	"github.com/vibast-solutions/ms-go-notifications/app/entity"
	"github.com/vibast-solutions/ms-go-notifications/app/repository"
)

var adminNow = time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)

// fakeAdminMessages is a programmable AdminMessageStore.
type fakeAdminMessages struct {
	listRows    []*repository.MessageSummaryRow
	listFilter  repository.MessageFilter
	listLimit   int
	msg         *entity.Message
	msgErr      error
	requeued    bool
	requeueErr  error
	requeuedID  string
	statusCount map[string]int
	msgBuckets  []repository.StatusBucketRow
	evBuckets   []repository.EventBucketRow
	attachment  *entity.MessageAttachment
	attachErr   error
}

func (f *fakeAdminMessages) ListForAdmin(_ context.Context, filter repository.MessageFilter, limit int) ([]*repository.MessageSummaryRow, error) {
	f.listFilter, f.listLimit = filter, limit
	return f.listRows, nil
}
func (f *fakeAdminMessages) FindByID(_ context.Context, _ string) (*entity.Message, error) {
	return f.msg, f.msgErr
}
func (f *fakeAdminMessages) LoadRecipients(_ context.Context, _ string) ([]*entity.MessageRecipient, error) {
	return nil, nil
}
func (f *fakeAdminMessages) LoadEvents(_ context.Context, _ string) ([]*entity.MessageEvent, error) {
	return nil, nil
}
func (f *fakeAdminMessages) LoadAttachmentsMeta(_ context.Context, _ string) ([]*entity.MessageAttachment, error) {
	return nil, nil
}
func (f *fakeAdminMessages) FindAttachment(_ context.Context, _, _ string) (*entity.MessageAttachment, error) {
	return f.attachment, f.attachErr
}
func (f *fakeAdminMessages) RequeueForRetry(_ context.Context, id string, _ *entity.MessageEvent, _ time.Time) (bool, error) {
	f.requeuedID = id
	return f.requeued, f.requeueErr
}
func (f *fakeAdminMessages) CountMessagesByStatus(_ context.Context, _ []string) (map[string]int, error) {
	return f.statusCount, nil
}
func (f *fakeAdminMessages) StatsMessageBuckets(_ context.Context, _, _ time.Time, _, _ string) ([]repository.StatusBucketRow, error) {
	return f.msgBuckets, nil
}
func (f *fakeAdminMessages) StatsEventBuckets(_ context.Context, _, _ time.Time, _, _ string) ([]repository.EventBucketRow, error) {
	return f.evBuckets, nil
}

// fakeReputation is a programmable ReputationReader.
type fakeReputation struct {
	bounceRate    float64
	complaintRate float64
	state         SendingState
}

func (f *fakeReputation) ComputeRates(context.Context, time.Duration) (int, int, int, float64, float64, error) {
	return 0, 0, 0, f.bounceRate, f.complaintRate, nil
}
func (f *fakeReputation) State(context.Context) (SendingState, error) { return f.state, nil }

// recordingDispatcher records enqueued ids.
type recordingDispatcher struct{ ids []string }

func (d *recordingDispatcher) Enqueue(id string) bool { d.ids = append(d.ids, id); return true }

func newAdminService(msgs *fakeAdminMessages) (*AdminService, *recordingDispatcher, *fakeReputation) {
	disp := &recordingDispatcher{}
	rep := &fakeReputation{}
	svc := NewAdminService(msgs, nil, nil, rep, disp)
	svc.now = func() time.Time { return adminNow }
	return svc, disp, rep
}

func summaryRow(id string, at time.Time) *repository.MessageSummaryRow {
	return &repository.MessageSummaryRow{ID: id, Status: "sent", FromEmail: "x@vibast.ro", Subject: "s", CreatedAt: at}
}

func TestListMessagesNoNextCursorWhenUnderLimit(t *testing.T) {
	msgs := &fakeAdminMessages{listRows: []*repository.MessageSummaryRow{summaryRow("01A", adminNow)}}
	svc, _, _ := newAdminService(msgs)

	resp, err := svc.ListMessages(context.Background(), MessageListParams{Limit: "25"})
	require.NoError(t, err)
	assert.Len(t, resp.Items, 1)
	assert.Empty(t, resp.NextCursor)
	assert.Equal(t, 26, msgs.listLimit, "asks for limit+1 to detect the next page")
}

func TestListMessagesEmitsNextCursorAtLimit(t *testing.T) {
	// limit=2, three rows returned → one extra row signals a next page.
	t2 := adminNow.Add(-time.Hour)
	t3 := adminNow.Add(-2 * time.Hour)
	msgs := &fakeAdminMessages{listRows: []*repository.MessageSummaryRow{
		summaryRow("01A", adminNow), summaryRow("01B", t2), summaryRow("01C", t3),
	}}
	svc, _, _ := newAdminService(msgs)

	resp, err := svc.ListMessages(context.Background(), MessageListParams{Limit: "2"})
	require.NoError(t, err)
	require.Len(t, resp.Items, 2)
	require.NotEmpty(t, resp.NextCursor)

	// The cursor round-trips to the last returned item (01B, t2).
	fields := map[string]string{}
	cur, ok := decodeMessageCursor(resp.NextCursor, fields)
	require.True(t, ok)
	assert.Empty(t, fields)
	assert.Equal(t, "01B", cur.id)
	assert.Equal(t, t2.UnixMilli(), cur.at.UnixMilli())
}

func TestListMessagesRejectsBadStatusAndTag(t *testing.T) {
	svc, _, _ := newAdminService(&fakeAdminMessages{})

	_, err := svc.ListMessages(context.Background(), MessageListParams{Status: "nope", Tag: "no-colon"})
	var ve *ValidationError
	require.ErrorAs(t, err, &ve)
	assert.Contains(t, ve.Fields, "status")
	assert.Contains(t, ve.Fields, "tag")
}

func TestListMessagesParsesTagIntoJSONPath(t *testing.T) {
	msgs := &fakeAdminMessages{}
	svc, _, _ := newAdminService(msgs)

	_, err := svc.ListMessages(context.Background(), MessageListParams{Tag: "app:web"})
	require.NoError(t, err)
	assert.Equal(t, `$."app"`, msgs.listFilter.TagPath)
	assert.Equal(t, "web", msgs.listFilter.TagValue)
}

func TestRetryOnlyFromFailed(t *testing.T) {
	msgs := &fakeAdminMessages{msg: &entity.Message{ID: "01M", Status: entity.MessageStatusSent}}
	svc, _, _ := newAdminService(msgs)

	err := svc.Retry(context.Background(), "01M", "ops")
	assert.ErrorIs(t, err, ErrInvalidState)
}

func TestRetryNotFound(t *testing.T) {
	msgs := &fakeAdminMessages{msgErr: sql.ErrNoRows}
	svc, _, _ := newAdminService(msgs)

	err := svc.Retry(context.Background(), "01M", "ops")
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestRetrySuccessEnqueues(t *testing.T) {
	msgs := &fakeAdminMessages{msg: &entity.Message{ID: "01M", Status: entity.MessageStatusFailed}, requeued: true}
	svc, disp, _ := newAdminService(msgs)

	err := svc.Retry(context.Background(), "01M", "ops")
	require.NoError(t, err)
	assert.Equal(t, "01M", msgs.requeuedID)
	assert.Equal(t, []string{"01M"}, disp.ids)
}

func TestRetryLostRaceIsInvalidState(t *testing.T) {
	// Read says failed, but the guarded UPDATE matched no row (status changed).
	msgs := &fakeAdminMessages{msg: &entity.Message{ID: "01M", Status: entity.MessageStatusFailed}, requeued: false}
	svc, disp, _ := newAdminService(msgs)

	err := svc.Retry(context.Background(), "01M", "ops")
	assert.ErrorIs(t, err, ErrInvalidState)
	assert.Empty(t, disp.ids, "no dispatch when the re-queue did not happen")
}

func TestStatsZeroFillsBucketsAndMerges(t *testing.T) {
	msgs := &fakeAdminMessages{
		statusCount: map[string]int{"queued": 4, "sending": 1},
		msgBuckets: []repository.StatusBucketRow{
			{Bucket: "2026-08-22T00:00:00Z", Status: "sent", Count: 5},
			{Bucket: "2026-08-22T00:00:00Z", Status: "failed", Count: 1},
		},
		evBuckets: []repository.EventBucketRow{
			{Bucket: "2026-08-22T00:00:00Z", Delivered: 4, BouncedHard: 1, BouncedSoft: 0, Complained: 1},
		},
	}
	svc, _, rep := newAdminService(msgs)
	rep.bounceRate = 0.02
	rep.complaintRate = 0.001
	rep.state = SendingState{Paused: true, Reason: "auto", Since: adminNow, By: "auto"}

	resp, err := svc.Stats(context.Background(), StatsParams{
		From:        "2026-08-21",
		To:          "2026-08-23",
		Granularity: "day",
	})
	require.NoError(t, err)

	// Two full days between 21 and 23 → two buckets, both present (zero-filled).
	require.Len(t, resp.Buckets, 2)
	assert.Equal(t, "2026-08-21T00:00:00Z", resp.Buckets[0].Bucket)
	assert.Equal(t, dtov1.StatCounts{}, resp.Buckets[0].StatCounts, "empty bucket is zeroed")

	b := resp.Buckets[1]
	assert.Equal(t, "2026-08-22T00:00:00Z", b.Bucket)
	assert.Equal(t, 6, b.Accepted, "sent + failed")
	assert.Equal(t, 5, b.Sent)
	assert.Equal(t, 1, b.Failed)
	assert.Equal(t, 4, b.Delivered)
	assert.Equal(t, 1, b.BouncedHard)
	assert.Equal(t, 1, b.Complained)

	assert.Equal(t, 6, resp.Totals.Accepted)
	assert.Equal(t, 0.02, resp.BounceRate)
	assert.Equal(t, 0.001, resp.ComplaintRate)
	assert.True(t, resp.Sending.Paused)
	require.NotNil(t, resp.Sending.Since)
	assert.Equal(t, dtov1.StatsBacklog{Queued: 4, Sending: 1}, resp.Backlog)
}

func TestStatsRejectsBadGranularity(t *testing.T) {
	svc, _, _ := newAdminService(&fakeAdminMessages{})
	_, err := svc.Stats(context.Background(), StatsParams{Granularity: "week"})
	var ve *ValidationError
	require.ErrorAs(t, err, &ve)
	assert.Contains(t, ve.Fields, "granularity")
}

func TestStatsRejectsFromAfterTo(t *testing.T) {
	svc, _, _ := newAdminService(&fakeAdminMessages{})
	_, err := svc.Stats(context.Background(), StatsParams{From: "2026-08-23", To: "2026-08-21"})
	var ve *ValidationError
	require.ErrorAs(t, err, &ve)
	assert.Contains(t, ve.Fields, "from")
}

func TestCursorRoundTrip(t *testing.T) {
	at := time.Date(2026, 8, 22, 9, 30, 0, 123_000_000, time.UTC)
	enc := encodeCursor(at, "user|weird@example.com")

	fields := map[string]string{}
	cur, ok := decodeCursor(enc, "cursor", fields)
	require.True(t, ok)
	assert.Empty(t, fields)
	assert.Equal(t, "user|weird@example.com", cur.id, "splits on the first delimiter only")
	assert.Equal(t, at.UnixMilli(), cur.at.UnixMilli())
}

func TestDecodeCursorMalformed(t *testing.T) {
	fields := map[string]string{}
	_, ok := decodeCursor("!!!not-base64!!!", "cursor", fields)
	assert.False(t, ok)
	assert.Contains(t, fields, "cursor")
}

// fakeAdminSuppressions is a programmable AdminSuppressionStore.
type fakeAdminSuppressions struct {
	rows       []*entity.Suppression
	insertErr  error
	inserted   *entity.Suppression
	deleteErr  error
	deletedFor string
	filter     repository.SuppressionFilter
}

func (f *fakeAdminSuppressions) ListForAdmin(_ context.Context, filter repository.SuppressionFilter, _ int) ([]*entity.Suppression, error) {
	f.filter = filter
	return f.rows, nil
}
func (f *fakeAdminSuppressions) Insert(_ context.Context, s *entity.Suppression) error {
	f.inserted = s
	return f.insertErr
}
func (f *fakeAdminSuppressions) Delete(_ context.Context, email string) error {
	f.deletedFor = email
	return f.deleteErr
}

func newSuppressionService(sup *fakeAdminSuppressions) *AdminService {
	svc := NewAdminService(&fakeAdminMessages{}, sup, nil, &fakeReputation{}, &recordingDispatcher{})
	svc.now = func() time.Time { return adminNow }
	return svc
}

func TestAddSuppressionNormalizesAndSetsManual(t *testing.T) {
	sup := &fakeAdminSuppressions{}
	svc := newSuppressionService(sup)

	resp, err := svc.AddSuppression(context.Background(), &dtov1.CreateSuppressionRequest{Email: "  Bad@Example.COM ", Note: "spammer"})
	require.NoError(t, err)
	assert.Equal(t, "bad@example.com", resp.Email)
	assert.Equal(t, entity.SuppressionReasonManual, resp.Reason)
	assert.Equal(t, "bad@example.com", sup.inserted.Email)
	require.NotNil(t, sup.inserted.Note)
	assert.Equal(t, "spammer", *sup.inserted.Note)
}

func TestAddSuppressionRejectsBadEmail(t *testing.T) {
	svc := newSuppressionService(&fakeAdminSuppressions{})
	_, err := svc.AddSuppression(context.Background(), &dtov1.CreateSuppressionRequest{Email: "not-an-email"})
	var ve *ValidationError
	require.ErrorAs(t, err, &ve)
	assert.Contains(t, ve.Fields, "email")
}

func TestAddSuppressionDuplicateConflict(t *testing.T) {
	sup := &fakeAdminSuppressions{insertErr: repository.ErrDuplicateKey}
	svc := newSuppressionService(sup)
	_, err := svc.AddSuppression(context.Background(), &dtov1.CreateSuppressionRequest{Email: "dup@example.com"})
	assert.ErrorIs(t, err, ErrAlreadyExists)
}

func TestDeleteSuppressionNotFound(t *testing.T) {
	sup := &fakeAdminSuppressions{deleteErr: sql.ErrNoRows}
	svc := newSuppressionService(sup)
	err := svc.DeleteSuppression(context.Background(), "X@Example.com")
	assert.ErrorIs(t, err, ErrNotFound)
	assert.Equal(t, "x@example.com", sup.deletedFor, "email normalized before delete")
}
