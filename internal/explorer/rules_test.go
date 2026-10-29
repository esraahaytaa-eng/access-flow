package explorer

import (
	"testing"
	"time"
)

func TestStateAt(t *testing.T) {
	t0 := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	at := func(m int) *time.Time { v := t0.Add(time.Duration(m) * time.Minute); return &v }
	for _, c := range []struct {
		name                       string
		requested, failed, retried *time.Time
		suspended                  bool
		want                       string
	}{
		{"active", nil, nil, nil, false, StateActive},
		{"suspended", nil, nil, nil, true, StateSuspended},
		{"removal requested", at(1), nil, nil, false, StateRevocationRequested},
		{"removal requested while suspended", at(1), nil, nil, true, StateRevocationRequested},
		{"removal failed", at(1), at(2), nil, false, StateRevocationFailed},
		{"failed, then retried", at(1), at(2), at(3), false, StateRevocationRequested},
		{"retried, failed again", at(1), at(4), at(3), false, StateRevocationFailed},
		{"an earlier removal's failure", at(5), at(2), nil, false, StateRevocationRequested},
	} {
		if got := StateAt(c.requested, c.failed, c.retried, c.suspended); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}
