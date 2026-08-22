package v1

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"github.com/vibast-solutions/ms-go-notifications/app/entity"
)

// Fixed validation limits from Business Logic §2 (the config-tunable ones —
// recipients and payload size — arrive via Limits).
const (
	maxSubjectLen        = 998
	maxBodyBytes         = 1 << 20 // 1 MB per text/html body
	maxReplyTo           = 5
	maxHeaders           = 20
	maxAttachments       = 10
	maxFilenameLen       = 255
	maxTags              = 10
	maxTagKeyValueLen    = 64
	maxMetadataBytes     = 4096
	maxIdempotencyKeyLen = 128
)

var tagKeyValueRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// Limits carries the config-tunable size limits applied during validation.
// Injected so the DTO stays free of config imports.
type Limits struct {
	MaxRecipients   int
	MaxPayloadBytes int
}

// Address is a name/address pair. It unmarshals from either an object
// {"email","name"} or a plain string "jane@example.com" (shorthand accepted
// everywhere an address is expected).
type Address struct {
	Email string `json:"email"`
	Name  string `json:"name,omitempty"`
}

// UnmarshalJSON accepts both the object form and the plain-string shorthand.
func (a *Address) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if strings.HasPrefix(trimmed, "\"") {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		a.Email = s
		a.Name = ""
		return nil
	}
	type alias Address
	var obj alias
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	*a = Address(obj)
	return nil
}

// Attachment is one inbound attachment; Content is base64 in the request and
// is decoded during validation (available afterwards via Decoded).
type Attachment struct {
	Filename    string `json:"filename"`
	ContentType string `json:"contentType"`
	Content     string `json:"content"`

	decoded []byte
}

// Decoded returns the raw attachment bytes, populated by Validate.
func (a *Attachment) Decoded() []byte { return a.decoded }

// SendEmailRequest is the POST /v1/emails body. Validate normalizes it in
// place (lower-cased emails, de-duplicated recipients, decoded attachments).
type SendEmailRequest struct {
	From           Address           `json:"from"`
	To             []Address         `json:"to"`
	Cc             []Address         `json:"cc"`
	Bcc            []Address         `json:"bcc"`
	ReplyTo        []Address         `json:"replyTo"`
	Subject        string            `json:"subject"`
	Text           string            `json:"text"`
	HTML           string            `json:"html"`
	Headers        map[string]string `json:"headers"`
	Attachments    []Attachment      `json:"attachments"`
	Tags           map[string]string `json:"tags"`
	Metadata       json.RawMessage   `json:"metadata"`
	IdempotencyKey string            `json:"idempotencyKey"`
}

// ErrorResponse is the standard API error envelope. It is produced by the
// middleware error handler; it lives here so swagger can document it.
type ErrorResponse struct {
	Error   string            `json:"error" example:"validation_error"`
	Message string            `json:"message" example:"one or more fields are invalid"`
	Fields  map[string]string `json:"fields,omitempty"`
}

// SendEmailResponse is the POST /v1/emails reply (202 create / 200 replay).
type SendEmailResponse struct {
	ID        string    `json:"id"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"createdAt"`
}

// RecipientResponse is one recipient in the GET shape (no bodies).
type RecipientResponse struct {
	Type        string     `json:"type"`
	Email       string     `json:"email"`
	Name        string     `json:"name,omitempty"`
	Status      string     `json:"status"`
	LastEventAt *time.Time `json:"lastEventAt"`
}

// EventResponse is one timeline event in the GET shape.
type EventResponse struct {
	Type      string          `json:"type"`
	At        time.Time       `json:"at"`
	Recipient *string         `json:"recipient,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
}

// EmailResponse is the GET /v1/emails/{id} shape: status + timeline, never
// the message bodies or attachment contents.
type EmailResponse struct {
	ID                string              `json:"id"`
	Status            string              `json:"status"`
	Channel           string              `json:"channel"`
	From              Address             `json:"from"`
	Subject           string              `json:"subject"`
	Provider          *string             `json:"provider"`
	ProviderMessageID *string             `json:"providerMessageId"`
	Attempts          int                 `json:"attempts"`
	LastError         *string             `json:"lastError"`
	Tags              map[string]string   `json:"tags,omitempty"`
	Metadata          json.RawMessage     `json:"metadata,omitempty"`
	IdempotencyKey    *string             `json:"idempotencyKey"`
	Recipients        []RecipientResponse `json:"recipients"`
	Events            []EventResponse     `json:"events"`
	CreatedAt         time.Time           `json:"createdAt"`
	SentAt            *time.Time          `json:"sentAt"`
}

// Validate enforces every Business Logic §2 rule that maps to a
// 400 validation_error and normalizes the request in place. It returns nil on
// success or a field→message map on failure. The total decoded payload check
// (413 payload_too_large) and the domain check (403) are enforced by the
// service, not here.
func (r *SendEmailRequest) Validate(lim Limits) map[string]string {
	fields := map[string]string{}

	// from
	r.From.Email = normalizeEmail(r.From.Email)
	if r.From.Email == "" {
		fields["from.email"] = "is required"
	} else if !validEmail(r.From.Email) {
		fields["from.email"] = "must be a valid email address (no display name)"
	}

	// recipients: normalize, validate, de-duplicate case-insensitively across
	// to → cc → bcc (first occurrence wins).
	seen := map[string]bool{}
	r.To = r.normalizeList(r.To, "to", fields, seen)
	r.Cc = r.normalizeList(r.Cc, "cc", fields, seen)
	r.Bcc = r.normalizeList(r.Bcc, "bcc", fields, seen)

	if len(r.To) == 0 {
		fields["to"] = "at least one recipient is required"
	}
	if total := len(r.To) + len(r.Cc) + len(r.Bcc); total > lim.MaxRecipients {
		fields["to"] = fmt.Sprintf("too many recipients: %d (max %d)", total, lim.MaxRecipients)
	}

	// subject
	r.Subject = strings.TrimSpace(r.Subject)
	switch {
	case r.Subject == "":
		fields["subject"] = "is required"
	case len(r.Subject) > maxSubjectLen:
		fields["subject"] = fmt.Sprintf("must be at most %d characters", maxSubjectLen)
	case containsCRLF(r.Subject):
		fields["subject"] = "must not contain line breaks"
	}

	// bodies: at least one, each within the per-body cap.
	if strings.TrimSpace(r.Text) == "" && strings.TrimSpace(r.HTML) == "" {
		fields["text"] = "at least one of text or html is required"
	}
	if len(r.Text) > maxBodyBytes {
		fields["text"] = "must be at most 1 MB"
	}
	if len(r.HTML) > maxBodyBytes {
		fields["html"] = "must be at most 1 MB"
	}

	// replyTo
	if len(r.ReplyTo) > maxReplyTo {
		fields["replyTo"] = fmt.Sprintf("must be at most %d addresses", maxReplyTo)
	}
	for i := range r.ReplyTo {
		r.ReplyTo[i].Email = normalizeEmail(r.ReplyTo[i].Email)
		if !validEmail(r.ReplyTo[i].Email) {
			fields[fmt.Sprintf("replyTo[%d]", i)] = "must be a valid email address"
		}
	}

	r.validateHeaders(fields)
	r.validateAttachments(fields)
	r.validateTags(fields)

	// metadata: valid JSON is guaranteed by json.RawMessage; only size here.
	if len(r.Metadata) > maxMetadataBytes {
		fields["metadata"] = fmt.Sprintf("must be at most %d bytes", maxMetadataBytes)
	}

	// idempotencyKey
	r.IdempotencyKey = strings.TrimSpace(r.IdempotencyKey)
	if len(r.IdempotencyKey) > maxIdempotencyKeyLen {
		fields["idempotencyKey"] = fmt.Sprintf("must be at most %d characters", maxIdempotencyKeyLen)
	}

	if len(fields) == 0 {
		return nil
	}
	return fields
}

// normalizeList lower-cases, validates and de-duplicates one recipient list,
// recording bad addresses in fields. seen carries duplicates across lists.
func (r *SendEmailRequest) normalizeList(list []Address, field string, fields map[string]string, seen map[string]bool) []Address {
	out := make([]Address, 0, len(list))
	for i := range list {
		addr := list[i]
		addr.Email = normalizeEmail(addr.Email)
		if !validEmail(addr.Email) {
			fields[fmt.Sprintf("%s[%d]", field, i)] = "must be a valid email address"
			continue
		}
		if seen[addr.Email] {
			continue
		}
		seen[addr.Email] = true
		out = append(out, addr)
	}
	return out
}

func (r *SendEmailRequest) validateHeaders(fields map[string]string) {
	if len(r.Headers) > maxHeaders {
		fields["headers"] = fmt.Sprintf("must be at most %d entries", maxHeaders)
		return
	}
	for name, value := range r.Headers {
		if !strings.HasPrefix(strings.ToUpper(name), "X-") {
			fields["headers"] = fmt.Sprintf("header %q must start with X-", name)
			return
		}
		if containsCRLF(name) || containsCRLF(value) {
			fields["headers"] = fmt.Sprintf("header %q must not contain line breaks", name)
			return
		}
	}
}

func (r *SendEmailRequest) validateAttachments(fields map[string]string) {
	if len(r.Attachments) > maxAttachments {
		fields["attachments"] = fmt.Sprintf("must be at most %d attachments", maxAttachments)
		return
	}
	for i := range r.Attachments {
		a := &r.Attachments[i]
		key := fmt.Sprintf("attachments[%d]", i)
		if strings.TrimSpace(a.Filename) == "" {
			fields[key+".filename"] = "is required"
			continue
		}
		if len(a.Filename) > maxFilenameLen {
			fields[key+".filename"] = fmt.Sprintf("must be at most %d characters", maxFilenameLen)
			continue
		}
		if strings.ContainsAny(a.Filename, `/\`) {
			fields[key+".filename"] = "must not contain path separators"
			continue
		}
		if strings.TrimSpace(a.ContentType) == "" {
			fields[key+".contentType"] = "is required"
			continue
		}
		decoded, err := base64.StdEncoding.DecodeString(a.Content)
		if err != nil {
			fields[key+".content"] = "must be valid base64"
			continue
		}
		a.decoded = decoded
	}
}

func (r *SendEmailRequest) validateTags(fields map[string]string) {
	if len(r.Tags) > maxTags {
		fields["tags"] = fmt.Sprintf("must be at most %d entries", maxTags)
		return
	}
	for k, v := range r.Tags {
		if len(k) > maxTagKeyValueLen || !tagKeyValueRe.MatchString(k) {
			fields["tags"] = fmt.Sprintf("key %q must be 1-%d chars of [A-Za-z0-9_-]", k, maxTagKeyValueLen)
			return
		}
		if len(v) > maxTagKeyValueLen || !tagKeyValueRe.MatchString(v) {
			fields["tags"] = fmt.Sprintf("value for %q must be 1-%d chars of [A-Za-z0-9_-]", k, maxTagKeyValueLen)
			return
		}
	}
}

// DecodedPayloadBytes returns the decoded size counted against
// MAX_PAYLOAD_BYTES: text + html + decoded attachment contents. Only valid
// after a successful Validate (attachments must be decoded).
func (r *SendEmailRequest) DecodedPayloadBytes() int {
	total := len(r.Text) + len(r.HTML)
	for i := range r.Attachments {
		total += len(r.Attachments[i].decoded)
	}
	return total
}

// normalizeEmail trims and lower-cases an address for comparison and storage.
func normalizeEmail(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// validEmail reports whether s is a bare RFC 5322 addr-spec (no display name).
func validEmail(s string) bool {
	if s == "" {
		return false
	}
	addr, err := mail.ParseAddress(s)
	if err != nil {
		return false
	}
	return addr.Name == "" && addr.Address == s
}

func containsCRLF(s string) bool { return strings.ContainsAny(s, "\r\n") }

// NewEmailResponse maps a message and its children to the GET response shape,
// deliberately omitting bodies and attachment contents.
func NewEmailResponse(msg *entity.Message, recipients []*entity.MessageRecipient, events []*entity.MessageEvent) EmailResponse {
	resp := EmailResponse{
		ID:                msg.ID,
		Status:            msg.Status,
		Channel:           msg.Channel,
		From:              Address{Email: msg.FromEmail},
		Subject:           msg.Subject,
		Provider:          msg.Provider,
		ProviderMessageID: msg.ProviderMessageID,
		Attempts:          msg.Attempts,
		LastError:         msg.LastError,
		Tags:              msg.Tags,
		Metadata:          msg.Metadata,
		IdempotencyKey:    msg.IdempotencyKey,
		Recipients:        make([]RecipientResponse, 0, len(recipients)),
		Events:            make([]EventResponse, 0, len(events)),
		CreatedAt:         msg.CreatedAt,
		SentAt:            msg.SentAt,
	}
	if msg.FromName != nil {
		resp.From.Name = *msg.FromName
	}
	for _, rec := range recipients {
		rr := RecipientResponse{
			Type:        rec.Type,
			Email:       rec.Email,
			Status:      rec.Status,
			LastEventAt: rec.LastEventAt,
		}
		if rec.Name != nil {
			rr.Name = *rec.Name
		}
		resp.Recipients = append(resp.Recipients, rr)
	}
	for _, ev := range events {
		resp.Events = append(resp.Events, EventResponse{
			Type:      ev.Type,
			At:        ev.OccurredAt,
			Recipient: ev.RecipientEmail,
			Data:      ev.Payload,
		})
	}
	return resp
}
