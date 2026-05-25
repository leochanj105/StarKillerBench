// Unit tests for inventory, derived from SPEC.yaml.
//
// What this service is:
//   inventory tracks per-(hotel, room_type, date) room stock. Admin sets
//   total via SetStock; bookings Hold to reserve, then Commit (sale done)
//   or Release (cancel before commit). State is in-memory.
//
// Test contract assumed about the implementation:
//   - `server.NewServer()` returns a *Server implementing pb.InventoryServiceServer.
//   - Methods have standard gRPC signatures.
//   - Errors via google.golang.org/grpc/status with the codes in SPEC.error_semantics.

package server_test

import (
	"context"
	"testing"

	pb "agentbench/services/inventory/api/v1"
	"agentbench/services/inventory/internal/server"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// setStockOK calls SetStock and fails the test on error. Used to populate
// inventory before testing Hold/Commit/Release flows.
func setStockOK(t *testing.T, s pb.InventoryServiceServer, hotel, room, date string, qty int32) {
	t.Helper()
	_, err := s.SetStock(context.Background(), &pb.SetStockRequest{
		HotelId: hotel, RoomType: room, Date: date, Quantity: qty,
	})
	if err != nil {
		t.Fatalf("SetStock(%s/%s/%s, %d): %v", hotel, room, date, qty, err)
	}
}

// holdOK calls Hold and returns the hold_id; fails the test on error.
func holdOK(t *testing.T, s pb.InventoryServiceServer, hotel, room, date string, qty int32) string {
	t.Helper()
	r, err := s.Hold(context.Background(), &pb.HoldRequest{
		HotelId: hotel, RoomType: room, Date: date, Quantity: qty,
	})
	if err != nil {
		t.Fatalf("Hold: %v", err)
	}
	return r.GetHoldId()
}

func wantCode(t *testing.T, err error, want codes.Code) {
	t.Helper()
	if got := status.Code(err); got != want {
		t.Errorf("status code = %v, want %v (err=%v)", got, want, err)
	}
}

// ════════════════════════════════════════════════════════════════════════════
// SetStock
// ════════════════════════════════════════════════════════════════════════════

// SetStock with valid inputs succeeds and a subsequent Hold against that
// (hotel, room_type, date) key works.
func TestSetStock_Happy(t *testing.T) {
	s := server.NewServer()
	setStockOK(t, s, "H1", "STD", "2025-01-01", 5)
	if _, err := s.Hold(context.Background(), &pb.HoldRequest{
		HotelId: "H1", RoomType: "STD", Date: "2025-01-01", Quantity: 1,
	}); err != nil {
		t.Errorf("Hold after SetStock: %v", err)
	}
}

// SetStock rejects negative quantity with InvalidArgument.
func TestSetStock_InvalidArgument(t *testing.T) {
	s := server.NewServer()
	_, err := s.SetStock(context.Background(), &pb.SetStockRequest{
		HotelId: "H1", RoomType: "STD", Date: "2025-01-01", Quantity: -1,
	})
	wantCode(t, err, codes.InvalidArgument)
}

// ════════════════════════════════════════════════════════════════════════════
// Hold
// ════════════════════════════════════════════════════════════════════════════

// Hold against existing stock returns a non-empty hold_id.
func TestHold_ReturnsNonEmptyID(t *testing.T) {
	s := server.NewServer()
	setStockOK(t, s, "H1", "STD", "2025-01-01", 5)
	if id := holdOK(t, s, "H1", "STD", "2025-01-01", 1); id == "" {
		t.Errorf("hold_id empty")
	}
}

// Two Holds against the same key get different hold_ids.
func TestHold_DistinctIDs(t *testing.T) {
	s := server.NewServer()
	setStockOK(t, s, "H1", "STD", "2025-01-01", 5)
	a := holdOK(t, s, "H1", "STD", "2025-01-01", 1)
	b := holdOK(t, s, "H1", "STD", "2025-01-01", 1)
	if a == b {
		t.Errorf("two Holds returned the same hold_id: %q", a)
	}
}

// Hold for a (hotel, room_type, date) with no SetStock record returns NotFound.
func TestHold_NotFound(t *testing.T) {
	s := server.NewServer()
	_, err := s.Hold(context.Background(), &pb.HoldRequest{
		HotelId: "H1", RoomType: "STD", Date: "2025-01-01", Quantity: 1,
	})
	wantCode(t, err, codes.NotFound)
}

// Hold exceeding available stock returns FailedPrecondition.
func TestHold_InsufficientStock(t *testing.T) {
	s := server.NewServer()
	setStockOK(t, s, "H1", "STD", "2025-01-01", 2)
	holdOK(t, s, "H1", "STD", "2025-01-01", 2) // exhaust the 2 units
	_, err := s.Hold(context.Background(), &pb.HoldRequest{
		HotelId: "H1", RoomType: "STD", Date: "2025-01-01", Quantity: 1,
	})
	wantCode(t, err, codes.FailedPrecondition)
}

// Hold with non-positive quantity returns InvalidArgument.
func TestHold_InvalidQuantity(t *testing.T) {
	s := server.NewServer()
	setStockOK(t, s, "H1", "STD", "2025-01-01", 5)
	cases := []struct {
		name string
		qty  int32
	}{
		{"zero", 0},
		{"negative", -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.Hold(context.Background(), &pb.HoldRequest{
				HotelId: "H1", RoomType: "STD", Date: "2025-01-01", Quantity: tc.qty,
			})
			wantCode(t, err, codes.InvalidArgument)
		})
	}
}

// ════════════════════════════════════════════════════════════════════════════
// Commit
// ════════════════════════════════════════════════════════════════════════════

// Commit on an active hold succeeds and consumes the hold.
func TestCommit_Happy(t *testing.T) {
	s := server.NewServer()
	setStockOK(t, s, "H1", "STD", "2025-01-01", 5)
	id := holdOK(t, s, "H1", "STD", "2025-01-01", 2)
	if _, err := s.Commit(context.Background(), &pb.CommitRequest{HoldId: id}); err != nil {
		t.Errorf("Commit: %v", err)
	}
}

// After Commit, the held quantity becomes sold — total available stays
// reduced by that quantity, exactly as if the hold were never released.
func TestCommit_ReducesAvailability(t *testing.T) {
	s := server.NewServer()
	setStockOK(t, s, "H1", "STD", "2025-01-01", 2)
	id := holdOK(t, s, "H1", "STD", "2025-01-01", 2)
	if _, err := s.Commit(context.Background(), &pb.CommitRequest{HoldId: id}); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	// available was 0 (all held); after Commit, still 0 (all sold).
	_, err := s.Hold(context.Background(), &pb.HoldRequest{
		HotelId: "H1", RoomType: "STD", Date: "2025-01-01", Quantity: 1,
	})
	wantCode(t, err, codes.FailedPrecondition)
}

// Commit with an unknown hold_id returns NotFound.
func TestCommit_NotFound(t *testing.T) {
	s := server.NewServer()
	_, err := s.Commit(context.Background(), &pb.CommitRequest{HoldId: "unknown"})
	wantCode(t, err, codes.NotFound)
}

// Commit on an already-consumed hold (already committed) returns FailedPrecondition.
func TestCommit_AlreadyConsumed(t *testing.T) {
	s := server.NewServer()
	setStockOK(t, s, "H1", "STD", "2025-01-01", 5)
	id := holdOK(t, s, "H1", "STD", "2025-01-01", 1)
	if _, err := s.Commit(context.Background(), &pb.CommitRequest{HoldId: id}); err != nil {
		t.Fatalf("first Commit: %v", err)
	}
	_, err := s.Commit(context.Background(), &pb.CommitRequest{HoldId: id})
	wantCode(t, err, codes.FailedPrecondition)
}

// ════════════════════════════════════════════════════════════════════════════
// Release
// ════════════════════════════════════════════════════════════════════════════

// Release on an active hold succeeds and returns the units to available.
func TestRelease_Happy(t *testing.T) {
	s := server.NewServer()
	setStockOK(t, s, "H1", "STD", "2025-01-01", 2)
	id := holdOK(t, s, "H1", "STD", "2025-01-01", 2)
	if _, err := s.Release(context.Background(), &pb.ReleaseRequest{HoldId: id}); err != nil {
		t.Fatalf("Release: %v", err)
	}
	// After Release, the 2 units are back in available.
	if _, err := s.Hold(context.Background(), &pb.HoldRequest{
		HotelId: "H1", RoomType: "STD", Date: "2025-01-01", Quantity: 2,
	}); err != nil {
		t.Errorf("re-Hold after Release: %v", err)
	}
}

// Release with unknown hold_id returns NotFound.
func TestRelease_NotFound(t *testing.T) {
	s := server.NewServer()
	_, err := s.Release(context.Background(), &pb.ReleaseRequest{HoldId: "unknown"})
	wantCode(t, err, codes.NotFound)
}

// Release on already-consumed hold (committed before) returns FailedPrecondition.
func TestRelease_AlreadyCommitted(t *testing.T) {
	s := server.NewServer()
	setStockOK(t, s, "H1", "STD", "2025-01-01", 5)
	id := holdOK(t, s, "H1", "STD", "2025-01-01", 1)
	if _, err := s.Commit(context.Background(), &pb.CommitRequest{HoldId: id}); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	_, err := s.Release(context.Background(), &pb.ReleaseRequest{HoldId: id})
	wantCode(t, err, codes.FailedPrecondition)
}
