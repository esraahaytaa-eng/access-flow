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

	"github.com/esraahaytaa-eng/access-flow/internal/auth"
	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

var errSeparation = platform.NewError(http.StatusForbidden, "separation_of_duties")

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
	today, _ := time.Parse(time.DateOnly, now.In(loc).Format(time.DateOnly))
	if !expires.After(today) {
		return nil, platform.ValidationError("duration", "expiry_not_in_future")
	}
	return &expires, nil
}

// Allowed status changes (A9). Anything else is a conflict.
var (
	itemMoves = map[string][]string{"PENDING_APPROVAL": {"APPROVED", "REJECTED", "WITHDRAWN", "CANCELLED"}}
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
