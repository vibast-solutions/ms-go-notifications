package dto

import (
	"errors"
	"net/mail"
	"strings"

	"github.com/labstack/echo/v4"
	types "github.com/vibast-solutions/ms-go-notifications/app/types"
)

var (
	ErrMissingFields    = errors.New("request_id, recipient, subject, and content are required")
	ErrInvalidRecipient = errors.New("recipient must be a valid email address")
	ErrSubjectTooShort  = errors.New("subject must be at least 4 characters")
	ErrContentTooShort  = errors.New("content must be at least 11 characters")
)

type SendRawRequest struct {
	RequestID string `json:"request_id"`
	Recipient string `json:"recipient"`
	Subject   string `json:"subject"`
	Content   string `json:"content"`
}

// FromEchoContext binds and normalizes a request from Echo.
func FromEchoContext(ctx echo.Context) (SendRawRequest, error) {
	var req SendRawRequest
	if err := ctx.Bind(&req); err != nil {
		return SendRawRequest{}, err
	}
	req.normalize()
	return req, nil
}

// FromGRPC converts and normalizes a gRPC request.
func FromGRPC(req *types.SendRawEmailRequest) SendRawRequest {
	if req == nil {
		return SendRawRequest{}
	}
	dto := SendRawRequest{
		RequestID: req.GetRequestId(),
		Recipient: req.GetRecipient(),
		Subject:   req.GetSubject(),
		Content:   req.GetContent(),
	}
	dto.normalize()
	return dto
}

// Validate checks required fields and format constraints.
func (r *SendRawRequest) Validate() error {
	if r.RequestID == "" || r.Recipient == "" || r.Subject == "" || r.Content == "" {
		return ErrMissingFields
	}
	if _, err := mail.ParseAddress(r.Recipient); err != nil {
		return ErrInvalidRecipient
	}
	if len(r.Subject) < 4 {
		return ErrSubjectTooShort
	}
	if len(r.Content) < 11 {
		return ErrContentTooShort
	}
	return nil
}

// normalize trims whitespace for all fields.
func (r *SendRawRequest) normalize() {
	r.RequestID = strings.TrimSpace(r.RequestID)
	r.Recipient = strings.TrimSpace(r.Recipient)
	r.Subject = strings.TrimSpace(r.Subject)
	r.Content = strings.TrimSpace(r.Content)
}
