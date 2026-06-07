// Unit tests for booking, derived from SPEC.yaml.
//
// What this service is:
//   booking orchestrates the hotel reservation saga across two upstream
//   services (inventory, payment). CreateBooking runs:
//       inventory.Hold → payment.Authorize → payment.Capture
//                      → inventory.Commit  → persist record
//   On failure at step N, compensating actions for steps 1..N-1 run.
//
// Test contract this file assumes about the implementation:
//   - `server.NewServer(payment, inventory)` returns a *Server where the two
//     args implement the gRPC-generated PaymentServiceClient and
//     InventoryServiceClient interfaces. Tests inject mocks that record
//     calls.
//   - Standard gRPC handler signatures; errors via status.Error.
//
// These tests are authored out-of-band and audited by a human. The agent
// MUST NOT modify this file.

package server_test

import (
	"context"
	"testing"

	bookingpb "agentbench/services/booking/api/v1"
	"agentbench/services/booking/internal/server"
	inventorypb "agentbench/services/inventory/api/v1"
	paymentpb "agentbench/services/payment/api/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ─── Mocks ──────────────────────────────────────────────────────────────────
//
// Each mock implements the gRPC-generated *ServiceClient interface. Fields
// `XxxFn` configure per-test behavior; `XxxCalls` record what was invoked
// so tests can verify saga ordering and compensations.

type mockInventory struct {
	SetStockFn func(ctx context.Context, in *inventorypb.SetStockRequest, opts ...grpc.CallOption) (*inventorypb.SetStockResponse, error)
	HoldFn     func(ctx context.Context, in *inventorypb.HoldRequest, opts ...grpc.CallOption) (*inventorypb.HoldResponse, error)
	CommitFn   func(ctx context.Context, in *inventorypb.CommitRequest, opts ...grpc.CallOption) (*inventorypb.CommitResponse, error)
	ReleaseFn  func(ctx context.Context, in *inventorypb.ReleaseRequest, opts ...grpc.CallOption) (*inventorypb.ReleaseResponse, error)

	HoldCalls    []*inventorypb.HoldRequest
	CommitCalls  []*inventorypb.CommitRequest
	ReleaseCalls []*inventorypb.ReleaseRequest
}

func (m *mockInventory) SetStock(ctx context.Context, in *inventorypb.SetStockRequest, opts ...grpc.CallOption) (*inventorypb.SetStockResponse, error) {
	if m.SetStockFn != nil {
		return m.SetStockFn(ctx, in, opts...)
	}
	return &inventorypb.SetStockResponse{}, nil
}
func (m *mockInventory) Hold(ctx context.Context, in *inventorypb.HoldRequest, opts ...grpc.CallOption) (*inventorypb.HoldResponse, error) {
	m.HoldCalls = append(m.HoldCalls, in)
	if m.HoldFn != nil {
		return m.HoldFn(ctx, in, opts...)
	}
	return &inventorypb.HoldResponse{HoldId: "hold-default"}, nil
}
func (m *mockInventory) Commit(ctx context.Context, in *inventorypb.CommitRequest, opts ...grpc.CallOption) (*inventorypb.CommitResponse, error) {
	m.CommitCalls = append(m.CommitCalls, in)
	if m.CommitFn != nil {
		return m.CommitFn(ctx, in, opts...)
	}
	return &inventorypb.CommitResponse{}, nil
}
func (m *mockInventory) Release(ctx context.Context, in *inventorypb.ReleaseRequest, opts ...grpc.CallOption) (*inventorypb.ReleaseResponse, error) {
	m.ReleaseCalls = append(m.ReleaseCalls, in)
	if m.ReleaseFn != nil {
		return m.ReleaseFn(ctx, in, opts...)
	}
	return &inventorypb.ReleaseResponse{}, nil
}

// ReturnStock satisfies the InventoryServiceClient interface (gained in
// inventory v3). The booking saga never calls it; this is a no-op so the
// mock keeps compiling against the wider interface.
func (m *mockInventory) ReturnStock(ctx context.Context, in *inventorypb.ReturnStockRequest, opts ...grpc.CallOption) (*inventorypb.ReturnStockResponse, error) {
	return &inventorypb.ReturnStockResponse{}, nil
}

type mockPayment struct {
	AuthorizeFn func(ctx context.Context, in *paymentpb.AuthorizeRequest, opts ...grpc.CallOption) (*paymentpb.AuthorizeResponse, error)
	CaptureFn   func(ctx context.Context, in *paymentpb.CaptureRequest, opts ...grpc.CallOption) (*paymentpb.CaptureResponse, error)
	VoidFn      func(ctx context.Context, in *paymentpb.VoidRequest, opts ...grpc.CallOption) (*paymentpb.VoidResponse, error)
	RefundFn    func(ctx context.Context, in *paymentpb.RefundRequest, opts ...grpc.CallOption) (*paymentpb.RefundResponse, error)

	AuthorizeCalls []*paymentpb.AuthorizeRequest
	CaptureCalls   []*paymentpb.CaptureRequest
	VoidCalls      []*paymentpb.VoidRequest
	RefundCalls    []*paymentpb.RefundRequest
}

func (m *mockPayment) Authorize(ctx context.Context, in *paymentpb.AuthorizeRequest, opts ...grpc.CallOption) (*paymentpb.AuthorizeResponse, error) {
	m.AuthorizeCalls = append(m.AuthorizeCalls, in)
	if m.AuthorizeFn != nil {
		return m.AuthorizeFn(ctx, in, opts...)
	}
	return &paymentpb.AuthorizeResponse{AuthId: "auth-default"}, nil
}
func (m *mockPayment) Capture(ctx context.Context, in *paymentpb.CaptureRequest, opts ...grpc.CallOption) (*paymentpb.CaptureResponse, error) {
	m.CaptureCalls = append(m.CaptureCalls, in)
	if m.CaptureFn != nil {
		return m.CaptureFn(ctx, in, opts...)
	}
	return &paymentpb.CaptureResponse{}, nil
}
func (m *mockPayment) Void(ctx context.Context, in *paymentpb.VoidRequest, opts ...grpc.CallOption) (*paymentpb.VoidResponse, error) {
	m.VoidCalls = append(m.VoidCalls, in)
	if m.VoidFn != nil {
		return m.VoidFn(ctx, in, opts...)
	}
	return &paymentpb.VoidResponse{}, nil
}
func (m *mockPayment) Refund(ctx context.Context, in *paymentpb.RefundRequest, opts ...grpc.CallOption) (*paymentpb.RefundResponse, error) {
	m.RefundCalls = append(m.RefundCalls, in)
	if m.RefundFn != nil {
		return m.RefundFn(ctx, in, opts...)
	}
	return &paymentpb.RefundResponse{}, nil
}

// ─── Helpers ────────────────────────────────────────────────────────────────

func validRequest() *bookingpb.CreateBookingRequest {
	return &bookingpb.CreateBookingRequest{
		UserId:         "user-1",
		HotelId:        "hotel-1",
		RoomType:       "STD",
		Date:           "2025-01-01",
		Amount:         100,
		Currency:       "USD",
		PaymentToken:   "tok",
		IdempotencyKey: "key-1",
	}
}

func wantCode(t *testing.T, err error, want codes.Code) {
	t.Helper()
	if got := status.Code(err); got != want {
		t.Errorf("code = %v, want %v (err=%v)", got, want, err)
	}
}

// ════════════════════════════════════════════════════════════════════════════
// CreateBooking — happy path and saga branches
// ════════════════════════════════════════════════════════════════════════════

// Happy path: all upstream calls succeed; booking is persisted.
// Verifies the saga calls Hold → Authorize → Capture → Commit and no
// compensations.
func TestCreateBooking_HappyPath(t *testing.T) {
	pay, inv := &mockPayment{}, &mockInventory{}
	s := server.NewServer(pay, inv)

	resp, err := s.CreateBooking(context.Background(), validRequest())
	if err != nil {
		t.Fatalf("CreateBooking: %v", err)
	}
	if resp.GetBookingId() == "" {
		t.Errorf("booking_id is empty")
	}
	if len(inv.HoldCalls) != 1 {
		t.Errorf("inv.Hold calls = %d, want 1", len(inv.HoldCalls))
	}
	if len(pay.AuthorizeCalls) != 1 {
		t.Errorf("pay.Authorize calls = %d, want 1", len(pay.AuthorizeCalls))
	}
	if len(pay.CaptureCalls) != 1 {
		t.Errorf("pay.Capture calls = %d, want 1", len(pay.CaptureCalls))
	}
	if len(inv.CommitCalls) != 1 {
		t.Errorf("inv.Commit calls = %d, want 1", len(inv.CommitCalls))
	}
	if len(inv.ReleaseCalls) != 0 || len(pay.VoidCalls) != 0 || len(pay.RefundCalls) != 0 {
		t.Errorf("unexpected compensations: release=%d void=%d refund=%d",
			len(inv.ReleaseCalls), len(pay.VoidCalls), len(pay.RefundCalls))
	}
}

// Hold fails first → no payment calls, no compensations needed.
func TestCreateBooking_HoldFails(t *testing.T) {
	inv := &mockInventory{HoldFn: func(_ context.Context, _ *inventorypb.HoldRequest, _ ...grpc.CallOption) (*inventorypb.HoldResponse, error) {
		return nil, status.Error(codes.FailedPrecondition, "no stock")
	}}
	pay := &mockPayment{}
	s := server.NewServer(pay, inv)

	_, err := s.CreateBooking(context.Background(), validRequest())
	wantCode(t, err, codes.FailedPrecondition)
	if len(pay.AuthorizeCalls) != 0 {
		t.Errorf("Authorize called after Hold failed (%d times)", len(pay.AuthorizeCalls))
	}
	if len(inv.ReleaseCalls) != 0 {
		t.Errorf("Release called unnecessarily (%d times)", len(inv.ReleaseCalls))
	}
}

// Authorize fails after Hold → MUST call inventory.Release to compensate;
// MUST NOT call Capture or Commit.
func TestCreateBooking_AuthorizeFails_ReleasesHold(t *testing.T) {
	pay := &mockPayment{AuthorizeFn: func(_ context.Context, _ *paymentpb.AuthorizeRequest, _ ...grpc.CallOption) (*paymentpb.AuthorizeResponse, error) {
		return nil, status.Error(codes.FailedPrecondition, "card declined")
	}}
	inv := &mockInventory{}
	s := server.NewServer(pay, inv)

	_, err := s.CreateBooking(context.Background(), validRequest())
	wantCode(t, err, codes.FailedPrecondition)
	if len(inv.ReleaseCalls) != 1 {
		t.Errorf("inv.Release calls = %d, want 1", len(inv.ReleaseCalls))
	}
	if len(pay.CaptureCalls) != 0 || len(inv.CommitCalls) != 0 {
		t.Errorf("subsequent saga steps called after Authorize fail")
	}
}

// Capture fails after Authorize → MUST call payment.Void AND inventory.Release.
func TestCreateBooking_CaptureFails_VoidsAndReleases(t *testing.T) {
	pay := &mockPayment{CaptureFn: func(_ context.Context, _ *paymentpb.CaptureRequest, _ ...grpc.CallOption) (*paymentpb.CaptureResponse, error) {
		return nil, status.Error(codes.Internal, "capture error")
	}}
	inv := &mockInventory{}
	s := server.NewServer(pay, inv)

	_, err := s.CreateBooking(context.Background(), validRequest())
	wantCode(t, err, codes.FailedPrecondition)
	if len(pay.VoidCalls) != 1 {
		t.Errorf("pay.Void calls = %d, want 1", len(pay.VoidCalls))
	}
	if len(inv.ReleaseCalls) != 1 {
		t.Errorf("inv.Release calls = %d, want 1", len(inv.ReleaseCalls))
	}
}

// Commit fails after Capture → MUST call payment.Refund (money was already
// charged; inventory was held but never committed so no inventory action needed).
func TestCreateBooking_CommitFails_Refunds(t *testing.T) {
	pay := &mockPayment{}
	inv := &mockInventory{CommitFn: func(_ context.Context, _ *inventorypb.CommitRequest, _ ...grpc.CallOption) (*inventorypb.CommitResponse, error) {
		return nil, status.Error(codes.Internal, "commit error")
	}}
	s := server.NewServer(pay, inv)

	_, err := s.CreateBooking(context.Background(), validRequest())
	wantCode(t, err, codes.FailedPrecondition)
	if len(pay.RefundCalls) != 1 {
		t.Errorf("pay.Refund calls = %d, want 1", len(pay.RefundCalls))
	}
}

// Idempotency: a second CreateBooking with the same idempotency_key returns
// the same booking_id and does NOT re-run the saga (no extra upstream calls).
func TestCreateBooking_Idempotent(t *testing.T) {
	pay, inv := &mockPayment{}, &mockInventory{}
	s := server.NewServer(pay, inv)

	r1, err := s.CreateBooking(context.Background(), validRequest())
	if err != nil {
		t.Fatalf("first CreateBooking: %v", err)
	}
	r2, err := s.CreateBooking(context.Background(), validRequest())
	if err != nil {
		t.Errorf("second CreateBooking (same idempotency_key): %v", err)
	}
	if r1.GetBookingId() != r2.GetBookingId() {
		t.Errorf("idempotent calls returned different ids: %q vs %q", r1.BookingId, r2.BookingId)
	}
	if len(inv.HoldCalls) != 1 || len(pay.AuthorizeCalls) != 1 {
		t.Errorf("saga ran twice on idempotent retry (Hold=%d, Authorize=%d)",
			len(inv.HoldCalls), len(pay.AuthorizeCalls))
	}
}

// Missing required fields → InvalidArgument, no upstream calls.
func TestCreateBooking_InvalidArgument(t *testing.T) {
	pay, inv := &mockPayment{}, &mockInventory{}
	s := server.NewServer(pay, inv)

	cases := []struct {
		name string
		mut  func(*bookingpb.CreateBookingRequest)
	}{
		{"empty user_id", func(r *bookingpb.CreateBookingRequest) { r.UserId = "" }},
		{"empty hotel_id", func(r *bookingpb.CreateBookingRequest) { r.HotelId = "" }},
		{"empty room_type", func(r *bookingpb.CreateBookingRequest) { r.RoomType = "" }},
		{"empty date", func(r *bookingpb.CreateBookingRequest) { r.Date = "" }},
		{"empty currency", func(r *bookingpb.CreateBookingRequest) { r.Currency = "" }},
		{"empty payment_token", func(r *bookingpb.CreateBookingRequest) { r.PaymentToken = "" }},
		{"empty idempotency_key", func(r *bookingpb.CreateBookingRequest) { r.IdempotencyKey = "" }},
		{"zero amount", func(r *bookingpb.CreateBookingRequest) { r.Amount = 0 }},
		{"negative amount", func(r *bookingpb.CreateBookingRequest) { r.Amount = -1 }},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := validRequest()
			req.IdempotencyKey = req.IdempotencyKey + "-" + tc.name // distinct keys per subtest
			_ = i
			tc.mut(req)
			_, err := s.CreateBooking(context.Background(), req)
			wantCode(t, err, codes.InvalidArgument)
		})
	}
}

// ════════════════════════════════════════════════════════════════════════════
// GetBooking / ListBookings
// ════════════════════════════════════════════════════════════════════════════

// GetBooking on a confirmed booking returns the fields, including auth_id
// (used by cancellation later).
func TestGetBooking_Happy(t *testing.T) {
	pay := &mockPayment{AuthorizeFn: func(_ context.Context, _ *paymentpb.AuthorizeRequest, _ ...grpc.CallOption) (*paymentpb.AuthorizeResponse, error) {
		return &paymentpb.AuthorizeResponse{AuthId: "auth-known"}, nil
	}}
	inv := &mockInventory{}
	s := server.NewServer(pay, inv)

	r, err := s.CreateBooking(context.Background(), validRequest())
	if err != nil {
		t.Fatalf("CreateBooking: %v", err)
	}
	got, err := s.GetBooking(context.Background(), &bookingpb.GetBookingRequest{BookingId: r.GetBookingId()})
	if err != nil {
		t.Fatalf("GetBooking: %v", err)
	}
	if got.GetUserId() != "user-1" || got.GetHotelId() != "hotel-1" {
		t.Errorf("GetBooking returned wrong fields: %+v", got)
	}
	if got.GetAuthId() != "auth-known" {
		t.Errorf("GetBooking did not persist auth_id (got %q)", got.GetAuthId())
	}
	if got.GetStatus() != "confirmed" {
		t.Errorf("status = %q, want \"confirmed\"", got.GetStatus())
	}
}

// GetBooking on unknown id returns NotFound.
func TestGetBooking_NotFound(t *testing.T) {
	pay, inv := &mockPayment{}, &mockInventory{}
	s := server.NewServer(pay, inv)
	_, err := s.GetBooking(context.Background(), &bookingpb.GetBookingRequest{BookingId: "unknown"})
	wantCode(t, err, codes.NotFound)
}

// ListBookings for a user with no bookings returns empty list (not error).
func TestListBookings_Empty(t *testing.T) {
	pay, inv := &mockPayment{}, &mockInventory{}
	s := server.NewServer(pay, inv)
	r, err := s.ListBookings(context.Background(), &bookingpb.ListBookingsRequest{UserId: "u-none"})
	if err != nil {
		t.Fatalf("ListBookings: %v", err)
	}
	if len(r.GetBookingIds()) != 0 {
		t.Errorf("expected empty list, got %d ids", len(r.BookingIds))
	}
}

// ListBookings returns exactly the user's bookings (filters by user_id).
func TestListBookings_FiltersByUser(t *testing.T) {
	pay, inv := &mockPayment{}, &mockInventory{}
	s := server.NewServer(pay, inv)

	for i, owner := range []string{"user-1", "user-1", "user-2"} {
		req := validRequest()
		req.UserId = owner
		req.IdempotencyKey = "key-list-" + string(rune('A'+i))
		if _, err := s.CreateBooking(context.Background(), req); err != nil {
			t.Fatalf("seed booking %d: %v", i, err)
		}
	}

	r, err := s.ListBookings(context.Background(), &bookingpb.ListBookingsRequest{UserId: "user-1"})
	if err != nil {
		t.Fatalf("ListBookings: %v", err)
	}
	if len(r.GetBookingIds()) != 2 {
		t.Errorf("user-1 should have 2 bookings, got %d", len(r.BookingIds))
	}
}
