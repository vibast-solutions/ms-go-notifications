// Package v1 holds the HTTP handlers for API v1. Only the health check lives
// here for now; /v1/emails, /v1/admin/* and /v1/webhooks/ses arrive in later
// stories.
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

// HealthController serves GET /healthz.
type HealthController struct {
	db DBPinger
}

// NewHealthController returns a HealthController backed by db.
func NewHealthController(db DBPinger) *HealthController {
	return &HealthController{db: db}
}

// Healthz reports liveness and database reachability: 200 when the DB ping
// succeeds, 503 when it fails.
func (h *HealthController) Healthz(c *echo.Context) error {
	ctx, cancel := context.WithTimeout(c.Request().Context(), 2*time.Second)
	defer cancel()

	if err := h.db.PingContext(ctx); err != nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"status": "error", "db": "error"})
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "ok", "db": "ok"})
}
