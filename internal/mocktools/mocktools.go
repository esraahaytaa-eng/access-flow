// Package mocktools is the Mock Tool Service: a small stand-in for the internal tools that
// AccessFlow administers over an API (A1, A2). It has its own database and no knowledge of
// AccessFlow; it is reachable only on the internal Docker network and has no authentication.
package mocktools

import (
	"context"
	"embed"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

// Migrations holds the mock's own SQL migrations, applied at its start.
//
//go:embed migrations/*.sql
var Migrations embed.FS

const maxField = 254 // fits any email address, AccessFlow's account names

var (
	errNotGranted  = platform.NewError(http.StatusConflict, "not_granted")
	errSuspended   = platform.NewError(http.StatusConflict, "level_suspended")
	errReusedID    = platform.NewError(http.StatusConflict, "operation_id_reused")
	operationTypes = map[string]bool{"GRANT": true, "REVOKE": true, "SUSPEND": true, "RESUME": true}
)

// Operation is one grant, revoke, suspend or resume of one (tool, account, level).
type Operation struct {
	OperationID uuid.UUID `json:"operationId"`
	Type        string    `json:"type"`
	Tool        string    `json:"tool"`
	Account     string    `json:"account"`
	Level       string    `json:"level"`
}

// Done is an applied operation: the request echoed with its result.
type Done struct {
	Operation
	Result string `json:"result"` // APPLIED or NO_CHANGE
}

func (op Operation) validate() error {
	if op.OperationID == uuid.Nil {
		return platform.ValidationError("operationId", "invalid_operation")
	}
	if !operationTypes[op.Type] {
		return platform.ValidationError("type", "invalid_operation")
	}
	for _, f := range []struct{ name, value string }{{"tool", op.Tool}, {"account", op.Account}, {"level", op.Level}} {
		if f.value == "" || len(f.value) > maxField {
			return platform.ValidationError(f.name, "invalid_operation")
		}
	}
	return nil
}

// Handler serves the mock's HTTP API.
func Handler(pool *pgxpool.Pool) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /healthz", platform.HealthHandler(pool.Ping))
	mux.HandleFunc("POST /v1/operations", func(w http.ResponseWriter, r *http.Request) {
		var op Operation
		if err := platform.DecodeJSON(w, r, &op); err != nil {
			platform.WriteError(w, r, err)
			return
		}
		if err := op.validate(); err != nil {
			platform.WriteError(w, r, err)
			return
		}
		done, err := apply(r.Context(), pool, op)
		if err != nil {
			platform.WriteError(w, r, err)
			return
		}
		platform.WriteJSON(w, http.StatusOK, done)
	})
	mux.HandleFunc("GET /v1/tools/{tool}/accounts/{account}", func(w http.ResponseWriter, r *http.Request) {
		state, err := lookup(r.Context(), pool, r.PathValue("tool"), r.PathValue("account"))
		if err != nil {
			platform.WriteError(w, r, err)
			return
		}
		platform.WriteJSON(w, http.StatusOK, state)
	})
	return mux
}

// apply runs op once per operation ID. A repeat with the same content returns the stored
// result; the same ID with other content is rejected. Only applied operations are stored: a
// rejected one (409) rolls back, so a retry with its ID is evaluated again.
func apply(ctx context.Context, pool *pgxpool.Pool, op Operation) (Done, error) {
	done := Done{Operation: op}
	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		// A concurrent insert of the same ID waits here until the first transaction ends.
		tag, err := tx.Exec(ctx, `INSERT INTO operations (operation_id, type, tool, account, level) VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (operation_id) DO NOTHING`, op.OperationID, op.Type, op.Tool, op.Account, op.Level)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			var stored Done
			if err := tx.QueryRow(ctx, "SELECT operation_id, type, tool, account, level, result FROM operations WHERE operation_id = $1",
				op.OperationID).Scan(&stored.OperationID, &stored.Type, &stored.Tool, &stored.Account, &stored.Level, &stored.Result); err != nil {
				return err
			}
			if stored.Operation != op {
				return errReusedID
			}
			done = stored
			return nil
		}
		if done.Result, err = effect(ctx, tx, op); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, "UPDATE operations SET result = $2 WHERE operation_id = $1", op.OperationID, done.Result)
		return err
	})
	return done, err
}

// effect changes the permission and reports whether anything changed. Each statement
// that might race with another operation on the same level re-reads in a new statement.
func effect(ctx context.Context, tx pgx.Tx, op Operation) (string, error) {
	key := []any{op.Tool, op.Account, op.Level}
	changed := func(sql string, extra ...any) (bool, error) {
		tag, err := tx.Exec(ctx, sql, append(key, extra...)...)
		return tag.RowsAffected() > 0, err
	}
	status := func() (string, error) {
		var s string
		err := tx.QueryRow(ctx, "SELECT status FROM permissions WHERE tool = $1 AND account = $2 AND level = $3", key...).Scan(&s)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return s, err
	}
	result := func(ok bool, err error) (string, error) {
		if err != nil {
			return "", err
		}
		if ok {
			return "APPLIED", nil
		}
		return "NO_CHANGE", nil
	}

	switch op.Type {
	case "GRANT":
		if _, err := tx.Exec(ctx, "INSERT INTO accounts (tool, account) VALUES ($1, $2) ON CONFLICT DO NOTHING", op.Tool, op.Account); err != nil {
			return "", err
		}
		ok, err := changed("INSERT INTO permissions (tool, account, level, status) VALUES ($1, $2, $3, 'ACTIVE') ON CONFLICT DO NOTHING")
		if err != nil || ok {
			return result(ok, err)
		}
		s, err := status()
		if err == nil && s == "SUSPENDED" {
			return "", errSuspended // only RESUME brings a suspended level back
		}
		return result(false, err)
	case "REVOKE":
		return result(changed("DELETE FROM permissions WHERE tool = $1 AND account = $2 AND level = $3"))
	default: // SUSPEND, RESUME
		from, to := "ACTIVE", "SUSPENDED"
		if op.Type == "RESUME" {
			from, to = to, from
		}
		ok, err := changed(`UPDATE permissions SET status = $5, updated_at = now()
			WHERE tool = $1 AND account = $2 AND level = $3 AND status = $4`, from, to)
		if err != nil || ok {
			return result(ok, err)
		}
		s, err := status()
		if err == nil && s == "" {
			return "", errNotGranted
		}
		return result(false, err)
	}
}

// AccountState is the lookup answer. An unknown account is Exists=false with no levels,
// never 404, so "removed" cannot be confused with a wrong URL.
type AccountState struct {
	Tool    string        `json:"tool"`
	Account string        `json:"account"`
	Exists  bool          `json:"exists"`
	Levels  []LevelStatus `json:"levels"`
}

// LevelStatus is one permission of an account.
type LevelStatus struct {
	Level  string `json:"level"`
	Status string `json:"status"`
}

func lookup(ctx context.Context, pool *pgxpool.Pool, tool, account string) (AccountState, error) {
	state := AccountState{Tool: tool, Account: account, Levels: []LevelStatus{}}
	if err := pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM accounts WHERE tool = $1 AND account = $2)", tool, account).Scan(&state.Exists); err != nil {
		return state, err
	}
	rows, err := pool.Query(ctx, "SELECT level, status FROM permissions WHERE tool = $1 AND account = $2 ORDER BY level", tool, account)
	if err != nil {
		return state, err
	}
	levels, err := pgx.CollectRows(rows, pgx.RowToStructByPos[LevelStatus])
	if err != nil {
		return state, err
	}
	state.Levels = append(state.Levels, levels...)
	return state, nil
}
