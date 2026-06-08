// Package server implements the ReviewService gRPC handlers.
//
// Storage is in-memory (v1): a map from hotel_id to that hotel's reviews in
// post order. The rating aggregate is computed synchronously on read.
package server

import (
	"context"
	"fmt"
	"sync"

	pb "agentbench/services/review/api/v1"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Server holds the in-memory review store.
type Server struct {
	pb.UnimplementedReviewServiceServer

	mu      sync.Mutex
	reviews map[string][]*pb.Review // hotel_id -> reviews in post order
	seq     int64                   // monotonic counter for review ids
}

// NewServer returns a ready-to-serve Server.
func NewServer() *Server {
	return &Server{reviews: make(map[string][]*pb.Review)}
}

// PostReview appends a review for a hotel and returns its generated review_id.
func (s *Server) PostReview(ctx context.Context, req *pb.PostReviewRequest) (*pb.PostReviewResponse, error) {
	if req.GetHotelId() == "" || req.GetUserId() == "" {
		return nil, status.Error(codes.InvalidArgument, "hotel_id and user_id are required")
	}
	if req.GetRating() < 1 || req.GetRating() > 5 {
		return nil, status.Errorf(codes.InvalidArgument, "rating must be in [1, 5], got %d", req.GetRating())
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.seq++
	reviewID := fmt.Sprintf("rv-%d", s.seq)
	r := &pb.Review{
		ReviewId: reviewID,
		HotelId:  req.GetHotelId(),
		UserId:   req.GetUserId(),
		Rating:   req.GetRating(),
		Text:     req.GetText(),
	}
	s.reviews[req.GetHotelId()] = append(s.reviews[req.GetHotelId()], r)

	return &pb.PostReviewResponse{ReviewId: reviewID}, nil
}

// ListReviews returns one page (1-based) of a hotel's reviews plus the hotel's
// total review count. An unknown hotel yields an empty page with total_count 0.
func (s *Server) ListReviews(ctx context.Context, req *pb.ListReviewsRequest) (*pb.ListReviewsResponse, error) {
	if req.GetPage() < 1 || req.GetPageSize() < 1 {
		return nil, status.Error(codes.InvalidArgument, "page and page_size must be positive")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	all := s.reviews[req.GetHotelId()]
	total := int32(len(all))

	start := (req.GetPage() - 1) * req.GetPageSize()
	if start >= total {
		return &pb.ListReviewsResponse{Reviews: nil, TotalCount: total}, nil
	}
	end := start + req.GetPageSize()
	if end > total {
		end = total
	}

	page := make([]*pb.Review, end-start)
	copy(page, all[start:end])

	return &pb.ListReviewsResponse{Reviews: page, TotalCount: total}, nil
}

// GetAggregate returns the number of reviews and their mean rating for a hotel.
// A hotel with no reviews yields review_count 0 and avg_rating 0.
func (s *Server) GetAggregate(ctx context.Context, req *pb.GetAggregateRequest) (*pb.GetAggregateResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	all := s.reviews[req.GetHotelId()]
	count := int32(len(all))
	if count == 0 {
		return &pb.GetAggregateResponse{ReviewCount: 0, AvgRating: 0}, nil
	}

	var sum int64
	for _, r := range all {
		sum += int64(r.GetRating())
	}
	avg := float64(sum) / float64(count)

	return &pb.GetAggregateResponse{ReviewCount: count, AvgRating: avg}, nil
}
