package requests

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/esraahaytaa-eng/access-flow/internal/access"
	"github.com/esraahaytaa-eng/access-flow/internal/auth"
	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

var (
	errGrantNotActive = platform.NewError(http.StatusConflict, "grant_not_active")
	errChanging       = platform.NewError(http.StatusConflict, "change_in_progress")
	errNotActive      = platform.NewError(http.StatusConflict, "beneficiary_not_active") // the body names no beneficiary
)

// ChangeInput asks for a held grant's level with a new duration (an EXTENSION, D6), or for
// another level of its tool (a LEVEL_CHANGE, D4).
type ChangeInput struct {
	SubmissionID  uuid.UUID
	AccessLevelID uuid.UUID
	Justification string
	Duration      Duration
}

// Change requests an extension or a level change of an ACTIVE grant that is not yet due, by
// its employee or, on their behalf, whoever may submit a new request for them (checkOnBehalf:
// the management chain, an HR Manager or a Super Admin; ENG-006); anyone else gets 404, as
// with revoke. The item is approved under the requested level's current steps and the
// grant stays as it is until final approval. The bool is false when the submission ID was
// already used by this submitter (A9).
func (s *Service) Change(ctx context.Context, actor auth.Principal, grantID uuid.UUID, in ChangeInput) (Request, bool, error) {
	justification, err := checkText(in.Justification, "justification", "justification_required")
	if err != nil {
		return Request{}, false, err
	}
	var req Request
	created := false
	kind := "LEVEL_CHANGE"
	err = platform.InTx(ctx, s.pool, func(tx pgx.Tx, now time.Time) error {
		created, kind = false, "LEVEL_CHANGE" // fn may run again (platform.InTx)
		var employee, heldLevel uuid.UUID     // neither ever changes
		// An orphan account has no employee to change it for.
		err := tx.QueryRow(ctx, "SELECT employee_id, access_level_id FROM grants WHERE id = $1 AND employee_id IS NOT NULL",
			grantID).Scan(&employee, &heldLevel)
		if errors.Is(err, pgx.ErrNoRows) {
			return platform.ErrNotFound
		}
		if err != nil {
			return err
		}
		if in.AccessLevelID == heldLevel {
			kind = "EXTENSION"
		}
		// As Submit: the beneficiary's lock first, then read.
		if _, err := tx.Exec(ctx, "SELECT 1 FROM employees WHERE id = $1 FOR UPDATE", employee); err != nil {
			return err
		}
		var status string
		var manager *uuid.UUID
		var tool uuid.UUID
		var expires *time.Time
		var live, changing bool
		if err := tx.QueryRow(ctx, `SELECT e.status, e.manager_id, g.tool_id, g.expires_on,
			       g.status = 'ACTIVE' AND (g.expires_on IS NULL OR g.expires_on >= $2),
			       EXISTS (SELECT 1 FROM grants n WHERE n.replaces_grant_id = g.id AND n.status NOT IN ('REVOKED', 'CANCELLED'))
			FROM grants g JOIN employees e ON e.id = g.employee_id WHERE g.id = $1`, grantID, access.Today(now, s.loc)).
			Scan(&status, &manager, &tool, &expires, &live, &changing); err != nil {
			return err
		}
		if err := checkOnBehalf(ctx, tx, actor, employee); errors.Is(err, errBeneficiaryNotAllowed) {
			return platform.ErrNotFound // nobody else may see it
		} else if err != nil {
			return err
		}
		if prev, err := existing(ctx, tx, actor, in.SubmissionID, kind); err != nil || prev != nil {
			if prev != nil {
				req = *prev
			}
			return err
		}
		switch {
		case status != "ACTIVE":
			return errNotActive
		case !live:
			return errGrantNotActive
		case changing: // its level change was approved: the new grant is on its way
			return errChanging
		}
		c, err := checkLevel(ctx, tx, employee, in.AccessLevelID, &grantID)
		if err != nil {
			return err
		}
		if c.toolID != tool {
			return platform.ValidationError("accessLevelId", "level_of_another_tool")
		}
		newExpires, err := ParseDuration(in.Duration, now, s.loc)
		if err != nil {
			return err
		}
		if kind == "EXTENSION" && (expires == nil || (newExpires != nil && !newExpires.After(*expires))) {
			return platform.ValidationError("duration", "not_an_extension") // a later date, or Until Revoked
		}
		item := newItem{c, ItemInput{in.AccessLevelID, justification, in.Duration}, newExpires, &grantID}
		if req, err = s.create(ctx, tx, now, actor, kind, in.SubmissionID, employee, manager, []newItem{item}); err != nil {
			return err
		}
		created = true
		return nil
	})
	if isUnique(err, "requests_submission_id_key") {
		return s.repeated(ctx, actor, in.SubmissionID, kind)
	}
	return req, created, err
}

func (s *Service) handleChange(w http.ResponseWriter, r *http.Request) {
	ids, err := platform.PathIDs(r, "id")
	var in struct {
		SubmissionID  *uuid.UUID
		AccessLevelID *uuid.UUID
		Justification *string
		Duration      *Duration
	}
	if err == nil {
		err = platform.DecodeRequired(w, r, &in, func() []platform.Field {
			return []platform.Field{{Name: "submissionId", Present: in.SubmissionID != nil},
				{Name: "accessLevelId", Present: in.AccessLevelID != nil}, {Name: "justification", Present: in.Justification != nil},
				{Name: "duration", Present: in.Duration != nil}}
		})
	}
	var req Request
	created := false
	if err == nil {
		req, created, err = s.Change(r.Context(), actor(r), ids[0], ChangeInput{*in.SubmissionID, *in.AccessLevelID, *in.Justification, *in.Duration})
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	platform.Respond(w, r, status, req, err)
}
