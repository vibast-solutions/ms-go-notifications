// Package v1 holds the HTTP handlers for API v1: /v1/emails, /v1/whoami,
// /v1/admin/* and /v1/webhooks/ses, plus the unversioned /healthz.
package v1

import (
	"context"
	"net/http"
	"time"

	"github.com/labstack/echo/v5"
)

// DBPinger is the slice of *sql.DB the health check needs.
type DBPinger interface {
	PingContext(ctx context.Context) error
}

// SendingStateReader reports whether sending is currently paused, for the
// informational sendingPaused field. Satisfied by the reputation service.
type SendingStateReader interface {
	SendingPaused(ctx context.Context) (bool, error)
}

// HealthController serves GET /healthz.
type HealthController struct {
	db      DBPinger
	sending SendingStateReader
}

// NewHealthController returns a HealthController backed by db and the reputation
// service (for the informational sendingPaused flag).
func NewHealthController(db DBPinger, sending SendingStateReader) *HealthController {
	return &HealthController{db: db, sending: sending}
}

// healthOK is the 200 payload; sendingPaused is informational (a pause never
// makes the service unhealthy).
type healthOK struct {
	Status        string `json:"status"`
	DB            string `json:"db"`
	SendingPaused bool   `json:"sendingPaused"`
}

// Healthz reports liveness and database reachability: 200 when the DB ping
// succeeds, 503 when it fails. The 200 payload also reports whether sending is
// paused (best effort — an error reading it defaults to false, never a 5xx).
func (h *HealthController) Healthz(c *echo.Context) error {
	ctx, cancel := context.WithTimeout(c.Request().Context(), 2*time.Second)
	defer cancel()

	if err := h.db.PingContext(ctx); err != nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"status": "error", "db": "error"})
	}

	paused := false
	if h.sending != nil {
		if p, err := h.sending.SendingPaused(ctx); err == nil {
			paused = p
		}
	}
	return c.JSON(http.StatusOK, healthOK{Status: "ok", DB: "ok", SendingPaused: paused})
}
