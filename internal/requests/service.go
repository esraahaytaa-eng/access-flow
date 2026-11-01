package requests

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/esraahaytaa-eng/access-flow/internal/access"
	"github.com/esraahaytaa-eng/access-flow/internal/audit"
	"github.com/esraahaytaa-eng/access-flow/internal/auth"
	"github.com/esraahaytaa-eng/access-flow/internal/catalog"
	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

var (
	errSubmissionReused = platform.NewError(http.StatusConflict, "submission_id_reused")
	errItemNotPending   = platform.NewError(http.StatusConflict, "item_not_pending")
	errStepDecided      = platform.NewError(http.StatusConflict, "step_already_decided")
	errStepNotOpen      = platform.NewError(http.StatusConflict, "step_not_open")
	errVersionConflict  = platform.NewError(http.StatusConflict, "version_conflict")
)

const (
	maxItems      = 20
	maxTextLength = 1000 // justifications and reasons
)

// Service handles requests and approvals.
type Service struct {
	pool *pgxpool.Pool
	loc  *time.Location // company timezone (A6)
	// Accounts looks up tool-specific accounts (the GitHub adapter); nil when none is
	// configured, and then a tool with an account kind cannot be requested.
	Accounts AccountLookup
}

func NewService(pool *pgxpool.Pool, loc *time.Location) *Service {
	return &Service{pool: pool, loc: loc}
}

// Person names an employee.
type Person struct {
	ID       uuid.UUID `json:"id"`
	FullName string    `json:"fullName"`
}

// Step is one approval step of an item.
type Step struct {
	ID            uuid.UUID  `json:"id"`
	Position      int        `json:"position"`
	Type          string     `json:"type"`
	Status        string     `json:"status"`
	AssigneeID    *uuid.UUID `json:"assigneeId"`
	AssigneeName  *string    `json:"assigneeName"`
	Pool          string     `json:"pool,omitempty"`
	RoutingNote   *string    `json:"routingNote"`
	OpenedAt      *time.Time `json:"openedAt"` // the inbox's waiting time starts here (D9)
	DecidedBy     *uuid.UUID `json:"decidedBy"`
	DecidedByName *string    `json:"decidedByName"`
	DecidedAt     *time.Time `json:"decidedAt"`
	Reason        *string    `json:"reason"`
	Version       int        `json:"version"`
}

// Item is one requested level with its steps.
type Item struct {
	ID            uuid.UUID    `json:"id"`
	RequestID     uuid.UUID    `json:"requestId"`
	Beneficiary   Person       `json:"beneficiary"`
	SubmittedBy   Person       `json:"submittedBy"`
	ToolID        uuid.UUID    `json:"toolId"`
	Tool          string       `json:"tool"`
	AccessLevelID uuid.UUID    `json:"accessLevelId"`
	Level         string       `json:"level"`
	Justification string       `json:"justification"`
	Duration      Duration     `json:"duration"`
	Status        string       `json:"status"`
	Version       int          `json:"version"`
	CreatedAt     time.Time    `json:"createdAt"`
	ClosedAt      *time.Time   `json:"closedAt"`
	ClosedReason  *string      `json:"closedReason"`
	Steps         []Step       `json:"steps"`
	Fulfillment   *Fulfillment `json:"fulfillment"` // once approved; approval is not access

	// Account: the tool-specific account the access is for (A1 note 2026-11-01); nil when
	// the tool uses the employee's email.
	Account *access.ExternalAccount `json:"account"`
}

// Fulfillment is the grant of an approved item, with its display status (Fulfillment methods).
type Fulfillment struct {
	GrantID      uuid.UUID `json:"grantId"`
	Status       string    `json:"status"`
	Evidence     *string   `json:"evidence"`
	ClosedReason *string   `json:"closedReason"` // e.g. "access level retired"
}

// Request is one submission.
type Request struct {
	ID           uuid.UUID `json:"id"`
	SubmissionID uuid.UUID `json:"submissionId"`
	Kind         string    `json:"kind"`
	Beneficiary  Person    `json:"beneficiary"`
	SubmittedBy  Person    `json:"submittedBy"`
	CreatedAt    time.Time `json:"createdAt"`
	Items        []Item    `json:"items"`
}

// querier is a pool or a transaction.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

func loadSteps(ctx context.Context, q querier, itemIDs []uuid.UUID) (map[uuid.UUID][]Step, error) {
	rows, err := q.Query(ctx, `
		SELECT st.item_id, st.id, st.position, st.type, st.status, st.assignee_id, a.full_name, coalesce(st.pool, ''),
		       st.routing_note, st.opened_at, st.decided_by, d.full_name, st.decided_at, st.reason, st.version
		FROM approval_steps st LEFT JOIN employees a ON a.id = st.assignee_id LEFT JOIN employees d ON d.id = st.decided_by
		WHERE st.item_id = ANY($1) ORDER BY st.item_id, st.position`, itemIDs)
	if err != nil {
		return nil, err
	}
	out := map[uuid.UUID][]Step{}
	var itemID uuid.UUID
	var s Step
	_, err = pgx.ForEachRow(rows, []any{&itemID, &s.ID, &s.Position, &s.Type, &s.Status, &s.AssigneeID, &s.AssigneeName, &s.Pool,
		&s.RoutingNote, &s.OpenedAt, &s.DecidedBy, &s.DecidedByName, &s.DecidedAt, &s.Reason, &s.Version}, func() error {
		out[itemID] = append(out[itemID], s)
		return nil
	})
	return out, err
}

// loadItems returns the items matching where (over i, r) with their steps, newest request first.
func loadItems(ctx context.Context, q querier, where string, args ...any) ([]Item, error) {
	rows, err := q.Query(ctx, `
		SELECT i.id, i.request_id, b.id, b.full_name, s.id, s.full_name, t.id, t.name, l.id, l.name, i.justification,
		       i.duration_type, to_char(i.expires_on, 'YYYY-MM-DD'), i.status, i.version, i.created_at, i.closed_at,
		       i.closed_reason, g.id, g.method, g.status, g.activation_evidence, g.closed_reason, o.ticket_ref IS NOT NULL,
		       i.account, i.account_ref
		FROM request_items i JOIN requests r ON r.id = i.request_id
		LEFT JOIN grants g ON g.request_item_id = i.id OR (r.kind = 'EXTENSION' AND i.status = 'APPROVED' AND g.id = i.grant_id)
		LEFT JOIN operations o ON o.grant_id = g.id AND o.type = 'GRANT'
		JOIN employees b ON b.id = r.beneficiary_id JOIN employees s ON s.id = r.submitted_by
		JOIN tools t ON t.id = i.tool_id JOIN access_levels l ON l.id = i.access_level_id
		WHERE `+where+` ORDER BY r.id DESC, i.id`, args...)
	if err != nil {
		return nil, err
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Item, error) {
		var it Item
		var grant *uuid.UUID
		var method, status, evidence, reason *string
		var ticket bool
		var account, ref *string
		err := row.Scan(&it.ID, &it.RequestID, &it.Beneficiary.ID, &it.Beneficiary.FullName, &it.SubmittedBy.ID, &it.SubmittedBy.FullName,
			&it.ToolID, &it.Tool, &it.AccessLevelID, &it.Level, &it.Justification, &it.Duration.Type, &it.Duration.ExpiresOn,
			&it.Status, &it.Version, &it.CreatedAt, &it.ClosedAt, &it.ClosedReason, &grant, &method, &status, &evidence, &reason, &ticket, &account, &ref)
		if account != nil && ref != nil {
			it.Account = &access.ExternalAccount{Name: *account, Ref: *ref}
		}
		if grant != nil {
			it.Fulfillment = &Fulfillment{GrantID: *grant, Status: access.DisplayStatus(*method, *status, ticket), Evidence: evidence, ClosedReason: reason}
		}
		return it, err
	})
	if err != nil || len(items) == 0 {
		return items, err
	}
	ids := make([]uuid.UUID, len(items))
	for i, it := range items {
		ids[i] = it.ID
	}
	steps, err := loadSteps(ctx, q, ids)
	for i := range items {
		items[i].Steps = append([]Step{}, steps[items[i].ID]...) // [] for a direct grant, which has no steps
	}
	return items, err
}

func loadItem(ctx context.Context, q querier, id uuid.UUID) (Item, error) {
	items, err := loadItems(ctx, q, "i.id = $1", id)
	if err != nil {
		return Item{}, err
	}
	if len(items) == 0 {
		return Item{}, platform.ErrNotFound
	}
	return items[0], nil
}

// loadRequests returns up to 100 requests matching where (over r), newest first.
func loadRequests(ctx context.Context, q querier, where string, args ...any) ([]Request, error) {
	rows, err := q.Query(ctx, `
		SELECT r.id, r.submission_id, r.kind, b.id, b.full_name, s.id, s.full_name, r.created_at
		FROM requests r JOIN employees b ON b.id = r.beneficiary_id JOIN employees s ON s.id = r.submitted_by
		WHERE `+where+` ORDER BY r.id DESC LIMIT 100`, args...)
	if err != nil {
		return nil, err
	}
	reqs, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Request, error) {
		var r Request
		err := row.Scan(&r.ID, &r.SubmissionID, &r.Kind, &r.Beneficiary.ID, &r.Beneficiary.FullName, &r.SubmittedBy.ID,
			&r.SubmittedBy.FullName, &r.CreatedAt)
		r.Items = []Item{}
		return r, err
	})
	if err != nil || len(reqs) == 0 {
		return reqs, err
	}
	ids := make([]uuid.UUID, len(reqs))
	byID := map[uuid.UUID]*Request{}
	for i := range reqs {
		ids[i] = reqs[i].ID
		byID[reqs[i].ID] = &reqs[i]
	}
	items, err := loadItems(ctx, q, "i.request_id = ANY($1)", ids)
	for _, it := range items {
		byID[it.RequestID].Items = append(byID[it.RequestID].Items, it)
	}
	return reqs, err
}

// List returns the requests the actor is the beneficiary or the submitter of.
func (s *Service) List(ctx context.Context, actor auth.Principal) ([]Request, error) {
	return loadRequests(ctx, s.pool, "r.beneficiary_id = $1 OR r.submitted_by = $1", actor.EmployeeID)
}

// Get returns one of the actor's requests; anyone else's is not found (no existence leak).
func (s *Service) Get(ctx context.Context, actor auth.Principal, id uuid.UUID) (Request, error) {
	reqs, err := loadRequests(ctx, s.pool, "r.id = $1 AND (r.beneficiary_id = $2 OR r.submitted_by = $2)", id, actor.EmployeeID)
	if err != nil {
		return Request{}, err
	}
	if len(reqs) == 0 {
		return Request{}, platform.ErrNotFound
	}
	return reqs[0], nil
}

// ItemInput is one requested level.
type ItemInput struct {
	AccessLevelID uuid.UUID
	Justification string
	Duration      Duration
	Account       string // the tool-specific account, when the tool has an account kind
}

// Submission is a NEW_ACCESS request; a nil BeneficiaryID means the submitter.
type Submission struct {
	SubmissionID  uuid.UUID
	BeneficiaryID *uuid.UUID
	Items         []ItemInput
}

// itemError puts err on items[i]'s field.
func itemError(i int, err error) error {
	var e *platform.Error
	if errors.As(err, &e) {
		moved := *e
		moved.Field = fmt.Sprintf("items[%d].%s", i, e.Field)
		return &moved
	}
	return err
}

// checkText trims a required free-text value (justification, reason).
func checkText(s, field, code string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", platform.ValidationError(field, code)
	}
	return s, platform.CheckText(s, field, maxTextLength)
}

// existing returns the request of kind for submissionID, if any. Only its submitter gets
// it back: a guessed or leaked ID never reveals someone else's request.
func existing(ctx context.Context, q querier, actor auth.Principal, submissionID uuid.UUID, kind string) (*Request, error) {
	reqs, err := loadRequests(ctx, q, "r.submission_id = $1", submissionID)
	if err != nil || len(reqs) == 0 {
		return nil, err
	}
	if reqs[0].SubmittedBy.ID != actor.EmployeeID || reqs[0].Kind != kind {
		return nil, errSubmissionReused
	}
	return &reqs[0], nil
}

func isUnique(err error, constraint string) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505" && pg.ConstraintName == constraint
}

// checkOnBehalf lets an employee request for themselves, for anyone below them in the
// management chain, or, as HR or a Super Admin, for anyone (D3, 2026-10-30).
func checkOnBehalf(ctx context.Context, tx pgx.Tx, actor auth.Principal, beneficiary uuid.UUID) error {
	if beneficiary == actor.EmployeeID || actor.HasRole("HR_MANAGER") || actor.HasRole("SUPER_ADMIN") {
		return nil
	}
	var manages bool
	if err := tx.QueryRow(ctx, `WITH RECURSIVE chain AS (
			SELECT manager_id FROM employees WHERE id = $1
			UNION SELECT e.manager_id FROM employees e JOIN chain c ON e.id = c.manager_id)
		SELECT EXISTS (SELECT 1 FROM chain WHERE manager_id = $2)`, beneficiary, actor.EmployeeID).Scan(&manages); err != nil {
		return err
	}
	if !manages {
		return errBeneficiaryNotAllowed
	}
	return nil
}

// Submit creates a NEW_ACCESS request: every item valid, or nothing is created. The
// bool is false when the submission ID was already used by this submitter (A9).
func (s *Service) Submit(ctx context.Context, actor auth.Principal, in Submission) (Request, bool, error) {
	if len(in.Items) == 0 {
		return Request{}, false, platform.ValidationError("items", "items_required")
	}
	if len(in.Items) > maxItems {
		return Request{}, false, platform.ValidationError("items", "too_many_items")
	}
	justifications := make([]string, len(in.Items))
	for i, it := range in.Items {
		j, err := checkText(it.Justification, "justification", "justification_required")
		if err != nil {
			return Request{}, false, itemError(i, err)
		}
		justifications[i] = j
	}
	// A retry returns the stored request before any GitHub lookup (A9); the check in the
	// transaction below still settles concurrent first submissions.
	if prev, err := existing(ctx, s.pool, actor, in.SubmissionID, "NEW_ACCESS"); err != nil || prev != nil {
		if prev != nil {
			return *prev, false, nil
		}
		return Request{}, false, err
	}
	accounts := make([]*access.ExternalAccount, len(in.Items))
	for i, it := range in.Items {
		if i > 0 && slices.ContainsFunc(in.Items[:i], func(o ItemInput) bool { return o.AccessLevelID == it.AccessLevelID }) {
			continue // refused as duplicate_tool below; GitHub is asked once per level
		}
		acc, err := s.resolveAccount(ctx, it.AccessLevelID, it.Account)
		if err != nil {
			return Request{}, false, itemError(i, err)
		}
		accounts[i] = acc
	}
	beneficiary := actor.EmployeeID
	if in.BeneficiaryID != nil {
		beneficiary = *in.BeneficiaryID
	}

	var req Request
	created := false
	err := platform.InTx(ctx, s.pool, func(tx pgx.Tx, now time.Time) error {
		created = false // fn may run again (platform.InTx)
		// A9: one submission at a time per beneficiary. Lock first, then read (M3).
		// shortcut: this lock is taken before the tool's FOR SHARE, the reverse of catalog
		// UpdateTool (tool, then the new owner); making someone a tool's owner while they submit
		// for it can deadlock; PostgreSQL aborts one and platform.InTx retries it.
		tag, err := tx.Exec(ctx, "SELECT 1 FROM employees WHERE id = $1 FOR UPDATE", beneficiary)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return platform.ValidationError("beneficiaryId", "beneficiary_not_found")
		}
		if prev, err := existing(ctx, tx, actor, in.SubmissionID, "NEW_ACCESS"); err != nil || prev != nil {
			if prev != nil {
				req = *prev
			}
			return err
		}
		if err := checkOnBehalf(ctx, tx, actor, beneficiary); err != nil {
			return err
		}
		var status string
		var managerID *uuid.UUID
		if err := tx.QueryRow(ctx, "SELECT status, manager_id FROM employees WHERE id = $1", beneficiary).Scan(&status, &managerID); err != nil {
			return err
		}
		if status != "ACTIVE" {
			return platform.ValidationError("beneficiaryId", "beneficiary_not_active")
		}

		var all []newItem
		seen := map[uuid.UUID]bool{}
		for i, it := range in.Items {
			c, err := checkLevel(ctx, tx, beneficiary, it.AccessLevelID, nil)
			if err != nil {
				return itemError(i, err)
			}
			if seen[c.toolID] {
				return itemError(i, platform.ValidationError("accessLevelId", "duplicate_tool"))
			}
			seen[c.toolID] = true
			expires, err := ParseDuration(it.Duration, now, s.loc)
			if err != nil {
				return itemError(i, err)
			}
			it.Justification = justifications[i]
			all = append(all, newItem{c, it, expires, nil, accounts[i]})
		}
		if req, err = s.create(ctx, tx, now, actor, "NEW_ACCESS", in.SubmissionID, beneficiary, managerID, all); err != nil {
			return err
		}
		created = true
		return queueEmail(ctx, tx, req)
	})
	if isUnique(err, "requests_submission_id_key") {
		return s.repeated(ctx, actor, in.SubmissionID, "NEW_ACCESS")
	}
	return req, created, err
}

// newItem is a checked item, ready to create.
type newItem struct {
	levelCheck
	ItemInput
	expires *time.Time
	grantID *uuid.UUID // the grant an EXTENSION or LEVEL_CHANGE acts on
	account *access.ExternalAccount
}

// create writes a request of kind with its items and their steps, records REQUEST_SUBMITTED
// and opens each item's first step, in the caller's transaction under the beneficiary's lock.
func (s *Service) create(ctx context.Context, tx pgx.Tx, now time.Time, actor auth.Principal, kind string,
	submissionID, beneficiary uuid.UUID, managerID *uuid.UUID, all []newItem) (Request, error) {
	var requestID uuid.UUID
	if err := tx.QueryRow(ctx, `INSERT INTO requests (submission_id, kind, beneficiary_id, submitted_by, created_at)
			VALUES ($1, $2, $3, $4, $5) RETURNING id`, submissionID, kind, beneficiary, actor.EmployeeID, now).Scan(&requestID); err != nil {
		return Request{}, err
	}
	// D3: the beneficiary's current direct manager's submission satisfies the Manager step,
	// unless the submitter sets reporting lines (HR Manager, Super Admin) and so could have made
	// themself the manager (R-3, D3 note 2026-10-31): their Manager step goes to someone else.
	bySubmission := managerID != nil && *managerID == actor.EmployeeID && !actor.HasRole("HR_MANAGER") && !actor.HasRole("SUPER_ADMIN")
	var details []map[string]any
	var itemIDs []uuid.UUID
	for _, c := range all {
		var itemID uuid.UUID
		account, ref := accountColumns(c.account)
		if err := tx.QueryRow(ctx, `INSERT INTO request_items (request_id, beneficiary_id, tool_id, access_level_id, justification,
				duration_type, expires_on, grant_id, account, account_ref, created_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11) RETURNING id`,
			requestID, beneficiary, c.toolID, c.AccessLevelID, c.Justification, c.Duration.Type, c.expires, c.grantID, account, ref, now).Scan(&itemID); err != nil {
			return Request{}, err
		}
		for pos, typ := range c.steps {
			var err error
			if pos == 0 && bySubmission {
				_, err = tx.Exec(ctx, `INSERT INTO approval_steps (item_id, position, type, status, decided_by, decided_at)
						VALUES ($1, 1, 'MANAGER', 'APPROVED_BY_SUBMISSION', $2, $3)`, itemID, actor.EmployeeID, now)
			} else {
				_, err = tx.Exec(ctx, "INSERT INTO approval_steps (item_id, position, type) VALUES ($1, $2, $3)", itemID, pos+1, typ)
			}
			if err != nil {
				return Request{}, err
			}
		}
		itemIDs = append(itemIDs, itemID)
		d := accountDetails(map[string]any{"itemId": itemID, "tool": c.tool, "level": c.level, "duration": c.Duration.Type, "expiresOn": c.expires}, c.account)
		if c.grantID != nil {
			d["grantId"] = *c.grantID
		}
		details = append(details, d)
	}
	if err := audit.Record(ctx, tx, audit.Event{ActorID: &actor.EmployeeID, Action: "REQUEST_SUBMITTED", Entity: "request",
		EntityID: &requestID, SubjectID: &beneficiary, Details: map[string]any{"submissionId": submissionID, "kind": kind,
			"approvedBySubmission": bySubmission, "items": details}}); err != nil {
		return Request{}, err
	}
	for _, id := range itemIDs { // after REQUEST_SUBMITTED, so the trail reads in order
		if err := advance(ctx, tx, now, s.loc, actor.EmployeeID, id); err != nil {
			return Request{}, err
		}
	}
	reqs, err := loadRequests(ctx, tx, "r.id = $1", requestID)
	if err != nil {
		return Request{}, err
	}
	return reqs[0], nil
}

// repeated answers a create whose submission ID was committed concurrently by another
// transaction (A9), for another beneficiary whose row we did not lock: the original for
// its submitter, otherwise submission_id_reused.
func (s *Service) repeated(ctx context.Context, actor auth.Principal, submissionID uuid.UUID, kind string) (Request, bool, error) {
	prev, err := existing(ctx, s.pool, actor, submissionID, kind)
	if err != nil {
		return Request{}, false, err
	}
	if prev == nil {
		return Request{}, false, errSubmissionReused
	}
	return *prev, false, nil
}

type levelCheck struct {
	toolID      uuid.UUID
	tool, level string
	steps       []string
}

// checkLevel: the level and its tool are ACTIVE, the beneficiary has no open item for the
// tool (2026-10-28: one open item per person and tool) and holds no open grant for it but
// the one being changed, if any (D4). The tool row lock holds off a concurrent retirement;
// the level is read after it (M3: lock first, then read).
func checkLevel(ctx context.Context, tx pgx.Tx, beneficiary, levelID uuid.UUID, changing *uuid.UUID) (levelCheck, error) {
	var c levelCheck
	err := tx.QueryRow(ctx, "SELECT tool_id FROM access_levels WHERE id = $1", levelID).Scan(&c.toolID)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, platform.ValidationError("accessLevelId", "level_not_found")
	}
	if err != nil {
		return c, err
	}
	var toolStatus, levelStatus string
	if err := tx.QueryRow(ctx, "SELECT name, status FROM tools WHERE id = $1 FOR SHARE", c.toolID).Scan(&c.tool, &toolStatus); err != nil {
		return c, err
	}
	// One statement, after the beneficiary's lock: an approval turns the open item into an
	// open grant in one transaction, so this sees one or the other (D4, A9).
	var owner, infosec, open, held bool
	if err := tx.QueryRow(ctx, `SELECT name, status, requires_tool_owner, requires_infosec,
		EXISTS (SELECT 1 FROM request_items WHERE beneficiary_id = $2 AND tool_id = $3 AND status = 'PENDING_APPROVAL'),
		EXISTS (SELECT 1 FROM grants WHERE employee_id = $2 AND tool_id = $3 AND status NOT IN ('REVOKED', 'CANCELLED')
		        AND id IS DISTINCT FROM $4)
		FROM access_levels WHERE id = $1`, levelID, beneficiary, c.toolID, changing).Scan(&c.level, &levelStatus, &owner, &infosec, &open, &held); err != nil {
		return c, err
	}
	if toolStatus != "ACTIVE" || levelStatus != "ACTIVE" {
		return c, platform.ValidationError("accessLevelId", "level_not_requestable")
	}
	if open {
		return c, &platform.Error{Status: http.StatusConflict, Code: "open_request_exists", Field: "accessLevelId"}
	}
	if held {
		return c, &platform.Error{Status: http.StatusConflict, Code: "access_exists", Field: "accessLevelId"}
	}
	c.steps = catalog.Steps(owner, infosec)
	return c, nil
}

// queueEmail writes the one Mock Email Outbox message of a NEW_ACCESS submission whose
// Manager step went to a person (O2); never for an EXTENSION or LEVEL_CHANGE. Nothing when
// the manager submitted (D3) or the step went to the InfoSec pool. All items route the
// Manager step alike: same people, same exclusions, in one transaction.
func queueEmail(ctx context.Context, tx pgx.Tx, req Request) error {
	var recipient *uuid.UUID
	var tools []string
	for _, it := range req.Items {
		if s := it.Steps[0]; s.Type == "MANAGER" && s.Status == "PENDING" && s.AssigneeID != nil {
			recipient = s.AssigneeID
			tools = append(tools, it.Tool+" ("+it.Level+")")
		}
	}
	if recipient == nil {
		return nil
	}
	_, err := tx.Exec(ctx, "INSERT INTO email_outbox (recipient_id, request_id, subject, body) VALUES ($1, $2, $3, $4)",
		recipient, req.ID, "Erişim talebi: "+req.Beneficiary.FullName,
		req.Beneficiary.FullName+" şu araçlar için erişim talep ediyor: "+strings.Join(tools, ", ")+
			". Onaylamak veya reddetmek için AccessFlow'u açın; e-posta üzerinden onay verilemez.")
	return err
}

// Email is one Mock Email Outbox message: an access request (O2), an activation link (M18) or
// a role-acceptance link (D2 succession), whose link is never listed; it is revealed
// separately while linkAvailable.
type Email struct {
	ID            uuid.UUID  `json:"id"`
	Kind          string     `json:"kind"`
	Recipient     Person     `json:"recipient"`
	RequestID     *uuid.UUID `json:"requestId"`
	Subject       string     `json:"subject"`
	Body          string     `json:"body"`
	CreatedAt     time.Time  `json:"createdAt"`
	LinkAvailable bool       `json:"linkAvailable"`
}

// Outbox lists the newest 100 simulated emails. It is a Super Admin screen (O2 note 2026-10-31):
// a Super Admin sees the access-request and activation emails and their own role-acceptance
// emails; anyone else sees only their own pending role-acceptance email (D2 succession) and is
// refused without one. An Auditor is always refused (O7).
func (s *Service) Outbox(ctx context.Context, actor auth.Principal) ([]Email, error) {
	if actor.HasRole("AUDITOR") {
		return nil, platform.ErrForbidden
	}
	superAdmin := actor.HasRole("SUPER_ADMIN")
	rows, err := s.pool.Query(ctx, `SELECT o.id, o.kind, e.id, e.full_name, o.request_id, o.subject, o.body, o.created_at,
		       o.sealed_token IS NOT NULL AND e.status = 'ACTIVE' AND CASE o.kind
		         WHEN 'ACTIVATION' THEN t.used_at IS NULL AND t.revoked_at IS NULL AND t.expires_at > now()
		         WHEN 'ROLE_ACCEPTANCE' THEN n.status = 'PENDING' AND n.expires_at > now() END
		FROM email_outbox o JOIN employees e ON e.id = o.recipient_id LEFT JOIN activation_tokens t ON t.id = o.activation_id
		LEFT JOIN super_admin_nominations n ON n.id = o.nomination_id
		WHERE (o.kind IN ('ACCESS_REQUEST', 'ACTIVATION') AND $1)
		   OR (o.kind = 'ROLE_ACCEPTANCE' AND o.recipient_id = $2 AND ($1 OR (n.status = 'PENDING' AND n.expires_at > now())))
		ORDER BY o.id DESC LIMIT 100`, superAdmin, actor.EmployeeID)
	if err != nil {
		return nil, err
	}
	emails, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Email, error) {
		var m Email
		var available *bool
		err := row.Scan(&m.ID, &m.Kind, &m.Recipient.ID, &m.Recipient.FullName, &m.RequestID, &m.Subject, &m.Body, &m.CreatedAt, &available)
		m.LinkAvailable = available != nil && *available
		return m, err
	})
	if err == nil && !superAdmin && len(emails) == 0 {
		return nil, platform.ErrForbidden
	}
	return emails, err
}
