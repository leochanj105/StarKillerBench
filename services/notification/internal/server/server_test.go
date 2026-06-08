// Unit tests for notification, derived from SPEC.yaml.
//
// What this service is:
//   notification (v1) is a synchronous placeholder for the async message
//   worker. Notify(event_type, user_id, booking_id) returns the outbound
//   message(s) it would send for that event. Stateless and deterministic.
//
// Test contract assumed about the implementation:
//   - `server.NewServer()` returns a *Server implementing
//     pb.NotificationServiceServer.
//   - Standard gRPC signatures; errors via google.golang.org/grpc/status with
//     the codes in SPEC.error_semantics.
//
// Authored out-of-band and audited before the agent implements. The agent
// must not modify this file.

package server_test

import (
	"context"
	"strings"
	"testing"

	pb "agentbench/services/notification/api/v1"
	"agentbench/services/notification/internal/server"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func wantCode(t *testing.T, err error, want codes.Code) {
	t.Helper()
	if got := status.Code(err); got != want {
		t.Errorf("status code = %v, want %v (err=%v)", got, want, err)
	}
}

func notifyOK(t *testing.T, s pb.NotificationServiceServer, event, user, booking string) []*pb.Message {
	t.Helper()
	r, err := s.Notify(context.Background(), &pb.NotifyRequest{
		EventType: event, UserId: user, BookingId: booking,
	})
	if err != nil {
		t.Fatalf("Notify(%s): %v", event, err)
	}
	return r.GetMessages()
}

// ════════════════════════════════════════════════════════════════════════════
// Notify
// ════════════════════════════════════════════════════════════════════════════

// A confirmed-booking event yields one email message that references the
// booking_id.
func TestNotify_Confirmed(t *testing.T) {
	s := server.NewServer()
	msgs := notifyOK(t, s, "booking.confirmed", "u-1", "b-123")
	if len(msgs) != 1 {
		t.Fatalf("messages = %d, want 1", len(msgs))
	}
	if msgs[0].GetChannel() != "email" {
		t.Errorf("channel = %q, want email", msgs[0].GetChannel())
	}
	if !strings.Contains(msgs[0].GetBody(), "b-123") {
		t.Errorf("body %q should reference booking_id b-123", msgs[0].GetBody())
	}
}

// A cancelled-booking event yields one email message that references the
// booking_id.
func TestNotify_Cancelled(t *testing.T) {
	s := server.NewServer()
	msgs := notifyOK(t, s, "booking.cancelled", "u-1", "b-456")
	if len(msgs) != 1 {
		t.Fatalf("messages = %d, want 1", len(msgs))
	}
	if !strings.Contains(msgs[0].GetBody(), "b-456") {
		t.Errorf("body %q should reference booking_id b-456", msgs[0].GetBody())
	}
}

// An unrecognized event type is InvalidArgument.
func TestNotify_UnknownEvent(t *testing.T) {
	s := server.NewServer()
	_, err := s.Notify(context.Background(), &pb.NotifyRequest{
		EventType: "booking.exploded", UserId: "u-1", BookingId: "b-1",
	})
	wantCode(t, err, codes.InvalidArgument)
}

// Empty required fields are InvalidArgument.
func TestNotify_EmptyFields(t *testing.T) {
	s := server.NewServer()
	cases := []*pb.NotifyRequest{
		{EventType: "", UserId: "u-1", BookingId: "b-1"},
		{EventType: "booking.confirmed", UserId: "", BookingId: "b-1"},
		{EventType: "booking.confirmed", UserId: "u-1", BookingId: ""},
	}
	for _, req := range cases {
		_, err := s.Notify(context.Background(), req)
		wantCode(t, err, codes.InvalidArgument)
	}
}
