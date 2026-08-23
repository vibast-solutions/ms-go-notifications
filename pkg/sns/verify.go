package sns

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha1" //nolint:gosec // SHA1withRSA is mandated by SNS SignatureVersion 1.
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sync"
)

// certHostPattern is the only host family a signing certificate (or a
// SubscribeURL) may live on. Enforcing it stops an attacker from pointing
// SigningCertURL at a host they control and getting us to trust their key.
var certHostPattern = regexp.MustCompile(`^sns\.[a-z0-9-]+\.amazonaws\.com$`)

// ErrUntrustedCertURL is returned when a SigningCertURL/SubscribeURL is not an
// https URL on an sns.<region>.amazonaws.com host.
var ErrUntrustedCertURL = errors.New("sns: certificate/subscribe url is not a trusted https sns endpoint")

// ErrBadSignature is returned when an envelope's signature does not verify
// against its signing certificate.
var ErrBadSignature = errors.New("sns: signature verification failed")

// Verifier validates SNS message signatures. It downloads and caches the
// signing certificate referenced by each message (after checking the URL is a
// trusted https SNS endpoint) and verifies the RSA signature over the message's
// canonical string. A single Verifier is safe for concurrent use.
type Verifier struct {
	client *http.Client
	cache  sync.Map // certURL(string) -> *rsa.PublicKey

	// fetchKey resolves a (already host-validated) cert URL to its RSA public
	// key. Overridable in tests to avoid a live HTTP fetch; nil uses the
	// default caching HTTP fetch.
	fetchKey func(ctx context.Context, certURL string) (*rsa.PublicKey, error)
}

// NewVerifier returns a Verifier using client for certificate downloads (and
// subscription confirmation). A nil client falls back to http.DefaultClient;
// callers should pass one with a timeout.
func NewVerifier(client *http.Client) *Verifier {
	if client == nil {
		client = http.DefaultClient
	}
	return &Verifier{client: client}
}

// Verify checks env's signature. It validates the signing-cert URL host,
// fetches (and caches) the certificate's public key, rebuilds the canonical
// string for env's type, and verifies the RSA-SHA1 (version 1) or RSA-SHA256
// (version 2) signature. It returns ErrUntrustedCertURL, ErrBadSignature, or a
// wrapped fetch/parse error.
func (v *Verifier) Verify(ctx context.Context, env *Envelope) error {
	if err := validateTrustedURL(env.SigningCertURL); err != nil {
		return err
	}

	fetch := v.fetchKey
	if fetch == nil {
		fetch = v.httpFetchKey
	}
	key, err := fetch(ctx, env.SigningCertURL)
	if err != nil {
		return err
	}

	sig, err := base64.StdEncoding.DecodeString(env.Signature)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrBadSignature, err)
	}

	var (
		hashType crypto.Hash
		digest   []byte
	)
	canonical := env.canonicalString()
	switch env.SignatureVersion {
	case "1":
		sum := sha1.Sum(canonical) //nolint:gosec // required by SNS SignatureVersion 1.
		hashType, digest = crypto.SHA1, sum[:]
	case "2":
		sum := sha256.Sum256(canonical)
		hashType, digest = crypto.SHA256, sum[:]
	default:
		return fmt.Errorf("%w: unsupported SignatureVersion %q", ErrBadSignature, env.SignatureVersion)
	}

	if err := rsa.VerifyPKCS1v15(key, hashType, digest, sig); err != nil {
		return fmt.Errorf("%w: %v", ErrBadSignature, err)
	}
	return nil
}

// httpFetchKey downloads the signing certificate (memoised per URL) and returns
// its RSA public key. The URL host is assumed already validated by Verify.
func (v *Verifier) httpFetchKey(ctx context.Context, certURL string) (*rsa.PublicKey, error) {
	if cached, ok := v.cache.Load(certURL); ok {
		return cached.(*rsa.PublicKey), nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, certURL, nil)
	if err != nil {
		return nil, fmt.Errorf("building cert request: %w", err)
	}
	resp, err := v.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching signing certificate: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching signing certificate: status %d", resp.StatusCode)
	}
	pemBytes, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("reading signing certificate: %w", err)
	}

	key, err := publicKeyFromPEM(pemBytes)
	if err != nil {
		return nil, err
	}
	v.cache.Store(certURL, key)
	return key, nil
}

// publicKeyFromPEM extracts the RSA public key from a PEM-encoded X.509
// certificate.
func publicKeyFromPEM(pemBytes []byte) (*rsa.PublicKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("sns: signing certificate is not valid PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing signing certificate: %w", err)
	}
	key, ok := cert.PublicKey.(*rsa.PublicKey)
	if !ok {
		return nil, errors.New("sns: signing certificate does not carry an RSA public key")
	}
	return key, nil
}

// validateTrustedURL enforces that rawURL is an https URL whose host matches the
// sns.<region>.amazonaws.com pattern, guarding both the cert download and the
// subscription-confirmation fetch against SSRF / key-substitution.
func validateTrustedURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUntrustedCertURL, err)
	}
	if u.Scheme != "https" || !certHostPattern.MatchString(u.Host) {
		return ErrUntrustedCertURL
	}
	return nil
}

// ConfirmSubscription completes a SubscriptionConfirmation by GETting the
// envelope's SubscribeURL (after validating it is a trusted SNS endpoint). SNS
// activates the subscription on that request. The context bounds the call.
func (v *Verifier) ConfirmSubscription(ctx context.Context, env *Envelope) error {
	if err := validateTrustedURL(env.SubscribeURL); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, env.SubscribeURL, nil)
	if err != nil {
		return fmt.Errorf("building subscribe request: %w", err)
	}
	resp, err := v.client.Do(req)
	if err != nil {
		return fmt.Errorf("confirming subscription: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("confirming subscription: status %d", resp.StatusCode)
	}
	return nil
}
