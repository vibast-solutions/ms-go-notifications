package v1

import (
	"context"
	"net/http"

	"github.com/labstack/echo/v5"

	dtov1 "github.com/vibast-solutions/ms-go-notifications/app/dto/v1"
	"github.com/vibast-solutions/ms-go-notifications/app/entity"
	"github.com/vibast-solutions/ms-go-notifications/app/middleware"
)

// EmailAccepter is the slice of the email service the controller needs.
type EmailAccepter interface {
	Accept(ctx context.Context, apiKey *entity.APIKey, req *dtov1.SendEmailRequest) (*entity.Message, bool, error)
	GetForAPIKey(ctx context.Context, id, apiKeyID string) (*entity.Message, []*entity.MessageRecipient, []*entity.MessageEvent, error)
}

// EmailsController serves the consumer email endpoints under /v1/emails.
type EmailsController struct {
	emails EmailAccepter
}

// NewEmailsController returns an EmailsController backed by the email service.
func NewEmailsController(emails EmailAccepter) *EmailsController {
	return &EmailsController{emails: emails}
}

// Send accepts an email for delivery.
//
//	@Summary		Accept an email
//	@Description	Validate a send request, store it queued (or suppressed if every recipient is suppressed), and hand it to the dispatcher. Returns 202 for a new message and 200 for an idempotent replay of the same idempotencyKey.
//	@Tags			emails
//	@Accept			json
//	@Produce		json
//	@Param			request	body		dtov1.SendEmailRequest	true	"Email to send"
//	@Success		202		{object}	dtov1.SendEmailResponse	"Accepted (new message)"
//	@Success		200		{object}	dtov1.SendEmailResponse	"Idempotent replay of an existing message"
//	@Failure		400		{object}	dtov1.ErrorResponse		"validation_error"
//	@Failure		401		{object}	dtov1.ErrorResponse		"unauthorized"
//	@Failure		403		{object}	dtov1.ErrorResponse		"domain_not_allowed"
//	@Failure		413		{object}	dtov1.ErrorResponse		"payload_too_large"
//	@Failure		500		{object}	dtov1.ErrorResponse		"internal_error"
//	@Security		BearerAuth
//	@Router			/emails [post]
func (h *EmailsController) Send(c *echo.Context) error {
	key := middleware.GetAPIKey(c)
	if key == nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "missing API key")
	}

	var req dtov1.SendEmailRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}

	msg, created, err := h.emails.Accept(c.Request().Context(), key, &req)
	if err != nil {
		return err
	}

	resp := dtov1.SendEmailResponse{ID: msg.ID, Status: msg.Status, CreatedAt: msg.CreatedAt}
	if created {
		return c.JSON(http.StatusAccepted, resp)
	}
	return c.JSON(http.StatusOK, resp)
}

// Get returns a message's status and timeline.
//
//	@Summary		Get an email's status and timeline
//	@Description	Returns the message status, recipients and event timeline. Bodies and attachment contents are never returned. 404 when the id is unknown or owned by another API key.
//	@Tags			emails
//	@Produce		json
//	@Param			id	path		string				true	"Message id (ULID)"
//	@Success		200	{object}	dtov1.EmailResponse
//	@Failure		401	{object}	dtov1.ErrorResponse	"unauthorized"
//	@Failure		404	{object}	dtov1.ErrorResponse	"not_found"
//	@Failure		500	{object}	dtov1.ErrorResponse	"internal_error"
//	@Security		BearerAuth
//	@Router			/emails/{id} [get]
func (h *EmailsController) Get(c *echo.Context) error {
	key := middleware.GetAPIKey(c)
	if key == nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "missing API key")
	}

	msg, recipients, events, err := h.emails.GetForAPIKey(c.Request().Context(), c.Param("id"), key.ID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, dtov1.NewEmailResponse(msg, recipients, events))
}
