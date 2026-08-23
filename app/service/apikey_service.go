package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/vibast-solutions/ms-go-notifications/app/entity"
)

// Plaintext key layout: ntf_<8 chars [a-z0-9]>_<32 chars [A-Za-z0-9]>.
// Only the sha256 hex of the full plaintext is stored.
const (
	keyPrefixLen = 8
	keySecretLen = 32

	prefixAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	secretAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

	// authCacheTTL bounds how long a revocation done on another instance can
	// keep authenticating here (Business Logic §1: ≤ 30 s).
	authCacheTTL = 30 * time.Second
)

var plaintextKeyRe = regexp.MustCompile(`^ntf_([a-z0-9]{8})_[A-Za-z0-9]{32}$`)

// cachedKey is one auth-cache entry, keyed by key_prefix.
type cachedKey struct {
	key     *entity.APIKey
	expires time.Time
}

// APIKeyService implements APIKeys on top of an APIKeyRepository, with a
// short-lived in-memory cache on the Authenticate path.
type APIKeyService struct {
	repo           APIKeyRepository
	allowedDomains map[string]bool

	mu    sync.Mutex
	cache map[string]cachedKey

	now func() time.Time
}

// NewAPIKeyService returns an APIKeyService validating domains against
// allowedDomains (the ALLOWED_DOMAINS config list).
func NewAPIKeyService(repo APIKeyRepository, allowedDomains []string) *APIKeyService {
	allowed := make(map[string]bool, len(allowedDomains))
	for _, d := range allowedDomains {
		allowed[strings.ToLower(strings.TrimSpace(d))] = true
	}
	return &APIKeyService{
		repo:           repo,
		allowedDomains: allowed,
		cache:          map[string]cachedKey{},
		now:            time.Now,
	}
}

// Generate validates the input, mints a plaintext key, and stores its prefix
// and hash. The returned plaintext is the only time the secret exists outside
// the caller's hands.
func (s *APIKeyService) Generate(ctx context.Context, name string, scopes, domains []string) (string, *entity.APIKey, error) {
	scopes = normalizeList(scopes)
	domains = normalizeList(domains)
	if err := validateKeyInput(name, scopes, domains, s.allowedDomains); err != nil {
		return "", nil, err
	}

	prefix, err := randomString(prefixAlphabet, keyPrefixLen)
	if err != nil {
		return "", nil, fmt.Errorf("generating key prefix: %w", err)
	}
	secret, err := randomString(secretAlphabet, keySecretLen)
	if err != nil {
		return "", nil, fmt.Errorf("generating key secret: %w", err)
	}
	plaintext := "ntf_" + prefix + "_" + secret

	key := &entity.APIKey{
		ID:        entity.NewID(),
		Name:      strings.TrimSpace(name),
		KeyPrefix: prefix,
		KeyHash:   hashKey(plaintext),
		Scopes:    scopes,
		Status:    entity.APIKeyStatusActive,
		CreatedAt: s.now().UTC().Truncate(time.Millisecond),
		Domains:   domains,
	}
	if err := s.repo.Create(ctx, key, domains); err != nil {
		return "", nil, fmt.Errorf("storing api key: %w", err)
	}
	return plaintext, key, nil
}

// Authenticate resolves a plaintext bearer token: parse the prefix, load the
// key (30 s cache), constant-time compare the hash, reject revoked keys.
func (s *APIKeyService) Authenticate(ctx context.Context, plaintext string) (*entity.APIKey, error) {
	m := plaintextKeyRe.FindStringSubmatch(plaintext)
	if m == nil {
		return nil, ErrInvalidAPIKey
	}
	prefix := m[1]

	key, err := s.lookup(ctx, prefix)
	if err != nil {
		return nil, err
	}

	provided := hashKey(plaintext)
	if subtle.ConstantTimeCompare([]byte(provided), []byte(key.KeyHash)) != 1 {
		return nil, ErrInvalidAPIKey
	}
	if key.Status != entity.APIKeyStatusActive {
		return nil, ErrAPIKeyRevoked
	}
	return key, nil
}

// lookup returns the key for a prefix, from cache when fresh.
func (s *APIKeyService) lookup(ctx context.Context, prefix string) (*entity.APIKey, error) {
	now := s.now()

	s.mu.Lock()
	entry, ok := s.cache[prefix]
	s.mu.Unlock()
	if ok && now.Before(entry.expires) {
		return entry.key, nil
	}

	key, err := s.repo.FindByPrefix(ctx, prefix)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrInvalidAPIKey
		}
		return nil, fmt.Errorf("loading api key: %w", err)
	}

	s.mu.Lock()
	s.cache[prefix] = cachedKey{key: key, expires: now.Add(authCacheTTL)}
	s.mu.Unlock()
	return key, nil
}

// List returns all keys with their domains.
func (s *APIKeyService) List(ctx context.Context) ([]*entity.APIKey, error) {
	keys, err := s.repo.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing api keys: %w", err)
	}
	return keys, nil
}

// Revoke marks the key revoked and evicts it from the auth cache so it fails
// immediately in-process.
func (s *APIKeyService) Revoke(ctx context.Context, id string) error {
	err := s.repo.Revoke(ctx, id, s.now().UTC().Truncate(time.Millisecond))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("revoking api key: %w", err)
	}
	s.evictByID(id)
	return nil
}

// ReplaceDomains swaps the key's domain set after validating it against the
// key's scopes, then evicts the cache entry so the change applies here now.
func (s *APIKeyService) ReplaceDomains(ctx context.Context, id string, domains []string) error {
	domains = normalizeList(domains)

	key, err := s.findByID(ctx, id)
	if err != nil {
		return err
	}
	if err := validateDomains(key.Scopes, domains, s.allowedDomains); err != nil {
		return err
	}

	err = s.repo.ReplaceDomains(ctx, id, domains, s.now().UTC().Truncate(time.Millisecond))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("replacing api key domains: %w", err)
	}
	s.evictByID(id)
	return nil
}

// TouchLastUsed stamps last_used_at now. The auth middleware throttles calls
// to at most one per key per minute.
func (s *APIKeyService) TouchLastUsed(ctx context.Context, id string) error {
	if err := s.repo.TouchLastUsed(ctx, id, s.now().UTC().Truncate(time.Millisecond)); err != nil {
		return fmt.Errorf("touching api key: %w", err)
	}
	return nil
}

// Get returns the key (with domains) for the given id, or ErrNotFound. Used by
// the admin api-keys endpoints to echo a key after mutating it.
func (s *APIKeyService) Get(ctx context.Context, id string) (*entity.APIKey, error) {
	return s.findByID(ctx, id)
}

// NameByID returns the name of the key with the given id, or ErrNotFound when
// it does not exist. Used to label admin message details with the owning key.
func (s *APIKeyService) NameByID(ctx context.Context, id string) (string, error) {
	name, err := s.repo.NameByID(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("loading api key name: %w", err)
	}
	return name, nil
}

// findByID resolves an id through List — key counts are tiny (operator CLI
// scale) and the repository API stays minimal.
func (s *APIKeyService) findByID(ctx context.Context, id string) (*entity.APIKey, error) {
	keys, err := s.repo.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing api keys: %w", err)
	}
	for _, k := range keys {
		if k.ID == id {
			return k, nil
		}
	}
	return nil, ErrNotFound
}

func (s *APIKeyService) evictByID(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for prefix, entry := range s.cache {
		if entry.key.ID == id {
			delete(s.cache, prefix)
		}
	}
}

// validateKeyInput enforces the creation rules: name 1–100 chars, scopes a
// non-empty subset of {send, admin}, domains per validateDomains.
func validateKeyInput(name string, scopes, domains []string, allowed map[string]bool) error {
	fields := map[string]string{}

	name = strings.TrimSpace(name)
	if name == "" || len(name) > 100 {
		fields["name"] = "must be 1-100 characters"
	}

	if len(scopes) == 0 {
		fields["scopes"] = "at least one scope is required"
	}
	for _, sc := range scopes {
		if sc != entity.ScopeSend && sc != entity.ScopeAdmin {
			fields["scopes"] = fmt.Sprintf("unknown scope %q (valid: %s, %s)", sc, entity.ScopeSend, entity.ScopeAdmin)
		}
	}

	if _, has := fields["scopes"]; !has {
		if err := validateDomains(scopes, domains, allowed); err != nil {
			var ve *ValidationError
			if errors.As(err, &ve) {
				for k, v := range ve.Fields {
					fields[k] = v
				}
			} else {
				return err
			}
		}
	}

	if len(fields) > 0 {
		return NewValidationError(fields)
	}
	return nil
}

// validateDomains enforces the scope/domain coupling: send keys need >= 1
// domain, each in ALLOWED_DOMAINS; admin-only keys must have none.
func validateDomains(scopes, domains []string, allowed map[string]bool) error {
	hasSend := false
	for _, sc := range scopes {
		if sc == entity.ScopeSend {
			hasSend = true
		}
	}

	fields := map[string]string{}
	switch {
	case hasSend && len(domains) == 0:
		fields["domains"] = "at least one domain is required for the send scope"
	case !hasSend && len(domains) > 0:
		fields["domains"] = "admin-only keys must not have domains"
	default:
		for _, d := range domains {
			if !allowed[d] {
				fields["domains"] = fmt.Sprintf("domain %q is not in ALLOWED_DOMAINS", d)
				break
			}
		}
	}

	if len(fields) > 0 {
		return NewValidationError(fields)
	}
	return nil
}

// normalizeList trims, lower-cases, de-duplicates and drops empty items,
// preserving first-seen order.
func normalizeList(items []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, item := range items {
		item = strings.ToLower(strings.TrimSpace(item))
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	return out
}

// hashKey returns the sha256 hex digest of the full plaintext key.
func hashKey(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// randomString draws n characters uniformly from alphabet using crypto/rand,
// with rejection sampling to avoid modulo bias.
func randomString(alphabet string, n int) (string, error) {
	max := byte(256 - 256%len(alphabet))
	out := make([]byte, 0, n)
	buf := make([]byte, n)
	for len(out) < n {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		for _, b := range buf {
			if b >= max {
				continue
			}
			out = append(out, alphabet[int(b)%len(alphabet)])
			if len(out) == n {
				break
			}
		}
	}
	return string(out), nil
}
