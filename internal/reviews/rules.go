// Package reviews runs the quarterly access reviews (D8): campaigns, review items, the
// day-7, day-14 and day-21 steps and the open findings they leave.
package reviews

import (
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/esraahaytaa-eng/access-flow/internal/auth"
)

// Quarter returns the calendar quarter of at in the company timezone, as "2026-Q4", and
// the instant it began.
func Quarter(at time.Time, loc *time.Location) (string, time.Time) {
	local := at.In(loc)
	q := (int(local.Month())-1)/3 + 1
	start := time.Date(local.Year(), time.Month(3*(q-1)+1), 1, 0, 0, 0, 0, loc)
	return fmt.Sprintf("%d-Q%d", local.Year(), q), start
}

// StepDue reports whether the campaign's day step (7, 14 or 21) is due: that many whole
// days have passed since it started (D8).
func StepDue(startedAt, now time.Time, day int) bool {
	return !now.Before(startedAt.Add(time.Duration(day) * 24 * time.Hour))
}

// Owner is a tool's owner as the snapshot sees them.
type Owner struct {
	ID         uuid.UUID
	Active     bool
	SuperAdmin bool
}

// Reviewer returns who reviews employee's access to the owner's tool: the owner, or nil for
// the InfoSec pool with the reason. The owner's own access goes to someone else (D8); an
// inactive owner cannot answer, and a Super Admin can never Keep.
func Reviewer(owner Owner, employee uuid.UUID) (*uuid.UUID, string) {
	switch {
	case owner.ID == employee:
		return nil, "own access"
	case !owner.Active:
		return nil, "owner inactive"
	case owner.SuperAdmin:
		return nil, "owner is a Super Admin"
	}
	return &owner.ID, ""
}

// individually are the flags whose items must be decided one by one (D8).
var individually = []string{"RETAINED_ON_TRANSFER", "EARLIER_POLICY", "DIRECT_GRANT", "OVERRIDE", "SUSPENDED", "IMPORTED_BASELINE"}

// BulkEligible reports whether an item may be kept in bulk: its level does not require
// InfoSec approval and it carries none of the flags that need an individual decision.
func BulkEligible(requiresInfosec bool, flags []string) bool {
	return !requiresInfosec && !slices.ContainsFunc(flags, func(f string) bool { return slices.Contains(individually, f) })
}

// MayDecide returns the error code that stops actor recording decision (KEEP or REVOKE) on
// employee's item, or "" when allowed. The reviewer decides; InfoSec decides pool items and,
// after the day-14 escalation, every item; a Super Admin may only Revoke. Nobody decides on
// their own access.
func MayDecide(actor auth.Principal, decision string, reviewer *uuid.UUID, employee uuid.UUID, escalated bool) string {
	switch {
	case actor.EmployeeID == employee:
		return "separation_of_duties"
	case decision == "KEEP" && actor.HasRole("SUPER_ADMIN"):
		return "super_admin_keep_forbidden"
	case reviewer != nil && *reviewer == actor.EmployeeID:
		return ""
	case actor.HasRole("INFOSEC") && (reviewer == nil || escalated):
		return ""
	case decision == "REVOKE" && actor.HasRole("SUPER_ADMIN"):
		return ""
	}
	return "not_reviewer"
}
