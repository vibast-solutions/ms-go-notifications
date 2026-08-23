package v1

import (
	"time"

	"github.com/vibast-solutions/ms-go-notifications/app/entity"
)

// MessageSummary is one row of the admin message history
// (GET /v1/admin/messages). It carries no bodies — only what a list view needs.
type MessageSummary struct {
	ID             string     `json:"id"`
	Status         string     `json:"status"`
	From           Address    `json:"from"`
	Subject        string     `json:"subject"`
	RecipientCount int        `json:"recipientCount"`
	FirstTo        *string    `json:"firstTo"`
	APIKeyName     string     `json:"apiKeyName"`
	Provider       *string    `json:"provider"`
	Attempts       int        `json:"attempts"`
	CreatedAt      time.Time  `json:"createdAt"`
	SentAt         *time.Time `json:"sentAt"`
}

// MessageListResponse is the paginated admin history payload. NextCursor is
// empty on the last page.
type MessageListResponse struct {
	Items      []MessageSummary `json:"items"`
	NextCursor string           `json:"nextCursor,omitempty"`
}

// AdminAttachmentMeta describes one attachment in the admin detail view.
// Purged is true once the content has been nulled by the cleanup command.
type AdminAttachmentMeta struct {
	ID          string `json:"id"`
	Filename    string `json:"filename"`
	ContentType string `json:"contentType"`
	SizeBytes   int    `json:"sizeBytes"`
	Purged      bool   `json:"purged"`
}

// AdminKeyRef identifies the API key that owns a message.
type AdminKeyRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// AdminMessageResponse is the full admin detail (GET /v1/admin/messages/{id}):
// the consumer GET shape plus the bodies, headers, attachment metadata, owning
// key and lock columns for debugging. Event payloads are returned in full.
type AdminMessageResponse struct {
	EmailResponse
	Text        *string               `json:"text"`
	HTML        *string               `json:"html"`
	Headers     map[string]string     `json:"headers,omitempty"`
	Attachments []AdminAttachmentMeta `json:"attachments"`
	APIKey      AdminKeyRef           `json:"apiKey"`
	LockedAt    *time.Time            `json:"lockedAt"`
	LockedBy    *string               `json:"lockedBy"`
}

// NewAdminMessageResponse maps a message and its children to the admin detail
// shape. It reuses the consumer response for the shared fields, then adds the
// admin-only ones (bodies, headers, attachment metadata, owning key, locks).
func NewAdminMessageResponse(msg *entity.Message, recipients []*entity.MessageRecipient,
	events []*entity.MessageEvent, attachments []*entity.MessageAttachment, keyName string) AdminMessageResponse {

	resp := AdminMessageResponse{
		EmailResponse: NewEmailResponse(msg, recipients, events),
		Text:          msg.TextBody,
		HTML:          msg.HTMLBody,
		Headers:       msg.Headers,
		Attachments:   make([]AdminAttachmentMeta, 0, len(attachments)),
		APIKey:        AdminKeyRef{ID: msg.APIKeyID, Name: keyName},
		LockedAt:      msg.LockedAt,
		LockedBy:      msg.LockedBy,
	}
	for _, a := range attachments {
		resp.Attachments = append(resp.Attachments, AdminAttachmentMeta{
			ID:          a.ID,
			Filename:    a.Filename,
			ContentType: a.ContentType,
			SizeBytes:   a.SizeBytes,
			Purged:      a.PurgedAt != nil,
		})
	}
	return resp
}
