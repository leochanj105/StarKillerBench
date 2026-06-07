// Package server implements the ProfileService gRPC handlers.
//
// v1 is in-memory: UpsertProfile seeds a record into a map; GetProfile reads it
// back. State lives for the lifetime of the process and is lost on restart.
package server

import (
	"context"
	"sync"

	pb "agentbench/services/profile/api/v1"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// profile is the stored value for one hotel_id.
type profile struct {
	name        string
	address     string
	description string
	amenities   []string
	roomTypes   []string
}

// Server implements pb.ProfileServiceServer.
type Server struct {
	pb.UnimplementedProfileServiceServer

	mu       sync.RWMutex
	profiles map[string]profile
}

// NewServer returns a Server with an empty in-memory profile store.
func NewServer() *Server {
	return &Server{profiles: make(map[string]profile)}
}

// UpsertProfile seeds or overwrites the profile for one hotel_id.
func (s *Server) UpsertProfile(ctx context.Context, req *pb.UpsertProfileRequest) (*pb.UpsertProfileResponse, error) {
	if req.GetHotelId() == "" {
		return nil, status.Error(codes.InvalidArgument, "hotel_id is required")
	}
	if req.GetName() == "" {
		return nil, status.Error(codes.InvalidArgument, "name is required")
	}

	s.mu.Lock()
	s.profiles[req.GetHotelId()] = profile{
		name:        req.GetName(),
		address:     req.GetAddress(),
		description: req.GetDescription(),
		amenities:   req.GetAmenities(),
		roomTypes:   req.GetRoomTypes(),
	}
	s.mu.Unlock()

	return &pb.UpsertProfileResponse{}, nil
}

// GetProfile returns the full profile for hotel_id, or NotFound.
func (s *Server) GetProfile(ctx context.Context, req *pb.GetProfileRequest) (*pb.GetProfileResponse, error) {
	s.mu.RLock()
	p, ok := s.profiles[req.GetHotelId()]
	s.mu.RUnlock()
	if !ok {
		return nil, status.Errorf(codes.NotFound, "no profile for hotel_id %q", req.GetHotelId())
	}

	return &pb.GetProfileResponse{
		HotelId:     req.GetHotelId(),
		Name:        p.name,
		Address:     p.address,
		Description: p.description,
		Amenities:   p.amenities,
		RoomTypes:   p.roomTypes,
	}, nil
}
