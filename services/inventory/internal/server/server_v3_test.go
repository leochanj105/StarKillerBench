// Unit tests for inventory v3: ReturnStock — the post-commit undo that
// returns a sold unit to availability when a confirmed booking is
// cancelled.
//
// These run against the in-memory backend (server.NewServer()), so they
// need no Postgres — same as the v1 tests. The ReturnStock contract is
// backend-agnostic; the durable backend must present identical behavior.
//
// Kept separate from server_test.go (v1) and server_v2_test.go (Postgres)
// so each version's contract is reviewed in isolation. The agent must not
// modify any of these files.
//
// SPEC.yaml `interface.ReturnStock` and its `error_semantics` are the
// contract pinned here.

package server_test

import (
	"context"
	"testing"

	pb "agentbench/services/inventory/api/v1"
	"agentbench/services/inventory/internal/server"

	"google.golang.org/grpc/codes"
)

// commitOne sets up `total` stock, holds `qty`, and commits it, leaving
// sold=qty. Returns nothing — callers assert via subsequent Hold/ReturnStock.
func commitOne(t *testing.T, s pb.InventoryServiceServer, hotel, room, date string, total, qty int32) {
	t.Helper()
	setStockOK(t, s, hotel, room, date, total)
	id := holdOK(t, s, hotel, room, date, qty)
	if _, err := s.Commit(context.Background(), &pb.CommitRequest{HoldId: id}); err != nil {
		t.Fatalf("Commit: %v", err)
	}
}

// ════════════════════════════════════════════════════════════════════════════
// ReturnStock happy path
//
// After a Commit, sold is reduced and the units become available again:
// set total=2, sell 2 (available 0), ReturnStock 1 → available 1, so a
// Hold of 1 now succeeds and a Hold of 2 does not.
// ════════════════════════════════════════════════════════════════════════════
func TestReturnStock_RestoresAvailability(t *testing.T) {
	s := server.NewServer()
	ctx := context.Background()
	commitOne(t, s, "H1", "STD", "2026-08-01", 2, 2) // sold=2, available=0

	if _, err := s.ReturnStock(ctx, &pb.ReturnStockRequest{
		HotelId: "H1", RoomType: "STD", Date: "2026-08-01", Quantity: 1,
	}); err != nil {
		t.Fatalf("ReturnStock: %v", err)
	}

	// One unit back: Hold(1) works.
	if _, err := s.Hold(ctx, &pb.HoldRequest{
		HotelId: "H1", RoomType: "STD", Date: "2026-08-01", Quantity: 1,
	}); err != nil {
		t.Errorf("Hold(1) after ReturnStock should succeed: %v", err)
	}
	// But only one came back: Hold of another 1 (total available now 0) fails.
	_, err := s.Hold(ctx, &pb.HoldRequest{
		HotelId: "H1", RoomType: "STD", Date: "2026-08-01", Quantity: 1,
	})
	wantCode(t, err, codes.FailedPrecondition)
}

// ReturnStock with non-positive quantity is InvalidArgument.
func TestReturnStock_InvalidQuantity(t *testing.T) {
	s := server.NewServer()
	commitOne(t, s, "H1", "STD", "2026-08-01", 2, 1)
	for _, q := range []int32{0, -1} {
		_, err := s.ReturnStock(context.Background(), &pb.ReturnStockRequest{
			HotelId: "H1", RoomType: "STD", Date: "2026-08-01", Quantity: q,
		})
		wantCode(t, err, codes.InvalidArgument)
	}
}

// ReturnStock for a (hotel, room, date) with no stock record is NotFound.
func TestReturnStock_NotFound(t *testing.T) {
	s := server.NewServer()
	_, err := s.ReturnStock(context.Background(), &pb.ReturnStockRequest{
		HotelId: "nope", RoomType: "STD", Date: "2026-08-01", Quantity: 1,
	})
	wantCode(t, err, codes.NotFound)
}

// ReturnStock for more than is currently sold is FailedPrecondition — you
// can't un-sell units that were never sold. Here sold=1 but we try to
// return 2.
func TestReturnStock_ExceedsSold(t *testing.T) {
	s := server.NewServer()
	commitOne(t, s, "H1", "STD", "2026-08-01", 3, 1) // sold=1
	_, err := s.ReturnStock(context.Background(), &pb.ReturnStockRequest{
		HotelId: "H1", RoomType: "STD", Date: "2026-08-01", Quantity: 2,
	})
	wantCode(t, err, codes.FailedPrecondition)
}
