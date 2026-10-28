# M7 — Manual and Vendor Fulfillment, Direct Grants: Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Status:** approved 2026-10-28 with the Ponytail refinements and all three decisions as recommended; recorded in DECISIONS.md (Fulfillment methods: "Manual and Vendor tasks", "direct-grant notifications"). Branch `feat/fulfillment` (shared with M6, one PR; M6 is committed as `4a2d65a`, not pushed).

**Goal:** Manual and Vendor tools are fulfilled by people through admin-group tasks with self-reported evidence, approved items can be cancelled before work starts, and a Super Admin can grant (temporary, ≤ 90 days) or revoke directly for every method.

**Architecture:**
- **A task is the Manual or Vendor operation itself.** Each Manual or Vendor `GRANT` or `REVOKE` operation, which M6 already creates and the runner skips, *is* the task (Fulfillment methods: one workflow; A6: "Manual and Vendor fulfillment remain human-managed tasks, not runner work").
- **New columns on `operations`:** `claimed_by` and `ticket_ref`. `claimed_at` is `first_attempt_at`, so "never sent" keeps the meaning M6 gave it. On a finished task, `claimed_by` is the admin who confirmed it or reported the failure; the audit event's actor records them too.
- **Code:** task actions live in `internal/access/tasks.go`. The direct grant lives in `requests` and reuses Submit's checks and `access.CreateGrant`.
- **No new tables, no runner change, no new dependencies.**

**Tech Stack:** as in M6.

**Spec:**
- `CASE.md` §3 ("some need a ticket to the vendor").
- `DECISIONS.md`:
  - Fulfillment methods: the table, and the refinements "grant lifecycle" and "safe fulfillment";
  - D1 (direct grant and revoke, initiator rule, 90 days, notifications);
  - F2 (the beneficiary never fulfils their own item);
  - O4 (task queue), O5 (cancel before provisioning starts), O7 (the Fulfillment Tasks screen, Exceptions);
  - A9 (idempotent transitions, version checks, `SELF_REPORTED` evidence with the confirming actor, and the ticket reference required for Vendor).
- Main plan M7, and the M6 follow-ups recorded there.

## Global Constraints

- Everything in the main plan's Global Constraints and in M6 applies, including:
  - lock first, then read in a new statement;
  - lock order employee → operations → grant;
  - a state change and its audit event in one transaction;
  - real time only.
- **No Manual or Vendor result is ever `VERIFIED`:** the database `CHECK` from M6, and tests.
- **Evidence:** a confirmed Manual or Vendor activation is `SELF_REPORTED`, with the confirming admin and time. A Vendor confirmation requires a recorded ticket reference.
- **Who acts on a task:** an active member of the tool's admin group, never the grant's employee (F2), and for a direct action never its initiator (D1).
- **Idempotency:** a repeat of an applied task action by the same actor returns the current state with no event (A9). A stale version is `409 version_conflict`.
- **Direct grants:**
  - always `TEMPORARY`;
  - `expiresOn` after today and at most 90 days from creation, in the company timezone;
  - never for the Super Admin's own access;
  - one submission ID per creation, so a repeat returns the original (A9).

## Decisions needed before implementation

1. **Who works a task.** Recommended:
   - **Only active members of the tool's admin group** claim, record a ticket, confirm and report failure.
   - Rationale:
     - Fulfillment Tasks shows "the user's Admin Groups" (O7).
     - D1 asks for "a different authorized admin".
     - A Super Admin cannot add themselves to a group (D2 refinement), so the person who can grant alone cannot also confirm alone.
   - **Retrying a `FAILED` task** stays as in M6: a Super Admin or a group member.
   - *Alternative:* Super Admins may also work tasks. Weaker separation.
2. **Suspend and resume tasks.** The main plan's M7 task 2 says "same for revoke / suspend / resume tasks", but `SUSPEND` and `RESUME` operations do not exist until suspension (M9, D7). Recommended:
   - M7 builds tasks on the operation type generically and tests `GRANT` and `REVOKE`.
   - M9 adds the two types and their tasks.
   - The main plan wording is updated.
3. **Direct-grant notifications (D1: "InfoSec, the beneficiary's manager and the bypassed approver").** Recommended:
   - all active InfoSec members;
   - the beneficiary's manager;
   - the tool owner when the level requires a Tool Owner step (the approver the direct grant bypasses).
   - Direct revoke is audited only; it removes access and bypasses no approval.

## My calls, listed for you to check

1. **Task lifecycle**, on the operation; the queue shows `PENDING` and `IN_PROGRESS` Manual and Vendor operations:
   - **Claim:** `PENDING → IN_PROGRESS`. Sets `claimed_by` and `first_attempt_at`. Two admins race and exactly one wins; the other gets `409 task_claimed`.
     - **Takeover:** an `IN_PROGRESS` task can be claimed again when its claimer is no longer an active member of the group. Without this, a task is stuck forever once its claimer leaves the group. The same rule covers terminated and suspended claimers in M9.
     - **Retired before provisioning:** claiming a `GRANT` task never sent locks the tool `FOR SHARE` and checks `RetiredReason`, as the runner does. A retired level or tool cancels the grant (`cancelGrant`) and the claim answers `409 level_retired`.
   - **No release action** (cut in the Ponytail review: O7 lists only record ticket, confirm and report failure). Someone who claimed by mistake reports a failure; a stuck claim is freed by takeover.
   - **Record ticket** (Vendor, by the claimer, `{ticketRef}`, at most 100 characters, trimmed, non-empty).
     - The grant then displays `WAITING_VENDOR`.
     - Recording again replaces the reference and is audited.
   - **Confirm** (by the claimer):
     - The operation becomes `SUCCEEDED`, with `completed_at` (`claimed_by` is the confirming admin).
     - A `GRANT` makes the grant `ACTIVE`, with `activation_evidence = 'SELF_REPORTED'` and `effective_from`.
     - A `REVOKE` makes it `REVOKED`, with `effective_to`.
     - Vendor without a ticket → `409 ticket_required`.
   - **Report failure** (by the claimer, `{reason}`):
     - The operation becomes `FAILED`, with `last_error` and `completed_at`.
     - The grant becomes `FAILED` or `REVOCATION_FAILED`.
     - It appears in Exceptions, which already lists every `FAILED` operation.
     - Retry (M6 endpoint, `operations.go`): back to `PENDING` with `claimed_by` and `ticket_ref` cleared (the old reference stays in its `TICKET_RECORDED` event, so a refused ticket is never reused as evidence). The grant returns to `PENDING_FULFILLMENT` for Manual and Vendor (today's code picks `PROVISIONING`, the API status) or to `REVOCATION_REQUESTED`.
2. **`WAITING_VENDOR` is a display status, not a stored grant status.** It is shown for a Vendor `GRANT` task with a ticket. This keeps the approved grant status list, and `DisplayStatus` gains the ticket.
3. **Removal of a Manual or Vendor grant uses the M6 rules unchanged:**
   - A task never claimed is cancelled.
   - Otherwise the grant becomes `REVOCATION_REQUESTED` and a `REVOKE` task is created.
   - While the grant's own `GRANT` task is claimed, the `REVOKE` task is hidden from the queue and cannot be claimed. This mirrors the runner's wait.
   - A `GRANT` task confirmed afterwards records `effective_from` and keeps `REVOCATION_REQUESTED`.
   - A `GRANT` task reported failed afterwards is cancelled, and the `REVOKE` task takes over.
   - `grantMoves` gains `PENDING_FULFILLMENT → REVOCATION_REQUESTED`, and `FAILED → PENDING_FULFILLMENT` for retry.
4. **Cancellation before work starts (O5).** `POST /request-items/{id}/withdraw` is extended to `APPROVED` items whose grant was never sent: an unclaimed task, or an API operation never claimed.
   - It is allowed for the beneficiary or the submitter, as today. The item becomes `WITHDRAWN` and the grant and operation `CANCELLED` (`ITEM_WITHDRAWN`, `GRANT_CANCELLED`).
   - Once work has started → `409 fulfillment_started`; the person gives up the access with revoke instead.
   - This updates the M6 plan's call 6 ("nothing remains for API items"): an API operation the runner has not yet claimed can also be withdrawn. It is the same never-sent check, so it costs no extra code.
   - Lock order: item, then employee, operations, grant.
5. **Direct grant:** `POST /api/v1/direct-grants {submissionId, beneficiaryId, accessLevelId, justification, expiresOn}` (Super Admin).
   - `requests.kind` gains `DIRECT_GRANT`. One item, no approval steps.
   - The same checks as Submit run:
     - an active beneficiary;
     - an active level and tool;
     - no open item or grant for the tool (`open_request_exists`, `access_exists`).
   - The item is `APPROVED` at once, with `access.CreateGrant` in the same transaction.
   - Audit: `DIRECT_GRANT_CREATED`, with the justification.
   - Errors:
     - Own access → `403 separation_of_duties`.
     - No `expiresOn` → `400 expires_required`; a past date or today → `400 expires_in_past`; more than 90 days → `400 duration_too_long`. The item is always `TEMPORARY`.
     - A non-Super Admin → `403`.
6. **Direct revoke:** `POST /api/v1/grants/{id}/revoke` also accepts a Super Admin on any grant, with the reason required as today.
   - The operation reason is `DIRECT` and the audit event is `GRANT_REVOCATION_REQUESTED` with `by: DIRECT`.
   - The initiator rule covers its task too: "initiator" is the `revocation_requested_by` of a `REVOKE`, or the submitter of a `DIRECT_GRANT` item for a `GRANT`.
7. **The Fulfillment Tasks list:** `GET /api/v1/fulfillment-tasks`, the open tasks of the actor's admin groups. Each task shows:
   - type, status and method;
   - group, tool, level and employee;
   - related request and item;
   - ticket reference, claimer, and `createdAt` (time pending is derived);
   - version.

   Tasks the actor may not work (their own grant, or a direct action they initiated) are listed but marked `canAct: false`, so the group sees the work exists. Oldest first; at most 200 (shortcut).
8. **Audit actions:** `TASK_CLAIMED` (with `takeover: true` when it replaced a former member), `TICKET_RECORDED`, `GRANT_ACTIVATED` and `GRANT_REVOKED` (with `evidence: SELF_REPORTED`, actor = the confirming admin), `OPERATION_FAILED`, `DIRECT_GRANT_CREATED`.

## Review Focus

1. **Two admins act at once** (claim and claim, confirm and fail, confirm and revoke). There is exactly one result, and the loser gets a clear `409`. Pinned in Task 2.
2. **The initiator or the beneficiary works their own task**, including after a takeover, and for a direct revoke. The answer is `403 separation_of_duties`. Pinned in Tasks 2 and 4.
3. **Vendor confirmed without a ticket, or a blank ticket.** This is rejected, and the grant is never `ACTIVE`. Pinned in Task 2.
4. **Withdraw racing the claim.** Either the withdrawal wins (`CANCELLED`, task gone) or the claim wins (`409 fulfillment_started`). It is never both. Pinned in Task 3.
5. **Membership removed while a task is claimed.** The former member can no longer act, and another member can take the task over. Pinned in Task 2.

## Files

```
internal/platform/migrations/00007_tasks.sql     operations columns, request kind DIRECT_GRANT (Task 2/4)
internal/access/toolclient_test.go               timing fix (Task 1)
internal/access/tasks.go                         task list and actions (Task 2)
internal/requests/tasks_test.go, direct_test.go  API scenario tests (Tasks 2–4), in the existing test world
internal/access/rules.go                         grantMoves, DisplayStatus(method, status, ticket) (Task 2)
internal/access/operations.go                    Retry: Manual/Vendor status, clears claim and ticket (Task 2)
internal/requests/service.go                     item fulfillment: DisplayStatus with the ticket (Task 2)
internal/access/grants.go                        Revoke: Super Admin direct revoke (Task 4)
internal/requests/decide.go                      Withdraw of approved, never-sent items (Task 3)
internal/requests/direct.go                      DirectGrant (Task 4)
internal/requests/http.go, access/http.go        routes
README.md, main plan                             docs (Task 5)
```

## Tasks

Each task follows Red → Green → Refactor: write each listed test, run it, and see it fail for the right reason before writing the code.

### Task 1: Fix the `TestApplyAndLookup` timing failure (required before the M6 + M7 PR)

- [ ] **Reproduce** (systematic debugging): run `go test -race -count=20 ./internal/access/ -run TestApplyAndLookup` while the full suite runs in parallel. Record the failure message.
- [ ] **Confirm or reject the suspected root cause.** The test's client uses the 300 ms timeout meant for the fault cases, and a loaded machine can exceed it. The same cause was fixed in the M6 runner tests.
- [ ] **Fix:** `newMock` takes the timeout as a parameter. Only the fault cases that test the timeout use 300 ms; the others use 5 s, as in production.
- [ ] **Verify:** the reproduction command passes 20 out of 20 under load. If it does not reproduce, the plan step is "no failure found in N runs", and the fix still applies, since the cause is the same as in M6.

### Task 2: Fulfillment Tasks for Manual and Vendor grants and removals

**Produces:**
- Migration `00007_tasks.sql`: `operations` gains `claimed_by uuid REFERENCES employees` and `ticket_ref text CHECK (btrim(ticket_ref) <> '' AND length(ticket_ref) <= 100)`.
- `access.Task` (the list row).
- On `*access.Service`:
  - `Tasks(ctx, actor) ([]Task, error)`;
  - `Claim`, `RecordTicket`, `Confirm` and `Fail`, each `(ctx, actor, opID, version[, text]) (Task, error)`.
- Routes `GET /fulfillment-tasks` and `POST /fulfillment-tasks/{id}/claim|ticket|confirm|fail`.
- `DisplayStatus(method, status string, ticket bool)`: `PENDING_FULFILLMENT` with a ticket shows `WAITING_VENDOR`.

- [ ] **Tests first**, in `requests/tasks_test.go`, using the world with a Manual tool (GitHub) and a Vendor tool, and a group member `fixer`:
  - `TestManualTaskEndToEnd`:
    - approve → the task is in `fixer`'s queue (`PENDING`, the related request and item, the group);
    - claim → confirm → My Access shows `ACTIVE / SELF_REPORTED` and `effectiveFrom`;
    - `activation_evidence = 'SELF_REPORTED'`, the operation's `claimed_by` and the `GRANT_ACTIVATED` actor are `fixer`;
    - the task leaves the queue.
  - `TestVendorTaskNeedsATicket`:
    - confirm before a ticket → `409 ticket_required`;
    - a blank ticket → `400`;
    - record `"VND-123"` → the item and My Access show `WAITING_VENDOR`;
    - confirm → `ACTIVE / SELF_REPORTED`, and the audit event names the ticket.
  - `TestTaskFailureAndRetry`: report failure with a reason → `FAILED` in Exceptions → retry (M6 endpoint) → `PENDING`, unclaimed and without a ticket, the grant back to `PENDING_FULFILLMENT` (never `PROVISIONING`) → claim → confirm → `ACTIVE`. Vendor variant: after retry, confirm → `409 ticket_required` until a new ticket is recorded.
  - `TestClaimSkipsRetiredLevel`: retire the level after approval → claim → `409 level_retired`, grant and operation `CANCELLED` with the reason. Once a task was claimed, retirement no longer cancels it (safe fulfillment).
  - `TestManualRemovalTask`: revoke an `ACTIVE` Manual grant → a `REVOKE` task → claim → confirm → `REVOKED / SELF_REPORTED` with `effectiveTo`.
  - `TestRevokeWhileGrantTaskClaimed`:
    - the `REVOKE` task is not listed and cannot be claimed (`409 task_not_ready`) until the `GRANT` task ends;
    - confirm the `GRANT` → `effective_from` set, the grant stays `REVOCATION_REQUESTED`;
    - the `REVOKE` task appears.
    - Variant: report the `GRANT` failed → `CANCELLED`, and the `REVOKE` task appears.
  - `TestTaskPermissions`:
    - a non-member, a Super Admin who is not a member, and the Auditor → `403`;
    - the beneficiary who is a member → `403 separation_of_duties`;
    - a non-claimer member confirms → `403 not_claimer`;
    - a member removed after claiming → `403`; another member then takes the task over (`TASK_CLAIMED` with `takeover: true`) and confirms.
  - `TestTaskConcurrency`: two members claim at once → one `200`, one `409 task_claimed`. Confirm and fail at once → one result and one event.
  - `TestTaskActionsAreIdempotent`: a repeated claim, ticket (same value) or confirm by the same actor → `200`, the same state, no second event. A stale version → `409 version_conflict`.
  - `TestNoManualResultIsVerified`: a direct `UPDATE` setting `VERIFIED` on a Manual grant → `23514`. This is a regression guard for the M6 `CHECK`.

### Task 3: Cancellation before work starts (O5)

- [ ] **Tests first:**
  - `TestWithdrawApprovedBeforeWorkStarts`: for Manual, Vendor, and an API operation never claimed. The beneficiary, or a submitter who is not the manager, withdraws → item `WITHDRAWN`, grant and operation `CANCELLED`, nothing in the queue, the Mock Tool Service never called. Then a new request for the tool is allowed.
  - `TestWithdrawAfterWorkStarted`: a claimed task, or an API operation claimed by the runner → `409 fulfillment_started`; nothing changes.
  - `TestWithdrawRacingClaim`: `WhileLocked` with the claim holding the operation → the withdrawal answers `409 fulfillment_started`. In reverse, with the withdrawal first, the claim → `409` (the task is cancelled).
  - `TestTransitions`: `APPROVED → WITHDRAWN` is now allowed in `itemMoves`, guarded by the never-sent check.

### Task 4: Direct grants and direct revoke (D1)

**Produces:**
- `requests.Service.DirectGrant(ctx, actor, DirectGrantInput) (Request, bool, error)`;
- the route `POST /direct-grants`;
- `requests.kind` gains `DIRECT_GRANT` (in migration `00007`);
- `access.Revoke` accepts a Super Admin with reason `DIRECT`.

- [ ] **Tests first:**
  - `TestDirectGrantAPI`:
    - Super Admin direct-grants Jira Viewer for 30 days → `201`, kind `DIRECT_GRANT`, item `APPROVED` with no steps, a grant `PROVISIONING`;
    - `RunOnce` → `ACTIVE / VERIFIED`;
    - audit `DIRECT_GRANT_CREATED` with the justification;
    - the same submission ID again → `200`, the original.
  - `TestDirectGrantManualInitiatorCannotConfirm`: the Super Admin is also a group member → the task shows `canAct: false`, and their confirm gets `403 separation_of_duties`. Another member confirms → `ACTIVE / SELF_REPORTED`.
  - `TestDirectGrantRules` (table):
    - for themselves → `403 separation_of_duties`;
    - no `expiresOn` → `400 expires_required`; today or a past date → `400 expires_in_past`; 91 days → `400 duration_too_long`;
    - exactly 90 days → `201`;
    - a non-Super Admin → `403`;
    - a retired level → `400 level_not_requestable`;
    - an open grant for the tool → `409 access_exists`;
    - an inactive beneficiary → `400`.
  - `TestDirectGrantNotifications`: InfoSec members, the manager, and the tool owner (for a level that requires a Tool Owner step) each get one `DIRECT_GRANT` notification; the owner gets none for a Manager-only level.
  - `TestDirectRevoke`:
    - a Super Admin revokes someone else's API grant → verified removal with reason `DIRECT`;
    - on a Manual grant → a `REVOKE` task that the same Super Admin, as a member, cannot confirm (`403 separation_of_duties`);
    - a missing reason → `400`.

### Task 5: Wiring, docs, full suite, Docker journey, reviews

- [ ] Routes in `main.go` are already registered through `access` and `requests`. Add router cases for `/api/v1/fulfillment-tasks` and `/api/v1/direct-grants`.
- [ ] Documentation:
  - The README covers tasks, evidence, withdrawal before work starts, direct grants and limitations.
  - The main plan's M7 status. Decision 2 updates the M7 and M9 wording.
- [ ] Full suite: exit 0.
- [ ] Docker journey (throwaway project):
  1. A Manual GitHub request is approved and appears in the IT Operations group's Fulfillment Tasks; it is claimed and confirmed → `ACTIVE / SELF_REPORTED`.
  2. A Vendor tool (created through the catalog API): a ticket is recorded → `WAITING_VENDOR` → confirmed.
  3. A failure is reported → it shows in Exceptions → retry → confirmed.
  4. An approved item is withdrawn before it is claimed → `CANCELLED`.
  5. A direct grant (API) → `ACTIVE / VERIFIED`, notifications sent. A direct grant (Manual): the initiator cannot confirm it; another member confirms.
  6. The Manual grant is revoked → removal task → confirmed → `REVOKED / SELF_REPORTED`.
- [ ] Superpowers `requesting-code-review` (independent) and `ponytail-review`; fix the findings; re-run.
- [ ] Report; ask for commit approval: `feat: fulfil manual and vendor access through admin-group tasks, with direct grants`.

## Acceptance criteria

- The Manual and Vendor rows of the fulfillment table behave exactly as specified:
  - `PENDING_FULFILLMENT`, then `WAITING_VENDOR` once a ticket is recorded;
  - `ACTIVE / SELF_REPORTED` with the confirming admin and time;
  - a reported failure → `FAILED` in Exceptions and retryable;
  - the same for removal.
- No Manual or Vendor result is `VERIFIED`, and a Vendor confirmation needs a ticket.
- Only an active member of the tool's admin group works a task, never the beneficiary or a direct action's initiator. Concurrent actions give one result.
- An approved item can be cancelled by its requester or submitter until work starts, and never afterwards.
- A Super Admin direct grant is temporary (≤ 90 days), never for themselves, skips only the approval steps, uses the tool's method, and notifies InfoSec, the manager and the bypassed owner. A direct revoke works for every method.
- `TestApplyAndLookup` passes reliably under load.

## Not in M7

- Suspend and resume tasks (M9, decision 2).
- Expiry, extensions and level changes (M8).
- Release of tasks on termination (M9).
- Override and direct-grant evidence reports (M12/M13).
- UI (U1 catch-up).
