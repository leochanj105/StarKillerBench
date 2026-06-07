// Unit tests for pricing, derived from SPEC.yaml.
//
// What this service is:
//   pricing quotes a stay. v1 is in-memory and deterministic: SetRatePlan
//   seeds a nightly rate per (hotel, room_type); Quote computes
//     subtotal = nightly_rate * nights
//     taxes    = subtotal / 10
//     fees     = 1500
//     total    = subtotal + taxes + fees
//   nights = number of nights between check_in and check_out (YYYY-MM-DD).
//
// Test contract assumed about the implementation:
//   - `server.NewServer()` returns a *Server implementing pb.PricingServiceServer.
//   - Standard gRPC signatures; errors via google.golang.org/grpc/status with
//     the codes in SPEC.error_semantics.
//
// Authored out-of-band and audited before the agent implements. The agent
// must not modify this file.

package server_test

import (
	"context"
	"testing"

	pb "agentbench/services/pricing/api/v1"
	"agentbench/services/pricing/internal/server"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func setRatePlanOK(t *testing.T, s pb.PricingServiceServer, hotel, room string, rate int64) {
	t.Helper()
	if _, err := s.SetRatePlan(context.Background(), &pb.SetRatePlanRequest{
		HotelId: hotel, RoomType: room, NightlyRate: rate, Currency: "USD",
	}); err != nil {
		t.Fatalf("SetRatePlan(%s/%s): %v", hotel, room, err)
	}
}

func wantCode(t *testing.T, err error, want codes.Code) {
	t.Helper()
	if got := status.Code(err); got != want {
		t.Errorf("status code = %v, want %v (err=%v)", got, want, err)
	}
}

// ════════════════════════════════════════════════════════════════════════════
// Quote
// ════════════════════════════════════════════════════════════════════════════

// Three nights at 10000/night: subtotal 30000, taxes 3000, fees 1500,
// total 34500, nights 3, currency echoed.
func TestQuote_Happy(t *testing.T) {
	s := server.NewServer()
	setRatePlanOK(t, s, "H1", "STD", 10000)

	q, err := s.Quote(context.Background(), &pb.QuoteRequest{
		HotelId: "H1", RoomType: "STD",
		CheckIn: "2026-07-01", CheckOut: "2026-07-04", Guests: 2,
	})
	if err != nil {
		t.Fatalf("Quote: %v", err)
	}
	if q.GetNights() != 3 {
		t.Errorf("nights = %d, want 3", q.GetNights())
	}
	if q.GetNightlyRate() != 10000 {
		t.Errorf("nightly_rate = %d, want 10000", q.GetNightlyRate())
	}
	if q.GetSubtotal() != 30000 {
		t.Errorf("subtotal = %d, want 30000", q.GetSubtotal())
	}
	if q.GetTaxes() != 3000 {
		t.Errorf("taxes = %d, want 3000", q.GetTaxes())
	}
	if q.GetFees() != 1500 {
		t.Errorf("fees = %d, want 1500", q.GetFees())
	}
	if q.GetTotal() != 34500 {
		t.Errorf("total = %d, want 34500", q.GetTotal())
	}
	if q.GetCurrency() != "USD" {
		t.Errorf("currency = %q, want USD", q.GetCurrency())
	}
}

// A single night: subtotal 10000, taxes 1000, fees 1500, total 12500.
func TestQuote_OneNight(t *testing.T) {
	s := server.NewServer()
	setRatePlanOK(t, s, "H1", "STD", 10000)
	q, err := s.Quote(context.Background(), &pb.QuoteRequest{
		HotelId: "H1", RoomType: "STD",
		CheckIn: "2026-07-01", CheckOut: "2026-07-02", Guests: 1,
	})
	if err != nil {
		t.Fatalf("Quote: %v", err)
	}
	if q.GetNights() != 1 || q.GetTotal() != 12500 {
		t.Errorf("nights=%d total=%d, want 1 and 12500", q.GetNights(), q.GetTotal())
	}
}

// No rate plan for the (hotel, room_type) → NotFound.
func TestQuote_NotFound(t *testing.T) {
	s := server.NewServer()
	_, err := s.Quote(context.Background(), &pb.QuoteRequest{
		HotelId: "H1", RoomType: "STD",
		CheckIn: "2026-07-01", CheckOut: "2026-07-02", Guests: 1,
	})
	wantCode(t, err, codes.NotFound)
}

// check_out not strictly after check_in → InvalidArgument (covers equal and
// reversed).
func TestQuote_BadDateRange(t *testing.T) {
	s := server.NewServer()
	setRatePlanOK(t, s, "H1", "STD", 10000)
	cases := []struct{ in, out string }{
		{"2026-07-02", "2026-07-02"}, // equal
		{"2026-07-05", "2026-07-02"}, // reversed
	}
	for _, c := range cases {
		_, err := s.Quote(context.Background(), &pb.QuoteRequest{
			HotelId: "H1", RoomType: "STD", CheckIn: c.in, CheckOut: c.out, Guests: 1,
		})
		wantCode(t, err, codes.InvalidArgument)
	}
}

// Malformed date → InvalidArgument.
func TestQuote_MalformedDate(t *testing.T) {
	s := server.NewServer()
	setRatePlanOK(t, s, "H1", "STD", 10000)
	_, err := s.Quote(context.Background(), &pb.QuoteRequest{
		HotelId: "H1", RoomType: "STD", CheckIn: "not-a-date", CheckOut: "2026-07-02", Guests: 1,
	})
	wantCode(t, err, codes.InvalidArgument)
}

// Non-positive guests → InvalidArgument.
func TestQuote_BadGuests(t *testing.T) {
	s := server.NewServer()
	setRatePlanOK(t, s, "H1", "STD", 10000)
	_, err := s.Quote(context.Background(), &pb.QuoteRequest{
		HotelId: "H1", RoomType: "STD", CheckIn: "2026-07-01", CheckOut: "2026-07-02", Guests: 0,
	})
	wantCode(t, err, codes.InvalidArgument)
}

// ════════════════════════════════════════════════════════════════════════════
// SetRatePlan
// ════════════════════════════════════════════════════════════════════════════

// Re-seeding overwrites the nightly rate.
func TestSetRatePlan_Updates(t *testing.T) {
	s := server.NewServer()
	setRatePlanOK(t, s, "H1", "STD", 10000)
	setRatePlanOK(t, s, "H1", "STD", 20000)
	q, err := s.Quote(context.Background(), &pb.QuoteRequest{
		HotelId: "H1", RoomType: "STD", CheckIn: "2026-07-01", CheckOut: "2026-07-02", Guests: 1,
	})
	if err != nil {
		t.Fatalf("Quote: %v", err)
	}
	if q.GetNightlyRate() != 20000 {
		t.Errorf("nightly_rate = %d, want 20000 (overwrite)", q.GetNightlyRate())
	}
}

// Non-positive rate → InvalidArgument.
func TestSetRatePlan_InvalidRate(t *testing.T) {
	s := server.NewServer()
	for _, r := range []int64{0, -1} {
		_, err := s.SetRatePlan(context.Background(), &pb.SetRatePlanRequest{
			HotelId: "H1", RoomType: "STD", NightlyRate: r, Currency: "USD",
		})
		wantCode(t, err, codes.InvalidArgument)
	}
}

// Empty hotel_id / room_type / currency → InvalidArgument.
func TestSetRatePlan_EmptyFields(t *testing.T) {
	s := server.NewServer()
	cases := []*pb.SetRatePlanRequest{
		{HotelId: "", RoomType: "STD", NightlyRate: 100, Currency: "USD"},
		{HotelId: "H1", RoomType: "", NightlyRate: 100, Currency: "USD"},
		{HotelId: "H1", RoomType: "STD", NightlyRate: 100, Currency: ""},
	}
	for _, req := range cases {
		_, err := s.SetRatePlan(context.Background(), req)
		wantCode(t, err, codes.InvalidArgument)
	}
}
