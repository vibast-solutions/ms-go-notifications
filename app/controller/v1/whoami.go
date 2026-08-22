package v1

import (
	"net/http"

	"github.com/labstack/echo/v5"

	"github.com/vibast-solutions/ms-go-notifications/app/middleware"
)

// WhoamiController serves GET /v1/whoami: it echoes the authenticated key's
// identity, useful as an SDK smoke test.
type WhoamiController struct{}

// NewWhoamiController returns a WhoamiController.
func NewWhoamiController() *WhoamiController {
	return &WhoamiController{}
}

// whoamiResponse is the GET /v1/whoami payload.
type whoamiResponse struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Scopes  []string `json:"scopes"`
	Domains []string `json:"domains"`
}

// Whoami returns the authenticated key's id, name, scopes and domains.
func (h *WhoamiController) Whoami(c *echo.Context) error {
	key := middleware.GetAPIKey(c)
	if key == nil {
		// Unreachable when the route is wired with auth.Require; guard anyway.
		return echo.NewHTTPError(http.StatusUnauthorized, "missing API key")
	}

	resp := whoamiResponse{
		ID:      key.ID,
		Name:    key.Name,
		Scopes:  key.Scopes,
		Domains: key.Domains,
	}
	if resp.Domains == nil {
		resp.Domains = []string{}
	}
	return c.JSON(http.StatusOK, resp)
}
