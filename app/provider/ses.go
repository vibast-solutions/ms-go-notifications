package provider

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"
)

type SESProvider struct {
	client *sesv2.Client
	source string
}

// NewSESProvider builds a provider that sends email via AWS SES.
func NewSESProvider(cfg aws.Config, source string) *SESProvider {
	return &SESProvider{
		client: sesv2.NewFromConfig(cfg),
		source: source,
	}
}

// SendRaw sends a raw MIME email via SES.
func (p *SESProvider) SendRaw(ctx context.Context, recipient string, raw []byte) error {
	if recipient == "" {
		return fmt.Errorf("recipient is required")
	}
	if len(raw) == 0 {
		return fmt.Errorf("raw content is required")
	}

	_, err := p.client.SendEmail(ctx, &sesv2.SendEmailInput{
		FromEmailAddress: aws.String(p.source),
		Destination: &types.Destination{
			ToAddresses: []string{recipient},
		},
		Content: &types.EmailContent{
			Raw: &types.RawMessage{Data: raw},
		},
	})
	if err != nil {
		return fmt.Errorf("ses send raw email: %w", err)
	}

	return nil
}
