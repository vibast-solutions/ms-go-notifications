package provider

import "context"

type EmailProvider interface {
	SendRaw(ctx context.Context, recipient string, raw []byte) error
}
