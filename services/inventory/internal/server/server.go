package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"

	pb "agentbench/services/inventory/api/v1"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type stockRecord struct {
	total int32
	sold  int32
	held  int32
}

const (
	holdStatusHeld      = "held"
	holdStatusCommitted = "committed"
	holdStatusReleased  = "released"
)

type holdRecord struct {
	stockKey string
	quantity int32
	status   string
}

type Server struct {
	pb.UnimplementedInventoryServiceServer

	mu     sync.Mutex
	stock  map[string]*stockRecord
	holds  map[string]*holdRecord
}

func NewServer() *Server {
	return &Server{
		stock: make(map[string]*stockRecord),
		holds: make(map[string]*holdRecord),
	}
}

func stockKey(hotelID, roomType, date string) string {
	return fmt.Sprintf("%s|%s|%s", hotelID, roomType, date)
}

func newHoldID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

func (s *Server) SetStock(ctx context.Context, req *pb.SetStockRequest) (*pb.SetStockResponse, error) {
	if req.GetQuantity() < 0 {
		return nil, status.Error(codes.InvalidArgument, "quantity must be non-negative")
	}
	key := stockKey(req.GetHotelId(), req.GetRoomType(), req.GetDate())

	s.mu.Lock()
	defer s.mu.Unlock()

	rec, ok := s.stock[key]
	if !ok {
		s.stock[key] = &stockRecord{total: req.GetQuantity()}
	} else {
		rec.total = req.GetQuantity()
	}
	return &pb.SetStockResponse{}, nil
}

func (s *Server) Hold(ctx context.Context, req *pb.HoldRequest) (*pb.HoldResponse, error) {
	if req.GetQuantity() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "quantity must be positive")
	}
	key := stockKey(req.GetHotelId(), req.GetRoomType(), req.GetDate())

	s.mu.Lock()
	defer s.mu.Unlock()

	rec, ok := s.stock[key]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "no stock for %s", key)
	}
	available := rec.total - rec.sold - rec.held
	if available < req.GetQuantity() {
		return nil, status.Errorf(codes.FailedPrecondition, "insufficient stock: available=%d requested=%d", available, req.GetQuantity())
	}
	rec.held += req.GetQuantity()

	id := newHoldID()
	s.holds[id] = &holdRecord{
		stockKey: key,
		quantity: req.GetQuantity(),
		status:   holdStatusHeld,
	}
	return &pb.HoldResponse{HoldId: id}, nil
}

func (s *Server) Commit(ctx context.Context, req *pb.CommitRequest) (*pb.CommitResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	h, ok := s.holds[req.GetHoldId()]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "unknown hold_id")
	}
	if h.status != holdStatusHeld {
		return nil, status.Errorf(codes.FailedPrecondition, "hold already %s", h.status)
	}
	rec, ok := s.stock[h.stockKey]
	if !ok {
		return nil, status.Errorf(codes.Internal, "stock for hold missing")
	}
	rec.held -= h.quantity
	rec.sold += h.quantity
	h.status = holdStatusCommitted
	return &pb.CommitResponse{}, nil
}

func (s *Server) Release(ctx context.Context, req *pb.ReleaseRequest) (*pb.ReleaseResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	h, ok := s.holds[req.GetHoldId()]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "unknown hold_id")
	}
	if h.status != holdStatusHeld {
		return nil, status.Errorf(codes.FailedPrecondition, "hold already %s", h.status)
	}
	rec, ok := s.stock[h.stockKey]
	if !ok {
		return nil, status.Errorf(codes.Internal, "stock for hold missing")
	}
	rec.held -= h.quantity
	h.status = holdStatusReleased
	return &pb.ReleaseResponse{}, nil
}
