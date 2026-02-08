package preparer

import (
	"context"
	"fmt"
)

type EmailPreparer interface {
	Prepare(ctx context.Context, recipient string, subject string, content string) ([]byte, error)
}

type Message struct {
	Recipient string
	Subject   string
	Content   string
	Raw       []byte
}

type Step interface {
	Prepare(ctx context.Context, msg *Message) error
}

type Chain struct {
	steps []Step
}

// NewChain builds an email preparer chain from steps.
func NewChain(steps ...Step) *Chain {
	return &Chain{steps: steps}
}

// Prepare runs all preparer steps and returns the final raw message.
func (c *Chain) Prepare(ctx context.Context, recipient string, subject string, content string) ([]byte, error) {
	msg := &Message{
		Recipient: recipient,
		Subject:   subject,
		Content:   content,
	}

	for _, step := range c.steps {
		if err := step.Prepare(ctx, msg); err != nil {
			return nil, err
		}
	}

	if len(msg.Raw) == 0 {
		return nil, fmt.Errorf("prepared raw message is empty")
	}

	return msg.Raw, nil
}
