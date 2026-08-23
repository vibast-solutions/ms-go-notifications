package v1

import (
	"context"
	"net/http"

	"github.com/labstack/echo/v5"

	dtov1 "github.com/vibast-solutions/ms-go-notifications/app/dto/v1"
	"github.com/vibast-solutions/ms-go-notifications/app/middleware"
	"github.com/vibast-solutions/ms-go-notifications/app/service"
)

// defaultManualPauseReason is stored when an operator pauses without giving one.
const defaultManualPauseReason = "manual"

// SendingBreaker is the slice of the reputation service the controller needs:
// read the pause state and manually pause/resume.
type SendingBreaker interface {
	State(ctx context.Context) (service.SendingState, error)
	Pause(ctx context.Context, reason, by string) error
	Resume(ctx context.Context, by string) error
}

// AdminSendingController serves the circuit-breaker admin endpoints under
// /v1/admin/sending. All routes require the admin scope.
type AdminSendingController struct {
	breaker SendingBreaker
}

// NewAdminSendingController returns a controller backed by the reputation
// service.
func NewAdminSendingController(breaker SendingBreaker) *AdminSendingController {
	return &AdminSendingController{breaker: breaker}
}

// Get returns the current sending (pause) state.
//
//	@Summary		Get sending state
//	@Description	Returns the reputation circuit-breaker state: whether sending is paused and, if so, the reason, since when, and who paused it.
//	@Tags			admin
//	@Produce		json
//	@Success		200	{object}	dtov1.SendingStateResponse
//	@Failure		401	{object}	dtov1.ErrorResponse	"unauthorized"
//	@Failure		403	{object}	dtov1.ErrorResponse	"forbidden"
//	@Failure		500	{object}	dtov1.ErrorResponse	"internal_error"
//	@Security		BearerAuth
//	@Router			/admin/sending [get]
func (h *AdminSendingController) Get(c *echo.Context) error {
	state, err := h.breaker.State(c.Request().Context())
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, toSendingStateResponse(state))
}

// Pause manually pauses sending.
//
//	@Summary		Pause sending
//	@Description	Manually pauses sending. Queued and newly accepted messages accumulate until sending is resumed. An optional reason is recorded.
//	@Tags			admin
//	@Accept			json
//	@Produce		json
//	@Param			request	body		dtov1.PauseSendingRequest	false	"Optional pause reason"
//	@Success		200		{object}	dtov1.SendingStateResponse
//	@Failure		400		{object}	dtov1.ErrorResponse	"bad_request"
//	@Failure		401		{object}	dtov1.ErrorResponse	"unauthorized"
//	@Failure		403		{object}	dtov1.ErrorResponse	"forbidden"
//	@Failure		500		{object}	dtov1.ErrorResponse	"internal_error"
//	@Security		BearerAuth
//	@Router			/admin/sending/pause [post]
func (h *AdminSendingController) Pause(c *echo.Context) error {
	var req dtov1.PauseSendingRequest
	// The body is optional; only reject a present-but-malformed one.
	if c.Request().ContentLength != 0 {
		if err := c.Bind(&req); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
		}
	}
	reason := req.Reason
	if reason == "" {
		reason = defaultManualPauseReason
	}

	ctx := c.Request().Context()
	if err := h.breaker.Pause(ctx, reason, actorName(c)); err != nil {
		return err
	}
	state, err := h.breaker.State(ctx)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, toSendingStateResponse(state))
}

// Resume manually resumes sending.
//
//	@Summary		Resume sending
//	@Description	Manually resumes sending after an automatic or manual pause. The sweeper re-dispatches the queued backlog.
//	@Tags			admin
//	@Produce		json
//	@Success		200	{object}	dtov1.SendingStateResponse
//	@Failure		401	{object}	dtov1.ErrorResponse	"unauthorized"
//	@Failure		403	{object}	dtov1.ErrorResponse	"forbidden"
//	@Failure		500	{object}	dtov1.ErrorResponse	"internal_error"
//	@Security		BearerAuth
//	@Router			/admin/sending/resume [post]
func (h *AdminSendingController) Resume(c *echo.Context) error {
	ctx := c.Request().Context()
	if err := h.breaker.Resume(ctx, actorName(c)); err != nil {
		return err
	}
	state, err := h.breaker.State(ctx)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, toSendingStateResponse(state))
}

// actorName returns the calling API key's name, recorded as the "by" field of a
// manual pause/resume. It is always set on admin routes (auth.Require("admin")).
func actorName(c *echo.Context) string {
	if key := middleware.GetAPIKey(c); key != nil {
		return key.Name
	}
	return ""
}

// toSendingStateResponse maps the service state to the API DTO, leaving since
// null while sending is running.
func toSendingStateResponse(s service.SendingState) dtov1.SendingStateResponse {
	resp := dtov1.SendingStateResponse{Paused: s.Paused, Reason: s.Reason, By: s.By}
	if s.Paused && !s.Since.IsZero() {
		since := s.Since
		resp.Since = &since
	}
	return resp
}
