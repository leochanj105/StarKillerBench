package server

import (
	"context"

	bookingpb "agentbench/services/booking/api/v1"
	pb "agentbench/services/cancellation/api/v1"
	inventorypb "agentbench/services/inventory/api/v1"
	paymentpb "agentbench/services/payment/api/v1"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Server struct {
	pb.UnimplementedCancellationServiceServer

	booking   bookingpb.BookingServiceClient
	payment   paymentpb.PaymentServiceClient
	inventory inventorypb.InventoryServiceClient
}

// NewServer builds a v1 cancellation server: refund only, no inventory
// return. Retained so the frozen v1 test file keeps passing.
func NewServer(booking bookingpb.BookingServiceClient, payment paymentpb.PaymentServiceClient) *Server {
	return &Server{booking: booking, payment: payment}
}

// NewServerWithInventory builds a v2 cancellation server that, after the
// refund, returns the sold room to inventory via ReturnStock.
func NewServerWithInventory(booking bookingpb.BookingServiceClient, payment paymentpb.PaymentServiceClient, inventory inventorypb.InventoryServiceClient) *Server {
	return &Server{booking: booking, payment: payment, inventory: inventory}
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

	// Reversal 1: refund the money.
	if _, err := s.payment.Refund(ctx, &paymentpb.RefundRequest{
		AuthId: b.GetAuthId(),
		Amount: b.GetAmount(),
	}); err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}

	// Reversal 2: return the sold room to availability. No compensation:
	// a failure here leaves the refund applied (money returned, room not).
	if s.inventory != nil {
		if _, err := s.inventory.ReturnStock(ctx, &inventorypb.ReturnStockRequest{
			HotelId:  b.GetHotelId(),
			RoomType: b.GetRoomType(),
			Date:     b.GetDate(),
			Quantity: 1,
		}); err != nil {
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		}
	}

	return &pb.CancelResponse{}, nil
}
