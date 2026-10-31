package explorer

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/esraahaytaa-eng/access-flow/internal/auth"
	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

type Service struct {
	pool *pgxpool.Pool
	loc  *time.Location // company timezone: date filters mean whole days there (A6)
}

func NewService(pool *pgxpool.Pool, loc *time.Location) *Service {
	return &Service{pool: pool, loc: loc}
}

type Person struct {
	ID       uuid.UUID `json:"id"`
	FullName string    `json:"fullName"`
}

// person builds a Person from a nullable id and name.
func person(id *uuid.UUID, name *string) *Person {
	if id == nil || name == nil {
		return nil
	}
	return &Person{*id, *name}
}

// MaxEvents bounds the override and policy reports.
const MaxEvents = 1000

type Event struct {
	ID       uuid.UUID      `json:"id"`
	At       time.Time      `json:"at"`
	Actor    *Person        `json:"actor"` // nil: the system
	Action   string         `json:"action"`
	Entity   string         `json:"entity"`
	EntityID *uuid.UUID     `json:"entityId"`
	Subject  *Person        `json:"subject"`
	Reason   *string        `json:"reason"`
	Details  map[string]any `json:"details"`
}

type EventFilter struct {
	Actions  []string
	Actor    *uuid.UUID
	Tool     *uuid.UUID
	From, To *time.Time // dates in the company timezone, both days included
	Text     string
}

// toolEvents matches the events about one tool: the tool, its levels, and the requests,
// request items, approval steps, grants, operations and review items for it. Its eight
// "?" all take the tool id.
const toolEvents = `((e.entity_type = 'tool' AND e.entity_id = ?)
	OR (e.entity_type = 'access_level' AND EXISTS (SELECT 1 FROM access_levels x WHERE x.id = e.entity_id AND x.tool_id = ?))
	OR (e.entity_type = 'request' AND EXISTS (SELECT 1 FROM request_items x WHERE x.request_id = e.entity_id AND x.tool_id = ?))
	OR (e.entity_type = 'request_item' AND EXISTS (SELECT 1 FROM request_items x WHERE x.id = e.entity_id AND x.tool_id = ?))
	OR (e.entity_type = 'approval_step' AND EXISTS (SELECT 1 FROM approval_steps st JOIN request_items x ON x.id = st.item_id
		WHERE st.id = e.entity_id AND x.tool_id = ?))
	OR (e.entity_type = 'grant' AND EXISTS (SELECT 1 FROM grants x WHERE x.id = e.entity_id AND x.tool_id = ?))
	OR (e.entity_type = 'operation' AND EXISTS (SELECT 1 FROM operations o JOIN grants x ON x.id = o.grant_id
		WHERE o.id = e.entity_id AND x.tool_id = ?))
	OR (e.entity_type = 'review_item' AND EXISTS (SELECT 1 FROM review_items ri JOIN grants x ON x.id = ri.grant_id
		WHERE ri.id = e.entity_id AND x.tool_id = ?)))`

// eventWhere is the actor's scope and f as conditions on audit_events e.
func (s *Service) eventWhere(ctx context.Context, actor auth.Principal, f EventFilter) (Where, error) {
	var w Where
	scope, err := ScopeOf(ctx, s.pool, actor)
	if err != nil {
		return w, err
	}
	scope.Restrict(&w, "e.subject_employee_id")
	if len(f.Actions) > 0 {
		w.Add("e.action = ANY(?)", f.Actions)
	}
	if f.Actor != nil {
		w.Add("e.actor_id = ?", *f.Actor)
	}
	if t := f.Tool; t != nil {
		w.Add(toolEvents, *t, *t, *t, *t, *t, *t, *t, *t)
	}
	if f.From != nil {
		w.Add("e.occurred_at >= ?", *f.From)
	}
	if f.To != nil {
		w.Add("e.occurred_at < ?", f.To.AddDate(0, 0, 1))
	}
	if f.Text != "" {
		w.Add(`(e.action ILIKE ? OR e.reason ILIKE ? OR e.details::text ILIKE ? OR a.full_name ILIKE ? OR s.full_name ILIKE ?)`,
			Like(f.Text), Like(f.Text), Like(f.Text), Like(f.Text), Like(f.Text))
	}
	return w, nil
}

const eventSelect = `SELECT e.id, e.occurred_at, e.actor_id, a.full_name, e.action, e.entity_type, e.entity_id,
		e.subject_employee_id, s.full_name, e.reason, e.details`

const eventFrom = ` FROM audit_events e LEFT JOIN employees a ON a.id = e.actor_id LEFT JOIN employees s ON s.id = e.subject_employee_id
		WHERE `

// newestFirst orders by a unique key, so pages are deterministic; new events only push
// older ones to later pages, never skip them.
const newestFirst = ` ORDER BY e.occurred_at DESC, e.id DESC`

// Events is one page (platform.PageSize) of the audit log in the actor's scope, newest
// first, with the total of every matching event.
func (s *Service) Events(ctx context.Context, actor auth.Principal, f EventFilter, offset int) (events []Event, total int, err error) {
	w, err := s.eventWhere(ctx, actor, f)
	if err != nil {
		return nil, 0, err
	}
	page := newestFirst + ` LIMIT ` + strconv.Itoa(platform.PageSize) + ` OFFSET ` + strconv.Itoa(offset)
	if f.Text != "" {
		// A text search reads every event anyway, so it counts the matches in the same pass.
		rows, _ := s.pool.Query(ctx, eventSelect+`, count(*) OVER ()`+eventFrom+w.SQL()+page, w.Args...)
		events, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (Event, error) { return scanEvent(row, &total) })
		return events, total, err
	}
	// Otherwise the count is separate (PERF-011): in the page query it made PostgreSQL join and
	// sort every matching event before cutting the page, instead of walking the time index.
	// One read-only snapshot keeps the total and the page consistent.
	err = pgx.BeginTxFunc(ctx, s.pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT count(*)`+eventFrom+w.SQL(), w.Args...).Scan(&total); err != nil {
			return err
		}
		rows, _ := tx.Query(ctx, eventSelect+eventFrom+w.SQL()+page, w.Args...)
		events, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (Event, error) { return scanEvent(row) })
		return err
	})
	return events, total, err
}

// EachEvent checks the actor's scope, then returns a function that calls fn for every event
// matching f, newest first, streamed from one query (one consistent snapshot) for the CSV export.
func (s *Service) EachEvent(ctx context.Context, actor auth.Principal, f EventFilter) (func(fn func(Event) error) error, error) {
	w, err := s.eventWhere(ctx, actor, f)
	if err != nil {
		return nil, err
	}
	return func(fn func(Event) error) error {
		rows, err := s.pool.Query(ctx, eventSelect+eventFrom+w.SQL()+newestFirst, w.Args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			e, err := scanEvent(rows)
			if err == nil {
				err = fn(e)
			}
			if err != nil {
				return err
			}
		}
		return rows.Err()
	}, nil
}

func (s *Service) events(ctx context.Context, w Where, limit int) ([]Event, error) {
	rows, _ := s.pool.Query(ctx, eventSelect+eventFrom+w.SQL()+newestFirst+` LIMIT `+strconv.Itoa(limit), w.Args...)
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Event, error) { return scanEvent(row) })
}

// scanEvent reads one eventSelect row, then any extra columns into extra.
func scanEvent(row pgx.Row, extra ...any) (Event, error) {
	var e Event
	var actorID, subjectID *uuid.UUID
	var actorName, subjectName *string
	err := row.Scan(append([]any{&e.ID, &e.At, &actorID, &actorName, &e.Action, &e.Entity, &e.EntityID, &subjectID, &subjectName, &e.Reason, &e.Details}, extra...)...)
	e.Actor, e.Subject = person(actorID, actorName), person(subjectID, subjectName)
	return e, err
}

type Tool struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

// Filters are the choices of the event filters, without the employee directory: everyone
// who acted in an event in the actor's scope, and every tool, retired ones included.
func (s *Service) Filters(ctx context.Context, actor auth.Principal) (actors []Person, tools []Tool, err error) {
	w, err := s.eventWhere(ctx, actor, EventFilter{})
	if err != nil {
		return nil, nil, err
	}
	rows, _ := s.pool.Query(ctx, `SELECT a.id, a.full_name FROM employees a WHERE EXISTS (SELECT 1 FROM audit_events e
		WHERE e.actor_id = a.id AND `+w.SQL()+`) ORDER BY a.full_name, a.id`, w.Args...)
	if actors, err = pgx.CollectRows(rows, pgx.RowToStructByPos[Person]); err != nil {
		return nil, nil, err
	}
	rows, _ = s.pool.Query(ctx, "SELECT id, name FROM tools ORDER BY lower(name), id")
	tools, err = pgx.CollectRows(rows, pgx.RowToStructByPos[Tool])
	return actors, tools, err
}

type Approver struct {
	Type   string `json:"type"` // MANAGER, TOOL_OWNER, INFOSEC
	Status string `json:"status"`
	Name   string `json:"name"`
}

// Held is one grant effective at the queried instant, or removed in the 30 days before it.
type Held struct {
	GrantID         uuid.UUID  `json:"grantId"`
	Employee        *Person    `json:"employee"` // nil: an orphan account (D10)
	Account         *string    `json:"account"`
	Tool            string     `json:"tool"`
	Level           string     `json:"level"`
	Method          string     `json:"method"`
	State           string     `json:"state,omitempty"`
	Evidence        string     `json:"evidence"` // VERIFIED, SELF_REPORTED or IMPORTED
	ConfirmedBy     *Person    `json:"confirmedBy"`
	EffectiveFrom   time.Time  `json:"effectiveFrom"` // for imported access: when it was imported
	EffectiveTo     *time.Time `json:"effectiveTo"`
	RemovalEvidence *string    `json:"removalEvidence"`
	RemovedBy       *Person    `json:"removedBy"`
	Imported        bool       `json:"imported"`
	Direct          *Person    `json:"direct"` // a Super Admin's direct grant: who granted it (D1)
	Approvers       []Approver `json:"approvers"`
}

type AccessFilter struct {
	Employee, Tool *uuid.UUID
}

// Access is effective access at the instant at (O1, A9): held grants with their state at
// that instant, and the grants removed in the 30 days before it.
func (s *Service) Access(ctx context.Context, actor auth.Principal, at time.Time, f AccessFilter) (held, removed []Held, err error) {
	scope, err := ScopeOf(ctx, s.pool, actor)
	if err != nil {
		return nil, nil, err
	}
	filter := func(w *Where) {
		scope.Restrict(w, "g.employee_id")
		if f.Employee != nil {
			w.Add("g.employee_id = ?", *f.Employee)
		}
		if f.Tool != nil {
			w.Add("g.tool_id = ?", *f.Tool)
		}
	}
	var w Where
	filter(&w)
	w.Add(start+" <= ? AND (g.effective_to IS NULL OR g.effective_to > ?)", at, at)
	if held, err = s.held(ctx, w, at); err != nil {
		return nil, nil, err
	}
	w = Where{}
	filter(&w)
	w.Add("g.effective_to <= ? AND g.effective_to > ?", at, at.AddDate(0, 0, -30))
	removed, err = s.held(ctx, w, at)
	return held, removed, err
}

// start is when a grant became effective: its verified or confirmed activation, or for
// imported access the import, the first time AccessFlow recorded it (D10: never invented).
const start = "coalesce(g.effective_from, i.imported_at)"

// removalEvidence is how a removed grant's removal was proven: the tool's API, or a person's confirmation.
const removalEvidence = "CASE WHEN g.effective_to IS NOT NULL THEN CASE WHEN g.method = 'API' THEN 'VERIFIED' ELSE 'SELF_REPORTED' END END"

// overrideEvents and policyEvents select the D1 and F2 evidence events (the reports and the quarter's counts).
const (
	overrideEvents = `(e.action IN ('SUPER_ADMIN_OVERRIDE', 'DIRECT_GRANT_CREATED')
		OR e.action = 'GRANT_REVOCATION_REQUESTED' AND e.details->>'by' = 'DIRECT')`
	policyEvents = "(e.action = 'ACCESS_LEVEL_UPDATED' AND e.details ? 'steps')"
)

func (s *Service) held(ctx context.Context, w Where, at time.Time) ([]Held, error) {
	w.Args = append(w.Args, at)
	atArg := "$" + strconv.Itoa(len(w.Args))
	rows, _ := s.pool.Query(ctx, `SELECT g.id, g.employee_id, e.full_name, g.account, t.name, l.name, g.method,
			CASE WHEN g.import_id IS NOT NULL THEN 'IMPORTED' ELSE g.activation_evidence END,
			act.actor_id, act.full_name, `+start+`, g.effective_to,
			`+removalEvidence+`,
			rem.actor_id, rem.full_name, g.import_id IS NOT NULL,
			CASE WHEN r.kind = 'DIRECT_GRANT' THEN r.submitted_by END, sb.full_name,
			coalesce((SELECT json_agg(json_build_object('type', st.type, 'status', st.status, 'name', d.full_name) ORDER BY st.position)
				FROM approval_steps st JOIN employees d ON d.id = st.decided_by WHERE st.item_id = g.request_item_id), '[]'),
			(SELECT max(a.occurred_at) FROM audit_events a
				WHERE a.action = 'GRANT_REVOCATION_REQUESTED' AND a.entity_id = g.id AND a.occurred_at <= `+atArg+`),
			(SELECT max(a.occurred_at) FROM audit_events a JOIN operations o ON o.id = a.entity_id AND o.type = 'REVOKE'
				WHERE a.action = 'OPERATION_FAILED' AND o.grant_id = g.id AND a.occurred_at <= `+atArg+`),
			(SELECT max(a.occurred_at) FROM audit_events a JOIN operations o ON o.id = a.entity_id AND o.type = 'REVOKE'
				WHERE a.action = 'OPERATION_RETRIED' AND o.grant_id = g.id AND a.occurred_at <= `+atArg+`),
			EXISTS (SELECT 1 FROM grant_suspensions su WHERE su.grant_id = g.id AND su.effective_from <= `+atArg+`
				AND (su.effective_to IS NULL OR su.effective_to > `+atArg+`))
		FROM grants g
		JOIN tools t ON t.id = g.tool_id
		JOIN access_levels l ON l.id = g.access_level_id
		LEFT JOIN employees e ON e.id = g.employee_id
		LEFT JOIN imports i ON i.id = g.import_id
		LEFT JOIN request_items ri ON ri.id = g.request_item_id
		LEFT JOIN requests r ON r.id = ri.request_id
		LEFT JOIN employees sb ON sb.id = r.submitted_by
		LEFT JOIN LATERAL (SELECT a.actor_id, x.full_name FROM audit_events a JOIN employees x ON x.id = a.actor_id
			WHERE a.action = 'GRANT_ACTIVATED' AND a.entity_id = g.id ORDER BY a.occurred_at DESC LIMIT 1) act ON true
		LEFT JOIN LATERAL (SELECT a.actor_id, x.full_name FROM audit_events a JOIN employees x ON x.id = a.actor_id
			WHERE a.action = 'GRANT_REVOKED' AND a.entity_id = g.id ORDER BY a.occurred_at DESC LIMIT 1) rem ON true
		WHERE `+w.SQL()+`
		ORDER BY e.full_name NULLS LAST, g.account, t.name, l.name`, w.Args...)
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Held, error) {
		var h Held
		var employeeID, confirmerID, removerID, directID *uuid.UUID
		var employeeName, confirmerName, removerName, directName *string
		var requested, failed, retried *time.Time
		var suspended bool
		err := row.Scan(&h.GrantID, &employeeID, &employeeName, &h.Account, &h.Tool, &h.Level, &h.Method, &h.Evidence,
			&confirmerID, &confirmerName, &h.EffectiveFrom, &h.EffectiveTo, &h.RemovalEvidence, &removerID, &removerName, &h.Imported,
			&directID, &directName, &h.Approvers, &requested, &failed, &retried, &suspended)
		h.Employee, h.ConfirmedBy, h.RemovedBy, h.Direct = person(employeeID, employeeName), person(confirmerID, confirmerName),
			person(removerID, removerName), person(directID, directName)
		if h.EffectiveTo == nil || h.EffectiveTo.After(at) {
			h.State = StateAt(requested, failed, retried, suspended)
		}
		return h, err
	})
}

// requireFull: the evidence reports are company-wide only (O7, D11), and not for an Auditor,
// whatever other roles they hold (O7 note 2026-10-31).
func (s *Service) requireFull(ctx context.Context, actor auth.Principal) error {
	if actor.HasRole("AUDITOR") {
		return platform.ErrForbidden
	}
	scope, err := ScopeOf(ctx, s.pool, actor)
	if err == nil && !scope.Full {
		err = platform.ErrForbidden
	}
	return err
}

type Holder struct {
	Role      string    `json:"role"`  // SUPER_ADMIN, INFOSEC, HR_MANAGER, AUDITOR or ADMIN_GROUP
	Group     *string   `json:"group"` // the admin group's name
	Employee  Person    `json:"employee"`
	Since     time.Time `json:"since"`
	GrantedBy *Person   `json:"grantedBy"` // nil: the system (initial bootstrap)
}

// Privileged lists who held each AccessFlow role and admin group at the instant at (D11).
func (s *Service) Privileged(ctx context.Context, actor auth.Principal, at time.Time) ([]Holder, error) {
	if err := s.requireFull(ctx, actor); err != nil {
		return nil, err
	}
	rows, _ := s.pool.Query(ctx, `SELECT role, grp, employee_id, full_name, valid_from, granted_by, granter FROM (
			SELECT r.role, NULL::text AS grp, r.employee_id, e.full_name, r.valid_from, r.granted_by, b.full_name AS granter
			FROM role_assignments r JOIN employees e ON e.id = r.employee_id LEFT JOIN employees b ON b.id = r.granted_by
			WHERE r.valid_from <= $1 AND (r.valid_to IS NULL OR r.valid_to > $1)
			UNION ALL
			SELECT 'ADMIN_GROUP', g.name, m.employee_id, e.full_name, m.valid_from, m.granted_by, b.full_name
			FROM admin_group_memberships m JOIN admin_groups g ON g.id = m.group_id JOIN employees e ON e.id = m.employee_id
			LEFT JOIN employees b ON b.id = m.granted_by
			WHERE m.valid_from <= $1 AND (m.valid_to IS NULL OR m.valid_to > $1)) h
		ORDER BY array_position(ARRAY['SUPER_ADMIN', 'INFOSEC', 'HR_MANAGER', 'AUDITOR', 'ADMIN_GROUP'], role), grp, full_name`, at)
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Holder, error) {
		var h Holder
		var byID *uuid.UUID
		var byName *string
		err := row.Scan(&h.Role, &h.Group, &h.Employee.ID, &h.Employee.FullName, &h.Since, &byID, &byName)
		h.GrantedBy = person(byID, byName)
		return h, err
	})
}

// Overrides lists every Super Admin action that skipped approvals: overrides, direct
// grants and direct removals (D1).
func (s *Service) Overrides(ctx context.Context, actor auth.Principal) ([]Event, error) {
	if err := s.requireFull(ctx, actor); err != nil {
		return nil, err
	}
	var w Where
	w.Add(overrideEvents)
	return s.events(ctx, w, MaxEvents)
}

// PolicyChanges lists changes to access levels' approval steps (F2).
func (s *Service) PolicyChanges(ctx context.Context, actor auth.Principal) ([]Event, error) {
	if err := s.requireFull(ctx, actor); err != nil {
		return nil, err
	}
	var w Where
	w.Add(policyEvents)
	return s.events(ctx, w, MaxEvents)
}

type Leaver struct {
	Employee        Person     `json:"employee"`
	TerminatedAt    *time.Time `json:"terminatedAt"`
	Tool            string     `json:"tool"`
	Level           string     `json:"level"`
	Status          string     `json:"status"`
	EffectiveTo     *time.Time `json:"effectiveTo"`
	RemovalEvidence *string    `json:"removalEvidence"`
}

// Leavers lists every grant terminated employees held, and whether its removal is proven:
// the CASE's "14 people who had left still had accounts".
func (s *Service) Leavers(ctx context.Context, actor auth.Principal) ([]Leaver, error) {
	if err := s.requireFull(ctx, actor); err != nil {
		return nil, err
	}
	rows, _ := s.pool.Query(ctx, `SELECT e.id, e.full_name,
			(SELECT max(a.occurred_at) FROM audit_events a WHERE a.action = 'EMPLOYEE_TERMINATED' AND a.subject_employee_id = e.id),
			t.name, l.name, g.status, g.effective_to, `+removalEvidence+`
		FROM grants g JOIN employees e ON e.id = g.employee_id JOIN tools t ON t.id = g.tool_id JOIN access_levels l ON l.id = g.access_level_id
		WHERE e.status = 'TERMINATED' AND (g.effective_from IS NOT NULL OR g.import_id IS NOT NULL)
		ORDER BY e.full_name, t.name, l.name`)
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Leaver, error) {
		var l Leaver
		err := row.Scan(&l.Employee.ID, &l.Employee.FullName, &l.TerminatedAt, &l.Tool, &l.Level, &l.Status, &l.EffectiveTo, &l.RemovalEvidence)
		return l, err
	})
}

type Campaign struct {
	ID          uuid.UUID  `json:"id"`
	Kind        string     `json:"kind"`
	Quarter     *string    `json:"quarter"`
	StartedAt   time.Time  `json:"startedAt"`
	RemindedAt  *time.Time `json:"remindedAt"`
	EscalatedAt *time.Time `json:"escalatedAt"`
	ClosedAt    *time.Time `json:"closedAt"`
}

type QuarterCounts struct {
	Total          int `json:"total"`
	Kept           int `json:"kept"`
	Revoked        int `json:"revoked"`
	Unanswered     int `json:"unanswered"`
	AlreadyRemoved int `json:"alreadyRemoved"`
	AutoRevoked    int `json:"autoRevoked"`
	OpenFindings   int `json:"openFindings"` // carried over to the next quarter
	// CarriedFindings are earlier campaigns' findings still open; not part of Total.
	CarriedFindings int `json:"carriedFindings"`
}

type ReviewRow struct {
	Employee     string     `json:"employee"`
	Tool         string     `json:"tool"`
	Level        string     `json:"level"`
	Reviewer     *string    `json:"reviewer"` // nil: the InfoSec pool
	Decision     *string    `json:"decision"`
	DecidedBy    *string    `json:"decidedBy"`
	DecidedAt    *time.Time `json:"decidedAt"`
	Reason       *string    `json:"reason"`
	Late         bool       `json:"late"`
	ClosedReason *string    `json:"closedReason"`
	Finding      bool       `json:"finding"`
	CarriedOver  bool       `json:"carriedOver"` // an open finding from an earlier campaign
}

type Quarter struct {
	Campaign      *Campaign     `json:"campaign"` // nil: no campaign yet
	Counts        QuarterCounts `json:"counts"`
	PolicyChanges int           `json:"policyChanges"` // since the campaign started
	Overrides     int           `json:"overrides"`     // likewise
	Items         []ReviewRow   `json:"items"`
}

// LatestQuarter is the quarterly evidence package for the latest campaign (D8, D1, D11).
func (s *Service) LatestQuarter(ctx context.Context, actor auth.Principal) (Quarter, error) {
	q := Quarter{Items: []ReviewRow{}}
	if err := s.requireFull(ctx, actor); err != nil {
		return q, err
	}
	var c Campaign
	err := s.pool.QueryRow(ctx, `SELECT id, kind, quarter, started_at, reminded_at, escalated_at, closed_at FROM review_campaigns
		ORDER BY started_at DESC LIMIT 1`).Scan(&c.ID, &c.Kind, &c.Quarter, &c.StartedAt, &c.RemindedAt, &c.EscalatedAt, &c.ClosedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return q, nil
	}
	if err != nil {
		return q, err
	}
	q.Campaign = &c
	rows, _ := s.pool.Query(ctx, `SELECT coalesce(e.full_name, g.account), t.name, l.name, r.full_name, i.decision, d.full_name, i.decided_at,
			i.decision_reason, i.late, i.closed_reason, i.finding_at IS NOT NULL AND i.decision IS NULL AND i.closed_reason IS NULL,
			i.campaign_id <> $1
		FROM review_items i JOIN grants g ON g.id = i.grant_id JOIN tools t ON t.id = g.tool_id JOIN access_levels l ON l.id = g.access_level_id
		LEFT JOIN employees e ON e.id = g.employee_id LEFT JOIN employees r ON r.id = i.reviewer_id LEFT JOIN employees d ON d.id = i.decided_by
		WHERE i.campaign_id = $1 OR i.finding_at IS NOT NULL AND i.decision IS NULL AND i.closed_reason IS NULL
		ORDER BY 1, 2, 3`, c.ID)
	if q.Items, err = pgx.CollectRows(rows, pgx.RowToStructByPos[ReviewRow]); err != nil {
		return q, err
	}
	for _, it := range q.Items {
		if it.CarriedOver {
			q.Counts.CarriedFindings++
			continue
		}
		q.Counts.Total++
		switch {
		case it.Decision != nil && *it.Decision == "KEEP":
			q.Counts.Kept++
		case it.Decision != nil:
			q.Counts.Revoked++
		case it.ClosedReason != nil && *it.ClosedReason == "ALREADY_REMOVED":
			q.Counts.AlreadyRemoved++
		case it.ClosedReason != nil:
			q.Counts.AutoRevoked++
		case it.Finding:
			q.Counts.OpenFindings++
		default:
			q.Counts.Unanswered++
		}
	}
	err = s.pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE `+policyEvents+`), count(*) FILTER (WHERE `+overrideEvents+`)
		FROM audit_events e WHERE e.occurred_at >= $1`, c.StartedAt).Scan(&q.PolicyChanges, &q.Overrides)
	return q, err
}
