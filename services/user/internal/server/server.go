// Package server implements the UserService gRPC handlers.
//
// State is in-memory and per-process (see SPEC.yaml). The loyalty balance is a
// read-modify-write counter; concurrent accruals for the same user must
// serialize without losing updates, enforced here with a mutex.
package server

import (
	"context"
	"sync"

	pb "agentbench/services/user/api/v1"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// user is the per-user record held in the in-memory store.
type user struct {
	currency string
	locale   string
	points   int64
}

// Server implements pb.UserServiceServer.
type Server struct {
	pb.UnimplementedUserServiceServer

	mu    sync.Mutex
	users map[string]*user
}

// NewServer returns a Server with an empty user store.
func NewServer() *Server {
	return &Server{users: make(map[string]*user)}
}

// CreateUser creates a user record with zero points and empty preferences.
func (s *Server) CreateUser(ctx context.Context, req *pb.CreateUserRequest) (*pb.CreateUserResponse, error) {
	if req.GetUserId() == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.users[req.GetUserId()]; ok {
		return nil, status.Errorf(codes.AlreadyExists, "user %q already exists", req.GetUserId())
	}
	s.users[req.GetUserId()] = &user{}
	return &pb.CreateUserResponse{}, nil
}

// GetUser returns the user's preferences and current points balance.
func (s *Server) GetUser(ctx context.Context, req *pb.GetUserRequest) (*pb.GetUserResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	u, ok := s.users[req.GetUserId()]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "user %q not found", req.GetUserId())
	}
	return &pb.GetUserResponse{
		UserId:   req.GetUserId(),
		Currency: u.currency,
		Locale:   u.locale,
		Points:   u.points,
	}, nil
}

// UpdatePreferences overwrites the user's preference fields.
func (s *Server) UpdatePreferences(ctx context.Context, req *pb.UpdatePreferencesRequest) (*pb.UpdatePreferencesResponse, error) {
	if req.GetUserId() == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	u, ok := s.users[req.GetUserId()]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "user %q not found", req.GetUserId())
	}
	u.currency = req.GetCurrency()
	u.locale = req.GetLocale()
	return &pb.UpdatePreferencesResponse{}, nil
}

// GetPoints returns the current loyalty points balance.
func (s *Server) GetPoints(ctx context.Context, req *pb.GetPointsRequest) (*pb.GetPointsResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	u, ok := s.users[req.GetUserId()]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "user %q not found", req.GetUserId())
	}
	return &pb.GetPointsResponse{Points: u.points}, nil
}

// AccrueOnBooking adds amount/100 loyalty points (integer division) to the
// user's balance and returns the new total. The increment is performed under
// the store lock so concurrent accruals for the same user do not lose updates.
func (s *Server) AccrueOnBooking(ctx context.Context, req *pb.AccrueOnBookingRequest) (*pb.AccrueOnBookingResponse, error) {
	if req.GetUserId() == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	if req.GetAmount() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "amount must be positive")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	u, ok := s.users[req.GetUserId()]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "user %q not found", req.GetUserId())
	}
	u.points += req.GetAmount() / 100
	return &pb.AccrueOnBookingResponse{Points: u.points}, nil
}
