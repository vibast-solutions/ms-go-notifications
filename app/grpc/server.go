package grpc

import (
	"context"
	"errors"

	"github.com/sirupsen/logrus"
	"github.com/vibast-solutions/ms-go-notifications/app/dto"
	"github.com/vibast-solutions/ms-go-notifications/app/queue"
	"github.com/vibast-solutions/ms-go-notifications/app/service"
	types "github.com/vibast-solutions/ms-go-notifications/app/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Server struct {
	types.UnimplementedNotificationsServiceServer
	emailService *service.EmailService
	producer     queue.EmailPublisher
}

// NewServer constructs a gRPC server handler.
func NewServer(emailService *service.EmailService, producer queue.EmailPublisher) *Server {
	return &Server{emailService: emailService, producer: producer}
}

// SendRawEmail validates the request, stores history, and enqueues for delivery.
func (s *Server) SendRawEmail(ctx context.Context, req *types.SendRawEmailRequest) (*types.SendRawEmailResponse, error) {
	msg := dto.FromGRPC(req)
	if err := msg.Validate(); err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{
			"request_id": msg.RequestID,
			"recipient":  msg.Recipient,
		}).Debug("Send raw validation failed (grpc)")
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	logrus.WithFields(logrus.Fields{
		"request_id": msg.RequestID,
		"recipient":  msg.Recipient,
	}).Info("Received send raw request (grpc)")

	if err := s.emailService.CreateRequest(ctx, msg.RequestID, msg.Recipient, msg.Subject, msg.Content); err != nil {
		if errors.Is(err, service.ErrDuplicateRequestID) {
			logrus.WithField("request_id", msg.RequestID).Warn("Duplicate request_id")
			return nil, status.Error(codes.AlreadyExists, "duplicate request_id")
		}
		logrus.WithError(err).WithField("request_id", msg.RequestID).Error("Failed to create email history")
		return nil, status.Error(codes.Internal, "failed to create email history")
	}

	if err := s.producer.Publish(ctx, queue.EmailMessage{
		RequestID: msg.RequestID,
		Recipient: msg.Recipient,
		Subject:   msg.Subject,
		Content:   msg.Content,
	}); err != nil {
		_ = s.emailService.DeleteRequest(ctx, msg.RequestID)
		logrus.WithError(err).WithField("request_id", msg.RequestID).Error("Failed to queue email")
		return nil, status.Error(codes.Internal, "failed to queue email")
	}

	logrus.WithField("request_id", msg.RequestID).Info("Email request queued (grpc)")
	return &types.SendRawEmailResponse{Success: true}, nil
}
