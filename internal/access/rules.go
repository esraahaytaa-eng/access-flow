package access

import (
	"slices"
	"time"
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
	if status != "ACTIVE" && status != "REVOKED" {
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
	"ACTIVE":               {"REVOCATION_REQUESTED"},
	"FAILED":               {"PROVISIONING", "PENDING_FULFILLMENT", "REVOCATION_REQUESTED"},
	"REVOCATION_REQUESTED": {"REVOKED", "REVOCATION_FAILED"},
	"REVOCATION_FAILED":    {"REVOCATION_REQUESTED"},
}

var opMoves = map[string][]string{
	"PENDING":     {"IN_PROGRESS", "CANCELLED"},
	"IN_PROGRESS": {"SUCCEEDED", "PENDING", "FAILED", "CANCELLED"},
	"FAILED":      {"PENDING", "CANCELLED"},
}

func canMove(moves map[string][]string, from, to string) bool {
	return slices.Contains(moves[from], to)
}
