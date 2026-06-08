// Unit tests for admin, derived from SPEC.yaml.
//
// What this service is:
//   admin is a stateless hotelier-facing proxy. Each RPC validates input and
//   forwards to the owning service (geo, profile, inventory, pricing, ads).
//
// Test contract assumed about the implementation:
//   - `server.NewServer(geo, profile, inventory, pricing, ads)` returns a
//     *Server where the five args implement the gRPC-generated *ServiceClient
//     interfaces. Tests inject mocks that record calls.
//   - Standard gRPC signatures; errors via status.Error with the codes in
//     SPEC.error_semantics; upstream errors are propagated.
//
// Authored out-of-band and audited before the agent implements. The agent
// must not modify this file.

package server_test

import (
	"context"
	"testing"

	adspb "agentbench/services/ads/api/v1"
	pb "agentbench/services/admin/api/v1"
	"agentbench/services/admin/internal/server"
	geopb "agentbench/services/geo/api/v1"
	inventorypb "agentbench/services/inventory/api/v1"
	pricingpb "agentbench/services/pricing/api/v1"
	profilepb "agentbench/services/profile/api/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ─── Mocks: each records the one method admin uses; the rest are Unimplemented.

type mockGeo struct {
	UpsertFn     func(*geopb.UpsertHotelRequest) (*geopb.UpsertHotelResponse, error)
	UpsertCalls  []*geopb.UpsertHotelRequest
}

func (m *mockGeo) UpsertHotel(ctx context.Context, in *geopb.UpsertHotelRequest, opts ...grpc.CallOption) (*geopb.UpsertHotelResponse, error) {
	m.UpsertCalls = append(m.UpsertCalls, in)
	if m.UpsertFn != nil {
		return m.UpsertFn(in)
	}
	return &geopb.UpsertHotelResponse{}, nil
}
func (m *mockGeo) Nearby(ctx context.Context, in *geopb.NearbyRequest, opts ...grpc.CallOption) (*geopb.NearbyResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not mocked")
}

type mockProfile struct {
	UpsertFn    func(*profilepb.UpsertProfileRequest) (*profilepb.UpsertProfileResponse, error)
	UpsertCalls []*profilepb.UpsertProfileRequest
}

func (m *mockProfile) UpsertProfile(ctx context.Context, in *profilepb.UpsertProfileRequest, opts ...grpc.CallOption) (*profilepb.UpsertProfileResponse, error) {
	m.UpsertCalls = append(m.UpsertCalls, in)
	if m.UpsertFn != nil {
		return m.UpsertFn(in)
	}
	return &profilepb.UpsertProfileResponse{}, nil
}
func (m *mockProfile) GetProfile(ctx context.Context, in *profilepb.GetProfileRequest, opts ...grpc.CallOption) (*profilepb.GetProfileResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not mocked")
}

type mockInventory struct {
	SetStockFn    func(*inventorypb.SetStockRequest) (*inventorypb.SetStockResponse, error)
	SetStockCalls []*inventorypb.SetStockRequest
}

func (m *mockInventory) SetStock(ctx context.Context, in *inventorypb.SetStockRequest, opts ...grpc.CallOption) (*inventorypb.SetStockResponse, error) {
	m.SetStockCalls = append(m.SetStockCalls, in)
	if m.SetStockFn != nil {
		return m.SetStockFn(in)
	}
	return &inventorypb.SetStockResponse{}, nil
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
	return nil, status.Error(codes.Unimplemented, "not mocked")
}

type mockPricing struct {
	SetRatePlanFn    func(*pricingpb.SetRatePlanRequest) (*pricingpb.SetRatePlanResponse, error)
	SetRatePlanCalls []*pricingpb.SetRatePlanRequest
}

func (m *mockPricing) SetRatePlan(ctx context.Context, in *pricingpb.SetRatePlanRequest, opts ...grpc.CallOption) (*pricingpb.SetRatePlanResponse, error) {
	m.SetRatePlanCalls = append(m.SetRatePlanCalls, in)
	if m.SetRatePlanFn != nil {
		return m.SetRatePlanFn(in)
	}
	return &pricingpb.SetRatePlanResponse{}, nil
}
func (m *mockPricing) Quote(ctx context.Context, in *pricingpb.QuoteRequest, opts ...grpc.CallOption) (*pricingpb.QuoteResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not mocked")
}

type mockAds struct {
	SetCampaignFn    func(*adspb.SetCampaignRequest) (*adspb.SetCampaignResponse, error)
	SetCampaignCalls []*adspb.SetCampaignRequest
}

func (m *mockAds) SetCampaign(ctx context.Context, in *adspb.SetCampaignRequest, opts ...grpc.CallOption) (*adspb.SetCampaignResponse, error) {
	m.SetCampaignCalls = append(m.SetCampaignCalls, in)
	if m.SetCampaignFn != nil {
		return m.SetCampaignFn(in)
	}
	return &adspb.SetCampaignResponse{CampaignId: "camp-default"}, nil
}
func (m *mockAds) SelectSponsored(ctx context.Context, in *adspb.SelectSponsoredRequest, opts ...grpc.CallOption) (*adspb.SelectSponsoredResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not mocked")
}
func (m *mockAds) LogImpression(ctx context.Context, in *adspb.LogImpressionRequest, opts ...grpc.CallOption) (*adspb.LogImpressionResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not mocked")
}
func (m *mockAds) LogClick(ctx context.Context, in *adspb.LogClickRequest, opts ...grpc.CallOption) (*adspb.LogClickResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not mocked")
}

type mocks struct {
	geo   *mockGeo
	prof  *mockProfile
	inv   *mockInventory
	price *mockPricing
	ads   *mockAds
}

func newServer() (pb.AdminServiceServer, *mocks) {
	m := &mocks{&mockGeo{}, &mockProfile{}, &mockInventory{}, &mockPricing{}, &mockAds{}}
	return server.NewServer(m.geo, m.prof, m.inv, m.price, m.ads), m
}

func wantCode(t *testing.T, err error, want codes.Code) {
	t.Helper()
	if got := status.Code(err); got != want {
		t.Errorf("status code = %v, want %v (err=%v)", got, want, err)
	}
}

// ════════════════════════════════════════════════════════════════════════════
// UpsertHotel → geo + profile
// ════════════════════════════════════════════════════════════════════════════

func TestUpsertHotel_FansOut(t *testing.T) {
	s, m := newServer()
	_, err := s.UpsertHotel(context.Background(), &pb.UpsertHotelRequest{
		HotelId: "H1", Name: "Grand", Address: "1 Main", Lat: 40, Lng: -74,
		RoomTypes: []string{"STD", "DLX"},
	})
	if err != nil {
		t.Fatalf("UpsertHotel: %v", err)
	}
	if len(m.geo.UpsertCalls) != 1 {
		t.Fatalf("geo.UpsertHotel calls = %d, want 1", len(m.geo.UpsertCalls))
	}
	g := m.geo.UpsertCalls[0]
	if g.GetHotelId() != "H1" || g.GetLat() != 40 || g.GetLng() != -74 {
		t.Errorf("geo got %+v, want H1/40/-74", g)
	}
	if len(m.prof.UpsertCalls) != 1 {
		t.Fatalf("profile.UpsertProfile calls = %d, want 1", len(m.prof.UpsertCalls))
	}
	p := m.prof.UpsertCalls[0]
	if p.GetHotelId() != "H1" || p.GetName() != "Grand" || len(p.GetRoomTypes()) != 2 {
		t.Errorf("profile got %+v, want H1/Grand/2 room types", p)
	}
}

// Empty hotel_id/name is rejected before any upstream call.
func TestUpsertHotel_Invalid(t *testing.T) {
	s, m := newServer()
	_, err := s.UpsertHotel(context.Background(), &pb.UpsertHotelRequest{HotelId: "", Name: "Grand"})
	wantCode(t, err, codes.InvalidArgument)
	_, err = s.UpsertHotel(context.Background(), &pb.UpsertHotelRequest{HotelId: "H1", Name: ""})
	wantCode(t, err, codes.InvalidArgument)
	if len(m.geo.UpsertCalls) != 0 || len(m.prof.UpsertCalls) != 0 {
		t.Errorf("upstreams called despite invalid input")
	}
}

// An upstream error is propagated.
func TestUpsertHotel_UpstreamError(t *testing.T) {
	s, m := newServer()
	m.geo.UpsertFn = func(*geopb.UpsertHotelRequest) (*geopb.UpsertHotelResponse, error) {
		return nil, status.Error(codes.Unavailable, "geo down")
	}
	_, err := s.UpsertHotel(context.Background(), &pb.UpsertHotelRequest{HotelId: "H1", Name: "Grand"})
	if status.Code(err) == codes.OK {
		t.Errorf("expected an error to propagate from geo")
	}
}

// ════════════════════════════════════════════════════════════════════════════
// SetInventory / SetRatePlan / SetCampaign
// ════════════════════════════════════════════════════════════════════════════

func TestSetInventory_ForwardsToInventory(t *testing.T) {
	s, m := newServer()
	_, err := s.SetInventory(context.Background(), &pb.SetInventoryRequest{
		HotelId: "H1", RoomType: "STD", Date: "2026-09-01", Total: 5,
	})
	if err != nil {
		t.Fatalf("SetInventory: %v", err)
	}
	if len(m.inv.SetStockCalls) != 1 {
		t.Fatalf("inventory.SetStock calls = %d, want 1", len(m.inv.SetStockCalls))
	}
	c := m.inv.SetStockCalls[0]
	if c.GetHotelId() != "H1" || c.GetRoomType() != "STD" || c.GetDate() != "2026-09-01" || c.GetQuantity() != 5 {
		t.Errorf("SetStock got %+v, want H1/STD/2026-09-01/5", c)
	}
}

func TestSetRatePlan_ForwardsToPricing(t *testing.T) {
	s, m := newServer()
	_, err := s.SetRatePlan(context.Background(), &pb.SetRatePlanRequest{
		HotelId: "H1", RoomType: "STD", NightlyRate: 10000, Currency: "USD",
	})
	if err != nil {
		t.Fatalf("SetRatePlan: %v", err)
	}
	if len(m.price.SetRatePlanCalls) != 1 {
		t.Fatalf("pricing.SetRatePlan calls = %d, want 1", len(m.price.SetRatePlanCalls))
	}
	c := m.price.SetRatePlanCalls[0]
	if c.GetHotelId() != "H1" || c.GetNightlyRate() != 10000 || c.GetCurrency() != "USD" {
		t.Errorf("SetRatePlan got %+v, want H1/10000/USD", c)
	}
}

func TestSetCampaign_ForwardsAndReturnsID(t *testing.T) {
	s, m := newServer()
	m.ads.SetCampaignFn = func(*adspb.SetCampaignRequest) (*adspb.SetCampaignResponse, error) {
		return &adspb.SetCampaignResponse{CampaignId: "camp-7"}, nil
	}
	r, err := s.SetCampaign(context.Background(), &pb.SetCampaignRequest{
		AdvertiserId: "adv", HotelId: "H1", DailyBudget: 1000, Bid: 50,
	})
	if err != nil {
		t.Fatalf("SetCampaign: %v", err)
	}
	if r.GetCampaignId() != "camp-7" {
		t.Errorf("campaign_id = %q, want camp-7 (propagated)", r.GetCampaignId())
	}
	if len(m.ads.SetCampaignCalls) != 1 {
		t.Errorf("ads.SetCampaign calls = %d, want 1", len(m.ads.SetCampaignCalls))
	}
}

// Validation: empty required fields → InvalidArgument, no upstream call.
func TestSetters_Invalid(t *testing.T) {
	s, m := newServer()
	_, err := s.SetInventory(context.Background(), &pb.SetInventoryRequest{HotelId: "", RoomType: "STD", Date: "d", Total: 1})
	wantCode(t, err, codes.InvalidArgument)
	_, err = s.SetRatePlan(context.Background(), &pb.SetRatePlanRequest{HotelId: "H1", RoomType: "", NightlyRate: 1, Currency: "USD"})
	wantCode(t, err, codes.InvalidArgument)
	_, err = s.SetCampaign(context.Background(), &pb.SetCampaignRequest{AdvertiserId: "", HotelId: "H1", DailyBudget: 1, Bid: 1})
	wantCode(t, err, codes.InvalidArgument)

	if len(m.inv.SetStockCalls) != 0 || len(m.price.SetRatePlanCalls) != 0 || len(m.ads.SetCampaignCalls) != 0 {
		t.Errorf("upstreams called despite invalid input")
	}
}
