package requests

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/esraahaytaa-eng/access-flow/internal/access"
	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

// AccountLookup finds an account of kind (GITHUB_LOGIN) in its tool, as the tool spells it,
// or fails with access.ErrAccountInvalid, ErrAccountNotFound or ErrAccountNotAllowed.
type AccountLookup func(ctx context.Context, kind, name string) (access.ExternalAccount, error)

// GitHub could not answer; the field lets the form show it next to the username.
var (
	errAccountUnavailable = &platform.Error{Status: http.StatusServiceUnavailable, Code: "account_check_unavailable", Field: "account"}
	errAccountRateLimited = &platform.Error{Status: http.StatusTooManyRequests, Code: "account_check_rate_limited", Field: "account"}
)

// resolveAccount checks an item's account against its tool's account kind and looks it up
// (A1 note 2026-11-01). It runs before the transaction, so no network call waits under a
// lock; reading the kind unlocked is safe because it never changes (migration 00020). nil
// for a tool that uses the employee's email.
func (s *Service) resolveAccount(ctx context.Context, levelID uuid.UUID, name string) (*access.ExternalAccount, error) {
	var kind string
	err := s.pool.QueryRow(ctx, `SELECT coalesce(t.account_kind, '') FROM access_levels l JOIN tools t ON t.id = l.tool_id
		WHERE l.id = $1`, levelID).Scan(&kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil // checkLevel reports the unknown level
	}
	if err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	switch {
	case kind == "" && name == "":
		return nil, nil
	case kind == "":
		return nil, platform.ValidationError("account", "account_not_allowed")
	case name == "":
		return nil, platform.ValidationError("account", "account_required")
	case s.Accounts == nil: // no adapter configured for this kind
		return nil, errAccountUnavailable
	}
	acc, err := s.Accounts(ctx, kind, name)
	switch {
	case errors.Is(err, access.ErrAccountInvalid):
		return nil, platform.ValidationError("account", "account_invalid")
	case errors.Is(err, access.ErrAccountNotFound):
		return nil, platform.ValidationError("account", "account_not_found")
	case errors.Is(err, access.ErrAccountNotAllowed):
		return nil, platform.ValidationError("account", "account_not_allowed")
	case errors.Is(err, access.ErrRateLimited):
		slog.WarnContext(ctx, "account lookup rate limited", "kind", kind)
		return nil, errAccountRateLimited
	case err != nil:
		slog.WarnContext(ctx, "account lookup failed", "kind", kind, "error", err)
		return nil, errAccountUnavailable
	}
	return &acc, nil
}

// accountDetails adds an item's external account to its audit details.
func accountDetails(d map[string]any, acc *access.ExternalAccount) map[string]any {
	if acc != nil {
		d["account"], d["accountRef"] = acc.Name, acc.Ref
	}
	return d
}

// accountColumns returns an account's name and reference for an INSERT, or two NULLs.
func accountColumns(acc *access.ExternalAccount) (name, ref *string) {
	if acc == nil {
		return nil, nil
	}
	return &acc.Name, &acc.Ref
}
