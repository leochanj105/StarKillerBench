// Integration tests for the StarkillerBench Hotel Reservation saga.
//
// Talks to four real, container-deployed services via gRPC on host-published
// ports. The compose stack is expected to be already up — use
// framework/scripts/run_integration to orchestrate up → wait → test → down.
//
// Each test uses a unique (hotel_id, user_id) tuple so the in-memory state
// across services doesn't leak between tests. Tests do NOT run in parallel
// against shared state.

package integration_test

import (
	"context"
	"os"
	"testing"
	"time"

	bookingpb "agentbench/services/booking/api/v1"
	cancellationpb "agentbench/services/cancellation/api/v1"
	inventorypb "agentbench/services/inventory/api/v1"
	paymentpb "agentbench/services/payment/api/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

var (
	bookingClient      bookingpb.BookingServiceClient
	inventoryClient    inventorypb.InventoryServiceClient
	paymentClient      paymentpb.PaymentServiceClient
	cancellationClient cancellationpb.CancellationServiceClient
)

func TestMain(m *testing.M) {
	bookingClient = bookingpb.NewBookingServiceClient(mustDial(envOr("BOOKING_ADDR", "localhost:18103")))
	inventoryClient = inventorypb.NewInventoryServiceClient(mustDial(envOr("INVENTORY_ADDR", "localhost:18102")))
	paymentClient = paymentpb.NewPaymentServiceClient(mustDial(envOr("PAYMENT_ADDR", "localhost:18101")))
	cancellationClient = cancellationpb.NewCancellationServiceClient(mustDial(envOr("CANCELLATION_ADDR", "localhost:18104")))
	os.Exit(m.Run())
}

func mustDial(addr string) *grpc.ClientConn {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		panic("dial " + addr + ": " + err.Error())
	}
	return conn
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// setStock pre-seeds inventory and fails the test if SetStock errors. Used at
// the top of each saga test so the test doesn't depend on prior tests' state.
func setStock(t *testing.T, hotel, room, date string, qty int32) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := inventoryClient.SetStock(ctx, &inventorypb.SetStockRequest{
		HotelId: hotel, RoomType: room, Date: date, Quantity: qty,
	}); err != nil {
		t.Fatalf("SetStock(%s/%s/%s=%d): %v", hotel, room, date, qty, err)
	}
}

func wantCode(t *testing.T, err error, want codes.Code) {
	t.Helper()
	if got := status.Code(err); got != want {
		t.Errorf("status code = %v, want %v (err=%v)", got, want, err)
	}
}

// ════════════════════════════════════════════════════════════════════════════
// 1. Happy-path saga: full chain runs, booking persists, inventory commits.
// ════════════════════════════════════════════════════════════════════════════

// On success, the booking service must have: held → authorized → captured →
// committed. We verify the visible side-effects across services: the booking
// is persisted with status=confirmed, and the inventory unit is consumed
// (committed) so it stays gone — a subsequent Hold for the remaining stock
// works, but holding one more than remains is FailedPrecondition.
func TestSaga_HappyPath(t *testing.T) {
	ctx := context.Background()
	setStock(t, "H_happy", "STD", "2026-06-01", 2)

	resp, err := bookingClient.CreateBooking(ctx, &bookingpb.CreateBookingRequest{
		UserId: "u_happy", HotelId: "H_happy", RoomType: "STD", Date: "2026-06-01",
		Amount: 10000, Currency: "USD", PaymentToken: "tok_test", IdempotencyKey: "k_happy",
	})
	if err != nil {
		t.Fatalf("CreateBooking: %v", err)
	}
	if resp.GetBookingId() == "" {
		t.Fatalf("booking_id empty")
	}

	b, err := bookingClient.GetBooking(ctx, &bookingpb.GetBookingRequest{BookingId: resp.GetBookingId()})
	if err != nil {
		t.Fatalf("GetBooking: %v", err)
	}
	if b.GetStatus() != "confirmed" {
		t.Errorf("status = %q, want confirmed", b.GetStatus())
	}
	if b.GetUserId() != "u_happy" || b.GetHotelId() != "H_happy" || b.GetAmount() != 10000 {
		t.Errorf("GetBooking returned wrong record: %+v", b)
	}

	// Stock was 2, the saga committed 1 (sold). One unit should remain.
	if _, err := inventoryClient.Hold(ctx, &inventorypb.HoldRequest{
		HotelId: "H_happy", RoomType: "STD", Date: "2026-06-01", Quantity: 1,
	}); err != nil {
		t.Errorf("Hold of remaining 1 unit failed: %v", err)
	}
	// Now nothing is available — this Hold must fail with FailedPrecondition.
	_, err = inventoryClient.Hold(ctx, &inventorypb.HoldRequest{
		HotelId: "H_happy", RoomType: "STD", Date: "2026-06-01", Quantity: 1,
	})
	wantCode(t, err, codes.FailedPrecondition)
}

// ════════════════════════════════════════════════════════════════════════════
// 2. Saga fails cleanly when inventory cannot satisfy the Hold.
// ════════════════════════════════════════════════════════════════════════════

// With zero stock seeded, the inventory.Hold call inside CreateBooking must
// fail and the booking service must surface FailedPrecondition (per its
// error_semantics). No booking is persisted.
func TestSaga_InsufficientStock(t *testing.T) {
	ctx := context.Background()
	setStock(t, "H_nostock", "DLX", "2026-06-02", 0)

	_, err := bookingClient.CreateBooking(ctx, &bookingpb.CreateBookingRequest{
		UserId: "u_nostock", HotelId: "H_nostock", RoomType: "DLX", Date: "2026-06-02",
		Amount: 5000, Currency: "USD", PaymentToken: "tok_test", IdempotencyKey: "k_nostock",
	})
	wantCode(t, err, codes.FailedPrecondition)
}

// ════════════════════════════════════════════════════════════════════════════
// 3. Cancel flow: book, then cancel via the cancellation service.
// ════════════════════════════════════════════════════════════════════════════

// cancellation.Cancel must look the booking up via booking.GetBooking and
// then call payment.Refund. We verify success, then check the negative path:
// cancelling an unknown booking_id surfaces NotFound (cancellation reflects
// booking's NotFound).
func TestSaga_Cancel(t *testing.T) {
	ctx := context.Background()
	setStock(t, "H_cancel", "STD", "2026-06-03", 1)

	resp, err := bookingClient.CreateBooking(ctx, &bookingpb.CreateBookingRequest{
		UserId: "u_cancel", HotelId: "H_cancel", RoomType: "STD", Date: "2026-06-03",
		Amount: 8000, Currency: "USD", PaymentToken: "tok_test", IdempotencyKey: "k_cancel",
	})
	if err != nil {
		t.Fatalf("CreateBooking: %v", err)
	}

	if _, err := cancellationClient.Cancel(ctx, &cancellationpb.CancelRequest{BookingId: resp.GetBookingId()}); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	_, err = cancellationClient.Cancel(ctx, &cancellationpb.CancelRequest{BookingId: "does-not-exist"})
	wantCode(t, err, codes.NotFound)
}

// ════════════════════════════════════════════════════════════════════════════
// 4. ListBookings groups bookings by user_id across multiple saga runs.
// ════════════════════════════════════════════════════════════════════════════

// Two bookings for alice, one for bob, in a single (hotel, date). Each must
// land in the right user's list. A third user with no bookings sees an empty
// list.
func TestSaga_ListBookings(t *testing.T) {
	ctx := context.Background()
	setStock(t, "H_list", "STD", "2026-06-04", 5)

	mkBooking := func(user, idem string) string {
		t.Helper()
		r, err := bookingClient.CreateBooking(ctx, &bookingpb.CreateBookingRequest{
			UserId: user, HotelId: "H_list", RoomType: "STD", Date: "2026-06-04",
			Amount: 5000, Currency: "USD", PaymentToken: "tok_test", IdempotencyKey: idem,
		})
		if err != nil {
			t.Fatalf("CreateBooking(%s): %v", user, err)
		}
		return r.GetBookingId()
	}
	mkBooking("alice_list", "k_list_a1")
	mkBooking("alice_list", "k_list_a2")
	mkBooking("bob_list", "k_list_b1")

	a, err := bookingClient.ListBookings(ctx, &bookingpb.ListBookingsRequest{UserId: "alice_list"})
	if err != nil {
		t.Fatalf("ListBookings(alice): %v", err)
	}
	if got := len(a.GetBookingIds()); got != 2 {
		t.Errorf("alice has %d bookings, want 2", got)
	}

	b, err := bookingClient.ListBookings(ctx, &bookingpb.ListBookingsRequest{UserId: "bob_list"})
	if err != nil {
		t.Fatalf("ListBookings(bob): %v", err)
	}
	if got := len(b.GetBookingIds()); got != 1 {
		t.Errorf("bob has %d bookings, want 1", got)
	}

	nobody, err := bookingClient.ListBookings(ctx, &bookingpb.ListBookingsRequest{UserId: "nobody"})
	if err != nil {
		t.Fatalf("ListBookings(nobody): %v", err)
	}
	if got := len(nobody.GetBookingIds()); got != 0 {
		t.Errorf("nobody has %d bookings, want 0", got)
	}
}

// ════════════════════════════════════════════════════════════════════════════
// 5. Idempotency: replaying CreateBooking with the same key returns the
//    original booking_id without re-running the saga.
// ════════════════════════════════════════════════════════════════════════════

// Two calls with the same idempotency_key must:
//   - return the same booking_id;
//   - consume only one unit of inventory (so the remaining stock is N-1, not
//     N-2). We verify this by setting stock=2 and confirming a Hold of 1
//     still succeeds after the duplicate booking call.
func TestSaga_Idempotency(t *testing.T) {
	ctx := context.Background()
	setStock(t, "H_idem", "STD", "2026-06-05", 2)

	req := &bookingpb.CreateBookingRequest{
		UserId: "u_idem", HotelId: "H_idem", RoomType: "STD", Date: "2026-06-05",
		Amount: 5000, Currency: "USD", PaymentToken: "tok_test", IdempotencyKey: "k_idem",
	}
	first, err := bookingClient.CreateBooking(ctx, req)
	if err != nil {
		t.Fatalf("CreateBooking #1: %v", err)
	}
	second, err := bookingClient.CreateBooking(ctx, req)
	if err != nil {
		t.Fatalf("CreateBooking #2: %v", err)
	}
	if first.GetBookingId() != second.GetBookingId() {
		t.Errorf("idempotency violated: first=%s second=%s", first.GetBookingId(), second.GetBookingId())
	}

	// Stock=2, one unit committed by the (single, idempotent) booking → 1 left.
	if _, err := inventoryClient.Hold(ctx, &inventorypb.HoldRequest{
		HotelId: "H_idem", RoomType: "STD", Date: "2026-06-05", Quantity: 1,
	}); err != nil {
		t.Errorf("Hold of remaining 1 unit failed (would mean 2 were consumed): %v", err)
	}
}
