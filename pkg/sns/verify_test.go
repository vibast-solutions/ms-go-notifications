package sns

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1" //nolint:gosec // matches SignatureVersion 1 under test.
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testKey is a package-level RSA key reused across tests (generation is slow).
var testKey = mustGenerateKey()

func mustGenerateKey() *rsa.PrivateKey {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return k
}

// certPEM builds a self-signed certificate carrying priv's public key.
func certPEM(t *testing.T, priv *rsa.PrivateKey) []byte {
	t.Helper()
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "sns.amazonaws.com"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	require.NoError(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

// signEnvelope signs env's canonical string with priv at the given signature
// version and fills in Signature/SignatureVersion.
func signEnvelope(t *testing.T, priv *rsa.PrivateKey, env *Envelope, version string) {
	t.Helper()
	env.SignatureVersion = version
	canonical := env.canonicalString()

	var (
		hash   crypto.Hash
		digest []byte
	)
	switch version {
	case "1":
		sum := sha1.Sum(canonical) //nolint:gosec // SignatureVersion 1.
		hash, digest = crypto.SHA1, sum[:]
	case "2":
		sum := sha256.Sum256(canonical)
		hash, digest = crypto.SHA256, sum[:]
	default:
		t.Fatalf("unknown version %q", version)
	}
	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, hash, digest)
	require.NoError(t, err)
	env.Signature = base64.StdEncoding.EncodeToString(sig)
}

// testVerifier returns a Verifier whose cert fetch is stubbed to key, so tests
// exercise signature logic without an HTTP fetch. Host validation still runs.
func testVerifier(key *rsa.PublicKey) *Verifier {
	v := NewVerifier(nil)
	v.fetchKey = func(context.Context, string) (*rsa.PublicKey, error) { return key, nil }
	return v
}

func notificationEnvelope() *Envelope {
	return &Envelope{
		Type:           TypeNotification,
		MessageId:      "22b480b4-1111-2222-3333-444455556666",
		TopicArn:       "arn:aws:sns:us-east-1:123456789012:ses-events",
		Message:        `{"eventType":"Delivery"}`,
		Timestamp:      "2026-08-23T10:00:00.000Z",
		SigningCertURL: "https://sns.us-east-1.amazonaws.com/SimpleNotificationService-abc.pem",
	}
}

func TestVerifyValidV1(t *testing.T) {
	env := notificationEnvelope()
	signEnvelope(t, testKey, env, "1")

	require.NoError(t, testVerifier(&testKey.PublicKey).Verify(context.Background(), env))
}

func TestVerifyValidV2(t *testing.T) {
	env := notificationEnvelope()
	signEnvelope(t, testKey, env, "2")

	require.NoError(t, testVerifier(&testKey.PublicKey).Verify(context.Background(), env))
}

func TestVerifyValidSubscriptionConfirmation(t *testing.T) {
	env := &Envelope{
		Type:           TypeSubscriptionConfirmation,
		MessageId:      "sub-1",
		TopicArn:       "arn:aws:sns:us-east-1:123456789012:ses-events",
		Message:        "You have chosen to subscribe",
		Token:          "abcdef",
		SubscribeURL:   "https://sns.us-east-1.amazonaws.com/?Action=ConfirmSubscription",
		Timestamp:      "2026-08-23T10:00:00.000Z",
		SigningCertURL: "https://sns.us-east-1.amazonaws.com/cert.pem",
	}
	signEnvelope(t, testKey, env, "1")

	require.NoError(t, testVerifier(&testKey.PublicKey).Verify(context.Background(), env))
}

func TestVerifyTamperedBodyFails(t *testing.T) {
	env := notificationEnvelope()
	signEnvelope(t, testKey, env, "1")
	env.Message = `{"eventType":"Bounce"}` // change payload after signing

	err := testVerifier(&testKey.PublicKey).Verify(context.Background(), env)
	assert.ErrorIs(t, err, ErrBadSignature)
}

func TestVerifyWrongCertHostFails(t *testing.T) {
	env := notificationEnvelope()
	env.SigningCertURL = "https://evil.example.com/cert.pem"
	signEnvelope(t, testKey, env, "1")

	fetched := false
	v := NewVerifier(nil)
	v.fetchKey = func(context.Context, string) (*rsa.PublicKey, error) {
		fetched = true
		return &testKey.PublicKey, nil
	}

	err := v.Verify(context.Background(), env)
	assert.ErrorIs(t, err, ErrUntrustedCertURL)
	assert.False(t, fetched, "cert must not be fetched from an untrusted host")
}

func TestVerifyHTTPCertURLFails(t *testing.T) {
	env := notificationEnvelope()
	env.SigningCertURL = "http://sns.us-east-1.amazonaws.com/cert.pem" // not https
	signEnvelope(t, testKey, env, "1")

	err := testVerifier(&testKey.PublicKey).Verify(context.Background(), env)
	assert.ErrorIs(t, err, ErrUntrustedCertURL)
}

func TestVerifyWrongKeyFails(t *testing.T) {
	other := mustGenerateKey()
	env := notificationEnvelope()
	signEnvelope(t, testKey, env, "1")

	err := testVerifier(&other.PublicKey).Verify(context.Background(), env)
	assert.ErrorIs(t, err, ErrBadSignature)
}

func TestVerifyUnsupportedSignatureVersion(t *testing.T) {
	env := notificationEnvelope()
	signEnvelope(t, testKey, env, "1")
	env.SignatureVersion = "3"

	err := testVerifier(&testKey.PublicKey).Verify(context.Background(), env)
	assert.ErrorIs(t, err, ErrBadSignature)
}

// TestHTTPFetchKeyDownloadsAndCaches exercises the real cert fetch path (the
// host check lives in Verify, so httpFetchKey accepts the test server URL) and
// confirms the result is cached (a second call succeeds after the server stops).
func TestHTTPFetchKeyDownloadsAndCaches(t *testing.T) {
	pemBytes := certPEM(t, testKey)
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_, _ = w.Write(pemBytes)
	}))

	v := NewVerifier(srv.Client())
	key, err := v.httpFetchKey(context.Background(), srv.URL)
	require.NoError(t, err)
	assert.True(t, key.Equal(&testKey.PublicKey))

	srv.Close() // second call must be served from cache
	key2, err := v.httpFetchKey(context.Background(), srv.URL)
	require.NoError(t, err)
	assert.True(t, key2.Equal(&testKey.PublicKey))
	assert.Equal(t, 1, hits, "certificate should be fetched once and cached")
}

func TestConfirmSubscriptionRejectsUntrustedURL(t *testing.T) {
	env := &Envelope{SubscribeURL: "https://evil.example.com/confirm"}
	err := NewVerifier(nil).ConfirmSubscription(context.Background(), env)
	assert.ErrorIs(t, err, ErrUntrustedCertURL)
}

func TestValidateTrustedURL(t *testing.T) {
	cases := map[string]struct {
		url string
		ok  bool
	}{
		"valid region host":  {"https://sns.us-east-1.amazonaws.com/x.pem", true},
		"valid other region": {"https://sns.eu-west-2.amazonaws.com/x.pem", true},
		"http scheme":        {"http://sns.us-east-1.amazonaws.com/x.pem", false},
		"foreign host":       {"https://sns.us-east-1.amazonaws.com.evil.com/x.pem", false},
		"lookalike":          {"https://not-sns.amazonaws.com/x.pem", false},
		"empty":              {"", false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := validateTrustedURL(tc.url)
			if tc.ok {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}

func TestParseRejectsUnknownType(t *testing.T) {
	_, err := Parse([]byte(`{"Type":"Nonsense","MessageId":"1","TopicArn":"arn"}`))
	assert.Error(t, err)
}

func TestParseRejectsMalformedJSON(t *testing.T) {
	_, err := Parse([]byte(`{not json`))
	assert.Error(t, err)
}

func TestParseNotification(t *testing.T) {
	env, err := Parse([]byte(`{"Type":"Notification","MessageId":"m1","TopicArn":"arn","Message":"{}"}`))
	require.NoError(t, err)
	assert.Equal(t, TypeNotification, env.Type)
	assert.Equal(t, "m1", env.MessageId)
}

func TestTopicAllowList(t *testing.T) {
	arn := "arn:aws:sns:us-east-1:123456789012:ses-events"
	allow := NewTopicAllowList([]string{arn, ""})
	assert.True(t, allow.Allows(arn))
	assert.False(t, allow.Allows("arn:aws:sns:us-east-1:123456789012:other"))

	empty := NewTopicAllowList(nil)
	assert.False(t, empty.Allows(arn), "empty allow-list must fail closed")
}
