// Unit tests for profile, derived from SPEC.yaml.
//
// What this service is:
//   profile serves hotel metadata. v1 is in-memory: UpsertProfile seeds a
//   record; GetProfile returns it. Stateless across restarts.
//
// Test contract assumed about the implementation:
//   - `server.NewServer()` returns a *Server implementing pb.ProfileServiceServer.
//   - Standard gRPC signatures; errors via google.golang.org/grpc/status with
//     the codes in SPEC.error_semantics.
//
// Authored out-of-band and audited before the agent implements. The agent
// must not modify this file.

package server_test

import (
	"context"
	"testing"

	pb "agentbench/services/profile/api/v1"
	"agentbench/services/profile/internal/server"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func wantCode(t *testing.T, err error, want codes.Code) {
	t.Helper()
	if got := status.Code(err); got != want {
		t.Errorf("status code = %v, want %v (err=%v)", got, want, err)
	}
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ════════════════════════════════════════════════════════════════════════════
// GetProfile / UpsertProfile
// ════════════════════════════════════════════════════════════════════════════

// Round-trip: a seeded profile is returned field-for-field by GetProfile.
func TestGetProfile_Happy(t *testing.T) {
	s := server.NewServer()
	_, err := s.UpsertProfile(context.Background(), &pb.UpsertProfileRequest{
		HotelId:     "H1",
		Name:        "Grand Hotel",
		Address:     "1 Main St",
		Description: "A fine hotel.",
		Amenities:   []string{"wifi", "pool"},
		RoomTypes:   []string{"STD", "DLX"},
	})
	if err != nil {
		t.Fatalf("UpsertProfile: %v", err)
	}

	got, err := s.GetProfile(context.Background(), &pb.GetProfileRequest{HotelId: "H1"})
	if err != nil {
		t.Fatalf("GetProfile: %v", err)
	}
	if got.GetHotelId() != "H1" || got.GetName() != "Grand Hotel" ||
		got.GetAddress() != "1 Main St" || got.GetDescription() != "A fine hotel." {
		t.Errorf("scalar fields mismatch: %+v", got)
	}
	if !sameStrings(got.GetAmenities(), []string{"wifi", "pool"}) {
		t.Errorf("amenities = %v, want [wifi pool]", got.GetAmenities())
	}
	if !sameStrings(got.GetRoomTypes(), []string{"STD", "DLX"}) {
		t.Errorf("room_types = %v, want [STD DLX]", got.GetRoomTypes())
	}
}

// GetProfile for an unknown hotel is NotFound.
func TestGetProfile_NotFound(t *testing.T) {
	s := server.NewServer()
	_, err := s.GetProfile(context.Background(), &pb.GetProfileRequest{HotelId: "nope"})
	wantCode(t, err, codes.NotFound)
}

// Re-upserting overwrites: GetProfile returns the latest values.
func TestUpsertProfile_Updates(t *testing.T) {
	s := server.NewServer()
	ctx := context.Background()
	if _, err := s.UpsertProfile(ctx, &pb.UpsertProfileRequest{HotelId: "H1", Name: "Old"}); err != nil {
		t.Fatalf("first UpsertProfile: %v", err)
	}
	if _, err := s.UpsertProfile(ctx, &pb.UpsertProfileRequest{HotelId: "H1", Name: "New"}); err != nil {
		t.Fatalf("second UpsertProfile: %v", err)
	}
	got, err := s.GetProfile(ctx, &pb.GetProfileRequest{HotelId: "H1"})
	if err != nil {
		t.Fatalf("GetProfile: %v", err)
	}
	if got.GetName() != "New" {
		t.Errorf("name = %q, want New (overwrite)", got.GetName())
	}
}

// Empty hotel_id is InvalidArgument.
func TestUpsertProfile_EmptyID(t *testing.T) {
	s := server.NewServer()
	_, err := s.UpsertProfile(context.Background(), &pb.UpsertProfileRequest{HotelId: "", Name: "X"})
	wantCode(t, err, codes.InvalidArgument)
}

// Empty name is InvalidArgument.
func TestUpsertProfile_EmptyName(t *testing.T) {
	s := server.NewServer()
	_, err := s.UpsertProfile(context.Background(), &pb.UpsertProfileRequest{HotelId: "H1", Name: ""})
	wantCode(t, err, codes.InvalidArgument)
}
