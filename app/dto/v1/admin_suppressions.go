package v1

import (
	"time"

	"github.com/vibast-solutions/ms-go-notifications/app/entity"
)

// SuppressionResponse is one suppression in the admin list/detail shape.
type SuppressionResponse struct {
	Email           string     `json:"email"`
	Reason          string     `json:"reason"`
	SourceMessageID *string    `json:"sourceMessageId"`
	Note            *string    `json:"note"`
	CreatedAt       time.Time  `json:"createdAt"`
	ExpiresAt       *time.Time `json:"expiresAt"`
}

// SuppressionListResponse is the paginated suppression list. NextCursor is
// empty on the last page.
type SuppressionListResponse struct {
	Items      []SuppressionResponse `json:"items"`
	NextCursor string                `json:"nextCursor,omitempty"`
}

// CreateSuppressionRequest is the POST /v1/admin/suppressions body. The reason
// is always "manual" for admin-added entries; only the note is caller-supplied.
type CreateSuppressionRequest struct {
	Email string `json:"email"`
	Note  string `json:"note,omitempty"`
}

// NewSuppressionResponse maps a suppression entity to its response shape.
func NewSuppressionResponse(s *entity.Suppression) SuppressionResponse {
	return SuppressionResponse{
		Email:           s.Email,
		Reason:          s.Reason,
		SourceMessageID: s.SourceMessageID,
		Note:            s.Note,
		CreatedAt:       s.CreatedAt,
		ExpiresAt:       s.ExpiresAt,
	}
}
