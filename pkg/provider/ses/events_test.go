package ses

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseEventDelivery(t *testing.T) {
	raw := []byte(`{
	  "eventType": "Delivery",
	  "mail": { "timestamp": "2026-08-23T10:00:00.000Z", "messageId": "ses-msg-1",
	            "destination": ["jane@example.com"] },
	  "delivery": { "timestamp": "2026-08-23T10:00:05.000Z", "recipients": ["jane@example.com"],
	                "smtpResponse": "250 2.0.0 OK", "processingTimeMillis": 900 }
	}`)

	ev, err := ParseEvent(raw)
	require.NoError(t, err)
	assert.Equal(t, EventDelivery, ev.Type)
	assert.Equal(t, "ses-msg-1", ev.MessageID)
	require.NotNil(t, ev.Delivery)
	assert.Equal(t, "250 2.0.0 OK", ev.Delivery.SMTPResponse)
	assert.Equal(t, time.Date(2026, 8, 23, 10, 0, 5, 0, time.UTC), ev.Timestamp)

	details := ev.StatusRecipients()
	require.Len(t, details, 1)
	assert.Equal(t, "jane@example.com", details[0].Email)
	assert.Equal(t, []string{"jane@example.com"}, ev.TimelineRecipients())
}

func TestParseEventPermanentBounce(t *testing.T) {
	raw := []byte(`{
	  "eventType": "Bounce",
	  "mail": { "timestamp": "2026-08-23T10:00:00.000Z", "messageId": "ses-msg-2",
	            "destination": ["bad@example.com"] },
	  "bounce": {
	    "bounceType": "Permanent", "bounceSubType": "General",
	    "timestamp": "2026-08-23T10:00:03.000Z",
	    "bouncedRecipients": [
	      { "emailAddress": "bad@example.com", "action": "failed", "status": "5.1.1",
	        "diagnosticCode": "smtp; 550 5.1.1 user unknown" }
	    ]
	  }
	}`)

	ev, err := ParseEvent(raw)
	require.NoError(t, err)
	assert.Equal(t, EventBounce, ev.Type)
	require.NotNil(t, ev.Bounce)
	assert.Equal(t, BounceTypePermanent, ev.Bounce.Type)
	assert.Equal(t, "General", ev.Bounce.Subtype)

	details := ev.StatusRecipients()
	require.Len(t, details, 1)
	assert.Equal(t, "bad@example.com", details[0].Email)
	assert.Equal(t, BounceTypePermanent, details[0].BounceType)
	assert.Equal(t, "General", details[0].BounceSubtype)
	assert.Equal(t, "smtp; 550 5.1.1 user unknown", details[0].Diagnostic)
}

func TestParseEventTransientBounce(t *testing.T) {
	raw := []byte(`{
	  "eventType": "Bounce",
	  "mail": { "messageId": "ses-msg-3", "destination": ["slow@example.com"] },
	  "bounce": {
	    "bounceType": "Transient", "bounceSubType": "MailboxFull",
	    "bouncedRecipients": [ { "emailAddress": "slow@example.com" } ]
	  }
	}`)

	ev, err := ParseEvent(raw)
	require.NoError(t, err)
	require.NotNil(t, ev.Bounce)
	assert.Equal(t, BounceTypeTransient, ev.Bounce.Type)
	require.Len(t, ev.Bounce.Recipients, 1)
	assert.Equal(t, BounceTypeTransient, ev.Bounce.Recipients[0].BounceType)
}

func TestParseEventComplaint(t *testing.T) {
	raw := []byte(`{
	  "eventType": "Complaint",
	  "mail": { "messageId": "ses-msg-4", "destination": ["angry@example.com"] },
	  "complaint": {
	    "timestamp": "2026-08-23T11:00:00.000Z",
	    "complaintFeedbackType": "abuse",
	    "complainedRecipients": [ { "emailAddress": "angry@example.com" } ]
	  }
	}`)

	ev, err := ParseEvent(raw)
	require.NoError(t, err)
	assert.Equal(t, EventComplaint, ev.Type)
	require.NotNil(t, ev.Complaint)
	assert.Equal(t, "abuse", ev.Complaint.FeedbackType)
	details := ev.StatusRecipients()
	require.Len(t, details, 1)
	assert.Equal(t, "abuse", details[0].Diagnostic)
}

func TestParseEventDeliveryDelay(t *testing.T) {
	raw := []byte(`{
	  "eventType": "DeliveryDelay",
	  "mail": { "messageId": "ses-msg-5", "destination": ["wait@example.com"] },
	  "deliveryDelay": {
	    "delayType": "TransientCommunicationFailure",
	    "delayedRecipients": [ { "emailAddress": "wait@example.com", "status": "4.4.1",
	                             "diagnosticCode": "smtp; 421 try later" } ]
	  }
	}`)

	ev, err := ParseEvent(raw)
	require.NoError(t, err)
	assert.Equal(t, EventDeliveryDelay, ev.Type)
	require.NotNil(t, ev.DeliveryDelay)
	assert.Equal(t, "TransientCommunicationFailure", ev.DeliveryDelay.DelayType)
	details := ev.StatusRecipients()
	require.Len(t, details, 1)
	assert.Equal(t, "smtp; 421 try later", details[0].Diagnostic)
}

func TestParseEventReject(t *testing.T) {
	raw := []byte(`{
	  "eventType": "Reject",
	  "mail": { "messageId": "ses-msg-6", "destination": ["a@example.com","b@example.com"] },
	  "reject": { "reason": "Bad content" }
	}`)

	ev, err := ParseEvent(raw)
	require.NoError(t, err)
	assert.Equal(t, EventReject, ev.Type)
	require.NotNil(t, ev.Reject)
	assert.Equal(t, "Bad content", ev.Reject.Reason)
	assert.Nil(t, ev.StatusRecipients())
	// Reject has no per-recipient status, so the timeline falls back to
	// mail.destination for the event rows.
	assert.Equal(t, []string{"a@example.com", "b@example.com"}, ev.TimelineRecipients())
}

// TestParseLegacyNotificationFormat covers the older SES → SNS format that uses
// notificationType instead of eventType.
func TestParseLegacyNotificationFormat(t *testing.T) {
	raw := []byte(`{
	  "notificationType": "Bounce",
	  "mail": { "messageId": "ses-msg-7", "destination": ["bad@example.com"] },
	  "bounce": {
	    "bounceType": "Permanent", "bounceSubType": "General",
	    "bouncedRecipients": [ { "emailAddress": "bad@example.com" } ]
	  }
	}`)

	ev, err := ParseEvent(raw)
	require.NoError(t, err)
	assert.Equal(t, EventBounce, ev.Type)
	require.NotNil(t, ev.Bounce)
	assert.Equal(t, BounceTypePermanent, ev.Bounce.Type)
}

func TestParseEventUnknownType(t *testing.T) {
	raw := []byte(`{ "eventType": "Open",
	  "mail": { "messageId": "ses-msg-8", "destination": ["jane@example.com"] } }`)

	ev, err := ParseEvent(raw)
	require.NoError(t, err)
	assert.Equal(t, EventOpen, ev.Type)
	assert.Nil(t, ev.StatusRecipients())
	assert.Equal(t, []string{"jane@example.com"}, ev.TimelineRecipients())
}

func TestParseEventMissingMessageID(t *testing.T) {
	_, err := ParseEvent([]byte(`{ "eventType": "Delivery", "mail": { "destination": [] } }`))
	assert.Error(t, err)
}

func TestParseEventMalformedJSON(t *testing.T) {
	_, err := ParseEvent([]byte(`{ not json`))
	assert.Error(t, err)
}

func TestTimelineRecipientsEmptyFallback(t *testing.T) {
	// No status recipients and no destination → a single NULL-recipient event.
	ev, err := ParseEvent([]byte(`{ "eventType": "Send", "mail": { "messageId": "m" } }`))
	require.NoError(t, err)
	assert.Equal(t, []string{""}, ev.TimelineRecipients())
}
