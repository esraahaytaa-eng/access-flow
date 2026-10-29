# M9 — Termination and Suspension: Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Status:** revised 2026-10-29.
- Decisions 1–3 and the suspension / termination distinction are approved. Decision 4 is approved with the last-Super-Admin guard.
- The independent Ponytail review findings 1–11 are incorporated.
- Decision 5 approved 2026-10-29 (the guard covers suspension, termination and scheduling). Implemented 2026-10-29 on `feat/termination-suspension`, awaiting commit approval.
- **Deviation — placement:** the person-level orchestration lives in `requests` (`internal/requests/lifecycle.go`, `schedule.go`), not `people`. `people` → `requests` would be an import cycle with the existing `requests` test fixtures; `people` exports the record-level helpers (`LockLifecycle`, `KeepsASuperAdmin`, `SetStatus`, `SetTermination`, `EndEmployment`). For the same reason `sweeps.New` takes `requests.TerminateDue` as a job.
- `catalog.checkOwner` already locked the owner (M4); only `checkManager` needed the lock.
- M9 comes before U1, as you decided 2026-10-29. `design/` stays untouched and out of every commit.
- Branch `feat/termination-suspension` from `main` (`a5da1f2`); a new PR.

**Goal:** HR suspends, reactivates and terminates employees, immediately or on a schedule. Every open grant of the person ends in a tracked, visible state, including grants still provisioning and level-change replacement grants:
- suspended through `SUSPEND` operations;
- resumed through `RESUME` operations;
- or removed with termination priority until the removal is verified or confirmed.

Access that is still live in a tool for a suspended or terminated person stays visible and actionable in Exceptions.

**Architecture:**
- **Operations:** two new operation types, `SUSPEND` and `RESUME`, in the existing `operations` table. API ones are run by the existing runner and verified. Manual and Vendor ones go to the existing task queue and are self-reported. There is no new engine (Fulfillment methods: one workflow).
- **Grant status (decision 1):** one new stored grant status, `SUSPENDED`. Requested and failed suspension or resume are read from the grant's open operation. Suspension intervals are stored in `grant_suspensions` (A9).
- **One function brings a grant to a target state:** `access.settle(..., target)`, where the target is `SUSPENDED` (suspension) or `ACTIVE` (reactivation). Termination uses the existing `requestRemoval` on every open grant.
- **A `SUSPEND` or `RESUME` that is no longer wanted is cancelled, never failed or retried.** It is unwanted when its grant is being removed, or when a newer `SUSPEND`/`RESUME` exists. This extends M6's rule: a `GRANT` failing after its removal was requested is cancelled. A stale operation in Exceptions can then never flip access the wrong way.
- **Person-level actions live in `people` (A8).** They call `requests` (cancel the person's pending items, re-route steps assigned to them) and `access` (their grants), in one transaction per action.
- **Scheduled terminations** are a second sweep in the M8 `sweeps` loop.
- **No new dependencies.** The UI slice goes into U1's catch-up.

**Tech Stack:** Go, chi, pgx, goose, PostgreSQL 18, as in M6–M8.

**Spec:**
- `CASE.md` §3 (removal on leaving; the "14 leavers" problem).
- `DECISIONS.md`:
  - D7 (termination and suspension); D2 (Super Admin continuity);
  - F2 (fallback routing), D9 (re-route, no delegation);
  - A4 (token version, status check), A5 (identity, reactivation), A6 (priorities, sweeps), A9 (transitions, employee lock, intervals);
  - Fulfillment methods (labels; "Suspend and resume tasks come with suspension (D7, M9)");
  - O1, O3, O7 (Exceptions), O8;
  - the F4 refinement on employee administration.
- Main plan, M9:
  - the M8 requirement on in-flight replacement grants;
  - the M5 follow-up (re-route steps of suspended or terminated assignees);
  - the M3/M4 follow-ups (`checkManager` and `checkOwner` lock the manager or owner).

## Global Constraints

- Everything in M6–M8 applies:
  - lock first, then read in a new statement;
  - a state change and its audit event in one transaction;
  - real time only (D12);
  - parameterized SQL;
  - stable error codes, no display text.
- **Lock order for a person-level action** (suspend, reactivate, terminate, schedule or cancel a termination, the termination sweep):
  0. the advisory transaction lock `people.LifecycleLockKey` (only for the last-Super-Admin check; see decision 4);
  1. the pending request items that name the person, as beneficiary or as the assignee of a pending step, `ORDER BY id`;
  2. the person's employee row;
  3. the person's operations;
  4. the person's grants.

  Items come before the employee for a reason the review confirmed. An approval locks its item, then its audit insert takes a key-share lock on the approver's row (`audit_events_actor_fkey`). The reverse order would deadlock with an approval in progress.
- **One action, one transaction:** every D7 effect of one action commits together, or none do.
- **Operations on one grant run in creation order.** `revokeWaits` is generalized to `waitsForEarlier`. An operation is not claimable, and its task is not ready, while an older operation of the same grant is `PENDING` or `IN_PROGRESS`.
- **Priorities, following A6 literally:**
  - 0 — termination removals.
  - 1 — other removals and `SUSPEND`. A6 says "then other revocations and suspensions". An earlier draft used 0; that is corrected here.
  - 2 — grants and `RESUME`.
- **Actors:** every function that serves the termination sweep takes `actor *uuid.UUID`, where nil means the system. This covers `requests.EndFor`, `closeItem`, re-route audit, `SuspendAll`, `ResumeAll`, `RemoveAll` and `terminate`.
- **No emails or notifications** for these actions (O2). They are audited only.
- **Error codes:** reuse the existing codes:
  - `403 self_change_forbidden`;
  - `403 privileged_reset_requires_super_admin`;
  - `409 version_conflict`.

  One new code, `409 employee_status`, is a state conflict, such as reactivating an `ACTIVE` employee or terminating a `TERMINATED` one. It is not input validation, which is why it is not the existing `400 employee_not_active`.

## Decisions

1. **How suspension is stored: approved.**
   - The grant is stored as `SUSPENDED` only once the `SUSPEND` is verified or confirmed.
   - While a `SUSPEND` is open, the grant stays `ACTIVE`. Its `suspension` field shows `SUSPENSION_REQUESTED`, or `SUSPENSION_FAILED` after a failed attempt.
   - Resume works the same way: the grant stays `SUSPENDED` and shows `RESUME_REQUESTED` or `RESUME_FAILED`.
   - `DisplayStatus` gives `SUSPENDED / VERIFIED` or `SUSPENDED / SELF_REPORTED`.
   - Intervals are stored in `grant_suspensions`.
2. **Grants in flight at suspension: approved.**
   - A `GRANT` never sent is cancelled ("employee suspended").
   - A `GRANT` that was sent, or that failed, continues. If it becomes `ACTIVE` while the employee is suspended, the same transaction queues its `SUSPEND`.
   - **Level change:** the old grant is suspended. The new grant is cancelled if never sent; otherwise it is suspended on activation, and M8's rule then removes the old one.
   - **Termination** removes every open grant, the replacement grant included.
   - The M8 requirement line in the main plan is amended with a dated note.
3. **Who may act: approved.**
   - HR Managers and Super Admins may suspend, reactivate, terminate and schedule.
   - A target holding Super Admin or InfoSec needs a Super Admin.
   - Nobody acts on themselves.
   - The existing `checkReset` is reused unchanged and renamed `checkAccountAction`.
4. **Roles and memberships: approved, with a guard.**
   - Termination closes the person's role assignments and admin-group memberships (`valid_to = now`, audited).
   - Suspension keeps them.
   - **The last active Super Admin cannot be terminated** (`409 last_super_admin`). No other recovery path exists before M14:
     - the initial Super Admin is created only when no Super Admin has ever existed;
     - `recover-admin` is M14;
     - `SUPER_ADMIN` cannot be assigned through the role endpoint.
   - The check runs under `LifecycleLockKey`, so two Super Admins terminating each other at once cannot both succeed.
5. **Extending the guard — needs your answer.** Recommended: the guard also covers **suspension** and **scheduling** a termination. "The last active Super Admin" then means no *other* `ACTIVE` employee with an open `SUPER_ADMIN` role and no termination scheduled.
   - **Suspension:** reactivating a Super Admin needs a Super Admin (decision 3). Suspending the last one would lock everyone out exactly as termination does.
   - **Scheduling:** if the schedule were accepted, the sweep would later have to refuse it. A leaver would then silently stay active.
   - Reactivating someone and cancelling a schedule only add Super Admins, so the sweep's re-check can then never block. If it ever does, it skips the termination, logs a warning and leaves the schedule due.
   - *Alternative:* guard termination only, as you literally stated.

## My calls (approved with the plan unless you object)

1. **Inputs:**
   - Each action takes `reason` (required, 1–1000 characters) and the employee's `version`.
   - Allowed starting status:
     - suspend: `ACTIVE`;
     - reactivate: `SUSPENDED`;
     - terminate: `ACTIVE` or `SUSPENDED`;
     - schedule or cancel a termination: `ACTIVE` or `SUSPENDED`.
   - Any other status gives `409 employee_status`.
2. **Scheduled termination:**
   - `effectiveAt` is an RFC 3339 instant in the future (`400 effective_at_not_future`).
   - There is one schedule per employee; scheduling again replaces it.
   - An immediate termination clears the schedule; reactivation keeps it.
   - A schedule that falls due runs whether the employee is `ACTIVE` or `SUSPENDED`. The actor is the system, and the audit records who scheduled it.
3. **Pending requests:**
   - Items where the person is the beneficiary are cancelled.
   - Items they submitted for someone else stay.
   - Pending steps assigned to them are re-routed now through the F2 routing extracted from `advance` (`STEP_REROUTED`). They are not routed back on reactivation.
4. **Claimed tasks are released without a write.** A claimer who is not an `ACTIVE` employee counts as inactive, so M7's takeover rule applies.
5. **Termination per grant:**
   - Each open grant's `PENDING`/`FAILED` `SUSPEND`/`RESUME` operations are cancelled. Then `requestRemoval` runs with reason `TERMINATION` at priority 0.
   - This covers `SUSPENDED` grants, grants in flight or failed, and replacement grants.
   - Removals already open are raised to priority 0.
   - A `SUSPEND`/`RESUME` still `IN_PROGRESS` is cancelled when it records, never rescheduled. The removal therefore waits for at most one attempt.
6. **Reactivation re-evaluation (D7, review finding 4):**
   - Every grant that *would get a `RESUME`* is checked first. That is a `SUSPENDED` grant, or one whose sent `SUSPEND` is being cancelled.
   - If its level or tool is retired, or its `expires_on` is before today, it is not restored: `requestRemoval` runs with reason `RETIRED` or `EXPIRED`.
   - Every other grant is settled to `ACTIVE`.
7. **During a suspension:**
   - The expiry sweep includes `SUSPENDED` grants.
   - Managers and Super Admins can revoke a suspended grant.
   - A failed `GRANT` can still be retried; it is suspended on activation.
   - Reviews are M11.
8. **Exceptions: visibility and action (your emphasis):**
   - **`suspensionGaps`** (new) lists every grant of a `SUSPENDED` employee that is still not suspended in the tool and whose `SUSPEND` has failed at least once (`last_error` set). It shows from the first failed attempt until the suspension is verified or confirmed, using the same rule as the M8 overlap list.
     - Each row carries the operation, so it is actionable: an operation that has reached `FAILED` is retried from Exceptions, and a Manual or Vendor one is reworked from its task.
     - It also lists a grant that activated while the employee was suspended and whose `SUSPEND` is failing.
   - **`securityRevocations`** (new) lists every open `REVOKE` of a `TERMINATED` employee, from termination until it is verified or confirmed (O7).
   - **Failed operations:** failed `SUSPEND` and `RESUME` operations also stay in the existing `failedOperations`.
   - **Retry guard (review finding 1):** a retry of a `SUSPEND` needs the employee to be `SUSPENDED`; a retry of a `RESUME` needs them to be `ACTIVE`. Otherwise `409 operation_superseded`.
   - Unwanted operations are cancelled (Architecture), so these lists never show a stale operation.

## Review Focus

1. **The employee is `SUSPENDED` but the tool still grants access:**
   - a failed `SUSPEND`;
   - a `SUSPEND` that failed with `409` (level missing or already changed in the tool);
   - a grant activating after the suspension.

   Each must appear in `suspensionGaps` with a working retry or task, until the tool state is verified or confirmed. Tests in Task 3.
2. **A stale `SUSPEND` or `RESUME` that fails or finishes late after the opposite action:** it is cancelled, never retried. Its verified result still records the interval, so history matches the tool. Tests in Task 4.
3. **A replacement grant activating after a suspension or termination:** it ends suspended or removed, never `ACTIVE` and untracked. Tests in Task 2 and Task 5.
4. **An approver being suspended while approving:** the approval waits and then fails. This applies to assigned steps and to pool steps. No decision is recorded after the suspension. Test in Task 2.
5. **Scheduled termination after downtime, a schedule cancelled during the sweep, or two concurrent actions:** each runs once, or not at all. Tests in Task 6.

---

### Task 1: Schema and pure rules

**Files:**
- Create: `internal/platform/migrations/00009_suspension_termination.sql`
- Modify: `internal/access/rules.go`, `internal/access/rules_test.go`

**Interfaces:**
- Produces:
  - the grant status `SUSPENDED`;
  - the operation types `SUSPEND` and `RESUME`;
  - the table `grant_suspensions(id, grant_id, effective_from, effective_to, evidence)`;
  - the columns `employees.termination_at`, `termination_scheduled_by` and `termination_reason`;
  - `needsOp(target, effective string, cancelledSent bool) bool`.

- [ ] **Step 1: Write the failing tests.**
  - `TestGrantMovesForSuspension`:
    - allowed: `ACTIVE→SUSPENDED`, `SUSPENDED→ACTIVE`, `SUSPENDED→REVOCATION_REQUESTED`;
    - not allowed: `SUSPENDED→CANCELLED`, `PROVISIONING→SUSPENDED`.
  - `TestDisplayStatusSuspended`: API → `SUSPENDED / VERIFIED`; Manual and Vendor → `SUSPENDED / SELF_REPORTED`.
  - `TestNeedsOp`, a table. `effective` is the in-flight operation's target if there is one, otherwise the grant status (review finding 11):

    | target | effective | cancelledSent | result |
    |---|---|---|---|
    | `SUSPENDED` | `ACTIVE` | false | true |
    | `SUSPENDED` | `SUSPENDED` | false | false |
    | `SUSPENDED` | `SUSPENDED` | true | true |
    | `ACTIVE` | `ACTIVE` | true | true (a failed suspend may have applied) |
    | `ACTIVE` | `ACTIVE` | false | false |
    | `ACTIVE` | `SUSPENDED` | false | true |
  - Migration test:
    - a `RESUME` operation can be inserted;
    - a second `PENDING` `SUSPEND` on one grant violates `operations_one_open_suspension`;
    - `termination_at` without `termination_scheduled_by` violates the CHECK;
    - a second open interval for one grant violates the unique index;
    - the runtime user can insert and update `grant_suspensions` but cannot delete from it.
- [ ] **Step 2: Run the tests and see them fail.** Command: `docker compose --profile test run --rm tests sh -c "go test -race -count=1 -run 'TestGrantMovesForSuspension|TestDisplayStatusSuspended|TestNeedsOp|TestMigration' ./internal/access/ ./internal/platform/"`.
- [ ] **Step 3: Implement.**
  - **Migration:**
    - extend the grant status and operation type CHECKs;
    - add the unique index `operations_one_open_suspension ON operations (grant_id) WHERE type IN ('SUSPEND','RESUME') AND status IN ('PENDING','FAILED')`;
    - create `grant_suspensions`, with a unique index on open intervals and `evidence IN ('VERIFIED','SELF_REPORTED')`; grant `SELECT, INSERT, UPDATE` to `accessflow_app`;
    - add the employee columns with `CHECK ((termination_at IS NULL) = (termination_scheduled_by IS NULL))` and a partial index on `termination_at`.
  - **Rules:** add the three moves to `grantMoves`, `SUSPENDED` to the evidence branch, and `needsOp` = `cancelledSent || effective != target`.
- [ ] **Step 4: Run the tests and see them pass**, then `gofmt -l` and `go vet ./...`.

### Task 2: Suspend an employee, all open grants (API)

**Files:**
- Create: `internal/people/lifecycle.go`, `internal/people/lifecycle_test.go`, `internal/access/suspension.go`, `internal/requests/lifecycle.go`
- Modify:
  - `internal/people/http.go`, `internal/people/rules.go`;
  - `internal/access/runner.go`: `call`, `record`, `succeed`, `fail`, and `revokeWaits` → `waitsForEarlier`;
  - `internal/access/expiry.go`: `openStatuses` gains `SUSPENDED`;
  - `internal/access/grants.go`: `requestRemoval` accepts `SUSPENDED`, cancels open `SUSPEND`/`RESUME` operations, and takes a `priority`;
  - `internal/requests/decide.go`:
    - `route(ctx, tx, it, step) (Route, error)` is extracted from `advance`;
    - `candidate` locks `FOR SHARE` before reading the status (review finding 5);
    - `decide` checks under `FOR SHARE` that the actor is `ACTIVE` (review finding 6);
    - `closeItem` takes `actor *uuid.UUID`.

**Interfaces:**
- Produces:
  - `POST /employees/{id}/suspend` with body `{reason, version}`, returning `Employee`;
  - `func (s *Service) Suspend(ctx, actor auth.Principal, id uuid.UUID, version int, reason string) (Employee, error)`;
  - `requests.LockFor(ctx, tx, employee uuid.UUID) error`;
  - `requests.EndFor(ctx, tx, now, actor *uuid.UUID, employee uuid.UUID, reason string) error`;
  - `access.SuspendAll(ctx, tx, now, actor *uuid.UUID, employee uuid.UUID) error`;
  - `access.settle(ctx, tx, now, actor *uuid.UUID, grant, employee uuid.UUID, target, reason string) error`;
  - `Grant.suspension *string`.

- [ ] **Step 1: Write the failing tests** in `internal/people/lifecycle_test.go`. They run against real PostgreSQL, with the in-process mock tool and runner.
  - `TestSuspendAPI`. HR suspends dev, who holds an ACTIVE API grant:
    - the response is `200`; the status is `SUSPENDED` and the version + 1;
    - the old cookie gets `401` and login fails with the generic error;
    - the pending item is `CANCELLED` ("employee suspended");
    - the grant shows `SUSPENSION_REQUESTED`, with a `SUSPEND` at priority 1;
    - after draining it is `SUSPENDED / VERIFIED`, with one open interval, and the mock shows the level `SUSPENDED`;
    - the audit has `EMPLOYEE_SUSPENDED` and `GRANT_SUSPENDED`.
  - `TestSuspendDuringProvisioning`:
    - a `GRANT` never sent → `CANCELLED`;
    - a `GRANT` in flight (delayed fault) → it activates, gets a `SUSPEND`, and ends `SUSPENDED / VERIFIED`;
    - the mock shows no `ACTIVE` level.
  - `TestSuspendDuringLevelChange`:
    - the new grant was never sent → it is cancelled and the old one ends `SUSPENDED`;
    - the new grant is in flight → it activates and is suspended, and the old one is removed;
    - finally the mock holds only the new level, `SUSPENDED`.
  - `TestExpiryAndRevokeDuringSuspension`:
    - the sweep removes a suspended grant whose date has passed (`REVOKED / VERIFIED`, interval closed);
    - the manager revokes another suspended grant;
    - a pending `SUSPEND` is cancelled, and a `REVOKE` waits for one in flight.
  - `TestSuspendReroutesSteps`:
    - the suspended manager's pending Manager step moves to the manager's manager (`STEP_REROUTED`);
    - the manager's old cookie gets `401` on approve.
  - `TestApproveWhileApproverIsSuspended`, with subtests for an assigned step and an InfoSec pool step:
    - the approval and the suspension run concurrently (`WhileLocked`);
    - no `APPROVED` step by the suspended person exists after the suspension commits.

    This is the test that fails if the lock order is changed to employee-before-items.
  - `TestRerouteToSomeoneBeingSuspended`: re-routing to M while M's suspension commits leaves the step with an `ACTIVE` assignee or in the InfoSec pool, never with M.
  - `TestSuspendPermissions`:
    - a plain employee → `403`;
    - self → `403 self_change_forbidden`;
    - HR on an InfoSec member → `403 privileged_reset_requires_super_admin`;
    - a Super Admin on that member → `200`;
    - already suspended → `409 employee_status`;
    - a stale version → `409 version_conflict`;
    - an empty reason → `400`.
  - `TestSuspendConcurrently`: two suspensions of the same person yield one `200` and one `409`, and one `SUSPEND` per grant.
  - `TestSuspendWhileRunnerRecords`: the suspension and the runner's `record` on one employee, 20 rounds with `-race`, with no deadlock.
- [ ] **Step 2: Run the tests and see them fail.** Command: `… -run 'TestSuspend|TestApproveWhile|TestReroute|TestExpiryAndRevokeDuring' ./internal/people/`.
- [ ] **Step 3: Implement.**
  - **`Suspend`:**
    1. lock in the order above;
    2. check `checkAccountAction`, the status and the version, then the last-Super-Admin guard (decision 4 or 5);
    3. update the employee: status `SUSPENDED`, `token_version + 1`, `version + 1`;
    4. call `requests.EndFor`, then `access.SuspendAll`;
    5. record `EMPLOYEE_SUSPENDED`.
  - **`SuspendAll`** locks operations, then grants, `ORDER BY id`. For each open grant:
    - a never-sent `GRANT` → `cancelGrant(…, "employee suspended")`;
    - `ACTIVE` or `SUSPENDED` → `settle(target SUSPENDED, reason "SUSPENSION")`.
  - **`settle`:**
    1. Cancel the grant's `PENDING` and `FAILED` `SUSPEND`/`RESUME` operations, and note whether any was sent.
    2. Compute `effective`: the target of an `IN_PROGRESS` `SUSPEND` (`SUSPENDED`) or `RESUME` (`ACTIVE`), otherwise the grant status.
    3. If `needsOp`, insert the target's operation: `SUSPEND` at priority 1, or `RESUME` at priority 2.
  - **Runner `call`:**
    - a `SUSPEND` is verified when the level is held `SUSPENDED`;
    - a `RESUME` when the level is held `ACTIVE`.
  - **`succeed`:**
    - On a verified or confirmed `SUSPEND`, it always opens an interval unless one is open (review finding 3). On a `RESUME` it always closes the open interval.
    - Only the status move (`ACTIVE`⇄`SUSPENDED`) is skipped when the grant is already in removal or already in the target state. Then `succeed` is a no-op for the status (review finding 11).
    - After any move to `ACTIVE`, it runs `settle(target SUSPENDED)` if the employee is `SUSPENDED`.
    - A `REVOKE` closes an open interval.
  - **Unwanted operation:** a `SUSPEND`/`RESUME` is unwanted when its grant is in removal or a newer `SUSPEND`/`RESUME` exists for it.
    - `record` cancels such an operation instead of rescheduling it.
    - `fail` cancels it instead of failing it.
    - The check sits next to the existing `GRANT && REVOCATION_REQUESTED` rule.
- [ ] **Step 4: Run the tests and see them pass**, then the M6–M8 `access` and `requests` suites.

### Task 3: Manual and Vendor suspension; gaps in Exceptions

**Files:** modify `internal/access/tasks.go` (a claimer counts as inactive unless an `ACTIVE` employee), `internal/access/operations.go` (`SuspensionGaps`, the `Retry` guard), `internal/requests/decide.go` (`Exceptions.suspensionGaps`), and the tests.

**Interfaces:** produces `access.SuspensionGaps(ctx, pool) ([]Operation, error)`.

- [ ] **Step 1: Write the failing tests.**
  - `TestSuspendManualAndVendor`, with MANUAL and VENDOR subtests:
    - the `SUSPEND` task appears in the group queue, and Vendor needs a ticket;
    - Confirm gives `SUSPENDED / SELF_REPORTED`, with the confirming admin in the audit.
  - `TestSuspensionGapIsVisibleAndActionable`:
    - **API fault:**
      - after the first failed attempt the grant is listed in `suspensionGaps`, while still `ACTIVE`, showing `SUSPENSION_FAILED`;
      - after 5 attempts it is `FAILED` and also listed in `failedOperations`;
      - InfoSec and Auditors can read the list; a plain employee gets `403`;
      - a retry followed by a drain gives `SUSPENDED / VERIFIED`, and the list is then empty.
    - **`409` from the tool** (level removed directly in the mock): `FAILED`, listed, and a revoke by the manager clears it.
    - **Manual task reported as failed:** listed until a retried task is confirmed.
  - `TestSuspensionGapForAGrantActivatedAfterSuspension`: an in-flight grant activates, and its `SUSPEND` fails. It is listed.
  - `TestSuspendedClaimerTaskCanBeTakenOver`: a task claimed by an admin who is then suspended can be claimed by another member (`takeover: true`).
- [ ] **Step 2: Run the tests and see them fail.**
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run the tests and see them pass.**

### Task 4: Reactivation, and stale operations

**Files:** modify `internal/people/lifecycle.go`, `internal/access/suspension.go` (`ResumeAll`), `internal/people/http.go`, and the tests.

**Interfaces:**
- Produces:
  - `POST /employees/{id}/reactivate` with body `{reason, version}`;
  - `Reactivate(...)`;
  - `access.ResumeAll(ctx, tx, now, loc, actor *uuid.UUID, employee uuid.UUID) error`.

- [ ] **Step 1: Write the failing tests.**
  - `TestReactivateAPI`:
    - after reactivation the status is `ACTIVE` and `token_version` + 1;
    - the token from before the suspension stays rejected;
    - the grant shows `RESUME_REQUESTED`, then `ACTIVE / VERIFIED`;
    - the interval is closed and the mock shows `ACTIVE`.
  - `TestReactivateReevaluates`:
    - a suspended grant whose level was retired → removal (`RETIRED`), with no `RESUME`;
    - one with `expires_on` yesterday → removal (`EXPIRED`);
    - a grant whose `SUSPEND` failed and whose level was then retired → removal, with no `RESUME` (review finding 4);
    - revoked grants are untouched; cancelled items stay cancelled.
  - `TestReactivateDuringSuspend`:
    - the `SUSPEND` was never sent → it is cancelled, with no `RESUME`, and the grant stays `ACTIVE`;
    - the `SUSPEND` `FAILED` → it is cancelled, a `RESUME` is queued and verifies `ACTIVE`, and the old `SUSPEND` is not in Exceptions;
    - the `SUSPEND` is `IN_PROGRESS` → the `RESUME` runs after it, ending `ACTIVE`, with the interval opened and then closed.
  - `TestStaleSuspensionOperationsAreCancelled` (review findings 1–3):
    - a `SUSPEND` in flight fails after reactivation → it is `CANCELLED`, not `FAILED` or `PENDING`;
    - a `RESUME` in flight fails after a re-suspension → `CANCELLED`;
    - a `RESUME` in flight is verified after the manager revoked the grant → the interval is closed at the `RESUME` time and the `REVOKE` follows;
    - retrying a `FAILED` `SUSPEND` of an `ACTIVE` employee → `409 operation_superseded`, and the mirror case for a `RESUME`.
  - `TestReactivateResumeManual`: confirming the `RESUME` task gives `ACTIVE / SELF_REPORTED`.
  - `TestReactivateRules`: an `ACTIVE` or `TERMINATED` target → `409 employee_status`; the permission matrix is the same as for suspension.
- [ ] **Step 2: Run the tests and see them fail.**
- [ ] **Step 3: Implement.** `ResumeAll` runs the re-evaluation in my call 6, then `settle(target ACTIVE, reason "REACTIVATION")`.
- [ ] **Step 4: Run the tests and see them pass.**

### Task 5: Immediate termination

**Files:** modify `internal/people/lifecycle.go`, `internal/access/suspension.go` (`RemoveAll`), `internal/access/operations.go` (`SecurityRevocations`), `internal/requests/decide.go`, and the tests.

**Interfaces:**
- Produces:
  - `POST /employees/{id}/terminate` with body `{reason, version}`;
  - `Terminate(...)`, which shares `terminate(ctx, tx, now, actor *uuid.UUID, e Employee, reason string) error` with Task 6;
  - `access.RemoveAll(ctx, tx, now, actor *uuid.UUID, employee uuid.UUID) error`;
  - `access.SecurityRevocations(ctx, pool) ([]Operation, error)`.

- [ ] **Step 1: Write the failing tests.**
  - `TestTerminateRemovesEverything`. Dev holds an ACTIVE API grant, a suspended Manual grant, a FAILED Vendor grant, and a level change with the new grant in flight. After termination:
    - the status is `TERMINATED` and the old token gets `401`;
    - roles and memberships are closed;
    - items are cancelled and steps re-routed;
    - every grant is `REVOCATION_REQUESTED` (or `CANCELLED` if never sent), with a `REVOKE` at priority 0;
    - `SUSPEND` and `RESUME` operations are cancelled;
    - every removal is listed in `securityRevocations` until it is drained or confirmed, after which none is listed;
    - the mock holds no level.
  - `TestTerminateDuringSuspendInFlight` (review finding 2): a `SUSPEND` is in flight with a fault. It is cancelled when it records, and the `REVOKE` runs on the next claim.
  - `TestTerminateDuringResumeInFlight` (review finding 3): the interval closes when the `RESUME` is verified, then the `REVOKE` runs.
  - `TestTerminationRemovalsRunFirst`: the termination `REVOKE` is claimed before an older priority-1 removal and a priority-2 grant. An already open `REVOKE` is raised to priority 0.
  - `TestTerminateReplacementInFlight`: an `IN_PROGRESS` replacement grant never becomes `ACTIVE`. Both grants end `REVOKED`, and the old one has one removal.
  - `TestTerminateLastSuperAdmin`:
    - terminating the only active Super Admin → `409 last_super_admin`, and nothing changes;
    - with two Super Admins, concurrent terminations of each other → exactly one succeeds;
    - with decision 5 approved: the suspension and the scheduling of the last one → `409`; a Super Admin whose termination is scheduled does not count.
  - `TestTerminateRules`:
    - an already `TERMINATED` employee → `409 employee_status`;
    - the permission matrix;
    - a concurrent terminate and suspend → one outcome;
    - no reactivation after termination.
- [ ] **Step 2: Run the tests and see them fail.**
- [ ] **Step 3: Implement.** Implement my call 5. Close roles and memberships as `RemoveRole` and `RemoveMember` do, with their audit actions.
- [ ] **Step 4: Run the tests and see them pass.**

### Task 6: Scheduled termination

**Files:**
- Modify: `internal/people/lifecycle.go`, `internal/people/http.go`, `internal/sweeps/sweeps.go`, `cmd/accessflow/main.go`
- Test: `internal/people/schedule_test.go`

**Interfaces:**
- Produces:
  - `PUT /employees/{id}/termination` with body `{effectiveAt, reason, version}`;
  - `POST /employees/{id}/termination/cancel` with body `{reason, version}`;
  - `people.TerminateDue(ctx, pool) (int, error)`, guarded by `TerminationLockKey`;
  - `sweeps.RunOnce` runs `TerminateDue` after `ExpireDue`.

- [ ] **Step 1: Write the failing tests.**
  - `TestScheduleTermination`:
    - scheduling, rescheduling (with from/to) and cancelling are each audited (`TERMINATION_SCHEDULED` / `_RESCHEDULED` / `_CANCELLED`);
    - a past `effectiveAt` → `400 effective_at_not_future`;
    - the permission matrix.
  - `TestScheduledTerminationRuns`:
    - a schedule made due by SQL (D12) is run by the sweep with the system as actor and the scheduler in the details;
    - running the sweep twice, or 4 sweeps concurrently, terminates once and creates one set of `REVOKE`s;
    - a suspended employee's schedule runs too.
  - `TestImmediateTerminationOverridesSchedule`: an immediate termination clears the schedule, and the sweep then does nothing.
  - `TestScheduleCancelledWhileSweeping`: the sweep lists the employee and waits for the employee lock while a cancel commits. Its re-check under the lock then does nothing (`WhileLocked`).
- [ ] **Step 2: Run the tests and see them fail.**
- [ ] **Step 3: Implement.** Follow the M8 `ExpireDue` pattern: an advisory lock around the listing, then one transaction per employee in the lock order, re-checking `termination_at <= now AND status <> 'TERMINATED'`.
- [ ] **Step 4: Run the tests and see them pass.**

### Task 7: Follow-ups, documentation, journey

**Files:** modify `internal/people/service.go` (`checkManager`), `internal/catalog/service.go` (`checkOwner`), `README.md`, `DECISIONS.md`, the main plan, and this plan.

- [ ] **Step 1: Write the failing tests.** `TestManagerTerminatedWhileAssigned`, and the same for a tool owner, run concurrently. Either the assignment fails as not active, or it committed before the termination; never after.
- [ ] **Step 2: Implement.** Take `FOR SHARE` on the manager or owner row before reading the status.
- [ ] **Step 3: Run the tests and see them pass.**
- [ ] **Step 4: Docs.**
  - **README:**
    - endpoints;
    - the suspension, reactivation and termination rules;
    - the Exceptions lists;
    - limitations:
      - the M8 window between listing and locking items now also applies to person-level actions;
      - a released task still shows the old claimer until another member takes it over;
      - person-level actions are serialized by one advisory lock.
  - **DECISIONS.md:** dated D7 refinement notes for decisions 1–5 as approved, and the last-Super-Admin guard (D2) until M14.
  - **Main plan:** the M9 status, and the M8 requirement line amended per decision 2.
- [ ] **Step 5: Verify and review.**
  - Run the full suite `docker compose --profile test run --rm tests`.
  - Run the Docker journey in the throwaway `accessflow-m2check` project: suspend, observe the gap in Exceptions, retry, reactivate, terminate.
  - Then the Superpowers code review and the Ponytail review.

## Out of scope

- Transfer and rehire (M10).
- Reviews during suspension (M11).
- Leavers' pre-existing accounts (M12).
- Super Admin recovery and promotion (M14), which lifts the guard.
- The UI slice (U1 catch-up).
