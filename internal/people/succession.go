package people

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/esraahaytaa-eng/access-flow/internal/audit"
	"github.com/esraahaytaa-eng/access-flow/internal/auth"
	"github.com/esraahaytaa-eng/access-flow/internal/notify"
	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

// NominationLifetime: an open Super Admin nomination expires after 7 days (D2).
const NominationLifetime = 7 * 24 * time.Hour

var (
	errSeparation         = platform.NewError(http.StatusForbidden, "separation_of_duties")
	errSuperAdminRequired = platform.NewError(http.StatusForbidden, "super_admin_required")
	errAlreadySuperAdmin  = platform.NewError(http.StatusConflict, "already_super_admin")
	errNominationPending  = platform.NewError(http.StatusConflict, "nomination_pending")
	errNominationClosed   = platform.NewError(http.StatusConflict, "nomination_closed")
	errNominationExpired  = platform.NewError(http.StatusConflict, "nomination_expired")
	errRecoveryEnded      = platform.NewError(http.StatusConflict, "recovery_ended")
)

// checkNominator (D2): a Super Admin proposes someone else; while no Super Admin is active
// (recovery) an HR Manager proposes, themself allowed.
func checkNominator(actor auth.Principal, nominee uuid.UUID, recovery bool) error {
	switch {
	case recovery && actor.HasRole("HR_MANAGER"):
		return nil
	case recovery || !actor.HasRole("SUPER_ADMIN"):
		return platform.ErrForbidden
	case actor.EmployeeID == nominee:
		return errSelfChange
	}
	return nil
}

// checkAuthorizer (D2): two people, never the nominee. A recovery nomination is authorized
// by InfoSec; a promotion by a Super Admin when one other than the proposer and the
// nominee is active (otherSuperAdmin), else by InfoSec.
func checkAuthorizer(actor auth.Principal, nominee, proposer uuid.UUID, recovery, otherSuperAdmin bool) error {
	switch {
	case actor.EmployeeID == nominee || actor.EmployeeID == proposer:
		return errSeparation
	case !recovery && otherSuperAdmin && actor.HasRole("SUPER_ADMIN"):
		return nil
	case !recovery && otherSuperAdmin && actor.HasRole("INFOSEC"):
		return errSuperAdminRequired
	case !otherSuperAdmin && actor.HasRole("INFOSEC"):
		return nil
	}
	return platform.ErrForbidden
}

type Person struct {
	ID       uuid.UUID `json:"id"`
	FullName string    `json:"fullName"`
}

type Nomination struct {
	ID           uuid.UUID  `json:"id"`
	Nominee      Person     `json:"nominee"`
	ProposedBy   Person     `json:"proposedBy"`
	Recovery     bool       `json:"recovery"`
	Status       string     `json:"status"`
	CreatedAt    time.Time  `json:"createdAt"`
	ExpiresAt    time.Time  `json:"expiresAt"`
	DecidedBy    *uuid.UUID `json:"decidedBy"`
	DecidedAt    *time.Time `json:"decidedAt"`
	Reason       *string    `json:"reason"`
	Version      int        `json:"version"`
	CanAuthorize bool       `json:"canAuthorize"` // for the caller (UI convenience; the server decides)
}

const nominationColumns = `n.id, ne.id, ne.full_name, pe.id, pe.full_name, n.recovery, n.status, n.created_at, n.expires_at,
	n.decided_by, n.decided_at, n.reason, n.version`

const nominationFrom = ` FROM super_admin_nominations n JOIN employees ne ON ne.id = n.nominee_id JOIN employees pe ON pe.id = n.proposed_by`

func scanNomination(row pgx.Row) (Nomination, error) {
	var n Nomination
	err := row.Scan(&n.ID, &n.Nominee.ID, &n.Nominee.FullName, &n.ProposedBy.ID, &n.ProposedBy.FullName, &n.Recovery, &n.Status,
		&n.CreatedAt, &n.ExpiresAt, &n.DecidedBy, &n.DecidedAt, &n.Reason, &n.Version)
	return n, err
}

type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// activeSuperAdmins counts active Super Admins other than the employees in except.
func activeSuperAdmins(ctx context.Context, q querier, except ...uuid.UUID) (int, error) {
	var n int
	err := q.QueryRow(ctx, `SELECT count(*) FROM role_assignments r JOIN employees e ON e.id = r.employee_id
		WHERE r.role = 'SUPER_ADMIN' AND r.valid_to IS NULL AND e.status = 'ACTIVE' AND NOT (e.id = ANY($1))`, append([]uuid.UUID{}, except...)).Scan(&n)
	return n, err
}

// Nominate proposes nominee as a Super Admin (D2). Without an active Super Admin it is a
// recovery nomination by an HR Manager. Runs under LifecycleLockKey, like every change to
// who is active, so "no active Super Admin" is never decided on stale data.
func (s *Service) Nominate(ctx context.Context, actor auth.Principal, nominee uuid.UUID) (Nomination, error) {
	var n Nomination
	err := platform.InTx(ctx, s.pool, func(tx pgx.Tx, now time.Time) error {
		if err := LockLifecycleAs(ctx, tx, actor); err != nil {
			return err
		}
		active, err := activeSuperAdmins(ctx, tx)
		if err != nil {
			return err
		}
		recovery := active == 0
		if err := checkNominator(actor, nominee, recovery); err != nil {
			return err
		}
		target, err := LockEmployee(ctx, tx, nominee)
		if err != nil {
			return err
		}
		switch {
		case target.Status != "ACTIVE":
			return errNotActive
		case slices.Contains(target.Roles, "SUPER_ADMIN"):
			return errAlreadySuperAdmin
		}
		// No job closes nominations (O8): an expired one, or a recovery one outlived by
		// recovery, is closed here so it no longer blocks a new one.
		rows, _ := tx.Query(ctx, `UPDATE super_admin_nominations SET version = version + 1, decided_at = $2,
				status = CASE WHEN expires_at <= $2 THEN 'EXPIRED' ELSE 'CANCELLED' END
			WHERE nominee_id = $1 AND status = 'PENDING' AND (expires_at <= $2 OR recovery AND $3)
			RETURNING id, status`, nominee, now, !recovery)
		type closed struct {
			ID     uuid.UUID
			Status string
		}
		stale, err := pgx.CollectRows(rows, pgx.RowToStructByPos[closed])
		if err != nil {
			return err
		}
		for _, c := range stale {
			if err := audit.Record(ctx, tx, audit.Event{Action: "SUPER_ADMIN_NOMINATION_" + c.Status, Entity: "super_admin_nomination",
				EntityID: &c.ID, SubjectID: &nominee}); err != nil {
				return err
			}
		}
		var id uuid.UUID
		err = tx.QueryRow(ctx, `INSERT INTO super_admin_nominations (nominee_id, proposed_by, recovery, created_at, expires_at)
			VALUES ($1, $2, $3, $4, $5) RETURNING id`, nominee, actor.EmployeeID, recovery, now, now.Add(NominationLifetime)).Scan(&id)
		if isUniqueViolation(err) {
			return errNominationPending
		}
		if err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{ActorID: &actor.EmployeeID, Action: "SUPER_ADMIN_NOMINATED", Entity: "super_admin_nomination",
			EntityID: &id, SubjectID: &nominee, Details: map[string]any{"recovery": recovery}}); err != nil {
			return err
		}
		authorizers := "INFOSEC"
		if others, err := activeSuperAdmins(ctx, tx, actor.EmployeeID, nominee); err != nil {
			return err
		} else if !recovery && others > 0 {
			authorizers = "SUPER_ADMIN"
		}
		if err := notify.ToRole(ctx, tx, authorizers, "SUPER_ADMIN_NOMINATED", "super_admin_nomination", id,
			map[string]any{"nominee": target.FullName}); err != nil {
			return err
		}
		n, err = scanNomination(tx.QueryRow(ctx, "SELECT "+nominationColumns+nominationFrom+" WHERE n.id = $1", id))
		return err
	})
	return n, err
}

// Decide authorizes or declines a pending nomination (D2). Authorizing adds the Super Admin
// role; the nominee's other roles and groups stay. Concurrent decisions are serialized by
// LifecycleLockKey and the nomination's version.
func (s *Service) Decide(ctx context.Context, actor auth.Principal, id uuid.UUID, version int, authorize bool, reason *string) (Nomination, error) {
	var n Nomination
	err := platform.InTx(ctx, s.pool, func(tx pgx.Tx, now time.Time) error {
		if err := LockLifecycleAs(ctx, tx, actor); err != nil {
			return err
		}
		cur, err := scanNomination(tx.QueryRow(ctx, "SELECT "+nominationColumns+nominationFrom+" WHERE n.id = $1 FOR UPDATE OF n", id))
		if errors.Is(err, pgx.ErrNoRows) {
			return platform.ErrNotFound
		}
		if err != nil {
			return err
		}
		active, err := activeSuperAdmins(ctx, tx)
		if err != nil {
			return err
		}
		others, err := activeSuperAdmins(ctx, tx, cur.ProposedBy.ID, cur.Nominee.ID)
		if err != nil {
			return err
		}
		switch {
		case cur.Status != "PENDING":
			return errNominationClosed
		case !cur.ExpiresAt.After(now):
			return errNominationExpired
		case cur.Version != version:
			return errVersionConflict
		case cur.Recovery && active > 0:
			return errRecoveryEnded // a Super Admin is back: they nominate the usual way
		}
		if err := checkAuthorizer(actor, cur.Nominee.ID, cur.ProposedBy.ID, cur.Recovery, others > 0); err != nil {
			return err
		}
		status, action := "DECLINED", "SUPER_ADMIN_NOMINATION_DECLINED"
		if authorize {
			status, action = "AUTHORIZED", "SUPER_ADMIN_NOMINATION_AUTHORIZED"
			nominee, err := LockEmployee(ctx, tx, cur.Nominee.ID)
			if err != nil {
				return err
			}
			switch {
			case nominee.Status != "ACTIVE":
				return errNotActive
			case slices.Contains(nominee.Roles, "SUPER_ADMIN"): // e.g. made one by recover-admin meanwhile
				return errAlreadySuperAdmin
			}
			if _, err := tx.Exec(ctx, "INSERT INTO role_assignments (employee_id, role, valid_from, granted_by) VALUES ($1, 'SUPER_ADMIN', $2, $3)",
				nominee.ID, now, actor.EmployeeID); err != nil {
				return err
			}
			if err := audit.Record(ctx, tx, audit.Event{ActorID: &actor.EmployeeID, Action: "ROLE_ASSIGNED", Entity: "employee",
				EntityID: &nominee.ID, SubjectID: &nominee.ID, Details: map[string]any{"role": "SUPER_ADMIN", "nominationId": id}}); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE super_admin_nominations SET status = $2, decided_by = $3, decided_at = $4, reason = $5,
			version = version + 1 WHERE id = $1`, id, status, actor.EmployeeID, now, reason); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{ActorID: &actor.EmployeeID, Action: action, Entity: "super_admin_nomination", EntityID: &id,
			SubjectID: &cur.Nominee.ID, Reason: deref(reason), Details: map[string]any{"proposedBy": cur.ProposedBy.ID, "recovery": cur.Recovery}}); err != nil {
			return err
		}
		n, err = scanNomination(tx.QueryRow(ctx, "SELECT "+nominationColumns+nominationFrom+" WHERE n.id = $1", id))
		return err
	})
	return n, err
}

// seesSuccession: who sees nominations and the Super Admin health (D2).
func seesSuccession(p auth.Principal) bool {
	return hasAny(p, "SUPER_ADMIN", "INFOSEC", "HR_MANAGER", "AUDITOR")
}

// Nominations lists the open, unexpired nominations, each saying whether the caller may
// authorize it. Others see none.
func (s *Service) Nominations(ctx context.Context, actor auth.Principal) ([]Nomination, error) {
	if !seesSuccession(actor) {
		return []Nomination{}, nil
	}
	rows, _ := s.pool.Query(ctx, "SELECT "+nominationColumns+nominationFrom+
		" WHERE n.status = 'PENDING' AND n.expires_at > now() ORDER BY n.created_at")
	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Nomination, error) { return scanNomination(row) })
	if err != nil {
		return nil, err
	}
	active, err := activeSuperAdmins(ctx, s.pool)
	if err != nil {
		return nil, err
	}
	// A recovery nomination is moot once a Super Admin is active again (closed at the next nomination).
	list = slices.DeleteFunc(list, func(n Nomination) bool { return n.Recovery && active > 0 })
	for i := range list {
		n := &list[i]
		others, cerr := activeSuperAdmins(ctx, s.pool, n.ProposedBy.ID, n.Nominee.ID)
		if cerr != nil {
			return nil, cerr
		}
		n.CanAuthorize = checkAuthorizer(actor, n.Nominee.ID, n.ProposedBy.ID, n.Recovery, others > 0) == nil
	}
	return list, nil
}

type Health struct {
	ActiveSuperAdmins int  `json:"activeSuperAdmins"`
	InfoSecMembers    int  `json:"infosecMembers"`
	RecoveryRequired  bool `json:"recoveryRequired"` // ADMIN_RECOVERY_REQUIRED: no active Super Admin
	Warning           bool `json:"warning"`          // fewer than two active Super Admins
}

// Health is the Super Admin health check (D2).
func (s *Service) Health(ctx context.Context, actor auth.Principal) (Health, error) {
	var h Health
	if !seesSuccession(actor) {
		return h, platform.ErrForbidden
	}
	err := s.pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE r.role = 'SUPER_ADMIN'), count(*) FILTER (WHERE r.role = 'INFOSEC')
		FROM role_assignments r JOIN employees e ON e.id = r.employee_id WHERE r.valid_to IS NULL AND e.status = 'ACTIVE'`).
		Scan(&h.ActiveSuperAdmins, &h.InfoSecMembers)
	h.RecoveryRequired, h.Warning = h.ActiveSuperAdmins == 0, h.ActiveSuperAdmins < 2
	return h, err
}

// AfterLeaving runs in the transaction that terminated e: it cancels the open nominations e
// proposed or was proposed in, and when e was the last active Super Admin it records
// ADMIN_RECOVERY_REQUIRED and tells HR and InfoSec (D2). Only a termination can leave no
// active Super Admin: a suspension is made by an active Super Admin. The caller holds
// LifecycleLockKey.
func AfterLeaving(ctx context.Context, tx pgx.Tx, now time.Time, actor *uuid.UUID, e Employee) error {
	rows, _ := tx.Query(ctx, `UPDATE super_admin_nominations SET status = 'CANCELLED', decided_at = $2, version = version + 1
		WHERE status = 'PENDING' AND (nominee_id = $1 OR proposed_by = $1) RETURNING id, nominee_id`, e.ID, now)
	type cancelled struct{ ID, Nominee uuid.UUID }
	list, err := pgx.CollectRows(rows, pgx.RowToStructByPos[cancelled])
	if err != nil {
		return err
	}
	for _, c := range list {
		if err := audit.Record(ctx, tx, audit.Event{ActorID: actor, Action: "SUPER_ADMIN_NOMINATION_CANCELLED", Entity: "super_admin_nomination",
			EntityID: &c.ID, SubjectID: &c.Nominee, Details: map[string]any{"left": e.ID}}); err != nil {
			return err
		}
	}
	// A suspended Super Admin was no longer active: recovery, if any, began earlier.
	if e.Status != "ACTIVE" || !slices.Contains(e.Roles, "SUPER_ADMIN") {
		return nil
	}
	if active, err := activeSuperAdmins(ctx, tx); err != nil || active > 0 {
		return err
	}
	if err := audit.Record(ctx, tx, audit.Event{ActorID: actor, Action: "ADMIN_RECOVERY_REQUIRED", Entity: "employee", EntityID: &e.ID,
		SubjectID: &e.ID}); err != nil {
		return err
	}
	for _, role := range []string{"HR_MANAGER", "INFOSEC"} {
		if err := notify.ToRole(ctx, tx, role, "ADMIN_RECOVERY_REQUIRED", "employee", e.ID, nil); err != nil {
			return err
		}
	}
	return nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
