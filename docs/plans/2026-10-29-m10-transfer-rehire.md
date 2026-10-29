# M10 — Transfer and Rehire: Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Status:** draft 2026-10-29, revised after the independent Ponytail plan review (findings 1–11 incorporated); decision 1 and the plan approved 2026-10-29. Nothing is implemented. `design/` stays untouched and out of every commit. Branch `feat/transfer-rehire` from `main` (`91cad9b`); a new PR.

**Goal:** a department or manager change is a transfer. HR reviews the person's open access, every item revoked unless kept with a written justification. Pending requests follow the new manager. Access completing after HR chose revocation is removed and shown as an incident. A terminated employee is rehired on the same record with a new temporary password and nothing restored.

**Architecture:**
- **Transfer** is one more person-level action (`requests/transfer.go`). It uses M9's `personAction`: lock order, actor and version checks, one transaction for every effect.
- **Revocation by default:** the API takes only the grants to **keep**. One access function, `RemoveExcept`, locks the person's grants, validates the keep list and removes every other reviewable grant through the existing `requestRemoval` (reason `TRANSFER`, priority 1). A grant that appears between HR's review and the submit is therefore revoked, never kept silently.
- **Kept items:** three columns on `grants` (`retained_at`, `retained_by`, `retention_justification`) hold the latest retention. History is in the `EMPLOYEE_TRANSFERRED` audit details (A9), as for every other employee change. The new manager is notified in-app, and M11 reads the columns.
- **Stale grant:** the transfer's removal of a grant still in flight waits for the `GRANT` (`waitsForEarlier`). When that `GRANT` activates on a grant whose open removal has reason `TRANSFER`, `succeed` stamps `grants.stale_at` and audits `STALE_GRANT_INCIDENT`. Exceptions lists it until the removal is verified or confirmed. The removal itself is already queued; nothing new runs.
- **Rehire** is a status change `TERMINATED → ACTIVE` in `people`, under `LifecycleLockKey` like every status change, with a new temporary password through the existing reset code.
- No new dependencies, tables, background jobs or emails (O2, O8). The UI slice moves to U1's catch-up, as for M9.

**Tech Stack:** Go, chi, pgx, goose, PostgreSQL 18, as in M6–M9.

**Spec:**
- `CASE.md` §3 ("removed … when the person changes team"; joiners, movers and leavers).
- `DECISIONS.md`:
  - D5 (mover); A5 (identity, rehire); D7 (rehire is an explicit process with fresh requests);
  - F4 (employee administration, `self_change_forbidden`, terminated employees not editable);
  - F2 (routing), O1 (effective access), O2 (in-app notifications only), O7 (Exceptions: "D5 stale grant"), A9;
  - D8 bulk-Keep exclusion of retained items (consumed in M11).
- Main plan, M10 (tasks, acceptance criteria, required tests).

## Global Constraints

- Everything in M6–M9 applies:
  - lock first, then read in a new statement;
  - a state change and its audit event in one transaction;
  - real time only (D12);
  - parameterized SQL;
  - stable error codes, no display text.
- **Lock order for a transfer:** as every person-level action (M9), in this order. The steps in Task 3 follow it.
  - `LifecycleLockKey`;
  - the pending items naming the person, `ORDER BY id`;
  - their beneficiaries (key share);
  - the person;
  - their operations and grants (`RemoveExcept`);
  - then, shared, the new manager (`checkManager`, `FOR SHARE`) and the people steps are re-routed to.
- **Manager changes happen only under `LifecycleLockKey`** (transfer and rehire). That serializes the cycle check, so the separate `accessflow manager tree` advisory lock in `UpdateEmployee` is removed with the manager change it guarded. A comment on `people.SetPlacement` says any future manager-changing path must hold `LifecycleLockKey`.
- **Who acts (as M9 decision 3):**
  - HR Managers and Super Admins act;
  - a target holding Super Admin or InfoSec needs a Super Admin (`403 privileged_reset_requires_super_admin`);
  - nobody acts on themselves (`403 self_change_forbidden`).
- **Error codes:** reuse `409 version_conflict`, `409 employee_status` and the M3 `manager_*` codes. New codes:
  - `409 transfer_required`;
  - `400 not_a_transfer`;
  - `400 justification_required` (with `field`);
  - `400 grant_not_open`;
  - `400 duplicate_grant`.
- **Every policy call below is recorded in DECISIONS.md** as a dated refinement under D5, A5 and D7 (Task 6), as in M8 and M9.

## Decision (approved 2026-10-29)

1. **Pending items whose Manager step the previous manager already approved.** When the manager changes, they are cancelled ("transferred"); the employee submits a new request for the new manager. A Manager step still `PENDING` is re-routed to the new manager; one still `WAITING` opens with the new manager anyway. A department-only transfer re-routes no Manager step. Recorded in DECISIONS.md D5 (refinement, M10).
   - Rejected: re-opening the decided step (rewrites history, A9); keeping the old approval (unreviewed access could activate).
2. **Rehire of a former manager (approved 2026-10-29, after the code review).** A rehire is refused (`409 has_direct_reports`) while anyone not terminated still reports to the person; HR transfers those reports first. Otherwise the rehired manager would silently take back their former reports' Manager steps, team view and revoke rights. Recorded in DECISIONS.md A5 (refinement, M10 review). Test: `TestRehireRefusedWithDirectReports`.
3. **A transfer with nothing to review (approved 2026-10-29, after the code review).** "Revoke everything" (cancel every pending request) applies only when the transfer removed at least one grant. A person with no reviewable grants keeps their requests, which follow call 4's manager rules (re-route a pending Manager step, cancel what the previous manager approved; nothing on a department-only transfer). This amends call 4. Recorded in DECISIONS.md D5 (refinement, M10 review). Test: `TestTransferWithoutGrantsKeepsRequests`.

## My calls (approved with the plan unless you object)

1. **API shape.**
   - `POST /api/v1/employees/{id}/transfer` takes `{version, managerId, department, jobTitle, reason, keep: [{grantId, justification}]}`. All fields are required except `keep`, which defaults to `[]`; `null` clears a field, as in `PUT`.
     - *Amended 2026-10-29 (implementation):* `keep` is required too, so a forgotten key never revokes everything; an explicit `null` keeps nothing.
   - `PUT /api/v1/employees/{id}` keeps job-title changes (audit only, D5). A changed `managerId` or `department` there is `409 transfer_required`.
   - A transfer whose manager and department are both unchanged is `400 not_a_transfer`.
2. **What HR reviews — one predicate for listing and keeping.**
   - A **reviewable grant** is one of the person's grants in `PROVISIONING`, `PENDING_FULFILLMENT`, `ACTIVE` or `FAILED` (*amended 2026-10-29 (implementation):* and `SUSPENDED` while its resume is on its way, so a transfer never leaves it undecided; recorded in DECISIONS.md D5). That is "all active access", plus grants still in flight, so HR decides on those too (D5 stale grant). Grants already being removed (`REVOCATION_REQUESTED`, `REVOCATION_FAILED`) are not reviewable; their removal continues.
   - `GET /api/v1/employees/{id}/grants` (HR Manager, Super Admin) lists exactly the reviewable grants, with `retainedOnTransfer`. Pre-selection for revocation is the API default; Revoke All / Keep All are UI shortcuts over `keep`.
   - A `keep` entry must name a reviewable grant (`400 grant_not_open`), at most once (`400 duplicate_grant`), with a justification of 1–1000 characters (`400 justification_required`, `field: keep[i].justification`).
   - The list inherits the 200-row shortcut of the grants query. Grants beyond it are revoked by default; this is documented in the README.
3. **Who is transferable:** `ACTIVE` employees only, else `409 employee_status`. A suspended person is reactivated first, so their grants are in a settled state when HR decides.
4. **Pending requests (D5), only when the manager changes:**
   - **`keep` is empty ("HR revokes everything"):** the person's pending items (as beneficiary) are cancelled ("transferred"), as at suspension. Nothing is re-routed.
   - **Otherwise:**
     - the person's `PENDING` Manager steps are re-routed through F2 routing to the new manager (`STEP_REROUTED`, reason "transfer");
     - items whose Manager step is already decided are handled per the decision above;
     - other items stay pending.
   - A department-only transfer changes no item, unless `keep` is empty: then the D5 default still cancels them.
   - A pending extension or level change of a revoked grant is cancelled by `requestRemoval`, as for any closed grant (M8).
5. **Kept items:**
   - `grants.retained_at`, `retained_by` and `retention_justification` are set; a later transfer overwrites them. The full history is in each `EMPLOYEE_TRANSFERRED` event's details (kept grants with their justifications, and revoked grants).
   - The grant keeps its status and expiry. `Grant.retainedOnTransfer` is `{at, justification}`.
   - One in-app notification `GRANTS_RETAINED_ON_TRANSFER` goes to the new manager, listing the kept grants. There is none when nothing is kept or there is no new manager. There is no email (O2).
6. **Stale grant (D5, O7):**
   - The removal of a grant still in flight is queued at the transfer. It waits for an API call in flight, and a claimed Manual or Vendor task finishes first (the M7 rule; the M9 cancellation is for termination only).
   - When that `GRANT` activates, `succeed` sets `grants.stale_at` and records `STALE_GRANT_INCIDENT` if the grant's open `REVOKE` has reason `TRANSFER`. A stored column, not a time comparison: `created_at` is the transaction start (the M9 ordering lesson).
   - The removal then runs as already queued, which is "revoked immediately".
   - Exceptions gains `staleGrants`: grants with `stale_at` set whose removal is not yet verified or confirmed.
7. **Rehire (A5, D7):**
   - `POST /api/v1/employees/{id}/rehire` takes `{version, reason, managerId, department, jobTitle}` and returns `200 {employee, temporaryPassword}`. It needs a `TERMINATED` employee, else `409 employee_status`.
   - Like `personAction`, it re-checks under `LifecycleLockKey` that the actor is still `ACTIVE`.
   - It reuses the M3 manager checks, the M3 temporary password (72 hours, forced change) and `SetStatus`. It bumps the token version and clears a lockout, then audits `EMPLOYEE_REHIRED`.
   - Nothing is restored:
     - no grant, role or admin group membership;
     - no cancelled request;
     - a schedule was already cleared at termination.
   - **Leaver removals still open at rehire keep running.** `securityRevocations` switches from "employee is `TERMINATED`" to "priority 0". Priority 0 is set only for termination removals (`requestRemoval` with `TERMINATION`, and `RemoveAll`'s raise), so a rehire cannot hide them. A comment on `SecurityRevocations` says no other removal may use priority 0.
8. **Manager and department history** stays in the audit events (`EMPLOYEE_TRANSFERRED` with from/to, kept and revoked grants). There is no transfer table.

## Review Focus

1. **An item approved by the old manager that activates after the transfer:**
   - an in-flight grant ends removed and listed in `staleGrants` (Task 4);
   - a still-pending item follows the decision above (Task 3).
2. **A grant created after HR opened the review:** it is not in `keep`, so it is revoked. Test in Task 3.
3. **The old manager deciding a pending Manager step while the transfer commits:** the decision commits first (then it is a decided step, per the decision above), or it waits and is refused (`403`, no longer the assignee). It is never recorded after the transfer. Concurrency test in Task 3.
4. **A rehire while a leaver's removal is still open:** the removal keeps running and stays in `securityRevocations`, and nothing is restored. Test in Task 5.
5. **A department-only transfer, a "transfer" that changes nothing, or a job-title-only change:**
   - a department-only transfer re-routes nothing (step versions unchanged);
   - an unchanged transfer is `400 not_a_transfer`;
   - a job-title-only change through `PUT` is audit only.

   Tests in Task 2 and Task 3.

---

### Task 1: Schema and pure rules

**Files:**
- Create: `internal/platform/migrations/00010_transfer_rehire.sql`
- Modify: `internal/access/rules.go`, `internal/access/rules_test.go`

**Interfaces:**
- Produces:
  - columns `grants.retained_at timestamptz`, `grants.retained_by uuid REFERENCES employees`, `grants.retention_justification text CHECK (btrim(retention_justification) <> '')`, with a CHECK that all three are null together, and `grants.stale_at timestamptz`;
  - `type Keep struct { GrantID uuid.UUID; Justification string }` (in `access`);
  - `func checkKeeps(reviewable []uuid.UUID, keep []Keep) (map[uuid.UUID]string, error)`: kept grant → trimmed justification.

- [ ] **Step 1: Write the failing test** `TestCheckKeeps`, table-driven:
  - empty keep → empty map;
  - a valid entry → `{g1: "shared on-call rota"}` (trimmed);
  - a blank justification → `justification_required` with field `keep[0].justification`;
  - more than 1000 characters → `justification_required`;
  - a grant not reviewable → `grant_not_open`;
  - the same grant twice → `duplicate_grant`.
- [ ] **Step 2: Run it and see it fail.** `docker compose --profile test run --rm tests sh -c "go test -race -count=1 -run TestCheckKeeps ./internal/access/"`
- [ ] **Step 3: Implement** the migration (in the style of `00009`) and `checkKeeps`.
- [ ] **Step 4: Run it and see it pass.**

### Task 2: `PUT` is job title only; HR sees a person's reviewable grants

**Files:**
- Modify:
  - `internal/people/service.go`: `UpdateEmployee`;
  - `internal/people/update_test.go`, `internal/people/manager_test.go`: manager-change and cycle tests move to Task 3;
  - `internal/access/http.go`, `internal/access/grants.go`.
- Test: `internal/people/update_test.go`, `internal/requests/transfer_test.go` (new)

**Interfaces:**
- Produces:
  - `people.CheckNewManager(ctx, tx, id, manager uuid.UUID) error` (exported `checkNewManager`);
  - `people.SetPlacement(ctx, tx, id uuid.UUID, manager *uuid.UUID, department, jobTitle *string) error`;
  - `access.reviewable` — the SQL predicate `g.status IN ('PROVISIONING','PENDING_FULFILLMENT','ACTIVE','FAILED')`, used by both the list and `RemoveExcept`;
  - `GET /employees/{id}/grants` → `{grants: []Grant}`;
  - `Grant.RetainedOnTransfer *Retention` with `json:"retainedOnTransfer"`, where `type Retention struct { At time.Time; Justification string }`.

- [ ] **Step 1: Write the failing tests.**
  - `TestUpdateRejectsTransfer`: `PUT` with a new manager → `409 transfer_required`; with a new department → `409 transfer_required`; nothing changes.
  - `TestUpdateJobTitleOnly`: `200`, one `EMPLOYEE_UPDATED` event, and no grant or request is touched.
  - `TestEmployeeGrants`:
    - HR sees dev's reviewable grants, not their revoked ones or one being removed;
    - a plain employee, or the target themself, gets `403`;
    - an unknown id gets `404`.
- [ ] **Step 2: Run them and see them fail.**
- [ ] **Step 3: Implement.**
  - `UpdateEmployee` compares manager and department with the locked row and returns `errTransferRequired` on a change. It drops the `accessflow manager tree` lock.
  - Move the existing manager tests: cycle, inactive manager, self as manager.
  - The route uses `s.grants(ctx, "g.employee_id = $1 AND "+reviewable, id)`, behind `HasRole(SUPER_ADMIN, HR_MANAGER)`. `retainedOnTransfer` is read from the new columns.
- [ ] **Step 4: Run the `people` and `access` packages and see them pass.**

### Task 3: Transfer revokes by default and keeps with justification

**Files:**
- Create: `internal/requests/transfer.go`, `internal/requests/transfer_test.go`
- Modify:
  - `internal/requests/http.go`: route;
  - `internal/requests/lifecycle.go`: pull the per-step body of `endFor` (route, `UPDATE`, audit) into `rerouteStep(ctx, tx, it, step, reason)`, used by `endFor` and by the transfer; split out `cancelOwn`;
  - `internal/access/suspension.go`: `RemoveExcept`.

**Interfaces:**
- Consumes: `checkKeeps`, `reviewable`, `people.CheckNewManager`, `people.SetPlacement` (Tasks 1–2), M9's `personAction`.
- Produces:
  - `func (s *Service) Transfer(ctx, actor auth.Principal, id uuid.UUID, in TransferInput) (people.Employee, error)`;
  - `type TransferInput struct { Version int; ManagerID *uuid.UUID; Department, JobTitle *string; Reason string; Keep []access.Keep }`;
  - `func RemoveExcept(ctx, tx, now, actor *uuid.UUID, employee uuid.UUID, keep []Keep) (kept map[uuid.UUID]string, revoked []uuid.UUID, err error)`. It calls `lockEmployeeGrants`, reads the reviewable ids, then calls `checkKeeps`. It sets the retention columns on the kept grants and calls `requestRemoval(..., "TRANSFER", "transferred")` on the rest.

- [ ] **Step 1: Write the failing tests** (real PostgreSQL, mock tool and runner, as `lifecycle_test.go`).
  - `TestTransferRevokesByDefault`. dev holds Jira (API, `ACTIVE`) and Figma (Manual, `ACTIVE`); HR transfers dev to a new manager with `keep: []`:
    - `200`, the new manager and department;
    - both grants `REVOCATION_REQUESTED` with a `REVOKE` of reason `TRANSFER` and priority 1;
    - after the runner and the Figma removal task, both are `REVOKED`;
    - one `EMPLOYEE_TRANSFERRED` event lists both as revoked.
  - `TestTransferKeepNeedsJustification`: a keep with `""` → `400 justification_required`; nothing changes (status, manager, version).
  - `TestTransferKeeps`. Keep Jira with "shared on-call rota":
    - Jira stays `ACTIVE` with its expiry;
    - `retainedOnTransfer.justification` is the text, and the event details carry it;
    - the new manager has one `GRANTS_RETAINED_ON_TRANSFER` notification naming Jira;
    - Figma is revoked.
  - `TestTransferRevokesGrantCreatedAfterReview`: read `GET /employees/{id}/grants`, then a new grant is approved, then the transfer keeps only the grants read. The new grant is revoked.
  - `TestTransferManagerStepMovesToNewManager`, with one grant kept:
    - dev's item with a `PENDING` Manager step assigned to mgr is re-assigned to the new manager (`STEP_REROUTED`), and stays pending;
    - an item whose Manager step mgr already approved is `CANCELLED` ("transferred").
  - `TestDepartmentOnlyTransferKeepsSteps`: same manager, new department, one grant kept. No `STEP_REROUTED`, step versions unchanged, items pending.
  - `TestTransferRevokeAllCancelsPendingRequests`: `keep: []` → dev's pending items are `CANCELLED` ("transferred"), and there is no `STEP_REROUTED`.
  - `TestTransferWhileOldManagerApproves`, in the style of `TestApproveWhileApproverIsSuspended`. The approval holds the item, then the transfer commits. Afterwards there is either an approval before the transfer, or the approval fails with `403`. No `STEP_APPROVED` by the old manager is recorded after `EMPLOYEE_TRANSFERRED` (compare by uuidv7 event id).
  - `TestTransferRules`:
    - `not_a_transfer`, `employee_status` (suspended), `self_change_forbidden`, `privileged_reset_requires_super_admin`, `version_conflict`;
    - manager cycle and inactive manager → the M3 codes;
    - `grant_not_open` for a grant being removed;
    - a plain employee → `403`.
- [ ] **Step 2: Run them and see them fail.**
- [ ] **Step 3: Implement** `Transfer` with `personAction(..., from: ["ACTIVE"])`. In the transaction, in lock order:
  1. the not-a-transfer check;
  2. `RemoveExcept`;
  3. `CheckNewManager` when the manager changes, then `SetPlacement`;
  4. if `keep` is empty, `cancelOwn`; else, if the manager changed, re-route `PENDING` Manager steps with `rerouteStep` and apply the decision above to decided ones;
  5. the notification and the `EMPLOYEE_TRANSFERRED` audit.
- [ ] **Step 4: Run them and see them pass**, then the `requests` package.

### Task 4: Stale grant incident

**Files:**
- Modify:
  - `internal/access/runner.go`: `succeed`;
  - `internal/access/operations.go`: `StaleGrants`;
  - `internal/requests/decide.go`: the Exceptions payload.
- Test: `internal/requests/transfer_test.go`

**Interfaces:**
- Produces:
  - `func StaleGrants(ctx, pool) ([]Operation, error)`: the open `REVOKE` of every grant with `stale_at` set;
  - `Exceptions.StaleGrants []access.Operation` with `json:"staleGrants"`.

- [ ] **Step 1: Write the failing tests.**
  - `TestStaleGrantAfterTransfer`, two subtests:
    - **API:** the approved `GRANT` is in flight (`inFlight`), then HR transfers with `keep: []`, then the call finishes. Assertions:
      - the grant activated with `effective_from` set;
      - `stale_at` is set, and there is one `STALE_GRANT_INCIDENT`;
      - it is listed in `staleGrants` until the runner verifies the removal;
      - the final grant is `REVOKED / VERIFIED`, and the tool no longer holds it.
    - **Manual:** a claimed `GRANT` task is confirmed after the transfer. It is listed until the removal task is confirmed.
  - `TestNoIncidentWithoutTransfer`: an ordinary revoke during an in-flight `GRANT` sets no `stale_at`.
- [ ] **Step 2: Run them and see them fail.**
- [ ] **Step 3: Implement.** In `succeed`'s `GRANT` branch, when `grantStatus` is `REVOCATION_REQUESTED` or `REVOCATION_FAILED` and the grant's open `REVOKE` has reason `TRANSFER`: set `stale_at = now` and record `STALE_GRANT_INCIDENT` (entity grant, subject employee). Then wire `staleGrants` into Exceptions for InfoSec, Super Admin and Auditor, as the M9 lists.
- [ ] **Step 4: Run them and see them pass.**

### Task 5: Rehire

**Files:**
- Modify:
  - `internal/people/lifecycle.go`: `Rehire`;
  - `internal/people/service.go`: extract `issueTemporaryPassword(ctx, tx, now, id, hash)` from `ResetPassword`;
  - `internal/people/http.go`;
  - `internal/access/operations.go`: the `SecurityRevocations` filter and its comment.
- Test: `internal/people/rehire_test.go`, `internal/requests/lifecycle_test.go`

**Interfaces:**
- Produces:
  - `func (s *Service) Rehire(ctx, actor auth.Principal, id uuid.UUID, in RehireInput) (Employee, string, error)`;
  - `type RehireInput struct { Version int; ManagerID *uuid.UUID; Department, JobTitle *string; Reason string }`.

- [ ] **Step 1: Write the failing tests.**
  - `TestRehireRestoresNothing`. dev held two grants, a role and a group membership, and was terminated, with every removal done:
    - rehire → `200` `ACTIVE`, the same id, the version + 1, a temporary password that signs in with `mustChangePassword`;
    - no grant is open, no role or membership is open, and their old cancelled items stay cancelled;
    - one `EMPLOYEE_REHIRED` event;
    - a new request goes through the normal approval.
  - `TestRehireRules`:
    - an `ACTIVE` employee → `409 employee_status`;
    - a plain employee → `403`;
    - a manager cycle (one of their old reports as manager) → `manager_cycle`;
    - a stale version → `409 version_conflict`;
    - an actor suspended while the rehire waited for the lifecycle lock → `401` (as `TestActorSuspendedWhileActing`).
  - `TestRehireKeepsLeaverRemovalsVisible`:
    - rehire while a termination removal task is open: the removal stays in `securityRevocations` and still completes; the grant is never restored;
    - an ordinary (priority 1) removal of an `ACTIVE` employee is not listed there.
- [ ] **Step 2: Run them and see them fail.**
- [ ] **Step 3: Implement.** `Rehire` in a transaction:
  1. take `LockLifecycle` and re-check the actor's status, then `LockEmployee`;
  2. check the status and version, and `CheckNewManager` when a manager is set;
  3. call `SetPlacement`, `SetStatus(ACTIVE)` and `issueTemporaryPassword`;
  4. bump the token version, clear the lockout, and audit `EMPLOYEE_REHIRED`.

  `SecurityRevocations` filters on `o.priority = 0` instead of `e.status = 'TERMINATED'`.
- [ ] **Step 4: Run them and see them pass.**

### Task 6: Decisions, documentation, journey, review

**Files:** `DECISIONS.md`, `README.md`, `docs/plans/2026-10-28-implementation-plan.md` (M10 status), this plan.

- [ ] **Step 1: DECISIONS.md.** Add dated refinements, "[APPROVED date] (M10)":
  - under D5 (decision 1 is already recorded): what HR reviews, `ACTIVE`-only transfers, "revoke everything" = nothing kept, the retention columns, and the stale-grant rule;
  - under A5 and D7: rehire, and leaver removals that keep running and stay visible after a rehire.
- [ ] **Step 2: README.**
  - The API rows: transfer, the employee's grants, rehire, `PUT` job title only, `staleGrants`.
  - A transfer and rehire rules section.
  - Limitations:
    - a transfer revokes a grant created after HR's review, and any beyond the first 200 listed;
    - a claimed Manual or Vendor grant task finishes before a transfer removal, then counts as a stale grant;
    - replacing a departed manager is a transfer for each report: revoke by default, or keep with justifications; a suspended report must be reactivated first;
    - the M9 deadlock cases also apply to transfers.
- [ ] **Step 3: Full suite.** `docker compose --profile test run --rm tests` passes (gofmt, go vet, `go test -race`).
- [ ] **Step 4: Docker journey** in the throwaway `accessflow-m2check` project:
  - dev1 holds Jira and GitHub;
  - HR transfers dev1 keeping Jira: the new manager is notified, and the GitHub task removes the access;
  - a stale grant via a claimed task appears in Exceptions until removed;
  - HR terminates, then rehires dev1: a new password, nothing restored.
- [ ] **Step 5: Reviews.** Superpowers code review and Ponytail review of the full diff; fix the findings test-first.

## Out of scope

- Quarterly reviews of retained items (M11 reads the retention columns).
- Leavers' pre-existing accounts (M12).
- Super Admin recovery (M14).
- The UI slice (U1 catch-up).
- Moving a person's reports along with them: a transfer changes the person's own manager only.
