package reviews

import (
	"context"
	"errors"
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
	"github.com/esraahaytaa-eng/access-flow/internal/notify"
	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

var (
	errCampaignActive  = platform.NewError(http.StatusConflict, "campaign_active")
	errItemClosed      = platform.NewError(http.StatusConflict, "review_item_closed")
	errTerminated      = platform.NewError(http.StatusConflict, "terminated_access_not_kept")
	errVersionConflict = platform.NewError(http.StatusConflict, "version_conflict")
)

type Service struct {
	pool *pgxpool.Pool
}

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

// Campaign is a review campaign as the review page shows it.
type Campaign struct {
	ID          uuid.UUID      `json:"id"`
	Kind        string         `json:"kind"`
	Quarter     *string        `json:"quarter"`
	StartedAt   time.Time      `json:"startedAt"`
	StartedBy   *access.Person `json:"startedBy"`
	RemindedAt  *time.Time     `json:"remindedAt"`
	EscalatedAt *time.Time     `json:"escalatedAt"`
	ClosedAt    *time.Time     `json:"closedAt"`
}

// Employee is the person whose access is reviewed.
type Employee struct {
	ID       uuid.UUID `json:"id"`
	FullName string    `json:"fullName"`
	JobTitle *string   `json:"jobTitle"`
	Status   string    `json:"status"`
}

// Item is one grant to decide in a campaign, or an open finding left by one (D8).
type Item struct {
	ID              uuid.UUID      `json:"id"`
	CampaignID      uuid.UUID      `json:"campaignId"`
	CampaignStarted time.Time      `json:"campaignStartedAt"`
	GrantID         uuid.UUID      `json:"grantId"`
	Employee        Employee       `json:"employee"`
	ToolID          uuid.UUID      `json:"toolId"`
	Tool            string         `json:"tool"`
	AccessLevelID   uuid.UUID      `json:"accessLevelId"`
	Level           string         `json:"level"`
	GrantStatus     string         `json:"grantStatus"`
	ExpiresOn       *string        `json:"expiresOn"`
	EffectiveFrom   *time.Time     `json:"effectiveFrom"`
	ReviewerID      *uuid.UUID     `json:"reviewerId"`
	ReviewerName    *string        `json:"reviewerName"`
	RoutingNote     *string        `json:"routingNote"`
	RequiresInfosec bool           `json:"requiresInfosec"`
	Flags           []string       `json:"flags"`
	Decision        *string        `json:"decision"`
	DecidedBy       *access.Person `json:"decidedBy"`
	DecidedAt       *time.Time     `json:"decidedAt"`
	DecisionReason  *string        `json:"decisionReason"`
	Late            bool           `json:"late"`
	FindingAt       *time.Time     `json:"findingAt"`
	ClosedReason    *string        `json:"closedReason"`
	ClosedAt        *time.Time     `json:"closedAt"`
	Version         int            `json:"version"`
}

// open is a review item still waiting for a decision.
const open = "i.decision IS NULL AND i.closed_reason IS NULL"

// StartCampaign starts a campaign now, by a Super Admin (D8 [DEFAULT], D10 certification).
func (s *Service) StartCampaign(ctx context.Context, actor auth.Principal) (Campaign, error) {
	if !actor.HasRole("SUPER_ADMIN") {
		return Campaign{}, platform.ErrForbidden
	}
	var id uuid.UUID
	err := platform.InTx(ctx, s.pool, func(tx pgx.Tx, now time.Time) error {
		var err error
		id, err = start(ctx, tx, now, "MANUAL", nil, &actor.EmployeeID)
		return err
	})
	if err != nil {
		return Campaign{}, err
	}
	return s.campaign(ctx, "c.id = $1", id)
}

// start creates a campaign and snapshots every ACTIVE or SUSPENDED grant of an employee,
// except those whose open finding is still the work item (D8). Reviewers are notified once.
func start(ctx context.Context, tx pgx.Tx, now time.Time, kind string, quarter *string, actor *uuid.UUID) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `INSERT INTO review_campaigns (kind, quarter, started_at, started_by) VALUES ($1, $2, $3, $4) RETURNING id`,
		kind, quarter, now, actor).Scan(&id)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return uuid.Nil, errCampaignActive
	}
	if err != nil {
		return uuid.Nil, err
	}
	rows, err := tx.Query(ctx, `
		SELECT g.id, g.employee_id, t.owner_id, o.status = 'ACTIVE',
		       EXISTS (SELECT 1 FROM role_assignments r WHERE r.employee_id = t.owner_id AND r.role = 'SUPER_ADMIN' AND r.valid_to IS NULL),
		       l.requires_infosec,
		       array_remove(ARRAY[
		         CASE WHEN g.retained_at IS NOT NULL THEN 'RETAINED_ON_TRANSFER' END,
		         CASE WHEN g.earlier_policy_at IS NOT NULL THEN 'EARLIER_POLICY' END,
		         CASE WHEN rq.kind = 'DIRECT_GRANT' THEN 'DIRECT_GRANT' END,
		         CASE WHEN EXISTS (SELECT 1 FROM approval_steps st WHERE st.item_id = g.request_item_id
		                           AND st.status = 'SUPER_ADMIN_OVERRIDE') THEN 'OVERRIDE' END,
		         CASE WHEN g.status = 'SUSPENDED' THEN 'SUSPENDED' END,
		         CASE WHEN g.import_id IS NOT NULL THEN 'IMPORTED_BASELINE' END], NULL)
		FROM grants g
		JOIN tools t ON t.id = g.tool_id JOIN employees o ON o.id = t.owner_id
		JOIN access_levels l ON l.id = g.access_level_id
		LEFT JOIN request_items ri ON ri.id = g.request_item_id LEFT JOIN requests rq ON rq.id = ri.request_id
		WHERE g.status IN ('ACTIVE', 'SUSPENDED') AND g.employee_id IS NOT NULL
		  AND NOT EXISTS (SELECT 1 FROM review_items i WHERE i.grant_id = g.id AND i.finding_at IS NOT NULL AND `+open+`)
		ORDER BY g.id`) // no lock: an item whose grant ends meanwhile closes as already removed
	if err != nil {
		return uuid.Nil, err
	}
	type snap struct {
		grant, employee uuid.UUID
		owner           Owner
		infosec         bool
		flags           []string
	}
	snaps, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (snap, error) {
		var x snap
		err := row.Scan(&x.grant, &x.employee, &x.owner.ID, &x.owner.Active, &x.owner.SuperAdmin, &x.infosec, &x.flags)
		return x, err
	})
	if err != nil {
		return uuid.Nil, err
	}
	reviewers := map[uuid.UUID]bool{}
	pool := false
	for _, x := range snaps {
		reviewer, note := Reviewer(x.owner, x.employee)
		if _, err := tx.Exec(ctx, `INSERT INTO review_items (campaign_id, grant_id, reviewer_id, routing_note, requires_infosec, flags)
			VALUES ($1, $2, $3, nullif($4, ''), $5, $6)`, id, x.grant, reviewer, note, x.infosec, x.flags); err != nil {
			return uuid.Nil, err
		}
		if reviewer == nil {
			pool = true
		} else {
			reviewers[*reviewer] = true
		}
	}
	details := map[string]any{"items": len(snaps)}
	for r := range reviewers {
		if err := notify.ToEmployee(ctx, tx, r, "REVIEW_STARTED", "review_campaign", id, details); err != nil {
			return uuid.Nil, err
		}
	}
	if pool {
		if err := notify.ToRole(ctx, tx, "INFOSEC", "REVIEW_STARTED", "review_campaign", id, details); err != nil {
			return uuid.Nil, err
		}
	}
	details["kind"] = kind
	return id, audit.Record(ctx, tx, audit.Event{ActorID: actor, Action: "REVIEW_CAMPAIGN_STARTED", Entity: "review_campaign",
		EntityID: &id, Details: details})
}

// Decide records Keep or Revoke on an item (D8). Revoke starts the normal removal with the
// reason; Keep does not extend expiry. A decision on an open finding is late.
func (s *Service) Decide(ctx context.Context, actor auth.Principal, id uuid.UUID, decision, reason string, version int) (Item, error) {
	reason = strings.TrimSpace(reason)
	switch decision {
	case "KEEP":
	case "REVOKE":
		if reason == "" {
			return Item{}, platform.ValidationError("reason", "reason_required")
		}
		if err := platform.CheckText(reason, "reason", 1000); err != nil {
			return Item{}, err
		}
	default:
		return Item{}, platform.ValidationError("decision", "invalid_decision")
	}
	err := platform.InTx(ctx, s.pool, func(tx pgx.Tx, now time.Time) error {
		return decide(ctx, tx, now, actor, id, decision, reason, version, false)
	})
	if err != nil {
		return Item{}, err
	}
	return s.item(ctx, id)
}

// errSkip marks an item bulk Keep leaves for an individual decision.
var errSkip = errors.New("decided individually")

// decide records one decision under the item's lock. Bulk Keep skips (errSkip) what must
// be decided individually, cannot be decided by actor, or changed since it was listed.
func decide(ctx context.Context, tx pgx.Tx, now time.Time, actor auth.Principal, id uuid.UUID, decision, reason string, version int, bulk bool) error {
	var grant, employee uuid.UUID
	var reviewer *uuid.UUID
	var current int
	var isOpen, finding, escalated, infosec, terminated bool
	var flags []string
	err := tx.QueryRow(ctx, `SELECT i.grant_id, g.employee_id, i.reviewer_id, i.version, `+open+`, i.finding_at IS NOT NULL,
		       c.escalated_at IS NOT NULL, i.requires_infosec, i.flags, e.status = 'TERMINATED'
		FROM review_items i JOIN grants g ON g.id = i.grant_id JOIN review_campaigns c ON c.id = i.campaign_id
		JOIN employees e ON e.id = g.employee_id
		WHERE i.id = $1 FOR UPDATE OF i`, id).Scan(&grant, &employee, &reviewer, &current, &isOpen, &finding, &escalated, &infosec, &flags, &terminated)
	if err == nil {
		err = access.StillActive(ctx, tx, actor.EmployeeID)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return platform.ErrNotFound
	}
	if err != nil {
		return err
	}
	code := MayDecide(actor, decision, reviewer, employee, escalated || finding)
	switch {
	case bulk && (code != "" || terminated || !isOpen || version != current || !BulkEligible(infosec, flags)):
		return errSkip
	case code == "not_reviewer" && !actor.HasRole("INFOSEC") && !actor.HasRole("SUPER_ADMIN") && !actor.HasRole("AUDITOR"):
		return platform.ErrNotFound // not theirs to see
	case code != "":
		return platform.NewError(http.StatusForbidden, code)
	case terminated && decision == "KEEP":
		return errTerminated // a leaver's access is removed by termination (D7), never kept
	case version != current:
		return errVersionConflict
	case !isOpen:
		return errItemClosed
	}
	// Lock order as every removal: pending change item, employee, operations, then the grant.
	if err := access.LockForRemoval(ctx, tx, grant); err != nil {
		return err
	}
	if held, err := reviewable(ctx, tx, grant); err != nil || !held {
		if err == nil && bulk {
			err = errSkip
		} else if err == nil {
			err = errItemClosed // already being removed: nothing left to decide
		}
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE review_items SET decision = $2, decided_by = $3, decided_at = $4, decision_reason = nullif($5, ''),
		late = finding_at IS NOT NULL, version = version + 1 WHERE id = $1`, id, decision, actor.EmployeeID, now, reason); err != nil {
		return err
	}
	// F2: the flag asked for this individual decision; it is now made. A step added after the
	// snapshot keeps the flag for the next campaign.
	if slices.Contains(flags, "EARLIER_POLICY") {
		if _, err := tx.Exec(ctx, "UPDATE grants SET earlier_policy_at = NULL WHERE id = $1", grant); err != nil {
			return err
		}
	}
	action := "REVIEW_KEPT"
	if decision == "REVOKE" {
		action = "REVIEW_REVOKED"
		if err := access.Remove(ctx, tx, now, &actor.EmployeeID, grant, "REVIEW", reason); err != nil {
			return err
		}
	}
	return audit.Record(ctx, tx, audit.Event{ActorID: &actor.EmployeeID, Action: action, Entity: "review_item", EntityID: &id,
		SubjectID: &employee, Reason: reason, Details: map[string]any{"grantId": grant, "late": finding, "bulk": bulk}})
}

// BulkKeep keeps every listed item actor may keep in bulk and skips the rest (D8: items
// requiring InfoSec approval and flagged items are decided individually).
// Versioned names an item at the version the reviewer saw.
type Versioned struct {
	ID      uuid.UUID
	Version int
}

func (s *Service) BulkKeep(ctx context.Context, actor auth.Principal, items []Versioned) (kept, skipped int, err error) {
	err = platform.InTx(ctx, s.pool, func(tx pgx.Tx, now time.Time) error {
		kept, skipped = 0, 0
		// One lock order for overlapping bulk Keeps.
		slices.SortFunc(items, func(a, b Versioned) int { return strings.Compare(a.ID.String(), b.ID.String()) })
		for _, it := range items {
			switch err := decide(ctx, tx, now, actor, it.ID, "KEEP", "", it.Version, true); {
			case errors.Is(err, errSkip) || errors.Is(err, platform.ErrNotFound):
				skipped++
			case err != nil:
				return err
			default:
				kept++
			}
		}
		return nil
	})
	return kept, skipped, err
}

// Page is the review page: the active campaign and the items actor may see in it, plus the
// open findings of earlier campaigns (D8).
type Page struct {
	Campaign *Campaign `json:"campaign"`
	Items    []Item    `json:"items"`
}

// sees is who sees all items: the review roles; everyone else sees their own.
func sees(actor auth.Principal) bool {
	return actor.HasRole("SUPER_ADMIN") || actor.HasRole("INFOSEC") || actor.HasRole("AUDITOR")
}

func (s *Service) Page(ctx context.Context, actor auth.Principal) (Page, error) {
	var page Page
	c, err := s.campaign(ctx, "c.closed_at IS NULL")
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return page, err
	default:
		page.Campaign = &c
	}
	scope := "($1::uuid IS NOT NULL)" // keeps the argument count the same for both scopes
	if !sees(actor) {
		scope = "i.reviewer_id = $1"
	}
	page.Items, err = s.items(ctx, scope+" AND (c.closed_at IS NULL OR (i.finding_at IS NOT NULL AND "+open+"))", actor.EmployeeID)
	return page, err
}

// OpenFindings are the items day 21 left unanswered and nobody has decided yet (Exceptions).
func OpenFindings(ctx context.Context, pool *pgxpool.Pool) ([]Item, error) {
	return (&Service{pool: pool}).items(ctx, "i.finding_at IS NOT NULL AND "+open)
}

func (s *Service) campaign(ctx context.Context, where string, args ...any) (Campaign, error) {
	var c Campaign
	var byID *uuid.UUID
	var byName *string
	err := s.pool.QueryRow(ctx, `SELECT c.id, c.kind, c.quarter, c.started_at, c.started_by, e.full_name, c.reminded_at, c.escalated_at, c.closed_at
		FROM review_campaigns c LEFT JOIN employees e ON e.id = c.started_by WHERE `+where+` ORDER BY c.started_at DESC LIMIT 1`, args...).
		Scan(&c.ID, &c.Kind, &c.Quarter, &c.StartedAt, &byID, &byName, &c.RemindedAt, &c.EscalatedAt, &c.ClosedAt)
	if byID != nil {
		c.StartedBy = &access.Person{ID: *byID, FullName: *byName}
	}
	return c, err
}

func (s *Service) item(ctx context.Context, id uuid.UUID) (Item, error) {
	items, err := s.items(ctx, "i.id = $1", id)
	if err != nil || len(items) == 0 {
		return Item{}, err
	}
	return items[0], nil
}

func (s *Service) items(ctx context.Context, where string, args ...any) ([]Item, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT i.id, c.id, c.started_at, g.id, e.id, e.full_name, e.job_title, e.status, t.id, t.name, l.id, l.name, g.status,
		       to_char(g.expires_on, 'YYYY-MM-DD'), g.effective_from, i.reviewer_id, r.full_name, i.routing_note, i.requires_infosec,
		       i.flags, i.decision, i.decided_by, d.full_name, i.decided_at, i.decision_reason, i.late, i.finding_at,
		       i.closed_reason, i.closed_at, i.version
		FROM review_items i JOIN review_campaigns c ON c.id = i.campaign_id
		JOIN grants g ON g.id = i.grant_id JOIN employees e ON e.id = g.employee_id
		JOIN tools t ON t.id = g.tool_id JOIN access_levels l ON l.id = g.access_level_id
		LEFT JOIN employees r ON r.id = i.reviewer_id LEFT JOIN employees d ON d.id = i.decided_by
		WHERE `+where+` ORDER BY e.full_name, t.name, l.name, i.id LIMIT 2000`, args...) // shortcut: 2000 rows; page above that
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Item, error) {
		var it Item
		var decidedBy *uuid.UUID
		var decidedName *string
		err := row.Scan(&it.ID, &it.CampaignID, &it.CampaignStarted, &it.GrantID, &it.Employee.ID, &it.Employee.FullName,
			&it.Employee.JobTitle, &it.Employee.Status, &it.ToolID, &it.Tool, &it.AccessLevelID, &it.Level, &it.GrantStatus,
			&it.ExpiresOn, &it.EffectiveFrom, &it.ReviewerID, &it.ReviewerName, &it.RoutingNote, &it.RequiresInfosec,
			&it.Flags, &it.Decision, &decidedBy, &decidedName, &it.DecidedAt, &it.DecisionReason, &it.Late, &it.FindingAt,
			&it.ClosedReason, &it.ClosedAt, &it.Version)
		if decidedBy != nil {
			it.DecidedBy = &access.Person{ID: *decidedBy, FullName: *decidedName}
		}
		return it, err
	})
}

// reviewable reports whether the grant is still held as it was snapshotted: ACTIVE or
// SUSPENDED, not being removed. The caller holds the grant's removal locks.
func reviewable(ctx context.Context, tx pgx.Tx, grant uuid.UUID) (bool, error) {
	var ok bool
	err := tx.QueryRow(ctx, "SELECT status IN ('ACTIVE', 'SUSPENDED') FROM grants WHERE id = $1", grant).Scan(&ok)
	return ok, err
}
