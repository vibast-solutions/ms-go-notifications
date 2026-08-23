// Package log is the development email provider: it logs a summary of each
// message and returns a synthetic provider id instead of sending anything.
package log

import (
	"context"
	"strings"

	"github.com/oklog/ulid/v2"
	logrus "github.com/sirupsen/logrus"

	"github.com/vibast-solutions/ms-go-notifications/pkg/provider"
)

// Provider implements provider.EmailProvider by logging instead of sending. It
// is the default in development (EMAIL_PROVIDER=log).
type Provider struct {
	log logrus.FieldLogger
}

// New returns a log Provider writing to the standard logrus logger.
func New() *Provider {
	return &Provider{log: logrus.StandardLogger()}
}

// Name returns the provider identifier stored on messages.
func (p *Provider) Name() string { return "log" }

// Send logs a one-line summary of msg and returns a fake provider message id of
// the form "log-<ulid>". It never fails.
func (p *Provider) Send(_ context.Context, msg *provider.OutboundEmail) (string, error) {
	id := "log-" + strings.ToLower(ulid.Make().String())
	p.log.WithFields(logrus.Fields{
		"provider":            "log",
		"message_id":          msg.MessageID,
		"from":                msg.From,
		"to":                  msg.To,
		"cc":                  msg.Cc,
		"bcc_count":           len(msg.Bcc),
		"subject":             msg.Subject,
		"raw_bytes":           len(msg.Raw),
		"provider_message_id": id,
	}).Info("email sent (log provider)")
	return id, nil
}
