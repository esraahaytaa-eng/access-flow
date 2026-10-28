# M5 — Requests and Approvals: Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Status:** approved 2026-10-28 with decision 1 (no self-edit) and decision 2 (one open item per person and tool), and the calls below including the submitter rule. Recorded in DECISIONS.md. Branch `feat/catalog-requests` (shared with M4, one PR).

**Goal:**
- An employee, or someone on their behalf, requests one or more tool levels, each with a justification and a duration.
- The right people approve or reject each item in the fixed order, Manager → Tool Owner → InfoSec, each step by a different person. Blocked steps are re-routed visibly.
- Super Admins can override a step; InfoSec and Super Admins can reassign one; requesters can withdraw items.
- An item that passes every step ends `APPROVED`. Fulfillment starts in M6.

**Architecture:** a new `internal/requests` package with the same layout as `catalog`:
- `rules.go`: pure routing, separation of duties, durations and transitions, with explicit time;
- `service.go`: transactions with SQL inline;
- `http.go`: handlers.

It reads the catalog and employees with SQL in its own transactions and calls `notify` and `audit` inside them. The Mock Email Outbox is one table, written by `notify` (O2). No new dependencies.

**Tech Stack:** as in M2–M4.

**Spec:** `CASE.md` §1–2; `DECISIONS.md` F1, F2, F3, D1 (override only; direct grants are M7), D3, D4, D6, D9, O2, O5, O7, A4, A6 (company timezone), A9, A10. Main plan: the M5 section and Review Focus 1, 2 and 5.

## Global Constraints

- Everything in the main plan's Global Constraints applies.
- Approvals are always made by authorized humans; nothing is approved automatically or by email (F2, O2). `APPROVED_BY_SUBMISSION` (D3) is the only step completed by an action other than a decision. It is recorded as such, with the submitting manager.
- Each step on one item is decided by a different person. The beneficiary never decides a step on their own item (F2).
- Steps are snapshotted at submission: the item's step rows are created from the level's flags at that moment (F2). Stricter-wins at provisioning is M6.
- A9:
  - The beneficiary's employee row is locked for submission.
  - Transitions are conditional updates on the expected status and version.
  - A repeated submission ID returns the original result.
  - A repeated transition returns the current state with no new audit event.
- First decision wins. The losing attempt gets `409` and an `APPROVAL_ATTEMPT_REJECTED` audit event written in its own transaction, so it survives the conflict (main plan Review Focus 1).
- Reasons are required for reject, override, reassign and withdraw. Justification is required on submission. All are plain text, at most 1000 characters (`platform.CheckText`).
- Visibility: an employee reads only requests where they are the beneficiary or the submitter, plus steps in their own inbox. InfoSec and Super Admin also see unroutable steps. Enforced in SQL (Review Focus 5).
- API errors: `{"error", "field"?}` (A7).

## Decisions needed before implementation

1. **May someone change their own manager, department or job title?** This is the open question recorded before M5. Today an HR Manager or Super Admin can edit their own employee record, so an HR Manager could pick who approves their own requests (the Manager step).
   - **Recommended:** nobody edits their own employee record. A self-edit returns `403 self_change_forbidden`, as for roles, groups and resets. Another HR Manager or Super Admin makes the change. This is a three-line rule in `people.UpdateEmployee` with one test, done as Task 1. A lone bootstrap Super Admin can still create an HR Manager to do it.
   - **Alternative:** allow it and rely on the audit trail. This is weaker, because the edit and the approval it enables would both look legitimate.
2. **One open request per person and tool, or per person, tool and level?** A9 lists the unique index as "(person, tool, level)".
   - **Recommended:** per (person, tool). With per-level, a person could have Jira Viewer and Jira Admin pending at once; both could be approved, breaking D4 (one level per tool). A different level of a tool already held is a `LEVEL_CHANGE` in M8, so nothing legitimate is blocked. This would be recorded in A9 with a dated note.
   - **Alternative:** keep A9 literally and leave the D4 check to M6 provisioning.

## My calls, listed for you to check

Each follows from F2, D3, D4, D6, D9 or O2. None changes an approved decision.

1. **Request shape:**
   - One submission has one beneficiary and one or more items.
   - Each item names an access level and carries its own justification and duration.
   - Approvers see the reason for that tool, and rejection is per item (F3).
   - The client sends a `submissionId` (UUID). Repeating it returns the original request, even if the body differs, but only to the same submitter. Anyone else gets `409 submission_id_reused`, so an ID never reveals another person's request.
2. **Who may submit:** any employee with a full session, for themselves or on behalf of any `ACTIVE` employee (D3, literally).
   - When the submitter is the beneficiary's current direct manager, the Manager step is `APPROVED_BY_SUBMISSION`.
   - Any other submitter gets the normal Manager step.
   - **Stricter than F2, for you to confirm:** an on-behalf submitter who is not the manager may not decide any step on that item. Requesting access for someone and then approving it is the same self-dealing F2 blocks for beneficiaries.
3. **Requestable and duplicates:**
   - The level and its tool must be `ACTIVE`.
   - One beneficiary may have at most one open item per **tool**, whatever the level (decision 2). A second one returns `409 open_request_exists` with the open item's ID.
   - Checking already-held grants, and offering an extension, start in M6/M8 when grants exist.
   - The same tool twice in one submission returns `400 duplicate_tool`.
4. **Durations (D6):**
   - `{"type": "UNTIL_REVOKED"}` or `{"type": "TEMPORARY", "expiresOn": "YYYY-MM-DD"}`. The date must be after today in the company timezone.
   - The company timezone comes from `COMPANY_TIMEZONE`, default `Europe/Istanbul` (A6). `time/tzdata` is embedded so the distroless image always has the zone (standard library).
   - There is no upper limit (D6: no duration limits). What the date means at grant time is M8.
5. **Sequential steps:** only the first undecided step is open (`PENDING`); later ones wait (`WAITING`). Each step is routed when it opens, and the routing reason is stored and shown (F2: re-routed visibly).
   - **Manager:** the beneficiary's manager → that manager's manager → the InfoSec pool. A person is skipped when they are not `ACTIVE`, are the beneficiary, or already decided a step on the item.
   - **Tool Owner:** the owner, unless excluded in the same way, → the InfoSec pool.
   - **InfoSec:** the pool of `ACTIVE` InfoSec members. Who is eligible is evaluated when someone decides, so an earlier decider or the beneficiary is never eligible.
   - **Nobody eligible:** the step is `PENDING` and `unroutable`: it has no assignee and no pool, or it is in the InfoSec pool and no `ACTIVE` InfoSec member outside the item's excluded set exists. This is computed in the query, not stored, so it follows role changes. It is listed for InfoSec and Super Admin, and an override resolves it (D1, F2).
   - Re-routing pending steps when an assignee is later terminated or suspended is M9. Until then an assignee who leaves keeps the step, and InfoSec can reassign it (D9).
6. **Decisions:** approve, or reject with a reason.
   - Rejecting makes the item `REJECTED` (final, O5) and its waiting steps `CANCELLED`. Other items in the submission are unaffected.
   - Approving the last step makes the item `APPROVED`.
   - The Auditor role alone gives no approval right. An auditor still decides steps routed to them as a manager or Tool Owner, because that comes from the relationship, not the role.
7. **Reassign (D9):** InfoSec or a Super Admin moves a `PENDING` step to a named person, with a reason (`REASSIGNED`).
   - The target must be `ACTIVE` and must not be excluded by separation of duties.
   - For an InfoSec step the target must hold `INFOSEC`.
   - Nobody reassigns a step on their own item.
8. **Override (D1):** a Super Admin decides any `PENDING` step as `SUPER_ADMIN_OVERRIDE`, with a reason, never on their own item.
   - The record keeps the original step and its assignee or pool.
   - InfoSec, the beneficiary's manager and the bypassed assignee are notified in-app.
   - An override cannot reverse a rejection; it applies only to items that are still `PENDING_APPROVAL`.
9. **Withdraw (O5):** the beneficiary or the submitter cancels a `PENDING_APPROVAL` item, with a reason. The item becomes `WITHDRAWN` and its open steps `CANCELLED`. O5 also allows withdrawal of an `APPROVED` item before provisioning starts; provisioning begins in M6, so M6 adds that transition.
10. **Mock Email Outbox (O2):** exactly one row per `NEW_ACCESS` submission with at least one item whose Manager step went to a person (the manager, or the manager's manager), addressed to that person.
    - No email when the Manager steps were `APPROVED_BY_SUBMISSION`, or when they went to the InfoSec pool (O2: no InfoSec emails).
    - No API sends email; the outbox is a table that Audit and InfoSec can read (`GET /api/v1/email-outbox`).
11. **Inbox:** `GET /api/v1/approvals` returns the caller's `PENDING` steps: those assigned to them, plus pool steps for InfoSec members who are eligible for that item. Each carries the item's beneficiary, tool, level, steps, justification, duration and `waitingSince`.

## Review Focus

1. **Concurrent decisions:** two approvers, or the same approver twice, decide one step at once. There is exactly one decision, one transition and one `STEP_APPROVED` event, and the loser gets `409` plus a surviving `APPROVAL_ATTEMPT_REJECTED`. Pinned in Task 4.
2. **Duplicate submission:** the same `submissionId` sent twice, including at the same time, gives one request, one audit event and one email. Pinned in Task 3.
3. **Separation of duties across steps:** the manager is also the Tool Owner, so the Tool Owner step goes to InfoSec. The same InfoSec member cannot decide both InfoSec steps. Pinned in Tasks 2 and 4.
4. **Reading others' requests:** an employee who is neither beneficiary nor submitter gets `404` (not `403`, to avoid an existence leak) for someone's request, and it is absent from lists. Pinned in Task 3.
5. **Decisions that land on a changed item:** approve after a withdrawal, approve after a rejection of an earlier step, override after a rejection. Each returns `409`, with no state change. Pinned in Tasks 4–5.

## Files

```
internal/people/service.go, *_test.go          self-edit rule (Task 1)
internal/platform/config.go                    COMPANY_TIMEZONE (Task 1)
internal/platform/migrations/00005_requests.sql requests, request_items, approval_steps, email_outbox (Task 1)
internal/requests/rules.go                     Route, eligibility, ValidateDuration, transitions (Task 2)
internal/requests/service.go                   Submit, Get, List (Task 3)
internal/requests/decide.go                    Inbox, Approve, Reject, Withdraw, Reassign, Override, Unroutable (Tasks 4–5)
internal/requests/http.go                      handlers, Register (Tasks 3–5)
internal/notify/notify.go                      ToEmployee, Email (Tasks 3, 5)
cmd/accessflow/main.go                         mount routes; tzdata import (Tasks 1, 3)
README.md, docs/plans/…                        API, rules, status (Task 6)
```

---

### Task 1: Self-edit rule, schema and company timezone

**Produces:**
- `people.UpdateEmployee` returns `403 self_change_forbidden` when the actor is the employee (decision 1).
- `platform.Config.CompanyTimezone *time.Location`, from `COMPANY_TIMEZONE`, default `Europe/Istanbul`. An unknown zone is a startup error.
- Migration `00005_requests.sql`:
  - `requests (id, submission_id uuid UNIQUE, beneficiary_id, submitted_by, kind CHECK 'NEW_ACCESS', created_at)`.
  - `request_items (id, request_id, access_level_id, tool_id, justification, duration_type CHECK ('TEMPORARY','UNTIL_REVOKED'), expires_on date, status CHECK ('PENDING_APPROVAL','APPROVED','REJECTED','WITHDRAWN'), version, decided_at)`.
    - CHECK: `expires_on` is set exactly when the type is `TEMPORARY`.
    - Unique partial index on `(beneficiary_id, tool_id) WHERE status = 'PENDING_APPROVAL'`; `beneficiary_id` is copied onto the item for this index.
  - `approval_steps (id, item_id, position, type CHECK ('MANAGER','TOOL_OWNER','INFOSEC'), status CHECK ('WAITING','PENDING','APPROVED','REJECTED','APPROVED_BY_SUBMISSION','SUPER_ADMIN_OVERRIDE','CANCELLED'), assignee_id NULL, pool text NULL CHECK (pool = 'INFOSEC'), routing_note text, decided_by, decided_at, reason, opened_at, version)`.
    - Unique on `(item_id, position)`.
    - CHECK: a `PENDING` step does not have both `assignee_id` and `pool` set.
  - `email_outbox (id, recipient_id, subject, body, request_id, created_at)`.
  - Grants `SELECT, INSERT, UPDATE`; no `DELETE`.
- [ ] Tests first:
  - `TestNobodyEditsTheirOwnRecord` (people);
  - `TestCompanyTimezoneConfig` (default, valid, invalid);
  - `TestRequestsSchema`: the duration CHECK, a second open item for the same tool → `23505`, no `DELETE` → `42501`.
- [ ] Implement; run `./internal/people/ ./internal/platform/`.

### Task 2: Pure rules: routing, separation of duties, durations, transitions

**Produces** (all pure, table-tested):
- `Candidate{ID uuid.UUID; Active bool}`.
- `RouteManager(beneficiary uuid.UUID, manager, managersManager *Candidate, excluded map[uuid.UUID]bool) Route` and `RouteToolOwner(owner Candidate, beneficiary uuid.UUID, excluded map[uuid.UUID]bool) Route`.
  - `Route{AssigneeID *uuid.UUID; Pool string; Note string}`.
  - Notes: `"manager"`, `"manager inactive → manager's manager"`, `"no eligible manager → InfoSec"`, `"tool owner"`, `"tool owner is the beneficiary → InfoSec"`, `"tool owner already decided a step → InfoSec"`, `"tool owner inactive → InfoSec"`.
- `Excluded(beneficiary, submitter uuid.UUID, submitterIsManager bool, deciders []uuid.UUID) map[uuid.UUID]bool`: the beneficiary, every earlier decider, and the submitter unless they are the manager (call 2).
- `CanDecide(actor auth.Principal, step Step, excluded map[uuid.UUID]bool) error`:
  - `forbidden` unless the actor is the assignee, or the step is in the InfoSec pool and the actor holds `INFOSEC`;
  - `separation_of_duties` when the actor is excluded.
- `ValidateDuration(d Duration, now time.Time, loc *time.Location) error`: `invalid_duration` or `expiry_not_in_future`.
- `next(steps []Step) (open *Step, done bool)`.
- [ ] Tests first:
  - `TestRouteManager` (table): active manager; terminated manager with an active manager's manager; both inactive; no manager; manager is excluded as an earlier decider; manager's manager is the beneficiary.
  - `TestRouteToolOwner` (table): every note above.
  - `TestExcluded`: on-behalf by the manager versus by someone else.
  - `TestCanDecide` (table): assignee; another person; InfoSec pool member; InfoSec member excluded as the earlier decider; beneficiary; Auditor role alone on a pool step.
  - `TestValidateDuration`: tomorrow is ok; today and yesterday are rejected; 23:30 Istanbul "today" against UTC; malformed date; `TEMPORARY` without a date; `UNTIL_REVOKED` with a date.
- [ ] Implement; run `./internal/requests/`.

### Task 3: Submit, read, email outbox

**Routes:**
- `POST /api/v1/requests` `{submissionId, beneficiaryId?, items: [{accessLevelId, justification, duration}]}` → `201 {request}`, or `200 {request}` for a repeat.
- `GET /api/v1/requests` returns my requests (as beneficiary or submitter), newest first.
- `GET /api/v1/requests/{id}` returns a request with its items and steps.
- `GET /api/v1/email-outbox` (InfoSec, Super Admin, Auditor).

**Behaviour:**
- One transaction:
  1. Lock the beneficiary row (A9).
  2. Re-check the submission ID after the lock (a unique violation on `submission_id` from a racing submission for another beneficiary is rolled back and answered by re-reading the original in a new transaction), then validate every item: an active level and tool, no open item for the tool, and a valid duration.
  3. Create the request, its items and all steps.
  4. Mark the Manager step `APPROVED_BY_SUBMISSION` when the submitter is the beneficiary's current manager.
  5. Route and open the first undecided step.
  6. Write `REQUEST_SUBMITTED` (one event, with item IDs) and the email (call 10).
- One invalid item rejects the whole submission, naming the field (`items[2].duration`).
- Errors:
  - `400`: `beneficiary_not_active`, `items_required` (at most 20 items), `level_not_requestable`, `duplicate_tool`;
  - `409 open_request_exists`;
  - `404` for someone else's request.
- [ ] Tests first:
  - `TestSubmitJiraViewerNeedsOnlyManager`;
  - `TestSubmitAWSAdminHasThreeSteps`;
  - `TestOnBehalfByManagerIsApprovedBySubmission` (no email);
  - `TestOnBehalfByOtherNeedsManager` (one email to the manager);
  - `TestOneEmailPerSubmission` (three items → one email);
  - `TestNoEmailWhenManagerStepGoesToInfoSec`;
  - `TestRepeatedSubmissionIDReturnsTheOriginal` (sequential, 10 concurrent, and concurrent with different beneficiaries → one request, one event, one email, never a 500);
  - `TestSubmissionIDOfAnotherSubmitterIsRejected` (`409 submission_id_reused`, no request content);
  - `TestOpenItemPerToolIsUnique` (a different level of the same tool → 409);
  - `TestRetiredLevelNotRequestable`;
  - `TestInvalidItemRejectsTheWholeSubmission`;
  - `TestCannotReadOthersRequests`;
  - `TestSubmitSeesARetirementCommittedWhileWaiting` (`testutil.WhileLocked` on the tool row);
  - `TestTemporaryPasswordSessionCannotSubmit`.
- [ ] Implement; run.

### Task 4: Inbox and decisions

**Routes:**
- `GET /api/v1/approvals` (inbox);
- `POST /api/v1/approvals/{stepId}/approve` `{version}`;
- `POST /api/v1/approvals/{stepId}/reject` `{version, reason}`.

**Behaviour:**
- One transaction locks the item, then reads its steps in a new statement (the M3 pattern).
- It checks that the item is `PENDING_APPROVAL`, the step is `PENDING` with the expected version, and `CanDecide` passes. Then it decides the step, routes and opens the next one or finishes the item, and writes `STEP_APPROVED`/`STEP_REJECTED` plus `ITEM_APPROVED`/`ITEM_REJECTED`.
- When the same actor repeats a decision already applied, the current state is returned with no event.
- A stale or conflicting attempt is answered `409 step_already_decided` or `409 version_conflict`, and `APPROVAL_ATTEMPT_REJECTED` is written in a separate transaction after the rollback.
- The inbox query applies eligibility in SQL. The pool steps an InfoSec member sees exclude items where they are the beneficiary or an earlier decider.
- [ ] Tests first:
  - `TestAWSAdminNeedsThreeDifferentPeople` (CASE flow: manager → owner → InfoSec → `APPROVED`);
  - `TestManagerWhoIsToolOwnerGoesToInfoSec` (two InfoSec steps by two members);
  - `TestBeneficiaryCannotApprove` (also not via the InfoSec pool);
  - `TestOnBehalfSubmitterCannotApprove`;
  - `TestRejectNeedsAReasonAndIsFinal` (a sibling item is unaffected);
  - `TestConcurrentApprovals` (10 goroutines, `-race`: one wins, one event, the losers are audited);
  - `TestRepeatedApproveIsIdempotent`;
  - `TestInboxShowsOnlyMySteps`, with waiting time;
  - `TestApproveSeesAWithdrawalCommittedWhileWaiting` (`WhileLocked`);
  - `TestDecisionPermissionMatrix` (5 roles × approve/reject on someone else's step).
- [ ] Implement; run.

### Task 5: Withdraw, reassign, override, unroutable

**Routes:**
- `POST /api/v1/requests/{id}/items/{itemId}/withdraw` `{version, reason}`;
- `POST /api/v1/approvals/{stepId}/reassign` `{version, assigneeId, reason}` (InfoSec, Super Admin);
- `POST /api/v1/approvals/{stepId}/override` `{version, reason}` (Super Admin);
- `GET /api/v1/approvals/unroutable` (InfoSec, Super Admin).

**Audit actions:** `ITEM_WITHDRAWN`, `STEP_REASSIGNED`, `SUPER_ADMIN_OVERRIDE`.

**Notifications:**
- override → InfoSec, the beneficiary's manager and the bypassed assignee;
- reassign → the new assignee.

- [ ] Tests first:
  - `TestWithdrawBeforeDecision` (by the beneficiary and by the submitter; a third person → 404);
  - `TestCannotWithdrawADecidedItem`;
  - `TestReassignRespectsSeparationOfDuties` (to the beneficiary → 400; to a non-InfoSec member for an InfoSec step → 400; to an inactive person → 400);
  - `TestOverrideRecordsTheSuperAdmin` (the step shows `SUPER_ADMIN_OVERRIDE`, not the original approver; three notifications);
  - `TestOverrideCannotReverseARejection`;
  - `TestNoOverrideOnOwnItem`;
  - `TestUnroutableStepListedAndResolvedByOverride` (no InfoSec members at all; and the only InfoSec member is the beneficiary);
  - `TestReassignPermissionMatrix`.
- [ ] Implement; run.

### Task 6: Docs, full suite, Docker journey, reviews

- [ ] README: request, approval, inbox, outbox and override API; rules; M5 status. Main plan: M5 status.
- [ ] Full suite `docker compose --profile test run --rm tests` → exit 0.
- [ ] Docker journey on the throwaway project, using the dev-fixture users:
  - dev1 requests Jira Viewer and AWS Admin in one submission → one email to the manager;
  - the manager approves both → Jira Viewer is `APPROVED`;
  - the AWS Tool Owner (infosec2) approves → the InfoSec step goes to the pool, and infosec2 does not see it;
  - infosec1 approves → `APPROVED`;
  - the manager submits for dev2 → `APPROVED_BY_SUBMISSION`, no email;
  - dev2 withdraws one item;
  - the auditor cannot approve.
- [ ] Superpowers `requesting-code-review` (independent) and `ponytail-review`; fix the findings; re-run.
- [ ] Report; ask for commit approval: `feat: add access requests, approval routing and decisions`.

## Implementation notes (2026-10-28)

Small deviations from the tasks above, made during implementation:
- Unroutable steps are listed by `GET /api/v1/approvals/open` (InfoSec, Super Admin) with an `unroutable` flag, instead of a separate `/approvals/unroutable`: InfoSec also needs every open step to reassign absent approvers (D9).
- Withdrawal is `POST /api/v1/request-items/{id}/withdraw`.
- A repeated decision by the same person returns the current state with no event, also when concurrent (a double-click); a decision by anyone else on a decided step is `409` and recorded.
- Routing every pending step to a person or the pool means "unroutable" only arises for a pool step with no eligible InfoSec member; the schema requires exactly one of assignee and pool on a `PENDING` step.
- `TestTemporaryPasswordSessionCannotSubmit` was not added: `RequireSession` already rejects temporary-password sessions on every route (covered by the auth tests).
- **M6 follow-up:** the open-item check covers `PENDING_APPROVAL` only. Until grants exist, a second request for a tool whose item is `APPROVED` but not provisioned is allowed; M6's held-grant check (grant created at final approval) must cover it.

## Acceptance criteria (main plan)

- Jira Viewer needs only Manager; AWS Admin needs Manager → Tool Owner → InfoSec by three different people (Tasks 3–4).
- Fallback routing is visible: every step shows its routing note (Tasks 2–4).
- Exactly one email per qualifying submission, and none for an on-behalf submission by the manager (Task 3).
- Required tests from the main plan: the routing table, beneficiary cannot approve, one person per step, concurrent approvals (`-race`), repeated submission ID, override cannot reverse a rejection, reassignment respects separation of duties, employee cannot read another's requests (Tasks 2–5).

## Not in M5

- Fulfillment, the stricter-wins check and grants (M6).
- Direct grants (M7).
- `EXTENSION` / `LEVEL_CHANGE`, held-grant duplicates and expiry (M8).
- Re-routing on termination or suspension (M9).
- The Exceptions screen (M6, fed by the unroutable list).
- UI (U1 catch-up).
