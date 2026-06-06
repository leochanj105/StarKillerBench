// Unit tests for inventory v2: Postgres-backed durability and concurrency.
//
// Audit surface for v2's added contract. These tests need a running
// Postgres reachable via PG_DSN; without it, they skip cleanly so that
// `go test ./...` in dev (or in the agent's sandbox, which has no docker)
// still passes. The integration compose stack provides Postgres; running
// `framework/scripts/run_integration` exports PG_DSN before exercising
// these tests.
//
// Kept separate from server_test.go (v1, in-memory) so each version's
// contract is reviewed in isolation. The agent must not modify either
// file.
//
// SPEC.yaml `state` and `dependencies` describe the schema and how the
// backend is wired up.

package server_test

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pb "agentbench/services/inventory/api/v1"
	"agentbench/services/inventory/internal/server"
	"agentbench/services/inventory/internal/store"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// pgServers returns two Server instances both pointing at the same
// Postgres database, plus a teardown function. The two-server setup lets
// tests prove durability: state written through s1 must be observable
// through s2, even after s1's reference is dropped. Tests that don't
// need two are free to use only s1.
//
// Skips the test if PG_DSN is unset (dev loop, agent sandbox) so the
// whole file is no-op without infra. Tests use unique keys so they
// don't depend on table truncation between runs; if you want a clean
// slate, truncate manually outside the test.
func pgServers(t *testing.T) (s1, s2 *server.Server, done func()) {
	t.Helper()
	dsn := os.Getenv("PG_DSN")
	if dsn == "" {
		t.Skip("PG_DSN not set; skipping Postgres-backed v2 test")
	}

	pg1, err := store.NewPGStore(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect pgStore #1: %v", err)
	}
	pg2, err := store.NewPGStore(context.Background(), dsn)
	if err != nil {
		pg1.Close()
		t.Fatalf("connect pgStore #2: %v", err)
	}
	return server.NewServerWithStore(pg1), server.NewServerWithStore(pg2), func() {
		pg1.Close()
		pg2.Close()
	}
}

// uniqKey returns a (hotel, room, date) triple unique to this test +
// invocation so concurrent test runs and re-runs don't collide on rows
// left behind in the Postgres tables.
func uniqKey(t *testing.T) (hotel, room, date string) {
	t.Helper()
	suffix := time.Now().UnixNano()
	return t.Name(), "STD", time.Unix(0, suffix).UTC().Format("2006-01-02")
}

// ════════════════════════════════════════════════════════════════════════════
// Durability across "service restart"
//
// State written through one Server must be visible through a second
// Server pointing at the same database. Models a service restart: the
// first server's process effectively goes away (we just stop using it),
// the second server is a fresh process. If the implementation kept any
// state in process memory rather than Postgres, this test fails.
// ════════════════════════════════════════════════════════════════════════════
func TestV2_DurableAcrossRestart(t *testing.T) {
	s1, s2, done := pgServers(t)
	defer done()
	ctx := context.Background()
	hotel, room, date := uniqKey(t)

	// Through s1: set stock and create a hold.
	if _, err := s1.SetStock(ctx, &pb.SetStockRequest{
		HotelId: hotel, RoomType: room, Date: date, Quantity: 3,
	}); err != nil {
		t.Fatalf("SetStock: %v", err)
	}
	h, err := s1.Hold(ctx, &pb.HoldRequest{
		HotelId: hotel, RoomType: room, Date: date, Quantity: 2,
	})
	if err != nil {
		t.Fatalf("Hold: %v", err)
	}

	// Through s2 (separate Server, same DB): Commit must succeed and the
	// remaining 1 unit of stock must still be Hold-able.
	if _, err := s2.Commit(ctx, &pb.CommitRequest{HoldId: h.GetHoldId()}); err != nil {
		t.Fatalf("Commit on restored hold: %v", err)
	}
	if _, err := s2.Hold(ctx, &pb.HoldRequest{
		HotelId: hotel, RoomType: room, Date: date, Quantity: 1,
	}); err != nil {
		t.Errorf("Hold of remaining unit after restart: %v", err)
	}
}

// ════════════════════════════════════════════════════════════════════════════
// Concurrent Hold against the last unit must not oversell
//
// Spins up N goroutines all trying to Hold the same final unit. Exactly
// one must succeed; the others must fail with FailedPrecondition (per
// SPEC's existing "insufficient stock" rule). If the backend forgets to
// take a row lock, more than one wins → oversell, and the test fails.
//
// This is the property that distinguishes a real durable store from the
// in-memory mutex-based memStore: with proper SELECT ... FOR UPDATE the
// transactions serialize cleanly; without it, race conditions slip
// through.
// ════════════════════════════════════════════════════════════════════════════
func TestV2_ConcurrentHold_NoOversell(t *testing.T) {
	s1, _, done := pgServers(t)
	defer done()
	ctx := context.Background()
	hotel, room, date := uniqKey(t)

	if _, err := s1.SetStock(ctx, &pb.SetStockRequest{
		HotelId: hotel, RoomType: room, Date: date, Quantity: 1,
	}); err != nil {
		t.Fatalf("SetStock: %v", err)
	}

	const racers = 10
	var wg sync.WaitGroup
	var successes atomic.Int32
	var failedPrecondition atomic.Int32
	var otherErr atomic.Int32

	wg.Add(racers)
	for i := 0; i < racers; i++ {
		go func() {
			defer wg.Done()
			_, err := s1.Hold(ctx, &pb.HoldRequest{
				HotelId: hotel, RoomType: room, Date: date, Quantity: 1,
			})
			switch {
			case err == nil:
				successes.Add(1)
			case grpcCode(err) == codes.FailedPrecondition:
				failedPrecondition.Add(1)
			default:
				otherErr.Add(1)
			}
		}()
	}
	wg.Wait()

	if got := successes.Load(); got != 1 {
		t.Errorf("expected exactly 1 successful Hold, got %d (oversell?)", got)
	}
	if got := failedPrecondition.Load(); got != racers-1 {
		t.Errorf("expected %d FailedPrecondition losers, got %d", racers-1, got)
	}
	if got := otherErr.Load(); got != 0 {
		t.Errorf("unexpected non-FailedPrecondition errors: %d", got)
	}
}

// grpcCode is a small helper used by the concurrency test to
// classify a returned error. status.Code() already returns codes.OK
// for nil, so we pass through to it.
func grpcCode(err error) codes.Code { return status.Code(err) }
