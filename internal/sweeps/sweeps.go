// Package sweeps runs the scheduled business jobs (O8, A6): in M8, temporary access
// expiry (D6). Each job is idempotent and catches up after downtime.
package sweeps

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/esraahaytaa-eng/access-flow/internal/access"
)

// Sweeps runs the jobs about once a minute.
type Sweeps struct {
	pool *pgxpool.Pool
	loc  *time.Location // company timezone
}

func New(pool *pgxpool.Pool, loc *time.Location) *Sweeps { return &Sweeps{pool: pool, loc: loc} }

// Run runs a round at once and then every minute until ctx ends.
func (s *Sweeps) Run(ctx context.Context) {
	for ctx.Err() == nil {
		if err := s.RunOnce(ctx); err != nil {
			slog.Error("sweeps", "error", err)
		}
		select {
		case <-ctx.Done():
		case <-time.After(time.Minute):
		}
	}
}

// RunOnce runs one round of every job.
func (s *Sweeps) RunOnce(ctx context.Context) error {
	n, err := access.ExpireDue(ctx, s.pool, s.loc)
	if n > 0 {
		slog.Info("temporary access expired", "grants", n)
	}
	return err
}
