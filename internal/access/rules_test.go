package access

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

func TestToday(t *testing.T) {
	istanbul, err := time.LoadLocation("Europe/Istanbul")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		now  time.Time
		want string
	}{
		{time.Date(2026, 3, 1, 21, 30, 0, 0, time.UTC), "2026-03-02"}, // already 2 March in Istanbul
		{time.Date(2026, 3, 1, 20, 59, 0, 0, time.UTC), "2026-03-01"},
		{time.Date(2026, 12, 31, 21, 0, 0, 0, time.UTC), "2027-01-01"},
	}
	for _, tc := range cases {
		got := Today(tc.now, istanbul)
		if got.Format(time.DateOnly) != tc.want || got.Location() != time.UTC || got.Hour() != 0 {
			t.Errorf("Today(%v) = %v; want %s at UTC midnight", tc.now, got, tc.want)
		}
	}
}

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
		{"API", "SUSPENDED", false, "SUSPENDED / VERIFIED"},
		{"MANUAL", "SUSPENDED", false, "SUSPENDED / SELF_REPORTED"},
		{"VENDOR", "SUSPENDED", true, "SUSPENDED / SELF_REPORTED"},
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
		{grantMoves, "ACTIVE", "SUSPENDED", true}, // D7: once the SUSPEND is verified or confirmed
		{grantMoves, "SUSPENDED", "ACTIVE", true}, // once the RESUME is
		{grantMoves, "SUSPENDED", "REVOCATION_REQUESTED", true},
		{grantMoves, "SUSPENDED", "CANCELLED", false},
		{grantMoves, "PROVISIONING", "SUSPENDED", false},
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

// needsOp: a SUSPEND or RESUME is queued unless the grant already is, or is about to be,
// in the target state; a cancelled operation that was sent may have changed the tool.
func TestNeedsOp(t *testing.T) {
	cases := []struct {
		target, effective string
		cancelledSent     bool
		want              bool
	}{
		{"SUSPENDED", "ACTIVE", false, true},
		{"SUSPENDED", "SUSPENDED", false, false},
		{"SUSPENDED", "SUSPENDED", true, true},
		{"ACTIVE", "ACTIVE", true, true}, // a failed SUSPEND may have applied
		{"ACTIVE", "ACTIVE", false, false},
		{"ACTIVE", "SUSPENDED", false, true},
	}
	for _, tc := range cases {
		if got := needsOp(tc.target, tc.effective, tc.cancelledSent); got != tc.want {
			t.Errorf("needsOp(%s, %s, %v) = %v; want %v", tc.target, tc.effective, tc.cancelledSent, got, tc.want)
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

func TestCheckKeeps(t *testing.T) {
	g1, g2, other := uuid.New(), uuid.New(), uuid.New()
	reviewable := []uuid.UUID{g1, g2}
	cases := []struct {
		name        string
		keep        []Keep
		want        map[uuid.UUID]string
		code, field string
	}{
		{"nothing kept", nil, map[uuid.UUID]string{}, "", ""},
		{"kept, trimmed", []Keep{{g1, "  shared on-call rota "}}, map[uuid.UUID]string{g1: "shared on-call rota"}, "", ""},
		{"blank justification", []Keep{{g1, "  "}}, nil, "justification_required", "keep[0].justification"},
		{"too long", []Keep{{g2, "ok"}, {g1, strings.Repeat("x", 1001)}}, nil, "value_too_long", "keep[1].justification"},
		{"not reviewable", []Keep{{other, "why"}}, nil, "grant_not_open", "keep[0].grantId"},
		{"twice", []Keep{{g1, "a"}, {g1, "b"}}, nil, "duplicate_grant", "keep[1].grantId"},
	}
	for _, tc := range cases {
		got, err := checkKeeps(reviewable, tc.keep)
		var perr *platform.Error
		switch {
		case tc.code == "" && err != nil:
			t.Errorf("%s: unexpected error %v", tc.name, err)
		case tc.code == "" && len(got) != len(tc.want):
			t.Errorf("%s: got %v; want %v", tc.name, got, tc.want)
		case tc.code != "" && (!errors.As(err, &perr) || perr.Code != tc.code || perr.Field != tc.field):
			t.Errorf("%s: error %v; want %s on %s", tc.name, err, tc.code, tc.field)
		}
		for id, j := range tc.want {
			if got[id] != j {
				t.Errorf("%s: %v = %q; want %q", tc.name, id, got[id], j)
			}
		}
	}
}
