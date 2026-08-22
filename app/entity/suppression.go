package entity

import "time"

// Suppression reasons.
const (
	SuppressionReasonHardBounce = "hard_bounce"
	SuppressionReasonComplaint  = "complaint"
	SuppressionReasonManual     = "manual"
)

// Suppression mirrors the suppressions table — addresses that must not be
// sent to, keyed by email.
type Suppression struct {
	Email           string
	Reason          string
	SourceMessageID *string
	Note            *string
	CreatedAt       time.Time
	ExpiresAt       *time.Time
}
