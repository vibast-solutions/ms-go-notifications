package service

import "errors"

// Domain errors. Controllers and middleware map these to HTTP statuses;
// repository/driver errors never cross the service boundary unwrapped.
var (
	// ErrInvalidAPIKey covers every authentication failure that is not a
	// revocation: malformed token, unknown prefix, wrong secret.
	ErrInvalidAPIKey = errors.New("invalid api key")
	// ErrAPIKeyRevoked is returned for a well-formed key that has been revoked.
	ErrAPIKeyRevoked = errors.New("api key revoked")
	// ErrNotFound is returned when an id does not resolve to a record.
	ErrNotFound = errors.New("not found")
)

// ValidationError carries field→message details for invalid input, mirroring
// the fields object of the API error envelope.
type ValidationError struct {
	Fields map[string]string
}

// Error implements the error interface.
func (e *ValidationError) Error() string {
	return "validation failed"
}

// NewValidationError builds a ValidationError from field→message pairs.
func NewValidationError(fields map[string]string) *ValidationError {
	return &ValidationError{Fields: fields}
}
