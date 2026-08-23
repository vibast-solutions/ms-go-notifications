package v1

import "time"

// PauseSendingRequest is the body of POST /v1/admin/sending/pause. The reason is
// optional; when omitted the controller records a default manual reason.
type PauseSendingRequest struct {
	Reason string `json:"reason,omitempty"`
}

// SendingStateResponse is the payload of the /v1/admin/sending endpoints and
// mirrors the circuit-breaker state: whether sending is paused and, when it is,
// why, since when, and who paused it ("auto" or an API key name). since is null
// while sending is running.
type SendingStateResponse struct {
	Paused bool       `json:"paused"`
	Reason string     `json:"reason"`
	Since  *time.Time `json:"since"`
	By     string     `json:"by"`
}
