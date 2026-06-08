// Package server implements the NotificationService gRPC handlers.
package server

import (
	"context"
	"fmt"

	pb "agentbench/services/notification/api/v1"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Server implements pb.NotificationServiceServer. It is stateless and
// deterministic: each event maps to a fixed set of outbound messages.
type Server struct {
	pb.UnimplementedNotificationServiceServer
}

// NewServer returns a ready-to-use *Server.
func NewServer() *Server {
	return &Server{}
}

// eventBodies maps a recognized event_type to a template producing the email
// body for a given booking_id.
var eventBodies = map[string]func(bookingID string) string{
	"booking.confirmed": func(bookingID string) string {
		return fmt.Sprintf("Your booking %s is confirmed.", bookingID)
	},
	"booking.cancelled": func(bookingID string) string {
		return fmt.Sprintf("Your booking %s has been cancelled.", bookingID)
	},
}

// Notify generates the outbound messages for one event and returns them. In v1
// every recognized event yields exactly one "email" message whose body
// references the booking_id.
func (s *Server) Notify(ctx context.Context, req *pb.NotifyRequest) (*pb.NotifyResponse, error) {
	if req.GetEventType() == "" || req.GetUserId() == "" || req.GetBookingId() == "" {
		return nil, status.Error(codes.InvalidArgument, "event_type, user_id, and booking_id are required")
	}

	body, ok := eventBodies[req.GetEventType()]
	if !ok {
		return nil, status.Errorf(codes.InvalidArgument, "unrecognized event_type %q", req.GetEventType())
	}

	return &pb.NotifyResponse{
		Messages: []*pb.Message{
			{
				Channel: "email",
				Body:    body(req.GetBookingId()),
			},
		},
	}, nil
}
