package catalog

import (
	"cmp"
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/esraahaytaa-eng/access-flow/internal/audit"
	"github.com/esraahaytaa-eng/access-flow/internal/auth"
	"github.com/esraahaytaa-eng/access-flow/internal/notify"
	"github.com/esraahaytaa-eng/access-flow/internal/people"
	"github.com/esraahaytaa-eng/access-flow/internal/platform"
	"github.com/esraahaytaa-eng/access-flow/internal/reviews"
)

var (
	errInvalidMethod   = platform.ValidationError("fulfillmentMethod", "invalid_fulfillment_method")
	errAccountKind     = platform.ValidationError("accountKind", "invalid_account_kind")
	errAccountKindAPI  = platform.ValidationError("accountKind", "account_kind_requires_api")
	errOwnerNotActive  = platform.ValidationError("ownerId", "owner_not_active")
	errGroupNotFound   = platform.ValidationError("adminGroupId", "admin_group_not_found")
	errToolNameTaken   = platform.NewError(http.StatusConflict, "tool_name_taken")
	errLevelNameTaken  = platform.NewError(http.StatusConflict, "level_name_taken")
	errToolRetired     = platform.NewError(http.StatusConflict, "tool_retired")
	errLevelRetired    = platform.NewError(http.StatusConflict, "level_retired")
	errVersionConflict = platform.NewError(http.StatusConflict, "version_conflict")
)

var methods = map[string]bool{"API": true, "MANUAL": true, "VENDOR": true}

// Service runs the catalog transactions.
type Service struct {
	pool *pgxpool.Pool
	// OwnerChanged, when set, runs in a tool owner change's transaction after its review items
	// moved: requests.ToolOwnerChanged, set by main (requests imports catalog, not the reverse).
	OwnerChanged func(ctx context.Context, tx pgx.Tx, actor *uuid.UUID, tool uuid.UUID) error
}

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

func actorRef(p auth.Principal) *uuid.UUID {
	if p.EmployeeID == uuid.Nil {
		return nil // the system (demo seed)
	}
	return &p.EmployeeID
}

// Tool is the administrative view of a tool.
type Tool struct {
	ID                uuid.UUID `json:"id"`
	Name              string    `json:"name"`
	FulfillmentMethod string    `json:"fulfillmentMethod"`
	OwnerID           uuid.UUID `json:"ownerId"`
	AdminGroupID      uuid.UUID `json:"adminGroupId"`
	AccountKind       string    `json:"accountKind,omitempty"` // empty: the account is the employee's email
	Status            string    `json:"status"`
	Version           int       `json:"version"`
}

// Level is the administrative view of an access level.
type Level struct {
	ID                uuid.UUID `json:"id"`
	ToolID            uuid.UUID `json:"toolId"`
	Name              string    `json:"name"`
	RequiresToolOwner bool      `json:"requiresToolOwner"`
	RequiresInfosec   bool      `json:"requiresInfosec"`
	Steps             []string  `json:"steps"`
	Status            string    `json:"status"`
	Version           int       `json:"version"`
}

// AccountGitHubLogin is the one tool account kind besides the email: a GitHub username,
// for the GitHub sandbox adapter (DECISIONS.md A1 note 2026-11-01).
const AccountGitHubLogin = "GITHUB_LOGIN"

// NewTool is the input for CreateTool. The method is fixed for the tool's lifetime.
type NewTool struct {
	Name, FulfillmentMethod string
	OwnerID, AdminGroupID   uuid.UUID
	AccountKind             string // "" or GITHUB_LOGIN; fixed once the tool exists
}

// ToolUpdate carries every editable tool field and the version it was read at.
type ToolUpdate struct {
	Name                  string
	OwnerID, AdminGroupID uuid.UUID
	Version               int
}

// NewLevel is the input for CreateLevel.
type NewLevel struct {
	Name                               string
	RequiresToolOwner, RequiresInfosec bool
}

// LevelUpdate carries every editable level field and the version it was read at.
type LevelUpdate struct {
	Name                               string
	RequiresToolOwner, RequiresInfosec bool
	Version                            int
}

func isUnique(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// checkOwner: a Tool Owner must be ACTIVE and not an Auditor. FOR SHARE holds off a concurrent termination
// or suspension of the owner until this transaction commits.
func checkOwner(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	var status string
	err := tx.QueryRow(ctx, "SELECT status FROM employees WHERE id = $1 FOR SHARE", id).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && status != "ACTIVE") {
		return errOwnerNotActive
	}
	if err != nil {
		return err
	}
	return people.CheckNotAuditor(ctx, tx, id, "ownerId") // an Auditor decides no Tool Owner step or review (O7 note 2026-10-31)
}

func checkGroup(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	var ok bool
	if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM admin_groups WHERE id = $1)", id).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return errGroupNotFound
	}
	return nil
}

const toolColumns = "id, name, fulfillment_method, owner_id, admin_group_id, coalesce(account_kind, ''), status, version"

func scanTool(row pgx.Row) (Tool, error) {
	var t Tool
	err := row.Scan(&t.ID, &t.Name, &t.FulfillmentMethod, &t.OwnerID, &t.AdminGroupID, &t.AccountKind, &t.Status, &t.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return Tool{}, platform.ErrNotFound
	}
	return t, err
}

// lockTool locks the tool row. Every catalog write for a tool and its levels takes this
// lock first, so retiring a tool and changing its levels are serialized.
func lockTool(ctx context.Context, tx pgx.Tx, id uuid.UUID) (Tool, error) {
	return scanTool(tx.QueryRow(ctx, "SELECT "+toolColumns+" FROM tools WHERE id = $1 FOR UPDATE", id))
}

const levelColumns = "id, tool_id, name, requires_tool_owner, requires_infosec, status, version"

func scanLevel(row pgx.Row) (Level, error) {
	var l Level
	err := row.Scan(&l.ID, &l.ToolID, &l.Name, &l.RequiresToolOwner, &l.RequiresInfosec, &l.Status, &l.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return Level{}, platform.ErrNotFound
	}
	l.Steps = Steps(l.RequiresToolOwner, l.RequiresInfosec)
	return l, err
}

// getLevel reads a level of tool; call it after lockTool, as a new statement, so it sees
// changes committed while the lock was awaited.
func getLevel(ctx context.Context, tx pgx.Tx, toolID, id uuid.UUID) (Level, error) {
	return scanLevel(tx.QueryRow(ctx, "SELECT "+levelColumns+" FROM access_levels WHERE id = $1 AND tool_id = $2", id, toolID))
}

// CreateTool adds a tool with its owner and responsible admin group.
func (s *Service) CreateTool(ctx context.Context, actor auth.Principal, in NewTool) (Tool, error) {
	var tool Tool
	err := platform.InTx(ctx, s.pool, func(tx pgx.Tx, now time.Time) (err error) {
		tool, err = CreateToolTx(ctx, tx, now, actor, in)
		return err
	})
	return tool, err
}

// CreateToolTx is CreateTool in the caller's transaction (the onboarding import, M17), with
// every check: Super Admin, a valid name and method, an active owner, an existing group.
func CreateToolTx(ctx context.Context, tx pgx.Tx, now time.Time, actor auth.Principal, in NewTool) (Tool, error) {
	if err := canEdit(actor); err != nil {
		return Tool{}, err
	}
	name, err := cleanName(in.Name)
	if err != nil {
		return Tool{}, err
	}
	if !methods[in.FulfillmentMethod] {
		return Tool{}, errInvalidMethod
	}
	switch {
	case in.AccountKind != "" && in.AccountKind != AccountGitHubLogin:
		return Tool{}, errAccountKind
	case in.AccountKind != "" && in.FulfillmentMethod != "API": // only an API tool looks the account up and acts on it
		return Tool{}, errAccountKindAPI
	}
	if err := checkOwner(ctx, tx, in.OwnerID); err != nil {
		return Tool{}, err
	}
	if err := checkGroup(ctx, tx, in.AdminGroupID); err != nil {
		return Tool{}, err
	}
	tool, err := scanTool(tx.QueryRow(ctx, `INSERT INTO tools (name, fulfillment_method, owner_id, admin_group_id, account_kind, seeded, created_by, created_at)
		VALUES ($1, $2, $3, $4, nullif($5, ''), $6, $7, $8) RETURNING `+toolColumns,
		name, in.FulfillmentMethod, in.OwnerID, in.AdminGroupID, in.AccountKind, actor.EmployeeID == uuid.Nil, actorRef(actor), now))
	if isUnique(err) {
		return Tool{}, errToolNameTaken
	}
	if err != nil {
		return Tool{}, err
	}
	return tool, audit.Record(ctx, tx, audit.Event{ActorID: actorRef(actor), Action: "TOOL_CREATED", Entity: "tool", EntityID: &tool.ID,
		Details: map[string]any{"name": name, "fulfillmentMethod": in.FulfillmentMethod, "ownerId": in.OwnerID, "adminGroupId": in.AdminGroupID,
			"accountKind": cmp.Or(in.AccountKind, "EMAIL")}})
}

// UpdateTool changes a tool's name, owner or admin group; an unchanged edit does nothing.
func (s *Service) UpdateTool(ctx context.Context, actor auth.Principal, id uuid.UUID, in ToolUpdate) (Tool, error) {
	if err := canEdit(actor); err != nil {
		return Tool{}, err
	}
	name, err := cleanName(in.Name)
	if err != nil {
		return Tool{}, err
	}
	var tool Tool
	err = platform.InTx(ctx, s.pool, func(tx pgx.Tx, _ time.Time) error {
		cur, err := lockTool(ctx, tx, id)
		if err != nil {
			return err
		}
		if cur.Status == "RETIRED" {
			return errToolRetired
		}
		if cur.Version != in.Version {
			return errVersionConflict
		}
		changes := map[string]any{}
		if name != cur.Name {
			changes["name"] = map[string]any{"from": cur.Name, "to": name}
		}
		if in.OwnerID != cur.OwnerID {
			if err := checkOwner(ctx, tx, in.OwnerID); err != nil {
				return err
			}
			changes["ownerId"] = map[string]any{"from": cur.OwnerID, "to": in.OwnerID}
		}
		if in.AdminGroupID != cur.AdminGroupID {
			if err := checkGroup(ctx, tx, in.AdminGroupID); err != nil {
				return err
			}
			changes["adminGroupId"] = map[string]any{"from": cur.AdminGroupID, "to": in.AdminGroupID}
		}
		if len(changes) == 0 {
			tool = cur
			return nil
		}
		tool, err = scanTool(tx.QueryRow(ctx, `UPDATE tools SET name = $2, owner_id = $3, admin_group_id = $4, version = version + 1
			WHERE id = $1 RETURNING `+toolColumns, id, name, in.OwnerID, in.AdminGroupID))
		if isUnique(err) {
			return errToolNameTaken
		}
		if err != nil {
			return err
		}
		if in.OwnerID != cur.OwnerID {
			if err := reviews.OwnerChanged(ctx, tx, actorRef(actor), id, in.OwnerID); err != nil {
				return err
			}
			if s.OwnerChanged != nil {
				if err := s.OwnerChanged(ctx, tx, actorRef(actor), id); err != nil {
					return err
				}
			}
		}
		return audit.Record(ctx, tx, audit.Event{ActorID: actorRef(actor), Action: "TOOL_UPDATED", Entity: "tool", EntityID: &id, Details: changes})
	})
	return tool, err
}

// RetireTool stops new requests for every level of the tool; retiring is one-way and
// repeating it returns the current state.
func (s *Service) RetireTool(ctx context.Context, actor auth.Principal, id uuid.UUID, version int) (Tool, error) {
	if err := canEdit(actor); err != nil {
		return Tool{}, err
	}
	var tool Tool
	err := platform.InTx(ctx, s.pool, func(tx pgx.Tx, _ time.Time) error {
		cur, err := lockTool(ctx, tx, id)
		if err != nil || cur.Status == "RETIRED" {
			tool = cur
			return err
		}
		if cur.Version != version {
			return errVersionConflict
		}
		if tool, err = scanTool(tx.QueryRow(ctx, "UPDATE tools SET status = 'RETIRED', version = version + 1 WHERE id = $1 RETURNING "+toolColumns, id)); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{ActorID: actorRef(actor), Action: "TOOL_RETIRED", Entity: "tool", EntityID: &id,
			Details: map[string]any{"name": cur.Name}})
	})
	return tool, err
}

// notifyPolicy tells InfoSec that a level's approval steps were set or changed (F2, O2).
func notifyPolicy(ctx context.Context, tx pgx.Tx, tool Tool, level Level, from []string) error {
	return notify.ToRole(ctx, tx, "INFOSEC", "APPROVAL_POLICY_CHANGED", "access_level", level.ID, map[string]any{
		"tool": tool.Name, "level": level.Name, "steps": map[string]any{"from": from, "to": level.Steps}})
}

// CreateLevel adds an access level with its approval steps and notifies InfoSec.
func (s *Service) CreateLevel(ctx context.Context, actor auth.Principal, toolID uuid.UUID, in NewLevel) (Level, error) {
	var level Level
	err := platform.InTx(ctx, s.pool, func(tx pgx.Tx, now time.Time) (err error) {
		level, err = CreateLevelTx(ctx, tx, now, actor, toolID, in, true)
		return err
	})
	return level, err
}

// CreateLevelTx is CreateLevel in the caller's transaction. A bulk import passes notify
// false and sends InfoSec one notification for all its levels (F2, M17); each level is
// still audited.
func CreateLevelTx(ctx context.Context, tx pgx.Tx, now time.Time, actor auth.Principal, toolID uuid.UUID, in NewLevel, notify bool) (Level, error) {
	if err := canEdit(actor); err != nil {
		return Level{}, err
	}
	name, err := cleanName(in.Name)
	if err != nil {
		return Level{}, err
	}
	tool, err := lockTool(ctx, tx, toolID)
	if err != nil {
		return Level{}, err
	}
	if tool.Status == "RETIRED" {
		return Level{}, errToolRetired
	}
	level, err := scanLevel(tx.QueryRow(ctx, `INSERT INTO access_levels (tool_id, name, requires_tool_owner, requires_infosec, created_by, created_at)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING `+levelColumns, toolID, name, in.RequiresToolOwner, in.RequiresInfosec, actorRef(actor), now))
	if isUnique(err) {
		return Level{}, errLevelNameTaken
	}
	if err != nil {
		return Level{}, err
	}
	if err := audit.Record(ctx, tx, audit.Event{ActorID: actorRef(actor), Action: "ACCESS_LEVEL_CREATED", Entity: "access_level",
		EntityID: &level.ID, Details: map[string]any{"tool": tool.Name, "level": name, "steps": level.Steps}}); err != nil {
		return Level{}, err
	}
	if !notify {
		return level, nil
	}
	return level, notifyPolicy(ctx, tx, tool, level, nil)
}

// UpdateLevel renames a level or changes its steps; a step change notifies InfoSec.
func (s *Service) UpdateLevel(ctx context.Context, actor auth.Principal, toolID, id uuid.UUID, in LevelUpdate) (Level, error) {
	if err := canEdit(actor); err != nil {
		return Level{}, err
	}
	name, err := cleanName(in.Name)
	if err != nil {
		return Level{}, err
	}
	var level Level
	err = platform.InTx(ctx, s.pool, func(tx pgx.Tx, now time.Time) error {
		tool, err := lockTool(ctx, tx, toolID)
		if err != nil {
			return err
		}
		cur, err := getLevel(ctx, tx, toolID, id)
		if err != nil {
			return err
		}
		switch {
		case tool.Status == "RETIRED":
			return errToolRetired
		case cur.Status == "RETIRED":
			return errLevelRetired
		case cur.Version != in.Version:
			return errVersionConflict
		}
		changes := map[string]any{}
		if name != cur.Name {
			changes["name"] = map[string]any{"from": cur.Name, "to": name}
		}
		stepsChanged := in.RequiresToolOwner != cur.RequiresToolOwner || in.RequiresInfosec != cur.RequiresInfosec
		if stepsChanged {
			changes["steps"] = map[string]any{"from": cur.Steps, "to": Steps(in.RequiresToolOwner, in.RequiresInfosec)}
		}
		if len(changes) == 0 {
			level = cur
			return nil
		}
		level, err = scanLevel(tx.QueryRow(ctx, `UPDATE access_levels SET name = $2, requires_tool_owner = $3, requires_infosec = $4,
			version = version + 1 WHERE id = $1 RETURNING `+levelColumns, id, name, in.RequiresToolOwner, in.RequiresInfosec))
		if isUnique(err) {
			return errLevelNameTaken
		}
		if err != nil {
			return err
		}
		changes["tool"] = tool.Name
		if err := audit.Record(ctx, tx, audit.Event{ActorID: actorRef(actor), Action: "ACCESS_LEVEL_UPDATED", Entity: "access_level",
			EntityID: &id, Details: changes}); err != nil {
			return err
		}
		if !stepsChanged {
			return nil
		}
		// F2: a step added after approval; the next access review decides these open grants
		// individually (the statuses are access.openStatuses).
		if in.RequiresToolOwner && !cur.RequiresToolOwner || in.RequiresInfosec && !cur.RequiresInfosec {
			if _, err := tx.Exec(ctx, `UPDATE grants SET earlier_policy_at = $2 WHERE access_level_id = $1 AND earlier_policy_at IS NULL
				AND status IN ('PROVISIONING', 'PENDING_FULFILLMENT', 'ACTIVE', 'FAILED', 'SUSPENDED')`, id, now); err != nil {
				return err
			}
		}
		return notifyPolicy(ctx, tx, tool, level, cur.Steps)
	})
	return level, err
}

// RetireLevel stops new requests for the level; one-way, and repeating it returns the
// current state.
func (s *Service) RetireLevel(ctx context.Context, actor auth.Principal, toolID, id uuid.UUID, version int) (Level, error) {
	if err := canEdit(actor); err != nil {
		return Level{}, err
	}
	var level Level
	err := platform.InTx(ctx, s.pool, func(tx pgx.Tx, _ time.Time) error {
		tool, err := lockTool(ctx, tx, toolID)
		if err != nil {
			return err
		}
		cur, err := getLevel(ctx, tx, toolID, id)
		if err != nil || cur.Status == "RETIRED" {
			level = cur
			return err
		}
		if cur.Version != version {
			return errVersionConflict
		}
		if level, err = scanLevel(tx.QueryRow(ctx, "UPDATE access_levels SET status = 'RETIRED', version = version + 1 WHERE id = $1 RETURNING "+levelColumns, id)); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{ActorID: actorRef(actor), Action: "ACCESS_LEVEL_RETIRED", Entity: "access_level",
			EntityID: &id, Details: map[string]any{"tool": tool.Name, "level": cur.Name}})
	})
	return level, err
}
