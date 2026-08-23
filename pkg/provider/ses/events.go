package ses

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// SES event types. These match the eventType field of the event-publishing
// format; the legacy notification format (notificationType) only carries
// Delivery / Bounce / Complaint and is normalised onto the same constants.
const (
	EventDelivery      = "Delivery"
	EventBounce        = "Bounce"
	EventComplaint     = "Complaint"
	EventDeliveryDelay = "DeliveryDelay"
	EventReject        = "Reject"
	EventSend          = "Send"
	EventOpen          = "Open"
	EventClick         = "Click"
)

// Bounce types (bounce.bounceType). Only Permanent triggers a suppression.
const (
	BounceTypePermanent = "Permanent"
	BounceTypeTransient = "Transient"
)

// Event is a normalised SES notification decoded from the SNS Message payload.
// It exposes just the fields the ingestion logic needs; the full original JSON
// is kept in Raw for the event timeline.
type Event struct {
	Type      string    // one of the Event* constants; "" if unrecognised
	MessageID string    // mail.messageId — matches messages.provider_message_id
	Timestamp time.Time // event time (falls back to mail.timestamp, then zero)
	Raw       json.RawMessage

	Bounce        *BounceInfo
	Complaint     *ComplaintInfo
	Delivery      *DeliveryInfo
	DeliveryDelay *DeliveryDelayInfo
	Reject        *RejectInfo

	// destination is mail.destination, used as the recipient set for events
	// that do not carry their own recipient list (Send, Reject, Open, Click).
	destination []string
}

// RecipientDetail is a per-recipient effect derived from an event: the new
// status plus any bounce/diagnostic metadata to store on the recipient row.
type RecipientDetail struct {
	Email         string
	BounceType    string // Permanent | Transient (bounce only)
	BounceSubtype string // bounce only
	Diagnostic    string // diagnostic code, complaint feedback type, or delay status
}

// BounceInfo carries the parsed bounce object.
type BounceInfo struct {
	Type       string
	Subtype    string
	Recipients []RecipientDetail
}

// ComplaintInfo carries the parsed complaint object.
type ComplaintInfo struct {
	FeedbackType string
	Recipients   []RecipientDetail
}

// DeliveryInfo carries the parsed delivery object.
type DeliveryInfo struct {
	SMTPResponse string
	Recipients   []RecipientDetail
}

// DeliveryDelayInfo carries the parsed deliveryDelay object.
type DeliveryDelayInfo struct {
	DelayType  string
	Recipients []RecipientDetail
}

// RejectInfo carries the parsed reject object.
type RejectInfo struct {
	Reason string
}

// wire mirrors the subset of the SES event JSON we read, covering both the
// event-publishing format (eventType + typed sub-objects) and the legacy
// notification format (notificationType + the same sub-objects).
type wire struct {
	EventType        string `json:"eventType"`
	NotificationType string `json:"notificationType"`

	Mail struct {
		Timestamp   string   `json:"timestamp"`
		MessageID   string   `json:"messageId"`
		Destination []string `json:"destination"`
	} `json:"mail"`

	Bounce *struct {
		BounceType        string `json:"bounceType"`
		BounceSubType     string `json:"bounceSubType"`
		Timestamp         string `json:"timestamp"`
		BouncedRecipients []struct {
			EmailAddress   string `json:"emailAddress"`
			Action         string `json:"action"`
			Status         string `json:"status"`
			DiagnosticCode string `json:"diagnosticCode"`
		} `json:"bouncedRecipients"`
	} `json:"bounce"`

	Complaint *struct {
		Timestamp             string `json:"timestamp"`
		ComplaintFeedbackType string `json:"complaintFeedbackType"`
		ComplainedRecipients  []struct {
			EmailAddress string `json:"emailAddress"`
		} `json:"complainedRecipients"`
	} `json:"complaint"`

	Delivery *struct {
		Timestamp    string   `json:"timestamp"`
		SMTPResponse string   `json:"smtpResponse"`
		Recipients   []string `json:"recipients"`
	} `json:"delivery"`

	DeliveryDelay *struct {
		DelayType         string `json:"delayType"`
		Timestamp         string `json:"timestamp"`
		DelayedRecipients []struct {
			EmailAddress   string `json:"emailAddress"`
			Status         string `json:"status"`
			DiagnosticCode string `json:"diagnosticCode"`
		} `json:"delayedRecipients"`
	} `json:"deliveryDelay"`

	Reject *struct {
		Reason string `json:"reason"`
	} `json:"reject"`
}

// ParseEvent decodes an SES event from the raw SNS Message payload. It accepts
// both the event-publishing (eventType) and legacy notification
// (notificationType) formats. Unrecognised event types parse successfully with
// Type left empty so callers can still store them on the timeline.
func ParseEvent(raw []byte) (*Event, error) {
	var w wire
	if err := json.Unmarshal(raw, &w); err != nil {
		return nil, fmt.Errorf("decoding ses event: %w", err)
	}

	typ := w.EventType
	if typ == "" {
		typ = w.NotificationType
	}
	if w.Mail.MessageID == "" {
		return nil, fmt.Errorf("ses event missing mail.messageId")
	}

	ev := &Event{
		Type:        typ,
		MessageID:   w.Mail.MessageID,
		Raw:         append(json.RawMessage(nil), raw...),
		destination: w.Mail.Destination,
	}

	switch typ {
	case EventBounce:
		if w.Bounce != nil {
			bi := &BounceInfo{Type: w.Bounce.BounceType, Subtype: w.Bounce.BounceSubType}
			for _, r := range w.Bounce.BouncedRecipients {
				bi.Recipients = append(bi.Recipients, RecipientDetail{
					Email:         r.EmailAddress,
					BounceType:    w.Bounce.BounceType,
					BounceSubtype: w.Bounce.BounceSubType,
					Diagnostic:    r.DiagnosticCode,
				})
			}
			ev.Bounce = bi
			ev.Timestamp = parseTime(w.Bounce.Timestamp)
		}
	case EventComplaint:
		if w.Complaint != nil {
			ci := &ComplaintInfo{FeedbackType: w.Complaint.ComplaintFeedbackType}
			for _, r := range w.Complaint.ComplainedRecipients {
				ci.Recipients = append(ci.Recipients, RecipientDetail{
					Email:      r.EmailAddress,
					Diagnostic: w.Complaint.ComplaintFeedbackType,
				})
			}
			ev.Complaint = ci
			ev.Timestamp = parseTime(w.Complaint.Timestamp)
		}
	case EventDelivery:
		if w.Delivery != nil {
			di := &DeliveryInfo{SMTPResponse: w.Delivery.SMTPResponse}
			for _, addr := range w.Delivery.Recipients {
				di.Recipients = append(di.Recipients, RecipientDetail{Email: addr})
			}
			ev.Delivery = di
			ev.Timestamp = parseTime(w.Delivery.Timestamp)
		}
	case EventDeliveryDelay:
		if w.DeliveryDelay != nil {
			dd := &DeliveryDelayInfo{DelayType: w.DeliveryDelay.DelayType}
			for _, r := range w.DeliveryDelay.DelayedRecipients {
				dd.Recipients = append(dd.Recipients, RecipientDetail{
					Email:      r.EmailAddress,
					Diagnostic: firstNonEmpty(r.DiagnosticCode, r.Status),
				})
			}
			ev.DeliveryDelay = dd
			ev.Timestamp = parseTime(w.DeliveryDelay.Timestamp)
		}
	case EventReject:
		if w.Reject != nil {
			ev.Reject = &RejectInfo{Reason: w.Reject.Reason}
		}
	}

	if ev.Timestamp.IsZero() {
		ev.Timestamp = parseTime(w.Mail.Timestamp)
	}
	return ev, nil
}

// StatusRecipients returns the recipients whose status this event updates, with
// their metadata. It is empty for events that carry no per-recipient status
// change (Send, Reject, Open, Click, unknown).
func (e *Event) StatusRecipients() []RecipientDetail {
	switch e.Type {
	case EventBounce:
		if e.Bounce != nil {
			return e.Bounce.Recipients
		}
	case EventComplaint:
		if e.Complaint != nil {
			return e.Complaint.Recipients
		}
	case EventDelivery:
		if e.Delivery != nil {
			return e.Delivery.Recipients
		}
	case EventDeliveryDelay:
		if e.DeliveryDelay != nil {
			return e.DeliveryDelay.Recipients
		}
	}
	return nil
}

// TimelineRecipients returns the addresses an event should be recorded against
// on the message timeline. For status-changing events these are the affected
// recipients; for the rest (Send, Reject, Open, Click, unknown) it falls back
// to mail.destination so each stored event still has a recipient key for
// dedupe. Returns [""] when no recipient is known, so exactly one event is
// stored (with a NULL recipient).
func (e *Event) TimelineRecipients() []string {
	if details := e.StatusRecipients(); len(details) > 0 {
		out := make([]string, 0, len(details))
		for _, d := range details {
			out = append(out, d.Email)
		}
		return out
	}
	if len(e.destination) > 0 {
		return e.destination
	}
	return []string{""}
}

// parseTime parses an SES RFC 3339 timestamp, tolerating the sub-second
// precision SES uses. A zero time is returned for unparseable input.
func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
