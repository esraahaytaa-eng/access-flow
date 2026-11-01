package requests

import (
	"context"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/esraahaytaa-eng/access-flow/internal/access"
	"github.com/esraahaytaa-eng/access-flow/internal/audit"
	"github.com/esraahaytaa-eng/access-flow/internal/auth"
	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

// DirectGrantInput is a Super Admin's direct grant of one level (D1).
type DirectGrantInput struct {
	SubmissionID  uuid.UUID
	BeneficiaryID uuid.UUID
	AccessLevelID uuid.UUID
	Justification string
	ExpiresOn     *string
	Account       string // the tool-specific account, when the tool has an account kind
}

// DirectGrant creates a DIRECT_GRANT request whose one item is approved at once, with no
// approval steps, and its grant through the tool's own method (D1). It is temporary (at
// most 90 days), never for the Super Admin's own access, and passes Submit's checks. The
// bool is false when the submission ID was already used by this Super Admin (A9).
func (s *Service) DirectGrant(ctx context.Context, actor auth.Principal, in DirectGrantInput) (Request, bool, error) {
	if !actor.HasRole("SUPER_ADMIN") {
		return Request{}, false, platform.ErrForbidden
	}
	if in.BeneficiaryID == actor.EmployeeID {
		return Request{}, false, errSeparation
	}
	justification, err := checkText(in.Justification, "justification", "justification_required")
	if err != nil {
		return Request{}, false, err
	}
	acc, err := s.resolveAccount(ctx, in.AccessLevelID, in.Account)
	if err != nil {
		return Request{}, false, err
	}
	var req Request
	created := false
	err = platform.InTx(ctx, s.pool, func(tx pgx.Tx, now time.Time) error {
		// As Submit: the beneficiary's lock first, then read.
		tag, err := tx.Exec(ctx, "SELECT 1 FROM employees WHERE id = $1 FOR UPDATE", in.BeneficiaryID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return platform.ValidationError("beneficiaryId", "beneficiary_not_found")
		}
		if prev, err := existing(ctx, tx, actor, in.SubmissionID, "DIRECT_GRANT"); err != nil || prev != nil {
			if prev != nil {
				req = *prev
			}
			return err
		}
		var status string
		var manager *uuid.UUID
		if err := tx.QueryRow(ctx, "SELECT status, manager_id FROM employees WHERE id = $1", in.BeneficiaryID).Scan(&status, &manager); err != nil {
			return err
		}
		if status != "ACTIVE" {
			return platform.ValidationError("beneficiaryId", "beneficiary_not_active")
		}
		c, err := checkLevel(ctx, tx, in.BeneficiaryID, in.AccessLevelID, nil)
		if err != nil {
			return err
		}
		expires, err := DirectExpiry(in.ExpiresOn, now, s.loc)
		if err != nil {
			return err
		}

		var requestID, itemID uuid.UUID
		if err := tx.QueryRow(ctx, `INSERT INTO requests (submission_id, kind, beneficiary_id, submitted_by, created_at)
			VALUES ($1, 'DIRECT_GRANT', $2, $3, $4) RETURNING id`, in.SubmissionID, in.BeneficiaryID, actor.EmployeeID, now).Scan(&requestID); err != nil {
			return err
		}
		account, ref := accountColumns(acc)
		if err := tx.QueryRow(ctx, `INSERT INTO request_items (request_id, beneficiary_id, tool_id, access_level_id, justification,
			duration_type, expires_on, status, closed_at, created_at, account, account_ref)
			VALUES ($1, $2, $3, $4, $5, 'TEMPORARY', $6, 'APPROVED', $7, $7, $8, $9) RETURNING id`,
			requestID, in.BeneficiaryID, c.toolID, in.AccessLevelID, justification, expires, now, account, ref).Scan(&itemID); err != nil {
			return err
		}
		details := accountDetails(map[string]any{"requestId": requestID, "itemId": itemID, "tool": c.tool, "level": c.level,
			"expiresOn": expires.Format(time.DateOnly), "submissionId": in.SubmissionID}, acc)
		if err := audit.Record(ctx, tx, audit.Event{ActorID: &actor.EmployeeID, Action: "DIRECT_GRANT_CREATED", Entity: "request_item",
			EntityID: &itemID, SubjectID: &in.BeneficiaryID, Reason: justification, Details: details}); err != nil {
			return err
		}
		if _, err := access.CreateGrant(ctx, tx, now, actor.EmployeeID, itemID); err != nil {
			return err
		}
		if err := notifyDirectGrant(ctx, tx, itemID, c, manager, details); err != nil {
			return err
		}
		reqs, err := loadRequests(ctx, tx, "r.id = $1", requestID)
		if err != nil {
			return err
		}
		req, created = reqs[0], true
		return nil
	})
	if isUnique(err, "requests_submission_id_key") {
		return s.repeated(ctx, actor, in.SubmissionID, "DIRECT_GRANT")
	}
	return req, created, err
}

// notifyDirectGrant tells InfoSec, the beneficiary's manager and, when the level requires
// a Tool Owner step, the tool owner: the approvers the direct grant bypassed (D1). Each
// person is told once, even when bypassed in more than one way.
func notifyDirectGrant(ctx context.Context, tx pgx.Tx, itemID uuid.UUID, c levelCheck, manager *uuid.UUID, details map[string]any) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO notifications (recipient_id, kind, entity, entity_id, details)
		SELECT e.id, 'DIRECT_GRANT', 'request_item', $1, $2 FROM employees e
		WHERE (e.status = 'ACTIVE' AND EXISTS (SELECT 1 FROM role_assignments r WHERE r.employee_id = e.id AND r.role = 'INFOSEC' AND r.valid_to IS NULL))
		   OR e.id = $3
		   OR ($4 AND e.id = (SELECT owner_id FROM tools WHERE id = $5))`,
		itemID, details, manager, slices.Contains(c.steps, "TOOL_OWNER"), c.toolID)
	return err
}
