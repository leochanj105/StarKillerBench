// Unit tests for search, derived from SPEC.yaml.
//
// What this service is:
//   search is the read-path aggregator. Search(lat,lng,radius,dates,guests)
//   runs: geo.Nearby → profile.GetProfile (per hotel) → pricing.Quote (per
//   room_type) → keep cheapest priceable room per hotel → rank by total asc.
//
// Test contract assumed about the implementation:
//   - `server.NewServer(geo, profile, pricing)` returns a *Server where the
//     three args implement the gRPC-generated GeoServiceClient,
//     ProfileServiceClient, and PricingServiceClient interfaces. Tests inject
//     mocks.
//   - Standard gRPC signatures; errors via status.Error with the codes in
//     SPEC.error_semantics.
//
// Authored out-of-band and audited before the agent implements. The agent
// must not modify this file.

package server_test

import (
	"context"
	"testing"

	geopb "agentbench/services/geo/api/v1"
	pricingpb "agentbench/services/pricing/api/v1"
	profilepb "agentbench/services/profile/api/v1"
	pb "agentbench/services/search/api/v1"
	"agentbench/services/search/internal/server"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ─── Mocks ──────────────────────────────────────────────────────────────────

type mockGeo struct {
	NearbyFn func(ctx context.Context, in *geopb.NearbyRequest, opts ...grpc.CallOption) (*geopb.NearbyResponse, error)
}

func (m *mockGeo) UpsertHotel(ctx context.Context, in *geopb.UpsertHotelRequest, opts ...grpc.CallOption) (*geopb.UpsertHotelResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not mocked")
}
func (m *mockGeo) Nearby(ctx context.Context, in *geopb.NearbyRequest, opts ...grpc.CallOption) (*geopb.NearbyResponse, error) {
	return m.NearbyFn(ctx, in, opts...)
}

type mockProfile struct {
	GetProfileFn func(ctx context.Context, in *profilepb.GetProfileRequest, opts ...grpc.CallOption) (*profilepb.GetProfileResponse, error)
}

func (m *mockProfile) UpsertProfile(ctx context.Context, in *profilepb.UpsertProfileRequest, opts ...grpc.CallOption) (*profilepb.UpsertProfileResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not mocked")
}
func (m *mockProfile) GetProfile(ctx context.Context, in *profilepb.GetProfileRequest, opts ...grpc.CallOption) (*profilepb.GetProfileResponse, error) {
	return m.GetProfileFn(ctx, in, opts...)
}

type mockPricing struct {
	QuoteFn func(ctx context.Context, in *pricingpb.QuoteRequest, opts ...grpc.CallOption) (*pricingpb.QuoteResponse, error)
}

func (m *mockPricing) SetRatePlan(ctx context.Context, in *pricingpb.SetRatePlanRequest, opts ...grpc.CallOption) (*pricingpb.SetRatePlanResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not mocked")
}
func (m *mockPricing) Quote(ctx context.Context, in *pricingpb.QuoteRequest, opts ...grpc.CallOption) (*pricingpb.QuoteResponse, error) {
	return m.QuoteFn(ctx, in, opts...)
}

// ─── Mock builders for common shapes ─────────────────────────────────────────

// geoReturning builds a mockGeo whose Nearby returns the given ids.
func geoReturning(ids ...string) *mockGeo {
	return &mockGeo{NearbyFn: func(_ context.Context, _ *geopb.NearbyRequest, _ ...grpc.CallOption) (*geopb.NearbyResponse, error) {
		return &geopb.NearbyResponse{HotelIds: ids}, nil
	}}
}

// profileFromMap builds a mockProfile serving the given hotel→roomTypes map;
// unknown hotels return NotFound.
func profileFromMap(rooms map[string][]string) *mockProfile {
	return &mockProfile{GetProfileFn: func(_ context.Context, in *profilepb.GetProfileRequest, _ ...grpc.CallOption) (*profilepb.GetProfileResponse, error) {
		rt, ok := rooms[in.GetHotelId()]
		if !ok {
			return nil, status.Error(codes.NotFound, "no profile")
		}
		return &profilepb.GetProfileResponse{
			HotelId: in.GetHotelId(), Name: "Hotel " + in.GetHotelId(),
			Address: in.GetHotelId() + " St", RoomTypes: rt,
		}, nil
	}}
}

// pricingFromMap builds a mockPricing serving totals keyed by
// "<hotel>|<room_type>"; absent keys return NotFound. nightly_rate is echoed
// as total/3 just to have a distinct field; tests assert on total.
func pricingFromMap(totals map[string]int64) *mockPricing {
	return &mockPricing{QuoteFn: func(_ context.Context, in *pricingpb.QuoteRequest, _ ...grpc.CallOption) (*pricingpb.QuoteResponse, error) {
		key := in.GetHotelId() + "|" + in.GetRoomType()
		total, ok := totals[key]
		if !ok {
			return nil, status.Error(codes.NotFound, "no rate plan")
		}
		return &pricingpb.QuoteResponse{
			NightlyRate: total / 3, Nights: 3, Subtotal: total, Taxes: 0, Fees: 0,
			Total: total, Currency: "USD",
		}, nil
	}}
}

func searchReq() *pb.SearchRequest {
	return &pb.SearchRequest{
		Lat: 0, Lng: 0, RadiusKm: 50,
		CheckIn: "2026-07-01", CheckOut: "2026-07-04", Guests: 2,
	}
}

func doSearch(t *testing.T, s pb.SearchServiceServer, req *pb.SearchRequest) []*pb.SearchResult {
	t.Helper()
	r, err := s.Search(context.Background(), req)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	return r.GetResults()
}

func wantCode(t *testing.T, err error, want codes.Code) {
	t.Helper()
	if got := status.Code(err); got != want {
		t.Errorf("status code = %v, want %v (err=%v)", got, want, err)
	}
}

// ════════════════════════════════════════════════════════════════════════════
// Happy path + ranking
// ════════════════════════════════════════════════════════════════════════════

// Two hotels, each one room type. Results are ranked by total ascending, so
// the cheaper hotel (H2 @ 11000) comes before H1 @ 12500.
func TestSearch_RanksByPriceAscending(t *testing.T) {
	s := server.NewServer(
		geoReturning("H1", "H2"),
		profileFromMap(map[string][]string{"H1": {"STD"}, "H2": {"STD"}}),
		pricingFromMap(map[string]int64{"H1|STD": 12500, "H2|STD": 11000}),
	)
	res := doSearch(t, s, searchReq())
	if len(res) != 2 {
		t.Fatalf("results = %d, want 2", len(res))
	}
	if res[0].GetHotelId() != "H2" || res[1].GetHotelId() != "H1" {
		t.Errorf("order = [%s %s], want [H2 H1]", res[0].GetHotelId(), res[1].GetHotelId())
	}
	if res[0].GetTotal() != 11000 || res[1].GetTotal() != 12500 {
		t.Errorf("totals = [%d %d], want [11000 12500]", res[0].GetTotal(), res[1].GetTotal())
	}
	// Surfaced profile fields are carried through.
	if res[0].GetName() != "Hotel H2" || res[0].GetCurrency() != "USD" {
		t.Errorf("result fields not carried through: %+v", res[0])
	}
}

// A hotel with several room types is priced at its cheapest one.
func TestSearch_PicksCheapestRoomType(t *testing.T) {
	s := server.NewServer(
		geoReturning("H1"),
		profileFromMap(map[string][]string{"H1": {"STD", "DLX"}}),
		pricingFromMap(map[string]int64{"H1|STD": 12500, "H1|DLX": 20000}),
	)
	res := doSearch(t, s, searchReq())
	if len(res) != 1 {
		t.Fatalf("results = %d, want 1", len(res))
	}
	if res[0].GetTotal() != 12500 {
		t.Errorf("total = %d, want 12500 (cheapest room)", res[0].GetTotal())
	}
}

// A hotel whose room types are all unpriceable (pricing NotFound) is omitted,
// not errored.
func TestSearch_OmitsUnpriceableHotel(t *testing.T) {
	s := server.NewServer(
		geoReturning("H1", "H3"),
		profileFromMap(map[string][]string{"H1": {"STD"}, "H3": {"STD"}}),
		pricingFromMap(map[string]int64{"H1|STD": 12500}), // H3 has no rate plan
	)
	res := doSearch(t, s, searchReq())
	if len(res) != 1 || res[0].GetHotelId() != "H1" {
		t.Errorf("results = %+v, want only H1", res)
	}
}

// A hotel returned by geo but missing from profile (NotFound) is skipped.
func TestSearch_SkipsMissingProfile(t *testing.T) {
	s := server.NewServer(
		geoReturning("H1", "GHOST"),
		profileFromMap(map[string][]string{"H1": {"STD"}}), // GHOST absent → NotFound
		pricingFromMap(map[string]int64{"H1|STD": 12500}),
	)
	res := doSearch(t, s, searchReq())
	if len(res) != 1 || res[0].GetHotelId() != "H1" {
		t.Errorf("results = %+v, want only H1", res)
	}
}

// No hotels near → empty result, not an error.
func TestSearch_Empty(t *testing.T) {
	s := server.NewServer(
		geoReturning(),
		profileFromMap(map[string][]string{}),
		pricingFromMap(map[string]int64{}),
	)
	if res := doSearch(t, s, searchReq()); len(res) != 0 {
		t.Errorf("results = %+v, want empty", res)
	}
}

// ════════════════════════════════════════════════════════════════════════════
// Input validation — rejected before any upstream call
// ════════════════════════════════════════════════════════════════════════════

func TestSearch_InvalidInput(t *testing.T) {
	// Upstreams panic if called, proving validation happens first.
	geo := &mockGeo{NearbyFn: func(_ context.Context, _ *geopb.NearbyRequest, _ ...grpc.CallOption) (*geopb.NearbyResponse, error) {
		t.Fatalf("geo.Nearby called despite invalid input")
		return nil, nil
	}}
	prof := profileFromMap(map[string][]string{})
	price := pricingFromMap(map[string]int64{})
	s := server.NewServer(geo, prof, price)

	cases := []struct {
		name string
		req  *pb.SearchRequest
	}{
		{"lat out of range", &pb.SearchRequest{Lat: 91, Lng: 0, RadiusKm: 10, CheckIn: "2026-07-01", CheckOut: "2026-07-04", Guests: 1}},
		{"radius non-positive", &pb.SearchRequest{Lat: 0, Lng: 0, RadiusKm: 0, CheckIn: "2026-07-01", CheckOut: "2026-07-04", Guests: 1}},
		{"reversed dates", &pb.SearchRequest{Lat: 0, Lng: 0, RadiusKm: 10, CheckIn: "2026-07-04", CheckOut: "2026-07-01", Guests: 1}},
		{"malformed date", &pb.SearchRequest{Lat: 0, Lng: 0, RadiusKm: 10, CheckIn: "nope", CheckOut: "2026-07-04", Guests: 1}},
		{"guests non-positive", &pb.SearchRequest{Lat: 0, Lng: 0, RadiusKm: 10, CheckIn: "2026-07-01", CheckOut: "2026-07-04", Guests: 0}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := s.Search(context.Background(), c.req)
			wantCode(t, err, codes.InvalidArgument)
		})
	}
}

// geo.Nearby failure surfaces as Internal.
func TestSearch_GeoError(t *testing.T) {
	geo := &mockGeo{NearbyFn: func(_ context.Context, _ *geopb.NearbyRequest, _ ...grpc.CallOption) (*geopb.NearbyResponse, error) {
		return nil, status.Error(codes.Unavailable, "geo down")
	}}
	s := server.NewServer(geo, profileFromMap(map[string][]string{}), pricingFromMap(map[string]int64{}))
	_, err := s.Search(context.Background(), searchReq())
	wantCode(t, err, codes.Internal)
}
