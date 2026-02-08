package grpc

import (
	"context"
	"errors"

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
	producer     *queue.EmailProducer
}

// NewServer constructs a gRPC server handler.
func NewServer(emailService *service.EmailService, producer *queue.EmailProducer) *Server {
	return &Server{emailService: emailService, producer: producer}
}

// SendRawEmail validates the request, stores history, and enqueues for delivery.
func (s *Server) SendRawEmail(ctx context.Context, req *types.SendRawEmailRequest) (*types.SendRawEmailResponse, error) {
	msg := dto.FromGRPC(req)
	if err := msg.Validate(); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	if err := s.emailService.CreateRequest(ctx, msg.RequestID, msg.Recipient, msg.Subject, msg.Content); err != nil {
		if errors.Is(err, service.ErrDuplicateRequestID) {
			return nil, status.Error(codes.AlreadyExists, "duplicate request_id")
		}
		return nil, status.Error(codes.Internal, "failed to create email history")
	}

	if err := s.producer.Publish(ctx, queue.EmailMessage{
		RequestID: msg.RequestID,
		Recipient: msg.Recipient,
		Subject:   msg.Subject,
		Content:   msg.Content,
	}); err != nil {
		_ = s.emailService.DeleteRequest(ctx, msg.RequestID)
		return nil, status.Error(codes.Internal, "failed to queue email")
	}

	return &types.SendRawEmailResponse{Success: true}, nil
}
