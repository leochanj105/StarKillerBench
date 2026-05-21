// Unit tests for the payment gRPC service, derived from SPEC.yaml.
//
// What this service is:
//   payment is a mock external payment processor (think Stripe). Four RPCs:
//     - Authorize: "hold X dollars; here's a token to reference later"
//     - Capture:   "actually charge the held amount"
//     - Void:      "cancel the hold without charging"
//     - Refund:    "give back some/all of a captured charge"
//   State machine per authorization:
//     authorized ──Capture──→ captured ──Refund(× many, partial)──
//                ──Void───→ voided
//   Storage is a single in-memory map keyed by auth_id; reset on restart.
//
// Test contract this file assumes about the agent's implementation:
//   - `server.NewServer()` returns a *Server (or other type) that satisfies
//     pb.PaymentServiceServer.
//   - Handlers carry the standard gRPC signatures:
//        func (s *Server) Authorize(context.Context, *pb.AuthorizeRequest) (*pb.AuthorizeResponse, error)
//        ... and the analogous shapes for Capture / Void / Refund.
//   - Error returns use google.golang.org/grpc/status.Error with the codes
//     declared in SPEC.yaml's `error_semantics`.
//
// Authored out-of-band and audited by a human before the agent generates the
// implementation. The agent must NOT modify this file.

package server_test

import (
	"context"
	"testing"

	pb "agentbench/services/payment/api/v1"
	"agentbench/services/payment/internal/server"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// authOK runs a successful Authorize and returns the auth_id. The setup
// path of most other tests depends on this working — if it doesn't,
// failing fast here gives a clearer signal than a confusing downstream error.
func authOK(t *testing.T, s pb.PaymentServiceServer, amount int64) string {
	t.Helper()
	r, err := s.Authorize(context.Background(), &pb.AuthorizeRequest{
		Amount: amount, Currency: "USD", PaymentToken: "tok",
	})
	if err != nil {
		t.Fatalf("Authorize(amount=%d): %v", amount, err)
	}
	return r.GetAuthId()
}

// wantCode asserts the gRPC status code on a returned error.
func wantCode(t *testing.T, err error, want codes.Code) {
	t.Helper()
	if got := status.Code(err); got != want {
		t.Errorf("status code = %v, want %v (err=%v)", got, want, err)
	}
}

// ════════════════════════════════════════════════════════════════════════════
// Authorize
//
// "Hold X dollars on this card; give me a token." Funds are earmarked but
// not yet moved. Every later operation (Capture/Void/Refund) references the
// returned auth_id.
// ════════════════════════════════════════════════════════════════════════════

// A successful Authorize must return a non-empty auth_id. Without it,
// callers have no handle to reference the authorization later — the
// authorization would effectively be unreachable.
func TestAuthorize_ReturnsNonEmptyID(t *testing.T) {
	s := server.NewServer()
	if id := authOK(t, s, 100); id == "" {
		t.Errorf("auth_id is empty, want non-empty")
	}
}

// Two Authorize calls with identical inputs represent independent
// transactions. They must get distinct auth_ids; collisions would mean
// one authorization aliasing another (a later Capture on auth A would
// accidentally affect auth B).
func TestAuthorize_DistinctIDs(t *testing.T) {
	s := server.NewServer()
	a := authOK(t, s, 100)
	b := authOK(t, s, 100)
	if a == b {
		t.Errorf("two Authorize calls returned the same auth_id %q", a)
	}
}

// Three malformed-input cases, all of which SPEC declares must return
// InvalidArgument: a non-positive amount (zero or negative) makes no
// business sense, and an empty currency string is missing required data.
func TestAuthorize_InvalidArgument(t *testing.T) {
	s := server.NewServer()
	cases := []struct {
		name string
		req  *pb.AuthorizeRequest
	}{
		{"amount zero", &pb.AuthorizeRequest{Amount: 0, Currency: "USD", PaymentToken: "t"}},
		{"amount negative", &pb.AuthorizeRequest{Amount: -1, Currency: "USD", PaymentToken: "t"}},
		{"empty currency", &pb.AuthorizeRequest{Amount: 100, Currency: "", PaymentToken: "t"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.Authorize(context.Background(), tc.req)
			wantCode(t, err, codes.InvalidArgument)
		})
	}
}

// ════════════════════════════════════════════════════════════════════════════
// Capture
//
// "Actually charge the held amount." Moves an authorization from
// `authorized` to `captured`. Only valid against an authorization that's
// still in `authorized` state (or already `captured`, idempotently).
// ════════════════════════════════════════════════════════════════════════════

// The straight-line path: Authorize then Capture(auth_id) succeeds with no
// error. This is the most common flow in production — funds get held, then
// charged.
func TestCapture_Happy(t *testing.T) {
	s := server.NewServer()
	id := authOK(t, s, 100)
	if _, err := s.Capture(context.Background(), &pb.CaptureRequest{AuthId: id}); err != nil {
		t.Errorf("Capture after Authorize: %v", err)
	}
}

// Idempotency on retry. A client whose Capture request times out (network
// blip) doesn't know whether the server processed it; the safe action is
// to retry. The second Capture on an already-captured auth must succeed
// silently, not fail — otherwise the client is forced to distinguish
// "didn't capture" from "captured but I don't know," which is impossible.
func TestCapture_Idempotent(t *testing.T) {
	s := server.NewServer()
	id := authOK(t, s, 100)
	ctx := context.Background()
	if _, err := s.Capture(ctx, &pb.CaptureRequest{AuthId: id}); err != nil {
		t.Fatalf("first Capture: %v", err)
	}
	if _, err := s.Capture(ctx, &pb.CaptureRequest{AuthId: id}); err != nil {
		t.Errorf("second Capture should be a no-op, got: %v", err)
	}
}

// Capture against an auth_id that was never issued returns NotFound.
// There's nothing to capture; reject cleanly.
func TestCapture_NotFound(t *testing.T) {
	s := server.NewServer()
	_, err := s.Capture(context.Background(), &pb.CaptureRequest{AuthId: "no-such-id"})
	wantCode(t, err, codes.NotFound)
}

// State-machine violation: an authorization that was Voided cannot be
// Captured afterward (the hold has been released; there's nothing left to
// charge). FailedPrecondition is the correct code per gRPC conventions —
// the request was well-formed but the resource isn't in a state that
// allows it.
func TestCapture_AfterVoid_FailedPrecondition(t *testing.T) {
	s := server.NewServer()
	id := authOK(t, s, 100)
	ctx := context.Background()
	if _, err := s.Void(ctx, &pb.VoidRequest{AuthId: id}); err != nil {
		t.Fatalf("Void: %v", err)
	}
	_, err := s.Capture(ctx, &pb.CaptureRequest{AuthId: id})
	wantCode(t, err, codes.FailedPrecondition)
}

// ════════════════════════════════════════════════════════════════════════════
// Void
//
// "Release the hold without charging." Moves an authorization from
// `authorized` to `voided`. Only valid against an auth still in `authorized`
// state (or already `voided`, idempotently).
// ════════════════════════════════════════════════════════════════════════════

// The cancel-before-charge path: Authorize then Void. Releases the hold
// cleanly, no money moved.
func TestVoid_Happy(t *testing.T) {
	s := server.NewServer()
	id := authOK(t, s, 100)
	if _, err := s.Void(context.Background(), &pb.VoidRequest{AuthId: id}); err != nil {
		t.Errorf("Void after Authorize: %v", err)
	}
}

// Idempotency on retry — same reasoning as TestCapture_Idempotent: a
// network-timeout retry must not be punished. Second Void of an
// already-voided auth succeeds silently.
func TestVoid_Idempotent(t *testing.T) {
	s := server.NewServer()
	id := authOK(t, s, 100)
	ctx := context.Background()
	if _, err := s.Void(ctx, &pb.VoidRequest{AuthId: id}); err != nil {
		t.Fatalf("first Void: %v", err)
	}
	if _, err := s.Void(ctx, &pb.VoidRequest{AuthId: id}); err != nil {
		t.Errorf("second Void should be a no-op, got: %v", err)
	}
}

// Void against an unknown auth_id is NotFound, same as Capture.
func TestVoid_NotFound(t *testing.T) {
	s := server.NewServer()
	_, err := s.Void(context.Background(), &pb.VoidRequest{AuthId: "no-such-id"})
	wantCode(t, err, codes.NotFound)
}

// State-machine violation: once money has been captured, the auth can't be
// "voided" — that's what Refund is for. FailedPrecondition signals the
// state mismatch.
func TestVoid_AfterCapture_FailedPrecondition(t *testing.T) {
	s := server.NewServer()
	id := authOK(t, s, 100)
	ctx := context.Background()
	if _, err := s.Capture(ctx, &pb.CaptureRequest{AuthId: id}); err != nil {
		t.Fatalf("Capture: %v", err)
	}
	_, err := s.Void(ctx, &pb.VoidRequest{AuthId: id})
	wantCode(t, err, codes.FailedPrecondition)
}

// ════════════════════════════════════════════════════════════════════════════
// Refund
//
// "Give back some or all of a captured charge." Only valid against
// `captured` authorizations, and only up to (captured_amount − already_refunded).
// Refunds accumulate across multiple calls.
// ════════════════════════════════════════════════════════════════════════════

// Refund of less than the full captured amount: valid. The auth stays in
// `captured` state; only the refunded counter increases.
func TestRefund_Partial(t *testing.T) {
	s := server.NewServer()
	id := authOK(t, s, 100)
	ctx := context.Background()
	if _, err := s.Capture(ctx, &pb.CaptureRequest{AuthId: id}); err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if _, err := s.Refund(ctx, &pb.RefundRequest{AuthId: id, Amount: 30}); err != nil {
		t.Errorf("partial Refund: %v", err)
	}
}

// Refund for the full captured amount in one shot. Valid (exactly equals
// the remaining refundable balance).
func TestRefund_Full(t *testing.T) {
	s := server.NewServer()
	id := authOK(t, s, 100)
	ctx := context.Background()
	if _, err := s.Capture(ctx, &pb.CaptureRequest{AuthId: id}); err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if _, err := s.Refund(ctx, &pb.RefundRequest{AuthId: id, Amount: 100}); err != nil {
		t.Errorf("full Refund: %v", err)
	}
}

// Accumulation: a captured 100 minus two refunds of 30 leaves 40
// refundable. A third refund of 50 exceeds that and must fail with
// FailedPrecondition. This is the load-bearing test for the "refunded so
// far" counter — if the implementation forgets to track it, refunds would
// be evaluated against the original captured amount each time, and the
// over-refund check here would erroneously pass.
func TestRefund_AccumulatesAcrossCalls(t *testing.T) {
	s := server.NewServer()
	id := authOK(t, s, 100)
	ctx := context.Background()
	if _, err := s.Capture(ctx, &pb.CaptureRequest{AuthId: id}); err != nil {
		t.Fatalf("Capture: %v", err)
	}
	// Refund 30 + 30 = 60; remaining refundable = 40.
	if _, err := s.Refund(ctx, &pb.RefundRequest{AuthId: id, Amount: 30}); err != nil {
		t.Fatalf("first Refund: %v", err)
	}
	if _, err := s.Refund(ctx, &pb.RefundRequest{AuthId: id, Amount: 30}); err != nil {
		t.Fatalf("second Refund: %v", err)
	}
	// Third refund of 50 exceeds remaining 40 → FailedPrecondition.
	_, err := s.Refund(ctx, &pb.RefundRequest{AuthId: id, Amount: 50})
	wantCode(t, err, codes.FailedPrecondition)
}

// Refund against an unknown auth_id is NotFound.
func TestRefund_NotFound(t *testing.T) {
	s := server.NewServer()
	_, err := s.Refund(context.Background(), &pb.RefundRequest{AuthId: "no-such-id", Amount: 10})
	wantCode(t, err, codes.NotFound)
}

// Single-call over-refund: a captured 100 cannot be refunded for 150.
// FailedPrecondition signals the state/amount mismatch.
func TestRefund_OverCapture_FailedPrecondition(t *testing.T) {
	s := server.NewServer()
	id := authOK(t, s, 100)
	ctx := context.Background()
	if _, err := s.Capture(ctx, &pb.CaptureRequest{AuthId: id}); err != nil {
		t.Fatalf("Capture: %v", err)
	}
	_, err := s.Refund(ctx, &pb.RefundRequest{AuthId: id, Amount: 150})
	wantCode(t, err, codes.FailedPrecondition)
}

// Refund without a preceding Capture: the auth is in `authorized` state
// (or even `voided`), captured_amount = 0, so any positive refund exceeds
// the remaining (0 − 0) and returns FailedPrecondition. Covers the
// "refund the wrong thing" mistake — refunds only make sense after charge.
func TestRefund_WithoutCapture_FailedPrecondition(t *testing.T) {
	s := server.NewServer()
	id := authOK(t, s, 100)
	_, err := s.Refund(context.Background(), &pb.RefundRequest{AuthId: id, Amount: 10})
	wantCode(t, err, codes.FailedPrecondition)
}
