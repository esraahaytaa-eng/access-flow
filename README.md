# AccessFlow

AccessFlow runs access to a company's internal tools: people request access with a reason and a duration, the right people approve, access is granted and removed in the tool, tool owners review access every quarter, and Audit can see who had access to what on any date and who approved it. Go and PostgreSQL backend, React web UI in Turkish.

## Quick start

```bash
docker compose up
```

Open http://localhost:8080 (health: `GET /api/v1/health`). No other setup is needed: `compose.yaml` starts a **local demo** with ready-to-use accounts and realistic data.

### Demo accounts

| Account | Password | Role it demonstrates |
|---|---|---|
| `admin@accessflow.local` | `Demo-Admin-2026` | Super Admin (Okan Yılmaz) |
| `selin.aydin@accessflow.local` | `Demo-InfoSec-2026` | InfoSec |
| `ayse.demir@accessflow.local` | `Demo-HR-2026` | HR Manager, owner of the vendor-fulfilled Payroll Portal |
| `gizem.arslan@accessflow.local` | `Demo-Auditor-2026` | Auditor |
| `burak.sahin@accessflow.local` | `Demo-Manager-2026` | Manager of the product team |
| `can.ozturk@accessflow.local` | `Demo-Owner-2026` | Tool Owner (Jira, GitHub), IT Operations admin group |
| `deniz.kara@accessflow.local` | `Demo-Employee-2026` | Employee |

Each account has its own password (`DEMO_PASSWORDS` in `compose.yaml`). The sign-in page lists the accounts without passwords (a click fills the e-mail), and every page shows a "Demo Environment" badge. Demo labels and catalog names are in English; the rest of the UI is Turkish. The other demo employees have random passwords nobody knows.

**Demo data.** On first start the server builds a small company through the real services, at real time, as the people whose role each action needs (DECISIONS.md D12): 18 employees, a company catalog of eight departments and 18 job positions, six tools covering API, Manual and Vendor fulfillment, and requests in every state, from approvals waiting at each step to a failed task, imported legacy access with orphan accounts, a suspended and a terminated employee, and a running quarterly review. Nothing time-dependent is faked; overdue reminders and review findings appear when their real time comes.

The seed runs once, on a database that holds only the demo Super Admin, and later starts change nothing. If the log says `demo seed: the database holds data from an interrupted start or an earlier version`, start a separate project with its own fresh volumes, which leaves your existing data untouched:

```bash
docker compose down                   # keeps the existing volumes
docker compose -p accessflow-demo up  # fresh volumes named accessflow-demo_*
```

Deleting the main project's data instead (`docker compose down -v`) is destructive and never needed for the demo.

**Demo mode is local only.** The server refuses to start in demo mode unless `ACCESSFLOW_ENV=local`, `COOKIE_SECURE` and `JWT_SECRET` are unset, every demo account has a distinct valid password, and both database URLs point at the Compose `postgres` service with no query parameters (DECISIONS.md A4). A real deployment removes the three demo lines from `compose.yaml`.

## Main features

- **Requests:** any employee asks for a tool and access level with a reason and a duration (temporary or until revoked); managers can ask for their reports; starter packages are recommended by department.
- **Approvals:** each access level defines its steps (Manager, then optional Tool Owner and InfoSec); separation of duties throughout; stuck steps are re-routed, never skipped.
- **Granting and removing:** API tools are changed and verified automatically with retries; Manual and Vendor tools get tracked tasks for their admin groups. Access is removed on expiry, on transfer (pre-selected for removal) and on termination (removed first); suspension is reversible.
- **Reviews:** a quarterly campaign per tool owner with a reminder on day 7, InfoSec escalation on day 14 and automatic action on day 21.
- **Proof:** an append-only audit log, point-in-time access for any date with its approvers, and evidence reports for Audit.
- **Onboarding:** HR keeps a catalog of departments and job positions. A Super Admin imports employees (without passwords or roles), tools with their levels and approval steps, and existing access from one Excel workbook: download the template, preview, then import all or nothing. Existing records are never overwritten.
- **Existing access:** imported as baseline grants (no approval invented) from the workbook, an account CSV or the Mock Tool Service read, which also verifies reported API access; unmatched accounts become orphan findings for InfoSec.

## Architecture

| Service | Role | Exposed |
|---|---|---|
| `app` | Go server: JSON API under `/api/v1`, the embedded React build, the operation runner (one goroutine; the `operations` table is its queue) and the sweeps loop (temporary access expiry, every minute) | port 8080 |
| `mocktools` | Mock Tool Service, own database, reached by `app` only over HTTP | internal only |
| `postgres` | PostgreSQL 18 with the `accessflow` and `mocktools` databases | internal only |

`app` runs migrations at startup as `accessflow_owner` and then serves as `accessflow_app`, which has no DDL rights, may only insert into and read `audit_events`, and can never delete rows; a trigger also blocks `UPDATE`, `DELETE` and `TRUNCATE` and sets each event's time to the real transaction time.

### Layout

```
cmd/accessflow      server (and `healthcheck`, `bootstrap-password`, `recover-admin` commands)
cmd/mocktools       Mock Tool Service
internal/platform   config, DB pool and transactions, migrations, HTTP helpers
internal/audit      append-only audit writer
internal/auth       sign-in, sessions, passwords, initial Super Admin
internal/people     employees, department and job-position catalog, roles, admin groups, Super Admin succession
internal/catalog    tools, access levels and approval steps, starter packages
internal/requests   access requests, approval routing and decisions, Mock Email Outbox
internal/notify     in-app notifications
internal/access     grants, operations, the runner, fulfillment tasks, expiry, baseline import, orphans, the Mock Tool Service client
internal/reviews    access review campaigns, decisions, the day-7/14/21 steps and findings
internal/onboarding Excel onboarding workbook: template, parsing, validation, all-or-nothing import
internal/explorer   audit explorer: events, point-in-time access and evidence reports (read-only)
internal/sweeps     the scheduled jobs loop (expiry, scheduled terminations, review steps)
internal/mocktools  Mock Tool Service handlers and migrations
internal/demo       local demo accounts and seed (demo mode only)
internal/webui      embedded web build and SPA fallback
internal/testutil   test databases, fixtures, API test client, lock-wait and fault-injection helpers
web/                React + TypeScript UI (Turkish): pages, shared components, API client, translations
deploy/postgres     database and user setup, run on first start
```

## Running outside demo mode

### First sign-in

Without the three demo lines in `compose.yaml` (`ACCESSFLOW_DEMO`, `ACCESSFLOW_ENV`, `DEMO_PASSWORDS`), `app` creates the initial Super Admin (`admin@accessflow.local`, or `BOOTSTRAP_ADMIN_EMAIL`) with a one-time password and nothing else. The password is never logged; read it with:

```bash
docker compose exec app /accessflow bootstrap-password
```

Sign in with it at http://localhost:8080; the app asks for a new password first. Until it is changed every other API call answers `403 password_change_required`. After the change the old password stops working and the file is deleted. This password does not expire. Passwords that admins later issue to employees expire after 72 hours (DECISIONS.md F4).

### Super Admin recovery

If no active Super Admin is left, the app shows a recovery banner: an HR Manager nominates a new Super Admin in the UI and a different InfoSec member authorizes it (DECISIONS.md D2). When that is impossible too (for example nobody holds InfoSec), the last resort runs on the server:

```bash
docker compose exec app /accessflow recover-admin someone@company.com
docker compose exec app /accessflow bootstrap-password
```

It works only while no active Super Admin exists (it refuses otherwise). The employee with that email becomes Super Admin (one is created if none exists) with a new one-time password, read like the first one; their sessions end and the change is audited as `ADMIN_RECOVERED` with no actor.

### Configuration

All optional:

| Variable | Default | Meaning |
|---|---|---|
| `JWT_SECRET` | generated on first start and stored in the database | session signing secret, at least 32 bytes |
| `COOKIE_SECURE` | `false` | set `true` when served over HTTPS |
| `BOOTSTRAP_ADMIN_EMAIL` | `admin@accessflow.local` | the initial Super Admin |
| `BOOTSTRAP_FILE` | `/var/lib/accessflow/bootstrap-admin.txt` | where its one-time password is kept (the `appdata` volume) |
| `ACCESSFLOW_DEMO` | `false` (`true` in `compose.yaml`) | local demo mode: demo accounts and seed; refused outside the local setup |
| `ACCESSFLOW_ENV` | none (`local` in `compose.yaml`) | must be `local` for demo mode |
| `DEMO_PASSWORDS` | none (set in `compose.yaml`) | `account=password,…` for `admin`, `infosec`, `hr`, `auditor`, `manager`, `owner`, `employee` |
| `COMPANY_TIMEZONE` | `Europe/Istanbul` | the zone request expiry dates are in; an unknown zone stops startup |
| `MOCK_TOOLS_URL` | `http://mocktools:8081` (set in `compose.yaml`) | the Mock Tool Service the operation runner calls |

## Testing

Go is not needed on the host; tests run only in Docker:

```bash
docker compose --profile test run --rm tests
```

This runs `gofmt`, `go vet` and `go test -race` against a disposable in-memory PostgreSQL (`postgres-test`). Each test gets its own database cloned from a migrated template, connected as the runtime user, so the request-flow tests run in parallel. The frontend type check (`tsc --noEmit`) and the frontend tests (Vitest) run in the image build (`npm run build`), so `docker compose build` fails if either fails.

**CI:** GitHub Actions (`.github/workflows/ci.yml`) builds the images and runs the test command above as two parallel jobs on every pull request to `main` and every push to `main`, with the Go module and build caches kept between runs. A full CI run takes about 4 minutes. A pull request is merged only when CI is green on its latest commit.

`postgres-test` keeps running between test runs and holds its data in memory. To reset it (after changing `deploy/postgres/init.sh`, or to free databases left by interrupted runs): `docker compose --profile test down`.

## Important limitations

- A local case-study setup: fixed database credentials in `compose.yaml`, one `app` instance, plain HTTP on port 8080 (put HTTPS in front and set `COOKIE_SECURE=true` for real use).
- No real tool connectors: API tools are reached through the Mock Tool Service; Manual and Vendor evidence is self-reported by the admin who confirms it.
- Workbook-reported access is believed as reported: the API tool read verifies it later; Manual and Vendor access is first checked by the certification campaign. Re-imports never update existing employees or tools.
- No SSO and no real e-mail: HR hands over one-time passwords; notifications are in the app.
- The UI is Turkish only.

All limitations, with their reasons: [`docs/known-limitations.md`](docs/known-limitations.md).

## Documentation

- [`CASE.md`](CASE.md): the requirements.
- [`DECISIONS.md`](DECISIONS.md): approved business rules and architecture.
- [`docs/operating-summary.md`](docs/operating-summary.md): how it runs each week, its rules and assumptions (one page).
- [`docs/rollout-30-days.md`](docs/rollout-30-days.md): the first 30 days of adoption.
- [`docs/api.md`](docs/api.md): API contracts.
- [`docs/known-limitations.md`](docs/known-limitations.md): all limitations, planned changes and out-of-scope items.
- [`docs/qa-checklist.md`](docs/qa-checklist.md): manual browser QA.
- [`docs/plans/`](docs/plans/): milestone plans.
