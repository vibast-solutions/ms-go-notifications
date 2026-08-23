// Package mime builds raw RFC 5322 / MIME messages (text/html alternatives,
// attachments, custom headers, reply-to) for providers that send raw content
// (SES SendEmail with Content.Raw). Bcc recipients are delivered via the
// envelope only and never appear in the header block.
package mime
