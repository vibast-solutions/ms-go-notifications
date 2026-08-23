package log

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vibast-solutions/ms-go-notifications/pkg/provider"
)

func TestLogProviderName(t *testing.T) {
	assert.Equal(t, "log", New().Name())
}

func TestLogProviderSendReturnsFakeID(t *testing.T) {
	p := New()
	id, err := p.Send(context.Background(), &provider.OutboundEmail{
		MessageID: "01MSG",
		From:      "no-reply@vibast.ro",
		To:        []string{"jane@example.com"},
		Subject:   "Hi",
		Raw:       []byte("raw"),
	})
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(id, "log-"), "id must be prefixed log-, got %q", id)
	assert.Len(t, id, len("log-")+26, "id must be log- plus a 26-char ULID")

	id2, err := p.Send(context.Background(), &provider.OutboundEmail{MessageID: "01MSG2"})
	require.NoError(t, err)
	assert.NotEqual(t, id, id2, "each send gets a distinct id")
}
