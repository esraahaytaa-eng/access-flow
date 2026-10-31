package people

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/esraahaytaa-eng/access-flow/internal/auth"
	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

func TestCheckAuthorizer(t *testing.T) {
	nominee, proposer, other := uuid.New(), uuid.New(), uuid.New()
	as := func(id uuid.UUID, roles ...string) auth.Principal {
		return auth.Principal{EmployeeID: id, Roles: roles}
	}
	for _, c := range []struct {
		name            string
		actor           auth.Principal
		recovery, other bool
		want            error
	}{
		{"the nominee never authorizes", as(nominee, "SUPER_ADMIN", "INFOSEC"), false, true, errSeparation},
		{"nor the proposer", as(proposer, "SUPER_ADMIN"), false, true, errSeparation},
		{"another Super Admin", as(other, "SUPER_ADMIN"), false, true, nil},
		{"InfoSec while another Super Admin exists", as(other, "INFOSEC"), false, true, errSuperAdminRequired},
		{"InfoSec when the proposer is the only Super Admin", as(other, "INFOSEC"), false, false, nil},
		{"a Super Admin cannot stand in for InfoSec", as(other, "SUPER_ADMIN"), false, false, platform.ErrForbidden},
		{"recovery: InfoSec", as(other, "INFOSEC"), true, false, nil},
		{"recovery: anyone else", as(other, "HR_MANAGER"), true, false, platform.ErrForbidden},
		{"no role", as(other), false, true, platform.ErrForbidden},
	} {
		if got := checkAuthorizer(c.actor, nominee, proposer, c.recovery, c.other); !errors.Is(got, c.want) && got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

func TestCheckSuccessionAuthorizer(t *testing.T) {
	nominee, proposer, other := uuid.New(), uuid.New(), uuid.New()
	as := func(id uuid.UUID, roles ...string) auth.Principal {
		return auth.Principal{EmployeeID: id, Roles: roles}
	}
	for _, c := range []struct {
		name  string
		actor auth.Principal
		want  error
	}{
		{"InfoSec authorizes HR's succession", as(other, "INFOSEC"), nil},
		{"so does an active Super Admin", as(other, "SUPER_ADMIN"), nil},
		{"never HR's proposer, even with InfoSec", as(proposer, "HR_MANAGER", "INFOSEC"), errSeparation},
		{"never the nominee", as(nominee, "INFOSEC"), errSeparation},
		{"another HR Manager is not independent enough", as(other, "HR_MANAGER"), platform.ErrForbidden},
		{"nor an Auditor", as(other, "AUDITOR"), platform.ErrForbidden},
	} {
		if got := checkSuccessionAuthorizer(c.actor, nominee, proposer); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

func TestCheckNominator(t *testing.T) {
	me, nominee := uuid.New(), uuid.New()
	as := func(roles ...string) auth.Principal { return auth.Principal{EmployeeID: me, Roles: roles} }
	for _, c := range []struct {
		name       string
		actor      auth.Principal
		nominee    uuid.UUID
		active     int // Super Admins not terminated (a suspended one counts, F-1)
		succession bool
		want       error
	}{
		{"a Super Admin proposes", as("SUPER_ADMIN"), nominee, 1, false, nil},
		{"never themself", as("SUPER_ADMIN"), me, 2, false, errSelfChange},
		{"a Super Admin who is also HR promotes the usual way", as("SUPER_ADMIN", "HR_MANAGER"), nominee, 1, false, nil},
		{"HR: not while two Super Admins are active", as("HR_MANAGER"), nominee, 2, false, platform.ErrForbidden},
		{"HR: succession when the last one is leaving", as("HR_MANAGER"), nominee, 1, true, nil},
		{"HR: succession when none is left", as("HR_MANAGER"), nominee, 0, true, nil},
		{"HR: never themself", as("HR_MANAGER"), me, 0, false, errSelfChange},
		{"InfoSec never nominates", as("INFOSEC"), nominee, 0, false, platform.ErrForbidden},
	} {
		succession, err := checkNominator(c.actor, c.nominee, c.active)
		if err != c.want || succession != c.succession {
			t.Errorf("%s: %v %v, want %v %v", c.name, succession, err, c.succession, c.want)
		}
	}
}

func TestCheckLeaverAction(t *testing.T) {
	me, target := uuid.New(), uuid.New()
	as := func(roles ...string) auth.Principal { return auth.Principal{EmployeeID: me, Roles: roles} }
	for _, c := range []struct {
		name  string
		actor auth.Principal
		id    uuid.UUID
		roles []string
		want  error
	}{
		{"HR ends a Super Admin's employment (D2 succession)", as("HR_MANAGER"), target, []string{"SUPER_ADMIN"}, nil},
		{"HR still needs a Super Admin for InfoSec", as("HR_MANAGER"), target, []string{"SUPER_ADMIN", "INFOSEC"}, errPrivilegedReset},
		{"never oneself", as("HR_MANAGER"), me, []string{"SUPER_ADMIN"}, errSelfChange},
		{"a manager cannot", as(), target, nil, platform.ErrForbidden},
	} {
		if got := CheckLeaverAction(c.actor, c.id, c.roles); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
	// Password resets and reactivation keep the stricter rule.
	if got := CheckAccountAction(as("HR_MANAGER"), target, []string{"SUPER_ADMIN"}); got != errPrivilegedReset {
		t.Errorf("CheckAccountAction: %v", got)
	}
}
