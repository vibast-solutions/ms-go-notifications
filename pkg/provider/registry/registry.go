// Package registry constructs the configured email provider. It is the single
// place that knows every concrete implementation, keeping the provider
// interface package free of import cycles.
package registry

import (
	"context"
	"fmt"

	"github.com/vibast-solutions/ms-go-notifications/pkg/provider"
	"github.com/vibast-solutions/ms-go-notifications/pkg/provider/log"
	"github.com/vibast-solutions/ms-go-notifications/pkg/provider/ses"
)

// Options selects and configures the provider. Name is "ses" or "log";
// AWSRegion and SESConfigurationSet apply only to ses.
type Options struct {
	Name                string
	AWSRegion           string
	SESConfigurationSet string
}

// New returns the provider named by opts. It errors on an unknown name or when
// the ses client fails to initialise.
func New(ctx context.Context, opts Options) (provider.EmailProvider, error) {
	switch opts.Name {
	case provider.NameSES:
		return ses.New(ctx, opts.AWSRegion, opts.SESConfigurationSet)
	case provider.NameLog:
		return log.New(), nil
	default:
		return nil, fmt.Errorf("unknown email provider %q (want %q or %q)", opts.Name, provider.NameSES, provider.NameLog)
	}
}
