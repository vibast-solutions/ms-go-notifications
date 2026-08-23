package v1

import (
	"context"
	"net/http"

	"github.com/labstack/echo/v5"

	dtov1 "github.com/vibast-solutions/ms-go-notifications/app/dto/v1"
	"github.com/vibast-solutions/ms-go-notifications/app/service"
)

// AdminStats is the slice of the admin service the stats controller needs.
type AdminStats interface {
	Stats(ctx context.Context, p service.StatsParams) (*dtov1.StatsResponse, error)
}

// AdminStatsController serves GET /v1/admin/stats.
type AdminStatsController struct {
	svc AdminStats
}

// NewAdminStatsController returns a controller backed by the admin service.
func NewAdminStatsController(svc AdminStats) *AdminStatsController {
	return &AdminStatsController{svc: svc}
}

// Get returns the dashboard stats.
//
//	@Summary		Dashboard stats
//	@Description	Per-bucket accepted/sent/failed/suppressed/delivered/bounced/complained counts over a UTC window, with totals, 24h rolling bounce/complaint rates, sending state and live backlog gauges.
//	@Tags			admin
//	@Produce		json
//	@Param			from		query		string	false	"Window start (RFC3339 or YYYY-MM-DD, UTC). Default: 7 days ago"
//	@Param			to			query		string	false	"Window end (RFC3339 or YYYY-MM-DD, UTC). Default: now"
//	@Param			fromDomain	query		string	false	"Restrict to a sender domain"
//	@Param			granularity	query		string	false	"Bucket size: day or hour (default day)"
//	@Success		200			{object}	dtov1.StatsResponse
//	@Failure		400			{object}	dtov1.ErrorResponse	"validation_error"
//	@Failure		401			{object}	dtov1.ErrorResponse	"unauthorized"
//	@Failure		403			{object}	dtov1.ErrorResponse	"forbidden"
//	@Failure		500			{object}	dtov1.ErrorResponse	"internal_error"
//	@Security		BearerAuth
//	@Router			/admin/stats [get]
func (h *AdminStatsController) Get(c *echo.Context) error {
	p := service.StatsParams{
		From:        c.QueryParam("from"),
		To:          c.QueryParam("to"),
		FromDomain:  c.QueryParam("fromDomain"),
		Granularity: c.QueryParam("granularity"),
	}
	resp, err := h.svc.Stats(c.Request().Context(), p)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, resp)
}
