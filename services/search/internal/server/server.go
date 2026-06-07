// Package server implements the SearchService gRPC handler: the read-path
// aggregator that fans out to geo, profile, and pricing.
package server

import (
	"context"
	"time"

	geopb "agentbench/services/geo/api/v1"
	pricingpb "agentbench/services/pricing/api/v1"
	profilepb "agentbench/services/profile/api/v1"
	pb "agentbench/services/search/api/v1"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const dateLayout = "2006-01-02"

// Server aggregates geo, profile, and pricing into a ranked hotel search.
type Server struct {
	pb.UnimplementedSearchServiceServer

	geo     geopb.GeoServiceClient
	profile profilepb.ProfileServiceClient
	pricing pricingpb.PricingServiceClient
}

// NewServer wires the SearchService to its three upstreams.
func NewServer(geo geopb.GeoServiceClient, profile profilepb.ProfileServiceClient, pricing pricingpb.PricingServiceClient) *Server {
	return &Server{geo: geo, profile: profile, pricing: pricing}
}

// Search returns hotels near (lat, lng) within radius_km that have at least one
// priceable room for the stay, ranked by total price ascending.
func (s *Server) Search(ctx context.Context, req *pb.SearchRequest) (*pb.SearchResponse, error) {
	if err := validate(req); err != nil {
		return nil, err
	}

	near, err := s.geo.Nearby(ctx, &geopb.NearbyRequest{
		Lat: req.GetLat(), Lng: req.GetLng(), RadiusKm: req.GetRadiusKm(),
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "geo.Nearby: %v", err)
	}

	var results []*pb.SearchResult
	for _, hotelID := range near.GetHotelIds() {
		prof, err := s.profile.GetProfile(ctx, &profilepb.GetProfileRequest{HotelId: hotelID})
		if err != nil {
			// Missing profile → skip this hotel, not a Search error.
			continue
		}

		best := s.cheapestRoom(ctx, hotelID, prof, req)
		if best != nil {
			results = append(results, best)
		}
	}

	// Rank by total price ascending (insertion sort keeps it dependency-free
	// and stable for the small candidate sets search deals with).
	for i := 1; i < len(results); i++ {
		for j := i; j > 0 && results[j-1].GetTotal() > results[j].GetTotal(); j-- {
			results[j-1], results[j] = results[j], results[j-1]
		}
	}

	return &pb.SearchResponse{Results: results}, nil
}

// cheapestRoom prices every room type of a hotel and returns a SearchResult for
// the cheapest priceable one, or nil if none can be priced.
func (s *Server) cheapestRoom(ctx context.Context, hotelID string, prof *profilepb.GetProfileResponse, req *pb.SearchRequest) *pb.SearchResult {
	var best *pb.SearchResult
	for _, rt := range prof.GetRoomTypes() {
		quote, err := s.pricing.Quote(ctx, &pricingpb.QuoteRequest{
			HotelId:  hotelID,
			RoomType: rt,
			CheckIn:  req.GetCheckIn(),
			CheckOut: req.GetCheckOut(),
			Guests:   req.GetGuests(),
		})
		if err != nil {
			// Unpriceable room type (e.g. NotFound) → skip it.
			continue
		}
		if best == nil || quote.GetTotal() < best.GetTotal() {
			best = &pb.SearchResult{
				HotelId:     hotelID,
				Name:        prof.GetName(),
				Address:     prof.GetAddress(),
				NightlyRate: quote.GetNightlyRate(),
				Total:       quote.GetTotal(),
				Currency:    quote.GetCurrency(),
			}
		}
	}
	return best
}

// validate enforces the input rules from SPEC.error_semantics before any
// upstream call.
func validate(req *pb.SearchRequest) error {
	if req.GetLat() < -90 || req.GetLat() > 90 {
		return status.Errorf(codes.InvalidArgument, "lat %v outside [-90, 90]", req.GetLat())
	}
	if req.GetLng() < -180 || req.GetLng() > 180 {
		return status.Errorf(codes.InvalidArgument, "lng %v outside [-180, 180]", req.GetLng())
	}
	if req.GetRadiusKm() <= 0 {
		return status.Errorf(codes.InvalidArgument, "radius_km %v is non-positive", req.GetRadiusKm())
	}
	in, err := time.Parse(dateLayout, req.GetCheckIn())
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "check_in %q is not a valid YYYY-MM-DD date", req.GetCheckIn())
	}
	out, err := time.Parse(dateLayout, req.GetCheckOut())
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "check_out %q is not a valid YYYY-MM-DD date", req.GetCheckOut())
	}
	if !out.After(in) {
		return status.Errorf(codes.InvalidArgument, "check_out %q is not strictly after check_in %q", req.GetCheckOut(), req.GetCheckIn())
	}
	if req.GetGuests() <= 0 {
		return status.Errorf(codes.InvalidArgument, "guests %d is non-positive", req.GetGuests())
	}
	return nil
}
