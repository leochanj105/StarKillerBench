// Package server implements the AdminService handlers. admin owns no state;
// each RPC validates input and forwards to the service that owns the data.
package server

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "agentbench/services/admin/api/v1"
	adspb "agentbench/services/ads/api/v1"
	geopb "agentbench/services/geo/api/v1"
	inventorypb "agentbench/services/inventory/api/v1"
	pricingpb "agentbench/services/pricing/api/v1"
	profilepb "agentbench/services/profile/api/v1"
)

// Server fans admin writes out to the owning upstream services.
type Server struct {
	pb.UnimplementedAdminServiceServer

	geo       geopb.GeoServiceClient
	profile   profilepb.ProfileServiceClient
	inventory inventorypb.InventoryServiceClient
	pricing   pricingpb.PricingServiceClient
	ads       adspb.AdsServiceClient
}

// NewServer wires the upstream clients admin forwards to.
func NewServer(
	geo geopb.GeoServiceClient,
	profile profilepb.ProfileServiceClient,
	inventory inventorypb.InventoryServiceClient,
	pricing pricingpb.PricingServiceClient,
	ads adspb.AdsServiceClient,
) *Server {
	return &Server{
		geo:       geo,
		profile:   profile,
		inventory: inventory,
		pricing:   pricing,
		ads:       ads,
	}
}

// UpsertHotel forwards coordinates to geo and metadata to profile.
func (s *Server) UpsertHotel(ctx context.Context, req *pb.UpsertHotelRequest) (*pb.UpsertHotelResponse, error) {
	if req.GetHotelId() == "" || req.GetName() == "" {
		return nil, status.Error(codes.InvalidArgument, "hotel_id and name are required")
	}
	if _, err := s.geo.UpsertHotel(ctx, &geopb.UpsertHotelRequest{
		HotelId: req.GetHotelId(),
		Lat:     req.GetLat(),
		Lng:     req.GetLng(),
	}); err != nil {
		return nil, err
	}
	if _, err := s.profile.UpsertProfile(ctx, &profilepb.UpsertProfileRequest{
		HotelId:   req.GetHotelId(),
		Name:      req.GetName(),
		Address:   req.GetAddress(),
		RoomTypes: req.GetRoomTypes(),
	}); err != nil {
		return nil, err
	}
	return &pb.UpsertHotelResponse{}, nil
}

// SetInventory forwards total stock to inventory.SetStock.
func (s *Server) SetInventory(ctx context.Context, req *pb.SetInventoryRequest) (*pb.SetInventoryResponse, error) {
	if req.GetHotelId() == "" || req.GetRoomType() == "" {
		return nil, status.Error(codes.InvalidArgument, "hotel_id and room_type are required")
	}
	if _, err := s.inventory.SetStock(ctx, &inventorypb.SetStockRequest{
		HotelId:  req.GetHotelId(),
		RoomType: req.GetRoomType(),
		Date:     req.GetDate(),
		Quantity: req.GetTotal(),
	}); err != nil {
		return nil, err
	}
	return &pb.SetInventoryResponse{}, nil
}

// SetRatePlan forwards the nightly rate to pricing.SetRatePlan.
func (s *Server) SetRatePlan(ctx context.Context, req *pb.SetRatePlanRequest) (*pb.SetRatePlanResponse, error) {
	if req.GetHotelId() == "" || req.GetRoomType() == "" {
		return nil, status.Error(codes.InvalidArgument, "hotel_id and room_type are required")
	}
	if _, err := s.pricing.SetRatePlan(ctx, &pricingpb.SetRatePlanRequest{
		HotelId:     req.GetHotelId(),
		RoomType:    req.GetRoomType(),
		NightlyRate: req.GetNightlyRate(),
		Currency:    req.GetCurrency(),
	}); err != nil {
		return nil, err
	}
	return &pb.SetRatePlanResponse{}, nil
}

// SetCampaign creates an ad campaign via ads.SetCampaign and returns its id.
func (s *Server) SetCampaign(ctx context.Context, req *pb.SetCampaignRequest) (*pb.SetCampaignResponse, error) {
	if req.GetAdvertiserId() == "" || req.GetHotelId() == "" {
		return nil, status.Error(codes.InvalidArgument, "advertiser_id and hotel_id are required")
	}
	resp, err := s.ads.SetCampaign(ctx, &adspb.SetCampaignRequest{
		AdvertiserId: req.GetAdvertiserId(),
		HotelId:      req.GetHotelId(),
		DailyBudget:  req.GetDailyBudget(),
		Bid:          req.GetBid(),
	})
	if err != nil {
		return nil, err
	}
	return &pb.SetCampaignResponse{CampaignId: resp.GetCampaignId()}, nil
}
