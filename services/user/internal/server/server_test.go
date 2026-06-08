// Unit tests for user, derived from SPEC.yaml.
//
// What this service is:
//   user owns preferences + a loyalty points balance. v1 is in-memory. The
//   load-bearing behavior is AccrueOnBooking: a read-modify-write on the
//   balance that must serialize concurrent accruals for the same user.
//
// Test contract assumed about the implementation:
//   - `server.NewServer()` returns a *Server implementing pb.UserServiceServer.
//   - Standard gRPC signatures; errors via google.golang.org/grpc/status with
//     the codes in SPEC.error_semantics.
//
// Authored out-of-band and audited before the agent implements. The agent
// must not modify this file.

package server_test

import (
	"context"
	"sync"
	"testing"

	pb "agentbench/services/user/api/v1"
	"agentbench/services/user/internal/server"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func createUserOK(t *testing.T, s pb.UserServiceServer, id string) {
	t.Helper()
	if _, err := s.CreateUser(context.Background(), &pb.CreateUserRequest{UserId: id}); err != nil {
		t.Fatalf("CreateUser(%s): %v", id, err)
	}
}

func wantCode(t *testing.T, err error, want codes.Code) {
	t.Helper()
	if got := status.Code(err); got != want {
		t.Errorf("status code = %v, want %v (err=%v)", got, want, err)
	}
}

// ════════════════════════════════════════════════════════════════════════════
// CreateUser / GetUser / UpdatePreferences
// ════════════════════════════════════════════════════════════════════════════

// A new user starts with zero points and is readable.
func TestCreateUser_StartsEmpty(t *testing.T) {
	s := server.NewServer()
	createUserOK(t, s, "u-1")
	got, err := s.GetUser(context.Background(), &pb.GetUserRequest{UserId: "u-1"})
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if got.GetUserId() != "u-1" || got.GetPoints() != 0 {
		t.Errorf("new user = %+v, want user_id u-1 and 0 points", got)
	}
}

// Duplicate CreateUser is AlreadyExists.
func TestCreateUser_Duplicate(t *testing.T) {
	s := server.NewServer()
	createUserOK(t, s, "u-1")
	_, err := s.CreateUser(context.Background(), &pb.CreateUserRequest{UserId: "u-1"})
	wantCode(t, err, codes.AlreadyExists)
}

// GetUser on an unknown user is NotFound.
func TestGetUser_NotFound(t *testing.T) {
	s := server.NewServer()
	_, err := s.GetUser(context.Background(), &pb.GetUserRequest{UserId: "ghost"})
	wantCode(t, err, codes.NotFound)
}

// UpdatePreferences is reflected by GetUser.
func TestUpdatePreferences(t *testing.T) {
	s := server.NewServer()
	createUserOK(t, s, "u-1")
	if _, err := s.UpdatePreferences(context.Background(), &pb.UpdatePreferencesRequest{
		UserId: "u-1", Currency: "EUR", Locale: "fr-FR",
	}); err != nil {
		t.Fatalf("UpdatePreferences: %v", err)
	}
	got, err := s.GetUser(context.Background(), &pb.GetUserRequest{UserId: "u-1"})
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if got.GetCurrency() != "EUR" || got.GetLocale() != "fr-FR" {
		t.Errorf("prefs = (%q,%q), want (EUR,fr-FR)", got.GetCurrency(), got.GetLocale())
	}
}

// UpdatePreferences on an unknown user is NotFound.
func TestUpdatePreferences_NotFound(t *testing.T) {
	s := server.NewServer()
	_, err := s.UpdatePreferences(context.Background(), &pb.UpdatePreferencesRequest{UserId: "ghost", Currency: "USD"})
	wantCode(t, err, codes.NotFound)
}

// ════════════════════════════════════════════════════════════════════════════
// Loyalty points
// ════════════════════════════════════════════════════════════════════════════

// Accrual adds amount/100 points and GetPoints reflects the running total.
func TestAccrue_AddsPoints(t *testing.T) {
	s := server.NewServer()
	ctx := context.Background()
	createUserOK(t, s, "u-1")

	r, err := s.AccrueOnBooking(ctx, &pb.AccrueOnBookingRequest{UserId: "u-1", Amount: 10000})
	if err != nil {
		t.Fatalf("AccrueOnBooking: %v", err)
	}
	if r.GetPoints() != 100 {
		t.Errorf("points after 10000 = %d, want 100", r.GetPoints())
	}
	r, err = s.AccrueOnBooking(ctx, &pb.AccrueOnBookingRequest{UserId: "u-1", Amount: 5000})
	if err != nil {
		t.Fatalf("AccrueOnBooking #2: %v", err)
	}
	if r.GetPoints() != 150 {
		t.Errorf("points after +5000 = %d, want 150", r.GetPoints())
	}
	gp, _ := s.GetPoints(ctx, &pb.GetPointsRequest{UserId: "u-1"})
	if gp.GetPoints() != 150 {
		t.Errorf("GetPoints = %d, want 150", gp.GetPoints())
	}
}

// Concurrent accruals for the same user must not lose updates: 50 goroutines
// each accruing 1000 (→ 10 points) must leave exactly 500 points. A
// non-atomic RMW would drop some increments and undershoot.
func TestAccrue_ConcurrentNoLostUpdates(t *testing.T) {
	s := server.NewServer()
	ctx := context.Background()
	createUserOK(t, s, "u-1")

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			_, _ = s.AccrueOnBooking(ctx, &pb.AccrueOnBookingRequest{UserId: "u-1", Amount: 1000})
		}()
	}
	wg.Wait()

	gp, err := s.GetPoints(ctx, &pb.GetPointsRequest{UserId: "u-1"})
	if err != nil {
		t.Fatalf("GetPoints: %v", err)
	}
	if gp.GetPoints() != goroutines*10 {
		t.Errorf("points = %d, want %d (lost updates under concurrency)", gp.GetPoints(), goroutines*10)
	}
}

// Accrual on an unknown user is NotFound; a non-positive amount is
// InvalidArgument.
func TestAccrue_Errors(t *testing.T) {
	s := server.NewServer()
	ctx := context.Background()
	createUserOK(t, s, "u-1")

	_, err := s.AccrueOnBooking(ctx, &pb.AccrueOnBookingRequest{UserId: "ghost", Amount: 100})
	wantCode(t, err, codes.NotFound)

	_, err = s.AccrueOnBooking(ctx, &pb.AccrueOnBookingRequest{UserId: "u-1", Amount: 0})
	wantCode(t, err, codes.InvalidArgument)
}
