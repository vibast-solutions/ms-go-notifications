// Package sns parses and verifies AWS SNS webhook envelopes: JSON envelope
// decoding (Parse), RSA signature verification against the message's signing
// certificate (Verifier, SignatureVersion 1 = SHA1withRSA, 2 = SHA256withRSA),
// a trusted-endpoint check on the certificate and subscribe URLs, subscription
// auto-confirmation, and a topic ARN allow-list (TopicAllowList).
package sns
