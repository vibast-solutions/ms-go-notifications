package controller

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/sirupsen/logrus"
	"github.com/vibast-solutions/ms-go-notifications/app/dto"
	"github.com/vibast-solutions/ms-go-notifications/app/queue"
	"github.com/vibast-solutions/ms-go-notifications/app/service"
)

type EmailController struct {
	emailService *service.EmailService
	producer     queue.EmailPublisher
}

// NewEmailController constructs the HTTP email controller.
func NewEmailController(emailService *service.EmailService, producer queue.EmailPublisher) *EmailController {
	return &EmailController{emailService: emailService, producer: producer}
}

// SendRaw validates, stores, and enqueues an email send request.
func (c *EmailController) SendRaw(ctx echo.Context) error {
	req, err := dto.FromEchoContext(ctx)
	if err != nil {
		logrus.WithError(err).Debug("Failed to bind send raw request")
		return ctx.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}
	if err := req.Validate(); err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{
			"request_id": req.RequestID,
			"recipient":  req.Recipient,
		}).Debug("Send raw validation failed")
		return ctx.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}

	logrus.WithFields(logrus.Fields{
		"request_id": req.RequestID,
		"recipient":  req.Recipient,
	}).Info("Received send raw request (http)")

	if err := c.emailService.CreateRequest(ctx.Request().Context(), req.RequestID, req.Recipient, req.Subject, req.Content); err != nil {
		if errors.Is(err, service.ErrDuplicateRequestID) {
			logrus.WithField("request_id", req.RequestID).Warn("Duplicate request_id")
			return ctx.JSON(http.StatusBadRequest, map[string]string{"error": "duplicate request_id"})
		}
		logrus.WithError(err).WithField("request_id", req.RequestID).Error("Failed to create email history")
		return ctx.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create email history"})
	}

	if err := c.producer.Publish(ctx.Request().Context(), queue.EmailMessage{
		RequestID: req.RequestID,
		Recipient: req.Recipient,
		Subject:   req.Subject,
		Content:   req.Content,
	}); err != nil {
		_ = c.emailService.DeleteRequest(ctx.Request().Context(), req.RequestID)
		logrus.WithError(err).WithField("request_id", req.RequestID).Error("Failed to queue email")
		return ctx.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to queue email"})
	}

	logrus.WithField("request_id", req.RequestID).Info("Email request queued (http)")
	return ctx.JSON(http.StatusOK, map[string]string{"message": "email accepted"})
}
