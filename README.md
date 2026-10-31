# AccessFlow

AccessFlow manages access to a company's internal tools, from the request to its removal, with proof for the auditor.

**The problem it solves** (CASE.md): a regulated company of about 400 people and 60 tools grants access over chat. Nobody records who asked, who approved or why; admins hear about joiners, movers and leavers late; access is never removed (an audit found 14 leavers still holding accounts); and the quarterly access review the regulator asks for cannot be proven.

**What AccessFlow does instead:** every access is requested with a reason and a duration, approved by the right people under separation of duties, granted and removed in the tool (by API, or as a tracked task for a Manual or Vendor tool), reviewed every quarter by the tool owners, and recorded in an append-only audit log that answers who had access to what on any date and who approved it.

Go and PostgreSQL backend, React + TypeScript web UI in Turkish. A local case-study build, not a hardened production deployment (see [Important limitations](#important-limitations)).

## Quick start

```bash
docker compose up
```

Open http://localhost:8080 (health: `GET /api/v1/health`). If port 8080 is taken, choose another host port: `ACCESSFLOW_PORT=18080 docker compose up`. No other setup is needed: `compose.yaml` starts a **local demo** with ready-to-use accounts and realistic data. Only the app port is published; PostgreSQL and the Mock Tool Service stay on the internal Docker network.

### Demo accounts

| Account | Password | Role it demonstrates |
|---|---|---|
| `admin@accessflow.local` | `Demo-Admin-2026` | Super Admin (Okan Yılmaz) |
| `selin.aydin@accessflow.local` | `Demo-InfoSec-2026` | InfoSec |
| `ayse.demir@accessflow.local` | `Demo-HR-2026` | HR Manager, owner of the vendor-fulfilled Payroll Portal |
| `gizem.arslan@accessflow.local` | `Demo-Auditor-2026` | External Auditor: audit events only |
| `burak.sahin@accessflow.local` | `Demo-Manager-2026` | Manager of the product team |
| `can.ozturk@accessflow.local` | `Demo-Owner-2026` | Tool Owner (Jira, GitHub), IT Operations admin group |
| `deniz.kara@accessflow.local` | `Demo-Employee-2026` | Employee |

Each account has its own password (`DEMO_PASSWORDS` in `compose.yaml`). The sign-in page lists the accounts without passwords (a click fills the e-mail), and every page shows a "Demo Environment" badge. Demo labels and catalog names are in English; the rest of the UI is Turkish. The other demo employees have no password and no activation link; a Super Admin can send them one (see "Account activation").

**Demo data.** On first start the server builds a small company through the real services, at real time, as the people whose role each action needs (DECISIONS.md D12): 18 employees, a company catalog of eight departments and 18 job positions, six tools covering API, Manual and Vendor fulfillment, and requests in every state, from approvals waiting at each step to a failed task, imported legacy access with orphan accounts, a suspended and a terminated employee, and a running quarterly review. Nothing time-dependent is faked; overdue reminders and review findings appear when their real time comes.

The seed runs once, on a database that holds only the demo Super Admin, and later starts change nothing. If the log says `demo seed: the database holds data from an interrupted start or an earlier version`, start a separate project with its own fresh volumes, which leaves your existing data untouched:

```bash
docker compose down                   # keeps the existing volumes
docker compose -p accessflow-demo up  # fresh volumes named accessflow-demo_*
```

Deleting the main project's data instead (`docker compose down -v`) is destructive and never needed for the demo.

**Demo mode is local only.** The server refuses to start in demo mode unless `ACCESSFLOW_ENV=local`, `COOKIE_SECURE` and `JWT_SECRET` are unset, every demo account has a distinct valid password, and both database URLs point at the Compose `postgres` service with no query parameters (DECISIONS.md A4). A real deployment removes the three demo lines from `compose.yaml`.

## Workflows

- **Request:** any employee asks for a tool and access level with a reason and a duration (temporary, 2–90 days, or until revoked); managers can ask for their reports; starter packages are recommended by department and position.
- **Approve:** each access level defines its steps (Manager, then optional Tool Owner and InfoSec), decided in order. A step whose approver is missing is re-routed (the manager's manager, then the InfoSec pool), never skipped; one that nobody eligible can decide appears in Exceptions.
- **Grant and remove:** API tools are changed and then verified by a status lookup, with retries; Manual and Vendor tools get tracked tasks for their admin groups (a Vendor task carries the vendor ticket). Access ends on expiry, on revoke, on transfer (every grant pre-selected for removal; HR keeps one only with a justification) and on termination (removals first, ahead of all other work); suspension is reversible.
- **Joiners, movers, leavers:** HR creates, transfers, suspends, reactivates, schedules, terminates and rehires employees; each is one transaction with its access changes and audit events.
- **Quarterly access review** (Erişim Kontrolü; campaigns titled "Erişim Denetimi — <year> <n>. Çeyrek"): each tool owner keeps or removes every grant; a reminder on day 7, InfoSec escalation on day 14 and, on day 21, an open security finding for every unanswered item. A missed deadline never removes access automatically (D8).
- **Exceptions** (İstisnalar): failed operations, unroutable steps, level-change overlaps, failed suspensions, open leaver removals, stale grants, review findings and orphan accounts, each with its exact total.
- **Account activation:** a new, imported or rehired employee gets no password but a single-use activation link, valid 24 hours, in the local Mock Email Outbox. The link opens "Şifreni Belirle", where the employee chooses a password (12–128 characters, not a common password, not their email name); it signs nobody in. A Super Admin reveals links (each reveal audited), sees pending, expired and completed activations, and sends new links; a new link ends the current password and sessions at once.
- **Onboarding:** HR keeps a catalog of departments and job positions. A Super Admin imports employees (without passwords or roles; each gets an activation link), tools with their levels and approval steps, and existing access from one Excel workbook: download the template, preview, then import all or nothing. Existing records are never overwritten.
- **Existing access:** imported as baseline grants (no approval invented) from the workbook, an account CSV or the Mock Tool Service read, which also verifies reported API access; unmatched accounts become orphan findings for InfoSec.

## Security and separation of duties

Every permission is enforced on the server; the UI only hides what a role cannot do.

- **Nobody approves their own access.** The beneficiary, the submitter and anyone who already decided a step never decide another step of the same item. A direct manager's own submission counts as the Manager step, unless that manager is also an HR Manager or Super Admin (they can change reporting lines, so someone else decides the step).
- **Nobody fulfils their own access.** An admin-group member never works a task for their own grant or for a direct action they started; a Super Admin never gives themself a direct grant.
- **Super Admin is never granted by one person.** A routine promotion is nominated by a Super Admin and authorized by another Super Admin (by an InfoSec member when there is no other). HR succession (only when at most one Super Admin is not terminated) is nominated by an HR Manager, authorized by an independent InfoSec member or Super Admin, and accepted by the nominee in their own account; Super Admins and InfoSec are notified at each step.
- **External Auditor:** an account with the Auditor role reads, filters and exports the audit events and nothing else, whatever other roles it holds. The API refuses every other endpoint to it by default (`403`), and the UI shows only "Denetim kayıtları" (DECISIONS.md O7 note 2026-10-31). The role is refused to an account with open duties (other roles, groups, owned tools, reports, pending approvals or reviews, a nomination), and no such duty is given to an Auditor (`409`).
- **Activation links:** nobody reveals their own link, and the link of a Super Admin or InfoSec account is revealed only by a Super Admin who did not issue it, with the other Super Admins notified.
- **Sign-in:** argon2id password hashes, a per-account lockout (5 wrong passwords, 15 minutes), a per-address rate limit, signed `HttpOnly`, `SameSite` session cookies with CSRF protection, cross-site requests refused, and security headers on every response.
- **Database:** the app connects as a role with no DDL rights that can only insert into and read the audit log; SQL is parameterized; state changes and their audit events commit in one transaction. A transaction PostgreSQL aborts as a deadlock is retried, at most three attempts in all.

## Auditability

- An append-only audit log: the database role cannot update or delete events, and a trigger blocks `UPDATE`, `DELETE` and `TRUNCATE` and stamps each event with the real transaction time.
- Employees and business records are never deleted; history stays.
- The external Auditor pages through every audit event (exact totals), filters by date, actor, action, tool and text, and exports every matching event as one CSV file, streamed by the server.
- Point-in-time access for any date with who approved it, and evidence reports, for InfoSec and Super Admins: privileged users, Super Admin overrides and direct grants, approval-policy changes, leavers' access and the quarter's review with its findings.

## Verified performance

Measured on an isolated stack with 12,005 review items (`docs/qa/final/performance-audit.md`):

- Sign-in while the day-7 review sweep runs: 38.7 s → 0.79 s (day 21: 138.8 s → 1.0 s), after the sweep became set-based and stopped blocking sign-in.
- Point-in-time access query: 3.2 s → 0.57 s (p50), with PostgreSQL JIT turned off for the app.
- Review items, open findings, the task queue and the team list are paged on the server (100 a page, exact totals): every row reachable, page p50 23–137 ms.
- Starting a campaign over 12,000 grants takes about 9 s; accepted for a quarterly action (it does not block sign-in).

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

## Architecture decisions and trade-offs

The full reasoning, alternatives and consequences are in [`DECISIONS.md`](DECISIONS.md) (A1, A4, A6, F4, O2).

**Go and PostgreSQL, one modular monolith (A1).** The case asks for Go and React. One Go binary serves the API, the embedded UI, the operation runner and the sweeps; packages follow the domain (people, catalog, requests, access, reviews, explorer). PostgreSQL carries the correctness: transactions, row locks, partial unique indexes (one open grant per employee and level, one pending request per tool, one open removal per grant), `CHECK`-constrained state machines and an append-only audit table. Business rules are pure functions with table tests; database behaviour, locking and concurrency are tested against real PostgreSQL.

**Security and audit consistency.** Every state change and its audit event commit in one transaction, with one database timestamp; the app's database role can only insert into and read `audit_events`, and a trigger blocks changes. Sessions are checked against the database on every request (status, token version, roles), and an action that needs a role re-reads it after taking its locks, so a suspension or role removal committed while it waited is seen. Separation of duties (no self-approval, no self-fulfillment, no self-review) is enforced in the backend; the UI only hides actions.

**PostgreSQL as the operation queue (A6).** Access state, operations and audit events share one database, so approving access, creating the grant and queueing its `GRANT` operation are one atomic transaction, with no gap and no outbox relay. The runner claims due operations with `FOR UPDATE SKIP LOCKED` under a 30-second lease with a fresh token; the tool call runs outside any transaction; a result counts only if the lease is still held, so a stale worker cannot overwrite a newer one. The operation ID makes the call idempotent, unique indexes and the grant state machine stop duplicates, and success needs a read-back of the tool's state. Errors retry with backoff (5 attempts), then appear in Exceptions for a manual retry. At about 400 employees and 60 tools this is hundreds of operations a day; a broker would add a service to operate and a second source of truth for no needed capability. It becomes worth it with many thousands of operations a minute, several consumers of access events (SIEM, HR, data platform) or delivery to systems outside this database; the transactional write would stay, with an outbox feeding the broker.

**Mock Tool Service (A1).** The case is governance, not SaaS integration. Real GitHub, Jira or cloud connectors would need credentials, provider permission models and network access, and would make the demo and tests depend on outside systems. The Mock Tool Service is a separate container with its own database, reached only over HTTP. It grants, revokes, suspends and resumes a level per account (idempotent by operation ID, refusing invalid transitions with `409`), returns an account's held state, and lists a tool's accounts for the go-live import, which records what the tool holds, verifies spreadsheet-reported API access and flags accounts matching no employee. The runner depends on a small `ToolClient` boundary (`Apply`, `Account`; the import also lists accounts), so a real provider adapter would replace the HTTP client without touching approval, audit or lifecycle logic. No real adapter exists. Failures are injected in tests by an HTTP fault proxy; the running demo has no failure switch. **Manual and Vendor** tools never reach the service: an admin-group member confirms the outcome (Vendor with a ticket reference), and that evidence is labelled *self-reported* everywhere, because AccessFlow cannot see the tool. API access is *verified* by read-back. The mock proves AccessFlow's workflow, retries, verification and reconciliation, not the behaviour of real SaaS platforms.

**Mock Email Outbox (F4, O2).** Onboarding, rehire and recovery need a way to give an employee a credential they choose, and Managers are told about access requests. The case requires no e-mail provider, and `docker compose up` must work with no credentials, so e-mails go to an outbox table written in the same transaction as the change and shown in the app. An activation link is 32 random bytes; only its SHA-256 is stored; it is valid 24 hours and once, and issuing a new one revokes earlier links, removes the current password and ends every session. The outbox keeps the pending link AES-256-GCM-encrypted with a key on the `appdata` volume, never in the database, and clears it when the link is used, replaced or expires. A Super Admin reveals a link one e-mail at a time; every reveal is audited, and `ACTIVATION_COMPLETED` names who revealed the link that was used. Nobody reveals their own link, and a Super Admin's or InfoSec member's link is revealed only by another Super Admin who did not issue it, with the others notified. Mailpit-style mail catchers and printing links to the log were rejected: the same exposure with less control, or a credential in retained logs. **Boundary:** the reveal is a local-demo stand-in for delivery, not a production e-mail security model. A Super Admin could use an ordinary employee's revealed link; the audit makes that visible but does not prevent it. A real deployment sends each link straight to its employee through a controlled provider and removes the reveal.

**Demo boundaries versus a real deployment.** Demo mode (`compose.yaml`) seeds documented accounts and refuses to start outside local settings. A real deployment needs real e-mail delivery, real tool adapters, SSO or MFA, HTTPS with secure cookies, managed secrets and database credentials, and metrics on the queue and sweeps. See [Production limitations](#production-limitations-and-future-considerations).

## Running outside demo mode

### First sign-in

Without the three demo lines in `compose.yaml` (`ACCESSFLOW_DEMO`, `ACCESSFLOW_ENV`, `DEMO_PASSWORDS`), `app` creates the initial Super Admin (`admin@accessflow.local`, or `BOOTSTRAP_ADMIN_EMAIL`) with a one-time password and nothing else. The password is never logged; read it with:

```bash
docker compose exec app /accessflow bootstrap-password
```

Sign in with it at http://localhost:8080; the app asks for a new password first. Until it is changed every other API call answers `403 password_change_required`. After the change the old password stops working and the file is deleted. This password does not expire. Employees get activation links instead of passwords (DECISIONS.md F4).

### Account activation (Mock Email Outbox)

**Mock email delivery is for local demonstration only and is not suitable for production use.** No e-mail is sent. To activate an employee in the demo: sign in as the Super Admin, open Yönetim ayarları → E-posta kutusu, open the activation e-mail and choose "Bağlantıyı göster" (the reveal is audited), then open the link, ideally in a private window, and set a password; then sign in as that employee. Yönetim ayarları → Hesap etkinleştirme lists pending, expired, completed and missing activations and sends new links.

Links are stored only as SHA-256 hashes for authentication. The outbox keeps each pending link encrypted with a key in `/var/lib/accessflow/mock-email.key` on the `appdata` volume (never in the database), so links survive restarts; the encrypted copy is cleared when the link is used, replaced or expires. If that key is lost while links are pending, the app does not replace it silently: links can be neither shown nor issued until you run

```bash
docker compose exec app /accessflow reset-mail-key   # revokes the pending links, audited
docker compose restart app
```

and send new links from the activation screen.

### Super Admin recovery

If no active Super Admin is left, the app shows a recovery banner: an HR Manager nominates a successor in the UI, an InfoSec member (or another Super Admin) who is neither the proposer nor the nominee authorizes it, and only then does the nominee get a 24-hour link in their own mock outbox and accept the role, signed in as themself (DECISIONS.md D2 notes 2026-10-30 and 2026-10-31). A suspended Super Admin still counts, so suspending Super Admins does not open a succession. HR never sees the link. When that is impossible too (for example no active HR Manager is left), the last resort runs on the server:

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
| `LOGIN_RATE_LIMIT` | `60` | sign-in and activation attempts allowed per client address per minute (local demo default; see Production limitations) |
| `MOCK_EMAIL_KEY_FILE` | `/var/lib/accessflow/mock-email.key` | the key encrypting pending activation links in the Mock Email Outbox (the `appdata` volume) |

## API documentation

With the app running in the local demo setup (`ACCESSFLOW_DEMO=true`, the `compose.yaml` default; otherwise the page answers `404`), the API is documented at **http://localhost:8080/swagger/** (Swagger UI, read-only: it lists every endpoint under `/api/v1` with its roles, request and response schemas and error codes, and never sends requests). The OpenAPI document is `web/public/swagger/openapi.json`; the business rules behind each endpoint are in [`docs/api.md`](docs/api.md).

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
- No SSO and no real e-mail: activation links go to a local Mock Email Outbox, where a Super Admin reveals them (audited), for demonstration only; notifications are in the app. No multi-factor authentication yet (planned).
- Accepted security residuals: separation of duties stops one person, not collusion (two Super Admins together, or HR, an authorizer and the nominee in a succession); the session secret is stored in the database; the app's database role can insert audit events. Each is listed in `docs/known-limitations.md`.
- Long lists outside the paged ones show their first 200 rows (500 orphan accounts) with exact totals and a notice.

### Production limitations and future considerations

This version is a local case-study demo. Before real use:
- **Real e-mail delivery** would send each activation link to its employee and drop the outbox reveal, so no administrator could see a link. Until then a Super Admin who reveals a pending link could use it (audited; the employee would find the link used). Nobody can reveal their own link, and a Super Admin or InfoSec account's link only by a Super Admin who did not issue it, with the other Super Admins notified.
- **MFA:** an authenticator app, mandatory for privileged roles first (shown as "Planlanıyor" in the user menu).
- **Rate limiting:** sign-in and activation together allow 60 attempts per minute per client address (`LOGIN_RATE_LIMIT`), on top of the per-account lockout (5 wrong passwords lock an account for 15 minutes). With Docker Desktop every browser on the machine reaches the app through one Docker address, so the 60 attempts are shared by everyone using the demo; beyond that, sign-ins get "too many attempts" (`429` with `Retry-After`) for the rest of the minute. 60 is chosen for the local demo; review it for any other environment. The limit uses the connection's address and ignores `X-Forwarded-For`, so a reverse proxy needs a trusted-proxy setting first (otherwise all users would share the proxy's budget).
- **Secrets:** set `JWT_SECRET` from a secret store (a generated one is kept in the database), use HTTPS with `COOKIE_SECURE=true`, and replace the fixed database passwords.
- **Passwords:** add a breached-password check if an external service is acceptable; today only an offline common-password list is used.
- The UI is Turkish only.

All limitations, with their reasons: [`docs/known-limitations.md`](docs/known-limitations.md).

## Documentation

- [`CASE.md`](CASE.md): the requirements.
- [`DECISIONS.md`](DECISIONS.md): approved business rules and architecture.
- [`docs/operating-summary.md`](docs/operating-summary.md): how it runs each week, its rules and assumptions (one page).
- [`docs/rollout-30-days.md`](docs/rollout-30-days.md): the first 30 days of adoption.
- [`docs/api.md`](docs/api.md): API contracts and the rules behind them; Swagger UI at `/swagger/`.
- [`docs/known-limitations.md`](docs/known-limitations.md): all limitations, planned changes and out-of-scope items.
- [`docs/qa-checklist.md`](docs/qa-checklist.md): manual browser QA.
- [`docs/qa/final/`](docs/qa/final/): the final QA audit: security, performance and engineering reports, the test matrix, execution results and release readiness.
- [`docs/plans/`](docs/plans/): milestone plans.
