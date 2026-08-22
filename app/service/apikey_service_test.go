package service

import (
	"context"
	"database/sql"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vibast-solutions/ms-go-notifications/app/entity"
)

var allowedDomains = []string{"vibast.com", "alextools.ro"}

// fakeKeyRepo is an in-memory APIKeyRepository counting FindByPrefix calls
// (for cache assertions).
type fakeKeyRepo struct {
	keys      map[string]*entity.APIKey // by prefix
	findCalls int
}

func newFakeKeyRepo() *fakeKeyRepo {
	return &fakeKeyRepo{keys: map[string]*entity.APIKey{}}
}

func (f *fakeKeyRepo) Create(_ context.Context, key *entity.APIKey, domains []string) error {
	cp := *key
	cp.Domains = append([]string(nil), domains...)
	f.keys[key.KeyPrefix] = &cp
	return nil
}

func (f *fakeKeyRepo) FindByPrefix(_ context.Context, prefix string) (*entity.APIKey, error) {
	f.findCalls++
	key, ok := f.keys[prefix]
	if !ok {
		return nil, sql.ErrNoRows
	}
	return key, nil
}

func (f *fakeKeyRepo) List(_ context.Context) ([]*entity.APIKey, error) {
	var out []*entity.APIKey
	for _, k := range f.keys {
		out = append(out, k)
	}
	return out, nil
}

func (f *fakeKeyRepo) Revoke(_ context.Context, id string, at time.Time) error {
	for _, k := range f.keys {
		if k.ID == id && k.Status == entity.APIKeyStatusActive {
			k.Status = entity.APIKeyStatusRevoked
			k.RevokedAt = &at
			return nil
		}
	}
	return sql.ErrNoRows
}

func (f *fakeKeyRepo) ReplaceDomains(_ context.Context, id string, domains []string, _ time.Time) error {
	for _, k := range f.keys {
		if k.ID == id {
			k.Domains = append([]string(nil), domains...)
			return nil
		}
	}
	return sql.ErrNoRows
}

func (f *fakeKeyRepo) TouchLastUsed(_ context.Context, id string, at time.Time) error {
	for _, k := range f.keys {
		if k.ID == id {
			k.LastUsedAt = &at
			return nil
		}
	}
	return sql.ErrNoRows
}

func newTestService(t *testing.T) (*APIKeyService, *fakeKeyRepo) {
	t.Helper()
	repo := newFakeKeyRepo()
	return NewAPIKeyService(repo, allowedDomains), repo
}

func requireValidationField(t *testing.T, err error, field string) {
	t.Helper()
	var ve *ValidationError
	require.ErrorAs(t, err, &ve)
	assert.Contains(t, ve.Fields, field)
}

func TestGenerateFormatAndUniqueness(t *testing.T) {
	svc, _ := newTestService(t)
	format := regexp.MustCompile(`^ntf_[a-z0-9]{8}_[A-Za-z0-9]{32}$`)

	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		plaintext, key, err := svc.Generate(context.Background(), "test key", []string{"send"}, []string{"vibast.com"})
		require.NoError(t, err)
		assert.Regexp(t, format, plaintext)
		assert.False(t, seen[plaintext], "generated key must be unique")
		seen[plaintext] = true

		assert.Len(t, key.KeyPrefix, 8)
		assert.Len(t, key.KeyHash, 64)
		assert.NotContains(t, key.KeyHash, plaintext[len(plaintext)-32:])
		assert.Equal(t, entity.APIKeyStatusActive, key.Status)
		assert.Equal(t, []string{"vibast.com"}, key.Domains)
	}
}

func TestGenerateNormalizesInput(t *testing.T) {
	svc, _ := newTestService(t)

	_, key, err := svc.Generate(context.Background(), "  spaced  ",
		[]string{"Send", "send"}, []string{"VIBAST.com", "vibast.com "})
	require.NoError(t, err)
	assert.Equal(t, "spaced", key.Name)
	assert.Equal(t, []string{"send"}, key.Scopes)
	assert.Equal(t, []string{"vibast.com"}, key.Domains)
}

func TestGenerateValidation(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	cases := []struct {
		name    string
		keyName string
		scopes  []string
		domains []string
		field   string
	}{
		{"empty name", "", []string{"send"}, []string{"vibast.com"}, "name"},
		{"name too long", string(make([]byte, 101)), []string{"send"}, []string{"vibast.com"}, "name"},
		{"no scopes", "k", nil, nil, "scopes"},
		{"unknown scope", "k", []string{"root"}, nil, "scopes"},
		{"send without domains", "k", []string{"send"}, nil, "domains"},
		{"domain not allowed", "k", []string{"send"}, []string{"evil.com"}, "domains"},
		{"admin-only with domains", "k", []string{"admin"}, []string{"vibast.com"}, "domains"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := svc.Generate(ctx, tc.keyName, tc.scopes, tc.domains)
			requireValidationField(t, err, tc.field)
		})
	}
}

func TestAuthenticateHappyPath(t *testing.T) {
	svc, _ := newTestService(t)
	plaintext, created, err := svc.Generate(context.Background(), "k", []string{"send", "admin"}, []string{"vibast.com"})
	require.NoError(t, err)

	key, err := svc.Authenticate(context.Background(), plaintext)
	require.NoError(t, err)
	assert.Equal(t, created.ID, key.ID)
	assert.Equal(t, []string{"send", "admin"}, key.Scopes)
	assert.Equal(t, []string{"vibast.com"}, key.Domains)
}

func TestAuthenticateFailures(t *testing.T) {
	svc, _ := newTestService(t)
	plaintext, _, err := svc.Generate(context.Background(), "k", []string{"send"}, []string{"vibast.com"})
	require.NoError(t, err)

	t.Run("malformed token", func(t *testing.T) {
		_, err := svc.Authenticate(context.Background(), "not-a-key")
		assert.ErrorIs(t, err, ErrInvalidAPIKey)
	})
	t.Run("unknown prefix", func(t *testing.T) {
		unknown := "ntf_zzzzzzzz_" + plaintext[len(plaintext)-32:]
		_, err := svc.Authenticate(context.Background(), unknown)
		assert.ErrorIs(t, err, ErrInvalidAPIKey)
	})
	t.Run("wrong secret with valid prefix", func(t *testing.T) {
		wrong := plaintext[:len(plaintext)-32] + "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
		_, err := svc.Authenticate(context.Background(), wrong)
		assert.ErrorIs(t, err, ErrInvalidAPIKey)
	})
}

func TestAuthenticateRevokedKey(t *testing.T) {
	svc, _ := newTestService(t)
	plaintext, key, err := svc.Generate(context.Background(), "k", []string{"send"}, []string{"vibast.com"})
	require.NoError(t, err)

	require.NoError(t, svc.Revoke(context.Background(), key.ID))

	_, err = svc.Authenticate(context.Background(), plaintext)
	assert.ErrorIs(t, err, ErrAPIKeyRevoked)
}

func TestRevokeUnknownID(t *testing.T) {
	svc, _ := newTestService(t)
	assert.ErrorIs(t, svc.Revoke(context.Background(), "nope"), ErrNotFound)
}

func TestAuthenticateUsesCacheWithin30s(t *testing.T) {
	svc, repo := newTestService(t)
	now := time.Now()
	svc.now = func() time.Time { return now }

	plaintext, _, err := svc.Generate(context.Background(), "k", []string{"send"}, []string{"vibast.com"})
	require.NoError(t, err)

	_, err = svc.Authenticate(context.Background(), plaintext)
	require.NoError(t, err)
	_, err = svc.Authenticate(context.Background(), plaintext)
	require.NoError(t, err)
	assert.Equal(t, 1, repo.findCalls, "second authenticate within TTL must hit the cache")

	now = now.Add(31 * time.Second)
	_, err = svc.Authenticate(context.Background(), plaintext)
	require.NoError(t, err)
	assert.Equal(t, 2, repo.findCalls, "expired cache entry must be reloaded")
}

func TestRevokeEvictsCacheImmediately(t *testing.T) {
	svc, repo := newTestService(t)
	plaintext, key, err := svc.Generate(context.Background(), "k", []string{"send"}, []string{"vibast.com"})
	require.NoError(t, err)

	// Prime the cache, then revoke: the next authenticate must not serve the
	// stale active entry.
	_, err = svc.Authenticate(context.Background(), plaintext)
	require.NoError(t, err)
	require.NoError(t, svc.Revoke(context.Background(), key.ID))

	calls := repo.findCalls
	_, err = svc.Authenticate(context.Background(), plaintext)
	assert.ErrorIs(t, err, ErrAPIKeyRevoked)
	assert.Equal(t, calls+1, repo.findCalls, "revoke must evict the cache entry")
}

func TestReplaceDomains(t *testing.T) {
	svc, repo := newTestService(t)
	ctx := context.Background()
	_, key, err := svc.Generate(ctx, "k", []string{"send"}, []string{"vibast.com"})
	require.NoError(t, err)

	require.NoError(t, svc.ReplaceDomains(ctx, key.ID, []string{"ALEXTOOLS.ro"}))
	assert.Equal(t, []string{"alextools.ro"}, repo.keys[key.KeyPrefix].Domains)

	t.Run("rejects domain outside ALLOWED_DOMAINS", func(t *testing.T) {
		requireValidationField(t, svc.ReplaceDomains(ctx, key.ID, []string{"evil.com"}), "domains")
	})
	t.Run("rejects empty set for send key", func(t *testing.T) {
		requireValidationField(t, svc.ReplaceDomains(ctx, key.ID, nil), "domains")
	})
	t.Run("unknown id", func(t *testing.T) {
		assert.ErrorIs(t, svc.ReplaceDomains(ctx, "nope", []string{"vibast.com"}), ErrNotFound)
	})
	t.Run("admin-only key must keep zero domains", func(t *testing.T) {
		_, adminKey, err := svc.Generate(ctx, "admin", []string{"admin"}, nil)
		require.NoError(t, err)
		requireValidationField(t, svc.ReplaceDomains(ctx, adminKey.ID, []string{"vibast.com"}), "domains")
	})
}
