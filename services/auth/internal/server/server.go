// Package server implements the auth gRPC service.
//
// v1 is in-memory and deterministic: users live in a map keyed by email with a
// salted SHA-256 password hash; access tokens are stateless HMAC-SHA256 JWTs;
// refresh tokens are opaque random strings tracked in a sessions map.
package server

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	pb "agentbench/services/auth/api/v1"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	guestScope    = "guest"
	tokenLifetime = time.Hour
	defaultSecret = "dev-insecure-secret-change-me"
	secretEnvVar  = "AUTH_JWT_SECRET"
)

// user is the stored credential record, keyed by email.
type user struct {
	userID       string
	passwordHash string // SHA-256 over salt+password
	salt         string
	scope        string
}

// session is the server-side refresh-token record.
type session struct {
	userID string
	scope  string
}

// Server implements pb.AuthServiceServer.
type Server struct {
	pb.UnimplementedAuthServiceServer

	mu       sync.Mutex
	users    map[string]*user    // email -> user
	sessions map[string]*session // refresh_token -> session
	secret   []byte
	nextID   int
}

// NewServer constructs an in-memory auth server.
func NewServer() *Server {
	secret := os.Getenv(secretEnvVar)
	if secret == "" {
		secret = defaultSecret
	}
	return &Server{
		users:    make(map[string]*user),
		sessions: make(map[string]*session),
		secret:   []byte(secret),
	}
}

// Register creates a new user with the guest scope, storing a salted hash.
func (s *Server) Register(ctx context.Context, req *pb.RegisterRequest) (*pb.RegisterResponse, error) {
	if req.GetEmail() == "" || req.GetPassword() == "" {
		return nil, status.Error(codes.InvalidArgument, "email and password are required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.users[req.GetEmail()]; ok {
		return nil, status.Error(codes.AlreadyExists, "a user with that email already exists")
	}

	salt := randomString(16)
	s.nextID++
	s.users[req.GetEmail()] = &user{
		userID:       "user-" + strconv.Itoa(s.nextID),
		passwordHash: hashPassword(salt, req.GetPassword()),
		salt:         salt,
		scope:        guestScope,
	}
	return &pb.RegisterResponse{}, nil
}

// Login verifies the password and issues a JWT access token + opaque refresh token.
func (s *Server) Login(ctx context.Context, req *pb.LoginRequest) (*pb.LoginResponse, error) {
	if req.GetEmail() == "" || req.GetPassword() == "" {
		return nil, status.Error(codes.InvalidArgument, "email and password are required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	u, ok := s.users[req.GetEmail()]
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "invalid credentials")
	}
	if !hmac.Equal([]byte(hashPassword(u.salt, req.GetPassword())), []byte(u.passwordHash)) {
		return nil, status.Error(codes.Unauthenticated, "invalid credentials")
	}

	access, err := s.mintToken(u.userID, u.scope)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to mint token")
	}
	refresh := randomString(32)
	s.sessions[refresh] = &session{userID: u.userID, scope: u.scope}

	return &pb.LoginResponse{AccessToken: access, RefreshToken: refresh}, nil
}

// VerifyToken validates the access token's signature and expiry and returns its claims.
func (s *Server) VerifyToken(ctx context.Context, req *pb.VerifyTokenRequest) (*pb.VerifyTokenResponse, error) {
	if req.GetAccessToken() == "" {
		return nil, status.Error(codes.InvalidArgument, "access_token is required")
	}

	claims, err := s.parseToken(req.GetAccessToken())
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "invalid token")
	}
	return &pb.VerifyTokenResponse{UserId: claims.Sub, Scope: claims.Scope}, nil
}

// Refresh issues a new access token for a valid, non-revoked refresh token.
func (s *Server) Refresh(ctx context.Context, req *pb.RefreshRequest) (*pb.RefreshResponse, error) {
	if req.GetRefreshToken() == "" {
		return nil, status.Error(codes.InvalidArgument, "refresh_token is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[req.GetRefreshToken()]
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "invalid refresh token")
	}

	access, err := s.mintToken(sess.userID, sess.scope)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to mint token")
	}
	return &pb.RefreshResponse{AccessToken: access}, nil
}

// Logout revokes the session for the given refresh token. Idempotent.
func (s *Server) Logout(ctx context.Context, req *pb.LogoutRequest) (*pb.LogoutResponse, error) {
	if req.GetRefreshToken() == "" {
		return nil, status.Error(codes.InvalidArgument, "refresh_token is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, req.GetRefreshToken())

	return &pb.LogoutResponse{}, nil
}

// ── token helpers ───────────────────────────────────────────────────────────

// jwtClaims is the minimal claim set for v1 access tokens.
type jwtClaims struct {
	Sub   string `json:"sub"`
	Scope string `json:"scope"`
	Iat   int64  `json:"iat"`
	Exp   int64  `json:"exp"`
}

const jwtHeader = `{"alg":"HS256","typ":"JWT"}`

// mintToken builds a signed HS256 JWT. Caller need not hold the lock.
func (s *Server) mintToken(userID, scope string) (string, error) {
	now := time.Now()
	claims := jwtClaims{
		Sub:   userID,
		Scope: scope,
		Iat:   now.Unix(),
		Exp:   now.Add(tokenLifetime).Unix(),
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	signingInput := b64(([]byte(jwtHeader))) + "." + b64(payload)
	sig := s.sign(signingInput)
	return signingInput + "." + sig, nil
}

// parseToken validates the signature and expiry and returns the claims.
func (s *Server) parseToken(token string) (*jwtClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errInvalidToken
	}
	signingInput := parts[0] + "." + parts[1]
	if !hmac.Equal([]byte(s.sign(signingInput)), []byte(parts[2])) {
		return nil, errInvalidToken
	}
	payload, err := unb64(parts[1])
	if err != nil {
		return nil, errInvalidToken
	}
	var claims jwtClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, errInvalidToken
	}
	if time.Now().Unix() >= claims.Exp {
		return nil, errInvalidToken
	}
	return &claims, nil
}

// sign returns the base64url HMAC-SHA256 of the signing input.
func (s *Server) sign(signingInput string) string {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(signingInput))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

var errInvalidToken = status.Error(codes.Unauthenticated, "invalid token")

// hashPassword computes SHA-256 over salt+password, hex-encoded.
func hashPassword(salt, password string) string {
	sum := sha256.Sum256([]byte(salt + password))
	return hex.EncodeToString(sum[:])
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
func unb64(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}

// randomString returns a hex string from n random bytes.
func randomString(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand should never fail; fall back to a time-based value.
		return hex.EncodeToString([]byte(time.Now().String()))
	}
	return hex.EncodeToString(b)
}
