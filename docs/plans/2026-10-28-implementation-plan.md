# AccessFlow — Implementation Plan

> Workflow (CLAUDE.md): for each milestone — present scope and get approval → implement only that milestone → TDD and relevant tests → code review (Superpowers + Ponytail) and fixes → report changes, test results, risks → approval before commit → separate approval before push / PR → Squash and Merge only after merge approval → no next milestone without approval. Every task follows Red → Green → Refactor.

**Goal:** Deliver every CASE.md flow (requests, approvals, granting and removing, quarterly reviews, proof) as one working program started by `docker compose up`, working end to end before it is made pretty, with each workflow usable in the UI as soon as the design is available.

**Architecture:** Go modular monolith (`app`) serving the API, background work and the embedded React build on port 8080; a separate Go Mock Tool Service (`mocktools`) reached only over HTTP; one PostgreSQL 18 server with `accessflow` and `mocktools` databases (A1, A2, A8).

**Tech stack:** Go 1.27 · chi v5 · pgx v5 (pgxpool, hand-written SQL) · goose v3 (embedded) · google/uuid · golang-jwt v5 · x/crypto (argon2id) · Vite + React + TypeScript + React Router + TanStack Query · Vitest + React Testing Library. UI component library chosen from `design/` (A7).

**Spec:** `CASE.md` + `DECISIONS.md` (F1–F4, D1–D12, fulfillment methods, O1–O8, A1–A10). This plan argues from both; it does not change them.

## Global Constraints

- Only `app` is exposed, on port 8080; AccessFlow never reads the `mocktools` database (A2).
- A Mock Tool Service error, timeout (5 s) or unexpected response is never success (A2, A6).
- Audit events are append-only and written in the same transaction as the state change; the runtime DB user cannot update or delete them (A9).
- Each transaction reads the database time once and uses it for rules, state and audit (A9). Audit timestamps are always real; no historical evidence is ever fabricated, including by fixtures or the seed (D12, O1).
- Approvals are always made by authorized humans; nothing is approved automatically or by email (F2, O2).
- Each step on an item is decided by a different person; the beneficiary never approves, fulfils or reviews their own item (F2).
- Manual and Vendor results are never `VERIFIED` (A9).
- Direct grants: temporary, at most 90 days from creation — the only duration limit (D1, D6).
- Retries: 5 attempts, backoff 30 s, 1 min, 2 min, 4 min, then `FAILED` (A6, O3).
- JWT: HS256 only, 30-minute lifetime, re-issued under half-life, 12-hour cap; status, `token_version` and roles loaded from the DB on every request (A4).
- Temporary passwords: 16 characters, single use, 72-hour expiry; passwords 12–128 characters; 5 failures → 15-minute lock (A4).
- Every permission and data-visibility rule is enforced in backend handlers and SQL queries; the UI only hides actions (A4, A7).
- Company timezone setting, default `Europe/Istanbul` (A6).
- Real system time; no simulated clocks or demo controls (D12).
- Never physically delete employees or business records (A5).
- Tests run only in Docker: `docker compose --profile test run --rm tests` (A10).
- No UI component library and no visual design before `design/` is provided (A7).

## Review Focus (cross-cutting risks every milestone must keep covered)

1. **Concurrent decisions** — two people act on one step at once: the first wins; the second gets `409` and an audit event for the rejected attempt. Lesson from the discarded attempt: the rejected-attempt audit event must be written in its own transaction (or the transaction must commit and return a non-error result), otherwise it rolls back with the conflict.
2. **Duplicate submissions** — the same submission ID or transition repeated returns the original result and creates no duplicate grant, operation or audit event (A9).
3. **Removal racing provisioning** — revoke, terminate or suspend while a grant operation is queued, in flight or waiting on a vendor: no access is left behind untracked; a late completion becomes a stale-grant incident and is revoked (D5).
4. **Stale tokens and stale roles** — a suspended, terminated or logged-out user's token, or a user whose role was just removed, is rejected on the next request (A4).
5. **Data-visibility leaks** — a Manager querying audit or access data outside their reporting scope, or an employee reading another employee's requests, gets `403`/no rows; tested at the API, not the UI.

---

## Frontend integration strategy

- **Before `design/` arrives:** milestones deliver infrastructure and backend workflows proven by API scenario tests. The web app stays a placeholder page (build pipeline and embedding only). No visual design, no component library.
- **When `design/` arrives:** milestone **U1 (UI foundation)** is inserted before the next backend milestone. It reviews `design/`, selects the UI library (A7), and builds the app shell, navigation, fetch wrapper, status-badge mapping, timezone display and login / password change. It also builds the screens for any backend milestones already completed (catch-up), so the UI never lags by more than one milestone.
- **After U1:** every milestone has a **UI slice** delivering its screens together with its backend workflow, in the same milestone and PR. A milestone is not complete until its UI slice works against the real API.
- Each milestone below lists its UI slice. Before U1, a milestone's UI slice is carried into U1's catch-up scope instead.

Screens and areas (O7 plus its 2026-10-28 clarification): My Requests / My Access · Approval Inbox · Fulfillment Tasks · Exceptions · Audit Explorer · Employees (HR) · Catalog (tools and levels) · Reviews · Imports and orphan accounts · Administration (roles, groups, Super Admin functions, settings). One application, one simple navigation; links shown according to the user's roles.

## Test data strategy

- **From M1:** `internal/testutil` provides a migrated template database, a per-test clone and small builders (`fixtures.Employee(...)`, `fixtures.Tool(...)`, `fixtures.Level(...)`, `fixtures.Login(...)`, later `fixtures.ActiveGrant(...)`). Builders create records through the same SQL/services the application uses, at real time.
- Each milestone extends the builders it needs, so tests read like scenarios instead of SQL setup.
- A tiny **development fixture** (bootstrap Super Admin plus a handful of employees, tools and levels) is added in M3, so the API and later UI can be exercised manually long before the full seed. It is idempotent, marked seed-originated and created through the real services.
- The complete, realistic, idempotent **demo seed** remains its own later milestone (M15, D12).

## Super Admin bootstrap (security review of A4)

Approved 2026-10-28 and recorded in A4 (bootstrap credential delivery):
- On first start, if no Super Admin has ever existed, the app creates one from the configured email and writes the one-time temporary password to a file on a private named volume (`/var/lib/accessflow/bootstrap-admin.txt`, mode 0600), never to stdout/logs.
- The log line says only: `initial Super Admin created; read the one-time password with: docker compose exec app cat /var/lib/accessflow/bootstrap-admin.txt`.
- The file is deleted after the first successful password change; the password stays single use with a 72-hour expiry (unchanged A4 rules). If it expires unused, the D2 `recover-admin` command issues a new one through the same file.
- `docker compose up` still needs no manual setup. Seeded demo users (M15) keep the documented local-only password from A4.
- README documents the local limitation: anyone with access to the Docker host, daemon or volume can read the file while it exists.

## Audit Explorer visibility (security review)

Approved 2026-10-28 and recorded under O7:
- **Super Admin and InfoSec:** company-wide.
- **Auditor:** company-wide, read-only Audit Explorer and evidence reports; the role grants no approval, fulfillment, administration or access-modification permission.
- **Manager:** current direct and indirect reports, with those employees' full history; former reports excluded.
- **Everyone else:** no Audit Explorer; their own data only via My Requests / My Access.
- Enforced in one backend scope function that adds an employee-ID filter to every audit, point-in-time and report query; tested at the API for each role.

---

## Layout

```
cmd/accessflow/            server + background work; `recover-admin` one-off command (D2)
cmd/mocktools/             Mock Tool Service
internal/platform/         config, DB pool, tx helper, embedded migrations, HTTP helpers, error mapping
internal/audit/            audit event writer + Audit Explorer queries and scope filter (leaf)
internal/notify/           in-app notifications + Mock Email Outbox (leaf)
internal/auth/             passwords, JWT cookie, CSRF, login/logout, middleware, bootstrap
internal/people/           employees, roles, admin groups, transfer, suspension, termination, rehire, succession
internal/catalog/          tools, access levels, approval-step configuration
internal/requests/         requests, approval steps, routing, SoD, override, reassignment
internal/access/           grants, operations, runner, Mock Tool client, tasks, direct grant, import, orphans, exceptions
internal/reviews/          campaigns, review items, findings
internal/sweeps/           periodic sweep loop (advisory lock)
internal/mocktools/        Mock Tool Service handlers + store
internal/webui/            embedded frontend build
internal/testutil/         template-DB cloning, fixture builders, in-process Mock Tool Service with fault injection
internal/devfixture/       small development fixture (M3), later superseded by the seed (M15)
web/                       React app
deploy/                    Postgres init script, test Dockerfile
compose.yaml, Dockerfile, README.md
```

Package dependencies stay acyclic: `audit`, `notify` are leaves; `people`, `requests`, `reviews` may call `access`; `access` never calls back (A8). A package typically holds `rules.go` (pure), `store.go` (SQL), `service.go` (transactions), `http.go` (handlers).

---

## Milestones

### M0 — Project setup

- **Objective:** clean repository with requirements, decisions, engineering instructions and this plan.
- **Deliverables:** `CASE.md`, `DECISIONS.md`, `CLAUDE.md`, `.gitignore`, `docs/plans/2026-10-28-implementation-plan.md`.
- **Commit/PR:** one initial commit directly on `main` (the only direct commit), after approval; push separately after approval.

### M1 — Walking skeleton

- **Objective:** `docker compose up` on a clean machine starts all services; health endpoint; migrations and append-only audit work; test harness and fixture builders run.
- **Dependencies:** M0; Superpowers and Ponytail loaded in this project.
- **Tasks:**
  1. Go module; `internal/platform`: env config, pgxpool, `InTx(ctx, func(tx Tx, now time.Time) error)` reading `now()` once, typed errors → HTTP mapping, JSON helpers.
  2. `deploy/postgres/init.sh`: `accessflow` (owner + runtime users) and `mocktools` databases, each user restricted to its database.
  3. Embedded goose migrations run at startup as the owner user. Migration 1: `audit_events` with runtime grants `INSERT, SELECT` only and a trigger blocking `UPDATE`/`DELETE`/`TRUNCATE`.
  4. `audit.Record(ctx, tx, Event)`.
  5. `cmd/mocktools` with `/healthz` and its own migrations.
  6. `web/` Vite + React + TS placeholder page; multi-stage `Dockerfile` (npm ci → go build → small runtime image) embedding the build; SPA fallback for non-API paths.
  7. `compose.yaml`: `postgres`, `app`, `mocktools`, health checks, `depends_on`; only 8080 published; named volumes; `tests` service under the `test` profile.
  8. `internal/testutil`: template database, per-test clone, runtime-user connection, first fixture builder skeleton.
- **Acceptance criteria:** clean clone → `docker compose up` → all services healthy, no manual steps; `GET /api/v1/health` 200; `/` and `/any/route` serve the page; `mocktools` not reachable from the host; test command passes.
- **Required tests:** runtime user cannot `UPDATE`/`DELETE`/`TRUNCATE` `audit_events`; audit event and state change roll back together; config defaults; SPA fallback does not shadow `/api/v1/*`.
- **UI slice:** placeholder only (no design yet).

### M2 — Authentication

- **Objective:** secure login, logout, password change and Super Admin bootstrap.
- **Dependencies:** M1.
- **Tasks:**
  1. Migrations: `employees` (status, manager, department, job title, `token_version`, lockout fields, version), password hashes, role and admin-group assignment intervals (A5, A9), `settings` (generated JWT secret).
  2. argon2id; temporary passwords (F4, A4).
  3. JWT cookie issue / re-issue / validate; CSRF header + Origin check; middleware loading status, version, roles and groups from DB each request; role guard helpers.
  4. Login (generic error, lockout), logout (`token_version`++), forced password change.
  5. Bootstrap Super Admin via the one-time file (above).
- **Acceptance criteria:** bootstrap Super Admin logs in with the file password, must change it, file is removed; all A4 rules hold; logins, failures, lockouts and password changes are audited.
- **Required tests:** non-HS256 token rejected; expired token rejected; re-issue keeps login time and respects 12 h; logout invalidates a copied token; suspended/terminated user denied with valid token; temporary-password token allows only password change and logout; 5 failures → 15-minute lock; CSRF missing/mismatched/foreign Origin rejected; bootstrap password never appears in logs.
- **UI slice:** login, forced password change, logout.

### M3 — Employees, roles and development fixture

- **Objective:** HR creates and edits employees; Super Admin manages roles and admin groups; password resets; a small dev fixture for manual testing.
- **Dependencies:** M2.
- **Tasks:**
  1. HR: create employee (temporary password shown once), edit manager / department / job title (job-title-only change → audit only; transfer effects come in M10).
  2. Super Admin: assign/remove roles (InfoSec, HR Manager, Auditor) and admin-group memberships as intervals, audited (D2 general rule).
  3. Password reset; privileged targets need a different Super Admin (F4).
  4. Employee directory API with role-based field visibility.
  5. `internal/devfixture`: a handful of employees, two InfoSec members, through real services; idempotent; seed-originated marker.
- **Acceptance criteria:** HR and Super Admin flows work and are audited; non-HR cannot create employees; role removal takes effect on the next request.
- **Required tests:** permission matrix per endpoint (Auditor role alone cannot approve, fulfil, administer or modify access); self privileged reset rejected; manager cycle rejected; dev fixture idempotent.
- **UI slice:** Employees (list, create, edit), Administration (roles, groups, password reset).

### M4 — Catalog and Mock Tool Service

- **Objective:** Super Admin configures tools and access levels with approval steps; the Mock Tool Service manages per-level permissions idempotently.
- **Dependencies:** M3.
- **Tasks:**
  1. Migrations: `tools` (fulfillment method, owner, admin group), `access_levels` (requires Tool Owner / InfoSec, active flag); `notifications` table in `notify`.
  2. Catalog API: create / edit / retire tools and levels; step changes audited and notified to InfoSec (F2); requester catalog listing with department starter packages (F3).
  3. Mock Tool Service: accounts and permissions per (tool, account, level); grant / revoke / suspend / resume keyed by operation ID (idempotent); status lookup; own DB.
  4. `access.ToolClient` interface, HTTP implementation (5 s timeout); fault-injection wrapper in `testutil`.
  5. Dev fixture gains tools for all three methods.
- **Acceptance criteria:** catalog changes audited; adding a step notifies InfoSec; repeated Mock Tool operation ID has no extra effect; revoking one level never removes another level or the account (D4).
- **Required tests:** only Super Admin edits catalog; retired level not requestable; Mock Tool idempotency and per-level isolation; client treats 5xx, timeout and malformed body as errors.
- **UI slice:** Catalog (tools, levels, approval steps).

### M5 — Requests and approvals

- **Objective:** an employee requests tool/levels with justification and duration; the right people approve or reject in order.
- **Dependencies:** M4.
- **Tasks:**
  1. Migrations: `requests` (submission ID unique), `request_items` (kind, duration, expiry, status, version), `approval_steps` (type, assignee or pool, routing note, decision, version).
  2. Pure rules: required steps and order, routing with fallback (F2), SoD exclusions, `CanDecide`, duration validation (D6), item and step transitions.
  3. Submit `NEW_ACCESS` batch; on-behalf by direct manager → `APPROVED_BY_SUBMISSION` (D3), others on-behalf with normal Manager step; one Mock Email Outbox entry per qualifying submission (O2); held/pending duplicate rejected or extension offered (D4).
  4. Approval Inbox with waiting time; approve/reject (reason required); first decision wins with audited rejected attempt (D9).
  5. Withdrawal before provisioning (O5); InfoSec/Super Admin reassignment with reason (D9); Super Admin override (D1); unroutable steps listed (feeds Exceptions in M6).
  6. Approved item ends in `APPROVED`; fulfillment starts in M6.
- **Acceptance criteria:** Jira Viewer needs only Manager; AWS Admin needs Manager → Tool Owner → InfoSec by three different people; fallback routing is visible; exactly one email per qualifying submission, none for on-behalf by manager.
- **Required tests:** routing table (manager terminated → manager's manager → InfoSec pool; Tool Owner is beneficiary → InfoSec; nobody eligible → unroutable); beneficiary cannot approve; one person cannot decide two steps; concurrent approvals (`-race`); repeated submission ID returns original; override cannot reverse a rejection; reassignment respects SoD; employee cannot read another's requests.
- **UI slice:** request form (batch, duration, on-behalf), My Requests, Approval Inbox (approve, reject, override, reassign).

### M6 — API fulfillment end to end, Exceptions

- **Objective:** first complete CASE flow: request → approvals → provisioned in the Mock Tool Service → `ACTIVE / VERIFIED` → removed and verified.
- **Dependencies:** M5.
- **Tasks:**
  1. Migrations: `grants` (status, method, evidence, `effective_from`/`effective_to`, version), `operations` (type, reason, priority, status, attempts, next attempt, lease, last error, version) with the A9 unique partial indexes.
  2. On final approval: stricter-wins check (F2), then grant + `GRANT` operation in one transaction.
  3. Runner: `SKIP LOCKED` claim with 30 s lease, priority (A6), HTTP outside transactions, status-lookup verification, retries, `FAILED`.
  4. Display status mapping (fulfillment table, O1); My Access API.
  5. Removal: voluntary (O6), manager revoke of direct reports (D5), safe removal against open operations.
  6. Exceptions API (query-based) with failed operations and unroutable approvals; manual retry.
- **Acceptance criteria:** the full flow runs against the real Mock Tool Service; a failing tool reaches `FAILED` after 5 attempts and appears in Exceptions; manual retry works.
- **Required tests:** full API scenario; backoff schedule; lease expiry re-claims; success response with wrong state is not success; termination-priority ordering; revoke while grant queued / in flight / failed; stricter-wins adds a step before provisioning.
- **UI slice:** My Access (status, expiry, give up access), manager's team access with revoke, Exceptions (failed ops, retry).

### M7 — Manual and Vendor fulfillment, direct grants

- **Objective:** Manual and Vendor tools fulfilled through admin-group tasks with self-reported evidence; Super Admin direct grant / revoke for all methods.
- **Dependencies:** M6.
- **Tasks:**
  1. Fulfillment Tasks per admin group (status, group, related request, time pending — O4).
  2. Record vendor ticket (`WAITING_VENDOR`), confirm (`ACTIVE / SELF_REPORTED`, confirming admin), report failure (`FAILED`), retry; same for revoke / suspend / resume tasks.
  3. `DIRECT_GRANT` (temporary ≤ 90 days) and direct revoke with justification; initiator cannot complete own task; notifications to InfoSec, manager, bypassed approver (D1).
- **Acceptance criteria:** Manual and Vendor rows of the fulfillment table behave exactly as specified; no Manual/Vendor result is `VERIFIED`.
- **Required tests:** non-member cannot act; beneficiary cannot fulfil own task; Vendor confirm without ticket rejected; direct grant > 90 days or for self rejected; initiator cannot confirm own direct task; repeated confirm idempotent.
- **UI slice:** Fulfillment Tasks; direct grant / revoke in Administration.

### M8 — Sweeps, expiry, extensions and level changes

- **Objective:** temporary access expires automatically; extensions and level changes follow the normal workflow.
- **Dependencies:** M7.
- **Tasks:**
  1. `sweeps` loop (~1/min, advisory lock, explicit `now`, idempotent).
  2. Expiry sweep → `REVOKE` reason `EXPIRED` (D6).
  3. `EXTENSION` and Temporary → Until Revoked re-running current steps.
  4. `LEVEL_CHANGE` (D4): new grant first; old-level revoke created in the transaction that activates the new one; failure paths; overlap exception.
- **Acceptance criteria:** D4 and D6 hold for all three methods; overlap visible in Exceptions until removal verified/confirmed.
- **Required tests:** expiry sweep twice → one operation; grant stays active during extension; new-level failure keeps old level; old-level revoke failure shows overlap; second level outside a level change rejected.
- **UI slice:** extend / change level actions in My Access; overlap in Exceptions.

### M9 — Termination and suspension

- **Objective:** HR suspends, reactivates and terminates (immediate or scheduled); access removal is prioritized and visible until confirmed — the "14 leavers" problem.
- **Dependencies:** M8.
- **Tasks:**
  1. Suspension (D7): login blocked, token invalidated, `SUSPEND` operations, pending requests cancelled, steps / tasks / reviews released or reassigned; reactivation with re-evaluation and `RESUME`.
  2. Termination immediate and scheduled (sweep), cancel / reschedule; overrides pending provisioning and resume; security-revocation exception.
- **Acceptance criteria:** all D7 effects happen in one transaction per action; terminated employees' removals run first and stay in Exceptions until confirmed.
- **Required tests:** terminate during queued / in-flight grant; scheduled termination after downtime runs once; expiry during suspension; reactivation skips expired / revoked grants; suspended user's token rejected.
- **UI slice:** suspend / reactivate / terminate / schedule actions on Employees; security revocations in Exceptions.

### M10 — Transfer and rehire

- **Objective:** mover workflow (D5) and rehire (A5).
- **Dependencies:** M9.
- **Tasks:** transfer review with all access pre-selected for revocation, Revoke All / Keep All, keep requires justification (`RETAINED_ON_TRANSFER`, new manager notified), pending requests reassessed, stale-grant incident; rehire on the same record with a new temporary password and nothing restored.
- **Acceptance criteria:** D5 and A5 rules covered by tests; stale grant after transfer revoked and visible.
- **Required tests:** keep without justification rejected; Manager step moves to new manager; requests submitted before transfer cancelled on Revoke All; stale grant incident; rehire restores nothing.
- **UI slice:** transfer screen on Employees; rehire action.

### M11 — Quarterly access reviews

- **Objective:** campaigns every quarter (and manually); Tool Owners decide Keep / Revoke; non-responders chased per D8.
- **Dependencies:** M10.
- **Tasks:**
  1. Migrations: `review_campaigns` (one per quarter), `review_items`, day-step markers.
  2. Campaign sweep + manual start; snapshot of active and suspended grants; own-access reviewer routing.
  3. Keep / Revoke, bulk Keep with exclusions, Super Admin Revoke-only, late decisions.
  4. Day 7 reminder, day 14 escalation to InfoSec, day 21 auto-revoke (`REVOKED_NO_REVIEW`) or open finding; carried-over findings; closed as already removed.
  5. "Approved under earlier policy" flag from F2 step additions.
- **Acceptance criteria:** every D8 rule covered by a test; sweeps idempotent across repeats and downtime.
- **Required tests:** each day step exactly once; day 21 uses configuration at campaign start; open finding not re-added; Super Admin Keep rejected; bulk Keep skips flagged items.
- **UI slice:** Reviews (campaigns, my items, bulk Keep, findings); open findings in Exceptions.

### M12 — Baseline import and orphan accounts

- **Objective:** existing access imported and certified (D10).
- **Dependencies:** M11.
- **Tasks:** import from Mock Tool Service state (HTTP) and CSV for Manual / Vendor; case-insensitive email matching; terminated owner → immediate revoke; unmatched → `ORPHAN_ACCOUNT` finding with revoke / dismiss; import report; certification campaign start.
- **Acceptance criteria:** importing twice changes nothing; unknown tool / level rows listed; no invented approvals or activation dates.
- **Required tests:** idempotent import; terminated-employee account revoked; orphan revoke and dismiss; malformed CSV rejected safely.
- **UI slice:** Imports (upload, run, report); orphan actions in Exceptions.

### M13 — Proof: Audit Explorer and evidence

- **Objective:** Audit sees who had access to what on any date and who approved it (CASE §5).
- **Dependencies:** M12.
- **Tasks:** audit scope function (above); event search; point-in-time effective access with O1 states and evidence labels; approval chain per grant; privileged roles report for any date (D11); overrides and direct grants report (D1); quarterly evidence package (campaign results, carried-over findings, policy changes); Exceptions completeness check against all O7 kinds.
- **Acceptance criteria:** for any past timestamp, the access list matches effective intervals, including requested / failed removals as held; each role sees exactly its scope.
- **Required tests:** point-in-time at activation and removal instants; verified vs self-reported labels; privileged roles at a past date; Manager sees current reports' full history but not former reports or others; Auditor read-only company-wide; employee without role gets `403`.
- **UI slice:** Audit Explorer (search, point-in-time, reports, evidence export view).

### M14 — Super Admin succession and settings

- **Objective:** D2 promotion, recovery mode, `recover-admin`, admin settings.
- **Dependencies:** M13.
- **Tasks:** two-person promotion; nomination expiry and cancellation; `ADMIN_RECOVERY_REQUIRED` on last Super Admin termination (blocks only Super Admin configuration); HR nominates, InfoSec authorizes; `recover-admin` only with no active Super Admin (writes the one-time file); health warning below two Super Admins.
- **Required tests:** nominee cannot authorize; concurrent authorizations consistent; recovery command refused while a Super Admin is active; access workflows continue in recovery mode.
- **UI slice:** Administration → Super Admin (promotion, nominations, recovery, warning).

### M15 — Demo seed

- **Objective:** realistic, small, idempotent seed loaded by `docker compose up` (D12, D10), replacing the dev fixture.
- **Dependencies:** M14.
- **Tasks:** departments, employees (≥ 2 InfoSec), tools for all three methods with varied levels, requests in various states through the real services at real time, baseline import with a terminated employee's account and an orphan; seed marker; shared local-only password.
- **Acceptance criteria:** fresh start shows meaningful data on every screen; second start adds nothing; no fabricated timestamps.
- **Required tests:** seed idempotency; all seeded audit timestamps ≥ seed start time.

### M16 — Delivery

- **Scope:** README (setup, architecture, tests, known limitations including the bootstrap-file host-access limitation); one-page operating summary (weekly run, rules, assumptions); 30-day rollout plan (including formal privileged-role review, D11); browser smoke test of critical journeys; Ponytail audit of the codebase; clean-machine `docker compose up` verification; final CASE.md deliverable check.

### U1 — UI foundation (inserted when `design/` arrives)

- **Objective:** apply the design system and bring the UI up to the backend's current milestone.
- **Tasks:** review `design/`; choose and add the single UI library (A7; reported for approval as a dependency); app shell and role-based navigation; fetch wrapper (CSRF, 401 → login, 403 / validation errors); shared status-badge mapping (self-reported always labelled); company-timezone timestamps; catch-up UI slices of completed milestones.
- **Required tests:** fetch wrapper; status mapping; key forms; buttons disabled while in flight.

---

## Branch and PR grouping

Each milestone is still approved, implemented, reviewed and committed separately; related milestones share a short-lived branch and one PR, squash-merged into one commit on `main`.

| PR | Branch | Milestones | Squash commit title |
|---|---|---|---|
| — | `main` | M0 | `chore: initialize AccessFlow project …` |
| 1 | `feat/foundation` | M1 | `feat: walking skeleton with compose, migrations and append-only audit` |
| 2 | `feat/identity` | M2, M3 | `feat: authentication, employees and roles` |
| 3 | `feat/catalog-requests` | M4, M5 | `feat: tool catalog, mock tool service, requests and approvals` |
| 4 | `feat/fulfillment` | M6, M7 | `feat: API, manual and vendor fulfillment with exceptions` |
| 5 | `feat/lifecycle` | M8, M9, M10 | `feat: expiry, level changes and joiner-mover-leaver lifecycle` |
| 6 | `feat/reviews-import` | M11, M12 | `feat: quarterly reviews, baseline import and orphan accounts` |
| 7 | `feat/proof-admin` | M13, M14 | `feat: audit explorer, evidence and super admin succession` |
| 8 | `feat/seed-delivery` | M15, M16 | `feat: demo seed and delivery documentation` |
| — | `feat/ui-foundation` | U1 | `feat: UI foundation from design` (own PR, merged when ready) |

If a PR becomes large to review, it is split at a milestone boundary (with approval).

---

## Requirement coverage

| Requirement | Milestones |
|---|---|
| CASE §1 Requests (tool, why, how long) | M5 (D6 durations, D3 on-behalf, F3), M8 (extension) |
| CASE §2 Approvals by tool/sensitivity | M4 (F2 config), M5 (F2 routing, SoD, D1 override, D9), M6 (stricter wins) |
| CASE §3 Granting (three admin methods) | M4 (Mock Tool Service), M6 (API), M7 (Manual, Vendor, D1 direct grant) |
| CASE §3 Removal on expiry | M8 (D6) |
| CASE §3 Removal on team change | M10 (D5) |
| CASE §3 Removal on leaving | M9 (D7), M12 (leavers' existing accounts) |
| CASE §4 Quarterly reviews, chasing owners | M11 (D8) |
| CASE §5 Proof: who had access on any date, who approved | M1 (audit), M13 (O1 point-in-time, D11, evidence) |
| CASE Stack: Go, React, DB, `docker compose up` | M1, U1, M15 |
| CASE Deliverables: README, one-pager, 30-day plan | M16 (recording made by the user) |
| F1 access unit, F2 approval steps, F3 no eligibility, F4 credentials | M4; M4–M6, M11; M4–M5; M2–M3 |
| D1 override / direct grant | M5, M7, M13 (report) |
| D2 succession | M3 (role assignment), M14 |
| D3 on-behalf · D4 one level per tool · D5 mover · D6 duration | M5 · M4, M5, M8 · M10 · M5, M8 |
| D7 termination / suspension · D8 reviews · D9 reassignment | M9 · M11 · M5 |
| D10 import · D11 privileged roles report · D12 setup / seed / time | M12 · M13 · M1, M3, M15 |
| Fulfillment methods + item status table | M6, M7 |
| O1 effective access · O2 mock email · O3 retries · O4 tasks | M6, M13 · M5 · M6 · M7 |
| O5 withdrawal / rejection · O6 voluntary revocation | M5 · M6 |
| O7 screens (+ clarification) · O8 background processing | each UI slice + U1 · M6 (runner), M8, M9, M11 (sweeps) |
| A1–A3 architecture, compose, libraries | M1 |
| A4 authentication · A5 identity history | M2 · M3, M9, M10 |
| A6 runner and sweeps · A7 frontend · A8 layout | M6, M8 · U1 and slices · all |
| A9 integrity · A10 testing | all (indexes in M5, M6; evidence in M6, M7) · all |

## Simplicity review

Applied the Ponytail review process manually (the skill is not loaded in this session):
- No generic workflow engine, job queue, event bus or notification service: one `operations` table, one sweep loop, two small tables for notifications and the outbox.
- Exceptions are queries over existing state, not a stored table.
- No OpenAPI generation, no repository interfaces; one interface (`ToolClient`) because tests need fault injection.
- Fixtures and seed use the real services, so they cannot drift from the rules or fabricate history.
- Audit scope is one backend filter function reused by every audit and report query.
- Oversized milestones split: identity (M2 / M3), catalog vs requests (M4 / M5), lifecycle (M8 / M9 / M10).
- No functionality deferred; ordering puts the first complete flow at M6 and the security-critical leaver removal at M9.
