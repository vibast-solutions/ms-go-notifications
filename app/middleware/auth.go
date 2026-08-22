package middleware

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v5"
	log "github.com/sirupsen/logrus"

	"github.com/vibast-solutions/ms-go-notifications/app/entity"
	"github.com/vibast-solutions/ms-go-notifications/app/service"
)

// APIKeyContextKey is the Echo context key the authenticated key is stored
// under. Handlers read it via GetAPIKey.
const APIKeyContextKey = "api_key"

// lastUsedInterval throttles last_used_at writes to one per key per minute.
const lastUsedInterval = time.Minute

// Auth authenticates requests with bearer API keys and enforces scopes.
type Auth struct {
	keys service.APIKeys

	mu        sync.Mutex
	lastTouch map[string]time.Time

	now func() time.Time
}

// NewAuth returns an Auth middleware backed by the given key service.
func NewAuth(keys service.APIKeys) *Auth {
	return &Auth{
		keys:      keys,
		lastTouch: map[string]time.Time{},
		now:       time.Now,
	}
}

// Require returns middleware that authenticates the Authorization: Bearer
// token and, when scope is non-empty, requires the key to carry that scope.
// The key (with domains) is stored in the context for GetAPIKey. Failures
// use the standard error envelope: 401 unauthorized, 403 forbidden.
func (a *Auth) Require(scope string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			token, ok := bearerToken(c.Request().Header.Get("Authorization"))
			if !ok {
				return echo.NewHTTPError(http.StatusUnauthorized, "missing or malformed Authorization header")
			}

			key, err := a.keys.Authenticate(c.Request().Context(), token)
			if err != nil {
				if errors.Is(err, service.ErrInvalidAPIKey) || errors.Is(err, service.ErrAPIKeyRevoked) {
					return echo.NewHTTPError(http.StatusUnauthorized, "invalid API key")
				}
				return err
			}

			if scope != "" && !key.HasScope(scope) {
				return echo.NewHTTPError(http.StatusForbidden, "API key lacks the required scope: "+scope)
			}

			c.Set(APIKeyContextKey, key)
			a.touch(c.Request().Context(), key.ID)
			return next(c)
		}
	}
}

// GetAPIKey returns the authenticated key stored by Require, or nil on
// routes without auth.
func GetAPIKey(c *echo.Context) *entity.APIKey {
	key, _ := c.Get(APIKeyContextKey).(*entity.APIKey)
	return key
}

// touch updates last_used_at at most once per lastUsedInterval per key.
// Failures only log — usage stamping must never fail a request.
func (a *Auth) touch(ctx context.Context, id string) {
	now := a.now()

	a.mu.Lock()
	last, seen := a.lastTouch[id]
	if seen && now.Sub(last) < lastUsedInterval {
		a.mu.Unlock()
		return
	}
	a.lastTouch[id] = now
	a.mu.Unlock()

	if err := a.keys.TouchLastUsed(ctx, id); err != nil {
		log.WithError(err).WithField("api_key_id", id).Warn("failed to update last_used_at")
	}
}

// bearerToken extracts the token from an "Authorization: Bearer x" value.
func bearerToken(header string) (string, bool) {
	const prefix = "Bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", false
	}
	token := strings.TrimSpace(header[len(prefix):])
	return token, token != ""
}
