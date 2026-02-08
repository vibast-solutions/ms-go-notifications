package service

import "context"

type requestIDKey struct{}

// WithRequestID stores the request ID in the context.
func WithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, requestID)
}

// RequestIDFromContext extracts the request ID from the context.
func RequestIDFromContext(ctx context.Context) (string, bool) {
	value := ctx.Value(requestIDKey{})
	if value == nil {
		return "", false
	}
	requestID, ok := value.(string)
	return requestID, ok
}
