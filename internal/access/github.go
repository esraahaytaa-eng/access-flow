package access

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// GitHubToolClient gives Read on one private sandbox repository through the GitHub REST API,
// for the one tool GITHUB_TOOL_ID names (A1 note 2026-11-01). The account is the GitHub login
// and id the request recorded on the grant. Its ledger (github_access) records what
// AccessFlow created: GitHub is changed only for access recorded there, and anything the
// adapter cannot prove is a rejection that waits in Exceptions for a person. GitHub has no
// suspended state: a SUSPEND removes the access, a RESUME invites again (D7).
type GitHubToolClient struct {
	pool              *pgxpool.Pool
	base, repo, token string
	http              *http.Client
}

// ErrInvitationPending reports that GitHub sent the invitation and the person has not
// accepted it yet: the runner checks again later and counts no failed attempt.
var ErrInvitationPending = errors.New("github: the invitation is not accepted yet")

const (
	githubAPIVersion   = "2022-11-28"
	invitationsPerPage = 100
	maxInvitationPages = 10
	// Apply and the Account lookup after it stay inside the runner's 30-second lease.
	githubApplyDeadline   = 15 * time.Second
	githubAccountDeadline = 10 * time.Second
	noLedgerRecord        = "AccessFlow has no record of granting this GitHub access; review it by hand"
)

// githubLogin is GitHub's username rule: letters, digits and single inner hyphens, at most 39.
var githubLogin = regexp.MustCompile(`^[A-Za-z0-9](-?[A-Za-z0-9]){0,38}$`)

// rejection is an answer retrying cannot change: the runner fails the operation at once (O3).
type rejection struct{ msg string }

func (r rejection) Error() string { return r.msg }

// NewGitHubToolClient returns the adapter for repository (owner/name) at baseURL
// (https://api.github.com in production).
func NewGitHubToolClient(pool *pgxpool.Pool, baseURL, repository, token string, timeout time.Duration) *GitHubToolClient {
	return &GitHubToolClient{pool: pool, base: strings.TrimRight(baseURL, "/"), repo: repository, token: token,
		http: &http.Client{Timeout: timeout}}
}

// LookupUser finds the GitHub user a request names, for requests.Service.Accounts: GitHub's
// spelling of the login and the immutable id. An organization or bot is not a person's
// account; a malformed name is refused without a call.
func (c *GitHubToolClient) LookupUser(ctx context.Context, kind, login string) (ExternalAccount, error) {
	if kind != "GITHUB_LOGIN" {
		return ExternalAccount{}, fmt.Errorf("github: no lookup for account kind %q", kind)
	}
	if !githubLogin.MatchString(login) {
		return ExternalAccount{}, ErrAccountInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, githubAccountDeadline)
	defer cancel()
	var got struct {
		Login string `json:"login"`
		ID    int64  `json:"id"`
		Type  string `json:"type"`
	}
	status, err := c.send(ctx, http.MethodGet, "/users/"+login, nil, &got, http.StatusOK, http.StatusNotFound)
	switch {
	case err != nil:
		return ExternalAccount{}, err
	case status == http.StatusNotFound:
		return ExternalAccount{}, ErrAccountNotFound
	case got.Type != "User":
		return ExternalAccount{}, ErrAccountNotAllowed
	case got.ID == 0 || !strings.EqualFold(got.Login, login):
		return ExternalAccount{}, fmt.Errorf("github: the user answer for %s is incomplete", login)
	}
	return ExternalAccount{Name: got.Login, Ref: strconv.FormatInt(got.ID, 10)}, nil
}

// sameAccount confirms that GitHub user id still has the login AccessFlow recorded. Logins
// can be renamed and a freed one claimed by someone else, so a renamed or deleted account is
// never acted on by its old name, and its removal never reported done: a person decides.
func (c *GitHubToolClient) sameAccount(ctx context.Context, id int64, login string) error {
	var got struct {
		Login string `json:"login"`
	}
	status, err := c.send(ctx, http.MethodGet, "/user/"+strconv.FormatInt(id, 10), nil, &got, http.StatusOK, http.StatusNotFound)
	switch {
	case err != nil:
		return err
	case status == http.StatusNotFound:
		return rejection{fmt.Sprintf("GitHub user %d (%s) no longer exists; check the repository's access by hand", id, login)}
	case !strings.EqualFold(got.Login, login):
		return rejection{fmt.Sprintf("GitHub user %d was renamed from %s to %s; AccessFlow did not change its access: review it by hand",
			id, login, got.Login)}
	}
	return nil
}

// CheckRepository confirms at startup that the token reaches the repository and that it is
// private, so a wrong setting stops the application instead of every operation.
func (c *GitHubToolClient) CheckRepository(ctx context.Context) error {
	var got struct {
		FullName string `json:"full_name"`
		Private  bool   `json:"private"`
	}
	if _, err := c.send(ctx, http.MethodGet, c.repoPath(""), nil, &got, http.StatusOK); err != nil {
		return fmt.Errorf("GITHUB_REPOSITORY %s: %w", c.repo, err)
	}
	if !strings.EqualFold(got.FullName, c.repo) {
		return fmt.Errorf("GITHUB_REPOSITORY %s: GitHub answered for %q", c.repo, got.FullName)
	}
	if !got.Private {
		return fmt.Errorf("GITHUB_REPOSITORY %s is public; the adapter is for a private sandbox", c.repo)
	}
	return nil
}

// CheckTool confirms at startup that GITHUB_TOOL_ID names an API tool whose accounts are
// GitHub usernames: the runner handles only API operations, and only such a tool's requests
// record the login and id the adapter acts on.
func (c *GitHubToolClient) CheckTool(ctx context.Context, tool uuid.UUID) error {
	var method, kind string
	err := c.pool.QueryRow(ctx, "SELECT fulfillment_method, coalesce(account_kind, '') FROM tools WHERE id = $1", tool).Scan(&method, &kind)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("GITHUB_TOOL_ID %s names no tool; create the tool first, then restart", tool)
	case err == nil && method != "API":
		return fmt.Errorf("GITHUB_TOOL_ID %s is a %s tool; it must be an API tool", tool, method)
	case err == nil && kind != "GITHUB_LOGIN":
		return fmt.Errorf("GITHUB_TOOL_ID %s does not use GitHub usernames; create the tool with that account identity", tool)
	}
	return err
}

// ledger is AccessFlow's record of one grant's access in the repository.
type ledger struct {
	login, state string
	user         int64
	level        uuid.UUID
	invitation   *int64
	overdue      bool // invited more than 7 days ago: GitHub's own invitation lifetime
}

const ledgerColumns = "account, user_id, state, level_id, invitation_id, coalesce(invited_at < now() - interval '7 days', false)"

func scanLedger(row pgx.Row) (*ledger, error) {
	var l ledger
	err := row.Scan(&l.login, &l.user, &l.state, &l.level, &l.invitation, &l.overdue)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &l, err
}

// move changes the grant's ledger state from what this call read, never over a newer change.
func (c *GitHubToolClient) move(ctx context.Context, grant uuid.UUID, from, to string, invitation *int64) error {
	tag, err := c.pool.Exec(ctx, `UPDATE github_access SET state = $3, invitation_id = coalesce($4::bigint, invitation_id),
		invited_at = CASE WHEN $4::bigint IS NULL THEN invited_at ELSE now() END, updated_at = now()
		WHERE grant_id = $1 AND state = $2`, grant, from, to, invitation)
	if err == nil && tag.RowsAffected() != 1 {
		err = fmt.Errorf("github ledger: the grant's record is no longer %s", from)
	}
	return err
}

func (c *GitHubToolClient) Apply(ctx context.Context, op ToolOperation) (ToolResult, error) {
	ctx, cancel := context.WithTimeout(ctx, githubApplyDeadline)
	defer cancel()
	var grant uuid.UUID
	var ref *string
	if err := c.pool.QueryRow(ctx, "SELECT o.grant_id, g.account_ref FROM operations o JOIN grants g ON g.id = o.grant_id WHERE o.id = $1",
		op.ID).Scan(&grant, &ref); err != nil {
		return ToolResult{}, err
	}
	if ref == nil {
		return ToolResult{}, rejection{"the grant names no GitHub account; request the access with a GitHub username"}
	}
	user, err := strconv.ParseInt(*ref, 10, 64)
	if err != nil {
		return ToolResult{}, rejection{"the grant's GitHub user id is not a number"}
	}
	row, err := scanLedger(c.pool.QueryRow(ctx, "SELECT "+ledgerColumns+" FROM github_access WHERE grant_id = $1", grant))
	if err != nil {
		return ToolResult{}, err
	}
	if err := c.sameAccount(ctx, user, op.Account); err != nil {
		return ToolResult{}, err
	}
	switch op.Type {
	case "GRANT", "RESUME":
		return c.give(ctx, op, grant, user, row)
	case "REVOKE", "SUSPEND":
		return c.take(ctx, op, grant, row)
	}
	return ToolResult{}, rejection{"the GitHub adapter does not support " + op.Type}
}

// give invites the person with Read and reports ErrInvitationPending until GitHub shows the
// invitation accepted. Access that AccessFlow did not grant is never taken over.
func (c *GitHubToolClient) give(ctx context.Context, op ToolOperation, grant uuid.UUID, user int64, row *ledger) (ToolResult, error) {
	back := "REMOVED" // where a failed invitation leaves the record
	if op.Type == "RESUME" {
		back = "SUSPENDED"
	}
	switch {
	case row == nil && op.Type == "GRANT":
		login := op.Account
		if err := c.requireNoAccess(ctx, login); err != nil {
			return ToolResult{}, err
		}
		// Recorded before the call, so a crash after it leaves a record to find it by.
		level, err := uuid.Parse(op.Level)
		if err != nil {
			return ToolResult{}, err
		}
		_, err = c.pool.Exec(ctx, `INSERT INTO github_access (grant_id, repository, account, user_id, level_id, state)
			VALUES ($1, $2, $3, $4, $5, 'INVITING')`, grant, c.repo, login, user, level)
		if err != nil {
			return ToolResult{}, managedElsewhere(err)
		}
		return c.invite(ctx, grant, login)
	case row == nil:
		return ToolResult{}, rejection{noLedgerRecord}
	case row.state == "REMOVED" && op.Type == "GRANT": // a manual retry after a declined or expired invitation
		if err := c.requireNoAccess(ctx, row.login); err != nil {
			return ToolResult{}, err
		}
		if err := c.move(ctx, grant, "REMOVED", "INVITING", nil); err != nil {
			return ToolResult{}, managedElsewhere(err)
		}
		return c.invite(ctx, grant, row.login)
	case row.state == "SUSPENDED" && op.Type == "RESUME":
		if err := c.requireNoAccess(ctx, row.login); err != nil {
			return ToolResult{}, err
		}
		if err := c.move(ctx, grant, "SUSPENDED", "INVITING", nil); err != nil {
			return ToolResult{}, err
		}
		return c.invite(ctx, grant, row.login)
	case row.state == "INVITING": // an earlier attempt may have sent the invitation
		inv, err := c.findInvitation(ctx, func(i invitation) bool { return i.login() == strings.ToLower(row.login) })
		if err != nil {
			return ToolResult{}, err
		}
		if inv != nil && inv.Permissions == "read" {
			if err := c.move(ctx, grant, "INVITING", "INVITED", &inv.ID); err != nil {
				return ToolResult{}, err
			}
			return ToolResult{}, ErrInvitationPending
		}
		return c.settleInvitation(ctx, grant, row.login, "INVITING", back, true)
	case row.state == "INVITED":
		inv, err := c.findInvitation(ctx, func(i invitation) bool { return i.ID == *row.invitation })
		if err != nil {
			return ToolResult{}, err
		}
		if inv == nil {
			return c.settleInvitation(ctx, grant, row.login, "INVITED", back, false)
		}
		if !inv.Expired && !row.overdue {
			return ToolResult{}, ErrInvitationPending
		}
		if err := c.deleteInvitation(ctx, inv.ID); err != nil {
			return ToolResult{}, err
		}
		if err := c.move(ctx, grant, "INVITED", back, nil); err != nil {
			return ToolResult{}, err
		}
		return ToolResult{}, rejection{"the GitHub invitation was not accepted within 7 days"}
	case row.state == "ACTIVE":
		return ToolResult{Result: "NO_CHANGE"}, nil
	}
	return ToolResult{}, rejection{fmt.Sprintf("AccessFlow's GitHub record is %s; it cannot %s", row.state, op.Type)}
}

// settleInvitation reads the access once no invitation of AccessFlow's is pending: Read means
// it was accepted (ACTIVE); none means it was declined or never sent.
func (c *GitHubToolClient) settleInvitation(ctx context.Context, grant uuid.UUID, login, from, back string,
	sendIfNone bool) (ToolResult, error) {
	role, err := c.role(ctx, login)
	if err != nil {
		return ToolResult{}, err
	}
	switch role {
	case "read":
		return ToolResult{Result: "APPLIED"}, c.move(ctx, grant, from, "ACTIVE", nil)
	case "":
		if sendIfNone {
			return c.invite(ctx, grant, login)
		}
		if err := c.move(ctx, grant, from, back, nil); err != nil {
			return ToolResult{}, err
		}
		return ToolResult{}, rejection{"the GitHub invitation was declined or cancelled"}
	}
	return ToolResult{}, rejection{notGranted(role)}
}

// invite asks GitHub for Read (pull). 201 is an invitation; 204 adds an org member at once.
func (c *GitHubToolClient) invite(ctx context.Context, grant uuid.UUID, login string) (ToolResult, error) {
	var created struct {
		ID int64 `json:"id"`
	}
	status, err := c.send(ctx, http.MethodPut, c.repoPath("/collaborators/"+url.PathEscape(login)), map[string]string{"permission": "pull"},
		&created, http.StatusCreated, http.StatusNoContent)
	if err != nil {
		return ToolResult{}, err
	}
	if status == http.StatusNoContent {
		return ToolResult{Result: "APPLIED"}, c.move(ctx, grant, "INVITING", "ACTIVE", nil)
	}
	if created.ID == 0 {
		return ToolResult{}, errors.New("github: the invitation has no id")
	}
	if err := c.move(ctx, grant, "INVITING", "INVITED", &created.ID); err != nil {
		return ToolResult{}, err
	}
	return ToolResult{}, ErrInvitationPending
}

// take removes access AccessFlow recorded granting: its pending invitation, then the Read
// collaborator. A permission changed by hand, or access it has no record of, is a rejection.
func (c *GitHubToolClient) take(ctx context.Context, op ToolOperation, grant uuid.UUID, row *ledger) (ToolResult, error) {
	to := "REMOVED"
	if op.Type == "SUSPEND" {
		to = "SUSPENDED"
	}
	switch {
	case row == nil:
		return ToolResult{}, rejection{noLedgerRecord}
	case row.state == "REMOVED" || row.state == to:
		return ToolResult{Result: "NO_CHANGE"}, nil
	case row.state == "INVITED":
		if err := c.deleteInvitation(ctx, *row.invitation); err != nil {
			return ToolResult{}, err
		}
	case row.state == "INVITING":
		inv, err := c.findInvitation(ctx, func(i invitation) bool {
			return i.login() == strings.ToLower(row.login) && i.Permissions == "read"
		})
		if err != nil {
			return ToolResult{}, err
		}
		if inv != nil {
			if err := c.deleteInvitation(ctx, inv.ID); err != nil {
				return ToolResult{}, err
			}
		}
	}
	// After the invitation is gone, Read can only be AccessFlow's (accepted just before);
	// once SUSPENDED, AccessFlow holds nothing, so any access is someone else's.
	role, err := c.role(ctx, row.login)
	if err != nil {
		return ToolResult{}, err
	}
	switch {
	case role == "read" && row.state != "SUSPENDED":
		if _, err := c.send(ctx, http.MethodDelete, c.repoPath("/collaborators/"+url.PathEscape(row.login)), nil, nil, http.StatusNoContent); err != nil {
			return ToolResult{}, err
		}
	case role != "" && row.state == "ACTIVE":
		return ToolResult{}, rejection{fmt.Sprintf("the GitHub permission was changed outside AccessFlow (now %s); review it by hand", role)}
	case role != "":
		return ToolResult{}, rejection{notGranted(role)}
	}
	return ToolResult{Result: "APPLIED"}, c.move(ctx, grant, row.state, to, nil)
}

// Account reports the person's access as AccessFlow levels: Read it recorded as ACTIVE, or
// SUSPENDED once a suspension removed it. Any other access is not AccessFlow's to report.
func (c *GitHubToolClient) Account(ctx context.Context, tool, account string) (ToolAccount, error) {
	ctx, cancel := context.WithTimeout(ctx, githubAccountDeadline)
	defer cancel()
	// The newest record, ended or not, has the GitHub id AccessFlow acted on.
	row, err := scanLedger(c.pool.QueryRow(ctx, "SELECT "+ledgerColumns+` FROM github_access
		WHERE repository = $1 AND lower(account) = lower($2) ORDER BY state = 'REMOVED', updated_at DESC LIMIT 1`, c.repo, account))
	if err != nil {
		return ToolAccount{}, err
	}
	if row == nil {
		return ToolAccount{}, rejection{noLedgerRecord}
	}
	if err := c.sameAccount(ctx, row.user, row.login); err != nil {
		return ToolAccount{}, err
	}
	login := row.login
	role, err := c.role(ctx, login)
	if err != nil {
		return ToolAccount{}, err
	}
	switch {
	case role == "read" && row.state == "ACTIVE":
		return ToolAccount{Exists: true, Levels: []ToolLevel{{row.level.String(), "ACTIVE"}}}, nil
	case role != "":
		return ToolAccount{}, rejection{notGranted(role)}
	}
	inv, err := c.findInvitation(ctx, func(i invitation) bool { return i.login() == strings.ToLower(login) })
	if err != nil {
		return ToolAccount{}, err
	}
	if inv != nil {
		return ToolAccount{}, errors.New("github: an invitation is still pending")
	}
	if row.state == "SUSPENDED" {
		return ToolAccount{Exists: true, Levels: []ToolLevel{{row.level.String(), "SUSPENDED"}}}, nil
	}
	return ToolAccount{Exists: false, Levels: []ToolLevel{}}, nil
}

// managedElsewhere turns the ledger's one-live-record rule into a rejection.
func managedElsewhere(err error) error {
	if pgErr := (*pgconn.PgError)(nil); errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return rejection{"this GitHub account is already managed by AccessFlow under another grant"}
	}
	return err
}

func notGranted(role string) string {
	return fmt.Sprintf("the GitHub account has %s access that AccessFlow did not grant; review it by hand", role)
}

// requireNoAccess refuses an account that already has any access or pending invitation.
func (c *GitHubToolClient) requireNoAccess(ctx context.Context, login string) error {
	role, err := c.role(ctx, login)
	if err != nil {
		return err
	}
	if role != "" {
		return rejection{notGranted(role)}
	}
	inv, err := c.findInvitation(ctx, func(i invitation) bool { return i.login() == strings.ToLower(login) })
	if err != nil {
		return err
	}
	if inv != nil {
		return rejection{"the GitHub account has a pending invitation that AccessFlow did not grant; review it by hand"}
	}
	return nil
}

// role is the account's effective role on the repository (direct, team or org), "" for none.
func (c *GitHubToolClient) role(ctx context.Context, login string) (string, error) {
	var got struct {
		Permission string `json:"permission"`
		RoleName   string `json:"role_name"` // finer than permission: triage reports permission "read"
	}
	if _, err := c.send(ctx, http.MethodGet, c.repoPath("/collaborators/"+url.PathEscape(login)+"/permission"), nil, &got, http.StatusOK); err != nil {
		return "", err
	}
	role := cmp.Or(got.RoleName, got.Permission)
	if role == "" {
		return "", errors.New("github: the permission answer names no role")
	}
	if role == "none" {
		return "", nil
	}
	return role, nil
}

type invitation struct {
	ID          int64  `json:"id"`
	Permissions string `json:"permissions"`
	Expired     bool   `json:"expired"`
	Invitee     *struct {
		Login string `json:"login"`
	} `json:"invitee"`
}

func (i invitation) login() string {
	if i.Invitee == nil {
		return ""
	}
	return strings.ToLower(i.Invitee.Login)
}

// findInvitation returns the repository's first pending invitation matching match. Rather
// than guess past the last page it reads, it fails.
func (c *GitHubToolClient) findInvitation(ctx context.Context, match func(invitation) bool) (*invitation, error) {
	for page := 1; page <= maxInvitationPages; page++ {
		var list []invitation
		path := c.repoPath("/invitations?per_page=" + strconv.Itoa(invitationsPerPage) + "&page=" + strconv.Itoa(page))
		if _, err := c.send(ctx, http.MethodGet, path, nil, &list, http.StatusOK); err != nil {
			return nil, err
		}
		if i := slices.IndexFunc(list, match); i >= 0 {
			return &list[i], nil
		}
		if len(list) < invitationsPerPage {
			return nil, nil
		}
	}
	return nil, fmt.Errorf("github: more than %d pending invitations", maxInvitationPages*invitationsPerPage)
}

// deleteInvitation cancels an invitation; one already gone (accepted, declined) is fine.
func (c *GitHubToolClient) deleteInvitation(ctx context.Context, id int64) error {
	_, err := c.send(ctx, http.MethodDelete, c.repoPath("/invitations/"+strconv.FormatInt(id, 10)), nil, nil, http.StatusNoContent, http.StatusNotFound)
	return err
}

func (c *GitHubToolClient) repoPath(path string) string { return "/repos/" + c.repo + path }

// send calls the API at path and decodes a JSON answer into out. A status outside ok is an
// error: temporary (server errors, rate limits) or a rejection. The error names the status
// only: GitHub's message goes to the server log (redacted), never to stored errors or the UI.
func (c *GitHubToolClient) send(ctx context.Context, method, path string, body, out any, ok ...int) (int, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", githubAPIVersion)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, errors.New(c.redact(err.Error()))
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return 0, errors.New("github response: " + c.redact(err.Error()))
	}
	if !slices.Contains(ok, resp.StatusCode) {
		var answer struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(raw, &answer)
		slog.WarnContext(ctx, "github answer", "method", method, "status", resp.StatusCode,
			"message", c.redact(answer.Message[:min(len(answer.Message), 200)]))
		msg := fmt.Sprintf("github status %d", resp.StatusCode)
		limited := resp.Header.Get("Retry-After") != "" || resp.Header.Get("X-RateLimit-Remaining") == "0"
		switch {
		case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusForbidden && limited:
			return resp.StatusCode, fmt.Errorf("%s (%w)", msg, ErrRateLimited)
		case resp.StatusCode >= 500:
			return resp.StatusCode, errors.New(msg)
		}
		return resp.StatusCode, rejection{msg}
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp.StatusCode, fmt.Errorf("github response: %w", err)
		}
	}
	return resp.StatusCode, nil
}

func (c *GitHubToolClient) redact(s string) string {
	if c.token == "" {
		return s
	}
	return strings.ReplaceAll(s, c.token, "[redacted]")
}

// RouteTool sends the operations of one tool to github and every other tool's to other.
func RouteTool(tool uuid.UUID, github, other ToolClient) ToolClient {
	return toolRouter{tool.String(), github, other}
}

type toolRouter struct {
	tool          string
	github, other ToolClient
}

func (r toolRouter) pick(tool string) ToolClient {
	if tool == r.tool {
		return r.github
	}
	return r.other
}

func (r toolRouter) Apply(ctx context.Context, op ToolOperation) (ToolResult, error) {
	return r.pick(op.Tool).Apply(ctx, op)
}

func (r toolRouter) Account(ctx context.Context, tool, account string) (ToolAccount, error) {
	return r.pick(tool).Account(ctx, tool, account)
}
