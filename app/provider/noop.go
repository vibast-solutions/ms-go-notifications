package provider

import "context"

// NoopProvider is a stubbed provider that pretends to send emails.
type NoopProvider struct{}

// NewNoopProvider constructs a no-op email provider.
func NewNoopProvider() *NoopProvider {
	return &NoopProvider{}
}

// SendRaw returns nil without sending.
func (p *NoopProvider) SendRaw(_ context.Context, _ string, _ []byte) error {
	return nil
}
