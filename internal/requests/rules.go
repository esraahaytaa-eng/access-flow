// Package requests holds access requests and their approvals: routing with visible
// fallback, separation of duties, decisions, withdrawal, reassignment and override (F2,
// D1, D3, D6, D9, O5). Fulfillment of approved items starts in M6.
package requests

import (
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/esraahaytaa-eng/access-flow/internal/access"
	"github.com/esraahaytaa-eng/access-flow/internal/auth"
	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

var errSeparation = platform.NewError(http.StatusForbidden, "separation_of_duties")

// errBeneficiaryNotAllowed: only the beneficiary's management chain, HR or a Super Admin
// requests on their behalf (D3, 2026-10-30).
var errBeneficiaryNotAllowed = platform.NewError(http.StatusForbidden, "beneficiary_not_allowed")

// Excluded says why each listed person may not decide a step on an item (F2).
type Excluded map[uuid.UUID]string

// Exclusions: earlier approvers, the submitter and the beneficiary never decide a step.
// The submitter rule (approved 2026-10-28) also covers D3: a manager whose submission
// approved the Manager step is an earlier approver.
func Exclusions(beneficiary, submitter uuid.UUID, deciders []uuid.UUID) Excluded {
	ex := Excluded{}
	for _, id := range deciders {
		ex[id] = "an earlier approver"
	}
	if _, ok := ex[submitter]; !ok {
		ex[submitter] = "the submitter"
	}
	ex[beneficiary] = "the beneficiary"
	return ex
}

// Candidate is a possible assignee.
type Candidate struct {
	ID     uuid.UUID
	Active bool
}

// Route is where a step goes: one assignee or the InfoSec pool, with the reason shown.
type Route struct {
	AssigneeID *uuid.UUID
	Pool       string
	Note       string
}

const poolInfoSec = "INFOSEC"

// unavailable says why c cannot take a step, or "" when it can.
func unavailable(c *Candidate, ex Excluded) string {
	switch {
	case c == nil:
		return "missing"
	case !c.Active:
		return "inactive"
	case ex[c.ID] != "":
		return "is " + ex[c.ID]
	}
	return ""
}

func toPerson(id uuid.UUID, note string) Route { return Route{AssigneeID: &id, Note: note} }
func toPool(note string) Route                 { return Route{Pool: poolInfoSec, Note: note} }

// RouteManager: the manager, else the manager's manager, else InfoSec (F2).
func RouteManager(manager, managersManager *Candidate, ex Excluded) Route {
	why := unavailable(manager, ex)
	if why == "" {
		return toPerson(manager.ID, "manager")
	}
	why2 := unavailable(managersManager, ex)
	if why2 == "" {
		return toPerson(managersManager.ID, "manager "+why+" → manager's manager")
	}
	return toPool("manager " + why + ", manager's manager " + why2 + " → InfoSec")
}

// RouteToolOwner: the tool owner, else InfoSec (F2).
func RouteToolOwner(owner Candidate, ex Excluded) Route {
	if why := unavailable(&owner, ex); why != "" {
		return toPool("tool owner " + why + " → InfoSec")
	}
	return toPerson(owner.ID, "tool owner")
}

// CanDecide: the assignee, or an InfoSec member for a pool step, decides, unless
// separation of duties excludes them. Super Admins use override instead (D1).
func CanDecide(actor auth.Principal, step Step, ex Excluded) error {
	mine := step.AssigneeID != nil && *step.AssigneeID == actor.EmployeeID
	if !mine && !(step.Pool == poolInfoSec && actor.HasRole("INFOSEC")) {
		return platform.ErrForbidden
	}
	if ex[actor.EmployeeID] != "" {
		return errSeparation
	}
	return nil
}

// Duration is the requested duration (D6): UNTIL_REVOKED, or TEMPORARY until ExpiresOn.
type Duration struct {
	Type      string  `json:"type"`
	ExpiresOn *string `json:"expiresOn"` // YYYY-MM-DD in the company timezone
}

// ParseDuration checks d and returns the expiry date, which must be after today in the
// company timezone. D6 sets no upper limit.
func ParseDuration(d Duration, now time.Time, loc *time.Location) (*time.Time, error) {
	invalid := platform.ValidationError("duration", "invalid_duration")
	switch {
	case d.Type == "UNTIL_REVOKED" && d.ExpiresOn == nil:
		return nil, nil
	case d.Type != "TEMPORARY" || d.ExpiresOn == nil:
		return nil, invalid
	}
	expires, err := time.Parse(time.DateOnly, strings.TrimSpace(*d.ExpiresOn))
	if err != nil {
		return nil, invalid
	}
	if !expires.After(access.Today(now, loc)) {
		return nil, platform.ValidationError("duration", "expiry_not_in_future")
	}
	return &expires, nil
}

// maxDirectDays is the longest a direct grant may last from its creation (D1).
const maxDirectDays = 90

// DirectExpiry parses a direct grant's required expiry date, after today in the company
// timezone. Access lasts through that date (end of day, 2026-10-28), so it ends at 00:00
// the next day, which may not be later than 90 days from now (D1).
func DirectExpiry(expiresOn *string, now time.Time, loc *time.Location) (*time.Time, error) {
	if expiresOn == nil || strings.TrimSpace(*expiresOn) == "" {
		return nil, platform.ValidationError("expiresOn", "expires_required")
	}
	expires, err := time.Parse(time.DateOnly, strings.TrimSpace(*expiresOn))
	if err != nil {
		return nil, platform.ValidationError("expiresOn", "invalid_date")
	}
	ends := time.Date(expires.Year(), expires.Month(), expires.Day()+1, 0, 0, 0, 0, loc)
	switch {
	case !expires.After(access.Today(now, loc)):
		return nil, platform.ValidationError("expiresOn", "expires_in_past")
	case ends.After(now.AddDate(0, 0, maxDirectDays)):
		return nil, platform.ValidationError("expiresOn", "duration_too_long")
	}
	return &expires, nil
}

// Allowed status changes (A9). Anything else is a conflict.
var (
	itemMoves = map[string][]string{
		"PENDING_APPROVAL": {"APPROVED", "REJECTED", "WITHDRAWN", "CANCELLED"},
		"APPROVED":         {"WITHDRAWN"}, // only before work starts on the grant (O5)
	}
	stepMoves = map[string][]string{
		"WAITING": {"PENDING", "APPROVED_BY_SUBMISSION", "CANCELLED"},
		"PENDING": {"APPROVED", "REJECTED", "SUPER_ADMIN_OVERRIDE", "CANCELLED"},
	}
)

// policySteps returns the step types the level now requires that the item lacks, in the
// fixed order (F2: stricter wins). A step the level no longer requires is kept.
func policySteps(required []string, have []Step) []string {
	var missing []string
	for _, typ := range required {
		if !slices.ContainsFunc(have, func(s Step) bool { return s.Type == typ }) {
			missing = append(missing, typ)
		}
	}
	return missing
}

func canMove(moves map[string][]string, from, to string) bool {
	return slices.Contains(moves[from], to)
}
