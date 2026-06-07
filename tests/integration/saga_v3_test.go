// Integration test for inventory v3 + cancellation v2: cancelling a
// confirmed booking returns the room to availability end-to-end.
//
// Exercises the full chain against the live stack:
//   CreateBooking (saga commits the room) → the unit is sold →
//   Cancel (refund + inventory.ReturnStock) → the unit is bookable again.
//
// Kept separate from saga_test.go (v1) and saga_v2_test.go (payment
// chaos) so each version's audit surface is self-contained. Shares the
// TestMain / package-level clients defined in saga_test.go.

package integration_test

import (
	"context"
	"testing"

	bookingpb "agentbench/services/booking/api/v1"
	cancellationpb "agentbench/services/cancellation/api/v1"
	inventorypb "agentbench/services/inventory/api/v1"

	"google.golang.org/grpc/codes"
)

// After a confirmed booking consumes the only unit, Cancel must return it
// so the room can be booked again.
func TestSaga_CancelReturnsInventory(t *testing.T) {
	ctx := context.Background()
	setStock(t, "H_cancel_v3", "STD", "2026-08-10", 1)

	resp, err := bookingClient.CreateBooking(ctx, &bookingpb.CreateBookingRequest{
		UserId: "u_cancel_v3", HotelId: "H_cancel_v3", RoomType: "STD", Date: "2026-08-10",
		Amount: 7000, Currency: "USD", PaymentToken: "tok", IdempotencyKey: "k_cancel_v3",
	})
	if err != nil {
		t.Fatalf("CreateBooking: %v", err)
	}

	// The single unit is now sold: a Hold must fail.
	_, err = inventoryClient.Hold(ctx, &inventorypb.HoldRequest{
		HotelId: "H_cancel_v3", RoomType: "STD", Date: "2026-08-10", Quantity: 1,
	})
	wantCode(t, err, codes.FailedPrecondition)

	// Cancel: refunds and returns the room.
	if _, err := cancellationClient.Cancel(ctx, &cancellationpb.CancelRequest{
		BookingId: resp.GetBookingId(),
	}); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	// The unit is back: a Hold now succeeds.
	if _, err := inventoryClient.Hold(ctx, &inventorypb.HoldRequest{
		HotelId: "H_cancel_v3", RoomType: "STD", Date: "2026-08-10", Quantity: 1,
	}); err != nil {
		t.Errorf("Hold after cancel should succeed (room returned): %v", err)
	}
}
