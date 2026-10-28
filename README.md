# AccessFlow

Access requests, approvals, granting and removal, quarterly reviews and audit proof for internal tools.
Requirements: [`CASE.md`](CASE.md). Rules and architecture: [`DECISIONS.md`](DECISIONS.md). Plan: [`docs/plans/`](docs/plans/).

> Status: identity API (milestones M2–M3). Sign-in, sessions, password change, the initial Super Admin, employees, roles and admin groups work through the API; there are no screens yet (they arrive with the design). Access workflows arrive in later milestones.

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

For trying the API by hand, create a few employees (HR Manager, two InfoSec members, an Auditor, a manager with two reports):

```bash
docker compose exec app /accessflow dev-fixture
```

They are `hr@`, `infosec1@`, `infosec2@`, `auditor@`, `manager@`, `dev1@` and `dev2@accessflow.local`, all with the `SEED_PASSWORD` value. The command never runs on its own, can be run again safely (it skips existing employees and leaves them as they are) and marks what it creates as seeded (DECISIONS.md D12).

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

Only active employees receive roles, group memberships or password resets. Removing a role or membership closes it; nothing is deleted. Every change is in the audit log. In this milestone a department or manager change is only recorded; the transfer workflow (DECISIONS.md D5) comes later.

Sessions last 30 minutes, are renewed while in use, and end after at most 12 hours. Role, status and logout changes apply on the next request.

## Services

| Service | Role | Exposed |
|---|---|---|
| `app` | Go server: JSON API under `/api/v1` and the embedded React build (background jobs arrive in later milestones) | port 8080 |
| `mocktools` | Mock Tool Service, own database, reached by `app` only over HTTP | internal only |
| `postgres` | PostgreSQL 18 with the `accessflow` and `mocktools` databases | internal only |

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
internal/devfixture development employees (opt-in)
internal/webui      embedded web build and SPA fallback
internal/testutil   test databases, fixtures, API test client
web/                React + TypeScript app (placeholder until the design arrives)
deploy/postgres     database and user setup, run on first start
```

## Known limitations

- Database credentials are fixed local values in `compose.yaml` (a local case-study setup, DECISIONS.md A2). PostgreSQL applies them only when its volume is first created; to change them, remove the volume (`docker compose down -v`).
- There is no login rate limiting beyond the lockout (DECISIONS.md A4): 5 wrong passwords lock an account for 15 minutes, so anyone who knows an email can keep that account locked. Every attempt is audited.
- One `app` instance is assumed (A2). Initial Super Admin creation is safe across processes (advisory lock), but running several instances is not tested.
- `app` keeps the migration (owner) connection string in its environment for its whole lifetime, although it uses it only at startup. Separating it would need a one-shot migration service; with fixed local credentials it adds no protection here.
