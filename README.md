# AccessFlow

Access requests, approvals, granting and removal, quarterly reviews and audit proof for internal tools.
Requirements: [`CASE.md`](CASE.md). Rules and architecture: [`DECISIONS.md`](DECISIONS.md). Plan: [`docs/plans/`](docs/plans/).

> Status: identity and catalog API (milestones M2–M4). Sign-in, sessions, password change, the initial Super Admin, employees, roles, admin groups, the tool catalog with approval steps, starter packages and in-app notifications work through the API, and the Mock Tool Service applies per-level operations. There are no screens yet (they arrive with the design). Requests, approvals and provisioning arrive in later milestones.

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
| `PUT /api/v1/employees/{id}` | HR Manager or Super Admin: `{version, managerId, department, jobTitle}`, all four required (null clears; a missing one is `400 field_required`); `409 version_conflict` on a stale version; manager cycles are rejected |
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

Only active employees receive roles, group memberships or password resets. Removing a role or membership closes it; nothing is deleted. Every change is in the audit log. In this milestone a department or manager change is only recorded; the transfer workflow (DECISIONS.md D5) comes later. Catalog changes are audited the same way; approval-policy changes (a level's steps) also notify every active InfoSec member in the app (DECISIONS.md F2, O2).

Sessions last 30 minutes, are renewed while in use, and end after at most 12 hours. Role, status and logout changes apply on the next request.

## Services

| Service | Role | Exposed |
|---|---|---|
| `app` | Go server: JSON API under `/api/v1` and the embedded React build (background jobs arrive in later milestones) | port 8080 |
| `mocktools` | Mock Tool Service, own database, reached by `app` only over HTTP | internal only |
| `postgres` | PostgreSQL 18 with the `accessflow` and `mocktools` databases | internal only |

The Mock Tool Service keeps accounts and permissions per (tool, account, level) and applies operations idempotently by operation ID (DECISIONS.md D4, A6):

| Endpoint | |
|---|---|
| `POST /v1/operations` | `{operationId, type, tool, account, level}` with `type` `GRANT`, `REVOKE`, `SUSPEND` or `RESUME` → `{…, result}` with `APPLIED` or `NO_CHANGE`. A repeated operation ID returns its stored result without acting again; the same ID with other content is `409 operation_id_reused`. Only successful operations are stored: a rejected one (`409`) can be retried with the same ID and is evaluated again. Revoking one level keeps other levels and the account. `SUSPEND`/`RESUME` of a level not held is `409 not_granted`; `GRANT` of a suspended level is `409 level_suspended` |
| `GET /v1/tools/{tool}/accounts/{account}` | the account's actual state: `{exists, levels: [{level, status}]}`; an unknown account is `exists: false`, never 404 |

AccessFlow identifies tools and levels by their AccessFlow IDs and accounts by the employee's email. Its client (`internal/access`) treats any answer other than a well-formed `200` echoing the request (an error status, a timeout, a malformed body) as a failure. The operation runner that uses it arrives with API fulfillment.

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
internal/notify     in-app notifications
internal/access     Mock Tool Service client (grants and operations arrive later)
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
- The Mock Tool Service has no authentication: it is reachable only on the internal Docker network (DECISIONS.md A2). It is a stand-in for real tools, not a model of their security.
