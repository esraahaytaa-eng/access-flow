// Package catalog holds the tools, their access levels with approval steps, and starter
// packages (F1, F2, F3).
package catalog

import (
	"strings"

	"github.com/esraahaytaa-eng/access-flow/internal/auth"
	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

const maxNameLength = 100

// Steps lists a level's approval steps in their fixed order (F2): Manager always, then the
// optional Tool Owner and InfoSec steps.
func Steps(requiresToolOwner, requiresInfosec bool) []string {
	steps := []string{"MANAGER"}
	if requiresToolOwner {
		steps = append(steps, "TOOL_OWNER")
	}
	if requiresInfosec {
		steps = append(steps, "INFOSEC")
	}
	return steps
}

// canEdit: only a Super Admin configures the catalog (F2).
func canEdit(p auth.Principal) error {
	if !p.HasRole("SUPER_ADMIN") {
		return platform.ErrForbidden
	}
	return nil
}

// seesFullCatalog: these roles also see retired entries and versions, for configuration and evidence.
func seesFullCatalog(p auth.Principal) bool {
	return p.HasRole("SUPER_ADMIN") || p.HasRole("INFOSEC") || p.HasRole("AUDITOR")
}

// cleanName trims and checks a tool or level name.
func cleanName(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", platform.ValidationError("name", "name_required")
	}
	return s, platform.CheckText(s, "name", maxNameLength)
}
