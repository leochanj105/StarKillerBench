// Package server implements the ads gRPC service: a mock advertising bidder
// that runs a deterministic second-price auction over advertiser campaigns.
package server

import (
	"context"
	"fmt"
	"sort"
	"sync"

	pb "agentbench/services/ads/api/v1"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// campaign is the in-memory record for one advertiser campaign.
type campaign struct {
	campaignID      string
	advertiserID    string
	hotelID         string
	bid             int64
	remainingBudget int64 // decremented on win; never negative
}

// Server implements pb.AdsServiceServer with in-memory, per-process state.
type Server struct {
	pb.UnimplementedAdsServiceServer

	mu        sync.Mutex
	campaigns map[string]*campaign
	nextID    int64
}

// NewServer returns a ready-to-use *Server.
func NewServer() *Server {
	return &Server{
		campaigns: make(map[string]*campaign),
	}
}

// SetCampaign creates a campaign for a hotel and returns its generated id.
func (s *Server) SetCampaign(ctx context.Context, req *pb.SetCampaignRequest) (*pb.SetCampaignResponse, error) {
	if req.GetAdvertiserId() == "" || req.GetHotelId() == "" {
		return nil, status.Error(codes.InvalidArgument, "advertiser_id and hotel_id are required")
	}
	if req.GetDailyBudget() <= 0 || req.GetBid() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "daily_budget and bid must be positive")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.nextID++
	id := fmt.Sprintf("camp-%d", s.nextID)
	s.campaigns[id] = &campaign{
		campaignID:      id,
		advertiserID:    req.GetAdvertiserId(),
		hotelID:         req.GetHotelId(),
		bid:             req.GetBid(),
		remainingBudget: req.GetDailyBudget(),
	}
	return &pb.SetCampaignResponse{CampaignId: id}, nil
}

// SelectSponsored runs the second-price auction over eligible campaigns and
// charges each winner its price against remaining_budget.
func (s *Server) SelectSponsored(ctx context.Context, req *pb.SelectSponsoredRequest) (*pb.SelectSponsoredResponse, error) {
	if req.GetSlotCount() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "slot_count must be positive")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Eligible campaigns = those with remaining_budget > 0, sorted by bid desc.
	eligible := make([]*campaign, 0, len(s.campaigns))
	for _, c := range s.campaigns {
		if c.remainingBudget > 0 {
			eligible = append(eligible, c)
		}
	}
	sort.Slice(eligible, func(i, j int) bool {
		if eligible[i].bid != eligible[j].bid {
			return eligible[i].bid > eligible[j].bid
		}
		return eligible[i].campaignID < eligible[j].campaignID
	})

	slots := make([]*pb.SponsoredSlot, 0, req.GetSlotCount())
	filled := int32(0)
	for i := 0; i < len(eligible) && filled < req.GetSlotCount(); i++ {
		c := eligible[i]
		// Price is the next-lower bid among eligible campaigns, or this
		// campaign's own bid if it is the lowest/only bidder (first-price
		// fallback for the last slot).
		price := c.bid
		if i+1 < len(eligible) {
			price = eligible[i+1].bid
		}
		// Skip winners that cannot afford their price; leave the slot to the
		// next eligible campaign.
		if c.remainingBudget < price {
			continue
		}
		c.remainingBudget -= price
		slots = append(slots, &pb.SponsoredSlot{
			HotelId:    c.hotelID,
			CampaignId: c.campaignID,
			Price:      price,
		})
		filled++
	}

	return &pb.SelectSponsoredResponse{Slots: slots}, nil
}

// LogImpression acknowledges an impression. v1 does not persist or emit.
func (s *Server) LogImpression(ctx context.Context, req *pb.LogImpressionRequest) (*pb.LogImpressionResponse, error) {
	if req.GetHotelId() == "" {
		return nil, status.Error(codes.InvalidArgument, "hotel_id is required")
	}
	return &pb.LogImpressionResponse{}, nil
}

// LogClick acknowledges a click. v1 does not persist or emit.
func (s *Server) LogClick(ctx context.Context, req *pb.LogClickRequest) (*pb.LogClickResponse, error) {
	if req.GetHotelId() == "" {
		return nil, status.Error(codes.InvalidArgument, "hotel_id is required")
	}
	return &pb.LogClickResponse{}, nil
}
