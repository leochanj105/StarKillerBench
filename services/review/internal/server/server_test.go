// Unit tests for review, derived from SPEC.yaml.
//
// What this service is:
//   review stores hotel reviews (in-memory v1), serves them paginated, and
//   computes a per-hotel rating aggregate on demand.
//
// Test contract assumed about the implementation:
//   - `server.NewServer()` returns a *Server implementing pb.ReviewServiceServer.
//   - Standard gRPC signatures; errors via google.golang.org/grpc/status with
//     the codes in SPEC.error_semantics.
//
// Authored out-of-band and audited before the agent implements. The agent
// must not modify this file.

package server_test

import (
	"context"
	"math"
	"testing"

	pb "agentbench/services/review/api/v1"
	"agentbench/services/review/internal/server"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func postOK(t *testing.T, s pb.ReviewServiceServer, hotel, user string, rating int32) {
	t.Helper()
	r, err := s.PostReview(context.Background(), &pb.PostReviewRequest{
		HotelId: hotel, UserId: user, Rating: rating, Text: "ok",
	})
	if err != nil {
		t.Fatalf("PostReview(%s,%d): %v", hotel, rating, err)
	}
	if r.GetReviewId() == "" {
		t.Fatalf("PostReview returned empty review_id")
	}
}

func wantCode(t *testing.T, err error, want codes.Code) {
	t.Helper()
	if got := status.Code(err); got != want {
		t.Errorf("status code = %v, want %v (err=%v)", got, want, err)
	}
}

// ════════════════════════════════════════════════════════════════════════════
// PostReview / ListReviews
// ════════════════════════════════════════════════════════════════════════════

// Posted reviews are listed back for the hotel with total_count.
func TestListReviews_Happy(t *testing.T) {
	s := server.NewServer()
	postOK(t, s, "H1", "u-1", 4)
	postOK(t, s, "H1", "u-2", 5)

	r, err := s.ListReviews(context.Background(), &pb.ListReviewsRequest{HotelId: "H1", Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("ListReviews: %v", err)
	}
	if r.GetTotalCount() != 2 {
		t.Errorf("total_count = %d, want 2", r.GetTotalCount())
	}
	if len(r.GetReviews()) != 2 {
		t.Errorf("page len = %d, want 2", len(r.GetReviews()))
	}
}

// Pagination: 3 reviews, page_size 2 → page 1 has 2, page 2 has 1.
func TestListReviews_Paginates(t *testing.T) {
	s := server.NewServer()
	postOK(t, s, "H1", "u-1", 3)
	postOK(t, s, "H1", "u-2", 4)
	postOK(t, s, "H1", "u-3", 5)

	p1, err := s.ListReviews(context.Background(), &pb.ListReviewsRequest{HotelId: "H1", Page: 1, PageSize: 2})
	if err != nil {
		t.Fatalf("ListReviews p1: %v", err)
	}
	if len(p1.GetReviews()) != 2 || p1.GetTotalCount() != 3 {
		t.Errorf("page1 len=%d total=%d, want 2 and 3", len(p1.GetReviews()), p1.GetTotalCount())
	}
	p2, err := s.ListReviews(context.Background(), &pb.ListReviewsRequest{HotelId: "H1", Page: 2, PageSize: 2})
	if err != nil {
		t.Fatalf("ListReviews p2: %v", err)
	}
	if len(p2.GetReviews()) != 1 {
		t.Errorf("page2 len = %d, want 1", len(p2.GetReviews()))
	}
}

// An unknown hotel yields an empty page, not an error.
func TestListReviews_UnknownHotel(t *testing.T) {
	s := server.NewServer()
	r, err := s.ListReviews(context.Background(), &pb.ListReviewsRequest{HotelId: "nope", Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("ListReviews: %v", err)
	}
	if r.GetTotalCount() != 0 || len(r.GetReviews()) != 0 {
		t.Errorf("unknown hotel should yield empty page, got total=%d len=%d", r.GetTotalCount(), len(r.GetReviews()))
	}
}

// Bad rating (0 or 6) is InvalidArgument; bad pagination is InvalidArgument.
func TestReview_InvalidArgs(t *testing.T) {
	s := server.NewServer()
	for _, rating := range []int32{0, 6, -1} {
		_, err := s.PostReview(context.Background(), &pb.PostReviewRequest{HotelId: "H1", UserId: "u", Rating: rating, Text: "x"})
		wantCode(t, err, codes.InvalidArgument)
	}
	_, err := s.PostReview(context.Background(), &pb.PostReviewRequest{HotelId: "", UserId: "u", Rating: 5})
	wantCode(t, err, codes.InvalidArgument)

	_, err = s.ListReviews(context.Background(), &pb.ListReviewsRequest{HotelId: "H1", Page: 0, PageSize: 10})
	wantCode(t, err, codes.InvalidArgument)
	_, err = s.ListReviews(context.Background(), &pb.ListReviewsRequest{HotelId: "H1", Page: 1, PageSize: 0})
	wantCode(t, err, codes.InvalidArgument)
}

// ════════════════════════════════════════════════════════════════════════════
// GetAggregate
// ════════════════════════════════════════════════════════════════════════════

// Aggregate reports count and mean rating: ratings 4,5,3 → count 3, avg 4.0.
func TestGetAggregate(t *testing.T) {
	s := server.NewServer()
	postOK(t, s, "H1", "u-1", 4)
	postOK(t, s, "H1", "u-2", 5)
	postOK(t, s, "H1", "u-3", 3)

	a, err := s.GetAggregate(context.Background(), &pb.GetAggregateRequest{HotelId: "H1"})
	if err != nil {
		t.Fatalf("GetAggregate: %v", err)
	}
	if a.GetReviewCount() != 3 {
		t.Errorf("review_count = %d, want 3", a.GetReviewCount())
	}
	if math.Abs(a.GetAvgRating()-4.0) > 0.001 {
		t.Errorf("avg_rating = %v, want ~4.0", a.GetAvgRating())
	}
}

// A hotel with no reviews aggregates to count 0, avg 0.
func TestGetAggregate_Empty(t *testing.T) {
	s := server.NewServer()
	a, err := s.GetAggregate(context.Background(), &pb.GetAggregateRequest{HotelId: "nope"})
	if err != nil {
		t.Fatalf("GetAggregate: %v", err)
	}
	if a.GetReviewCount() != 0 || a.GetAvgRating() != 0 {
		t.Errorf("empty aggregate = (count %d, avg %v), want (0, 0)", a.GetReviewCount(), a.GetAvgRating())
	}
}
