// Package people manages employees, their AccessFlow roles and admin groups (F4, D2, A5).
package people

import (
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/esraahaytaa-eng/access-flow/internal/auth"
	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

const (
	maxNameLength = 200
	maxTextLength = 100 // department, job title
)

func hasAny(p auth.Principal, roles ...string) bool {
	for _, r := range roles {
		if p.HasRole(r) {
			return true
		}
	}
	return false
}

// canManageEmployees: HR Managers and Super Admins create and edit employees and reset
// passwords (F4, approved 2026-10-28).
func canManageEmployees(p auth.Principal) bool { return hasAny(p, "SUPER_ADMIN", "HR_MANAGER") }

// seesFullDirectory: these roles see every employee and every field (approved 2026-10-28).
func seesFullDirectory(p auth.Principal) bool {
	return hasAny(p, "SUPER_ADMIN", "HR_MANAGER", "INFOSEC", "AUDITOR")
}

var (
	errSelfChange      = platform.NewError(http.StatusForbidden, "self_change_forbidden")
	errPrivilegedReset = platform.NewError(http.StatusForbidden, "privileged_reset_requires_super_admin")
)

// checkReset: HR Managers and Super Admins reset others' passwords; an account holding
// Super Admin or InfoSec needs a Super Admin (F4). Nobody resets their own: they change it.
func checkReset(actor auth.Principal, target uuid.UUID, targetRoles []string) error {
	if !canManageEmployees(actor) {
		return platform.ErrForbidden
	}
	if actor.EmployeeID == target {
		return errSelfChange
	}
	privileged := auth.Principal{Roles: targetRoles}
	if hasAny(privileged, "SUPER_ADMIN", "INFOSEC") && !actor.HasRole("SUPER_ADMIN") {
		return errPrivilegedReset
	}
	return nil
}

var errInvalidRole = platform.ValidationError("role", "invalid_role")

// assignableRoles are the roles a Super Admin assigns directly (D2). SUPER_ADMIN itself
// needs the two-person promotion (M14).
var assignableRoles = map[string]bool{"INFOSEC": true, "HR_MANAGER": true, "AUDITOR": true}

// checkRoleChange: only a Super Admin changes roles, never their own (D2, 2026-10-28).
func checkRoleChange(actor auth.Principal, target uuid.UUID, role string) error {
	if !actor.HasRole("SUPER_ADMIN") {
		return platform.ErrForbidden
	}
	if actor.EmployeeID == target {
		return errSelfChange
	}
	if !assignableRoles[role] {
		return errInvalidRole
	}
	return nil
}

// checkMembershipChange: only a Super Admin changes group memberships, never their own (D2).
func checkMembershipChange(actor auth.Principal, target uuid.UUID) error {
	if !actor.HasRole("SUPER_ADMIN") {
		return platform.ErrForbidden
	}
	if actor.EmployeeID == target {
		return errSelfChange
	}
	return nil
}

// cleanGroupName trims and checks an admin group name.
func cleanGroupName(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", platform.ValidationError("name", "name_required")
	}
	return s, platform.CheckText(s, "name", maxTextLength)
}

// cleanName trims a required name and checks its length.
func cleanName(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", platform.ValidationError("fullName", "full_name_required")
	}
	return s, platform.CheckText(s, "fullName", maxNameLength)
}

// cleanText trims an optional text field; blank becomes nil.
func cleanText(s *string, field string) (*string, error) {
	if s == nil {
		return nil, nil
	}
	v := strings.TrimSpace(*s)
	if v == "" {
		return nil, nil
	}
	if err := platform.CheckText(v, field, maxTextLength); err != nil {
		return nil, err
	}
	return &v, nil
}
