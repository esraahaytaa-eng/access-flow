package catalog

import (
	"context"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

// An owner terminated while a Super Admin assigns them: the assignment waits for the
// termination and is then refused.
func TestOwnerTerminatedWhileAssigned(t *testing.T) {
	env := newEnv(t)
	_, admin := env.user("SUPER_ADMIN")
	owner := testutil.CreateEmployee(t, env.db, testutil.EmployeeSpec{})
	group := testutil.CreateGroup(t, env.db)
	err := testutil.WhileLocked(t, env.db.App, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "UPDATE employees SET status = 'TERMINATED' WHERE id = $1", owner.ID)
		return err
	}, func() error {
		r := admin.Do("POST", "/api/v1/tools", map[string]any{"name": "Jira", "fulfillmentMethod": "API", "ownerId": owner.ID, "adminGroupId": group})
		if r.Status != 400 || r.Body["error"] != "owner_not_active" {
			return fmt.Errorf("status %d %v; want 400 owner_not_active", r.Status, r.Body)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
