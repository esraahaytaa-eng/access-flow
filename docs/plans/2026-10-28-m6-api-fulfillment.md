# M6 — API Fulfillment and Exceptions: Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Status:** approved 2026-10-28 with the recommended decision (retired before provisioning → cancelled, audited, no tool call) and all listed calls; recorded in DECISIONS.md (Fulfillment methods refinements). Branch `feat/fulfillment`, shared with M7 (one PR, per the main plan).

**Goal:** the first complete CASE flow.
- An approved item becomes a grant.
- An API tool's grant is provisioned in the Mock Tool Service by a background runner and verified by status lookup (`ACTIVE / VERIFIED`).
- The employee or their manager can remove it again, and the removal is verified too.
- Failures retry with backoff, then appear in Exceptions for a manual retry.

**Architecture:** `internal/access` grows three files:
- `grants.go`: grant and operation creation, revoke and My Access, in the caller's transaction or its own;
- `runner.go`: claim, call, verify, record;
- `rules.go`: backoff, display status and allowed transitions, pure functions with explicit time.

`requests` calls `access` at final approval, inside the decision transaction (A8: `requests` may call `access`; `access` never calls back). The runner is a goroutine in `app`: the `operations` table is the queue (A6). No new dependencies.

**Tech Stack:** as in M2–M5; `access.HTTPToolClient` from M4.

**Spec:** `CASE.md` §3–4; `DECISIONS.md`:
- F2 (stricter wins);
- D4 (one level per tool);
- D5 (manager revokes direct reports);
- Fulfillment methods (status table);
- O1 (effective access), O3 (retries), O5, O6 (voluntary revocation), O8;
- A6 (runner), A9 (indexes, transitions, evidence, point-in-time), A10.

Main plan: the M6 section, including the M5 follow-ups and the retired-level requirement from the PR #4 review.

## Global Constraints

- Everything in the main plan's Global Constraints applies.
- The Mock Tool Service call runs **outside** any database transaction. The client has a 5-second timeout, and every call carries the operation ID (A6).
- "Success" means a `200` with result `APPLIED` or `NO_CHANGE`, followed by a status lookup that shows the expected state. Anything else is a failed attempt (A6, M4 client).
- Attempts follow O3/A6:
  - Up to 5 attempts, with backoff of 30 s, 1 min, 2 min and 4 min between them. After the 5th failure the operation is `FAILED`.
  - A `409` from the Mock Tool Service means the operation was rejected. That is final for this round and goes straight to `FAILED`. A manual retry re-sends the same operation ID. This is the M4 follow-up: the mock stores only successful operations, so the retry is evaluated again.
- Priority (A6): removals for termination or suspension first, then other removals, then grants. Oldest first within a priority. M6 creates priorities 1 and 2 only; priority 0 is used in M9 and is tested now.
- Effective access (O1, A9):
  - `effective_from` and `effective_to` are set only when a verified activation or removal is recorded, using the transaction time.
  - Requested or failed revocations still count as held access.
  - Activation evidence is `VERIFIED` for API grants only (a `CHECK` on the method).
- Every state change and its audit event share one transaction. Each runner step has its own short transaction: claim, then record.
- Real system time only; tests move `next_attempt_at` and `lease_until` in the database instead of waiting (D12).

## Decision needed before implementation

1. **What happens to an approved item whose level or tool is retired before provisioning?** (Requirement from the PR #4 review.)
   - **Recommended:** it is cancelled and visible as cancelled, never sent to the tool.
     - **At final approval** (the decision that completes the last step): the item becomes `CANCELLED` with the reason "access level retired" or "tool retired" (`ITEM_CANCELLED`), and no grant is created.
     - **When the runner claims a `GRANT` operation** (a retirement landed after approval): the operation and the grant become `CANCELLED` (`GRANT_CANCELLED`), with no call.
     - The requester sees the status and reason in My Requests and can ask for another level.
   - **Alternative:** treat it as a `FAILED` exception that an admin must resolve. This is weaker: there is nothing an admin can sensibly retry, so it would only add work to Exceptions.

## My calls, listed for you to check

1. **Grants for every approved item:**
   - One grant per approved item (unique), created in the same transaction as the final approval, together with its `GRANT` operation.
   - Manual and Vendor grants and operations are created too, but the runner only claims operations of API tools. Their tasks come in M7, so until then they stay `PENDING_FULFILLMENT`.
2. **Stricter wins (F2)**, checked at final approval after the retired check (order changed 2026-10-28, approved; see Implementation notes):
   - If the level's current flags require a step type the item does not have, the missing step is added after the decided steps, keeping Manager → Tool Owner → InfoSec order among the added ones (`STEP_ADDED_BY_POLICY`), and routed. The item stays pending.
   - Removing a step never removes one. Steps already decided are never re-decided.
3. **Duplicate protection (M5 follow-up):** a new request is rejected with `409 access_exists` (field `items[i].accessLevelId`) while the beneficiary has an open grant for the tool. An open grant is any status except `REVOKED` and `CANCELLED`.
   - This covers approved items waiting to be provisioned, because their grant exists from the moment of approval.
   - A `FAILED` grant counts as open: the tool stores permissions per level, so revoking an old failed grant would remove a newer grant's access. The employee gives up the failed grant first (a safe `REVOKE`), or an admin retries it.
   - `CreateGrant` locks the employee row (A9), and Submit checks open items and open grants for the tool in one statement, so a concurrent submit and approval cannot both pass.
4. **Grant statuses** (A9 transitions; display labels from the fulfillment table):
   - `PROVISIONING`, `PENDING_FULFILLMENT` (Manual/Vendor), `ACTIVE`, `FAILED`;
   - `REVOCATION_REQUESTED`, `REVOCATION_FAILED`, `REVOKED`;
   - `CANCELLED`, which never reached the tool.
5. **Removal:** `POST /api/v1/grants/{id}/revoke {version, reason}` by the grant's employee (O6) or their current direct manager (D5). The reason is required for both, for the audit.
   - It locks the employee row (A9).
   - Revoke depends on the grant's state:
     - **Queued and never attempted** (`GRANT` op `PENDING`, 0 attempts): cancel the operation and the grant, with no tool call. This is also the API answer to O5's "withdraw before provisioning starts".
     - **In flight, retrying, failed or active:** the grant becomes `REVOCATION_REQUESTED`. A pending `GRANT` op is cancelled and a `REVOKE` op is created.
     - The runner does not claim the `REVOKE` while that grant's `GRANT` op is in progress. A `GRANT` that completes afterwards records `effective_from` (the access really existed) and keeps `REVOCATION_REQUESTED`. Then the `REVOKE` runs (D5: a stale grant is removed at once).
     - Revoking an unheld level returns `NO_CHANGE`, so this is safe after an uncertain failure.
   - A repeat on a grant already being removed returns its current state with no event.
6. **Withdrawal of approved Manual/Vendor items before their task is claimed** (O5) is M7. Nothing remains for API items, because provisioning starts at approval.
7. **Who may retry:** `POST /api/v1/operations/{id}/retry {version}` (a `FAILED` operation) is allowed for a Super Admin or a member of the tool's admin group ("an authorized admin").
   - Never on your own grant (`403 separation_of_duties`): the beneficiary never fulfils their own item (F2).
   - It resets the attempt count, so a new round of 5 attempts starts, and moves the grant back to `PROVISIONING` or `REVOCATION_REQUESTED`. Audit event: `OPERATION_RETRIED`.
8. **Exceptions** (`GET /api/v1/exceptions`, query-based) are visible to InfoSec, Super Admins and Auditors (read-only, O7). They list:
   - `FAILED` operations, with tool, level, employee, attempts, last error and time;
   - unroutable approval steps (from M5).

   The handler lives in `requests`, which already owns the unroutable list, and calls `access.FailedOperations`.
9. **My Access** (`GET /api/v1/access`): my open and recent grants, with display status, method, evidence, `effective_from` and expiry.
   - **Team access** (`GET /api/v1/access/team`): the direct reports' grants, for the manager (D5).
   - Nobody else's grants are readable here. The Audit Explorer comes in M13.
10. **Request items** in `GET /requests` gain `fulfillment: {grantId, status, evidence}` once a grant exists. Item display follows the fulfillment table: an approved item never shows as active until its grant is.
11. **Runner shape:**
    - `access.Runner{pool, client}` exposes `RunOnce(ctx) (bool, error)`, which handles at most one operation and reports whether there was one, and `Run(ctx)`, which loops `RunOnce` and sleeps 1 s when idle.
    - `app` starts one runner goroutine; A2 assumes one instance. `SKIP LOCKED` and the lease are still tested with concurrent `RunOnce` calls.
    - Each claim records the attempt and a new lease token. A result is recorded only while the lease token still matches, so a worker whose lease expired cannot overwrite the result of the worker that re-claimed the operation.
    - Config: `MOCK_TOOLS_URL`, default `http://mocktools:8081`.
12. **Audit actions:** `GRANT_CREATED`, `OPERATION_ATTEMPT_FAILED` (every failed attempt, with the error), `OPERATION_FAILED`, `GRANT_ACTIVATED`, `GRANT_REVOCATION_REQUESTED`, `GRANT_REVOKED`, `GRANT_CANCELLED`, `OPERATION_RETRIED`, `ITEM_CANCELLED`, `STEP_ADDED_BY_POLICY`. The runner's events have the system as actor.

## Implementation notes (2026-10-28, approved; recorded in DECISIONS.md "safe fulfillment")

Refinements found while implementing; they tighten approved calls 5, 7 and 11 without changing them:
1. **"Never sent" is `operations.first_attempt_at IS NULL`**, set at the first claim, not `attempts = 0`. A manual retry resets the attempt count, so an operation whose earlier response was lost could otherwise be cancelled (on revoke or retirement) while the tool holds the permission.
2. **A `GRANT` attempt that fails after a removal was requested is cancelled, not retried**; the verified `REVOKE` takes over. A removal waits while the grant's `GRANT` is `PENDING` or `IN_PROGRESS`.
3. **Lock order everywhere:** employee, then operations, then grant (runner, revoke, retry), so they cannot deadlock.
4. **Once a `GRANT` may have reached the tool, retirement no longer cancels it:** it is completed and verified, so no permission is left behind unrecorded.

Approved with them: the retired check runs before stricter-wins step addition (`TestRetiredIsCheckedBeforeStricterSteps`).

From the independent reviews (applied, within the approved calls):
5. The runner locks the tool `FOR SHARE` before its retirement check (lock, then read); final approval locks the beneficiary before the tool, as Submit does.
6. A superseded `GRANT` is recorded as `OPERATION_CANCELLED`; `GRANT_REVOCATION_REQUESTED` notes whether a pending or failed `GRANT` was cancelled.
7. Item `fulfillment` carries `evidence` and `closedReason` (plan call 10), so a requester sees why a grant was cancelled after approval.
8. `failedAt` is the operation's `completed_at` (set on `FAILED`, cleared by a retry), not an audit-log scan; `operations(grant_id)` is indexed.

## Review Focus

1. **Retired before provisioning:** retirement before final approval, and between approval and the runner's claim. There is no Mock Tool Service call and no active grant. Pinned in Tasks 2 and 3.
2. **A worker whose lease expired:** worker A claims, its lease expires, B re-claims and finishes, then A answers late. There is one result and one `GRANT_ACTIVATED`, and the tool holds one permission. Pinned in Task 3.
3. **Revoke racing the runner:** a revoke while a grant is queued, in flight, retrying or failed. The end state at the tool is revoked, `effective_from`/`effective_to` match what happened, and there are never two open removals. Pinned in Task 4.
4. **"Success" that isn't:** a `200` whose status lookup shows the level missing, or a malformed or echo-mismatched body, is a failed attempt, never `ACTIVE`. Pinned in Task 3.
5. **Duplicate access:** a second request for a tool while its grant is provisioning, active or being revoked returns `409`, including under concurrent submit and approve. Pinned in Task 2.

## Files

```
internal/platform/migrations/00006_grants.sql   grants, operations, request_items CANCELLED (Task 1)
internal/platform/config.go                     MOCK_TOOLS_URL (Task 1)
internal/access/rules.go, rules_test.go         Backoff, DisplayStatus, transitions (Task 1)
internal/access/grants.go                       CreateGrant, Revoke, MyAccess, TeamAccess, Retry, FailedOperations (Tasks 2, 4, 5)
internal/access/runner.go                       Runner (Task 3)
internal/access/http.go                         access routes (Tasks 4, 5)
internal/requests/decide.go, service.go         stricter wins, retired check, CreateGrant, access_exists, fulfillment field (Task 2)
internal/requests/http.go                       GET /exceptions (Task 5)
internal/testutil/faults.go                     RespondNext (a 200 with a chosen body) (Task 3)
cmd/accessflow/main.go                          runner goroutine, routes (Tasks 3–5)
compose.yaml, README.md, docs/plans/…           MOCK_TOOLS_URL, API, status (Task 6)
```

---

Each task is Red → Green → Refactor. Write each listed test, run it and see it fail for the right reason before writing the code it covers.

### Task 1: Schema, config and pure rules

**Produces:**
- Migration `00006_grants.sql`:
  - `grants (id, request_item_id UNIQUE, employee_id, tool_id, access_level_id, method, status CHECK (…§call 4), activation_evidence CHECK ('VERIFIED' only when method = 'API'), effective_from, effective_to, expires_on, closed_reason (renamed from revocation_reason: it also holds cancellation reasons), revocation_requested_by, version, created_at)`.
    - A partial unique index on `(employee_id, tool_id, access_level_id)` where status is not `REVOKED` or `CANCELLED` (A9; `FAILED` stays open, see call 3).
  - `operations (id, grant_id, type CHECK ('GRANT','REVOKE'), reason, priority, status CHECK ('PENDING','IN_PROGRESS','SUCCEEDED','FAILED','CANCELLED'), attempts, next_attempt_at, lease_until, lease_token, last_error, version, created_at, completed_at)`.
    - A unique index giving one `GRANT` operation per grant.
    - A partial unique index giving one open `REVOKE` per grant (status `PENDING`, `IN_PROGRESS` or `FAILED`).
    - A claim index on `(status, priority, created_at)`.
  - `request_items.status` gains `CANCELLED`.
  - Grants `SELECT, INSERT, UPDATE`; no `DELETE`.
- `platform.Config.MockToolsURL` (`MOCK_TOOLS_URL`, default `http://mocktools:8081`).
- `access.Backoff(attempts int) (wait time.Duration, final bool)`: after attempt 1 → 30 s, 2 → 1 min, 3 → 2 min, 4 → 4 min, 5 → final.
- `access.DisplayStatus(method, grantStatus string) string`, following the fulfillment table.
- `canMove(grantMoves|opMoves, from, to)`.
- [ ] Tests first:
  - `TestGrantsSchema`: VERIFIED on a MANUAL grant → `23514`; a second open grant for the same level → `23505`; a second open `REVOKE` → `23505`; no `DELETE` → `42501`.
  - `TestBackoff` (table: attempts 1–5).
  - `TestDisplayStatus` (table: every method × status).
  - `TestGrantTransitions` (`REVOKED` is final; `CANCELLED` only from `PROVISIONING`/`PENDING_FULFILLMENT`).
  - Config: default and override.

### Task 2: Final approval creates the grant: stricter wins, retired check, duplicates

**Consumes:** M5 `advance`.

**Produces:**
- `access.CreateGrant(ctx, tx, now, actor uuid.UUID, itemID uuid.UUID) (uuid.UUID, error)`, called by `advance` instead of closing straight to `APPROVED`.
- `requests` adds:
  - `policySteps`: the current flags against the item's steps, for stricter wins;
  - `retiredReason`;
  - `access_exists` in `checkLevel`;
  - the `fulfillment` field on items.
- [ ] Tests first (requests package):
  - `TestFinalApprovalCreatesGrantAndOperation`: one grant `PROVISIONING`, one `GRANT` operation (priority 2) and `GRANT_CREATED`, in the same transaction as `ITEM_APPROVED`.
  - `TestManualToolGrantWaitsForFulfillment`: `PENDING_FULFILLMENT`, and the runner never claims it.
  - `TestStricterWinsAddsAStepBeforeProvisioning`: InfoSec is added to Jira Viewer after submission → after the manager approves, a pending INFOSEC step and no grant; after InfoSec approves, a grant. Also: removing a step never removes one.
  - `TestRetiredLevelIsNeverProvisioned` and `TestRetiredToolIsNeverProvisioned`: retire after submission, approve → item `CANCELLED` with reason, no grant, no operation, `ITEM_CANCELLED`.
  - `TestNoSecondRequestWhileAccessIsOpen`: a grant that is provisioning, active, failed or being revoked → `409 access_exists`, for the same level and for another level of the tool. A revoked or cancelled grant allows a new request. Also concurrent: a submit racing the final approval of the same tool → exactly one open item or grant.
  - `TestRequestShowsFulfillment`: the item's `fulfillment.status` follows the grant.

### Task 3: Runner

**Produces:**
- `access.NewRunner(pool, client ToolClient) *Runner`, with `RunOnce(ctx) (bool, error)` and `Run(ctx)`.
- `testutil.Faults.RespondNext(status int, body string)`.
- [ ] Tests first (access package, real mocktools database and handler behind `testutil.Faults`):
  - `TestRunnerProvisionsAndVerifies`: `ACTIVE`, `VERIFIED`, `effective_from` = the recording time; the mock holds the permission.
  - `TestRunnerSkipsRetiredLevel` (regression): retire between approval and claim → `CANCELLED`, and the mock received no request (Faults counts).
  - `TestSuccessWithWrongStateIsNotSuccess`: the lookup shows the level missing → failed attempt, `next_attempt_at` = now + 30 s.
  - `TestBackoffThenFailed`: 5 × 5xx → attempts 5 and `FAILED`, five `OPERATION_ATTEMPT_FAILED` and one `OPERATION_FAILED`.
  - `Test409IsFailedAtOnce`: `level_suspended` → `FAILED` after 1 attempt.
  - `TestTimeoutIsAFailedAttempt`: `DelayNext` beyond the client timeout.
  - `TestLeaseExpiryReclaims` and `TestLateWorkerCannotOverwrite`: stale lease token → its result is dropped; one `GRANT_ACTIVATED`; one permission.
  - `TestConcurrentRunnersClaimEachOperationOnce` (`-race`, two `RunOnce` loops, 20 operations).
  - `TestPriorityOrder`: priority 0 before 1 before 2, oldest first within each.
  - `TestRevokeWaitsForInFlightGrant`: the claim query skips a `REVOKE` while the same grant's `GRANT` is `IN_PROGRESS`.

### Task 4: Removal, My Access and Team Access

**Routes:** `POST /api/v1/grants/{id}/revoke`, `GET /api/v1/access`, `GET /api/v1/access/team`.

- [ ] Tests first:
  - `TestVoluntaryRevokeEndToEnd`: `REVOCATION_REQUESTED`, then after `RunOnce` `REVOKED` with `effective_to`; the mock no longer has the level and the account remains (D4).
  - `TestManagerRevokesDirectReport`: someone who is not the manager gets 404; a former manager gets 404.
  - `TestRevokeWhileQueuedCancels`: no tool call, `CANCELLED`.
  - `TestRevokeWhileInFlight`: `effective_from` and `effective_to` both set, final state `REVOKED`.
  - `TestRevokeWhileRetrying` and `TestRevokeAfterFailed`: a `REVOKE` runs and the final state is `REVOKED`.
  - `TestRepeatedRevokeIsIdempotent`, including concurrent repeats: one `REVOKE` operation, one event.
  - `TestMyAccessAndTeamVisibility`: own grants only; the manager sees direct reports' grants only; display statuses.
  - `TestRevokePermissionMatrix` (5 roles).

### Task 5: Exceptions and manual retry

**Routes:** `GET /api/v1/exceptions`, `POST /api/v1/operations/{id}/retry`.

- [ ] Tests first:
  - `TestExceptionsListFailedOperationsAndUnroutableSteps` (InfoSec, Super Admin, Auditor → 200; others → 403).
  - `TestManualRetryWorks`: `FAILED` → retry by an admin-group member → `RunOnce` → `ACTIVE`; same operation ID; `OPERATION_RETRIED`.
  - `TestRetryAfter409ReusesTheOperationID` (the M4 follow-up): the mock evaluates it again.
  - `TestRetryPermissionMatrix`: a non-member and the Auditor → 403; an admin-group member retrying their own grant → `403 separation_of_duties`.
  - `TestRetryIsIdempotent`: a second retry while `PENDING` returns the current state.

### Task 6: Wiring, docs, full suite, Docker journey, reviews

- [ ] Wire up the app:
  - `main.go` starts `Runner.Run(ctx)` with `NewHTTPToolClient(cfg.MockToolsURL, 5*time.Second)` and stops it on shutdown;
  - `compose.yaml` sets `MOCK_TOOLS_URL`;
  - the README covers the API, statuses, retries and limitations.
- [ ] Mark the main plan's M6 status.
- [ ] Run the full suite: exit 0.
- [ ] Docker journey (throwaway project):
  1. dev1 requests Jira Viewer; the manager approves.
  2. Within a few seconds the grant is `ACTIVE / VERIFIED`, and the mock's account lookup shows the level.
  3. dev1 gives it up → `REVOKED`, verified.
  4. Stop `mocktools`, request and approve → retries visible; then fast-forward `next_attempt_at` in the DB → `FAILED` in Exceptions.
  5. Start `mocktools` again → a manual retry → `ACTIVE`.
  6. A retired level → `CANCELLED`, and there is no mock call in its logs.
- [ ] Superpowers `requesting-code-review` (independent) and `ponytail-review`; fix the findings; re-run.
- [ ] Report; ask for commit approval: `feat: provision API access through the mock tool service with exceptions`.

## Acceptance criteria (main plan)

- The full flow runs against the real Mock Tool Service: request → approvals → `ACTIVE / VERIFIED` → removed and verified (Tasks 3–4, journey).
- A failing tool reaches `FAILED` after 5 attempts and appears in Exceptions; a manual retry works (Tasks 3, 5).
- An approved request never provisions a retired tool or level (Tasks 2–3).
- A second request is rejected while access to the tool is open, including between approval and provisioning (Task 2).

## Not in M6

- Manual and Vendor tasks, their withdrawal before claim, and direct grants (M7).
- Expiry of temporary grants, extensions and level changes (M8).
- Suspension, termination and priority-0 operations (M9; ordering is tested now).
- Re-routing pending approval steps of departed assignees, and tools whose owner left (M9).
- Transfer review (M10).
- UI (U1 catch-up).
