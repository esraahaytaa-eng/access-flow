package access

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/esraahaytaa-eng/access-flow/internal/audit"
	"github.com/esraahaytaa-eng/access-flow/internal/auth"
	"github.com/esraahaytaa-eng/access-flow/internal/explorer"
	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

var (
	errTaskClaimed  = platform.NewError(http.StatusConflict, "task_claimed")
	errTaskClosed   = platform.NewError(http.StatusConflict, "task_closed")
	errTaskNotReady = platform.NewError(http.StatusConflict, "task_not_ready")
	errTicket       = platform.NewError(http.StatusConflict, "ticket_required")
	errNotVendor    = platform.NewError(http.StatusConflict, "not_a_vendor_task")
	errLevelRetired = platform.NewError(http.StatusConflict, "level_retired")
	errNotClaimer   = platform.NewError(http.StatusForbidden, "not_claimer")
)

// member is the SQL test that the employee in arg is an active member of tool t's admin group.
func member(arg string) string {
	return `EXISTS (SELECT 1 FROM admin_group_memberships m
		WHERE m.group_id = t.admin_group_id AND m.employee_id = ` + arg + ` AND m.valid_to IS NULL)`
}

// initiates is the SQL test that the employee in arg initiated task o's direct action: the
// Super Admin of a direct grant or a direct revoke never works its task (D1).
func initiates(arg string) string {
	return `((o.type = 'GRANT' AND EXISTS (SELECT 1 FROM request_items di JOIN requests dr ON dr.id = di.request_id
		WHERE di.id = g.request_item_id AND dr.kind = 'DIRECT_GRANT' AND dr.submitted_by = ` + arg + `))
		OR (o.type = 'REVOKE' AND o.reason = 'DIRECT' AND g.revocation_requested_by = ` + arg + `))`
}

// waitsForEarlier is true while an older operation of operation o's grant is queued or
// running: a grant's operations reach the tool in the order they were created, so a
// removal goes after the GRANT that may still apply access (D5) and a RESUME after an
// in-flight SUSPEND (D7). Creation order is the id (uuidv7, set at insert), not created_at:
// that is the transaction's start time, earlier than a row committed while it waited for a lock.
const waitsForEarlier = `EXISTS (SELECT 1 FROM operations x WHERE x.grant_id = o.grant_id
	AND x.status IN ('PENDING', 'IN_PROGRESS') AND x.id < o.id)`

// Task is a Manual or Vendor operation in its admin group's queue (O4, O7).
type Task struct {
	ID            uuid.UUID  `json:"id"`
	GrantID       uuid.UUID  `json:"grantId"`
	Type          string     `json:"type"`
	Status        string     `json:"status"`
	Method        string     `json:"method"`
	GroupID       uuid.UUID  `json:"groupId"`
	Group         string     `json:"group"`
	ToolID        uuid.UUID  `json:"toolId"`
	Tool          string     `json:"tool"`
	AccessLevelID uuid.UUID  `json:"accessLevelId"`
	Level         string     `json:"level"`
	Employee      Person     `json:"employee"`  // an orphan account (D10): uuid.Nil, named by its account
	Account       string     `json:"account"`   // the account to act on in the tool
	RequestID     *uuid.UUID `json:"requestId"` // nil for an imported grant (D10)
	ItemID        *uuid.UUID `json:"itemId"`
	TicketRef     *string    `json:"ticketRef"`
	ClaimedBy     *Person    `json:"claimedBy"`
	CanAct        bool       `json:"canAct"` // false on the actor's own grant (F2) or direct action (D1)
	Version       int        `json:"version"`
	CreatedAt     time.Time  `json:"createdAt"`
}

// taskFrom joins an operation to what a task shows.
const taskFrom = ` FROM operations o JOIN grants g ON g.id = o.grant_id LEFT JOIN employees e ON e.id = g.employee_id
		JOIN tools t ON t.id = g.tool_id JOIN admin_groups ag ON ag.id = t.admin_group_id
		JOIN access_levels l ON l.id = g.access_level_id LEFT JOIN request_items i ON i.id = g.request_item_id
		LEFT JOIN employees c ON c.id = o.claimed_by`

// tasks lists a page of the Manual and Vendor operations matching where, ordered by a unique
// key; $1 is the actor.
func (s *Service) tasks(ctx context.Context, actor uuid.UUID, where string, limit, offset int, args ...any) ([]Task, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT o.id, g.id, o.type, o.status, g.method, ag.id, ag.name, t.id, t.name, l.id, l.name, `+holder+`,
		       coalesce(e.full_name, g.account), coalesce(g.account, e.email), i.request_id, i.id, o.ticket_ref, c.id, c.full_name,
		       `+canAct+`, o.version, o.created_at`+taskFrom+`
		WHERE g.method <> 'API' AND `+where+` ORDER BY o.priority, o.created_at, o.id LIMIT `+strconv.Itoa(limit)+` OFFSET `+strconv.Itoa(offset),
		append([]any{actor}, args...)...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Task, error) {
		var t Task
		var claimer *uuid.UUID
		var claimerName *string
		err := row.Scan(&t.ID, &t.GrantID, &t.Type, &t.Status, &t.Method, &t.GroupID, &t.Group, &t.ToolID, &t.Tool,
			&t.AccessLevelID, &t.Level, &t.Employee.ID, &t.Employee.FullName, &t.Account, &t.RequestID, &t.ItemID, &t.TicketRef,
			&claimer, &claimerName, &t.CanAct, &t.Version, &t.CreatedAt)
		if claimer != nil {
			t.ClaimedBy = &Person{ID: *claimer, FullName: *claimerName}
		}
		return t, err
	})
}

// canAct: never on the actor's own grant (F2) or a direct action they initiated (D1).
var canAct = "g.employee_id IS DISTINCT FROM $1 AND NOT " + initiates("$1")

// TaskPage is one page of the actor's queue in one tab, with every tab's count (PERF-004).
type TaskPage struct {
	Tasks    []Task     `json:"tasks"`
	Total    int        `json:"total"` // in the tab, matching the search
	Page     int        `json:"page"`
	PageSize int        `json:"pageSize"`
	Counts   TaskCounts `json:"counts"`
}

// TaskCounts are over the actor's whole queue, whatever the search.
type TaskCounts struct {
	Queue  int `json:"queue"`
	Mine   int `json:"mine"`
	Others int `json:"others"`
	CanAct int `json:"canAct"`
}

// taskTabs: the queue is unclaimed; claimed tasks are the actor's or another member's. No tab: all.
var taskTabs = map[string]string{"": "true", "queue": "o.status = 'PENDING'",
	"mine": "o.status <> 'PENDING' AND o.claimed_by = $1", "others": "o.status <> 'PENDING' AND o.claimed_by IS DISTINCT FROM $1"}

var errInvalidTab = platform.ValidationError("tab", "invalid_tab")

// Tasks is one page of the actor's Fulfillment Tasks queue: the open tasks of their admin
// groups, by priority (A6: leavers' removals, then other removals and suspensions, then
// grants and resumes), then oldest first. A task is listed once every older task of the same
// grant has ended (waitsForEarlier). search matches the person or account and the tool.
func (s *Service) Tasks(ctx context.Context, actor auth.Principal, tab, search string, page, offset int) (TaskPage, error) {
	out := TaskPage{Page: page, PageSize: platform.PageSize, Tasks: []Task{}}
	cond, ok := taskTabs[tab]
	if !ok {
		return out, errInvalidTab
	}
	var like *string
	if t := strings.TrimSpace(search); t != "" {
		like = new(explorer.Like(t))
	}
	queue := "o.status IN ('PENDING', 'IN_PROGRESS') AND " + member("$1") + " AND NOT " + waitsForEarlier
	matches := "($2::text IS NULL OR (coalesce(e.full_name, g.account) || ' ' || t.name) ILIKE $2)"
	c := &out.Counts
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE `+taskTabs["queue"]+`), count(*) FILTER (WHERE `+taskTabs["mine"]+`),
			count(*) FILTER (WHERE `+taskTabs["others"]+`), count(*) FILTER (WHERE `+canAct+`), count(*) FILTER (WHERE `+cond+` AND `+matches+`)`+taskFrom+`
		WHERE g.method <> 'API' AND `+queue, actor.EmployeeID, like).Scan(&c.Queue, &c.Mine, &c.Others, &c.CanAct, &out.Total); err != nil {
		return out, err
	}
	var err error
	out.Tasks, err = s.tasks(ctx, actor.EmployeeID, queue+" AND "+cond+" AND "+matches, platform.PageSize, offset, like)
	return out, err
}

// lockedTask is a task locked for an action, with what the actions decide on.
type lockedTask struct {
	grant, employee            uuid.UUID
	typ, method, status        string
	version                    int
	ticket                     *string
	mine, claimerActive, fresh bool // claimed by the actor; claimer still a member; never sent
}

// taskAction runs do on the task locked for actor, in one transaction, and returns the
// task. Only an active member of the tool's admin group acts, never on their own grant
// or on a direct action they initiated.
func (s *Service) taskAction(ctx context.Context, actor auth.Principal, id uuid.UUID,
	do func(tx pgx.Tx, now time.Time, t lockedTask) error) (Task, error) {
	err := platform.InTx(ctx, s.pool, func(tx pgx.Tx, now time.Time) error {
		var t lockedTask
		var isMember, initiator bool
		err := tx.QueryRow(ctx, `SELECT g.id, `+holder+`, g.method, `+member("$2")+`, `+initiates("$2")+`
			FROM operations o JOIN grants g ON g.id = o.grant_id JOIN tools t ON t.id = g.tool_id
			WHERE o.id = $1 AND g.method <> 'API'`, id, actor.EmployeeID).Scan(&t.grant, &t.employee, &t.method, &isMember, &initiator)
		if errors.Is(err, pgx.ErrNoRows) {
			return platform.ErrNotFound
		}
		if err != nil {
			return err
		}
		if !isMember {
			return platform.ErrForbidden
		}
		if t.employee == actor.EmployeeID || initiator {
			return errSeparation
		}
		// Lock order: employee, operations, grant (see Runner); then read.
		if _, err := tx.Exec(ctx, "SELECT 1 FROM employees WHERE id = $1 FOR UPDATE", t.employee); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "SELECT 1 FROM operations WHERE id = $1 FOR UPDATE", id); err != nil {
			return err
		}
		if err := StillActive(ctx, tx, actor.EmployeeID); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT o.type, o.status, o.version, o.ticket_ref, o.claimed_by IS NOT DISTINCT FROM $2,
			       o.claimed_by IS NOT NULL AND `+member("o.claimed_by")+`
			       AND (SELECT status FROM employees WHERE id = o.claimed_by) = 'ACTIVE', o.first_attempt_at IS NULL
			FROM operations o JOIN grants g ON g.id = o.grant_id JOIN tools t ON t.id = g.tool_id WHERE o.id = $1`,
			id, actor.EmployeeID).Scan(&t.typ, &t.status, &t.version, &t.ticket, &t.mine, &t.claimerActive, &t.fresh); err != nil {
			return err
		}
		return do(tx, now, t)
	})
	if err != nil {
		return Task{}, err
	}
	ts, err := s.tasks(ctx, actor.EmployeeID, "o.id = $2", 1, 0, id)
	if err != nil || len(ts) == 0 {
		return Task{}, err
	}
	return ts[0], nil
}

// StillActive re-checks the actor under the caller's locks: the session was checked before
// them, and a suspension or termination committing meanwhile ends their authority (D7).
func StillActive(ctx context.Context, tx pgx.Tx, actor uuid.UUID) error {
	var status string
	if err := tx.QueryRow(ctx, "SELECT status FROM employees WHERE id = $1 FOR SHARE", actor).Scan(&status); err != nil {
		return err
	}
	if status != "ACTIVE" {
		return platform.ErrUnauthorized
	}
	return nil
}

// claimedBy checks that the actor works this open task, at the version they saw.
func (t lockedTask) claimedBy(version int) error {
	switch {
	case t.status != "PENDING" && t.status != "IN_PROGRESS":
		return errTaskClosed
	case t.status != "IN_PROGRESS" || !t.mine:
		return errNotClaimer
	case t.version != version:
		return errVersionConflict
	}
	return nil
}

// Claim takes a queued task, or takes over one whose claimer has left the group or is no
// longer an active employee (D7). A grant task never sent whose level or tool was retired
// is cancelled instead (409 level_retired).
func (s *Service) Claim(ctx context.Context, actor auth.Principal, id uuid.UUID, version int) (Task, error) {
	retired := false
	task, err := s.taskAction(ctx, actor, id, func(tx pgx.Tx, now time.Time, t lockedTask) error {
		takeover := false
		switch {
		case t.status == "IN_PROGRESS" && t.mine:
			return nil
		case t.status == "IN_PROGRESS" && t.claimerActive:
			return errTaskClaimed
		case t.status == "IN_PROGRESS":
			takeover = true
		case t.status != "PENDING":
			return errTaskClosed
		}
		if t.version != version {
			return errVersionConflict
		}
		var waits bool
		if err := tx.QueryRow(ctx, "SELECT "+waitsForEarlier+" FROM operations o WHERE o.id = $1", id).Scan(&waits); err != nil {
			return err
		}
		if waits {
			return errTaskNotReady
		}
		if t.typ == "GRANT" && t.fresh {
			// shortcut: the tool's FOR SHARE comes after the employee's lock, the reverse of catalog
			// UpdateTool (tool, then the new owner), as in Submit; making the beneficiary the tool's
			// owner during this claim can deadlock; PostgreSQL aborts one and platform.InTx retries it.
			cancelled, err := cancelIfRetired(ctx, tx, now, &actor.EmployeeID, t.grant, t.employee)
			if err != nil || cancelled {
				retired = cancelled
				return err
			}
		}
		if err := setOp(ctx, tx, id, t.status, "IN_PROGRESS", "claimed_by = $2, first_attempt_at = coalesce(first_attempt_at, $3)",
			actor.EmployeeID, now); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{ActorID: &actor.EmployeeID, Action: "TASK_CLAIMED", Entity: "operation", EntityID: &id,
			SubjectID: &t.employee, Details: map[string]any{"takeover": takeover}})
	})
	if err == nil && retired {
		return Task{}, errLevelRetired
	}
	return task, err
}

// RecordTicket records the vendor's ticket reference on a claimed Vendor task.
func (s *Service) RecordTicket(ctx context.Context, actor auth.Principal, id uuid.UUID, version int, ref string) (Task, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return Task{}, platform.ValidationError("ticketRef", "ticket_ref_required")
	}
	if err := platform.CheckText(ref, "ticketRef", 100); err != nil {
		return Task{}, err
	}
	return s.taskAction(ctx, actor, id, func(tx pgx.Tx, now time.Time, t lockedTask) error {
		if t.status == "IN_PROGRESS" && t.mine && t.ticket != nil && *t.ticket == ref {
			return nil
		}
		if err := t.claimedBy(version); err != nil {
			return err
		}
		if t.method != "VENDOR" {
			return errNotVendor
		}
		if err := setOp(ctx, tx, id, t.status, t.status, "ticket_ref = $2", ref); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{ActorID: &actor.EmployeeID, Action: "TICKET_RECORDED", Entity: "operation", EntityID: &id,
			SubjectID: &t.employee, Details: map[string]any{"ticketRef": ref}})
	})
}

// Confirm records that the claimer did the work: SELF_REPORTED evidence (A9). A Vendor
// task needs its ticket reference.
func (s *Service) Confirm(ctx context.Context, actor auth.Principal, id uuid.UUID, version int) (Task, error) {
	return s.taskAction(ctx, actor, id, func(tx pgx.Tx, now time.Time, t lockedTask) error {
		if t.status == "SUCCEEDED" && t.mine {
			return nil
		}
		if err := t.claimedBy(version); err != nil {
			return err
		}
		if t.method == "VENDOR" && t.ticket == nil {
			return errTicket
		}
		grantStatus, err := lockGrant(ctx, tx, t.grant)
		if err != nil {
			return err
		}
		details := map[string]any{}
		if t.ticket != nil {
			details["ticketRef"] = *t.ticket
		}
		return succeed(ctx, tx, now, &actor.EmployeeID, id, t.grant, t.employee, t.typ, grantStatus, "SELF_REPORTED", details)
	})
}

// Fail records that the claimer could not do the work; the task waits in Exceptions for a retry.
func (s *Service) Fail(ctx context.Context, actor auth.Principal, id uuid.UUID, version int, reason string) (Task, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return Task{}, platform.ValidationError("reason", "reason_required")
	}
	if err := platform.CheckText(reason, "reason", 500); err != nil {
		return Task{}, err
	}
	return s.taskAction(ctx, actor, id, func(tx pgx.Tx, now time.Time, t lockedTask) error {
		if (t.status == "FAILED" || t.status == "CANCELLED") && t.mine {
			return nil
		}
		if err := t.claimedBy(version); err != nil {
			return err
		}
		grantStatus, err := lockGrant(ctx, tx, t.grant)
		if err != nil {
			return err
		}
		return fail(ctx, tx, now, &actor.EmployeeID, id, t.grant, t.employee, t.typ, grantStatus, reason, nil)
	})
}
