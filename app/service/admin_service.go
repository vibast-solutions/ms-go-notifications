package service

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	dtov1 "github.com/vibast-solutions/ms-go-notifications/app/dto/v1"
	"github.com/vibast-solutions/ms-go-notifications/app/entity"
	"github.com/vibast-solutions/ms-go-notifications/app/repository"
)

// Admin list/stats bounds.
const (
	defaultAdminLimit = 25
	maxAdminLimit     = 100
	maxStatsBuckets   = 1000
	defaultStatsDays  = 7
)

// tagFilterKeyRe constrains the key of a tag filter so it can be embedded in a
// JSON path safely (tag keys are stored under the same alphabet).
var tagFilterKeyRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// AdminMessageStore is the message persistence the admin service depends on.
// Implemented by repository.MessageRepository.
type AdminMessageStore interface {
	ListForAdmin(ctx context.Context, f repository.MessageFilter, limit int) ([]*repository.MessageSummaryRow, error)
	FindByID(ctx context.Context, id string) (*entity.Message, error)
	LoadRecipients(ctx context.Context, messageID string) ([]*entity.MessageRecipient, error)
	LoadEvents(ctx context.Context, messageID string) ([]*entity.MessageEvent, error)
	LoadAttachmentsMeta(ctx context.Context, messageID string) ([]*entity.MessageAttachment, error)
	FindAttachment(ctx context.Context, messageID, attachmentID string) (*entity.MessageAttachment, error)
	RequeueForRetry(ctx context.Context, id string, ev *entity.MessageEvent, at time.Time) (bool, error)
	CountMessagesByStatus(ctx context.Context, statuses []string) (map[string]int, error)
	StatsMessageBuckets(ctx context.Context, from, to time.Time, fromDomain, granularity string) ([]repository.StatusBucketRow, error)
	StatsEventBuckets(ctx context.Context, from, to time.Time, fromDomain, granularity string) ([]repository.EventBucketRow, error)
}

// AdminSuppressionStore is the suppression persistence the admin service
// depends on. Implemented by repository.SuppressionRepository.
type AdminSuppressionStore interface {
	ListForAdmin(ctx context.Context, f repository.SuppressionFilter, limit int) ([]*entity.Suppression, error)
	Insert(ctx context.Context, s *entity.Suppression) error
	Delete(ctx context.Context, email string) error
}

// KeyNameResolver resolves an API key id to its name for the admin detail.
// Implemented by APIKeyService (returns ErrNotFound for unknown ids).
type KeyNameResolver interface {
	NameByID(ctx context.Context, id string) (string, error)
}

// ReputationReader is the slice of the reputation service the stats endpoint
// needs: the trailing-window rates and the current pause state.
type ReputationReader interface {
	ComputeRates(ctx context.Context, window time.Duration) (sentRecipients, hardBounces, complaints int, bounceRate, complaintRate float64, err error)
	State(ctx context.Context) (SendingState, error)
}

// AdminService implements the operator-facing read/write API: message history
// and detail, attachment download, retry, dashboard stats and manual
// suppressions. API-key management stays in APIKeyService, and the sending
// circuit breaker in ReputationService.
type AdminService struct {
	messages     AdminMessageStore
	suppressions AdminSuppressionStore
	keyNames     KeyNameResolver
	reputation   ReputationReader
	dispatcher   Dispatcher

	now func() time.Time
}

// NewAdminService wires the admin service with its stores and collaborators.
func NewAdminService(messages AdminMessageStore, suppressions AdminSuppressionStore,
	keyNames KeyNameResolver, reputation ReputationReader, dispatcher Dispatcher) *AdminService {
	return &AdminService{
		messages:     messages,
		suppressions: suppressions,
		keyNames:     keyNames,
		reputation:   reputation,
		dispatcher:   dispatcher,
		now:          time.Now,
	}
}

// MessageListParams are the raw GET /v1/admin/messages query values. The
// service validates and normalizes them.
type MessageListParams struct {
	Status      string
	FromDomain  string
	From        string
	To          string
	Subject     string
	APIKeyID    string
	Tag         string // "key:value"
	CreatedFrom string
	CreatedTo   string
	Cursor      string
	Limit       string
}

// ListMessages validates the query, decodes the cursor and returns one page of
// history plus the cursor for the next page (empty when exhausted).
func (s *AdminService) ListMessages(ctx context.Context, p MessageListParams) (*dtov1.MessageListResponse, error) {
	fields := map[string]string{}

	limit := parseLimit(p.Limit, fields)
	filter := repository.MessageFilter{
		Status:     strings.TrimSpace(p.Status),
		FromDomain: strings.ToLower(strings.TrimSpace(p.FromDomain)),
		From:       strings.TrimSpace(p.From),
		To:         strings.TrimSpace(p.To),
		Subject:    strings.TrimSpace(p.Subject),
		APIKeyID:   strings.TrimSpace(p.APIKeyID),
	}

	if filter.Status != "" && !validMessageStatus(filter.Status) {
		fields["status"] = "unknown status"
	}
	if p.Tag != "" {
		if path, value, ok := parseTagFilter(p.Tag); ok {
			filter.TagPath, filter.TagValue = path, value
		} else {
			fields["tag"] = "must be key:value with key of [A-Za-z0-9_-]"
		}
	}
	filter.CreatedFrom = parseTimeField(p.CreatedFrom, "createdFrom", fields)
	filter.CreatedTo = parseTimeField(p.CreatedTo, "createdTo", fields)
	if cur, ok := decodeMessageCursor(p.Cursor, fields); ok {
		filter.BeforeCreatedAt, filter.BeforeID = cur.at, cur.id
	}

	if len(fields) > 0 {
		return nil, NewValidationError(fields)
	}

	rows, err := s.messages.ListForAdmin(ctx, filter, limit+1)
	if err != nil {
		return nil, fmt.Errorf("listing messages: %w", err)
	}

	resp := &dtov1.MessageListResponse{Items: make([]dtov1.MessageSummary, 0, limit)}
	for i, row := range rows {
		if i == limit {
			last := rows[limit-1]
			resp.NextCursor = encodeCursor(last.CreatedAt, last.ID)
			break
		}
		from := dtov1.Address{Email: row.FromEmail}
		if row.FromName != nil {
			from.Name = *row.FromName
		}
		resp.Items = append(resp.Items, dtov1.MessageSummary{
			ID:             row.ID,
			Status:         row.Status,
			From:           from,
			Subject:        row.Subject,
			RecipientCount: row.RecipientCount,
			FirstTo:        row.FirstTo,
			APIKeyName:     row.APIKeyName,
			Provider:       row.Provider,
			Attempts:       row.Attempts,
			CreatedAt:      row.CreatedAt,
			SentAt:         row.SentAt,
		})
	}
	return resp, nil
}

// MessageDetail returns the full admin detail for a message, or ErrNotFound.
func (s *AdminService) MessageDetail(ctx context.Context, id string) (*dtov1.AdminMessageResponse, error) {
	msg, err := s.messages.FindByID(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("loading message: %w", err)
	}
	recipients, err := s.messages.LoadRecipients(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("loading recipients: %w", err)
	}
	events, err := s.messages.LoadEvents(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("loading events: %w", err)
	}
	attachments, err := s.messages.LoadAttachmentsMeta(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("loading attachments: %w", err)
	}

	// The owning key name is best-effort: a deleted key must not 500 the detail.
	keyName := ""
	if name, err := s.keyNames.NameByID(ctx, msg.APIKeyID); err == nil {
		keyName = name
	} else if !errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("loading api key name: %w", err)
	}

	resp := dtov1.NewAdminMessageResponse(msg, recipients, events, attachments, keyName)
	return &resp, nil
}

// Attachment returns a single attachment (with content) for download, or
// ErrNotFound. The controller answers 410 when the returned attachment is
// purged (PurgedAt set / content nil).
func (s *AdminService) Attachment(ctx context.Context, messageID, attachmentID string) (*entity.MessageAttachment, error) {
	att, err := s.messages.FindAttachment(ctx, messageID, attachmentID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("loading attachment: %w", err)
	}
	return att, nil
}

// Retry re-queues a failed message and records a retry_requested event
// attributed to byName. Returns ErrNotFound when the id is unknown and
// ErrInvalidState when the message is not currently failed.
func (s *AdminService) Retry(ctx context.Context, id, byName string) error {
	msg, err := s.messages.FindByID(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("loading message: %w", err)
	}
	if msg.Status != entity.MessageStatusFailed {
		return ErrInvalidState
	}

	now := s.now().UTC().Truncate(time.Millisecond)
	payload := []byte(fmt.Sprintf(`{"by":%s}`, strconv.Quote(byName)))
	ev := &entity.MessageEvent{
		ID:         entity.NewID(),
		MessageID:  id,
		Type:       entity.EventTypeRetryRequested,
		Payload:    payload,
		OccurredAt: now,
		CreatedAt:  now,
	}

	requeued, err := s.messages.RequeueForRetry(ctx, id, ev, now)
	if err != nil {
		return fmt.Errorf("re-queuing message: %w", err)
	}
	if !requeued {
		// Lost the race: the status changed between the read and the guarded
		// update, so it is no longer failed.
		return ErrInvalidState
	}
	s.dispatcher.Enqueue(id)
	return nil
}

// SuppressionListParams are the raw GET /v1/admin/suppressions query values.
type SuppressionListParams struct {
	Email  string
	Reason string
	Cursor string
	Limit  string
}

// ListSuppressions returns one page of suppressions plus the next cursor.
func (s *AdminService) ListSuppressions(ctx context.Context, p SuppressionListParams) (*dtov1.SuppressionListResponse, error) {
	fields := map[string]string{}

	limit := parseLimit(p.Limit, fields)
	filter := repository.SuppressionFilter{
		Email:  strings.TrimSpace(p.Email),
		Reason: strings.TrimSpace(p.Reason),
	}
	if filter.Reason != "" && !validSuppressionReason(filter.Reason) {
		fields["reason"] = "unknown reason"
	}
	if cur, ok := decodeSuppressionCursor(p.Cursor, fields); ok {
		filter.BeforeCreatedAt, filter.BeforeEmail = cur.at, cur.id
	}

	if len(fields) > 0 {
		return nil, NewValidationError(fields)
	}

	rows, err := s.suppressions.ListForAdmin(ctx, filter, limit+1)
	if err != nil {
		return nil, fmt.Errorf("listing suppressions: %w", err)
	}

	resp := &dtov1.SuppressionListResponse{Items: make([]dtov1.SuppressionResponse, 0, limit)}
	for i, row := range rows {
		if i == limit {
			last := rows[limit-1]
			resp.NextCursor = encodeCursor(last.CreatedAt, last.Email)
			break
		}
		resp.Items = append(resp.Items, dtov1.NewSuppressionResponse(row))
	}
	return resp, nil
}

// AddSuppression records a manual suppression. Returns *ValidationError for a
// bad email and ErrAlreadyExists when the address is already suppressed.
func (s *AdminService) AddSuppression(ctx context.Context, req *dtov1.CreateSuppressionRequest) (*dtov1.SuppressionResponse, error) {
	fields := map[string]string{}
	email := dtov1.NormalizeEmail(req.Email)
	if email == "" {
		fields["email"] = "is required"
	} else if !dtov1.ValidEmail(email) {
		fields["email"] = "must be a valid email address"
	}
	note := strings.TrimSpace(req.Note)
	if len(note) > 500 {
		fields["note"] = "must be at most 500 characters"
	}
	if len(fields) > 0 {
		return nil, NewValidationError(fields)
	}

	now := s.now().UTC().Truncate(time.Millisecond)
	sup := &entity.Suppression{
		Email:     email,
		Reason:    entity.SuppressionReasonManual,
		Note:      optionalString(note),
		CreatedAt: now,
	}
	if err := s.suppressions.Insert(ctx, sup); err != nil {
		if errors.Is(err, repository.ErrDuplicateKey) {
			return nil, ErrAlreadyExists
		}
		return nil, fmt.Errorf("adding suppression: %w", err)
	}
	resp := dtov1.NewSuppressionResponse(sup)
	return &resp, nil
}

// DeleteSuppression removes a suppression by email. Returns ErrNotFound when no
// suppression matches.
func (s *AdminService) DeleteSuppression(ctx context.Context, email string) error {
	email = dtov1.NormalizeEmail(email)
	if err := s.suppressions.Delete(ctx, email); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("deleting suppression: %w", err)
	}
	return nil
}

// parseLimit parses the limit query value, applying the default and cap. An
// unparseable or out-of-range value records a field error.
func parseLimit(raw string, fields map[string]string) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultAdminLimit
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		fields["limit"] = "must be a positive integer"
		return defaultAdminLimit
	}
	if n > maxAdminLimit {
		return maxAdminLimit
	}
	return n
}

// parseTagFilter splits "key:value" and validates the key, returning the JSON
// path ($."key") and the value.
func parseTagFilter(raw string) (path, value string, ok bool) {
	i := strings.Index(raw, ":")
	if i <= 0 {
		return "", "", false
	}
	key := raw[:i]
	value = raw[i+1:]
	if !tagFilterKeyRe.MatchString(key) || value == "" {
		return "", "", false
	}
	return `$."` + key + `"`, value, true
}

// parseTimeField parses an optional RFC3339 or YYYY-MM-DD value (UTC),
// recording a field error on a malformed one.
func parseTimeField(raw, field string, fields map[string]string) *time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	t, err := parseTimeValue(raw)
	if err != nil {
		fields[field] = "must be RFC3339 or YYYY-MM-DD"
		return nil
	}
	return &t
}

// parseTimeValue accepts a full RFC3339 timestamp or a bare date (interpreted
// as UTC midnight).
func parseTimeValue(raw string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.UTC(), nil
	}
	if t, err := time.Parse("2006-01-02", raw); err == nil {
		return t.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("unrecognized time %q", raw)
}

func validMessageStatus(s string) bool {
	switch s {
	case entity.MessageStatusQueued, entity.MessageStatusSending, entity.MessageStatusSent,
		entity.MessageStatusFailed, entity.MessageStatusSuppressed:
		return true
	}
	return false
}

func validSuppressionReason(s string) bool {
	switch s {
	case entity.SuppressionReasonHardBounce, entity.SuppressionReasonComplaint, entity.SuppressionReasonManual:
		return true
	}
	return false
}

// cursorParts is a decoded keyset cursor: a timestamp and a tie-breaker tail
// (message id or suppression email).
type cursorParts struct {
	at *time.Time
	id string
}

// encodeCursor renders a keyset cursor as base64("<unixMillis>|<tail>").
func encodeCursor(at time.Time, tail string) string {
	raw := strconv.FormatInt(at.UTC().UnixMilli(), 10) + "|" + tail
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// decodeMessageCursor decodes an opaque cursor into (created_at, id). An empty
// cursor is not an error (ok=false, no fields). A malformed one records a field
// error.
func decodeMessageCursor(raw string, fields map[string]string) (cursorParts, bool) {
	return decodeCursor(raw, "cursor", fields)
}

func decodeSuppressionCursor(raw string, fields map[string]string) (cursorParts, bool) {
	return decodeCursor(raw, "cursor", fields)
}

func decodeCursor(raw, field string, fields map[string]string) (cursorParts, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return cursorParts{}, false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		fields[field] = "malformed cursor"
		return cursorParts{}, false
	}
	millis, tail, found := strings.Cut(string(decoded), "|")
	if !found {
		fields[field] = "malformed cursor"
		return cursorParts{}, false
	}
	ms, err := strconv.ParseInt(millis, 10, 64)
	if err != nil {
		fields[field] = "malformed cursor"
		return cursorParts{}, false
	}
	at := time.UnixMilli(ms).UTC()
	return cursorParts{at: &at, id: tail}, true
}
