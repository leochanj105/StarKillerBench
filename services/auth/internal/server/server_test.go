// Unit tests for auth, derived from SPEC.yaml.
//
// What this service is:
//   auth issues and verifies tokens. v1 is in-memory: Register stores a
//   salted password hash; Login verifies it and issues a JWT access token +
//   an opaque refresh token; VerifyToken validates the JWT; Refresh mints a
//   new access token; Logout revokes a refresh session.
//
// Test contract assumed about the implementation:
//   - `server.NewServer()` returns a *Server implementing pb.AuthServiceServer.
//   - Standard gRPC signatures; errors via google.golang.org/grpc/status with
//     the codes in SPEC.error_semantics.
//
// Tokens are opaque to these tests — they exercise round-trips
// (register → login → verify / refresh / logout) rather than inspecting token
// internals.
//
// Authored out-of-band and audited before the agent implements. The agent
// must not modify this file.

package server_test

import (
	"context"
	"testing"

	pb "agentbench/services/auth/api/v1"
	"agentbench/services/auth/internal/server"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func registerOK(t *testing.T, s pb.AuthServiceServer, email, pw string) {
	t.Helper()
	if _, err := s.Register(context.Background(), &pb.RegisterRequest{Email: email, Password: pw}); err != nil {
		t.Fatalf("Register(%s): %v", email, err)
	}
}

// loginOK logs in and returns (access_token, refresh_token), both asserted
// non-empty.
func loginOK(t *testing.T, s pb.AuthServiceServer, email, pw string) (string, string) {
	t.Helper()
	r, err := s.Login(context.Background(), &pb.LoginRequest{Email: email, Password: pw})
	if err != nil {
		t.Fatalf("Login(%s): %v", email, err)
	}
	if r.GetAccessToken() == "" || r.GetRefreshToken() == "" {
		t.Fatalf("Login returned empty tokens: access=%q refresh=%q", r.GetAccessToken(), r.GetRefreshToken())
	}
	return r.GetAccessToken(), r.GetRefreshToken()
}

func wantCode(t *testing.T, err error, want codes.Code) {
	t.Helper()
	if got := status.Code(err); got != want {
		t.Errorf("status code = %v, want %v (err=%v)", got, want, err)
	}
}

// ════════════════════════════════════════════════════════════════════════════
// Register / Login
// ════════════════════════════════════════════════════════════════════════════

// Register then Login succeeds and yields non-empty tokens.
func TestRegisterThenLogin(t *testing.T) {
	s := server.NewServer()
	registerOK(t, s, "a@x.com", "pw")
	loginOK(t, s, "a@x.com", "pw")
}

// Registering the same email twice is AlreadyExists.
func TestRegister_Duplicate(t *testing.T) {
	s := server.NewServer()
	registerOK(t, s, "a@x.com", "pw")
	_, err := s.Register(context.Background(), &pb.RegisterRequest{Email: "a@x.com", Password: "other"})
	wantCode(t, err, codes.AlreadyExists)
}

// Empty email or password is InvalidArgument.
func TestRegister_InvalidArgument(t *testing.T) {
	s := server.NewServer()
	_, err := s.Register(context.Background(), &pb.RegisterRequest{Email: "", Password: "pw"})
	wantCode(t, err, codes.InvalidArgument)
	_, err = s.Register(context.Background(), &pb.RegisterRequest{Email: "a@x.com", Password: ""})
	wantCode(t, err, codes.InvalidArgument)
}

// Wrong password is Unauthenticated.
func TestLogin_WrongPassword(t *testing.T) {
	s := server.NewServer()
	registerOK(t, s, "a@x.com", "pw")
	_, err := s.Login(context.Background(), &pb.LoginRequest{Email: "a@x.com", Password: "nope"})
	wantCode(t, err, codes.Unauthenticated)
}

// Unknown email is Unauthenticated (does not leak that the email is unknown
// via a different code).
func TestLogin_UnknownEmail(t *testing.T) {
	s := server.NewServer()
	_, err := s.Login(context.Background(), &pb.LoginRequest{Email: "ghost@x.com", Password: "pw"})
	wantCode(t, err, codes.Unauthenticated)
}

// ════════════════════════════════════════════════════════════════════════════
// VerifyToken
// ════════════════════════════════════════════════════════════════════════════

// A freshly issued access token verifies and returns the user's claims with
// the guest scope.
func TestVerifyToken_Happy(t *testing.T) {
	s := server.NewServer()
	registerOK(t, s, "a@x.com", "pw")
	access, _ := loginOK(t, s, "a@x.com", "pw")

	claims, err := s.VerifyToken(context.Background(), &pb.VerifyTokenRequest{AccessToken: access})
	if err != nil {
		t.Fatalf("VerifyToken: %v", err)
	}
	if claims.GetUserId() == "" {
		t.Errorf("user_id empty")
	}
	if claims.GetScope() != "guest" {
		t.Errorf("scope = %q, want guest", claims.GetScope())
	}
}

// Distinct users verify to distinct user_ids.
func TestVerifyToken_DistinctUsers(t *testing.T) {
	s := server.NewServer()
	registerOK(t, s, "a@x.com", "pw")
	registerOK(t, s, "b@x.com", "pw")
	at1, _ := loginOK(t, s, "a@x.com", "pw")
	at2, _ := loginOK(t, s, "b@x.com", "pw")

	c1, err := s.VerifyToken(context.Background(), &pb.VerifyTokenRequest{AccessToken: at1})
	if err != nil {
		t.Fatalf("VerifyToken a: %v", err)
	}
	c2, err := s.VerifyToken(context.Background(), &pb.VerifyTokenRequest{AccessToken: at2})
	if err != nil {
		t.Fatalf("VerifyToken b: %v", err)
	}
	if c1.GetUserId() == c2.GetUserId() {
		t.Errorf("distinct users share a user_id: %q", c1.GetUserId())
	}
}

// A garbage / unsigned token is Unauthenticated.
func TestVerifyToken_Invalid(t *testing.T) {
	s := server.NewServer()
	_, err := s.VerifyToken(context.Background(), &pb.VerifyTokenRequest{AccessToken: "not.a.jwt"})
	wantCode(t, err, codes.Unauthenticated)
}

// Empty access token is InvalidArgument.
func TestVerifyToken_Empty(t *testing.T) {
	s := server.NewServer()
	_, err := s.VerifyToken(context.Background(), &pb.VerifyTokenRequest{AccessToken: ""})
	wantCode(t, err, codes.InvalidArgument)
}

// ════════════════════════════════════════════════════════════════════════════
// Refresh / Logout
// ════════════════════════════════════════════════════════════════════════════

// A valid refresh token mints a new access token that itself verifies.
func TestRefresh_Happy(t *testing.T) {
	s := server.NewServer()
	registerOK(t, s, "a@x.com", "pw")
	_, refresh := loginOK(t, s, "a@x.com", "pw")

	r, err := s.Refresh(context.Background(), &pb.RefreshRequest{RefreshToken: refresh})
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if r.GetAccessToken() == "" {
		t.Fatalf("Refresh returned empty access token")
	}
	if _, err := s.VerifyToken(context.Background(), &pb.VerifyTokenRequest{AccessToken: r.GetAccessToken()}); err != nil {
		t.Errorf("refreshed access token failed to verify: %v", err)
	}
}

// An unknown refresh token is Unauthenticated.
func TestRefresh_Unknown(t *testing.T) {
	s := server.NewServer()
	_, err := s.Refresh(context.Background(), &pb.RefreshRequest{RefreshToken: "nope"})
	wantCode(t, err, codes.Unauthenticated)
}

// After Logout, the refresh token can no longer be refreshed.
func TestLogout_RevokesRefresh(t *testing.T) {
	s := server.NewServer()
	registerOK(t, s, "a@x.com", "pw")
	_, refresh := loginOK(t, s, "a@x.com", "pw")

	if _, err := s.Logout(context.Background(), &pb.LogoutRequest{RefreshToken: refresh}); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	_, err := s.Refresh(context.Background(), &pb.RefreshRequest{RefreshToken: refresh})
	wantCode(t, err, codes.Unauthenticated)
}

// Logout is idempotent: revoking an unknown token still returns success.
func TestLogout_Idempotent(t *testing.T) {
	s := server.NewServer()
	if _, err := s.Logout(context.Background(), &pb.LogoutRequest{RefreshToken: "never-existed"}); err != nil {
		t.Errorf("Logout of unknown token should succeed, got: %v", err)
	}
}

// Empty refresh token is InvalidArgument for both Refresh and Logout.
func TestRefreshLogout_EmptyToken(t *testing.T) {
	s := server.NewServer()
	_, err := s.Refresh(context.Background(), &pb.RefreshRequest{RefreshToken: ""})
	wantCode(t, err, codes.InvalidArgument)
	_, err = s.Logout(context.Background(), &pb.LogoutRequest{RefreshToken: ""})
	wantCode(t, err, codes.InvalidArgument)
}
