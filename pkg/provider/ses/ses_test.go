package ses

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"
	smithy "github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vibast-solutions/ms-go-notifications/pkg/provider"
)

// fakeSES returns a fixed output/error from SendEmail and records the input.
type fakeSES struct {
	out *sesv2.SendEmailOutput
	err error
	in  *sesv2.SendEmailInput
}

func (f *fakeSES) SendEmail(_ context.Context, in *sesv2.SendEmailInput, _ ...func(*sesv2.Options)) (*sesv2.SendEmailOutput, error) {
	f.in = in
	return f.out, f.err
}

func responseErr(status int, code string) error {
	return &smithyhttp.ResponseError{
		Response: &smithyhttp.Response{Response: &http.Response{StatusCode: status}},
		Err:      &smithy.GenericAPIError{Code: code, Message: "boom"},
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		retryable bool
		code      string
	}{
		{"message rejected", &types.MessageRejected{Message: aws.String("bad")}, false, "MessageRejected"},
		{"mail from not verified", &types.MailFromDomainNotVerifiedException{Message: aws.String("x")}, false, "MailFromDomainNotVerified"},
		{"account suspended", &types.AccountSuspendedException{Message: aws.String("x")}, false, "AccountSuspended"},
		{"sending paused", &types.SendingPausedException{Message: aws.String("x")}, false, "SendingPaused"},
		{"bad request", &types.BadRequestException{Message: aws.String("x")}, false, "BadRequest"},
		{"too many requests", &types.TooManyRequestsException{Message: aws.String("x")}, true, "TooManyRequests"},
		{"limit exceeded", &types.LimitExceededException{Message: aws.String("x")}, true, "LimitExceeded"},
		{"internal service error", &types.InternalServiceErrorException{Message: aws.String("x")}, true, "InternalServiceError"},
		{"throttling by code", &smithy.GenericAPIError{Code: "ThrottlingException", Message: "slow"}, true, "ThrottlingException"},
		{"5xx response", responseErr(503, "ServiceError"), true, "ServiceError"},
		{"other 4xx response", responseErr(404, "NotFound"), false, "NotFound"},
		{"server fault api error", &smithy.GenericAPIError{Code: "Unknown", Fault: smithy.FaultServer}, true, "Unknown"},
		{"network error defaults retryable", errors.New("dial tcp: i/o timeout"), true, ""},
		{"context deadline defaults retryable", context.DeadlineExceeded, true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pe := classify(tc.err)
			require.NotNil(t, pe)
			assert.Equal(t, tc.retryable, pe.Retryable, "retryable classification")
			assert.Equal(t, tc.code, pe.Code, "error code")
			assert.ErrorIs(t, pe, tc.err, "wraps the original error")
		})
	}
}

func TestSendSuccessReturnsMessageID(t *testing.T) {
	fake := &fakeSES{out: &sesv2.SendEmailOutput{MessageId: aws.String("ses-123")}}
	p := newWithAPI(fake, "my-config-set")

	id, err := p.Send(context.Background(), &provider.OutboundEmail{
		From: "no-reply@vibast.ro",
		To:   []string{"jane@example.com"},
		Cc:   []string{"cc@example.com"},
		Bcc:  []string{"bcc@example.com"},
		Raw:  []byte("raw message"),
	})
	require.NoError(t, err)
	assert.Equal(t, "ses-123", id)

	// Envelope carries all recipients; config set is applied; raw is passed through.
	require.NotNil(t, fake.in)
	assert.Equal(t, "no-reply@vibast.ro", aws.ToString(fake.in.FromEmailAddress))
	assert.Equal(t, []string{"jane@example.com"}, fake.in.Destination.ToAddresses)
	assert.Equal(t, []string{"cc@example.com"}, fake.in.Destination.CcAddresses)
	assert.Equal(t, []string{"bcc@example.com"}, fake.in.Destination.BccAddresses)
	assert.Equal(t, "my-config-set", aws.ToString(fake.in.ConfigurationSetName))
	assert.Equal(t, []byte("raw message"), fake.in.Content.Raw.Data)
}

func TestSendNoConfigSetOmitsIt(t *testing.T) {
	fake := &fakeSES{out: &sesv2.SendEmailOutput{MessageId: aws.String("id")}}
	p := newWithAPI(fake, "")
	_, err := p.Send(context.Background(), &provider.OutboundEmail{From: "a@b.co", To: []string{"c@d.co"}, Raw: []byte("x")})
	require.NoError(t, err)
	assert.Nil(t, fake.in.ConfigurationSetName)
}

func TestSendErrorIsClassified(t *testing.T) {
	fake := &fakeSES{err: &types.MessageRejected{Message: aws.String("nope")}}
	p := newWithAPI(fake, "")

	_, err := p.Send(context.Background(), &provider.OutboundEmail{From: "a@b.co", To: []string{"c@d.co"}, Raw: []byte("x")})
	var pe *provider.ProviderError
	require.ErrorAs(t, err, &pe)
	assert.False(t, pe.Retryable)
	assert.Equal(t, "MessageRejected", pe.Code)
}

func TestNameIsSES(t *testing.T) {
	assert.Equal(t, "ses", (&Provider{}).Name())
}
