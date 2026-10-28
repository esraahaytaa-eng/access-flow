# M8 — Sweeps, Expiry, Extensions and Level Changes: Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Status:** approved 2026-10-28 with decision 1 changed to end of day (see below). Branch `feat/lifecycle` from `main` (`6e998bf`).

**Goal:** Temporary access expires automatically into the normal removal workflow, and a held grant can be extended, made Until Revoked, or changed to another level of the same tool, each through the normal approval workflow, for all three fulfillment methods.

**Architecture:**
- **Sweeps:** a `sweeps` loop in the app, about once a minute, guarded by a PostgreSQL advisory lock (A6). M8 has one sweep, expiry; M9 and M12 add theirs to the same loop.
- **Removal logic:** the expiry sweep and the level change reuse M6's removal logic. Revoke's body after its locks becomes `access.requestRemoval`.
- **Request kinds:** extensions and level changes are new kinds, requested through **one endpoint**: the same level is an `EXTENSION`, another level of the tool is a `LEVEL_CHANGE` (D4: "an extension is offered"). Each has one item that names the grant it acts on (`request_items.grant_id`). Its approval steps are the level's current steps (D6, D4). The item, steps, D3 and `advance` block is extracted from `Submit` once and shared.
- **Final approval of a level change** creates the new grant with `replaces_grant_id`. The old grant's removal is created in the transaction that activates the new one (A9).
- **No new dependencies, no new screens:** the UI slice waits for the design.

**Tech Stack:** as in M6/M7.

**Spec:**
- `CASE.md` §3 ("removed when it expires").
- `DECISIONS.md`:
  - D4 (one level per tool, level change, overlap exception);
  - D6 (duration, expiry, extensions, Temporary → Until Revoked);
  - D1 (direct grants are temporary);
  - D3 (on-behalf by the manager);
  - O1, O2 (no email for `EXTENSION` / `LEVEL_CHANGE`), O7 (Exceptions: D4 overlap), O8;
  - A6 (sweeps), A9 (level changes, the employee lock, idempotent transitions, the unique indexes).
- Main plan M8; the M7 follow-ups in the main plan stay out of scope.

## Global Constraints

- Everything in M6/M7 applies, including:
  - lock first, then read in a new statement;
  - lock order employee → operations → grant;
  - a state change and its audit event in one transaction;
  - real time only (D12).
- **Every path that changes a grant locks the employee first**, including the runner's `record`, which gains the employee lock before its lease check (Ponytail finding 1). One transaction touching two grants of the same employee is then serialized by that one lock.
- **Pending change items before the employee:** a path that closes a grant (revoke, expiry) locks the pending items naming it, then the employee, the same order as approval (item → employee).
- **Dates:** the company timezone (`Europe/Istanbul`, the existing `loc`) decides what "today" is.
- **Idempotent sweeps:** a sweep catches up after downtime, and running it twice never creates a second operation or event (A6).
- **No emails** for `EXTENSION` or `LEVEL_CHANGE` (O2). In-app approval routing is unchanged.

## Decisions (approved 2026-10-28)

1. **When a temporary grant expires — approved as end of day.** Access stays valid **through the whole expiry date** in the company timezone. "Expires on 30 November" means the grant becomes due for removal on 1 December at 00:00 Europe/Istanbul.
   - The sweep removes grants with `expires_on < today`. A grant is "not yet due" while `expires_on >= today`.
   - **Direct grants (D1, at most 90 days from creation):** the access ends at 00:00 on the day after `expires_on`, and that moment must not be later than creation + 90 days. With Istanbul's fixed offset this makes the latest allowed date today + 89 (`duration_too_long` beyond it).
   - *(The recommended alternative, start of the expiry date, was not chosen.)*
2. **An item approved after its expiry date — approved.** A request item can wait for approval past the date the requester chose. **Final approval cancels it** when `expires_on < today` (`CANCELLED`, "expired before approval"), before any grant exists, in the same place as the retired check.
   - *Alternative:* provision it and let the sweep remove it a minute later. This makes a pointless tool call and briefly gives access that has already expired.
3. **When the overlap exception appears (D4) — approved.** From the **first failed attempt** of the old level's removal until that removal is verified or confirmed. Defined by the data, whatever the removal's reason: the old grant has an open `REVOKE` with `last_error` set, and a grant with `replaces_grant_id` = the old grant is `ACTIVE`.
   - A removal still pending normally (a runner retry not yet due, a Manual task not yet done) is ordinary work and not listed (O7).
   - *Alternative:* only while the removal is `FAILED`. Then an overlap disappears from Exceptions during a retry that has not succeeded.

4. **Extending a direct grant (D1: "always temporary, maximum 90 days") — approved.** **Allowed only through the normal workflow**, which re-runs the level's approval steps (D6). It may be extended to a later date or to Until Revoked, because the approvals the direct grant bypassed then apply. The 90-day limit stays on the direct grant itself.
   - *Alternative:* a direct grant can never be extended; the employee files a new request after it ends.

5. **Closing a grant cancels its pending extension or level change — approved** (My calls 2).

## My calls (approved with the plan)

1. **Who may ask for an extension or a level change:** the grant's employee, or their current direct manager on their behalf (D3: the manager's submission satisfies the Manager step). Only for a grant that is `ACTIVE`. Anyone else gets `404`, as with revoke.
2. **Extension rules:**
   - Only for a temporary grant.
   - The new duration is a later date, or Until Revoked; otherwise `400 not_an_extension`.
   - The grant stays `ACTIVE` while the extension waits.
   - Final approval updates the grant's `expires_on` (`GRANT_EXTENDED`, old and new dates). There is no operation and no tool call.
   - At final approval the grant must still be `ACTIVE` and not yet due (`expires_on` is null or `>= today`). Otherwise the item is `CANCELLED` ("the access has ended"), so an approval in the minute before the sweep cannot save a grant that is already due.
   - Expiry is never held back by a pending extension (D6).
   - When a grant is closed (revoked, expired or cancelled), its pending extension or level-change item is cancelled in the same transaction ("the access has ended"). It never blocks the next request through `request_items_one_open`.
   - A direct grant: decision 4.
3. **Level change rules:**
   - The new level belongs to the same tool, is not the held level, and is active. It is approved under the new level's steps (D4) with its own duration.
   - Final approval creates the new grant (`replaces_grant_id` = old) and its `GRANT` operation, as `CreateGrant` does.
   - When the new grant becomes `ACTIVE` (runner verification or task confirmation), the same transaction requests the old grant's removal (reason `LEVEL_CHANGE`, priority 1), unless the old grant is already being removed or is gone.
   - If the new grant fails or is cancelled, the old grant is untouched (D4).
   - If the old grant has stopped being `ACTIVE` before final approval, the item is `CANCELLED` ("the access has ended"); usually the closing path has already cancelled it.
   - The old grant expiring or being revoked while the new one is in progress is the normal removal; the new grant continues.
4. **One open request per person and tool** (the M5 index `request_items_one_open`) already allows only one pending extension or level change per tool.
   - Submit's `access_exists` check is skipped only for the grant named by an extension or level change.
   - Outside a level change, a second level on the tool is still rejected (`409 access_exists`).
   - A new unique index, `grants_one_open_change` on `replaces_grant_id` for open grants, guards the database.
   - `CreateGrant` copies `i.grant_id` into `replaces_grant_id` (NULL for other kinds), with no branch on the kind. `queueEmail` is called only from `Submit`, so O2 already holds with no kind check.
5. **Expiry sweep:**
   - It reads the due grant IDs, then expires each in its own transaction. After the locks it **re-reads the grant** and continues only if it is still open, not being removed, and `expires_on < today` (an extension approved in between wins). It uses `requestRemoval` with reason `EXPIRED`, priority 1, and a nil (system) actor.
   - A grant never sent is cancelled ("expired"). A claimed or failed grant gets a removal, as with revoke.
   - Notifications: none (D6: "no expiry reminders"); the audit event and My Access show it.
6. **The sweeps loop** lives in `internal/sweeps` (A8).
   - Each round takes `pg_try_advisory_xact_lock` in a short transaction and reads the due IDs there. If another instance holds it, the round is skipped. The lock only skips duplicate rounds; correctness comes from the employee lock, the re-check and `operations_one_open_revoke`.
   - Each grant is expired in its own transaction, so one bad row cannot block the others.
   - It runs at startup and then every minute, and stops before the pool closes, like the runner.
7. **Display and history:**
   - An `APPROVED` extension item's fulfillment shows the extended grant; a pending or rejected one shows none.
   - A level-change item's fulfillment shows the new grant.
   - My Access shows both grants during a change.
   - Nothing is rewritten: the old grant keeps its `effective_from`/`effective_to` and the new grant has its own (D4).

## Review Focus

1. **The sweep runs twice, or on two instances at once.** The result is one removal and one event per grant. Pinned in Task 2.
2. **The old level is removed before the new level is verified or confirmed.** This must never happen, for API and for Manual/Vendor. Pinned in Task 4.
3. **An extension or level change is approved after the grant already ended.** The item is cancelled; no zombie grant is extended or created. Pinned in Tasks 3 and 4.
4. **Expiry and an approval race.** Both lock the employee, so either the expiry or the approval comes first, never a mix. Pinned in Task 3.
5. **A second level of the tool through plain Submit, or a second level change for the tool.** These are rejected (`access_exists`, `open_request_exists`). Pinned in Task 4.

## Files

```
internal/platform/migrations/00008_lifecycle.sql   request kinds, request_items.grant_id, grants.replaces_grant_id, index (Tasks 3–4)
internal/access/rules.go                           Today(now, loc), used by requests' ParseDuration and DirectExpiry too (Task 1)
internal/access/grants.go                          requestRemoval (from Revoke), ExpireGrant, DueForExpiry (Tasks 1–2)
internal/access/runner.go                          record locks the employee; succeed: level-change removal on activation (Tasks 1, 4)
internal/access/operations.go                      Exceptions overlap list (Task 4)
internal/sweeps/sweeps.go, sweeps_test.go          loop with advisory lock (Task 2)
internal/requests/change.go, change_test.go        Change (one endpoint, both kinds); finish for both kinds (Tasks 3–4)
internal/requests/decide.go, service.go, rules.go  finish dispatch, expired-before-approval, checkLevel exception, the shared item/steps block
cmd/accessflow/main.go                             start the sweeps loop (Task 2)
README.md, main plan                               docs (Task 5)
```

## Tasks

Each task follows Red → Green → Refactor: write each listed test, run it, and see it fail for the right reason before writing the code.

### Task 1: Expiry rule and the shared removal

**Produces:**
- `access.Today(now time.Time, loc *time.Location) time.Time`: the company-timezone date. The sweep and `finish` compare `expires_on < today` in SQL (decision 1). `ParseDuration` and `DirectExpiry` switch to it.
- `DirectExpiry` enforces the end-of-day 90-day limit: the end of access, 00:00 on the day after `expires_on`, must not be after `now + 90 days`. `TestDirectExpiry` and `TestDirectGrantRules` change: today + 89 is accepted, today + 90 is `duration_too_long`.
- `record` locks the employee (`c.employee`) before its lease check.
- `requestRemoval(ctx, tx, now, actor *uuid.UUID, grant, employee uuid.UUID, by, reason string) error`, called after the employee and operation locks. It is Revoke's current body after the permission and version checks: cancel a grant never sent, otherwise cancel a queued or failed `GRANT` operation and queue a `REVOKE`. It returns nil for a grant already in removal or closed.

- [ ] **Tests first:**
  - `TestToday` (table): 21:30 UTC on 1 March is 2 March in Istanbul; 20:59 UTC is still 1 March. `TestParseDuration` and `TestDirectExpiry` stay green.
  - `TestRunnerWaitsForTheEmployee`: `WhileLocked` holds the employee row → the runner's `record` waits, then records (no deadlock with a concurrent revoke of the same grant).
  - The existing revoke tests (`TestVoluntaryRevokeEndToEnd`, `TestRevokePermissionMatrix`, the M6/M7 removal tests) must stay green through the refactor. There is no new behavior in this task.

### Task 2: The expiry sweep and the sweeps loop

**Produces:**
- `access.ExpireDue(ctx, pool, loc) (int, error)`: runs one round, returns how many grants it expired.
- `sweeps.New(pool, loc) *Sweeps` with `Run(ctx)` and `RunOnce(ctx) error`. It is started in `main.go` next to the runner.

- [ ] **Tests first** (real PostgreSQL; past dates are set with SQL, the clock stays real):
  - `TestExpirySweepRemovesTemporaryAccess`, for API, Manual and Vendor:
    - an `ACTIVE` grant whose `expires_on` was yesterday → `REVOCATION_REQUESTED`, a `REVOKE` operation with reason `EXPIRED` and priority 1, and `GRANT_REVOCATION_REQUESTED` with `by: EXPIRED` and no actor;
    - API: drain → `REVOKED / VERIFIED`. Manual: a removal task.
  - `TestExpirySweepIsIdempotent`: `RunOnce` twice one after the other, and twice concurrently in goroutines → one operation and one event.
  - `TestExpirySweepSkipsWhileAnotherHoldsTheLock`: an advisory lock held by a test transaction → `RunOnce` expires nothing. After release it expires the grant.
  - `TestExpiryOfAGrantNeverSent`: a `PROVISIONING` grant never claimed and expired → `CANCELLED`, reason "expired". A `FAILED` grant → a removal.
  - `TestUntilRevokedAndFutureGrantsAreKept`: a grant with no expiry, or expiring today (valid through the day) or tomorrow, is untouched.
  - `TestExpiryAndExtensionSerialize`: `WhileLocked` holds the employee and, in that transaction, moves the grant's `expires_on` to next month, as an extension approval does. The sweep, which already read the due list, waits; after the commit the grant stays `ACTIVE` with no `REVOKE` operation.
  - `TestClosingAGrantCancelsItsPendingChange`: dev's pending extension, and in another case a pending level change; the grant expires (sweep) or is revoked → the item is `CANCELLED` ("the access has ended") in the same transaction, and a new request for the tool is accepted.

### Task 3: Extensions and Temporary → Until Revoked (D6)

**Produces:**
- Migration `00008`:
  - `requests.kind` gains `EXTENSION` and `LEVEL_CHANGE`;
  - `request_items.grant_id uuid REFERENCES grants`, required for those two kinds (a `CHECK` through the kind is not possible on the item, so the service enforces it and the column is documented).
- `requests.Service.Change(ctx, actor, grantID uuid.UUID, in ChangeInput) (Request, bool, error)` with `ChangeInput{SubmissionID, AccessLevelID, Justification, Duration}`. The held level is an `EXTENSION`; another level of the same tool is a `LEVEL_CHANGE` (Task 4).
- Route `POST /api/v1/grants/{id}/change`.
- The item, steps, D3 and `advance` block is extracted from `Submit` and used by Submit and Change.
- `finish` for an `EXTENSION` item updates the grant's expiry instead of calling `CreateGrant`.
- Final approval of any kind cancels an item whose expiry date has been reached (decision 2).

- [ ] **Tests first:**
  - `TestExtensionKeepsAccessActive`:
    - dev's temporary Jira Viewer is `ACTIVE`; extend to a later date → `201`, kind `EXTENSION`, the level's current steps;
    - the grant stays `ACTIVE / VERIFIED` while pending;
    - the manager approves → the grant's `expiresOn` is the new date, `GRANT_EXTENDED` with both dates;
    - no new operation, no tool call, no Mock Email Outbox message.
  - `TestExtensionToUntilRevoked`: duration `UNTIL_REVOKED` → `expiresOn` null after approval.
  - `TestExtensionRules`:
    - an until-revoked grant, or an earlier or equal date → `400 not_an_extension`;
    - someone else's grant → `404`;
    - a grant not `ACTIVE` → `409 grant_not_active`;
    - approval on the day after the expiry date before the sweep ran → item `CANCELLED`, and the grant is not extended; approval on the expiry date itself extends it;
    - a second pending extension → `409 open_request_exists`;
    - the manager on behalf → the Manager step is `APPROVED_BY_SUBMISSION`;
    - the same submission ID → the original.
  - `TestExtensionUsesCurrentSteps`: the level now requires InfoSec → the extension needs the Manager and InfoSec steps.
  - `TestGrantExpiresWhileExtensionPending`: the sweep removes the grant; approving the extension afterwards → item `CANCELLED`, "the access has ended", and the grant is not changed.
  - `TestItemApprovedAfterItsExpiry`: a `NEW_ACCESS` item whose `expires_on` is set to yesterday before the final approval → `CANCELLED`, "expired before approval", and no grant; set to today → approved and provisioned.

### Task 4: Level changes (D4)

**Produces:**
- Migration `00008`: `grants.replaces_grant_id uuid REFERENCES grants` and the unique index `grants_one_open_change` on `replaces_grant_id` where the status is not `REVOKED` or `CANCELLED`.
- `Change` (Task 3) with another level of the tool.
- `access.CreateGrant` copies `i.grant_id` into `replaces_grant_id`.
- `succeed` (runner and Manual/Vendor confirm) requests the old grant's removal when a replacing grant becomes `ACTIVE`.
- `GET /api/v1/exceptions` gains `overlaps`: tool, employee, old and new level, and the old removal's last error (decision 3).

- [ ] **Tests first:**
  - `TestLevelChangeAPI`:
    - dev holds Jira Viewer `ACTIVE`; change to Jira Admin → the Admin level's steps (Manager, Tool Owner, InfoSec);
    - after approval, Viewer is still `ACTIVE` and Admin is `PROVISIONING`;
    - drain → Admin is `ACTIVE / VERIFIED` and Viewer goes to `REVOCATION_REQUESTED` (reason `LEVEL_CHANGE`) and then `REVOKED / VERIFIED`;
    - the mock holds only Admin; the account remains;
    - Viewer's `effective_to` is not earlier than Admin's `effective_from`.
  - `TestLevelChangeManual`:
    - the old level is still held until the new task is confirmed;
    - then the removal task appears for the old level;
    - its confirmation → `REVOKED / SELF_REPORTED`.
  - `TestLevelChangeNewGrantFails`: the new grant fails (a `409` from the tool, or a reported task failure) → the old grant is `ACTIVE` and unchanged, no removal exists, and the failure is in Exceptions and retryable.
  - `TestLevelChangeOverlap`:
    - the old removal fails (fault) → `overlaps` lists the tool and both levels;
    - after a retry that succeeds → it leaves `overlaps`.
    - A removal still pending normally is not listed.
    - The old grant revoked by dev (reason `VOLUNTARY`) while the new one provisions, then that removal fails → still listed (the overlap is defined by `replaces_grant_id`, not by the reason).
  - `TestLevelChangeRules`:
    - the same level, another tool's level, or a retired level → `400`;
    - someone else's grant → `404`;
    - while another change or extension is pending → `409 open_request_exists`;
    - plain Submit of a second level of the same tool → `409 access_exists`;
    - a direct `INSERT` of a second open grant replacing the same grant → `23505`.
  - `TestLevelChangeAfterOldGrantEnded`: dev revokes the old grant before approval → the pending item is `CANCELLED` ("the access has ended") by the revoke.
  - `TestOldGrantRemovedDuringChange`: the old grant is revoked while the new one is `PROVISIONING` → the new grant becomes `ACTIVE`; no second removal is created.
  - `TestNoEmailForExtensionOrLevelChange`: neither kind writes to the Mock Email Outbox (O2).

### Task 5: Wiring, docs, full suite, Docker journey, reviews

- [ ] Router case for `POST /api/v1/grants/{id}/change` in `cmd/accessflow/main_test.go` (unauthenticated → `401`; the table gains a method column).
- [ ] Documentation:
  - the README covers expiry, extensions, level changes, overlaps and limitations;
  - the main plan's M8 status;
  - DECISIONS.md gets dated refinements for decisions 1–5.
- [ ] Full suite: exit 0.
- [ ] Docker journey (throwaway project):
  1. A temporary Jira Viewer is set to expire yesterday with SQL → within about a minute it is `REVOKED / VERIFIED` through the real sweep loop.
  2. Extension approved → new expiry, access kept.
  3. Level change Viewer → Admin, API → the new level is verified, then the old one is removed.
  4. A level change with a removal fault → overlap in Exceptions → retry → gone.
  5. A Manual level change: the old level stays until the task is confirmed.
- [ ] Superpowers `requesting-code-review` (independent) and `ponytail-review`; fix the findings; re-run.
- [ ] Report; ask for commit approval: `feat: expire temporary access and support extensions and level changes`.

## Acceptance criteria

- Temporary grants of every method enter the normal removal workflow when their expiry date is reached. A sweep run twice, or concurrently, creates one removal.
- An extension, or a change to Until Revoked, re-runs the level's current approval steps and keeps the access active meanwhile.
- A level change is approved under the new level's steps. The new level is verified or confirmed first, and only then is the old level's removal created. A failed new level keeps the old one. A failed old-level removal shows as an overlap in Exceptions until the removal is verified or confirmed.
- Outside a level change, a second level of a tool is rejected.
- No email is sent for `EXTENSION` or `LEVEL_CHANGE`.

## Not in M8

- Termination and suspension sweeps (M9).
- Review campaign sweeps (M12).
- Transfers (D5).
- UI (the design catch-up).
- The M7 follow-ups listed in the main plan.
