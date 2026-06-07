// Unit tests for geo, derived from SPEC.yaml.
//
// What this service is:
//   geo answers "hotels near (lat, lng) within radius_km". v1 is in-memory:
//   hotels are seeded via UpsertHotel; Nearby does a haversine scan. Returns
//   hotel_ids within the radius.
//
// Test contract assumed about the implementation:
//   - `server.NewServer()` returns a *Server implementing pb.GeoServiceServer.
//   - Standard gRPC signatures; errors via google.golang.org/grpc/status with
//     the codes in SPEC.error_semantics.
//
// Distances used below rely on the fact that one degree of latitude is
// ~111 km. A hotel one degree of latitude away (~111 km) is outside a 50 km
// radius and inside a 200 km radius — robust to the exact haversine constant.
//
// Authored out-of-band and audited before the agent implements. The agent
// must not modify this file.

package server_test

import (
	"context"
	"testing"

	pb "agentbench/services/geo/api/v1"
	"agentbench/services/geo/internal/server"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func upsertOK(t *testing.T, s pb.GeoServiceServer, id string, lat, lng float64) {
	t.Helper()
	if _, err := s.UpsertHotel(context.Background(), &pb.UpsertHotelRequest{
		HotelId: id, Lat: lat, Lng: lng,
	}); err != nil {
		t.Fatalf("UpsertHotel(%s): %v", id, err)
	}
}

func nearby(t *testing.T, s pb.GeoServiceServer, lat, lng, radiusKm float64) []string {
	t.Helper()
	r, err := s.Nearby(context.Background(), &pb.NearbyRequest{Lat: lat, Lng: lng, RadiusKm: radiusKm})
	if err != nil {
		t.Fatalf("Nearby(%v,%v,%v): %v", lat, lng, radiusKm, err)
	}
	return r.GetHotelIds()
}

func contains(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func wantCode(t *testing.T, err error, want codes.Code) {
	t.Helper()
	if got := status.Code(err); got != want {
		t.Errorf("status code = %v, want %v (err=%v)", got, want, err)
	}
}

// ════════════════════════════════════════════════════════════════════════════
// Nearby
// ════════════════════════════════════════════════════════════════════════════

// A hotel at the query point is well within any positive radius.
func TestNearby_IncludesColocated(t *testing.T) {
	s := server.NewServer()
	upsertOK(t, s, "H1", 0, 0)
	if !contains(nearby(t, s, 0, 0, 10), "H1") {
		t.Errorf("H1 at the query point should be within 10km")
	}
}

// A hotel ~111 km north (one degree of latitude) is outside a 50 km radius
// and inside a 200 km radius.
func TestNearby_RadiusBoundary(t *testing.T) {
	s := server.NewServer()
	upsertOK(t, s, "FAR", 1.0, 0) // ~111 km from (0,0)

	if contains(nearby(t, s, 0, 0, 50), "FAR") {
		t.Errorf("FAR (~111km) should be outside a 50km radius")
	}
	if !contains(nearby(t, s, 0, 0, 200), "FAR") {
		t.Errorf("FAR (~111km) should be inside a 200km radius")
	}
}

// No seeded hotels → empty result, not an error.
func TestNearby_Empty(t *testing.T) {
	s := server.NewServer()
	if got := nearby(t, s, 0, 0, 100); len(got) != 0 {
		t.Errorf("expected empty result, got %v", got)
	}
}

// Re-upserting a hotel moves it: after moving H1 far away, it drops out of a
// small radius around its old location.
func TestUpsertHotel_Updates(t *testing.T) {
	s := server.NewServer()
	upsertOK(t, s, "H1", 0, 0)
	upsertOK(t, s, "H1", 10, 10) // move ~1500 km away
	if contains(nearby(t, s, 0, 0, 50), "H1") {
		t.Errorf("H1 was moved away; should no longer be near (0,0)")
	}
}

// Non-positive radius is InvalidArgument.
func TestNearby_InvalidRadius(t *testing.T) {
	s := server.NewServer()
	for _, r := range []float64{0, -1} {
		_, err := s.Nearby(context.Background(), &pb.NearbyRequest{Lat: 0, Lng: 0, RadiusKm: r})
		wantCode(t, err, codes.InvalidArgument)
	}
}

// Out-of-range query coordinates are InvalidArgument.
func TestNearby_InvalidCoords(t *testing.T) {
	s := server.NewServer()
	_, err := s.Nearby(context.Background(), &pb.NearbyRequest{Lat: 91, Lng: 0, RadiusKm: 10})
	wantCode(t, err, codes.InvalidArgument)
}

// ════════════════════════════════════════════════════════════════════════════
// UpsertHotel
// ════════════════════════════════════════════════════════════════════════════

// Empty hotel_id is InvalidArgument.
func TestUpsertHotel_EmptyID(t *testing.T) {
	s := server.NewServer()
	_, err := s.UpsertHotel(context.Background(), &pb.UpsertHotelRequest{HotelId: "", Lat: 0, Lng: 0})
	wantCode(t, err, codes.InvalidArgument)
}

// Out-of-range coordinates are InvalidArgument.
func TestUpsertHotel_InvalidCoords(t *testing.T) {
	s := server.NewServer()
	cases := []struct{ lat, lng float64 }{
		{91, 0}, {-91, 0}, {0, 181}, {0, -181},
	}
	for _, c := range cases {
		_, err := s.UpsertHotel(context.Background(), &pb.UpsertHotelRequest{
			HotelId: "H1", Lat: c.lat, Lng: c.lng,
		})
		wantCode(t, err, codes.InvalidArgument)
	}
}
