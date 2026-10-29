// Package sweeps runs the scheduled business jobs (O8, A6): in M8, temporary access
// expiry (D6); in M9, scheduled terminations (D7). Each job is idempotent and catches up
// after downtime.
package sweeps

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/esraahaytaa-eng/access-flow/internal/access"
)

// Sweeps runs the jobs about once a minute.
type Sweeps struct {
	pool         *pgxpool.Pool
	loc          *time.Location // company timezone
	terminateDue Job            // requests.TerminateDue, passed in: requests' tests use this package
}

// Job runs one scheduled job and returns how many records it changed.
type Job func(context.Context, *pgxpool.Pool) (int, error)

func New(pool *pgxpool.Pool, loc *time.Location, terminateDue Job) *Sweeps {
	return &Sweeps{pool: pool, loc: loc, terminateDue: terminateDue}
}

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
	n, expiryErr := access.ExpireDue(ctx, s.pool, s.loc)
	if n > 0 {
		slog.Info("temporary access expired", "grants", n)
	}
	n, err := s.terminateDue(ctx, s.pool)
	if n > 0 {
		slog.Info("scheduled terminations run", "employees", n)
	}
	return errors.Join(expiryErr, err)
}
