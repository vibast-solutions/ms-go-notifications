// Package provider defines the outbound email provider abstraction. Concrete
// implementations (ses, log) and the registry arrive with VIB-9.
package provider

import "context"

// Provider names, matching the EMAIL_PROVIDER config values.
const (
	NameSES = "ses"
	NameLog = "log"
)

// OutboundEmail is the fully built message handed to a provider. Raw holds the
// complete RFC 5322 message (built by pkg/mime); the envelope fields carry the
// recipients so raw-mode providers (SES) address the message without parsing
// the headers. Bcc is delivered via the envelope only — it is never present in
// Raw's header block.
type OutboundEmail struct {
	MessageID string
	From      string
	To        []string
	Cc        []string
	Bcc       []string
	ReplyTo   []string
	Subject   string
	Raw       []byte // complete raw MIME message
}

// ProviderError wraps a provider failure with retry classification. Unknown
// errors are classified retryable by the concrete providers.
type ProviderError struct {
	Retryable bool
	Code      string
	Err       error
}

func (e *ProviderError) Error() string {
	msg := "provider error"
	if e.Code != "" {
		msg += " [" + e.Code + "]"
	}
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	return msg
}

func (e *ProviderError) Unwrap() error {
	return e.Err
}

// EmailProvider delivers one message and returns the provider's message id.
// Errors must be wrapped as *ProviderError.
type EmailProvider interface {
	Name() string
	Send(ctx context.Context, msg *OutboundEmail) (providerMessageID string, err error)
}
