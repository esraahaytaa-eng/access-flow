package access

import (
	"testing"
	"time"
)

func TestBackoff(t *testing.T) {
	cases := []struct {
		attempts int
		wait     time.Duration
		final    bool
	}{
		{1, 30 * time.Second, false},
		{2, time.Minute, false},
		{3, 2 * time.Minute, false},
		{4, 4 * time.Minute, false},
		{5, 0, true},
		{6, 0, true},
	}
	for _, tc := range cases {
		if wait, final := Backoff(tc.attempts); wait != tc.wait || final != tc.final {
			t.Errorf("Backoff(%d) = %v, %v; want %v, %v", tc.attempts, wait, final, tc.wait, tc.final)
		}
	}
}

func TestDisplayStatus(t *testing.T) {
	cases := []struct {
		method, status string
		ticket         bool // the grant's task has a vendor ticket
		want           string
	}{
		{"API", "PROVISIONING", false, "PROVISIONING"},
		{"API", "ACTIVE", false, "ACTIVE / VERIFIED"},
		{"MANUAL", "ACTIVE", false, "ACTIVE / SELF_REPORTED"},
		{"VENDOR", "ACTIVE", false, "ACTIVE / SELF_REPORTED"},
		{"VENDOR", "ACTIVE", true, "ACTIVE / SELF_REPORTED"},
		{"MANUAL", "PENDING_FULFILLMENT", false, "PENDING_FULFILLMENT"},
		{"VENDOR", "PENDING_FULFILLMENT", false, "PENDING_FULFILLMENT"},
		{"VENDOR", "PENDING_FULFILLMENT", true, "WAITING_VENDOR"},
		{"VENDOR", "FAILED", true, "FAILED"},
		{"API", "FAILED", false, "FAILED"},
		{"API", "REVOCATION_REQUESTED", false, "REVOCATION_REQUESTED"}, // still held (O1)
		{"API", "REVOCATION_FAILED", false, "REVOCATION_FAILED"},
		{"API", "REVOKED", false, "REVOKED / VERIFIED"},
		{"MANUAL", "REVOKED", false, "REVOKED / SELF_REPORTED"},
		{"API", "CANCELLED", false, "CANCELLED"}, // never reached the tool
	}
	for _, tc := range cases {
		if got := DisplayStatus(tc.method, tc.status, tc.ticket); got != tc.want {
			t.Errorf("DisplayStatus(%s, %s, %v) = %q; want %q", tc.method, tc.status, tc.ticket, got, tc.want)
		}
	}
}

func TestGrantTransitions(t *testing.T) {
	cases := []struct {
		moves    map[string][]string
		from, to string
		ok       bool
	}{
		{grantMoves, "PROVISIONING", "ACTIVE", true},
		{grantMoves, "PROVISIONING", "CANCELLED", true},
		{grantMoves, "ACTIVE", "CANCELLED", false}, // held access is revoked, never cancelled
		{grantMoves, "ACTIVE", "REVOCATION_REQUESTED", true},
		{grantMoves, "FAILED", "PROVISIONING", true},                      // manual retry
		{grantMoves, "FAILED", "PENDING_FULFILLMENT", true},               // retry of a Manual or Vendor task
		{grantMoves, "PENDING_FULFILLMENT", "REVOCATION_REQUESTED", true}, // removal while the task is claimed
		{grantMoves, "FAILED", "REVOCATION_REQUESTED", true},
		{grantMoves, "REVOCATION_FAILED", "REVOCATION_REQUESTED", true},
		{grantMoves, "REVOKED", "ACTIVE", false}, // final
		{grantMoves, "CANCELLED", "PROVISIONING", false},
		{opMoves, "PENDING", "CANCELLED", true},
		{opMoves, "IN_PROGRESS", "CANCELLED", true}, // a grant superseded by its removal
		{opMoves, "FAILED", "PENDING", true},        // manual retry
		{opMoves, "SUCCEEDED", "PENDING", false},
	}
	for _, tc := range cases {
		if got := canMove(tc.moves, tc.from, tc.to); got != tc.ok {
			t.Errorf("%s → %s = %v; want %v", tc.from, tc.to, got, tc.ok)
		}
	}
}

func TestRetiredReason(t *testing.T) {
	cases := []struct{ tool, level, want string }{
		{"ACTIVE", "ACTIVE", ""},
		{"ACTIVE", "RETIRED", "access level retired"},
		{"RETIRED", "ACTIVE", "tool retired"},
		{"RETIRED", "RETIRED", "tool retired"},
	}
	for _, tc := range cases {
		if got := RetiredReason(tc.tool, tc.level); got != tc.want {
			t.Errorf("RetiredReason(%s, %s) = %q; want %q", tc.tool, tc.level, got, tc.want)
		}
	}
}
