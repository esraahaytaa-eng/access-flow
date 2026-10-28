package requests

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/esraahaytaa-eng/access-flow/internal/auth"
	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

func ref(id uuid.UUID) *uuid.UUID { return &id }

func TestExclusions(t *testing.T) {
	ben, sub, mgr := uuid.New(), uuid.New(), uuid.New()
	ex := Exclusions(ben, sub, []uuid.UUID{mgr})
	if ex[ben] != "the beneficiary" || ex[sub] != "the submitter" || ex[mgr] != "an earlier approver" || len(ex) != 3 {
		t.Fatalf("exclusions = %v", ex)
	}
	// Self-service: the beneficiary is the submitter. Under D3 the manager is both.
	if ex := Exclusions(ben, ben, nil); ex[ben] != "the beneficiary" || len(ex) != 1 {
		t.Fatalf("self-service exclusions = %v", ex)
	}
	if ex := Exclusions(ben, mgr, []uuid.UUID{mgr}); ex[mgr] != "an earlier approver" || len(ex) != 2 {
		t.Fatalf("D3 exclusions = %v", ex)
	}
}

func TestRouteManager(t *testing.T) {
	ben, mgr, skip := uuid.New(), uuid.New(), uuid.New()
	active := func(id uuid.UUID) *Candidate { return &Candidate{ID: id, Active: true} }
	inactive := func(id uuid.UUID) *Candidate { return &Candidate{ID: id} }
	cases := []struct {
		name          string
		manager, skip *Candidate
		ex            Excluded
		want          Route
	}{
		{"active manager", active(mgr), active(skip), nil, Route{AssigneeID: ref(mgr), Note: "manager"}},
		{"manager inactive", inactive(mgr), active(skip), nil,
			Route{AssigneeID: ref(skip), Note: "manager inactive → manager's manager"}},
		{"both inactive", inactive(mgr), inactive(skip), nil,
			Route{Pool: "INFOSEC", Note: "manager inactive, manager's manager inactive → InfoSec"}},
		{"no manager", nil, nil, nil, Route{Pool: "INFOSEC", Note: "manager missing, manager's manager missing → InfoSec"}},
		{"manager's manager is the submitter", inactive(mgr), active(skip), Excluded{skip: "the submitter"},
			Route{Pool: "INFOSEC", Note: "manager inactive, manager's manager is the submitter → InfoSec"}},
		{"manager's manager is the beneficiary", inactive(mgr), active(ben), Excluded{ben: "the beneficiary"},
			Route{Pool: "INFOSEC", Note: "manager inactive, manager's manager is the beneficiary → InfoSec"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := RouteManager(tc.manager, tc.skip, tc.ex); !sameRoute(got, tc.want) {
				t.Fatalf("route = %+v; want %+v", got, tc.want)
			}
		})
	}
}

func TestRouteToolOwner(t *testing.T) {
	owner := uuid.New()
	cases := []struct {
		name   string
		active bool
		ex     Excluded
		want   Route
	}{
		{"tool owner", true, nil, Route{AssigneeID: ref(owner), Note: "tool owner"}},
		{"owner inactive", false, nil, Route{Pool: "INFOSEC", Note: "tool owner inactive → InfoSec"}},
		{"owner is the beneficiary", true, Excluded{owner: "the beneficiary"}, Route{Pool: "INFOSEC", Note: "tool owner is the beneficiary → InfoSec"}},
		{"owner is the submitter", true, Excluded{owner: "the submitter"}, Route{Pool: "INFOSEC", Note: "tool owner is the submitter → InfoSec"}},
		{"owner already approved", true, Excluded{owner: "an earlier approver"}, Route{Pool: "INFOSEC", Note: "tool owner is an earlier approver → InfoSec"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := RouteToolOwner(Candidate{ID: owner, Active: tc.active}, tc.ex); !sameRoute(got, tc.want) {
				t.Fatalf("route = %+v; want %+v", got, tc.want)
			}
		})
	}
}

func sameRoute(a, b Route) bool {
	return a.Pool == b.Pool && a.Note == b.Note && (a.AssigneeID == nil) == (b.AssigneeID == nil) &&
		(a.AssigneeID == nil || *a.AssigneeID == *b.AssigneeID)
}

func TestCanDecide(t *testing.T) {
	me, other, ben := uuid.New(), uuid.New(), uuid.New()
	mine := Step{AssigneeID: ref(me)}
	pool := Step{Pool: "INFOSEC"}
	person := func(roles ...string) auth.Principal { return auth.Principal{EmployeeID: me, Roles: roles} }
	cases := []struct {
		name  string
		actor auth.Principal
		step  Step
		ex    Excluded
		want  error
	}{
		{"assignee", person(), mine, nil, nil},
		{"someone else's step", person("SUPER_ADMIN", "INFOSEC"), Step{AssigneeID: ref(other)}, nil, platform.ErrForbidden},
		{"InfoSec pool member", person("INFOSEC"), pool, nil, nil},
		{"pool without InfoSec", person("AUDITOR", "HR_MANAGER"), pool, nil, platform.ErrForbidden},
		{"InfoSec member who decided an earlier step", person("INFOSEC"), pool, Excluded{me: "an earlier approver"}, errSeparation},
		{"beneficiary in the pool", auth.Principal{EmployeeID: ben, Roles: []string{"INFOSEC"}}, pool, Excluded{ben: "the beneficiary"}, errSeparation},
		{"assignee who is excluded", person(), mine, Excluded{me: "the submitter"}, errSeparation},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := CanDecide(tc.actor, tc.step, tc.ex); !errors.Is(err, tc.want) {
				t.Fatalf("CanDecide = %v; want %v", err, tc.want)
			}
		})
	}
}

func TestParseDuration(t *testing.T) {
	istanbul, err := time.LoadLocation("Europe/Istanbul")
	if err != nil {
		t.Fatal(err)
	}
	// 21:30 UTC on 1 March is 00:30 on 2 March in Istanbul: "today" there is already the 2nd.
	now := time.Date(2026, 3, 1, 21, 30, 0, 0, time.UTC)
	date := func(s string) *string { return &s }
	cases := []struct {
		name string
		in   Duration
		want string // the expiry date, "" for none
		code string
	}{
		{"until revoked", Duration{Type: "UNTIL_REVOKED"}, "", ""},
		{"tomorrow in the company timezone", Duration{Type: "TEMPORARY", ExpiresOn: date("2026-03-03")}, "2026-03-03", ""},
		{"today in the company timezone, tomorrow in UTC", Duration{Type: "TEMPORARY", ExpiresOn: date("2026-03-02")}, "", "expiry_not_in_future"},
		{"yesterday", Duration{Type: "TEMPORARY", ExpiresOn: date("2026-02-28")}, "", "expiry_not_in_future"},
		{"far future: no limit (D6)", Duration{Type: "TEMPORARY", ExpiresOn: date("2099-12-31")}, "2099-12-31", ""},
		{"malformed date", Duration{Type: "TEMPORARY", ExpiresOn: date("03/03/2026")}, "", "invalid_duration"},
		{"temporary without a date", Duration{Type: "TEMPORARY"}, "", "invalid_duration"},
		{"until revoked with a date", Duration{Type: "UNTIL_REVOKED", ExpiresOn: date("2026-03-03")}, "", "invalid_duration"},
		{"unknown type", Duration{Type: "FOREVER"}, "", "invalid_duration"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseDuration(tc.in, now, istanbul)
			if tc.code != "" {
				var e *platform.Error
				if !errors.As(err, &e) || e.Code != tc.code || e.Field != "duration" {
					t.Fatalf("err = %v; want %s on duration", err, tc.code)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if (got == nil) != (tc.want == "") || (got != nil && got.Format(time.DateOnly) != tc.want) {
				t.Fatalf("expiry = %v; want %q", got, tc.want)
			}
		})
	}
}

func TestDirectExpiry(t *testing.T) {
	istanbul, err := time.LoadLocation("Europe/Istanbul")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 3, 1, 21, 30, 0, 0, time.UTC) // already 2 March in Istanbul
	date := func(s string) *string { return &s }
	cases := []struct {
		name string
		in   *string
		want string
		code string
	}{
		{"tomorrow", date("2026-03-03"), "2026-03-03", ""},
		{"90 days from today in the company timezone", date("2026-05-31"), "2026-05-31", ""},
		{"91 days", date("2026-06-01"), "", "duration_too_long"},
		{"today in the company timezone", date("2026-03-02"), "", "expires_in_past"},
		{"yesterday", date("2026-02-28"), "", "expires_in_past"},
		{"missing", nil, "", "expires_required"},
		{"blank", date(" "), "", "expires_required"},
		{"malformed", date("31/05/2026"), "", "invalid_date"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DirectExpiry(tc.in, now, istanbul)
			if tc.code != "" {
				var e *platform.Error
				if !errors.As(err, &e) || e.Code != tc.code || e.Field != "expiresOn" {
					t.Fatalf("err = %v; want %s on expiresOn", err, tc.code)
				}
				return
			}
			if err != nil || got.Format(time.DateOnly) != tc.want {
				t.Fatalf("expiry = %v, %v; want %s", got, err, tc.want)
			}
		})
	}
}

func TestTransitions(t *testing.T) {
	cases := []struct {
		moves    map[string][]string
		from, to string
		ok       bool
	}{
		{itemMoves, "PENDING_APPROVAL", "APPROVED", true},
		{itemMoves, "PENDING_APPROVAL", "WITHDRAWN", true},
		{itemMoves, "REJECTED", "APPROVED", false}, // a rejection is final (O5)
		{itemMoves, "APPROVED", "WITHDRAWN", true}, // only before work starts on the grant (O5)
		{itemMoves, "APPROVED", "REJECTED", false},
		{itemMoves, "PENDING_APPROVAL", "CANCELLED", true}, // retired before provisioning
		{itemMoves, "APPROVED", "CANCELLED", false},
		{stepMoves, "WAITING", "PENDING", true},
		{stepMoves, "WAITING", "APPROVED", false}, // steps are decided in order
		{stepMoves, "PENDING", "SUPER_ADMIN_OVERRIDE", true},
		{stepMoves, "REJECTED", "SUPER_ADMIN_OVERRIDE", false}, // an override cannot reverse a rejection (D1)
		{stepMoves, "APPROVED", "REJECTED", false},
	}
	for _, tc := range cases {
		if got := canMove(tc.moves, tc.from, tc.to); got != tc.ok {
			t.Errorf("%s → %s = %v; want %v", tc.from, tc.to, got, tc.ok)
		}
	}
}

func TestPolicySteps(t *testing.T) {
	have := func(types ...string) []Step {
		var out []Step
		for _, typ := range types {
			out = append(out, Step{Type: typ})
		}
		return out
	}
	cases := []struct {
		name     string
		required []string
		have     []Step
		want     []string
	}{
		{"nothing new", []string{"MANAGER", "TOOL_OWNER"}, have("MANAGER", "TOOL_OWNER"), nil},
		{"a step was removed: kept", []string{"MANAGER"}, have("MANAGER", "TOOL_OWNER"), nil},
		{"InfoSec added", []string{"MANAGER", "INFOSEC"}, have("MANAGER"), []string{"INFOSEC"}},
		{"both added, in order", []string{"MANAGER", "TOOL_OWNER", "INFOSEC"}, have("MANAGER"), []string{"TOOL_OWNER", "INFOSEC"}},
	}
	for _, tc := range cases {
		if got := policySteps(tc.required, tc.have); !slices.Equal(got, tc.want) {
			t.Errorf("%s: policySteps = %v; want %v", tc.name, got, tc.want)
		}
	}
}
