package server

import (
	"context"
	"errors"

	pb "agentbench/services/inventory/api/v1"
	"agentbench/services/inventory/internal/store"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Server implements pb.InventoryServiceServer. It validates request shapes,
// delegates state changes to a Store, and maps the store's sentinel errors
// to gRPC status codes per SPEC.error_semantics.
type Server struct {
	pb.UnimplementedInventoryServiceServer

	store store.Store
}

// NewServer returns a Server backed by the in-memory store (v1 default;
// used by unit tests and local dev without Postgres).
func NewServer() *Server {
	return &Server{store: store.NewMemStore()}
}

// NewServerWithStore returns a Server backed by the given store (used to
// wire the Postgres backend in v2).
func NewServerWithStore(s store.Store) *Server {
	return &Server{store: s}
}

// mapErr translates store sentinel errors to gRPC status errors.
func mapErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, store.ErrInsufficient):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, store.ErrConsumed):
		return status.Error(codes.FailedPrecondition, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}

func (s *Server) SetStock(ctx context.Context, req *pb.SetStockRequest) (*pb.SetStockResponse, error) {
	if req.GetQuantity() < 0 {
		return nil, status.Error(codes.InvalidArgument, "quantity must be non-negative")
	}
	if err := s.store.SetStock(ctx, req.GetHotelId(), req.GetRoomType(), req.GetDate(), req.GetQuantity()); err != nil {
		return nil, mapErr(err)
	}
	return &pb.SetStockResponse{}, nil
}

func (s *Server) Hold(ctx context.Context, req *pb.HoldRequest) (*pb.HoldResponse, error) {
	if req.GetQuantity() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "quantity must be positive")
	}
	id, err := s.store.Hold(ctx, req.GetHotelId(), req.GetRoomType(), req.GetDate(), req.GetQuantity())
	if err != nil {
		return nil, mapErr(err)
	}
	return &pb.HoldResponse{HoldId: id}, nil
}

func (s *Server) Commit(ctx context.Context, req *pb.CommitRequest) (*pb.CommitResponse, error) {
	if err := s.store.Commit(ctx, req.GetHoldId()); err != nil {
		return nil, mapErr(err)
	}
	return &pb.CommitResponse{}, nil
}

func (s *Server) Release(ctx context.Context, req *pb.ReleaseRequest) (*pb.ReleaseResponse, error) {
	if err := s.store.Release(ctx, req.GetHoldId()); err != nil {
		return nil, mapErr(err)
	}
	return &pb.ReleaseResponse{}, nil
}
