package v1

import (
	"context"
	"net/http"

	"github.com/labstack/echo/v5"

	dtov1 "github.com/vibast-solutions/ms-go-notifications/app/dto/v1"
	"github.com/vibast-solutions/ms-go-notifications/app/service"
)

// AdminSuppressions is the slice of the admin service the suppressions
// controller needs.
type AdminSuppressions interface {
	ListSuppressions(ctx context.Context, p service.SuppressionListParams) (*dtov1.SuppressionListResponse, error)
	AddSuppression(ctx context.Context, req *dtov1.CreateSuppressionRequest) (*dtov1.SuppressionResponse, error)
	DeleteSuppression(ctx context.Context, email string) error
}

// AdminSuppressionsController serves the admin suppression endpoints under
// /v1/admin/suppressions.
type AdminSuppressionsController struct {
	svc AdminSuppressions
}

// NewAdminSuppressionsController returns a controller backed by the admin
// service.
func NewAdminSuppressionsController(svc AdminSuppressions) *AdminSuppressionsController {
	return &AdminSuppressionsController{svc: svc}
}

// List returns paginated suppressions.
//
//	@Summary		List suppressions
//	@Description	Paginated suppression list. Cursor pagination on (createdAt, email) descending.
//	@Tags			admin
//	@Produce		json
//	@Param			email	query		string	false	"Filter by email (contains)"
//	@Param			reason	query		string	false	"Filter by reason (hard_bounce, complaint, manual)"
//	@Param			cursor	query		string	false	"Pagination cursor from a previous response"
//	@Param			limit	query		int		false	"Page size (1-100, default 25)"
//	@Success		200		{object}	dtov1.SuppressionListResponse
//	@Failure		400		{object}	dtov1.ErrorResponse	"validation_error"
//	@Failure		401		{object}	dtov1.ErrorResponse	"unauthorized"
//	@Failure		403		{object}	dtov1.ErrorResponse	"forbidden"
//	@Failure		500		{object}	dtov1.ErrorResponse	"internal_error"
//	@Security		BearerAuth
//	@Router			/admin/suppressions [get]
func (h *AdminSuppressionsController) List(c *echo.Context) error {
	p := service.SuppressionListParams{
		Email:  c.QueryParam("email"),
		Reason: c.QueryParam("reason"),
		Cursor: c.QueryParam("cursor"),
		Limit:  c.QueryParam("limit"),
	}
	resp, err := h.svc.ListSuppressions(c.Request().Context(), p)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, resp)
}

// Create adds a manual suppression.
//
//	@Summary		Add a manual suppression
//	@Description	Suppresses an address (reason "manual"). Returns 409 conflict when the address is already suppressed.
//	@Tags			admin
//	@Accept			json
//	@Produce		json
//	@Param			request	body		dtov1.CreateSuppressionRequest	true	"Address to suppress"
//	@Success		201		{object}	dtov1.SuppressionResponse
//	@Failure		400		{object}	dtov1.ErrorResponse	"validation_error"
//	@Failure		401		{object}	dtov1.ErrorResponse	"unauthorized"
//	@Failure		403		{object}	dtov1.ErrorResponse	"forbidden"
//	@Failure		409		{object}	dtov1.ErrorResponse	"conflict"
//	@Failure		500		{object}	dtov1.ErrorResponse	"internal_error"
//	@Security		BearerAuth
//	@Router			/admin/suppressions [post]
func (h *AdminSuppressionsController) Create(c *echo.Context) error {
	var req dtov1.CreateSuppressionRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	resp, err := h.svc.AddSuppression(c.Request().Context(), &req)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusCreated, resp)
}

// Delete removes a suppression.
//
//	@Summary		Remove a suppression
//	@Description	Removes a suppression by email. Returns 404 when the address is not suppressed.
//	@Tags			admin
//	@Produce		json
//	@Param			email	path	string	true	"Suppressed email address"
//	@Success		204		{object}	nil
//	@Failure		401		{object}	dtov1.ErrorResponse	"unauthorized"
//	@Failure		403		{object}	dtov1.ErrorResponse	"forbidden"
//	@Failure		404		{object}	dtov1.ErrorResponse	"not_found"
//	@Failure		500		{object}	dtov1.ErrorResponse	"internal_error"
//	@Security		BearerAuth
//	@Router			/admin/suppressions/{email} [delete]
func (h *AdminSuppressionsController) Delete(c *echo.Context) error {
	if err := h.svc.DeleteSuppression(c.Request().Context(), c.Param("email")); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}
