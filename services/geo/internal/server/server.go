// Package server implements the GeoService gRPC handlers.
//
// v1 is in-memory: hotel coordinates are seeded via UpsertHotel and Nearby
// does a linear great-circle (haversine) scan over them. State resets on
// restart.
package server

import (
	"context"
	"math"
	"sync"

	pb "agentbench/services/geo/api/v1"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// earthRadiusKm is the mean Earth radius used for haversine distances.
const earthRadiusKm = 6371.0

// coord is one hotel's seeded position.
type coord struct {
	lat float64
	lng float64
}

// Server is the in-memory GeoService implementation.
type Server struct {
	pb.UnimplementedGeoServiceServer

	mu     sync.RWMutex
	hotels map[string]coord
}

// NewServer returns a ready-to-serve *Server with empty state.
func NewServer() *Server {
	return &Server{hotels: make(map[string]coord)}
}

// validCoords reports whether (lat, lng) is a valid geographic coordinate.
func validCoords(lat, lng float64) bool {
	return lat >= -90 && lat <= 90 && lng >= -180 && lng <= 180
}

// UpsertHotel seeds or updates one hotel's coordinates, overwriting any prior
// coordinates for the same hotel_id.
func (s *Server) UpsertHotel(ctx context.Context, req *pb.UpsertHotelRequest) (*pb.UpsertHotelResponse, error) {
	if req.GetHotelId() == "" {
		return nil, status.Error(codes.InvalidArgument, "hotel_id is empty")
	}
	if !validCoords(req.GetLat(), req.GetLng()) {
		return nil, status.Error(codes.InvalidArgument, "lat/lng out of range")
	}

	s.mu.Lock()
	s.hotels[req.GetHotelId()] = coord{lat: req.GetLat(), lng: req.GetLng()}
	s.mu.Unlock()

	return &pb.UpsertHotelResponse{}, nil
}

// Nearby returns the ids of all seeded hotels whose haversine distance from
// (lat, lng) is <= radius_km. Order is unspecified.
func (s *Server) Nearby(ctx context.Context, req *pb.NearbyRequest) (*pb.NearbyResponse, error) {
	if !validCoords(req.GetLat(), req.GetLng()) {
		return nil, status.Error(codes.InvalidArgument, "lat/lng out of range")
	}
	if req.GetRadiusKm() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "radius_km is non-positive")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	ids := make([]string, 0)
	for id, c := range s.hotels {
		if haversineKm(req.GetLat(), req.GetLng(), c.lat, c.lng) <= req.GetRadiusKm() {
			ids = append(ids, id)
		}
	}
	return &pb.NearbyResponse{HotelIds: ids}, nil
}

// haversineKm returns the great-circle distance in km between two points.
func haversineKm(lat1, lng1, lat2, lng2 float64) float64 {
	rlat1 := lat1 * math.Pi / 180
	rlat2 := lat2 * math.Pi / 180
	dlat := (lat2 - lat1) * math.Pi / 180
	dlng := (lng2 - lng1) * math.Pi / 180

	a := math.Sin(dlat/2)*math.Sin(dlat/2) +
		math.Cos(rlat1)*math.Cos(rlat2)*math.Sin(dlng/2)*math.Sin(dlng/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return earthRadiusKm * c
}
