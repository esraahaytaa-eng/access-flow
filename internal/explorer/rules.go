package explorer

import "time"

// Held states at an instant (O1). Requested and failed removals still count as access.
const (
	StateActive              = "ACTIVE"
	StateSuspended           = "SUSPENDED"
	StateRevocationRequested = "REVOCATION_REQUESTED"
	StateRevocationFailed    = "REVOCATION_FAILED"
)

// StateAt is a held grant's O1 state at an instant, from the latest removal request,
// removal failure and retry recorded up to that instant (nil: none yet), and whether a
// suspension covered it. A retry after a failure means the removal is requested again.
func StateAt(requested, failed, retried *time.Time, suspended bool) string {
	switch {
	case requested == nil && suspended:
		return StateSuspended
	case requested == nil:
		return StateActive
	case failed != nil && !failed.Before(*requested) && (retried == nil || retried.Before(*failed)):
		return StateRevocationFailed
	default:
		return StateRevocationRequested
	}
}
