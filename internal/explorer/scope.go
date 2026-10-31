// Package explorer is the read-only Audit Explorer (CASE §5, O1, O7, D1, D11): audit
// events, point-in-time effective access and the evidence reports. It writes nothing.
package explorer

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/esraahaytaa-eng/access-flow/internal/auth"
	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

// Scope is what the actor may see in the Audit Explorer (O7, approved 2026-10-28).
type Scope struct {
	Full      bool        // Super Admin, InfoSec, Auditor (audit evidence only): company-wide
	Employees []uuid.UUID // otherwise a Manager's current direct and indirect reports
}

// ScopeOf is the one scope rule every audit, point-in-time and report query uses. A
// Manager sees their current reports' whole history and nothing else: former reports,
// other people and rows about no employee are excluded. Anyone else gets 403.
func ScopeOf(ctx context.Context, pool *pgxpool.Pool, actor auth.Principal) (Scope, error) {
	if actor.HasRole("SUPER_ADMIN") || actor.HasRole("INFOSEC") || actor.HasRole("AUDITOR") {
		return Scope{Full: true}, nil
	}
	rows, _ := pool.Query(ctx, `WITH RECURSIVE reports AS (
			SELECT id FROM employees WHERE manager_id = $1
			UNION SELECT e.id FROM employees e JOIN reports r ON e.manager_id = r.id)
		SELECT id FROM reports`, actor.EmployeeID)
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return Scope{}, err
	}
	if len(ids) == 0 {
		return Scope{}, platform.ErrForbidden
	}
	return Scope{Employees: ids}, nil
}

// Restrict limits w to rows whose employee column col is in the scope.
func (s Scope) Restrict(w *Where, col string) {
	if !s.Full {
		w.Add(col+" = ANY(?)", s.Employees)
	}
}

// Where collects AND-ed SQL conditions; each "?" becomes the next "$n".
type Where struct {
	conds []string
	Args  []any
}

func (w *Where) Add(cond string, args ...any) {
	for _, a := range args {
		w.Args = append(w.Args, a)
		cond = strings.Replace(cond, "?", fmt.Sprintf("$%d", len(w.Args)), 1)
	}
	w.conds = append(w.conds, cond)
}

// SQL is the WHERE clause, or "true".
func (w *Where) SQL() string {
	if len(w.conds) == 0 {
		return "true"
	}
	return strings.Join(w.conds, " AND ")
}

// Like is a case-insensitive "contains" pattern for q, with LIKE wildcards escaped.
func Like(q string) string {
	return "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(q) + "%"
}
