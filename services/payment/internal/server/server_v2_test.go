// Unit tests for payment v2: deterministic chaos & latency injection via
// reserved payment_token values.
//
// This file is the audit surface for v2's added behavior. It is kept
// separate from server_test.go (v1) so that each version's contract can
// be reviewed, kept, or rolled back in isolation. The agent must not
// modify either file.
//
// See SPEC.yaml `chaos` for the full contract these tests pin down.

package server_test

import (
	"context"
	"testing"
	"time"

	pb "agentbench/services/payment/api/v1"
	"agentbench/services/payment/internal/server"

	"google.golang.org/grpc/codes"
)

// ════════════════════════════════════════════════════════════════════════════
// Failure tokens
// ════════════════════════════════════════════════════════════════════════════

// tok_decline: Authorize must return FailedPrecondition AND must NOT
// allocate an auth_id (so callers can't accidentally Capture against a
// "ghost" auth that doesn't exist in normal state). Verifies both the
// error code and the absence of side effects.
func TestAuthorize_Chaos_Decline(t *testing.T) {
	s := server.NewServer()
	r, err := s.Authorize(context.Background(), &pb.AuthorizeRequest{
		Amount: 100, Currency: "USD", PaymentToken: "tok_decline",
	})
	wantCode(t, err, codes.FailedPrecondition)
	// Defensive: even if the implementation returned a response struct,
	// it must not contain a usable auth_id. A non-empty id with a non-nil
	// error would be a contract violation (callers shouldn't reference it).
	if r != nil && r.GetAuthId() != "" {
		t.Errorf("auth_id should be empty on decline, got %q", r.GetAuthId())
	}
}

// tok_capture_fail: Authorize must succeed normally and return a real
// auth_id; the failure surfaces only when Capture is called against it.
// Capture failing with FailedPrecondition AND leaving the auth in
// `authorized` state is what makes saga compensation possible — the
// booking saga responds by Voiding the auth, which must still succeed.
func TestCapture_Chaos_CaptureFail(t *testing.T) {
	s := server.NewServer()
	ctx := context.Background()
	r, err := s.Authorize(ctx, &pb.AuthorizeRequest{
		Amount: 100, Currency: "USD", PaymentToken: "tok_capture_fail",
	})
	if err != nil {
		t.Fatalf("Authorize(tok_capture_fail) should succeed at auth phase: %v", err)
	}
	id := r.GetAuthId()
	if id == "" {
		t.Fatalf("auth_id empty")
	}
	_, err = s.Capture(ctx, &pb.CaptureRequest{AuthId: id})
	wantCode(t, err, codes.FailedPrecondition)

	// Compensation path: after Capture chaos-fails, the auth must still be
	// Voidable. Without this property the booking saga can't roll back
	// cleanly, and the auth becomes orphaned in `authorized` forever.
	if _, err := s.Void(ctx, &pb.VoidRequest{AuthId: id}); err != nil {
		t.Errorf("Void after chaos Capture failure: %v", err)
	}
}

// tok_capture_fail must be deterministic, not flaky: retrying Capture
// after the first failure must fail the same way. Without this, the
// failure mode would only catch a saga's first attempt and miss the
// retry-loop case.
func TestCapture_Chaos_CaptureFail_Deterministic(t *testing.T) {
	s := server.NewServer()
	ctx := context.Background()
	r, _ := s.Authorize(ctx, &pb.AuthorizeRequest{
		Amount: 100, Currency: "USD", PaymentToken: "tok_capture_fail",
	})
	id := r.GetAuthId()
	_, err1 := s.Capture(ctx, &pb.CaptureRequest{AuthId: id})
	_, err2 := s.Capture(ctx, &pb.CaptureRequest{AuthId: id})
	wantCode(t, err1, codes.FailedPrecondition)
	wantCode(t, err2, codes.FailedPrecondition)
}

// tok_refund_fail: Authorize and Capture succeed normally; Refund fails
// deterministically. Multiple refund attempts (different amounts, retries)
// must all fail, mirroring a PSP that has flagged the auth as
// refund-blocked.
func TestRefund_Chaos_RefundFail(t *testing.T) {
	s := server.NewServer()
	ctx := context.Background()
	r, err := s.Authorize(ctx, &pb.AuthorizeRequest{
		Amount: 100, Currency: "USD", PaymentToken: "tok_refund_fail",
	})
	if err != nil {
		t.Fatalf("Authorize(tok_refund_fail): %v", err)
	}
	id := r.GetAuthId()
	if _, err := s.Capture(ctx, &pb.CaptureRequest{AuthId: id}); err != nil {
		t.Fatalf("Capture(tok_refund_fail) should succeed: %v", err)
	}

	// First refund: chaos-failed.
	_, err = s.Refund(ctx, &pb.RefundRequest{AuthId: id, Amount: 30})
	wantCode(t, err, codes.FailedPrecondition)

	// Different amount, still chaos-failed.
	_, err = s.Refund(ctx, &pb.RefundRequest{AuthId: id, Amount: 100})
	wantCode(t, err, codes.FailedPrecondition)
}

// ════════════════════════════════════════════════════════════════════════════
// Latency tokens
// ════════════════════════════════════════════════════════════════════════════

// tok_slow_<ms>: Authorize must sleep at least the declared duration
// before returning success. Generous slack (50ms tolerance) keeps the
// test stable under scheduler jitter; the meaningful assertion is the
// lower bound. We use a small duration (60ms) to keep the test fast.
func TestAuthorize_Chaos_Latency(t *testing.T) {
	s := server.NewServer()
	start := time.Now()
	r, err := s.Authorize(context.Background(), &pb.AuthorizeRequest{
		Amount: 100, Currency: "USD", PaymentToken: "tok_slow_60",
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Authorize(tok_slow_60): %v", err)
	}
	if r.GetAuthId() == "" {
		t.Errorf("auth_id empty after latency injection")
	}
	if elapsed < 60*time.Millisecond {
		t.Errorf("Authorize returned in %v, want at least 60ms", elapsed)
	}
}

// A malformed slow token must NOT crash and must NOT sleep — it falls
// back to ordinary token behavior (success, normal latency). Locks in
// the SPEC's "malformed suffix is treated as ordinary" rule and catches
// implementations that panic on Atoi failure.
func TestAuthorize_Chaos_Latency_Malformed(t *testing.T) {
	s := server.NewServer()
	start := time.Now()
	r, err := s.Authorize(context.Background(), &pb.AuthorizeRequest{
		Amount: 100, Currency: "USD", PaymentToken: "tok_slow_notanint",
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Errorf("malformed slow token should behave as ordinary token, got error: %v", err)
	}
	if r.GetAuthId() == "" {
		t.Errorf("auth_id empty on malformed slow token")
	}
	// Generous upper bound: a real ordinary Authorize should be sub-ms.
	if elapsed > 50*time.Millisecond {
		t.Errorf("malformed slow token introduced latency %v, expected ~zero", elapsed)
	}
}
