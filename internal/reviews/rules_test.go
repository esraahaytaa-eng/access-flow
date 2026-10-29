package reviews

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/esraahaytaa-eng/access-flow/internal/auth"
)

func TestQuarter(t *testing.T) {
	istanbul, err := time.LoadLocation("Europe/Istanbul")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		at, key, start string
	}{
		{"2026-10-29T12:00:00Z", "2026-Q4", "2026-09-30T21:00:00Z"},
		{"2026-09-30T20:59:59Z", "2026-Q3", "2026-06-30T21:00:00Z"}, // 23:59:59 in Istanbul
		{"2026-09-30T21:00:00Z", "2026-Q4", "2026-09-30T21:00:00Z"}, // midnight in Istanbul
		{"2027-01-01T00:00:00Z", "2027-Q1", "2026-12-31T21:00:00Z"},
	} {
		at, _ := time.Parse(time.RFC3339, c.at)
		key, start := Quarter(at, istanbul)
		if key != c.key || !start.Equal(mustTime(t, c.start)) {
			t.Errorf("Quarter(%s) = %s, %s; want %s, %s", c.at, key, start.UTC().Format(time.RFC3339), c.key, c.start)
		}
	}
}

func TestStepDue(t *testing.T) {
	start := mustTime(t, "2026-10-01T09:00:00Z")
	for _, c := range []struct {
		now  string
		day  int
		want bool
	}{
		{"2026-10-08T08:59:59Z", 7, false},
		{"2026-10-08T09:00:00Z", 7, true},
		{"2026-10-15T09:00:00Z", 14, true},
		{"2026-10-22T08:59:59Z", 21, false},
		{"2026-11-30T00:00:00Z", 21, true}, // after downtime
	} {
		if got := StepDue(start, mustTime(t, c.now), c.day); got != c.want {
			t.Errorf("StepDue(day %d at %s) = %v", c.day, c.now, got)
		}
	}
}

func TestReviewer(t *testing.T) {
	owner, emp := uuid.New(), uuid.New()
	for _, c := range []struct {
		name string
		o    Owner
		emp  uuid.UUID
		pool bool
		note string
	}{
		{"the owner reviews", Owner{ID: owner, Active: true}, emp, false, ""},
		{"own access goes to InfoSec", Owner{ID: owner, Active: true}, owner, true, "own access"},
		{"an inactive owner", Owner{ID: owner}, emp, true, "owner inactive"},
		{"a Super Admin owner cannot keep", Owner{ID: owner, Active: true, SuperAdmin: true}, emp, true, "owner is a Super Admin"},
	} {
		reviewer, note := Reviewer(c.o, c.emp)
		if (reviewer == nil) != c.pool || note != c.note || (!c.pool && *reviewer != owner) {
			t.Errorf("%s: reviewer %v, note %q", c.name, reviewer, note)
		}
	}
}

func TestBulkEligible(t *testing.T) {
	for _, c := range []struct {
		infosec bool
		flags   []string
		want    bool
	}{
		{false, nil, true},
		{true, nil, false},
		{false, []string{"IMPORTED_BASELINE"}, false},
		{false, []string{"SUSPENDED"}, false},
	} {
		if got := BulkEligible(c.infosec, c.flags); got != c.want {
			t.Errorf("BulkEligible(%v, %v) = %v", c.infosec, c.flags, got)
		}
	}
}

func TestMayDecide(t *testing.T) {
	owner, emp, sec, admin := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	ownerP := auth.Principal{EmployeeID: owner}
	secP := auth.Principal{EmployeeID: sec, Roles: []string{"INFOSEC"}}
	adminP := auth.Principal{EmployeeID: admin, Roles: []string{"SUPER_ADMIN"}}
	empP := auth.Principal{EmployeeID: emp, Roles: []string{"INFOSEC"}}
	for _, c := range []struct {
		name      string
		actor     auth.Principal
		decision  string
		reviewer  *uuid.UUID
		escalated bool
		want      string
	}{
		{"owner keeps", ownerP, "KEEP", &owner, false, ""},
		{"owner revokes", ownerP, "REVOKE", &owner, false, ""},
		{"InfoSec before escalation", secP, "KEEP", &owner, false, "not_reviewer"},
		{"InfoSec after escalation", secP, "KEEP", &owner, true, ""},
		{"InfoSec pool item", secP, "KEEP", nil, false, ""},
		{"Super Admin revokes", adminP, "REVOKE", &owner, false, ""},
		{"Super Admin may not keep", adminP, "KEEP", &owner, true, "super_admin_keep_forbidden"},
		{"Super Admin owner may not keep", auth.Principal{EmployeeID: owner, Roles: []string{"SUPER_ADMIN"}}, "KEEP", &owner, false, "super_admin_keep_forbidden"},
		{"never one's own access", empP, "KEEP", nil, true, "separation_of_duties"},
		{"someone else", auth.Principal{EmployeeID: uuid.New()}, "REVOKE", &owner, true, "not_reviewer"},
	} {
		got := ""
		if code := MayDecide(c.actor, c.decision, c.reviewer, emp, c.escalated); code != "" {
			got = code
		}
		if got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
