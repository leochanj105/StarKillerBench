// Unit tests for cancellation v2: returning the room to inventory on
// cancel (closing the v1 gap where money was refunded but the room stayed
// sold).
//
// v2 adds inventory as a third upstream dependency. The constructor pinned
// here is NewServerWithInventory(booking, payment, inventory); the v1
// constructor NewServer(booking, payment) stays valid (no inventory
// interaction) so the frozen v1 test file keeps passing.
//
// Reuses mockBooking and mockPayment from server_test.go (same package);
// adds a mockInventory recording ReturnStock calls. The agent must not
// modify either test file.
//
// SPEC.yaml `interface.Cancel` and its `error_semantics` are the contract
// pinned here.

package server_test

import (
	"context"
	"testing"

	pb "agentbench/services/cancellation/api/v1"
	"agentbench/services/cancellation/internal/server"
	inventorypb "agentbench/services/inventory/api/v1"
	paymentpb "agentbench/services/payment/api/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// mockInventory implements inventorypb.InventoryServiceClient. Only
// ReturnStock is exercised by cancellation; the rest return Unimplemented
// so an accidental call is loud.
type mockInventory struct {
	ReturnStockFn func(ctx context.Context, in *inventorypb.ReturnStockRequest, opts ...grpc.CallOption) (*inventorypb.ReturnStockResponse, error)

	ReturnStockCalls []*inventorypb.ReturnStockRequest
}

func (m *mockInventory) SetStock(ctx context.Context, in *inventorypb.SetStockRequest, opts ...grpc.CallOption) (*inventorypb.SetStockResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not mocked")
}
func (m *mockInventory) Hold(ctx context.Context, in *inventorypb.HoldRequest, opts ...grpc.CallOption) (*inventorypb.HoldResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not mocked")
}
func (m *mockInventory) Commit(ctx context.Context, in *inventorypb.CommitRequest, opts ...grpc.CallOption) (*inventorypb.CommitResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not mocked")
}
func (m *mockInventory) Release(ctx context.Context, in *inventorypb.ReleaseRequest, opts ...grpc.CallOption) (*inventorypb.ReleaseResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not mocked")
}
func (m *mockInventory) ReturnStock(ctx context.Context, in *inventorypb.ReturnStockRequest, opts ...grpc.CallOption) (*inventorypb.ReturnStockResponse, error) {
	m.ReturnStockCalls = append(m.ReturnStockCalls, in)
	if m.ReturnStockFn != nil {
		return m.ReturnStockFn(ctx, in, opts...)
	}
	return &inventorypb.ReturnStockResponse{}, nil
}

var _ inventorypb.InventoryServiceClient = (*mockInventory)(nil)

// ════════════════════════════════════════════════════════════════════════════
// Happy path: refund AND return the room.
//
// The default mockBooking returns hotel "h-1", room "STD", date
// "2025-01-01", auth "auth-1", amount 100. Cancel must refund with the
// auth/amount and call ReturnStock with the room key and quantity 1.
// ════════════════════════════════════════════════════════════════════════════
func TestCancel_v2_ReturnsRoomAndRefunds(t *testing.T) {
	book, pay, inv := &mockBooking{}, &mockPayment{}, &mockInventory{}
	s := server.NewServerWithInventory(book, pay, inv)

	if _, err := s.Cancel(context.Background(), &pb.CancelRequest{BookingId: "b-1"}); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	if len(pay.RefundCalls) != 1 {
		t.Fatalf("Refund calls = %d, want 1", len(pay.RefundCalls))
	}
	if len(inv.ReturnStockCalls) != 1 {
		t.Fatalf("ReturnStock calls = %d, want 1", len(inv.ReturnStockCalls))
	}
	rs := inv.ReturnStockCalls[0]
	if rs.GetHotelId() != "h-1" || rs.GetRoomType() != "STD" || rs.GetDate() != "2025-01-01" {
		t.Errorf("ReturnStock room key wrong: %+v", rs)
	}
	if rs.GetQuantity() != 1 {
		t.Errorf("ReturnStock quantity = %d, want 1", rs.GetQuantity())
	}
}

// ════════════════════════════════════════════════════════════════════════════
// Refund runs before ReturnStock, and a refund failure short-circuits:
// inventory must NOT be touched if the money wasn't returned.
// ════════════════════════════════════════════════════════════════════════════
func TestCancel_v2_RefundFails_NoInventoryReturn(t *testing.T) {
	book := &mockBooking{}
	pay := &mockPayment{RefundFn: func(_ context.Context, _ *paymentpb.RefundRequest, _ ...grpc.CallOption) (*paymentpb.RefundResponse, error) {
		return nil, status.Error(codes.Internal, "PSP outage")
	}}
	inv := &mockInventory{}
	s := server.NewServerWithInventory(book, pay, inv)

	_, err := s.Cancel(context.Background(), &pb.CancelRequest{BookingId: "b-1"})
	wantCode(t, err, codes.FailedPrecondition)
	if len(inv.ReturnStockCalls) != 0 {
		t.Errorf("ReturnStock called despite refund failure (%d calls)", len(inv.ReturnStockCalls))
	}
}

// ════════════════════════════════════════════════════════════════════════════
// ReturnStock failure surfaces as FailedPrecondition (after the refund has
// already happened — no compensation, per SPEC).
// ════════════════════════════════════════════════════════════════════════════
func TestCancel_v2_ReturnStockFails(t *testing.T) {
	book, pay := &mockBooking{}, &mockPayment{}
	inv := &mockInventory{ReturnStockFn: func(_ context.Context, _ *inventorypb.ReturnStockRequest, _ ...grpc.CallOption) (*inventorypb.ReturnStockResponse, error) {
		return nil, status.Error(codes.FailedPrecondition, "nothing sold to return")
	}}
	s := server.NewServerWithInventory(book, pay, inv)

	_, err := s.Cancel(context.Background(), &pb.CancelRequest{BookingId: "b-1"})
	wantCode(t, err, codes.FailedPrecondition)
	// The refund did happen (no compensation): one Refund call recorded.
	if len(pay.RefundCalls) != 1 {
		t.Errorf("Refund calls = %d, want 1 (refund precedes the failing return)", len(pay.RefundCalls))
	}
}
