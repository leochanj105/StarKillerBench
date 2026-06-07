// Package server implements the v1 PricingService handlers.
//
// State is in-memory and per-process: a nightly rate is seeded per
// (hotel_id, room_type) via SetRatePlan, and Quote prices a stay from it
// using the deterministic v1 formula in SPEC.yaml.
package server

import (
	"context"
	"sync"
	"time"

	pb "agentbench/services/pricing/api/v1"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// dateLayout is the YYYY-MM-DD format used for check_in / check_out.
const dateLayout = "2006-01-02"

// flatBookingFee is the flat per-stay booking fee in minor units.
const flatBookingFee = 1500

// ratePlan is a seeded nightly rate for one (hotel_id, room_type).
type ratePlan struct {
	nightlyRate int64
	currency    string
}

// Server implements pb.PricingServiceServer.
type Server struct {
	pb.UnimplementedPricingServiceServer

	mu    sync.RWMutex
	plans map[string]ratePlan // key: "<hotel_id>|<room_type>"
}

// NewServer returns a ready-to-use *Server with empty in-memory state.
func NewServer() *Server {
	return &Server{plans: make(map[string]ratePlan)}
}

func rateKey(hotelID, roomType string) string {
	return hotelID + "|" + roomType
}

// SetRatePlan seeds or overwrites the nightly rate for one
// (hotel_id, room_type).
func (s *Server) SetRatePlan(ctx context.Context, req *pb.SetRatePlanRequest) (*pb.SetRatePlanResponse, error) {
	if req.GetHotelId() == "" || req.GetRoomType() == "" {
		return nil, status.Error(codes.InvalidArgument, "hotel_id and room_type must be non-empty")
	}
	if req.GetNightlyRate() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "nightly_rate must be positive")
	}
	if req.GetCurrency() == "" {
		return nil, status.Error(codes.InvalidArgument, "currency must be non-empty")
	}

	s.mu.Lock()
	s.plans[rateKey(req.GetHotelId(), req.GetRoomType())] = ratePlan{
		nightlyRate: req.GetNightlyRate(),
		currency:    req.GetCurrency(),
	}
	s.mu.Unlock()

	return &pb.SetRatePlanResponse{}, nil
}

// Quote prices a stay using the v1 formula:
//
//	subtotal = nightly_rate * nights
//	taxes    = subtotal / 10
//	fees     = 1500
//	total    = subtotal + taxes + fees
func (s *Server) Quote(ctx context.Context, req *pb.QuoteRequest) (*pb.QuoteResponse, error) {
	if req.GetGuests() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "guests must be positive")
	}

	checkIn, err := time.Parse(dateLayout, req.GetCheckIn())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "check_in must be a valid YYYY-MM-DD date")
	}
	checkOut, err := time.Parse(dateLayout, req.GetCheckOut())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "check_out must be a valid YYYY-MM-DD date")
	}
	if !checkOut.After(checkIn) {
		return nil, status.Error(codes.InvalidArgument, "check_out must be strictly after check_in")
	}

	s.mu.RLock()
	plan, ok := s.plans[rateKey(req.GetHotelId(), req.GetRoomType())]
	s.mu.RUnlock()
	if !ok {
		return nil, status.Error(codes.NotFound, "no rate plan for (hotel_id, room_type)")
	}

	nights := int32(checkOut.Sub(checkIn).Hours() / 24)
	subtotal := plan.nightlyRate * int64(nights)
	taxes := subtotal / 10
	fees := int64(flatBookingFee)
	total := subtotal + taxes + fees

	return &pb.QuoteResponse{
		NightlyRate: plan.nightlyRate,
		Nights:      nights,
		Subtotal:    subtotal,
		Taxes:       taxes,
		Fees:        fees,
		Total:       total,
		Currency:    plan.currency,
	}, nil
}
