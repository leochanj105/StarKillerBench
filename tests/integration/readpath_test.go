// Read-path integration test: geo + profile + pricing + search, end-to-end
// against the live container stack.
//
// Seeds a few hotels across geo (coords), profile (room types), and pricing
// (rate plans), then calls search.Search and asserts the aggregator fans out
// correctly: geo-radius filtering, per-hotel cheapest-room pricing,
// omission of unpriceable hotels, and price-ascending ranking.
//
// Shares the integration_test package with saga_test.go and reuses its
// mustDial/envOr helpers. Read-path clients are set up lazily (saga_test.go
// owns TestMain) so this file stays self-contained.

package integration_test

import (
	"context"
	"sync"
	"testing"
	"time"

	geopb "agentbench/services/geo/api/v1"
	pricingpb "agentbench/services/pricing/api/v1"
	profilepb "agentbench/services/profile/api/v1"
	searchpb "agentbench/services/search/api/v1"
)

var (
	readPathOnce  sync.Once
	geoClient     geopb.GeoServiceClient
	profileClient profilepb.ProfileServiceClient
	pricingClient pricingpb.PricingServiceClient
	searchClient  searchpb.SearchServiceClient
)

func setupReadPath() {
	readPathOnce.Do(func() {
		geoClient = geopb.NewGeoServiceClient(mustDial(envOr("GEO_ADDR", "localhost:18105")))
		profileClient = profilepb.NewProfileServiceClient(mustDial(envOr("PROFILE_ADDR", "localhost:18106")))
		pricingClient = pricingpb.NewPricingServiceClient(mustDial(envOr("PRICING_ADDR", "localhost:18107")))
		searchClient = searchpb.NewSearchServiceClient(mustDial(envOr("SEARCH_ADDR", "localhost:18108")))
	})
}

// resultByID returns the SearchResult with the given hotel_id, or nil.
func resultByID(results []*searchpb.SearchResult, id string) *searchpb.SearchResult {
	for _, r := range results {
		if r.GetHotelId() == id {
			return r
		}
	}
	return nil
}

// Seeds four hotels and asserts Search composes the three upstreams correctly:
//
//   RP_A      at the query point, STD @ 10000/nt          → total 34500, included
//   RP_B      ~11 km away, STD @ 9000 + DLX @ 15000        → cheapest 31200, included
//   RP_FAR    ~111 km away (outside 50 km radius), priced  → excluded by geo
//   RP_NOPRICE at the query point, has a room but no rate  → excluded by pricing
//
// Expected: [RP_B (31200), RP_A (34500)], ranked by total ascending.
func TestReadPath_SearchAggregates(t *testing.T) {
	setupReadPath()
	ctx := context.Background()

	lat, lng := 40.0, -74.0
	checkIn, checkOut, guests := "2026-09-01", "2026-09-04", int32(2) // 3 nights

	upsertHotel := func(id string, hlat, hlng float64) {
		t.Helper()
		if _, err := geoClient.UpsertHotel(ctx, &geopb.UpsertHotelRequest{HotelId: id, Lat: hlat, Lng: hlng}); err != nil {
			t.Fatalf("geo.UpsertHotel(%s): %v", id, err)
		}
	}
	upsertProfile := func(id string, rooms ...string) {
		t.Helper()
		if _, err := profileClient.UpsertProfile(ctx, &profilepb.UpsertProfileRequest{
			HotelId: id, Name: "Hotel " + id, Address: id + " St", RoomTypes: rooms,
		}); err != nil {
			t.Fatalf("profile.UpsertProfile(%s): %v", id, err)
		}
	}
	setRate := func(id, room string, rate int64) {
		t.Helper()
		if _, err := pricingClient.SetRatePlan(ctx, &pricingpb.SetRatePlanRequest{
			HotelId: id, RoomType: room, NightlyRate: rate, Currency: "USD",
		}); err != nil {
			t.Fatalf("pricing.SetRatePlan(%s/%s): %v", id, room, err)
		}
	}

	// geo: RP_A & RP_NOPRICE at the point, RP_B ~11km, RP_FAR ~111km (out).
	upsertHotel("RP_A", 40.0, -74.0)
	upsertHotel("RP_NOPRICE", 40.0, -74.0)
	upsertHotel("RP_B", 40.1, -74.0)
	upsertHotel("RP_FAR", 41.0, -74.0)

	// profiles (RP_NOPRICE has a room type but will get no rate plan).
	upsertProfile("RP_A", "STD")
	upsertProfile("RP_NOPRICE", "STD")
	upsertProfile("RP_B", "STD", "DLX")
	upsertProfile("RP_FAR", "STD")

	// rate plans (none for RP_NOPRICE).
	setRate("RP_A", "STD", 10000)     // 3 nights → 30000 + 3000 + 1500 = 34500
	setRate("RP_B", "STD", 9000)      // 27000 + 2700 + 1500 = 31200 (cheapest for B)
	setRate("RP_B", "DLX", 15000)     // more expensive; should not be picked
	setRate("RP_FAR", "STD", 5000)    // priced, but excluded by geo radius

	// Give the eventually-consistent nothing here a beat (all sync gRPC, but
	// keep a tiny guard against connect races on a cold stack).
	time.Sleep(200 * time.Millisecond)

	resp, err := searchClient.Search(ctx, &searchpb.SearchRequest{
		Lat: lat, Lng: lng, RadiusKm: 50,
		CheckIn: checkIn, CheckOut: checkOut, Guests: guests,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	res := resp.GetResults()

	// RP_FAR (geo) and RP_NOPRICE (pricing) must be absent.
	if resultByID(res, "RP_FAR") != nil {
		t.Errorf("RP_FAR should be excluded by the 50km radius")
	}
	if resultByID(res, "RP_NOPRICE") != nil {
		t.Errorf("RP_NOPRICE should be excluded (no rate plan)")
	}

	if len(res) != 2 {
		t.Fatalf("results = %d, want 2 (RP_B, RP_A); got %+v", len(res), res)
	}

	// Ranked by total ascending: RP_B (31200) before RP_A (34500).
	if res[0].GetHotelId() != "RP_B" || res[1].GetHotelId() != "RP_A" {
		t.Errorf("order = [%s %s], want [RP_B RP_A]", res[0].GetHotelId(), res[1].GetHotelId())
	}
	if res[0].GetTotal() != 31200 {
		t.Errorf("RP_B total = %d, want 31200 (cheapest room = STD, not DLX)", res[0].GetTotal())
	}
	if res[1].GetTotal() != 34500 {
		t.Errorf("RP_A total = %d, want 34500", res[1].GetTotal())
	}
	// Carried-through profile fields.
	if b := resultByID(res, "RP_B"); b.GetName() != "Hotel RP_B" || b.GetCurrency() != "USD" {
		t.Errorf("RP_B fields not carried through: %+v", b)
	}
}
