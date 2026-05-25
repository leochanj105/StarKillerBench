package server

import (
	"context"

	bookingpb "agentbench/services/booking/api/v1"
	pb "agentbench/services/cancellation/api/v1"
	paymentpb "agentbench/services/payment/api/v1"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Server struct {
	pb.UnimplementedCancellationServiceServer

	booking bookingpb.BookingServiceClient
	payment paymentpb.PaymentServiceClient
}

func NewServer(booking bookingpb.BookingServiceClient, payment paymentpb.PaymentServiceClient) *Server {
	return &Server{booking: booking, payment: payment}
}

func (s *Server) Cancel(ctx context.Context, req *pb.CancelRequest) (*pb.CancelResponse, error) {
	if req.GetBookingId() == "" {
		return nil, status.Error(codes.InvalidArgument, "booking_id is required")
	}

	b, err := s.booking.GetBooking(ctx, &bookingpb.GetBookingRequest{BookingId: req.GetBookingId()})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, status.Error(codes.NotFound, "booking not found")
		}
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}

	if _, err := s.payment.Refund(ctx, &paymentpb.RefundRequest{
		AuthId: b.GetAuthId(),
		Amount: b.GetAmount(),
	}); err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}

	return &pb.CancelResponse{}, nil
}
