package platform_test

import (
	"context"
	"testing"

	"github.com/esraahaytaa-eng/access-flow/internal/platform"
	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

// PERF-010: JIT compilation took 1.6–4 s of a 0.2 s point-in-time query at 12k grants, so
// the application's connections run without it.
func TestNewPoolTurnsJITOff(t *testing.T) {
	ctx := context.Background()
	pool, err := platform.NewPool(ctx, testutil.Env(t, "TEST_APP_DATABASE_URL"))
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	defer pool.Close()
	var jit string
	if err := pool.QueryRow(ctx, "SHOW jit").Scan(&jit); err != nil {
		t.Fatalf("SHOW jit: %v", err)
	}
	if jit != "off" {
		t.Fatalf("jit = %q; want off", jit)
	}
}
