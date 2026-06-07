package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"

	bookingpb "agentbench/services/booking/api/v1"
	"agentbench/services/booking/internal/store"
	inventorypb "agentbench/services/inventory/api/v1"
	paymentpb "agentbench/services/payment/api/v1"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Server implements BookingService. It orchestrates the booking saga across
// the payment and inventory services and persists confirmed bookings through
// a pluggable Store (in-memory or Postgres).
type Server struct {
	bookingpb.UnimplementedBookingServiceServer

	payment   paymentpb.PaymentServiceClient
	inventory inventorypb.InventoryServiceClient
	store     store.Store

	// Per-idempotency-key serialization. Two concurrent CreateBooking calls
	// with the same key must not both run the saga; the keyed lock makes the
	// loser wait, observe the persisted record, and return it. (The store's
	// UNIQUE constraint backs this up across processes.)
	mu       sync.Mutex
	keyLocks map[string]*sync.Mutex
}

// NewServer builds a Server backed by an in-memory store. Used by v1 unit
// tests and local dev when PG_DSN is unset.
func NewServer(payment paymentpb.PaymentServiceClient, inventory inventorypb.InventoryServiceClient) *Server {
	return NewServerWithStore(payment, inventory, store.NewMemStore())
}

// NewServerWithStore builds a Server backed by the supplied store.
func NewServerWithStore(payment paymentpb.PaymentServiceClient, inventory inventorypb.InventoryServiceClient, st store.Store) *Server {
	return &Server{
		payment:   payment,
		inventory: inventory,
		store:     st,
		keyLocks:  make(map[string]*sync.Mutex),
	}
}

// lockFor returns the mutex guarding a single idempotency key.
func (s *Server) lockFor(key string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.keyLocks[key]
	if !ok {
		l = &sync.Mutex{}
		s.keyLocks[key] = l
	}
	return l
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

	lock := s.lockFor(req.GetIdempotencyKey())
	lock.Lock()
	defer lock.Unlock()

	// Idempotency: a key seen by a prior successful CreateBooking returns the
	// stored booking_id without re-running the saga.
	if id, ok, err := s.store.ByIdempotencyKey(ctx, req.GetIdempotencyKey()); err != nil {
		return nil, status.Errorf(codes.Internal, "idempotency lookup failed: %v", err)
	} else if ok {
		return &bookingpb.CreateBookingResponse{BookingId: id}, nil
	}

	// ── Saga: Hold → Authorize → Capture → Commit, with compensations ──
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

	// Persist. On a cross-process key conflict the store returns the winner's
	// booking_id; we hand that back rather than the one we minted.
	storedID, _, err := s.store.Create(ctx, store.Booking{
		BookingID:      newID(),
		IdempotencyKey: req.GetIdempotencyKey(),
		UserID:         req.GetUserId(),
		HotelID:        req.GetHotelId(),
		RoomType:       req.GetRoomType(),
		Date:           req.GetDate(),
		Amount:         req.GetAmount(),
		Currency:       req.GetCurrency(),
		AuthID:         authID,
		Status:         "confirmed",
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "persist booking failed: %v", err)
	}

	return &bookingpb.CreateBookingResponse{BookingId: storedID}, nil
}

func (s *Server) GetBooking(ctx context.Context, req *bookingpb.GetBookingRequest) (*bookingpb.GetBookingResponse, error) {
	b, ok, err := s.store.Get(ctx, req.GetBookingId())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get booking failed: %v", err)
	}
	if !ok {
		return nil, status.Errorf(codes.NotFound, "booking %q not found", req.GetBookingId())
	}
	return &bookingpb.GetBookingResponse{
		UserId:   b.UserID,
		HotelId:  b.HotelID,
		RoomType: b.RoomType,
		Date:     b.Date,
		Amount:   b.Amount,
		Currency: b.Currency,
		AuthId:   b.AuthID,
		Status:   b.Status,
	}, nil
}

func (s *Server) ListBookings(ctx context.Context, req *bookingpb.ListBookingsRequest) (*bookingpb.ListBookingsResponse, error) {
	ids, err := s.store.ListByUser(ctx, req.GetUserId())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list bookings failed: %v", err)
	}
	return &bookingpb.ListBookingsResponse{BookingIds: ids}, nil
}
