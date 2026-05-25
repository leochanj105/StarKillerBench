package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"

	bookingpb "agentbench/services/booking/api/v1"
	inventorypb "agentbench/services/inventory/api/v1"
	paymentpb "agentbench/services/payment/api/v1"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type booking struct {
	userID   string
	hotelID  string
	roomType string
	date     string
	amount   int64
	currency string
	authID   string
	status   string
}

type Server struct {
	bookingpb.UnimplementedBookingServiceServer

	payment   paymentpb.PaymentServiceClient
	inventory inventorypb.InventoryServiceClient

	mu          sync.Mutex
	bookings    map[string]*booking
	idempotency map[string]string
}

func NewServer(payment paymentpb.PaymentServiceClient, inventory inventorypb.InventoryServiceClient) *Server {
	return &Server{
		payment:     payment,
		inventory:   inventory,
		bookings:    make(map[string]*booking),
		idempotency: make(map[string]string),
	}
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func (s *Server) CreateBooking(ctx context.Context, req *bookingpb.CreateBookingRequest) (*bookingpb.CreateBookingResponse, error) {
	if req.GetUserId() == "" ||
		req.GetHotelId() == "" ||
		req.GetRoomType() == "" ||
		req.GetDate() == "" ||
		req.GetCurrency() == "" ||
		req.GetPaymentToken() == "" ||
		req.GetIdempotencyKey() == "" ||
		req.GetAmount() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "missing required field or non-positive amount")
	}

	s.mu.Lock()
	if id, ok := s.idempotency[req.GetIdempotencyKey()]; ok {
		s.mu.Unlock()
		return &bookingpb.CreateBookingResponse{BookingId: id}, nil
	}
	s.mu.Unlock()

	holdResp, err := s.inventory.Hold(ctx, &inventorypb.HoldRequest{
		HotelId:  req.GetHotelId(),
		RoomType: req.GetRoomType(),
		Date:     req.GetDate(),
		Quantity: 1,
	})
	if err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "inventory hold failed: %v", err)
	}
	holdID := holdResp.GetHoldId()

	authResp, err := s.payment.Authorize(ctx, &paymentpb.AuthorizeRequest{
		Amount:       req.GetAmount(),
		Currency:     req.GetCurrency(),
		PaymentToken: req.GetPaymentToken(),
	})
	if err != nil {
		_, _ = s.inventory.Release(ctx, &inventorypb.ReleaseRequest{HoldId: holdID})
		return nil, status.Errorf(codes.FailedPrecondition, "payment authorize failed: %v", err)
	}
	authID := authResp.GetAuthId()

	if _, err := s.payment.Capture(ctx, &paymentpb.CaptureRequest{AuthId: authID}); err != nil {
		_, _ = s.payment.Void(ctx, &paymentpb.VoidRequest{AuthId: authID})
		_, _ = s.inventory.Release(ctx, &inventorypb.ReleaseRequest{HoldId: holdID})
		return nil, status.Errorf(codes.FailedPrecondition, "payment capture failed: %v", err)
	}

	if _, err := s.inventory.Commit(ctx, &inventorypb.CommitRequest{HoldId: holdID}); err != nil {
		_, _ = s.payment.Refund(ctx, &paymentpb.RefundRequest{AuthId: authID, Amount: req.GetAmount()})
		return nil, status.Errorf(codes.FailedPrecondition, "inventory commit failed: %v", err)
	}

	bookingID := newID()
	s.mu.Lock()
	s.bookings[bookingID] = &booking{
		userID:   req.GetUserId(),
		hotelID:  req.GetHotelId(),
		roomType: req.GetRoomType(),
		date:     req.GetDate(),
		amount:   req.GetAmount(),
		currency: req.GetCurrency(),
		authID:   authID,
		status:   "confirmed",
	}
	s.idempotency[req.GetIdempotencyKey()] = bookingID
	s.mu.Unlock()

	return &bookingpb.CreateBookingResponse{BookingId: bookingID}, nil
}

func (s *Server) GetBooking(ctx context.Context, req *bookingpb.GetBookingRequest) (*bookingpb.GetBookingResponse, error) {
	s.mu.Lock()
	b, ok := s.bookings[req.GetBookingId()]
	s.mu.Unlock()
	if !ok {
		return nil, status.Errorf(codes.NotFound, "booking %q not found", req.GetBookingId())
	}
	return &bookingpb.GetBookingResponse{
		UserId:   b.userID,
		HotelId:  b.hotelID,
		RoomType: b.roomType,
		Date:     b.date,
		Amount:   b.amount,
		Currency: b.currency,
		AuthId:   b.authID,
		Status:   b.status,
	}, nil
}

func (s *Server) ListBookings(ctx context.Context, req *bookingpb.ListBookingsRequest) (*bookingpb.ListBookingsResponse, error) {
	ids := []string{}
	s.mu.Lock()
	for id, b := range s.bookings {
		if b.userID == req.GetUserId() {
			ids = append(ids, id)
		}
	}
	s.mu.Unlock()
	return &bookingpb.ListBookingsResponse{BookingIds: ids}, nil
}
