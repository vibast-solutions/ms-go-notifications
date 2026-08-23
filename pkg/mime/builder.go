package mime

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"sort"
	"strings"
	"time"
)

// Address is a name/address pair. Name is optional and, when present, is
// RFC 2047 encoded on output.
type Address struct {
	Name  string
	Email string
}

// Attachment is one file part of the message. Content is the raw bytes; it is
// base64 encoded on output with a Content-Disposition: attachment header.
type Attachment struct {
	Filename    string
	ContentType string
	Content     []byte
}

// Message is the fully validated input to Build. Bcc is intentionally absent:
// blind-carbon recipients are carried by the provider envelope, never in the
// header block. Text and HTML are both optional but at least one must be set
// (the caller validates this upstream).
type Message struct {
	// MessageID is the local part of the Message-ID header; Build wraps it as
	// <MessageID@domain> where domain is taken from From.
	MessageID string
	// Date stamps the Date header. The caller passes it explicitly so the
	// output is deterministic and testable.
	Date time.Time

	From    Address
	To      []Address
	Cc      []Address
	ReplyTo []Address

	Subject string
	Text    string
	HTML    string

	// Headers are custom headers (validated to X-* names, no CR/LF values).
	Headers map[string]string

	Attachments []Attachment
}

// Build renders m as a complete raw MIME message with CRLF line endings,
// suitable for SES SendEmail Content.Raw. The structure is:
//
//   - no attachments, one body        → a single text/plain or text/html entity
//   - no attachments, text + html     → multipart/alternative
//   - attachments                     → multipart/mixed wrapping the body
//     entity (single or alternative) plus one part per attachment
func Build(m *Message) ([]byte, error) {
	bodyType, bodyCTE, body, err := renderBody(m)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	writeHeader(&buf, "Date", m.Date.UTC().Format(time.RFC1123Z))
	writeHeader(&buf, "Message-ID", "<"+m.MessageID+"@"+domainOf(m.From.Email)+">")
	writeHeader(&buf, "From", formatAddress(m.From))
	if h := formatAddressList(m.To); h != "" {
		writeHeader(&buf, "To", h)
	}
	if h := formatAddressList(m.Cc); h != "" {
		writeHeader(&buf, "Cc", h)
	}
	if h := formatAddressList(m.ReplyTo); h != "" {
		writeHeader(&buf, "Reply-To", h)
	}
	writeHeader(&buf, "Subject", mime.QEncoding.Encode("utf-8", m.Subject))
	writeHeader(&buf, "MIME-Version", "1.0")

	// Custom headers, sorted for deterministic output.
	for _, name := range sortedKeys(m.Headers) {
		writeHeader(&buf, name, sanitizeHeaderValue(m.Headers[name]))
	}

	writeHeader(&buf, "Content-Type", bodyType)
	if bodyCTE != "" {
		writeHeader(&buf, "Content-Transfer-Encoding", bodyCTE)
	}
	buf.WriteString("\r\n")
	buf.Write(body)

	return buf.Bytes(), nil
}

// renderBody produces the root entity's Content-Type, its
// Content-Transfer-Encoding (empty for multipart roots), and the body bytes.
func renderBody(m *Message) (contentType, cte string, body []byte, err error) {
	innerType, innerCTE, inner, err := renderContent(m)
	if err != nil {
		return "", "", nil, err
	}
	if len(m.Attachments) == 0 {
		return innerType, innerCTE, inner, nil
	}

	// Wrap the body entity and the attachments in multipart/mixed.
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	bodyHeader := textprotoHeader(innerType, innerCTE)
	part, err := w.CreatePart(bodyHeader)
	if err != nil {
		return "", "", nil, fmt.Errorf("mixed body part: %w", err)
	}
	if _, err := part.Write(inner); err != nil {
		return "", "", nil, fmt.Errorf("writing body part: %w", err)
	}

	for _, att := range m.Attachments {
		if err := writeAttachment(w, att); err != nil {
			return "", "", nil, err
		}
	}
	if err := w.Close(); err != nil {
		return "", "", nil, fmt.Errorf("closing mixed: %w", err)
	}
	return `multipart/mixed; boundary="` + w.Boundary() + `"`, "", buf.Bytes(), nil
}

// renderContent produces the message body entity without attachments: a single
// leaf part when only one of text/html is present, or a multipart/alternative
// when both are.
func renderContent(m *Message) (contentType, cte string, body []byte, err error) {
	hasText := m.Text != ""
	hasHTML := m.HTML != ""

	switch {
	case hasText && hasHTML:
		var buf bytes.Buffer
		w := multipart.NewWriter(&buf)
		if err := writeTextPart(w, "text/plain", m.Text); err != nil {
			return "", "", nil, err
		}
		if err := writeTextPart(w, "text/html", m.HTML); err != nil {
			return "", "", nil, err
		}
		if err := w.Close(); err != nil {
			return "", "", nil, fmt.Errorf("closing alternative: %w", err)
		}
		return `multipart/alternative; boundary="` + w.Boundary() + `"`, "", buf.Bytes(), nil
	case hasHTML:
		return "text/html; charset=utf-8", "quoted-printable", quotedPrintable(m.HTML), nil
	default:
		// text-only (also the fallback when neither is set, which the caller
		// prevents; an empty text/plain is still a valid message).
		return "text/plain; charset=utf-8", "quoted-printable", quotedPrintable(m.Text), nil
	}
}

// writeTextPart writes a quoted-printable text/plain or text/html part into a
// multipart/alternative writer.
func writeTextPart(w *multipart.Writer, mediaType, content string) error {
	part, err := w.CreatePart(textprotoHeader(mediaType+"; charset=utf-8", "quoted-printable"))
	if err != nil {
		return fmt.Errorf("%s part: %w", mediaType, err)
	}
	if _, err := part.Write(quotedPrintable(content)); err != nil {
		return fmt.Errorf("writing %s part: %w", mediaType, err)
	}
	return nil
}

// writeAttachment writes one base64 attachment part with a Content-Disposition
// header. The filename is RFC 2047 encoded when it contains non-ASCII.
func writeAttachment(w *multipart.Writer, att Attachment) error {
	ct := att.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	name := mime.QEncoding.Encode("utf-8", att.Filename)
	header := textprotoHeader(ct+`; name="`+name+`"`, "base64")
	header.Set("Content-Disposition", `attachment; filename="`+name+`"`)

	part, err := w.CreatePart(header)
	if err != nil {
		return fmt.Errorf("attachment part %q: %w", att.Filename, err)
	}
	if _, err := part.Write(base64Wrap(att.Content)); err != nil {
		return fmt.Errorf("writing attachment %q: %w", att.Filename, err)
	}
	return nil
}

// quotedPrintable encodes s as quoted-printable with CRLF line endings.
func quotedPrintable(s string) []byte {
	var buf bytes.Buffer
	w := quotedprintable.NewWriter(&buf)
	_, _ = w.Write([]byte(s))
	_ = w.Close()
	return buf.Bytes()
}

// base64Wrap standard-base64 encodes b and folds it into 76-character lines
// separated by CRLF, as RFC 2045 requires.
func base64Wrap(b []byte) []byte {
	encoded := base64.StdEncoding.EncodeToString(b)
	const lineLen = 76
	var buf bytes.Buffer
	for len(encoded) > lineLen {
		buf.WriteString(encoded[:lineLen])
		buf.WriteString("\r\n")
		encoded = encoded[lineLen:]
	}
	buf.WriteString(encoded)
	return buf.Bytes()
}

// textprotoHeader builds a MIME part header with a Content-Type and optional
// Content-Transfer-Encoding.
func textprotoHeader(contentType, cte string) textproto.MIMEHeader {
	h := textproto.MIMEHeader{}
	h.Set("Content-Type", contentType)
	if cte != "" {
		h.Set("Content-Transfer-Encoding", cte)
	}
	return h
}

// formatAddress renders one address with an optional display name.
func formatAddress(a Address) string {
	return (&mail.Address{Name: a.Name, Address: a.Email}).String()
}

// formatAddressList renders a comma-separated address header value, or "" when
// the list is empty (so the caller can omit the header entirely).
func formatAddressList(addrs []Address) string {
	if len(addrs) == 0 {
		return ""
	}
	parts := make([]string, len(addrs))
	for i, a := range addrs {
		parts[i] = formatAddress(a)
	}
	return strings.Join(parts, ", ")
}

// writeHeader writes a single "Name: value" header line terminated by CRLF.
func writeHeader(buf *bytes.Buffer, name, value string) {
	buf.WriteString(name)
	buf.WriteString(": ")
	buf.WriteString(value)
	buf.WriteString("\r\n")
}

// sanitizeHeaderValue strips CR and LF defensively (values are validated
// upstream, but a raw newline would corrupt the header block / allow injection).
func sanitizeHeaderValue(v string) string {
	return strings.NewReplacer("\r", "", "\n", "").Replace(v)
}

// sortedKeys returns the map keys in ascending order.
func sortedKeys(m map[string]string) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// domainOf returns the domain part of an email address, or "localhost" when it
// has none (keeps Message-ID well-formed for degenerate input).
func domainOf(email string) string {
	if i := strings.LastIndex(email, "@"); i >= 0 && i < len(email)-1 {
		return email[i+1:]
	}
	return "localhost"
}
