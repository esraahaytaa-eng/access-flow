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

func TestCheckNominator(t *testing.T) {
	me, nominee := uuid.New(), uuid.New()
	as := func(roles ...string) auth.Principal { return auth.Principal{EmployeeID: me, Roles: roles} }
	for _, c := range []struct {
		name     string
		actor    auth.Principal
		nominee  uuid.UUID
		recovery bool
		want     error
	}{
		{"a Super Admin proposes", as("SUPER_ADMIN"), nominee, false, nil},
		{"never themself", as("SUPER_ADMIN"), me, false, errSelfChange},
		{"an HR Manager only in recovery", as("HR_MANAGER"), nominee, false, platform.ErrForbidden},
		{"recovery: an HR Manager", as("HR_MANAGER"), nominee, true, nil},
		{"recovery: themself too", as("HR_MANAGER"), me, true, nil},
		{"recovery: not InfoSec", as("INFOSEC"), nominee, true, platform.ErrForbidden},
	} {
		if got := checkNominator(c.actor, c.nominee, c.recovery); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}
