// Integration tests for payment v2 chaos behavior, observed end-to-end
// through the booking saga and cancellation flow.
//
// These tests exercise compensation paths that the v1 saga code already
// contained but never ran under real failure conditions. They depend on
// payment v2's reserved chaos tokens (tok_decline, tok_capture_fail,
// tok_refund_fail); see services/payment/SPEC.yaml `chaos`.
//
// Kept separate from saga_test.go (v1) so each version's audit surface is
// self-contained. Shares the TestMain / package-level clients defined in
// saga_test.go.

package integration_test

import (
	"context"
	"testing"

	bookingpb "agentbench/services/booking/api/v1"
	cancellationpb "agentbench/services/cancellation/api/v1"
	inventorypb "agentbench/services/inventory/api/v1"

	"google.golang.org/grpc/codes"
)

// ════════════════════════════════════════════════════════════════════════════
// Saga compensation when payment.Authorize is declined.
//
// Uses the v2 chaos token tok_decline. The booking saga must:
//   1. Hold inventory successfully
//   2. Receive FailedPrecondition from payment.Authorize
//   3. Compensate by Releasing the inventory hold
//   4. Surface FailedPrecondition to the caller
// We verify the compensation by re-Holding the full stock afterward; if
// the saga forgot to Release, this would fail.
// ════════════════════════════════════════════════════════════════════════════
func TestSaga_PaymentDeclined(t *testing.T) {
	ctx := context.Background()
	setStock(t, "H_decline", "STD", "2026-06-06", 1)

	_, err := bookingClient.CreateBooking(ctx, &bookingpb.CreateBookingRequest{
		UserId: "u_decline", HotelId: "H_decline", RoomType: "STD", Date: "2026-06-06",
		Amount: 5000, Currency: "USD", PaymentToken: "tok_decline", IdempotencyKey: "k_decline",
	})
	wantCode(t, err, codes.FailedPrecondition)

	// Inventory must be back to full availability.
	if _, err := inventoryClient.Hold(ctx, &inventorypb.HoldRequest{
		HotelId: "H_decline", RoomType: "STD", Date: "2026-06-06", Quantity: 1,
	}); err != nil {
		t.Errorf("Hold after declined payment should succeed (1 unit free again): %v", err)
	}
}

// ════════════════════════════════════════════════════════════════════════════
// Saga compensation when payment.Capture fails mid-saga.
//
// Uses tok_capture_fail. The saga must:
//   1. Hold inventory
//   2. Authorize succeeds
//   3. Capture fails with FailedPrecondition
//   4. Compensate by Void-ing the auth AND Releasing inventory
//   5. Surface FailedPrecondition
// Inventory release is verified the same way as TestSaga_PaymentDeclined.
// ════════════════════════════════════════════════════════════════════════════
func TestSaga_CaptureFailed(t *testing.T) {
	ctx := context.Background()
	setStock(t, "H_capfail", "STD", "2026-06-07", 1)

	_, err := bookingClient.CreateBooking(ctx, &bookingpb.CreateBookingRequest{
		UserId: "u_capfail", HotelId: "H_capfail", RoomType: "STD", Date: "2026-06-07",
		Amount: 5000, Currency: "USD", PaymentToken: "tok_capture_fail", IdempotencyKey: "k_capfail",
	})
	wantCode(t, err, codes.FailedPrecondition)

	if _, err := inventoryClient.Hold(ctx, &inventorypb.HoldRequest{
		HotelId: "H_capfail", RoomType: "STD", Date: "2026-06-07", Quantity: 1,
	}); err != nil {
		t.Errorf("Hold after Capture-failed compensation should succeed: %v", err)
	}
}

// ════════════════════════════════════════════════════════════════════════════
// Refund failure surfaces through cancellation.
//
// Uses tok_refund_fail. The booking saga itself completes (Authorize +
// Capture succeed for this token), so the booking persists normally.
// Cancellation, which calls payment.Refund, must surface the chaos
// failure as FailedPrecondition to its caller.
// ════════════════════════════════════════════════════════════════════════════
func TestSaga_RefundFailed(t *testing.T) {
	ctx := context.Background()
	setStock(t, "H_reffail", "STD", "2026-06-08", 1)

	resp, err := bookingClient.CreateBooking(ctx, &bookingpb.CreateBookingRequest{
		UserId: "u_reffail", HotelId: "H_reffail", RoomType: "STD", Date: "2026-06-08",
		Amount: 5000, Currency: "USD", PaymentToken: "tok_refund_fail", IdempotencyKey: "k_reffail",
	})
	if err != nil {
		t.Fatalf("CreateBooking(tok_refund_fail) should succeed at booking phase: %v", err)
	}

	_, err = cancellationClient.Cancel(ctx, &cancellationpb.CancelRequest{BookingId: resp.GetBookingId()})
	wantCode(t, err, codes.FailedPrecondition)
}
