package controller

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/vibast-solutions/ms-go-notifications/app/dto"
	"github.com/vibast-solutions/ms-go-notifications/app/queue"
	"github.com/vibast-solutions/ms-go-notifications/app/service"
)

type EmailController struct {
	emailService *service.EmailService
	producer     *queue.EmailProducer
}

// NewEmailController constructs the HTTP email controller.
func NewEmailController(emailService *service.EmailService, producer *queue.EmailProducer) *EmailController {
	return &EmailController{emailService: emailService, producer: producer}
}

// SendRaw validates, stores, and enqueues an email send request.
func (c *EmailController) SendRaw(ctx echo.Context) error {
	req, err := dto.FromEchoContext(ctx)
	if err != nil {
		return ctx.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}
	if err := req.Validate(); err != nil {
		return ctx.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}

	if err := c.emailService.CreateRequest(ctx.Request().Context(), req.RequestID, req.Recipient, req.Subject, req.Content); err != nil {
		if errors.Is(err, service.ErrDuplicateRequestID) {
			return ctx.JSON(http.StatusBadRequest, map[string]string{"error": "duplicate request_id"})
		}
		return ctx.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create email history"})
	}

	if err := c.producer.Publish(ctx.Request().Context(), queue.EmailMessage{
		RequestID: req.RequestID,
		Recipient: req.Recipient,
		Subject:   req.Subject,
		Content:   req.Content,
	}); err != nil {
		_ = c.emailService.DeleteRequest(ctx.Request().Context(), req.RequestID)
		return ctx.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to queue email"})
	}

	return ctx.JSON(http.StatusOK, map[string]string{"message": "email accepted"})
}
