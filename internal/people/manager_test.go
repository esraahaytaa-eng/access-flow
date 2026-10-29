package people

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

// A manager terminated while HR assigns them: the assignment waits for the termination and
// is then refused; it never commits a terminated manager.
func TestManagerTerminatedWhileAssigned(t *testing.T) {
	env := newEnv(t)
	_, c := env.user("HR_MANAGER")
	manager, _ := env.user()
	err := testutil.WhileLocked(t, env.db.App, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "UPDATE employees SET status = 'TERMINATED' WHERE id = $1", manager.ID)
		return err
	}, func() error {
		r := c.Do(http.MethodPost, "/api/v1/employees", map[string]any{"email": "new@example.com", "fullName": "New", "managerId": manager.ID})
		if r.Status != http.StatusBadRequest || r.Body["error"] != "manager_inactive" {
			return fmt.Errorf("status %d %v; want 400 manager_inactive", r.Status, r.Body)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
