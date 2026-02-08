package preparer

import (
	"context"
	"fmt"
	"strings"
)

type RawPreparer struct {
	source string
}

// NewRawPreparer creates a preparer that builds a raw MIME message.
func NewRawPreparer(source string) *RawPreparer {
	return &RawPreparer{source: source}
}

// Prepare builds a basic HTML MIME message with headers.
func (p *RawPreparer) Prepare(_ context.Context, msg *Message) error {
	if strings.TrimSpace(p.source) == "" {
		return fmt.Errorf("source email is required")
	}
	if strings.TrimSpace(msg.Recipient) == "" {
		return fmt.Errorf("recipient is required")
	}
	if strings.TrimSpace(msg.Subject) == "" {
		return fmt.Errorf("subject is required")
	}
	if strings.ContainsAny(msg.Subject, "\r\n") {
		return fmt.Errorf("subject contains invalid characters")
	}

	var b strings.Builder
	b.WriteString("From: ")
	b.WriteString(p.source)
	b.WriteString("\r\n")
	b.WriteString("To: ")
	b.WriteString(msg.Recipient)
	b.WriteString("\r\n")
	b.WriteString("Subject: ")
	b.WriteString(msg.Subject)
	b.WriteString("\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 7bit\r\n")
	b.WriteString("\r\n")
	b.WriteString(msg.Content)

	msg.Raw = []byte(b.String())
	return nil
}
