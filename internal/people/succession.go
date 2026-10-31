package people

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
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
	errNeedsAcceptance    = platform.NewError(http.StatusConflict, "nomination_needs_acceptance")
	errInvalidRoleLink    = platform.NewError(http.StatusBadRequest, "invalid_role_link")
	errSuperAdminsActive  = platform.NewError(http.StatusConflict, "super_admins_active") // succession is over
	errNotAuthorized      = platform.NewError(http.StatusConflict, "nomination_not_authorized")
	// ErrLastSuperAdmin: the last active Super Admin's employment ends only once a successor
	// has accepted the role (D2 note 2026-10-30).
	ErrLastSuperAdmin = platform.NewError(http.StatusConflict, "last_super_admin")
)

// checkNominator (D2): a Super Admin proposes someone else for the two-person promotion. An
// HR Manager who is not a Super Admin starts a succession while at most one Super Admin is
// not terminated (standing: a suspended one still counts, so HR cannot suspend their way into
// a succession, F-1); an independent authorizer and the nominee's acceptance follow (notes
// 2026-10-30 and 2026-10-31). Nobody nominates themself.
func checkNominator(actor auth.Principal, nominee uuid.UUID, standing int) (succession bool, err error) {
	switch {
	case actor.EmployeeID == nominee:
		return false, errSelfChange
	case actor.HasRole("SUPER_ADMIN"):
		return false, nil
	case actor.HasRole("HR_MANAGER") && standing <= 1:
		return true, nil
	}
	return false, platform.ErrForbidden
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

// checkSuccessionAuthorizer (D2 note 2026-10-31, R-2): HR's succession nomination is
// authorized by an InfoSec member or an active Super Admin who is neither HR's proposer nor
// the nominee. Only active employees have a session, so the authorizer is active.
func checkSuccessionAuthorizer(actor auth.Principal, nominee, proposer uuid.UUID) error {
	switch {
	case actor.EmployeeID == nominee || actor.EmployeeID == proposer:
		return errSeparation
	case actor.HasRole("SUPER_ADMIN") || actor.HasRole("INFOSEC"):
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
	Succession   bool       `json:"succession"`   // by HR, authorized, then accepted by the nominee through a link
	AuthorizedBy *uuid.UUID `json:"authorizedBy"` // a succession's authorizer; the link is sent only then
	AuthorizedAt *time.Time `json:"authorizedAt"`
	Status       string     `json:"status"`
	CreatedAt    time.Time  `json:"createdAt"`
	ExpiresAt    time.Time  `json:"expiresAt"`
	DecidedBy    *uuid.UUID `json:"decidedBy"`
	DecidedAt    *time.Time `json:"decidedAt"`
	Reason       *string    `json:"reason"`
	Version      int        `json:"version"`
	CanAuthorize bool       `json:"canAuthorize"` // for the caller (UI convenience; the server decides)
}

const nominationColumns = `n.id, ne.id, ne.full_name, pe.id, pe.full_name, n.recovery, n.succession, n.status, n.created_at, n.expires_at,
	n.decided_by, n.decided_at, n.reason, n.version, n.authorized_by, n.authorized_at`

const nominationFrom = ` FROM super_admin_nominations n JOIN employees ne ON ne.id = n.nominee_id JOIN employees pe ON pe.id = n.proposed_by`

func scanNomination(row pgx.Row) (Nomination, error) {
	var n Nomination
	err := row.Scan(&n.ID, &n.Nominee.ID, &n.Nominee.FullName, &n.ProposedBy.ID, &n.ProposedBy.FullName, &n.Recovery, &n.Succession, &n.Status,
		&n.CreatedAt, &n.ExpiresAt, &n.DecidedBy, &n.DecidedAt, &n.Reason, &n.Version, &n.AuthorizedBy, &n.AuthorizedAt)
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

// standingSuperAdmins counts Super Admins who are not terminated, active or suspended: HR's
// succession is open only while at most one stands (F-1).
func standingSuperAdmins(ctx context.Context, q querier) (int, error) {
	var n int
	err := q.QueryRow(ctx, `SELECT count(*) FROM role_assignments r JOIN employees e ON e.id = r.employee_id
		WHERE r.role = 'SUPER_ADMIN' AND r.valid_to IS NULL AND e.status <> 'TERMINATED'`).Scan(&n)
	return n, err
}

// Nominate proposes nominee as a Super Admin (D2): a promotion by a Super Admin, or a
// succession by an HR Manager, whose nominee gets a role-acceptance link. Runs under
// LifecycleLockKey, like every change to who is active, so the count of active Super Admins
// is never stale.
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
		standing, err := standingSuperAdmins(ctx, tx)
		if err != nil {
			return err
		}
		succession, err := checkNominator(actor, nominee, standing)
		if err != nil {
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
		case slices.Contains(target.Roles, "AUDITOR"): // an Auditor holds no duty (O7 note 2026-10-31)
			return &platform.Error{Status: http.StatusConflict, Code: "employee_is_auditor", Field: "nomineeId"}
		}
		// No job closes nominations (O8): an expired one, or a recovery one outlived by
		// recovery, is closed here so it no longer blocks a new one; a new succession also
		// closes every expired succession, whoever its nominee.
		rows, _ := tx.Query(ctx, `UPDATE super_admin_nominations SET version = version + 1, decided_at = $2,
				status = CASE WHEN expires_at <= $2 THEN 'EXPIRED' ELSE 'CANCELLED' END
			WHERE status = 'PENDING' AND (nominee_id = $1 AND (expires_at <= $2 OR recovery AND NOT succession AND $3)
				OR $4 AND succession AND expires_at <= $2)
			RETURNING id, status`, nominee, now, !recovery, succession)
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
		if succession { // one successor at a time
			var open bool
			if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM super_admin_nominations WHERE succession AND status = 'PENDING')").Scan(&open); err != nil {
				return err
			}
			if open {
				return errNominationPending
			}
		}
		var id uuid.UUID
		err = tx.QueryRow(ctx, `INSERT INTO super_admin_nominations (nominee_id, proposed_by, recovery, succession, created_at, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`, nominee, actor.EmployeeID, recovery, succession, now, now.Add(NominationLifetime)).Scan(&id)
		if isUniqueViolation(err) {
			return errNominationPending
		}
		if err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{ActorID: &actor.EmployeeID, Action: "SUPER_ADMIN_NOMINATED", Entity: "super_admin_nomination",
			EntityID: &id, SubjectID: &nominee, Details: map[string]any{"recovery": recovery, "succession": succession}}); err != nil {
			return err
		}
		if succession { // no link yet: InfoSec or an active Super Admin authorizes first (R-2)
			for _, role := range []string{"INFOSEC", "SUPER_ADMIN"} {
				if err := notify.ToRole(ctx, tx, role, "SUPER_ADMIN_NOMINATED", "super_admin_nomination", id,
					map[string]any{"nominee": target.FullName}, actor.EmployeeID, nominee); err != nil {
					return err
				}
			}
			n, err = scanNomination(tx.QueryRow(ctx, "SELECT "+nominationColumns+nominationFrom+" WHERE n.id = $1", id))
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
		case cur.Succession && cur.AuthorizedAt != nil:
			return errNeedsAcceptance // the nominee accepts through their link
		case !cur.ExpiresAt.After(now):
			return errNominationExpired
		case cur.Version != version:
			return errVersionConflict
		case cur.Recovery && !cur.Succession && active > 0:
			return errRecoveryEnded // a Super Admin is back: they nominate the usual way
		}
		if cur.Succession {
			if err := s.decideSuccession(ctx, tx, now, actor, cur, authorize, reason); err != nil {
				return err
			}
			n, err = scanNomination(tx.QueryRow(ctx, "SELECT "+nominationColumns+nominationFrom+" WHERE n.id = $1", id))
			return err
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

// decideSuccession authorizes HR's succession nomination, which sends the nominee their
// 24-hour link, or declines it (R-2). The caller holds LifecycleLockKey and the nomination.
func (s *Service) decideSuccession(ctx context.Context, tx pgx.Tx, now time.Time, actor auth.Principal, cur Nomination, authorize bool, reason *string) error {
	if err := checkSuccessionAuthorizer(actor, cur.Nominee.ID, cur.ProposedBy.ID); err != nil {
		return err
	}
	details := map[string]any{"proposedBy": cur.ProposedBy.ID, "recovery": cur.Recovery, "succession": true}
	if !authorize {
		if _, err := tx.Exec(ctx, `UPDATE super_admin_nominations SET status = 'DECLINED', decided_by = $2, decided_at = $3, reason = $4,
			version = version + 1 WHERE id = $1`, cur.ID, actor.EmployeeID, now, reason); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{ActorID: &actor.EmployeeID, Action: "SUPER_ADMIN_NOMINATION_DECLINED", Entity: "super_admin_nomination",
			EntityID: &cur.ID, SubjectID: &cur.Nominee.ID, Reason: deref(reason), Details: details})
	}
	if standing, err := standingSuperAdmins(ctx, tx); err != nil {
		return err
	} else if standing > 1 {
		return errSuperAdminsActive
	}
	nominee, err := LockEmployee(ctx, tx, cur.Nominee.ID)
	if err != nil {
		return err
	}
	switch {
	case nominee.Status != "ACTIVE":
		return errNotActive
	case slices.Contains(nominee.Roles, "SUPER_ADMIN"):
		return errAlreadySuperAdmin
	}
	if _, err := tx.Exec(ctx, "UPDATE super_admin_nominations SET authorized_by = $2, authorized_at = $3, version = version + 1 WHERE id = $1",
		cur.ID, actor.EmployeeID, now); err != nil {
		return err
	}
	if err := audit.Record(ctx, tx, audit.Event{ActorID: &actor.EmployeeID, Action: "SUPER_ADMIN_NOMINATION_AUTHORIZED", Entity: "super_admin_nomination",
		EntityID: &cur.ID, SubjectID: &cur.Nominee.ID, Reason: deref(reason), Details: details}); err != nil {
		return err
	}
	return s.sendRoleLink(ctx, tx, now, actor, cur.ID, cur.Nominee.ID, false)
}

// seesSuccession: who sees nominations and the Super Admin health (D2).
func seesSuccession(p auth.Principal) bool {
	return hasAny(p, "SUPER_ADMIN", "INFOSEC", "HR_MANAGER")
}

// Nominations lists the open, unexpired nominations, each saying whether the caller may
// authorize it, and the open succession nominations, expired ones as EXPIRED so HR can
// reissue their link. Others see none.
func (s *Service) Nominations(ctx context.Context, actor auth.Principal) ([]Nomination, error) {
	if !seesSuccession(actor) {
		return []Nomination{}, nil
	}
	rows, _ := s.pool.Query(ctx, "SELECT "+nominationColumns+nominationFrom+
		" WHERE n.status = 'PENDING' AND (n.expires_at > now() OR n.succession) ORDER BY n.created_at")
	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Nomination, error) { return scanNomination(row) })
	if err != nil {
		return nil, err
	}
	active, err := activeSuperAdmins(ctx, s.pool)
	if err != nil {
		return nil, err
	}
	// A recovery nomination is moot once a Super Admin is active again (closed at the next nomination).
	list = slices.DeleteFunc(list, func(n Nomination) bool { return n.Recovery && !n.Succession && active > 0 })
	for i := range list {
		n := &list[i]
		if n.Succession {
			if !n.ExpiresAt.After(time.Now()) {
				n.Status = "EXPIRED"
			}
			n.CanAuthorize = n.AuthorizedAt == nil && n.Status == "PENDING" && checkSuccessionAuthorizer(actor, n.Nominee.ID, n.ProposedBy.ID) == nil
			continue
		}
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

// AfterLeaving runs in the transaction that terminated or suspended e: it cancels the open
// nominations e proposed or was proposed in (D2), so a blocked account's proposal cannot be
// completed. The caller holds LifecycleLockKey.
func AfterLeaving(ctx context.Context, tx pgx.Tx, now time.Time, actor *uuid.UUID, e Employee) error {
	rows, _ := tx.Query(ctx, `UPDATE super_admin_nominations SET status = 'CANCELLED', decided_at = $2, version = version + 1
		WHERE status = 'PENDING' AND (nominee_id = $1 OR proposed_by = $1) RETURNING id, nominee_id`, e.ID, now)
	type cancelled struct{ ID, Nominee uuid.UUID }
	list, err := pgx.CollectRows(rows, pgx.RowToStructByPos[cancelled])
	if err != nil {
		return err
	}
	for _, c := range list {
		if _, err := tx.Exec(ctx, "UPDATE email_outbox SET sealed_token = NULL WHERE nomination_id = $1", c.ID); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{ActorID: actor, Action: "SUPER_ADMIN_NOMINATION_CANCELLED", Entity: "super_admin_nomination",
			EntityID: &c.ID, SubjectID: &c.Nominee, Details: map[string]any{"left": e.ID}}); err != nil {
			return err
		}
	}
	return nil
}

// CheckNotLastSuperAdmin refuses to end e's employment while no other Super Admin is active:
// HR first appoints a successor, who must accept (D2 note 2026-10-30). The caller holds
// LifecycleLockKey.
func CheckNotLastSuperAdmin(ctx context.Context, tx pgx.Tx, e Employee) error {
	if !slices.Contains(e.Roles, "SUPER_ADMIN") {
		return nil
	}
	others, err := activeSuperAdmins(ctx, tx, e.ID)
	if err == nil && others == 0 {
		err = ErrLastSuperAdmin
	}
	return err
}

// RecordRecovery runs in the transaction that suspended e: when e was the last active Super
// Admin (HR's emergency revocation), it records ADMIN_RECOVERY_REQUIRED and tells HR and
// InfoSec (D2). The caller holds LifecycleLockKey.
func RecordRecovery(ctx context.Context, tx pgx.Tx, actor *uuid.UUID, e Employee) error {
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

// sendRoleLink issues a succession nominee's link (auth.IssueRoleLink), audited without the
// token, and tells the nominee in the app.
func (s *Service) sendRoleLink(ctx context.Context, tx pgx.Tx, now time.Time, actor auth.Principal, id, nominee uuid.UUID, reissued bool) error {
	expires, err := s.auth.IssueRoleLink(ctx, tx, now, id, nominee)
	if err != nil {
		return err
	}
	if err := audit.Record(ctx, tx, audit.Event{ActorID: &actor.EmployeeID, Action: "SUPER_ADMIN_ROLE_LINK_ISSUED", Entity: "super_admin_nomination",
		EntityID: &id, SubjectID: &nominee, Details: map[string]any{"expiresAt": expires, "reissued": reissued}}); err != nil {
		return err
	}
	return notify.ToEmployee(ctx, tx, nominee, "SUPER_ADMIN_ROLE_OFFERED", "super_admin_nomination", id, nil)
}

// ReissueRoleLink replaces a succession nominee's link with a new 24-hour one, also after it
// expired (D2): by an HR Manager other than the nominee.
func (s *Service) ReissueRoleLink(ctx context.Context, actor auth.Principal, id uuid.UUID, version int) (Nomination, error) {
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
		switch {
		case !actor.HasRole("HR_MANAGER"):
			return platform.ErrForbidden
		case actor.EmployeeID == cur.Nominee.ID:
			return errSelfChange
		case !cur.Succession || cur.Status != "PENDING":
			return errNominationClosed
		case cur.AuthorizedAt == nil:
			return errNotAuthorized // no link exists before authorization (R-2)
		case cur.Version != version:
			return errVersionConflict
		}
		if standing, err := standingSuperAdmins(ctx, tx); err != nil {
			return err
		} else if standing > 1 {
			return errSuperAdminsActive
		}
		if nominee, err := LockEmployee(ctx, tx, cur.Nominee.ID); err != nil {
			return err
		} else if nominee.Status != "ACTIVE" {
			return errNotActive
		}
		if _, err := tx.Exec(ctx, "UPDATE super_admin_nominations SET version = version + 1 WHERE id = $1", id); err != nil {
			return err
		}
		if err := s.sendRoleLink(ctx, tx, now, actor, id, cur.Nominee.ID, true); err != nil {
			return err
		}
		n, err = scanNomination(tx.QueryRow(ctx, "SELECT "+nominationColumns+nominationFrom+" WHERE n.id = $1", id))
		return err
	})
	return n, err
}

// AcceptRole makes the signed-in nominee of a succession nomination a Super Admin through
// their link (D2 note 2026-10-30). The password is untouched; the nominee's sessions end so
// they sign in again with the new role. A used, replaced, expired or someone else's link gets
// the same answer, and a known link's failure is audited.
func (s *Service) AcceptRole(ctx context.Context, actor auth.Principal, token string) error {
	hash := auth.RoleLinkHash(token)
	if hash == nil {
		return errInvalidRoleLink
	}
	failed, outcome := false, errInvalidRoleLink
	err := platform.InTx(ctx, s.pool, func(tx pgx.Tx, now time.Time) error {
		failed, outcome = false, errInvalidRoleLink
		if err := LockLifecycleAs(ctx, tx, actor); err != nil {
			return err
		}
		cur, err := scanNomination(tx.QueryRow(ctx, "SELECT "+nominationColumns+nominationFrom+" WHERE n.token_hash = $1 FOR UPDATE OF n", hash))
		if errors.Is(err, pgx.ErrNoRows) { // unknown, or replaced by a reissue
			failed = true
			return audit.Record(ctx, tx, audit.Event{ActorID: &actor.EmployeeID, Action: "SUPER_ADMIN_ROLE_ACCEPT_FAILED", Entity: "super_admin_nomination",
				Details: map[string]any{"reason": "unknown_link"}})
		}
		if err != nil {
			return err
		}
		standing, err := standingSuperAdmins(ctx, tx)
		if err != nil {
			return err
		}
		var proposerHR bool
		if err := tx.QueryRow(ctx, `SELECT e.status = 'ACTIVE' AND EXISTS (SELECT 1 FROM role_assignments r
			WHERE r.employee_id = e.id AND r.role = 'HR_MANAGER' AND r.valid_to IS NULL) FROM employees e WHERE e.id = $1`, cur.ProposedBy.ID).Scan(&proposerHR); err != nil {
			return err
		}
		reason := ""
		switch {
		case cur.Status != "PENDING":
			reason = strings.ToLower(cur.Status)
		case !cur.ExpiresAt.After(now):
			reason = "expired"
		case actor.EmployeeID != cur.Nominee.ID:
			reason = "other_account"
		case cur.AuthorizedAt == nil: // a link sent before the independent authorization rule (R-2)
			reason = "not_authorized"
		case standing > 1:
			reason = "super_admins_active"
		case !proposerHR: // HR's half of the two-person rule no longer stands
			reason = "proposer_not_hr"
		}
		if reason != "" {
			failed = true
			if reason == "super_admins_active" {
				outcome = errSuperAdminsActive
			}
			return audit.Record(ctx, tx, audit.Event{ActorID: &actor.EmployeeID, Action: "SUPER_ADMIN_ROLE_ACCEPT_FAILED", Entity: "super_admin_nomination",
				EntityID: &cur.ID, SubjectID: &cur.Nominee.ID, Details: map[string]any{"reason": reason}})
		}
		nominee, err := LockEmployee(ctx, tx, cur.Nominee.ID)
		if err != nil {
			return err
		}
		if slices.Contains(nominee.Roles, "SUPER_ADMIN") { // e.g. made one by recover-admin meanwhile
			return errAlreadySuperAdmin
		}
		if _, err := tx.Exec(ctx, "INSERT INTO role_assignments (employee_id, role, valid_from, granted_by) VALUES ($1, 'SUPER_ADMIN', $2, $3)",
			nominee.ID, now, cur.ProposedBy.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE super_admin_nominations SET status = 'ACCEPTED', decided_by = $2, decided_at = $3, version = version + 1
			WHERE id = $1`, cur.ID, nominee.ID, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "UPDATE email_outbox SET sealed_token = NULL WHERE nomination_id = $1", cur.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "UPDATE employees SET token_version = token_version + 1 WHERE id = $1", nominee.ID); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{ActorID: &nominee.ID, Action: "ROLE_ASSIGNED", Entity: "employee", EntityID: &nominee.ID,
			SubjectID: &nominee.ID, Details: map[string]any{"role": "SUPER_ADMIN", "nominationId": cur.ID, "grantedBy": cur.ProposedBy.ID}}); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{ActorID: &nominee.ID, Action: "SUPER_ADMIN_ROLE_ACCEPTED", Entity: "super_admin_nomination",
			EntityID: &cur.ID, SubjectID: &nominee.ID, Details: map[string]any{"proposedBy": cur.ProposedBy.ID, "authorizedBy": cur.AuthorizedBy,
				"recovery": cur.Recovery}}); err != nil {
			return err
		}
		details := map[string]any{"nominee": nominee.FullName}
		for _, role := range []string{"SUPER_ADMIN", "INFOSEC"} { // every privileged user learns of the new Super Admin
			if err := notify.ToRole(ctx, tx, role, "SUPER_ADMIN_ROLE_ACCEPTED", "super_admin_nomination", cur.ID, details,
				nominee.ID, cur.ProposedBy.ID); err != nil {
				return err
			}
		}
		return notify.ToEmployee(ctx, tx, cur.ProposedBy.ID, "SUPER_ADMIN_ROLE_ACCEPTED", "super_admin_nomination", cur.ID, details)
	})
	if err == nil && failed {
		err = outcome
	}
	return err
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
