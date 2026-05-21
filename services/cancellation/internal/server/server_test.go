// Unit tests for cancellation, derived from SPEC.yaml.
//
// What this service is:
//   cancellation reverses a booking. Cancel(booking_id) calls
//   booking.GetBooking to retrieve the auth_id + amount, then calls
//   payment.Refund. Stateless.
//
// Test contract assumed about the implementation:
//   - `server.NewServer(booking, payment)` returns a *Server where the two
//     args implement bookingpb.BookingServiceClient and
//     paymentpb.PaymentServiceClient respectively. Tests inject mocks.
//   - Standard gRPC signatures; errors via status.Error.

package server_test

import (
	"context"
	"testing"

	bookingpb "agentbench/services/booking/internal/genpb/v1"
	pb "agentbench/services/cancellation/internal/genpb/v1"
	"agentbench/services/cancellation/internal/server"
	paymentpb "agentbench/services/payment/internal/genpb/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ─── Mocks ──────────────────────────────────────────────────────────────────

type mockBooking struct {
	CreateBookingFn func(ctx context.Context, in *bookingpb.CreateBookingRequest, opts ...grpc.CallOption) (*bookingpb.CreateBookingResponse, error)
	GetBookingFn    func(ctx context.Context, in *bookingpb.GetBookingRequest, opts ...grpc.CallOption) (*bookingpb.GetBookingResponse, error)
	ListBookingsFn  func(ctx context.Context, in *bookingpb.ListBookingsRequest, opts ...grpc.CallOption) (*bookingpb.ListBookingsResponse, error)

	GetBookingCalls []*bookingpb.GetBookingRequest
}

func (m *mockBooking) CreateBooking(ctx context.Context, in *bookingpb.CreateBookingRequest, opts ...grpc.CallOption) (*bookingpb.CreateBookingResponse, error) {
	if m.CreateBookingFn != nil {
		return m.CreateBookingFn(ctx, in, opts...)
	}
	return nil, status.Error(codes.Unimplemented, "not mocked")
}
func (m *mockBooking) GetBooking(ctx context.Context, in *bookingpb.GetBookingRequest, opts ...grpc.CallOption) (*bookingpb.GetBookingResponse, error) {
	m.GetBookingCalls = append(m.GetBookingCalls, in)
	if m.GetBookingFn != nil {
		return m.GetBookingFn(ctx, in, opts...)
	}
	// Default: a "confirmed" booking with predictable fields.
	return &bookingpb.GetBookingResponse{
		UserId: "u-1", HotelId: "h-1", RoomType: "STD", Date: "2025-01-01",
		Amount: 100, Currency: "USD", AuthId: "auth-1", Status: "confirmed",
	}, nil
}
func (m *mockBooking) ListBookings(ctx context.Context, in *bookingpb.ListBookingsRequest, opts ...grpc.CallOption) (*bookingpb.ListBookingsResponse, error) {
	if m.ListBookingsFn != nil {
		return m.ListBookingsFn(ctx, in, opts...)
	}
	return nil, status.Error(codes.Unimplemented, "not mocked")
}

type mockPayment struct {
	AuthorizeFn func(ctx context.Context, in *paymentpb.AuthorizeRequest, opts ...grpc.CallOption) (*paymentpb.AuthorizeResponse, error)
	CaptureFn   func(ctx context.Context, in *paymentpb.CaptureRequest, opts ...grpc.CallOption) (*paymentpb.CaptureResponse, error)
	VoidFn      func(ctx context.Context, in *paymentpb.VoidRequest, opts ...grpc.CallOption) (*paymentpb.VoidResponse, error)
	RefundFn    func(ctx context.Context, in *paymentpb.RefundRequest, opts ...grpc.CallOption) (*paymentpb.RefundResponse, error)

	RefundCalls []*paymentpb.RefundRequest
}

func (m *mockPayment) Authorize(ctx context.Context, in *paymentpb.AuthorizeRequest, opts ...grpc.CallOption) (*paymentpb.AuthorizeResponse, error) {
	if m.AuthorizeFn != nil {
		return m.AuthorizeFn(ctx, in, opts...)
	}
	return nil, status.Error(codes.Unimplemented, "not mocked")
}
func (m *mockPayment) Capture(ctx context.Context, in *paymentpb.CaptureRequest, opts ...grpc.CallOption) (*paymentpb.CaptureResponse, error) {
	if m.CaptureFn != nil {
		return m.CaptureFn(ctx, in, opts...)
	}
	return nil, status.Error(codes.Unimplemented, "not mocked")
}
func (m *mockPayment) Void(ctx context.Context, in *paymentpb.VoidRequest, opts ...grpc.CallOption) (*paymentpb.VoidResponse, error) {
	if m.VoidFn != nil {
		return m.VoidFn(ctx, in, opts...)
	}
	return nil, status.Error(codes.Unimplemented, "not mocked")
}
func (m *mockPayment) Refund(ctx context.Context, in *paymentpb.RefundRequest, opts ...grpc.CallOption) (*paymentpb.RefundResponse, error) {
	m.RefundCalls = append(m.RefundCalls, in)
	if m.RefundFn != nil {
		return m.RefundFn(ctx, in, opts...)
	}
	return &paymentpb.RefundResponse{}, nil
}

func wantCode(t *testing.T, err error, want codes.Code) {
	t.Helper()
	if got := status.Code(err); got != want {
		t.Errorf("code = %v, want %v (err=%v)", got, want, err)
	}
}

// ════════════════════════════════════════════════════════════════════════════
// Cancel
// ════════════════════════════════════════════════════════════════════════════

// Happy: Cancel looks up the booking and refunds with the booking's
// auth_id and amount. Both upstream calls happen exactly once.
func TestCancel_Happy(t *testing.T) {
	book, pay := &mockBooking{}, &mockPayment{}
	s := server.NewServer(book, pay)

	_, err := s.Cancel(context.Background(), &pb.CancelRequest{BookingId: "b-1"})
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if len(book.GetBookingCalls) != 1 {
		t.Errorf("book.GetBooking calls = %d, want 1", len(book.GetBookingCalls))
	}
	if len(pay.RefundCalls) != 1 {
		t.Errorf("pay.Refund calls = %d, want 1", len(pay.RefundCalls))
	}
	r := pay.RefundCalls[0]
	if r.GetAuthId() != "auth-1" || r.GetAmount() != 100 {
		t.Errorf("Refund called with wrong fields: auth_id=%q amount=%d",
			r.GetAuthId(), r.GetAmount())
	}
}

// Empty booking_id → InvalidArgument; no upstream calls.
func TestCancel_EmptyBookingId(t *testing.T) {
	book, pay := &mockBooking{}, &mockPayment{}
	s := server.NewServer(book, pay)

	_, err := s.Cancel(context.Background(), &pb.CancelRequest{BookingId: ""})
	wantCode(t, err, codes.InvalidArgument)
	if len(book.GetBookingCalls) != 0 || len(pay.RefundCalls) != 0 {
		t.Errorf("upstream calls made despite empty booking_id")
	}
}

// booking.GetBooking returns NotFound → Cancel returns NotFound; no Refund.
func TestCancel_BookingNotFound(t *testing.T) {
	book := &mockBooking{GetBookingFn: func(_ context.Context, _ *bookingpb.GetBookingRequest, _ ...grpc.CallOption) (*bookingpb.GetBookingResponse, error) {
		return nil, status.Error(codes.NotFound, "unknown booking")
	}}
	pay := &mockPayment{}
	s := server.NewServer(book, pay)

	_, err := s.Cancel(context.Background(), &pb.CancelRequest{BookingId: "missing"})
	wantCode(t, err, codes.NotFound)
	if len(pay.RefundCalls) != 0 {
		t.Errorf("Refund called even though booking not found")
	}
}

// payment.Refund returns any error → Cancel returns FailedPrecondition.
func TestCancel_RefundFails(t *testing.T) {
	book := &mockBooking{}
	pay := &mockPayment{RefundFn: func(_ context.Context, _ *paymentpb.RefundRequest, _ ...grpc.CallOption) (*paymentpb.RefundResponse, error) {
		return nil, status.Error(codes.Internal, "PSP outage")
	}}
	s := server.NewServer(book, pay)

	_, err := s.Cancel(context.Background(), &pb.CancelRequest{BookingId: "b-1"})
	wantCode(t, err, codes.FailedPrecondition)
}
