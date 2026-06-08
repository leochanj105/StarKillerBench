// Unit tests for ads, derived from SPEC.yaml.
//
// What this service is:
//   ads runs a second-price auction over advertiser campaigns. SetCampaign
//   seeds a (bid, daily_budget) for a hotel; SelectSponsored fills slots with
//   the highest bidders, each charged the next-lower bid (or its own bid as
//   the lowest/only bidder), deducting from budget. v1 is in-memory.
//
// Test contract assumed about the implementation:
//   - `server.NewServer()` returns a *Server implementing pb.AdsServiceServer.
//   - Standard gRPC signatures; errors via google.golang.org/grpc/status with
//     the codes in SPEC.error_semantics.
//
// Bids are kept distinct in these tests so the ranking is unambiguous.
//
// Authored out-of-band and audited before the agent implements. The agent
// must not modify this file.

package server_test

import (
	"context"
	"testing"

	pb "agentbench/services/ads/api/v1"
	"agentbench/services/ads/internal/server"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func setCampaignOK(t *testing.T, s pb.AdsServiceServer, adv, hotel string, budget, bid int64) {
	t.Helper()
	r, err := s.SetCampaign(context.Background(), &pb.SetCampaignRequest{
		AdvertiserId: adv, HotelId: hotel, DailyBudget: budget, Bid: bid,
	})
	if err != nil {
		t.Fatalf("SetCampaign(%s): %v", hotel, err)
	}
	if r.GetCampaignId() == "" {
		t.Fatalf("SetCampaign returned empty campaign_id")
	}
}

func selectSponsored(t *testing.T, s pb.AdsServiceServer, slots int32) []*pb.SponsoredSlot {
	t.Helper()
	r, err := s.SelectSponsored(context.Background(), &pb.SelectSponsoredRequest{SlotCount: slots})
	if err != nil {
		t.Fatalf("SelectSponsored(%d): %v", slots, err)
	}
	return r.GetSlots()
}

func wantCode(t *testing.T, err error, want codes.Code) {
	t.Helper()
	if got := status.Code(err); got != want {
		t.Errorf("status code = %v, want %v (err=%v)", got, want, err)
	}
}

// ════════════════════════════════════════════════════════════════════════════
// Second-price auction
// ════════════════════════════════════════════════════════════════════════════

// Three bidders A(100) B(80) C(60); two slots. Winners are A and B, ordered
// by bid descending. A pays B's bid (80); B pays C's bid (60) — the next-lower
// bid sets the price even though C wins no slot.
func TestSelectSponsored_SecondPrice(t *testing.T) {
	s := server.NewServer()
	setCampaignOK(t, s, "adv", "HA", 100000, 100)
	setCampaignOK(t, s, "adv", "HB", 100000, 80)
	setCampaignOK(t, s, "adv", "HC", 100000, 60)

	slots := selectSponsored(t, s, 2)
	if len(slots) != 2 {
		t.Fatalf("slots = %d, want 2", len(slots))
	}
	if slots[0].GetHotelId() != "HA" || slots[0].GetPrice() != 80 {
		t.Errorf("slot0 = (%s, %d), want (HA, 80)", slots[0].GetHotelId(), slots[0].GetPrice())
	}
	if slots[1].GetHotelId() != "HB" || slots[1].GetPrice() != 60 {
		t.Errorf("slot1 = (%s, %d), want (HB, 60)", slots[1].GetHotelId(), slots[1].GetPrice())
	}
}

// slot_count limits the number of winners: with one slot, only the top bidder
// is returned (priced at the next-lower bid).
func TestSelectSponsored_SlotCountLimits(t *testing.T) {
	s := server.NewServer()
	setCampaignOK(t, s, "adv", "HA", 100000, 100)
	setCampaignOK(t, s, "adv", "HB", 100000, 80)
	setCampaignOK(t, s, "adv", "HC", 100000, 60)

	slots := selectSponsored(t, s, 1)
	if len(slots) != 1 || slots[0].GetHotelId() != "HA" || slots[0].GetPrice() != 80 {
		t.Errorf("slots = %+v, want one (HA, 80)", slots)
	}
}

// A lone bidder pays its own bid (first-price fallback), and the budget is
// spent down: A bids 100 with budget 150. First select charges 100 (budget
// → 50); the second select finds A can't afford 100 and returns nothing.
func TestSelectSponsored_BudgetExhaustion(t *testing.T) {
	s := server.NewServer()
	setCampaignOK(t, s, "adv", "HA", 150, 100)

	first := selectSponsored(t, s, 1)
	if len(first) != 1 || first[0].GetPrice() != 100 {
		t.Fatalf("first select = %+v, want one priced 100 (first-price fallback)", first)
	}
	second := selectSponsored(t, s, 1)
	if len(second) != 0 {
		t.Errorf("second select = %+v, want empty (budget exhausted)", second)
	}
}

// No campaigns → empty slots, not an error.
func TestSelectSponsored_NoneEligible(t *testing.T) {
	s := server.NewServer()
	if slots := selectSponsored(t, s, 3); len(slots) != 0 {
		t.Errorf("slots = %+v, want empty", slots)
	}
}

// slot_count must be positive.
func TestSelectSponsored_InvalidSlotCount(t *testing.T) {
	s := server.NewServer()
	_, err := s.SelectSponsored(context.Background(), &pb.SelectSponsoredRequest{SlotCount: 0})
	wantCode(t, err, codes.InvalidArgument)
}

// ════════════════════════════════════════════════════════════════════════════
// SetCampaign / logging
// ════════════════════════════════════════════════════════════════════════════

// SetCampaign rejects empty ids and non-positive budget/bid.
func TestSetCampaign_Invalid(t *testing.T) {
	s := server.NewServer()
	cases := []*pb.SetCampaignRequest{
		{AdvertiserId: "", HotelId: "H", DailyBudget: 100, Bid: 10},
		{AdvertiserId: "a", HotelId: "", DailyBudget: 100, Bid: 10},
		{AdvertiserId: "a", HotelId: "H", DailyBudget: 0, Bid: 10},
		{AdvertiserId: "a", HotelId: "H", DailyBudget: 100, Bid: 0},
	}
	for _, req := range cases {
		_, err := s.SetCampaign(context.Background(), req)
		wantCode(t, err, codes.InvalidArgument)
	}
}

// LogImpression / LogClick acknowledge valid calls and reject an empty
// hotel_id.
func TestLogging(t *testing.T) {
	s := server.NewServer()
	ctx := context.Background()
	if _, err := s.LogImpression(ctx, &pb.LogImpressionRequest{HotelId: "HA", QueryId: "q1"}); err != nil {
		t.Errorf("LogImpression: %v", err)
	}
	if _, err := s.LogClick(ctx, &pb.LogClickRequest{HotelId: "HA", QueryId: "q1"}); err != nil {
		t.Errorf("LogClick: %v", err)
	}
	_, err := s.LogImpression(ctx, &pb.LogImpressionRequest{HotelId: "", QueryId: "q1"})
	wantCode(t, err, codes.InvalidArgument)
	_, err = s.LogClick(ctx, &pb.LogClickRequest{HotelId: "", QueryId: "q1"})
	wantCode(t, err, codes.InvalidArgument)
}
