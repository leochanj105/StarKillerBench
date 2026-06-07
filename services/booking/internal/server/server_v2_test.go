// Unit tests for booking v2: Postgres-backed durability and race-safe
// idempotency.
//
// Audit surface for v2's added contract. These tests need a running
// Postgres reachable via PG_DSN; without it they skip cleanly so that
// `go test ./...` in dev (and in the agent's sandbox, which has no
// docker) still passes. The integration compose stack provides a
// dedicated booking Postgres; `framework/scripts/run_integration` exports
// PG_DSN before exercising these tests.
//
// The upstream payment/inventory services are mocked here (this is a unit
// test of booking's persistence, not the cross-service saga — that's
// covered by tests/integration). The mocks below are defined locally and
// are concurrency-safe, unlike the slice-recording mocks in
// server_test.go, because TestV2_ConcurrentSameKey runs the saga from
// many goroutines at once.
//
// Kept separate from server_test.go (v1, in-memory) so each version's
// contract is reviewed in isolation. The agent must not modify either
// file.

package server_test

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	bookingpb "agentbench/services/booking/api/v1"
	"agentbench/services/booking/internal/server"
	"agentbench/services/booking/internal/store"
	inventorypb "agentbench/services/inventory/api/v1"
	paymentpb "agentbench/services/payment/api/v1"

	"google.golang.org/grpc"
)

// ─── Concurrency-safe upstream mocks (v2-local) ──────────────────────────────
//
// safeInventory counts Hold calls atomically and hands out unique hold
// ids; the counter lets the idempotency test assert the saga ran at most
// once. safePayment always succeeds with unique auth ids. Both satisfy
// the full gRPC client interfaces.

type safeInventory struct {
	holds atomic.Int64
}

func (m *safeInventory) SetStock(ctx context.Context, in *inventorypb.SetStockRequest, opts ...grpc.CallOption) (*inventorypb.SetStockResponse, error) {
	return &inventorypb.SetStockResponse{}, nil
}
func (m *safeInventory) Hold(ctx context.Context, in *inventorypb.HoldRequest, opts ...grpc.CallOption) (*inventorypb.HoldResponse, error) {
	n := m.holds.Add(1)
	return &inventorypb.HoldResponse{HoldId: "hold-" + itoa(n)}, nil
}
func (m *safeInventory) Commit(ctx context.Context, in *inventorypb.CommitRequest, opts ...grpc.CallOption) (*inventorypb.CommitResponse, error) {
	return &inventorypb.CommitResponse{}, nil
}
func (m *safeInventory) Release(ctx context.Context, in *inventorypb.ReleaseRequest, opts ...grpc.CallOption) (*inventorypb.ReleaseResponse, error) {
	return &inventorypb.ReleaseResponse{}, nil
}

type safePayment struct {
	auths atomic.Int64
}

func (m *safePayment) Authorize(ctx context.Context, in *paymentpb.AuthorizeRequest, opts ...grpc.CallOption) (*paymentpb.AuthorizeResponse, error) {
	n := m.auths.Add(1)
	return &paymentpb.AuthorizeResponse{AuthId: "auth-" + itoa(n)}, nil
}
func (m *safePayment) Capture(ctx context.Context, in *paymentpb.CaptureRequest, opts ...grpc.CallOption) (*paymentpb.CaptureResponse, error) {
	return &paymentpb.CaptureResponse{}, nil
}
func (m *safePayment) Void(ctx context.Context, in *paymentpb.VoidRequest, opts ...grpc.CallOption) (*paymentpb.VoidResponse, error) {
	return &paymentpb.VoidResponse{}, nil
}
func (m *safePayment) Refund(ctx context.Context, in *paymentpb.RefundRequest, opts ...grpc.CallOption) (*paymentpb.RefundResponse, error) {
	return &paymentpb.RefundResponse{}, nil
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// pgBookingServers returns two booking Servers backed by the same
// Postgres database (each with its own fresh mock upstreams), plus a
// teardown. The two-server setup lets the durability test prove that a
// booking persisted through s1 is readable through s2 — i.e. the record
// lives in Postgres, not process memory. Skips if PG_DSN is unset.
func pgBookingServers(t *testing.T) (s1, s2 *server.Server, done func()) {
	t.Helper()
	dsn := os.Getenv("PG_DSN")
	if dsn == "" {
		t.Skip("PG_DSN not set; skipping Postgres-backed v2 test")
	}
	st1, err := store.NewPGStore(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect pgStore #1: %v", err)
	}
	st2, err := store.NewPGStore(context.Background(), dsn)
	if err != nil {
		st1.Close()
		t.Fatalf("connect pgStore #2: %v", err)
	}
	s1 = server.NewServerWithStore(&safePayment{}, &safeInventory{}, st1)
	s2 = server.NewServerWithStore(&safePayment{}, &safeInventory{}, st2)
	return s1, s2, func() { st1.Close(); st2.Close() }
}

func createOK(t *testing.T, s *server.Server, user, key string) string {
	t.Helper()
	r, err := s.CreateBooking(context.Background(), &bookingpb.CreateBookingRequest{
		UserId: user, HotelId: "H_v2", RoomType: "STD", Date: "2026-07-01",
		Amount: 5000, Currency: "USD", PaymentToken: "tok", IdempotencyKey: key,
	})
	if err != nil {
		t.Fatalf("CreateBooking(%s,%s): %v", user, key, err)
	}
	return r.GetBookingId()
}

// ════════════════════════════════════════════════════════════════════════════
// Durability across "service restart"
//
// A booking created through s1 must be readable through s2 (a separate
// Server on the same DB). Models a restart: s1's process effectively goes
// away, s2 is a fresh process. If the record were kept in process memory,
// GetBooking on s2 would return NotFound.
// ════════════════════════════════════════════════════════════════════════════
func TestV2_DurableAcrossRestart(t *testing.T) {
	s1, s2, done := pgBookingServers(t)
	defer done()
	ctx := context.Background()

	id := createOK(t, s1, "u_durable", "k_durable_"+t.Name())

	got, err := s2.GetBooking(ctx, &bookingpb.GetBookingRequest{BookingId: id})
	if err != nil {
		t.Fatalf("GetBooking on second server: %v", err)
	}
	if got.GetUserId() != "u_durable" || got.GetStatus() != "confirmed" || got.GetAmount() != 5000 {
		t.Errorf("restored booking mismatched: %+v", got)
	}
}

// ════════════════════════════════════════════════════════════════════════════
// Durable idempotency across "restart"
//
// Replaying the same idempotency_key through a different Server must
// return the original booking_id without running the saga again. Pins
// that the idempotency index lives in Postgres, not memory.
// ════════════════════════════════════════════════════════════════════════════
func TestV2_IdempotentAcrossRestart(t *testing.T) {
	s1, s2, done := pgBookingServers(t)
	defer done()
	ctx := context.Background()

	key := "k_idem_" + t.Name()
	first := createOK(t, s1, "u_idem", key)

	r, err := s2.CreateBooking(ctx, &bookingpb.CreateBookingRequest{
		UserId: "u_idem", HotelId: "H_v2", RoomType: "STD", Date: "2026-07-01",
		Amount: 5000, Currency: "USD", PaymentToken: "tok", IdempotencyKey: key,
	})
	if err != nil {
		t.Fatalf("replayed CreateBooking on second server: %v", err)
	}
	if r.GetBookingId() != first {
		t.Errorf("idempotent replay returned %q, want original %q", r.GetBookingId(), first)
	}
}

// ════════════════════════════════════════════════════════════════════════════
// Concurrent CreateBooking with the same idempotency_key
//
// N goroutines fire CreateBooking with one shared key. The saga must run
// at most once (verified via the atomic Hold counter on the shared mock
// inventory) and every caller must receive the SAME booking_id. Without a
// transactional unique-constraint on idempotency_key, two callers would
// both run the saga (double Hold / double charge) and persist divergent
// bookings.
// ════════════════════════════════════════════════════════════════════════════
func TestV2_ConcurrentSameKey_SagaRunsOnce(t *testing.T) {
	dsn := os.Getenv("PG_DSN")
	if dsn == "" {
		t.Skip("PG_DSN not set; skipping Postgres-backed v2 test")
	}
	st, err := store.NewPGStore(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect pgStore: %v", err)
	}
	defer st.Close()

	inv := &safeInventory{}
	pay := &safePayment{}
	s := server.NewServerWithStore(pay, inv, st)

	const racers = 10
	key := "k_race_" + t.Name()
	req := &bookingpb.CreateBookingRequest{
		UserId: "u_race", HotelId: "H_v2", RoomType: "STD", Date: "2026-07-01",
		Amount: 5000, Currency: "USD", PaymentToken: "tok", IdempotencyKey: key,
	}

	var wg sync.WaitGroup
	ids := make([]string, racers)
	errs := make([]error, racers)
	wg.Add(racers)
	for i := 0; i < racers; i++ {
		go func(i int) {
			defer wg.Done()
			r, err := s.CreateBooking(context.Background(), req)
			if err != nil {
				errs[i] = err
				return
			}
			ids[i] = r.GetBookingId()
		}(i)
	}
	wg.Wait()

	// Every call must have succeeded (a loser on the idempotency race
	// returns the winner's id, it does not error).
	for i, err := range errs {
		if err != nil {
			t.Errorf("racer %d errored: %v", i, err)
		}
	}
	// All booking_ids must be identical and non-empty.
	for i := 1; i < racers; i++ {
		if ids[i] != ids[0] {
			t.Errorf("racer %d got booking_id %q, want %q (divergent bookings)", i, ids[i], ids[0])
		}
	}
	if ids[0] == "" {
		t.Fatalf("no booking_id produced")
	}
	// The saga must have run at most once: exactly one inventory Hold.
	if got := inv.holds.Load(); got != 1 {
		t.Errorf("inventory.Hold called %d times, want exactly 1 (saga ran more than once)", got)
	}
}

// Compile-time guard: the v2-local mocks satisfy the gRPC client
// interfaces the booking server depends on. Mirrors how the constructor
// is wired in production.
var (
	_ inventorypb.InventoryServiceClient = (*safeInventory)(nil)
	_ paymentpb.PaymentServiceClient     = (*safePayment)(nil)
)
