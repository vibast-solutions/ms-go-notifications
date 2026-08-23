// Package ses is the production email provider: it sends raw MIME messages via
// AWS SES v2 (SendEmail with Content.Raw) and classifies SDK errors into
// retryable / permanent for the worker's retry logic.
package ses

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"
	smithy "github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"github.com/vibast-solutions/ms-go-notifications/pkg/provider"
)

// sendAPI is the subset of the SES v2 client used here; it lets tests inject a
// fake without a live AWS endpoint.
type sendAPI interface {
	SendEmail(context.Context, *sesv2.SendEmailInput, ...func(*sesv2.Options)) (*sesv2.SendEmailOutput, error)
}

// Provider implements provider.EmailProvider against AWS SES v2.
type Provider struct {
	api       sendAPI
	configSet string
}

// New builds a Provider using the default AWS credential chain for region. When
// configSet is non-empty it is attached to every send so SES routes events to
// the configured SNS destination.
func New(ctx context.Context, region, configSet string) (*Provider, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("loading aws config: %w", err)
	}
	return &Provider{api: sesv2.NewFromConfig(cfg), configSet: configSet}, nil
}

// newWithAPI is the test seam: it wires a Provider around a fake sendAPI.
func newWithAPI(api sendAPI, configSet string) *Provider {
	return &Provider{api: api, configSet: configSet}
}

// Name returns the provider identifier stored on messages.
func (p *Provider) Name() string { return "ses" }

// Send delivers msg's raw MIME via SES. Recipients (to+cc+bcc) are passed as
// the envelope destination; Bcc is thus delivered without appearing in the
// message headers. On failure the SDK error is classified into a
// *provider.ProviderError.
func (p *Provider) Send(ctx context.Context, msg *provider.OutboundEmail) (string, error) {
	input := &sesv2.SendEmailInput{
		FromEmailAddress: aws.String(msg.From),
		Destination: &types.Destination{
			ToAddresses:  msg.To,
			CcAddresses:  msg.Cc,
			BccAddresses: msg.Bcc,
		},
		Content: &types.EmailContent{
			Raw: &types.RawMessage{Data: msg.Raw},
		},
	}
	if p.configSet != "" {
		input.ConfigurationSetName = aws.String(p.configSet)
	}

	out, err := p.api.SendEmail(ctx, input)
	if err != nil {
		return "", classify(err)
	}
	return aws.ToString(out.MessageId), nil
}

// throttleCodes are provider error codes that indicate transient overload and
// should be retried.
var throttleCodes = map[string]bool{
	"Throttling":                  true,
	"ThrottlingException":         true,
	"TooManyRequestsException":    true,
	"RequestThrottled":            true,
	"SlowDown":                    true,
	"ServiceUnavailable":          true,
	"ServiceUnavailableException": true,
}

// classify maps an SES/SDK error to a *provider.ProviderError. Retryable:
// throttling, rate/quota limits, server (5xx) faults, timeouts and network
// errors. Permanent: MessageRejected, MailFromDomainNotVerified,
// AccountSuspended, SendingPaused, BadRequest and other 4xx. Unknown errors
// default to retryable (Business Logic §6).
func classify(err error) *provider.ProviderError {
	// Permanent, typed exceptions (all non-throttling 4xx).
	var (
		rejected  *types.MessageRejected
		mailFrom  *types.MailFromDomainNotVerifiedException
		suspended *types.AccountSuspendedException
		paused    *types.SendingPausedException
		badReq    *types.BadRequestException
	)
	switch {
	case errors.As(err, &rejected):
		return permanent("MessageRejected", err)
	case errors.As(err, &mailFrom):
		return permanent("MailFromDomainNotVerified", err)
	case errors.As(err, &suspended):
		return permanent("AccountSuspended", err)
	case errors.As(err, &paused):
		return permanent("SendingPaused", err)
	case errors.As(err, &badReq):
		return permanent("BadRequest", err)
	}

	// Retryable, typed exceptions.
	var (
		tooMany  *types.TooManyRequestsException
		limit    *types.LimitExceededException
		internal *types.InternalServiceErrorException
	)
	switch {
	case errors.As(err, &tooMany):
		return retryable("TooManyRequests", err)
	case errors.As(err, &limit):
		return retryable("LimitExceeded", err)
	case errors.As(err, &internal):
		return retryable("InternalServiceError", err)
	}

	// Generic API error: classify by code and HTTP status.
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		code := apiErr.ErrorCode()
		if throttleCodes[code] {
			return retryable(code, err)
		}
		var respErr *smithyhttp.ResponseError
		if errors.As(err, &respErr) {
			switch status := respErr.HTTPStatusCode(); {
			case status >= 500:
				return retryable(code, err)
			case status >= 400:
				return permanent(code, err)
			}
		}
		if apiErr.ErrorFault() == smithy.FaultServer {
			return retryable(code, err)
		}
		// Unknown API error with no decisive status → default retryable.
		return retryable(code, err)
	}

	// Network errors, timeouts, context cancellation and anything else unknown
	// default to retryable.
	return retryable("", err)
}

func retryable(code string, err error) *provider.ProviderError {
	return &provider.ProviderError{Retryable: true, Code: code, Err: err}
}

func permanent(code string, err error) *provider.ProviderError {
	return &provider.ProviderError{Retryable: false, Code: code, Err: err}
}
