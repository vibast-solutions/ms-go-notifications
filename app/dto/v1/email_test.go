package v1

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testLimits() Limits { return Limits{MaxRecipients: 50, MaxPayloadBytes: 10 << 20} }

// validRequest returns a minimally valid request that Validate accepts.
func validRequest() SendEmailRequest {
	return SendEmailRequest{
		From:    Address{Email: "no-reply@vibast.ro", Name: "VIBAST"},
		To:      []Address{{Email: "jane@example.com"}},
		Subject: "Hello",
		Text:    "hi there",
	}
}

func TestAddressUnmarshalObjectAndShorthand(t *testing.T) {
	var req SendEmailRequest
	body := `{
		"from": "no-reply@vibast.ro",
		"to": ["jane@example.com", {"email": "john@example.com", "name": "John"}],
		"subject": "s", "text": "t"
	}`
	require.NoError(t, json.Unmarshal([]byte(body), &req))

	assert.Equal(t, "no-reply@vibast.ro", req.From.Email)
	require.Len(t, req.To, 2)
	assert.Equal(t, "jane@example.com", req.To[0].Email)
	assert.Empty(t, req.To[0].Name)
	assert.Equal(t, "john@example.com", req.To[1].Email)
	assert.Equal(t, "John", req.To[1].Name)
}

func TestValidateHappyPathNormalizes(t *testing.T) {
	req := SendEmailRequest{
		From:    Address{Email: "  No-Reply@Vibast.RO  "},
		To:      []Address{{Email: "Jane@Example.com"}},
		Cc:      []Address{{Email: "jane@example.com"}}, // duplicate of To (case-insensitive)
		Subject: "  Hi  ",
		HTML:    "<p>hi</p>",
	}
	fields := req.Validate(testLimits())
	require.Nil(t, fields, "expected valid request, got %v", fields)

	assert.Equal(t, "no-reply@vibast.ro", req.From.Email)
	assert.Equal(t, "jane@example.com", req.To[0].Email)
	assert.Empty(t, req.Cc, "cross-list duplicate must be dropped")
	assert.Equal(t, "Hi", req.Subject)
}

func TestValidateFromRules(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		req := validRequest()
		req.From = Address{}
		assert.Contains(t, req.Validate(testLimits()), "from.email")
	})
	t.Run("invalid", func(t *testing.T) {
		req := validRequest()
		req.From = Address{Email: "not-an-email"}
		assert.Contains(t, req.Validate(testLimits()), "from.email")
	})
	t.Run("display name inside email rejected", func(t *testing.T) {
		req := validRequest()
		req.From = Address{Email: "VIBAST <no-reply@vibast.ro>"}
		assert.Contains(t, req.Validate(testLimits()), "from.email")
	})
}

func TestValidateRecipientRules(t *testing.T) {
	t.Run("no recipients", func(t *testing.T) {
		req := validRequest()
		req.To = nil
		assert.Contains(t, req.Validate(testLimits()), "to")
	})
	t.Run("invalid recipient", func(t *testing.T) {
		req := validRequest()
		req.To = []Address{{Email: "nope"}}
		assert.Contains(t, req.Validate(testLimits()), "to[0]")
	})
	t.Run("too many recipients", func(t *testing.T) {
		req := validRequest()
		lim := Limits{MaxRecipients: 2, MaxPayloadBytes: 10 << 20}
		req.To = []Address{{Email: "a@example.com"}, {Email: "b@example.com"}, {Email: "c@example.com"}}
		assert.Contains(t, req.Validate(lim), "to")
	})
}

func TestValidateSubjectRules(t *testing.T) {
	t.Run("required", func(t *testing.T) {
		req := validRequest()
		req.Subject = "   "
		assert.Contains(t, req.Validate(testLimits()), "subject")
	})
	t.Run("too long", func(t *testing.T) {
		req := validRequest()
		req.Subject = strings.Repeat("x", maxSubjectLen+1)
		assert.Contains(t, req.Validate(testLimits()), "subject")
	})
	t.Run("no line breaks", func(t *testing.T) {
		req := validRequest()
		req.Subject = "line1\nline2"
		assert.Contains(t, req.Validate(testLimits()), "subject")
	})
}

func TestValidateBodyRules(t *testing.T) {
	t.Run("at least one body", func(t *testing.T) {
		req := validRequest()
		req.Text, req.HTML = "", ""
		assert.Contains(t, req.Validate(testLimits()), "text")
	})
	t.Run("text too large", func(t *testing.T) {
		req := validRequest()
		req.Text = strings.Repeat("x", maxBodyBytes+1)
		assert.Contains(t, req.Validate(testLimits()), "text")
	})
	t.Run("html too large", func(t *testing.T) {
		req := validRequest()
		req.Text = ""
		req.HTML = strings.Repeat("x", maxBodyBytes+1)
		assert.Contains(t, req.Validate(testLimits()), "html")
	})
}

func TestValidateReplyTo(t *testing.T) {
	req := validRequest()
	req.ReplyTo = []Address{
		{Email: "a@example.com"}, {Email: "b@example.com"}, {Email: "c@example.com"},
		{Email: "d@example.com"}, {Email: "e@example.com"}, {Email: "f@example.com"},
	}
	assert.Contains(t, req.Validate(testLimits()), "replyTo")

	req2 := validRequest()
	req2.ReplyTo = []Address{{Email: "bad"}}
	assert.Contains(t, req2.Validate(testLimits()), "replyTo[0]")
}

func TestValidateHeaders(t *testing.T) {
	t.Run("must start with X-", func(t *testing.T) {
		req := validRequest()
		req.Headers = map[string]string{"Subject": "spoof"}
		assert.Contains(t, req.Validate(testLimits()), "headers")
	})
	t.Run("X- prefix accepted", func(t *testing.T) {
		req := validRequest()
		req.Headers = map[string]string{"X-Entity-Ref": "contact-form"}
		assert.Nil(t, req.Validate(testLimits()))
	})
	t.Run("no CRLF in value", func(t *testing.T) {
		req := validRequest()
		req.Headers = map[string]string{"X-Bad": "a\r\nInjected: x"}
		assert.Contains(t, req.Validate(testLimits()), "headers")
	})
	t.Run("too many", func(t *testing.T) {
		req := validRequest()
		req.Headers = map[string]string{}
		for i := 0; i < maxHeaders+1; i++ {
			req.Headers["X-H"+string(rune('a'+i))] = "v"
		}
		assert.Contains(t, req.Validate(testLimits()), "headers")
	})
}

func TestValidateAttachments(t *testing.T) {
	good := base64.StdEncoding.EncodeToString([]byte("hello"))

	t.Run("decodes valid base64", func(t *testing.T) {
		req := validRequest()
		req.Attachments = []Attachment{{Filename: "a.txt", ContentType: "text/plain", Content: good}}
		require.Nil(t, req.Validate(testLimits()))
		assert.Equal(t, []byte("hello"), req.Attachments[0].Decoded())
	})
	t.Run("filename required", func(t *testing.T) {
		req := validRequest()
		req.Attachments = []Attachment{{ContentType: "text/plain", Content: good}}
		assert.Contains(t, req.Validate(testLimits()), "attachments[0].filename")
	})
	t.Run("no path separators", func(t *testing.T) {
		req := validRequest()
		req.Attachments = []Attachment{{Filename: "../etc/passwd", ContentType: "text/plain", Content: good}}
		assert.Contains(t, req.Validate(testLimits()), "attachments[0].filename")
	})
	t.Run("contentType required", func(t *testing.T) {
		req := validRequest()
		req.Attachments = []Attachment{{Filename: "a.txt", Content: good}}
		assert.Contains(t, req.Validate(testLimits()), "attachments[0].contentType")
	})
	t.Run("invalid base64", func(t *testing.T) {
		req := validRequest()
		req.Attachments = []Attachment{{Filename: "a.txt", ContentType: "text/plain", Content: "!!!not base64!!!"}}
		assert.Contains(t, req.Validate(testLimits()), "attachments[0].content")
	})
	t.Run("too many", func(t *testing.T) {
		req := validRequest()
		for i := 0; i < maxAttachments+1; i++ {
			req.Attachments = append(req.Attachments, Attachment{Filename: "a.txt", ContentType: "text/plain", Content: good})
		}
		assert.Contains(t, req.Validate(testLimits()), "attachments")
	})
}

func TestValidateTags(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		req := validRequest()
		req.Tags = map[string]string{"app": "vibast-website", "kind": "contact_form-1"}
		assert.Nil(t, req.Validate(testLimits()))
	})
	t.Run("bad chars", func(t *testing.T) {
		req := validRequest()
		req.Tags = map[string]string{"app": "has spaces"}
		assert.Contains(t, req.Validate(testLimits()), "tags")
	})
	t.Run("too many", func(t *testing.T) {
		req := validRequest()
		req.Tags = map[string]string{}
		for i := 0; i < maxTags+1; i++ {
			req.Tags["k"+string(rune('a'+i))] = "v"
		}
		assert.Contains(t, req.Validate(testLimits()), "tags")
	})
}

func TestValidateMetadataAndIdempotency(t *testing.T) {
	t.Run("metadata too large", func(t *testing.T) {
		req := validRequest()
		req.Metadata = json.RawMessage(`{"x":"` + strings.Repeat("y", maxMetadataBytes) + `"}`)
		assert.Contains(t, req.Validate(testLimits()), "metadata")
	})
	t.Run("idempotency key too long", func(t *testing.T) {
		req := validRequest()
		req.IdempotencyKey = strings.Repeat("k", maxIdempotencyKeyLen+1)
		assert.Contains(t, req.Validate(testLimits()), "idempotencyKey")
	})
}

func TestDecodedPayloadBytes(t *testing.T) {
	req := validRequest()
	req.Text = "12345"      // 5
	req.HTML = "1234567890" // 10
	req.Attachments = []Attachment{{
		Filename: "a.bin", ContentType: "application/octet-stream",
		Content: base64.StdEncoding.EncodeToString([]byte("abcdef")), // 6
	}}
	require.Nil(t, req.Validate(testLimits()))
	assert.Equal(t, 21, req.DecodedPayloadBytes())
}
