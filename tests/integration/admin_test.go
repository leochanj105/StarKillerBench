// Admin → search integration: a hotelier lists a hotel through the admin
// extranet, and a guest then finds it through search. End-to-end against the
// live container stack.
//
// This exercises admin's fan-out (UpsertHotel → geo + profile;
// SetRatePlan → pricing) and confirms the read path consumes those writes —
// the hotelier-to-guest loop in one test.
//
// Shares the integration_test package; reuses mustDial/envOr from
// saga_test.go and the search client setup from readpath_test.go.

package integration_test

import (
	"context"
	"sync"
	"testing"
	"time"

	adminpb "agentbench/services/admin/api/v1"
	searchpb "agentbench/services/search/api/v1"
)

var (
	adminOnce   sync.Once
	adminClient adminpb.AdminServiceClient
)

func setupAdmin() {
	adminOnce.Do(func() {
		adminClient = adminpb.NewAdminServiceClient(mustDial(envOr("ADMIN_ADDR", "localhost:18112")))
	})
}

// A hotelier sets up a hotel via admin (coordinates + profile + rate plan),
// then a guest search near that location returns the hotel with its price —
// proving admin's writes reached geo, profile, and pricing, and that search
// reads them back.
func TestAdminToSearch_HotelierListsHotel(t *testing.T) {
	setupAdmin()
	setupReadPath() // searchClient
	ctx := context.Background()

	const hotel = "ADM_H1"
	lat, lng := 30.0, 30.0

	// 1. Hotelier registers the hotel (→ geo coords + profile metadata).
	if _, err := adminClient.UpsertHotel(ctx, &adminpb.UpsertHotelRequest{
		HotelId: hotel, Name: "Admin Plaza", Address: "1 Admin Way",
		Lat: lat, Lng: lng, RoomTypes: []string{"STD"},
	}); err != nil {
		t.Fatalf("admin.UpsertHotel: %v", err)
	}

	// 2. Hotelier sets the nightly rate (→ pricing).
	if _, err := adminClient.SetRatePlan(ctx, &adminpb.SetRatePlanRequest{
		HotelId: hotel, RoomType: "STD", NightlyRate: 12000, Currency: "USD",
	}); err != nil {
		t.Fatalf("admin.SetRatePlan: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	// 3. Guest searches near the hotel's coordinates.
	resp, err := searchClient.Search(ctx, &searchpb.SearchRequest{
		Lat: lat, Lng: lng, RadiusKm: 25,
		CheckIn: "2026-10-01", CheckOut: "2026-10-04", Guests: 2, // 3 nights
	})
	if err != nil {
		t.Fatalf("search.Search: %v", err)
	}

	// The hotel must appear, priced from the rate admin set:
	// 3 nights × 12000 = 36000 + 10% tax (3600) + 1500 fee = 41100.
	var found *searchpb.SearchResult
	for _, r := range resp.GetResults() {
		if r.GetHotelId() == hotel {
			found = r
			break
		}
	}
	if found == nil {
		t.Fatalf("hotel %s not found in search results after admin setup; got %+v", hotel, resp.GetResults())
	}
	if found.GetName() != "Admin Plaza" {
		t.Errorf("name = %q, want Admin Plaza (from profile)", found.GetName())
	}
	if found.GetTotal() != 41100 {
		t.Errorf("total = %d, want 41100 (3×12000 + tax + fee, from pricing)", found.GetTotal())
	}
}
