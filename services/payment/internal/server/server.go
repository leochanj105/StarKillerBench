package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"

	pb "agentbench/services/payment/internal/genpb/v1"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	statusAuthorized = "authorized"
	statusCaptured   = "captured"
	statusVoided     = "voided"
)

type authorization struct {
	amount   int64
	currency string
	status   string
	refunded int64
}

type Server struct {
	pb.UnimplementedPaymentServiceServer

	mu    sync.Mutex
	auths map[string]*authorization
}

func NewServer() *Server {
	return &Server{auths: make(map[string]*authorization)}
}

func newAuthID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

func (s *Server) Authorize(_ context.Context, req *pb.AuthorizeRequest) (*pb.AuthorizeResponse, error) {
	if req.GetAmount() <= 0 || req.GetCurrency() == "" {
		return nil, status.Error(codes.InvalidArgument, "amount must be positive and currency must be non-empty")
	}
	id := newAuthID()
	s.mu.Lock()
	s.auths[id] = &authorization{
		amount:   req.GetAmount(),
		currency: req.GetCurrency(),
		status:   statusAuthorized,
	}
	s.mu.Unlock()
	return &pb.AuthorizeResponse{AuthId: id}, nil
}

func (s *Server) Capture(_ context.Context, req *pb.CaptureRequest) (*pb.CaptureResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.auths[req.GetAuthId()]
	if !ok {
		return nil, status.Error(codes.NotFound, "unknown auth_id")
	}
	switch a.status {
	case statusCaptured:
		return &pb.CaptureResponse{}, nil
	case statusAuthorized:
		a.status = statusCaptured
		return &pb.CaptureResponse{}, nil
	default:
		return nil, status.Errorf(codes.FailedPrecondition, "cannot capture auth in state %q", a.status)
	}
}

func (s *Server) Void(_ context.Context, req *pb.VoidRequest) (*pb.VoidResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.auths[req.GetAuthId()]
	if !ok {
		return nil, status.Error(codes.NotFound, "unknown auth_id")
	}
	switch a.status {
	case statusVoided:
		return &pb.VoidResponse{}, nil
	case statusAuthorized:
		a.status = statusVoided
		return &pb.VoidResponse{}, nil
	default:
		return nil, status.Errorf(codes.FailedPrecondition, "cannot void auth in state %q", a.status)
	}
}

func (s *Server) Refund(_ context.Context, req *pb.RefundRequest) (*pb.RefundResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.auths[req.GetAuthId()]
	if !ok {
		return nil, status.Error(codes.NotFound, "unknown auth_id")
	}
	if a.status != statusCaptured {
		return nil, status.Errorf(codes.FailedPrecondition, "cannot refund auth in state %q", a.status)
	}
	if req.GetAmount() <= 0 {
		return nil, status.Error(codes.FailedPrecondition, "refund amount must be positive")
	}
	remaining := a.amount - a.refunded
	if req.GetAmount() > remaining {
		return nil, status.Errorf(codes.FailedPrecondition, "refund amount %d exceeds remaining %d", req.GetAmount(), remaining)
	}
	a.refunded += req.GetAmount()
	return &pb.RefundResponse{}, nil
}
