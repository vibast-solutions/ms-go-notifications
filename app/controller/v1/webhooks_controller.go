package v1

import (
	"context"
	"io"
	"net/http"

	"github.com/labstack/echo/v5"
	log "github.com/sirupsen/logrus"

	"github.com/vibast-solutions/ms-go-notifications/pkg/provider/ses"
	"github.com/vibast-solutions/ms-go-notifications/pkg/sns"
)

// maxSNSBodyBytes caps the webhook body. SNS envelopes are small (a few KB);
// 256 KB is generous headroom and bounds memory per request.
const maxSNSBodyBytes = 256 * 1024

// SNSVerifier verifies SNS message signatures and confirms subscriptions.
// Implemented by *sns.Verifier.
type SNSVerifier interface {
	Verify(ctx context.Context, env *sns.Envelope) error
	ConfirmSubscription(ctx context.Context, env *sns.Envelope) error
}

// TopicAllowList reports whether an SNS topic ARN is accepted. Implemented by
// sns.TopicAllowList.
type TopicAllowList interface {
	Allows(arn string) bool
}

// SESEventHandler applies a parsed SES event. Implemented by
// *service.EventService.
type SESEventHandler interface {
	HandleSESEvent(ctx context.Context, snsMessageID string, ev *ses.Event) error
}

// WebhooksController serves the SNS webhook that carries SES delivery events.
// It has no bearer auth: the SNS signature is the credential.
type WebhooksController struct {
	verifier SNSVerifier
	topics   TopicAllowList
	events   SESEventHandler
	log      log.FieldLogger
}

// NewWebhooksController wires the webhook controller.
func NewWebhooksController(verifier SNSVerifier, topics TopicAllowList,
	events SESEventHandler, logger log.FieldLogger) *WebhooksController {
	if logger == nil {
		logger = log.StandardLogger()
	}
	return &WebhooksController{verifier: verifier, topics: topics, events: events, log: logger}
}

// SES handles POST /v1/webhooks/ses.
//
//	@Summary		SES event webhook (SNS)
//	@Description	Receives AWS SNS deliveries carrying SES events. The SNS signature is verified and the topic ARN checked against the allow-list; there is no bearer auth. SubscriptionConfirmation messages are auto-confirmed; Notification messages update message/recipient state. Returns 200 once a message is verified (even for an unknown message id), 403 on a bad signature or disallowed topic, and 400 on a malformed body.
//	@Tags			webhooks
//	@Accept			json
//	@Produce		json
//	@Success		200	"Accepted"
//	@Failure		400	"Malformed SNS envelope"
//	@Failure		403	"Bad signature or disallowed topic"
//	@Failure		500	"Internal error"
//	@Router			/webhooks/ses [post]
func (h *WebhooksController) SES(c *echo.Context) error {
	ctx := c.Request().Context()

	body, err := io.ReadAll(io.LimitReader(c.Request().Body, maxSNSBodyBytes+1))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "cannot read request body")
	}
	if len(body) > maxSNSBodyBytes {
		return echo.NewHTTPError(http.StatusBadRequest, "request body too large")
	}

	env, err := sns.Parse(body)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "malformed sns envelope")
	}

	// Fail closed on an unknown topic, and never trust an unverified signature.
	if !h.topics.Allows(env.TopicArn) {
		h.log.WithField("topic_arn", env.TopicArn).Warn("sns message from disallowed topic")
		return echo.NewHTTPError(http.StatusForbidden, "topic not allowed")
	}
	if err := h.verifier.Verify(ctx, env); err != nil {
		h.log.WithError(err).WithField("topic_arn", env.TopicArn).Warn("sns signature verification failed")
		return echo.NewHTTPError(http.StatusForbidden, "signature verification failed")
	}

	// Fast dispatch on the header, falling back to the (signed) envelope Type.
	msgType := c.Request().Header.Get("x-amz-sns-message-type")
	if msgType == "" {
		msgType = env.Type
	}

	switch msgType {
	case sns.TypeSubscriptionConfirmation:
		return h.confirm(c, env)
	case sns.TypeNotification:
		return h.notify(c, env)
	case sns.TypeUnsubscribeConfirmation:
		h.log.WithField("topic_arn", env.TopicArn).Info("sns unsubscribe confirmation received")
		return c.NoContent(http.StatusOK)
	default:
		return echo.NewHTTPError(http.StatusBadRequest, "unsupported sns message type")
	}
}

// confirm auto-confirms an SNS subscription by fetching its SubscribeURL. A
// failure returns 500 so SNS retries the confirmation.
func (h *WebhooksController) confirm(c *echo.Context, env *sns.Envelope) error {
	if err := h.verifier.ConfirmSubscription(c.Request().Context(), env); err != nil {
		h.log.WithError(err).WithField("topic_arn", env.TopicArn).Error("confirming sns subscription")
		return err
	}
	h.log.WithField("topic_arn", env.TopicArn).Info("sns subscription confirmed")
	return c.NoContent(http.StatusOK)
}

// notify parses the SES event carried in a verified Notification and applies it.
// Once verified the response is always 200 (even for an unknown message id or an
// unparseable payload) so SNS does not retry a message we can never accept; only
// an infrastructure failure while applying the event returns 500 to trigger a
// retry.
func (h *WebhooksController) notify(c *echo.Context, env *sns.Envelope) error {
	event, err := ses.ParseEvent([]byte(env.Message))
	if err != nil {
		h.log.WithError(err).WithField("sns_message_id", env.MessageId).
			Warn("dropping unparseable ses event")
		return c.NoContent(http.StatusOK)
	}

	if err := h.events.HandleSESEvent(c.Request().Context(), env.MessageId, event); err != nil {
		h.log.WithError(err).WithField("sns_message_id", env.MessageId).
			Error("handling ses event")
		return err // 500 → SNS retries
	}
	return c.NoContent(http.StatusOK)
}
