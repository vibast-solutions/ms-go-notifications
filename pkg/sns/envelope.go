package sns

import (
	"encoding/json"
	"fmt"
)

// Message types carried in an SNS envelope's Type field (and mirrored in the
// x-amz-sns-message-type header).
const (
	TypeSubscriptionConfirmation = "SubscriptionConfirmation"
	TypeNotification             = "Notification"
	TypeUnsubscribeConfirmation  = "UnsubscribeConfirmation"
)

// Envelope is the JSON document SNS POSTs to an HTTP(S) subscription. The same
// shape covers all three message types; which fields are populated depends on
// Type. Field names match the SNS wire format exactly (they also drive the
// signature canonical string, so their spelling matters).
type Envelope struct {
	Type             string `json:"Type"`
	MessageId        string `json:"MessageId"`
	Token            string `json:"Token"`        // Subscription/Unsubscribe confirmation
	TopicArn         string `json:"TopicArn"`     //
	Subject          string `json:"Subject"`      // Notification, optional
	Message          string `json:"Message"`      // the payload (SES event JSON for Notification)
	SubscribeURL     string `json:"SubscribeURL"` // Subscription/Unsubscribe confirmation
	Timestamp        string `json:"Timestamp"`
	SignatureVersion string `json:"SignatureVersion"`
	Signature        string `json:"Signature"`
	SigningCertURL   string `json:"SigningCertURL"`
	UnsubscribeURL   string `json:"UnsubscribeURL"` // Notification
}

// Parse decodes an SNS envelope from a raw request body. It returns an error
// for invalid JSON or an unrecognised Type so the caller can answer 400.
func Parse(body []byte) (*Envelope, error) {
	var env Envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("decoding sns envelope: %w", err)
	}
	switch env.Type {
	case TypeSubscriptionConfirmation, TypeNotification, TypeUnsubscribeConfirmation:
	default:
		return nil, fmt.Errorf("unknown sns message type %q", env.Type)
	}
	if env.MessageId == "" || env.TopicArn == "" {
		return nil, fmt.Errorf("sns envelope missing MessageId or TopicArn")
	}
	return &env, nil
}

// canonicalString builds the exact byte string SNS signed, following the
// documented field order for the message type. Each present field contributes
// two lines: the field name and its value, both newline-terminated. Subject is
// included only when present (Notification only).
func (e *Envelope) canonicalString() []byte {
	var b []byte
	add := func(name, value string) {
		b = append(b, name...)
		b = append(b, '\n')
		b = append(b, value...)
		b = append(b, '\n')
	}
	switch e.Type {
	case TypeNotification:
		add("Message", e.Message)
		add("MessageId", e.MessageId)
		if e.Subject != "" {
			add("Subject", e.Subject)
		}
		add("Timestamp", e.Timestamp)
		add("TopicArn", e.TopicArn)
		add("Type", e.Type)
	default: // SubscriptionConfirmation, UnsubscribeConfirmation
		add("Message", e.Message)
		add("MessageId", e.MessageId)
		add("SubscribeURL", e.SubscribeURL)
		add("Timestamp", e.Timestamp)
		add("Token", e.Token)
		add("TopicArn", e.TopicArn)
		add("Type", e.Type)
	}
	return b
}
