package mime

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testDate = time.Date(2026, 8, 23, 10, 30, 0, 0, time.UTC)

func baseMessage() *Message {
	return &Message{
		MessageID: "01MSGID",
		Date:      testDate,
		From:      Address{Email: "no-reply@vibast.ro", Name: "Vibast"},
		To:        []Address{{Email: "jane@example.com"}},
		Subject:   "Hi there",
	}
}

// parse renders m and parses the output with net/mail, failing the test on any
// error so every case exercises a real RFC 5322 parse.
func parse(t *testing.T, m *Message) *mail.Message {
	t.Helper()
	raw, err := Build(m)
	require.NoError(t, err)
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	require.NoError(t, err, "output must parse as an RFC 5322 message")
	return msg
}

// mediaType returns the parsed Content-Type media type and params.
func mediaType(t *testing.T, msg *mail.Message) (string, map[string]string) {
	t.Helper()
	mt, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	require.NoError(t, err)
	return mt, params
}

// readParts reads all parts of a multipart body given its boundary.
func readParts(t *testing.T, body io.Reader, boundary string) []*part {
	t.Helper()
	r := multipart.NewReader(body, boundary)
	var out []*part
	for {
		p, err := r.NextPart()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		data, err := io.ReadAll(p)
		require.NoError(t, err)
		out = append(out, &part{header: p, data: data})
	}
	return out
}

type part struct {
	header *multipart.Part
	data   []byte
}

func (p *part) mediaType(t *testing.T) (string, map[string]string) {
	t.Helper()
	mt, params, err := mime.ParseMediaType(p.header.Header.Get("Content-Type"))
	require.NoError(t, err)
	return mt, params
}

func TestBuildTextOnly(t *testing.T) {
	m := baseMessage()
	m.Text = "hello world"

	msg := parse(t, m)
	mt, params := mediaType(t, msg)
	assert.Equal(t, "text/plain", mt)
	assert.Equal(t, "utf-8", params["charset"])
	assert.Equal(t, "quoted-printable", msg.Header.Get("Content-Transfer-Encoding"))

	// multipart.Part auto-decodes quoted-printable; the top-level body here is
	// not multipart, so decode by reading via a fresh QP-aware path.
	body, err := io.ReadAll(msg.Body)
	require.NoError(t, err)
	assert.Contains(t, string(body), "hello world")
}

func TestBuildHTMLOnly(t *testing.T) {
	m := baseMessage()
	m.HTML = "<p>hi</p>"

	msg := parse(t, m)
	mt, _ := mediaType(t, msg)
	assert.Equal(t, "text/html", mt)
	body, err := io.ReadAll(msg.Body)
	require.NoError(t, err)
	assert.Contains(t, string(body), "<p>hi</p>")
}

func TestBuildAlternative(t *testing.T) {
	m := baseMessage()
	m.Text = "plain body"
	m.HTML = "<p>rich body</p>"

	msg := parse(t, m)
	mt, params := mediaType(t, msg)
	require.Equal(t, "multipart/alternative", mt)

	parts := readParts(t, msg.Body, params["boundary"])
	require.Len(t, parts, 2)

	t0, _ := parts[0].mediaType(t)
	t1, _ := parts[1].mediaType(t)
	assert.Equal(t, "text/plain", t0)
	assert.Equal(t, "text/html", t1)
	// QP is auto-decoded by multipart.Part.
	assert.Equal(t, "plain body", string(parts[0].data))
	assert.Equal(t, "<p>rich body</p>", string(parts[1].data))
}

func TestBuildWithAttachments(t *testing.T) {
	m := baseMessage()
	m.Text = "see attached"
	m.HTML = "<p>see attached</p>"
	m.Attachments = []Attachment{
		{Filename: "report.pdf", ContentType: "application/pdf", Content: []byte("PDF-BYTES-\x00\x01")},
	}

	msg := parse(t, m)
	mt, params := mediaType(t, msg)
	require.Equal(t, "multipart/mixed", mt)

	parts := readParts(t, msg.Body, params["boundary"])
	require.Len(t, parts, 2, "body entity + one attachment")

	// First part is the alternative body entity.
	bodyMT, bodyParams := parts[0].mediaType(t)
	require.Equal(t, "multipart/alternative", bodyMT)
	altParts := readParts(t, bytes.NewReader(parts[0].data), bodyParams["boundary"])
	require.Len(t, altParts, 2)

	// Second part is the attachment.
	attMT, _ := parts[1].mediaType(t)
	assert.Equal(t, "application/pdf", attMT)
	assert.Equal(t, "base64", parts[1].header.Header.Get("Content-Transfer-Encoding"))
	disp := parts[1].header.Header.Get("Content-Disposition")
	assert.True(t, strings.HasPrefix(disp, "attachment"), "attachment disposition, got %q", disp)

	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(string(parts[1].data), "\r\n", ""))
	require.NoError(t, err)
	assert.Equal(t, []byte("PDF-BYTES-\x00\x01"), decoded)
}

func TestBuildSingleBodyWithAttachment(t *testing.T) {
	m := baseMessage()
	m.Text = "only text"
	m.Attachments = []Attachment{{Filename: "a.txt", ContentType: "text/plain", Content: []byte("hello")}}

	msg := parse(t, m)
	mt, params := mediaType(t, msg)
	require.Equal(t, "multipart/mixed", mt)
	parts := readParts(t, msg.Body, params["boundary"])
	require.Len(t, parts, 2)

	// Body entity is a bare text/plain (no alternative when only one body).
	bodyMT, _ := parts[0].mediaType(t)
	assert.Equal(t, "text/plain", bodyMT)
}

func TestBuildUnicodeSubjectAndNames(t *testing.T) {
	m := baseMessage()
	m.From = Address{Email: "no-reply@vibast.ro", Name: "Ștefan — Vibast"}
	m.To = []Address{{Email: "jane@example.com", Name: "Jäne Doe"}}
	m.Subject = "Café — résumé ☕"
	m.Text = "hi"

	raw, err := Build(m)
	require.NoError(t, err)
	// The raw header block must be ASCII-safe (RFC 2047 encoded-words).
	headerBlock := string(raw[:bytes.Index(raw, []byte("\r\n\r\n"))])
	for i := 0; i < len(headerBlock); i++ {
		require.Lessf(t, headerBlock[i], byte(0x80), "non-ASCII byte in header block at %d", i)
	}

	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	require.NoError(t, err)

	dec := new(mime.WordDecoder)
	subject, err := dec.DecodeHeader(msg.Header.Get("Subject"))
	require.NoError(t, err)
	assert.Equal(t, "Café — résumé ☕", subject)

	from, err := mail.ParseAddress(msg.Header.Get("From"))
	require.NoError(t, err)
	assert.Equal(t, "Ștefan — Vibast", from.Name)
	assert.Equal(t, "no-reply@vibast.ro", from.Address)

	to, err := msg.Header.AddressList("To")
	require.NoError(t, err)
	require.Len(t, to, 1)
	assert.Equal(t, "Jäne Doe", to[0].Name)
}

func TestBuildUnicodeAttachmentFilename(t *testing.T) {
	m := baseMessage()
	m.Text = "hi"
	m.Attachments = []Attachment{{Filename: "rapoárt-2026.txt", ContentType: "text/plain", Content: []byte("x")}}

	msg := parse(t, m)
	_, params := mediaType(t, msg)
	parts := readParts(t, msg.Body, params["boundary"])
	require.Len(t, parts, 2)

	dec := new(mime.WordDecoder)
	_, attParams := parts[1].mediaType(t)
	name, err := dec.DecodeHeader(attParams["name"])
	require.NoError(t, err)
	assert.Equal(t, "rapoárt-2026.txt", name)
}

func TestBuildCustomHeadersAndReplyTo(t *testing.T) {
	m := baseMessage()
	m.Text = "hi"
	m.ReplyTo = []Address{{Email: "support@vibast.ro", Name: "Support"}}
	m.Cc = []Address{{Email: "cc@example.com"}}
	m.Headers = map[string]string{"X-Campaign": "welcome", "X-Ref": "abc123"}

	msg := parse(t, m)
	assert.Equal(t, "welcome", msg.Header.Get("X-Campaign"))
	assert.Equal(t, "abc123", msg.Header.Get("X-Ref"))

	replyTo, err := msg.Header.AddressList("Reply-To")
	require.NoError(t, err)
	require.Len(t, replyTo, 1)
	assert.Equal(t, "support@vibast.ro", replyTo[0].Address)

	cc, err := msg.Header.AddressList("Cc")
	require.NoError(t, err)
	require.Len(t, cc, 1)
}

func TestBuildBccNeverInHeaders(t *testing.T) {
	// The builder has no Bcc field by design; assert the rendered headers carry
	// no Bcc line even with Cc present (guards against a future regression that
	// routes bcc through the header block).
	m := baseMessage()
	m.Text = "hi"
	m.Cc = []Address{{Email: "cc@example.com"}}

	raw, err := Build(m)
	require.NoError(t, err)
	headerBlock := strings.ToLower(string(raw[:bytes.Index(raw, []byte("\r\n\r\n"))]))
	assert.NotContains(t, headerBlock, "\nbcc:")
	assert.False(t, strings.HasPrefix(headerBlock, "bcc:"))
}

func TestBuildRequiredHeadersPresent(t *testing.T) {
	m := baseMessage()
	m.Text = "hi"
	msg := parse(t, m)

	assert.NotEmpty(t, msg.Header.Get("Date"))
	assert.Equal(t, "1.0", msg.Header.Get("MIME-Version"))
	assert.Equal(t, "<01MSGID@vibast.ro>", msg.Header.Get("Message-ID"))
	assert.Equal(t, "Hi there", msg.Header.Get("Subject"))
}
