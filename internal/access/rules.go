package access

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

// Today is now's date in the company timezone, at UTC midnight as dates are read and
// compared everywhere (A6).
func Today(now time.Time, loc *time.Location) time.Time {
	y, m, d := now.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// Backoff returns the wait after a failed attempt, or final once the 5th has failed (O3).
func Backoff(attempts int) (wait time.Duration, final bool) {
	if attempts >= 5 {
		return 0, true
	}
	return 30 * time.Second << (attempts - 1), false
}

// DisplayStatus is the grant's screen label (Fulfillment methods): activation and removal
// carry their evidence; only API results are verified. A grant task with a vendor ticket
// is waiting for the vendor.
func DisplayStatus(method, status string, ticket bool) string {
	if status == "PENDING_FULFILLMENT" && ticket {
		return "WAITING_VENDOR"
	}
	if status != "ACTIVE" && status != "SUSPENDED" && status != "REVOKED" {
		return status
	}
	if method == "API" {
		return status + " / VERIFIED"
	}
	return status + " / SELF_REPORTED"
}

// RetiredReason says why an approved grant cannot be provisioned, or "" when it can.
func RetiredReason(toolStatus, levelStatus string) string {
	switch {
	case toolStatus == "RETIRED":
		return "tool retired"
	case levelStatus == "RETIRED":
		return "access level retired"
	}
	return ""
}

// Allowed transitions (A9). CANCELLED is only for grants that never reached the tool.
var grantMoves = map[string][]string{
	"PROVISIONING":         {"ACTIVE", "FAILED", "CANCELLED", "REVOCATION_REQUESTED"},
	"PENDING_FULFILLMENT":  {"ACTIVE", "FAILED", "CANCELLED", "REVOCATION_REQUESTED"},
	"ACTIVE":               {"SUSPENDED", "REVOCATION_REQUESTED"},
	"SUSPENDED":            {"ACTIVE", "REVOCATION_REQUESTED"},
	"FAILED":               {"PROVISIONING", "PENDING_FULFILLMENT", "REVOCATION_REQUESTED"},
	"REVOCATION_REQUESTED": {"REVOKED", "REVOCATION_FAILED"},
	"REVOCATION_FAILED":    {"REVOCATION_REQUESTED"},
}

var opMoves = map[string][]string{
	"PENDING":     {"IN_PROGRESS", "CANCELLED"},
	"IN_PROGRESS": {"SUCCEEDED", "PENDING", "FAILED", "CANCELLED"},
	"FAILED":      {"PENDING", "CANCELLED"},
}

// needsOp says whether a SUSPEND or RESUME must be queued to bring a grant to target
// (SUSPENDED or ACTIVE). effective is where the grant is heading: the target of its
// IN_PROGRESS SUSPEND or RESUME, else its status. A cancelled operation that was sent may
// have changed the tool, so the target state is then applied again.
func needsOp(target, effective string, cancelledSent bool) bool {
	return cancelledSent || effective != target
}

func canMove(moves map[string][]string, from, to string) bool {
	return slices.Contains(moves[from], to)
}

// Keep is a grant HR keeps at a transfer, with the required written justification (D5).
type Keep struct {
	GrantID       uuid.UUID `json:"grantId"`
	Justification string    `json:"justification"`
}

// checkKeeps validates a transfer's keep list against the person's reviewable grants and
// returns each kept grant's trimmed justification; every other grant is revoked (D5).
func checkKeeps(reviewable []uuid.UUID, keep []Keep) (map[uuid.UUID]string, error) {
	kept := make(map[uuid.UUID]string, len(keep))
	for i, k := range keep {
		field := fmt.Sprintf("keep[%d].", i)
		if !slices.Contains(reviewable, k.GrantID) {
			return nil, platform.ValidationError(field+"grantId", "grant_not_open")
		}
		if _, ok := kept[k.GrantID]; ok {
			return nil, platform.ValidationError(field+"grantId", "duplicate_grant")
		}
		j := strings.TrimSpace(k.Justification)
		if j == "" {
			return nil, platform.ValidationError(field+"justification", "justification_required")
		}
		if err := platform.CheckText(j, field+"justification", 1000); err != nil {
			return nil, err
		}
		kept[k.GrantID] = j
	}
	return kept, nil
}
