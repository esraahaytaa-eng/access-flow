package catalog

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/esraahaytaa-eng/access-flow/internal/auth"
	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

func TestSteps(t *testing.T) {
	for _, tc := range []struct {
		owner, infosec bool
		want           []string
	}{
		{false, false, []string{"MANAGER"}},
		{true, false, []string{"MANAGER", "TOOL_OWNER"}},
		{false, true, []string{"MANAGER", "INFOSEC"}},
		{true, true, []string{"MANAGER", "TOOL_OWNER", "INFOSEC"}},
	} {
		if got := Steps(tc.owner, tc.infosec); !slices.Equal(got, tc.want) {
			t.Errorf("Steps(%v, %v) = %v; want %v", tc.owner, tc.infosec, got, tc.want)
		}
	}
}

func TestCanEdit(t *testing.T) {
	for _, tc := range []struct {
		roles []string
		ok    bool
	}{
		{[]string{"SUPER_ADMIN"}, true},
		{[]string{"INFOSEC"}, false},
		{[]string{"HR_MANAGER"}, false},
		{[]string{"AUDITOR"}, false},
		{nil, false},
	} {
		err := canEdit(auth.Principal{Roles: tc.roles})
		if tc.ok != (err == nil) {
			t.Errorf("canEdit(%v) = %v; want ok=%v", tc.roles, err, tc.ok)
		}
		if !tc.ok && !errors.Is(err, platform.ErrForbidden) {
			t.Errorf("canEdit(%v) = %v; want forbidden", tc.roles, err)
		}
	}
}

func TestCleanName(t *testing.T) {
	for _, tc := range []struct{ in, want, code string }{
		{"  Jira ", "Jira", ""},
		{"   ", "", "name_required"},
		{"a\tb", "", "value_invalid"},
		{strings.Repeat("a", 101), "", "value_too_long"},
		{strings.Repeat("ş", 100), strings.Repeat("ş", 100), ""}, // counted in characters, not bytes
	} {
		got, err := cleanName(tc.in)
		var pe *platform.Error
		switch {
		case tc.code == "" && (err != nil || got != tc.want):
			t.Errorf("cleanName(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		case tc.code != "" && (!errors.As(err, &pe) || pe.Code != tc.code || pe.Field != "name"):
			t.Errorf("cleanName(%q) error = %v; want %s on name", tc.in, err, tc.code)
		}
	}
}
