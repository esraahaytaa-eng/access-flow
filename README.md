# AccessFlow

Access requests, approvals, granting and removal, quarterly reviews and audit proof for internal tools.
Requirements: [`CASE.md`](CASE.md). Rules and architecture: [`DECISIONS.md`](DECISIONS.md). Plan: [`docs/plans/`](docs/plans/).

> Status: identity, catalog, requests, approvals, API fulfillment, Manual / Vendor fulfillment with direct grants, and expiry, extensions and level changes (milestones M2–M8). Sign-in, sessions, password change, the initial Super Admin, employees, roles, admin groups, the tool catalog with approval steps, starter packages, in-app notifications, access requests and their approvals (routing, decisions, withdrawal, reassignment, Super Admin override, the Mock Email Outbox), API-tool provisioning through the Mock Tool Service with verification, removal, My Access, Team Access and Exceptions with manual retry, Fulfillment Tasks for Manual and Vendor tools, withdrawal of an approved item before work starts, Super Admin direct grants and revokes, automatic expiry of temporary access, extensions (including Temporary → Until Revoked) and level changes with the overlap exception work through the API. There are no screens yet (they arrive with the design).

## Run

```bash
docker compose up
```

Open http://localhost:8080. Health: `GET /api/v1/health`. No other setup is needed.

### First sign-in

On first start `app` creates the initial Super Admin (`admin@accessflow.local`, or `BOOTSTRAP_ADMIN_EMAIL`) with a one-time password. The password is never logged; read it with:

```bash
docker compose exec app /accessflow bootstrap-password
```

Sign in with it (`POST /api/v1/auth/login`) and change it (`POST /api/v1/auth/password`); until then every other API call answers `403 password_change_required`. After the change the old password stops working and the file is deleted. This password does not expire. Passwords that admins later issue to employees expire after 72 hours (DECISIONS.md F4).

### Configuration

All optional:

| Variable | Default | Meaning |
|---|---|---|
| `JWT_SECRET` | generated on first start and stored in the database | session signing secret, at least 32 bytes |
| `COOKIE_SECURE` | `false` | set `true` when served over HTTPS |
| `BOOTSTRAP_ADMIN_EMAIL` | `admin@accessflow.local` | the initial Super Admin |
| `BOOTSTRAP_FILE` | `/var/lib/accessflow/bootstrap-admin.txt` | where its one-time password is kept (the `appdata` volume) |
| `SEED_PASSWORD` | `accessflow-local-demo` (set in `compose.yaml`) | shared password of the development fixture users; local only |
| `COMPANY_TIMEZONE` | `Europe/Istanbul` | the zone request expiry dates are in; an unknown zone stops startup |
| `MOCK_TOOLS_URL` | `http://mocktools:8081` (set in `compose.yaml`) | the Mock Tool Service the operation runner calls |

### Development fixture

For trying the API by hand, create a few employees (HR Manager, two InfoSec members, an Auditor, a manager with two reports), two admin groups and four tools:

```bash
docker compose exec app /accessflow dev-fixture
```

The employees are `hr@`, `infosec1@`, `infosec2@`, `auditor@`, `manager@`, `dev1@` and `dev2@accessflow.local`, all with the `SEED_PASSWORD` value. The tools cover the three fulfillment methods:

| Tool | Method | Owner | Admin group | Levels (approval steps) |
|---|---|---|---|---|
| Jira | API | manager@ | IT Operations | Viewer (Manager) · Admin (Manager, Tool Owner, InfoSec) |
| GitHub | MANUAL | manager@ | IT Operations | Developer (Manager, Tool Owner) |
| AWS | API | infosec2@ | IT Operations | Admin (Manager, Tool Owner, InfoSec) |
| Payroll Portal | VENDOR | hr@ | Vendor Desk | User (Manager, Tool Owner) |

Jira Viewer and GitHub Developer form the Engineering starter package (dev1@ and dev2@ see them as recommended). The command never runs on its own, can be run again safely (it skips existing employees, groups and tools by name and leaves them as they are) and marks what it creates as seeded (DECISIONS.md D12).

## API

Errors are JSON: `{"error": "<code>"}`, plus `"field"` for a validation error. Signed-in unsafe requests (`POST` and others) need the `X-CSRF-Token` header set to the `accessflow_csrf` cookie. Cross-site unsafe requests, including sign-in, are rejected (`403 cross_origin_request`).

| Endpoint | |
|---|---|
| `POST /api/v1/auth/login` | `{email, password}` → session cookies and `{employee, mustChangePassword}`; one generic `401 invalid_credentials` for every failure; 5 failures in a row lock the account for 15 minutes |
| `GET /api/v1/auth/me` | the signed-in employee |
| `POST /api/v1/auth/password` | `{currentPassword, newPassword}` (12–128 characters, different from the current one); signs out the employee's other sessions |
| `POST /api/v1/auth/logout` | ends every session of the employee |
| `GET /api/v1/employees` | the directory: every employee with status, roles, groups and version for Super Admin, HR Manager, InfoSec and Auditor; otherwise active employees with id, email, name, department, job title and manager |
| `POST /api/v1/employees` | HR Manager or Super Admin: `{email, fullName, department?, jobTitle?, managerId?}` → `201 {employee, temporaryPassword}`; the password is shown once and expires after 72 hours |
| `PUT /api/v1/employees/{id}` | HR Manager or Super Admin: `{version, managerId, department, jobTitle}`, all four required (null clears; a missing one is `400 field_required`); `409 version_conflict` on a stale version; manager cycles are rejected; never your own record (`403 self_change_forbidden`), so nobody picks their own approver |
| `POST /api/v1/employees/{id}/password-reset` | HR Manager or Super Admin → `{temporaryPassword}`; accounts holding Super Admin or InfoSec need a Super Admin; never your own |
| `POST /api/v1/employees/{id}/roles` · `DELETE …/roles/{role}` | Super Admin: assign or remove `INFOSEC`, `HR_MANAGER`, `AUDITOR`; never your own |
| `GET /api/v1/admin-groups` · `POST /api/v1/admin-groups` | list (members for the directory roles); create `{name}` (Super Admin) |
| `POST /api/v1/admin-groups/{id}/members` · `DELETE …/members/{employeeId}` | Super Admin: add `{employeeId}` or remove a member; never yourself |
| `GET /api/v1/tools` | the catalog: requestable tools and levels with their approval steps, owner, admin group and `recommended` (the viewer's starter package); Super Admin, InfoSec and Auditor also see retired ones, with `status` and `version` |
| `POST /api/v1/tools` | Super Admin: `{name, fulfillmentMethod, ownerId, adminGroupId}`; the method (`API`, `MANUAL`, `VENDOR`) cannot change later; the owner must be active |
| `PUT /api/v1/tools/{id}` | Super Admin: `{name, ownerId, adminGroupId, version}`, all required |
| `POST /api/v1/tools/{id}/levels` | Super Admin: `{name, requiresToolOwner, requiresInfosec}`; Manager is always a step; notifies InfoSec |
| `PUT /api/v1/tools/{id}/levels/{levelId}` | Super Admin: `{name, requiresToolOwner, requiresInfosec, version}`, all required; a step change notifies InfoSec |
| `POST /api/v1/tools/{id}/retire` · `POST …/levels/{levelId}/retire` | Super Admin: `{version}`; one-way; retired tools and levels are no longer requestable |
| `GET /api/v1/starter-packages` · `POST` · `DELETE …/{id}` | list; add `{department, jobTitle?, accessLevelId}` or remove an entry (Super Admin). Entries recommend levels; any employee may request any requestable level |
| `GET /api/v1/notifications` · `POST …/{id}/read` | the signed-in employee's newest 100 notifications; mark one read |
| `POST /api/v1/requests` | `{submissionId, beneficiaryId?, items: [{accessLevelId, justification, duration}]}`, 1–20 items; `duration` is `{"type": "UNTIL_REVOKED"}` or `{"type": "TEMPORARY", "expiresOn": "YYYY-MM-DD"}` (after today in the company timezone). → `201 {request}`; the same `submissionId` again returns the original (`200`), only to its submitter (`409 submission_id_reused` for anyone else). One invalid item rejects the whole submission (`field` names it, e.g. `items[1].duration`); `409 open_request_exists` when the beneficiary already has an open item for the tool |
| `GET /api/v1/requests` · `GET …/{id}` | requests the signed-in employee is the beneficiary or submitter of, with items and steps; anyone else's is `404` |
| `POST /api/v1/request-items/{id}/withdraw` | beneficiary or submitter: `{version, reason}` while the item is pending approval, or once approved until work starts on its grant (its task is claimed or the runner sends it): the grant is then `CANCELLED`; afterwards `409 fulfillment_started` (give the access up with revoke) |
| `GET /api/v1/approvals` | the Approval Inbox: open steps assigned to me, or in the InfoSec pool when I am eligible; each with its item (beneficiary, tool, level, justification, duration, every step) and `openedAt` (waiting since) |
| `GET /api/v1/approvals/open` | InfoSec, Super Admin: every open step; `unroutable: true` when nobody may decide it |
| `POST /api/v1/approvals/{id}/approve` · `…/reject` | the assignee or an eligible InfoSec member: `{version}`, reject also `{reason}`. The first decision wins; a late or unauthorized attempt is `409`/`403` and recorded as `APPROVAL_ATTEMPT_REJECTED` |
| `POST /api/v1/approvals/{id}/reassign` | InfoSec, Super Admin: `{version, assigneeId, reason}`; never to yourself, and never on an item you are the beneficiary, submitter or an earlier approver of; the new assignee must be active, not excluded by separation of duties and, for an InfoSec step, an InfoSec member |
| `POST /api/v1/approvals/{id}/override` | Super Admin: `{version, reason}`; recorded as `SUPER_ADMIN_OVERRIDE`; notifies InfoSec, the beneficiary's manager and the bypassed assignee |
| `GET /api/v1/email-outbox` | InfoSec, Super Admin, Auditor: the Mock Email Outbox, newest 100 |
| `GET /api/v1/access` | My Access: my grants, open and past, with `status`, `displayStatus` (e.g. `ACTIVE / VERIFIED`), method, `effectiveFrom`/`effectiveTo` and expiry |
| `GET /api/v1/access/team` | the grants of my direct reports |
| `POST /api/v1/grants/{id}/revoke` | the grant's employee, their current direct manager or, directly, a Super Admin (reason `DIRECT`): `{version, reason}`. A grant never sent to the tool is `CANCELLED` at once; otherwise `REVOCATION_REQUESTED` and a removal is queued (a verified call for API tools, a removal task for Manual and Vendor). A repeat returns the current state; anyone else gets `404` |
| `POST /api/v1/direct-grants` | Super Admin: `{submissionId, beneficiaryId, accessLevelId, justification, expiresOn}` → a `DIRECT_GRANT` request whose item is `APPROVED` at once with no steps, fulfilled by the tool's method. Never for yourself (`403 separation_of_duties`); `expiresOn` is required (`expires_required`), after today (`expires_in_past`) and in the company timezone; access lasts through that date, so it ends at 00:00 the next day, which may be at most 90 days from now: today + 89 is the latest date (`duration_too_long`); Submit's checks apply (`level_not_requestable`, `open_request_exists`, `access_exists`, `beneficiary_not_active`). Notifies InfoSec, the beneficiary's manager and, when the level requires one, the tool owner. A repeated `submissionId` returns the original (`200`) |
| `POST /api/v1/grants/{id}/change` | the grant's employee or their current direct manager (D3), on an `ACTIVE` grant not yet due: `{submissionId, accessLevelId, justification, duration}`. The held level is an `EXTENSION` (only of a temporary grant, to a later date or Until Revoked, else `400 not_an_extension`); another level of the same tool is a `LEVEL_CHANGE` (`400 level_of_another_tool`, `level_not_requestable`). Approved under the requested level's current steps; one item per request. `409 grant_not_active`, `change_in_progress` (a level change of this grant is already approved), `open_request_exists`; anyone else gets `404`. A repeated `submissionId` returns the original (`200`) |
| `GET /api/v1/fulfillment-tasks` | the open Manual and Vendor tasks of my admin groups, oldest first: type (`GRANT`/`REVOKE`), status, method, group, tool, level, employee, request and item, ticket, claimer, `createdAt`; `canAct: false` on my own grant or a direct action I started. A removal is listed once the grant's own task has ended |
| `POST /api/v1/fulfillment-tasks/{id}/claim` · `…/ticket` · `…/confirm` · `…/fail` | an active member of the tool's admin group, never the grant's employee or a direct action's initiator (`403 separation_of_duties`); a Super Admin who is not a member cannot work a task. `{version}`; ticket also `{ticketRef}` (Vendor only, ≤ 100 characters), fail also `{reason}`. Claim takes a queued task (`409 task_claimed` if another member holds it) or takes over one whose claimer left the group; claiming a grant task whose level or tool was retired cancels it (`409 level_retired`). Only the claimer records the ticket, confirms or fails (`403 not_claimer`). Confirm gives `ACTIVE / SELF_REPORTED` (or `REVOKED / SELF_REPORTED`); a Vendor task needs its ticket (`409 ticket_required`), and with a ticket the grant shows `WAITING_VENDOR`. Fail sends the task to Exceptions. A repeat returns the current state; a stale version is `409 version_conflict` |
| `GET /api/v1/exceptions` | InfoSec, Super Admin, Auditor: `FAILED` operations (tool, level, employee, attempts, last error, `failedAt`), unroutable approval steps, and `overlaps`: level changes whose old level is still held because its removal failed at least once (tool, employee, old and new level and grant, the removal's status and last error), until the removal is verified or confirmed |
| `POST /api/v1/operations/{id}/retry` | Super Admin or a member of the tool's admin group, never on their own grant (`403 separation_of_duties`): `{version}` on a `FAILED` operation starts a new round of 5 attempts with the same operation ID; a Manual or Vendor task returns to the queue unclaimed and without its ticket (the old reference stays in the audit log) |

Only active employees receive roles, group memberships or password resets. Removing a role or membership closes it; nothing is deleted. Every change is in the audit log. In this milestone a department or manager change is only recorded; the transfer workflow (DECISIONS.md D5) comes later. Catalog changes are audited the same way; approval-policy changes (a level's steps) also notify every active InfoSec member in the app (DECISIONS.md F2, O2).

**Approval rules** (DECISIONS.md F2, D1, D3, D9, O2, O5):
- Each level's steps are copied onto the item at submission and decided in order: Manager → Tool Owner (optional) → InfoSec (optional). A step is routed when it opens, and its `routingNote` shows where it went and why.
- Manager step: the beneficiary's manager → the manager's manager → the InfoSec pool. Tool Owner step: the owner → the InfoSec pool. InfoSec step: the pool of active InfoSec members.
- Separation of duties: the beneficiary, the submitter and anyone who already decided a step on the item never decide another step on it. When the beneficiary's direct manager submits, the Manager step is `APPROVED_BY_SUBMISSION`.
- A rejection is final for that item; other items of the submission continue. A pool step with no eligible member is listed as unroutable and is resolved by reassignment or a Super Admin override.
- One email in the Mock Email Outbox per submission whose Manager step went to a person; none when the manager submitted, none to InfoSec. Nothing is ever approved by email.
- One open item per person and tool, whatever the level, and no new request while the person holds an open grant for the tool (`409 access_exists`; open is anything but `REVOKED` and `CANCELLED`, including `FAILED`).

**Fulfillment rules** (DECISIONS.md Fulfillment methods, A6, A9, O1, O3, O6, D5):
- At final approval a level or tool retired by then cancels the item first (`CANCELLED`, `closedReason`), with no grant and no further approval. Then the level's current steps are checked again: a step it now requires is added and routed (stricter wins). Otherwise the item is `APPROVED` and its grant and `GRANT` operation are created in the same transaction.
- API grants are `PROVISIONING` until the runner's call succeeds **and** the status lookup shows the level; then `ACTIVE / VERIFIED` with `effectiveFrom`. A `GRANT` never sent whose level or tool was retired in the meantime is `CANCELLED` without a call.
- A failed attempt retries after 30 s, 1 min, 2 min and 4 min; after the 5th, or at once on a `409` from the tool, the operation is `FAILED` and appears in Exceptions. Every attempt is audited.
- Removal is verified the same way (`REVOKED / VERIFIED`, `effectiveTo`); requested or failed removals still count as held access. A removal waits while the grant's own provisioning call may be in flight, and cancels a queued or failed provisioning so it cannot re-apply access.
- Removals run before grants; within a priority, oldest first.

**Expiry, extensions and level changes** (DECISIONS.md D4, D6, A6, A9):
- A temporary grant is valid through its whole expiry date in the company timezone. From 00:00 the next day the sweep (at startup, then every minute) requests its removal with reason `EXPIRED`, as the system and with no notification; a grant never sent to the tool is `CANCELLED` ("expired"). Only one instance lists the due grants at a time (advisory lock), and each grant is re-checked under its locks, so a second or concurrent round, or an extension approved meanwhile, never adds a removal.
- An item whose expiry date passed while it waited for approval is cancelled at final approval ("expired before approval"); no grant is created.
- An extension keeps the grant `ACTIVE` while it waits and, at final approval, only changes its expiry date (`GRANT_EXTENDED`): no operation and no tool call. A pending extension never holds expiry back. A direct grant can be extended only this way.
- A level change creates the new grant at final approval; the old one stays `ACTIVE` and untouched. Only when the new level is verified (API) or confirmed (Manual/Vendor) is the old level's removal (reason `LEVEL_CHANGE`) created, in the same transaction. If the new level fails, the old one is kept and nothing is removed.
- Closing a grant (revoke, expiry) cancels its pending extension or level change ("the access has ended"); one approved after the access ended or became due is cancelled the same way. No email is sent for an extension or level change (O2).

Sessions last 30 minutes, are renewed while in use, and end after at most 12 hours. Role, status and logout changes apply on the next request.

## Services

| Service | Role | Exposed |
|---|---|---|
| `app` | Go server: JSON API under `/api/v1`, the embedded React build, the operation runner (one goroutine; the `operations` table is its queue) and the sweeps loop (temporary access expiry, every minute) | port 8080 |
| `mocktools` | Mock Tool Service, own database, reached by `app` only over HTTP | internal only |
| `postgres` | PostgreSQL 18 with the `accessflow` and `mocktools` databases | internal only |

The Mock Tool Service keeps accounts and permissions per (tool, account, level) and applies operations idempotently by operation ID (DECISIONS.md D4, A6):

| Endpoint | |
|---|---|
| `POST /v1/operations` | `{operationId, type, tool, account, level}` with `type` `GRANT`, `REVOKE`, `SUSPEND` or `RESUME` → `{…, result}` with `APPLIED` or `NO_CHANGE`. A repeated operation ID returns its stored result without acting again; the same ID with other content is `409 operation_id_reused`. Only successful operations are stored: a rejected one (`409`) can be retried with the same ID and is evaluated again. Revoking one level keeps other levels and the account. `SUSPEND`/`RESUME` of a level not held is `409 not_granted`; `GRANT` of a suspended level is `409 level_suspended` |
| `GET /v1/tools/{tool}/accounts/{account}` | the account's actual state: `{exists, levels: [{level, status}]}`; an unknown account is `exists: false`, never 404 |

AccessFlow identifies tools and levels by their AccessFlow IDs and accounts by the employee's email. Its client (`internal/access`) treats any answer other than a well-formed `200` echoing the request (an error status, a timeout, a malformed body) as a failure. The operation runner calls it outside any database transaction, with a 5-second timeout and a 30-second lease per claim.

`app` runs migrations at startup as `accessflow_owner` and then serves as `accessflow_app`, which has no DDL rights, may only insert into and read `audit_events`, and can never delete rows; a trigger also blocks `UPDATE`, `DELETE` and `TRUNCATE` and sets each event's time to the real transaction time.

## Tests

Go is not needed on the host; tests run only in Docker:

```bash
docker compose --profile test run --rm tests
```

This runs `gofmt`, `go vet` and `go test -race` against a disposable in-memory PostgreSQL (`postgres-test`). Each test gets its own database cloned from a migrated template, connected as the runtime user. The frontend type check (`tsc --noEmit`) runs in the image build.

**CI:** GitHub Actions (`.github/workflows/ci.yml`) runs `docker compose build` (Go build, frontend type check and build) and the test command above on every pull request to `main` and every push to `main`. Results are on the pull request and in the repository's Actions tab. A PR is merged only when CI is green on its latest commit.

`postgres-test` keeps running between test runs and holds its data in memory. To reset it (after changing `deploy/postgres/init.sh`, or to free the templates and databases left by changed migrations or interrupted runs): `docker compose --profile test down`.

## Layout

```
cmd/accessflow      server (and `healthcheck`, `bootstrap-password`, `dev-fixture` commands)
cmd/mocktools       Mock Tool Service
internal/platform   config, DB pool and transactions, migrations, HTTP helpers
internal/audit      append-only audit writer
internal/auth       sign-in, sessions, passwords, initial Super Admin
internal/people     employees, roles, admin groups
internal/catalog    tools, access levels and approval steps, starter packages
internal/requests   access requests, approval routing and decisions, Mock Email Outbox
internal/notify     in-app notifications
internal/access     grants, operations, the runner, fulfillment tasks, expiry, the Mock Tool Service client
internal/sweeps     the scheduled jobs loop (expiry)
internal/mocktools  Mock Tool Service handlers and migrations
internal/devfixture development employees, groups and tools (opt-in)
internal/webui      embedded web build and SPA fallback
internal/testutil   test databases, fixtures, API test client, lock-wait and fault-injection helpers
web/                React + TypeScript app (placeholder until the design arrives)
deploy/postgres     database and user setup, run on first start
```

## Known limitations

- Database credentials are fixed local values in `compose.yaml` (a local case-study setup, DECISIONS.md A2). PostgreSQL applies them only when its volume is first created; to change them, remove the volume (`docker compose down -v`).
- There is no login rate limiting beyond the lockout (DECISIONS.md A4): 5 wrong passwords lock an account for 15 minutes, so anyone who knows an email can keep that account locked. Every attempt is audited.
- One `app` instance is assumed (A2). Initial Super Admin creation is safe across processes (advisory lock), but running several instances is not tested.
- `app` keeps the migration (owner) connection string in its environment for its whole lifetime, although it uses it only at startup. Separating it would need a one-shot migration service; with fixed local credentials it adds no protection here.
- Pending steps are not re-routed when their assignee is later suspended or terminated (that comes with the leaver workflow, M9); until then InfoSec reassigns them. A grant whose level or tool is retired after it reached the tool stays active until removed (retirement does not revoke held access).
- Manual and Vendor evidence is self-reported: AccessFlow records who confirmed and when (and the vendor ticket), but cannot check the tool. A task claimed once (or retried after a claim) is finished even if its level is retired meanwhile (safe fulfillment). Suspend and resume tasks come with suspension (M9).
- Someone who holds two recipient roles (for example a manager who is also InfoSec) gets one notification per role for a direct grant or a Super Admin override.
- Expiry is checked once a minute, so access can last up to a minute past midnight after its expiry date, plus the time the removal takes (a Manual or Vendor removal waits for its task).
- A removal of a grant that races with a brand-new change request for it can, in a narrow case, deadlock; PostgreSQL then fails one of the two (a 500) and it can be repeated.
- While a level change's old removal has failed (an overlap), the new grant cannot be extended (`409 access_exists`: the old grant still counts as held) and a failed new grant blocks another change of the old one (`409 change_in_progress`) until an admin resolves the exception.
- An approved extension cannot be withdrawn (it is already in force); give the access up with revoke instead.
- Withdrawing an approved item sets its `closed_at` to the withdrawal time; the approval time stays in its steps and the audit log.
- The Mock Tool Service has no authentication: it is reachable only on the internal Docker network (DECISIONS.md A2). It is a stand-in for real tools, not a model of their security.
