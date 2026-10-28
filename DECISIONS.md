# AccessFlow — Product Decisions

Business-rule decisions made during requirements discovery and the design simplification review, plus approved architecture decisions (section "Architecture"). Technical choices that are only proposed are listed separately and are not decisions.

**Labels**
- **[CASE]** — explicitly required by `CASE.md`
- **[POLICY]** — our chosen product policy
- **[DEFAULT]** — proposed default stated during discovery and not objected to; may be revisited

---

## Foundations

### F1. Access unit = tool + access level — [POLICY]
Every request, grant, review item and audit record refers to *(person, tool, access level)*. Approval requirements belong to the **access level**, not the tool.
*Rationale:* "As little access as possible" is meaningless if viewer and admin are indistinguishable.

### F2. Approval requirements per access level — [CASE] "the right people approve, depending on the tool and how sensitive the access is", [POLICY] the mechanism
- There is no sensitivity classification. Each access level directly defines its required approval steps:
  - **Manager** — always required
  - **Tool Owner** — optional
  - **InfoSec** — optional
- The order is fixed: Manager → Tool Owner → InfoSec. Example: Jira Viewer = Manager; GitHub Developer = Manager + Tool Owner; AWS Admin = Manager + Tool Owner + InfoSec.
- A Super Admin configures the steps when creating or editing an access level. There is no generic workflow engine.
- Approval requirements are snapshotted when a request is submitted.
- **Stricter wins before provisioning:** immediately before provisioning, the item is checked against the level's current requirements. If a mandatory step was added after submission, that step is added to the item and must be decided by an authorized human through the normal approval workflow before access is granted. Removing a step never removes a step the item already required.
- **Policy changes** are recorded in the audit history, generate an in-app notification to InfoSec, and appear in the quarterly audit evidence.
- **Effect on existing grants:** when steps are added to a level, its existing active grants are marked **approved under earlier policy** and must be decided individually in the next review campaign (see D8). When steps are removed, existing grants are unaffected. Historical approvals are never rewritten.
- Each step on one item must be decided by a different person. The beneficiary never approves, fulfils or reviews their own item.
- A blocked step is re-routed visibly, never skipped. Tool Owner step → InfoSec. Manager step → manager's manager → InfoSec. InfoSec step → another InfoSec member. If no eligible person exists, the item is shown in Exceptions (a Super Admin override, D1, can resolve it).
- Approvals are always made by authorized humans; nothing is ever approved automatically.

*Rationale:* each level's approval steps express how sensitive it is. Requirements are configured centrally and audited, so the same request always receives the same chain — fixing "the same request gets different answers in different tools".
*Trade-off:* consistency across tools depends on Super Admin configuration rather than a fixed classification; mitigated by audit, InfoSec notification and quarterly evidence of policy changes.

### F3. No eligibility restrictions — [POLICY]
Departments and job titles only drive **recommended starter packages**. Any employee may request any active tool/level; the approval steps are the only control. Rejections require a reason, are recorded per item and never trigger provisioning.
*Trade-off:* simpler and more flexible; relies on approvers to reject inappropriate requests.

### F4. Initial credentials — [DEFAULT]
HR creates the employee; the system generates a one-time temporary password shown once, stored only as a hash, expiring and single-use, with a forced change on first login. Resets of privileged accounts (Super Admin, InfoSec) require a Super Admin other than the target. No real email or SSO.
*Trade-off:* HR briefly knows a working credential; mitigated by single use, expiry and audit of first login.

---

## D1. Super Admin override and direct grants — [POLICY]
- Super Admin may override **any pending** approval step (Manager, Tool Owner, InfoSec) on any access level, with a mandatory reason and no InfoSec co-sign.
- Recorded as `SUPER_ADMIN_OVERRIDE` with the actual Super Admin's identity, the original step and its assignee, reason and timestamp. It never appears as the original approver's decision.
- Super Admin may **directly grant and revoke**, with justification. A direct action skips only the approval steps; fulfillment uses the tool's normal method (see Fulfillment methods): API through the Mock Tool Service with status verification; Manual through a task for the tool's administration group; Vendor through a tracked ticket with a manually recorded outcome. Manual and Vendor outcomes remain labelled self-reported.
- For Manual and Vendor direct actions, the initiating Super Admin may not complete or confirm the fulfillment; a different authorized admin must.
- Direct grants are always **temporary, maximum 90 days**, measured from the direct grant's creation time — the only duration limit in the system. No additional reminders or escalations apply.
- Never for the Super Admin's own access; no self-approval.
- [DEFAULT] An override cannot reverse a rejection; it applies only to live items. A direct grant is a normal access item (`DIRECT_GRANT`). InfoSec, the beneficiary's manager and the bypassed approver are notified in-app. Overrides and direct grants appear in a dedicated report included in quarterly evidence.

*Trade-off:* one Super Admin can grant any access alone. Controls are detective (visibility, notification, review), not preventive — which makes D2 the key control.

## D2. Super Admin promotion and succession — [POLICY]
- Exactly one Super Admin is bootstrapped at initial setup.
- Promotion is two-person: a Super Admin proposes, a **different** Super Admin authorizes; if only one exists, a different InfoSec member authorizes. The nominee never authorizes. Existing roles and groups are preserved.
- Other application roles and admin group memberships are assigned by a Super Admin, with audit.
- When the last Super Admin is terminated, termination proceeds immediately (login disabled, sessions ended) and the system enters `ADMIN_RECOVERY_REQUIRED`. An HR Manager nominates (self allowed); a different InfoSec member authorizes.
- **Last resort:** if no eligible authorizer exists, recovery reuses the initial Super Admin bootstrap mechanism. It is available only when no active Super Admin exists, and it is recorded in the audit history.
- A health warning shows whenever fewer than two active Super Admins exist. Concurrent cases cannot leave inconsistent state.
- [DEFAULT] Open nominations expire (e.g. 7 days) and are cancelled if the nominee or initiator is terminated. Recovery mode blocks only Super Admin-only configuration; access workflows keep running.

## D3. On-behalf requests — [POLICY]
When the beneficiary's **current direct manager** submits a request for them, the submission satisfies the Manager step, recorded as `APPROVED_BY_SUBMISSION`. Any other submitter triggers the normal Manager step. Remaining steps still apply with distinct people.
*Rationale:* a separate approval click by the same person adds no control and slows onboarding.

## D4. One access level per tool — [POLICY]
- At most one active level per person per tool in the settled state.
- A different level is a `LEVEL_CHANGE`, approved under the **new level's approval steps** and provisioned/verified normally.
- Requesting a level already held does not create a duplicate grant; an extension is offered where appropriate.
- Every operation identifies the specific **(tool, account, access level)**. The Mock Tool Service stores permissions per level, so revoking one level never removes another level or the account.
- Every level change grants and verifies (API) or confirms (Manual/Vendor) the new level first; only then is the revocation of the old level created.
- If granting the new level fails, the old level is preserved and nothing is revoked; the failure is visible and retryable (O3).
- If revoking the old level fails, both levels are held and an **overlap exception** is shown until removal is verified or confirmed.
- No additional workflows or recovery mechanisms beyond the normal operation pipeline.
- History records the actual time each level became effective and ended; nothing is rewritten. Reviews show the current effective level.

*Trade-off:* real tools may have independent, additive permissions; out of scope.

## D5. Mover (team change) — [CASE] removal on team change, [POLICY] the mechanism
- On a department or manager change, HR sees all active access, **pre-selected for revocation**, with Revoke All / Keep All shortcuts.
- Keeping any item requires a written justification. Kept items are marked `RETAINED_ON_TRANSFER`, keep their expiry, are notified to the new manager and included in the next quarterly review.
- Revocations use the normal workflow; access is not considered removed until confirmed.
- A job-title-only change produces an audit event only.
- Pending requests are reassessed: Manager step moves to the new manager; [DEFAULT] requests submitted before the transfer are cancelled when HR revokes everything.
- A stale grant that completes after HR chose revocation is revoked immediately and recorded as an incident.
- Managers can revoke direct reports' access at any time. No separate mover-review deadline.

*Rationale:* revocation-by-default satisfies CASE.md; per-item retention avoids breaking shared tools, and retained items stay visible.

## D6. Duration — [CASE] requester states how long, removal on expiry; [POLICY] options
- The requester explicitly chooses a duration for every access item:
  - **Temporary** — with an explicitly selected expiration date (quick picks such as 7, 30 or 90 days, or a custom date).
  - **Until Revoked** — no predefined expiration date.
- Both options are available for **every** access level, including levels requiring InfoSec approval. There are no per-level duration limits or duration settings.
- All approvers see the requested duration and justification before deciding.
- Temporary grants are revoked through the normal revocation workflow when their expiration date is reached.
- Until Revoked does not mean uncontrolled: such grants remain subject to quarterly reviews, manager revocation, transfer decisions, termination and other authorized revocations.
- Extensions (`EXTENSION`) and changing Temporary → Until Revoked follow the normal approval workflow, re-running the level's current approval steps.
- Super Admin direct grants are the only exception: always temporary, maximum 90 days (D1).
- No expiry reminders. Expiration dates are shown in the employee's access list and the manager's view; extensions are requested manually. Temporary access still expires automatically into the normal revocation workflow. Reviewers see the duration type and grant age.

*Trade-off:* privileged permissions (e.g. AWS Admin) may be held Until Revoked after a single approval chain. For these grants expiry provides no protection; removal relies on quarterly reviews (with day-21 auto-revocation for unanswered InfoSec-approved items, D8), manager and HR actions, and termination. Accepted for simplicity and to avoid duration configuration.

## D7. Termination and suspension — [CASE] removal on leaving, [POLICY] the rest
**Termination**
- Immediate or scheduled. Scheduled terminations run automatically at the effective time (one company timezone); overdue jobs run after recovery. HR can cancel or reschedule, with audit. Immediate termination overrides any schedule and any pending provisioning or resume.
- Effects: login disabled, sessions invalidated, pending requests cancelled, tasks released, approvals/reviews reassigned, all external access revoked through the normal workflow and visible until confirmed.
- Terminated employees never regain access automatically; rehire is an explicit process with fresh requests.

**Suspension**
- Immediately disables login, invalidates sessions, and blocks all actions by the employee.
- Active grants receive `SUSPEND` operations through the shared pipeline: verified for API (Mock Tool Service), self-reported disable tasks for Manual and Vendor. Not marked suspended until confirmed; failures are visible, retryable security exceptions.
- Pending requests are cancelled; approvals, reviews and claimed tasks are released or reassigned.
- Expiry, reviews and authorized revocations continue during suspension.
- On reactivation each grant is re-evaluated: expired, revoked or retired → not restored; otherwise `RESUME` through the normal workflow, active only once verified/confirmed.
- History distinguishes active, suspended, revoked and failed-suspension states.

*Trade-off:* two extra operation types and extra manual/vendor tasks, in exchange for reversible suspension without re-approval.

## D8. Quarterly access reviews — [CASE] quarterly review with chasing, [POLICY] the rules
- A campaign starts every 3 months and covers all active and suspended grants, temporary and Until Revoked.
- Tool Owners decide **Keep** or **Revoke** per item.
- **Day 7:** one in-app reminder to the reviewer for each unanswered item. No other reminder schedules.
- **Day 14:** all unanswered items escalate to InfoSec; authorized InfoSec reviewers may decide directly.
- **Day 21:** unanswered items whose access level **requires InfoSec approval** (per the level's configuration when the campaign starts) are revoked automatically through the normal workflow, reason `REVOKED_NO_REVIEW`. Using the current configuration also covers imported grants, which have no approval snapshot.
- All other unanswered items stay active but are recorded as **overdue unreviewed findings**, visible and actionable for InfoSec until explicitly decided. An unanswered item is never treated as approved or reviewed.
- An item with an open finding is **not added to later campaigns**; the original finding remains the actionable, audited work item until resolved. No duplicate findings are created.
- Open findings stay visible and actionable in **Exceptions**, showing the original campaign and how long the finding has been open. Later campaigns' quarterly evidence lists them as **carried-over open findings**.
- Besides InfoSec, the Tool Owner or their eligible re-routed reviewer may still submit a **late** Keep or Revoke decision; it is recorded as late in the audit history. (Super Admin remains Revoke-only.)
- A finding closes automatically as **already removed** when its grant is independently revoked or expires; the closure is recorded.
- No weekly reminders or further escalations for open findings.
- Keep does not extend expiry. Revoke uses the normal revocation workflow; failed revocations remain security exceptions.
- Super Admin may record **Revoke** on any review item, but never **Keep**; Keep decisions come only from the Tool Owner (or their re-routed reviewer) or InfoSec. *Rationale:* the role that can grant access alone (D1) must not also certify that access should stay.
- All decisions, escalations, non-response actions and fulfillment outcomes are recorded in the append-only audit history. Jobs and decisions are idempotent and safe under concurrency.
- [DEFAULT] Campaigns start on calendar quarters; a Super Admin can also start one manually (needed for the D10 go-live certification campaign). Items are snapshotted at start; grants that end during the campaign close as "already removed". An owner's own access, and an InfoSec member's own access, go to a different reviewer.
- [DEFAULT] Bulk Keep is allowed except for items whose level requires InfoSec approval and flagged items: retained on transfer, override or direct grant, suspended, imported baseline, approved under earlier policy. These are decided individually.

*Rationale:* CASE.md requires owners to be chased; auto-revoking InfoSec-approved access enforces least privilege where risk is highest, while findings for other items avoid mass disruption without hiding non-response.
*Trade-off:* unreviewed access to levels not requiring InfoSec can persist until InfoSec acts; it is visible, but not removed.

## D9. Absent approvers: manual reassignment, no delegation — [CASE] "unblocked fast", [POLICY] the mechanism
- No user-defined delegation and no automatic reminders or time-based escalation.
- Pending approval steps stay in the assignee's Approval Inbox, showing how long they have been waiting.
- InfoSec or a Super Admin may manually reassign a pending step to another eligible approver with a reason, recorded as `REASSIGNED`. All separation-of-duties rules apply to the new approver.
- Fallback routing (F2) still applies when an approver is actually unavailable (e.g. terminated, suspended, or excluded by separation of duties).
- Concurrent decisions on one step: the first wins; later attempts are rejected and recorded.
- Super Admin override (D1) remains available.

*Rationale:* reassignment and fallback routing cover absences without a delegate concept (a common separation-of-duties loophole) or extra timer jobs.
*Trade-off:* an absent approver's steps wait until someone reassigns them; the waiting time is visible.

## D10. Existing access at go-live: import + certify — [CASE] addresses "14 leavers still had accounts", [POLICY] the mechanism
- Existing access is imported as `IMPORTED_BASELINE` grants: API tools by reading the Mock Tool Service's state; manual and vendor tools by a simple CSV import. No complex integrations or mapping engines.
- Accounts are matched to employees by corporate email, case-insensitively.
- Imported grants record their source (mock service or CSV file name), import timestamp, importing actor, original account identifier and level, and are marked "existed before AccessFlow; approver unknown". No approval records or activation dates are invented.
- Imports are idempotent: an already-imported (tool, account, level) is skipped.
- CSV rows with an unknown tool or level are rejected and listed in an import report.
- An account belonging to a **terminated** employee is recorded as a grant and revocation starts immediately through the normal workflow.
- An unmatched account becomes an `ORPHAN_ACCOUNT` finding for InfoSec, never auto-assigned. [DEFAULT] InfoSec may revoke the account through the normal workflow or dismiss the finding with a reason.
- Imported grants are Until Revoked; no expiration date is imposed solely because of import.
- An initial certification campaign under D8 rules runs after import; unanswered items whose level requires InfoSec approval are auto-revoked on day 21.
- Seed data includes valid employee access, a terminated employee with an existing account, and an orphan account.

*Rationale:* governance must cover the access most likely to be wrong — access that predates the system.
*Trade-off:* imported access has no approval evidence; the certification campaign provides the first recorded decision.

## D11. AccessFlow privileged roles: evidence report, no review workflow — [POLICY]
- No review campaign for AccessFlow's own roles and groups.
- The Audit Explorer provides a **Privileged roles** report: who holds Super Admin, InfoSec, HR Manager, Auditor and each administration group, since when, and who granted it, queryable for any past date.
- The report is part of the quarterly evidence package. A formal review of privileged roles is listed in the 30-day plan.

*Rationale:* regulators ask who controls the access system; the report answers that from existing audit data at low cost.
*Trade-off:* privileged roles are visible but not periodically re-certified.

## D12. Setup, seed data and time — [CASE] one-command startup, [POLICY] the rest
- The application starts with a single `docker compose up`.
- Standard database migrations.
- A small, idempotent seed with realistic employees, tools, roles, requests and access records, including at least two InfoSec members (needed by F2's distinct-person fallback).
- Real system time and normal background jobs only. No simulated clocks, "run due jobs now" controls, demo-specific workflows or recording-specific features.
- No fabricated historical audit events. [DEFAULT] Seeded records are marked as seed-originated, and the seed's own actions are recorded at the real time they run.
- The screen recording is a separate delivery task and does not shape the application.

*Rationale:* correct behaviour, maintainability, security and tests take priority over presentation.

## Fulfillment methods — [CASE] the three ways tools are administered today
| Method | Grant / revoke / suspend / resume | Evidence |
|---|---|---|
| **API** | Automatic through the local Mock Tool Service, only after all required approvals | **Verified** by status lookup |
| **Manual** | Fulfillment task for the tool's administration group, claimed and completed by an authorized admin | **Self-reported** confirmation |
| **Vendor** | Tracked ticket with an external reference and a manually recorded outcome | **Self-reported** confirmation |

- All three support revocation, failure visibility and full audit history.
- Approval decisions are always made by authorized humans; automatic API provisioning happens only after those approvals. No real third-party integrations.
- All three methods share **one fulfillment workflow** (operation → execution or task → verification or confirmation); no separate engines per method.

**Item status** — each request item shows its own progress, independently of other items in the same submission. Approval and fulfillment are separate; an approved item is never shown as active access until fulfillment is verified or confirmed.

| Method | After all approvals | While in progress | Done | Failure |
|---|---|---|---|---|
| **API** | Grant operation sent automatically to the Mock Tool Service | `PROVISIONING` (including bounded retries, O3) | `ACTIVE / VERIFIED` after a successful status lookup | `FAILED`, retryable by an authorized admin |
| **Manual** | Fulfillment task for the responsible Admin Group | `PENDING_FULFILLMENT` | `ACTIVE / SELF_REPORTED` when an authorized admin confirms; who and when recorded | Admin reports failure → `FAILED` |
| **Vendor** | Fulfillment task for the responsible Admin Group; admin records the vendor ticket reference | `PENDING_FULFILLMENT` until the ticket reference is recorded, then `WAITING_VENDOR` | `ACTIVE / SELF_REPORTED` when the admin records the vendor's confirmation; never claimed as independently verified | Admin reports failure → `FAILED` |

Revocation, suspension and resume follow the same pattern and labels (see O1, D7).

## Operational rules — [DEFAULT] unless noted
- **O1. Effective access.** Access is effective from verified activation (API) or confirmed activation (manual/vendor, labelled self-reported) until verified or confirmed removal. This is the basis of point-in-time queries, which show each held grant in one of these states:
  - **Active**
  - **Revocation requested** — who, when, why
  - **Revocation failed** — failure details and retries; an open exception
  - **Revocation confirmed** — *verified* by status lookup (API) or *confirmed — self-reported* (Manual/Vendor, with confirming admin and ticket reference)

  Requested and failed revocations still count as held. Activation and removal times are the times the verification or confirmation was recorded in AccessFlow; earlier times are never entered or fabricated. Suspension (D7) uses the same labels.
- **O2. Notifications** — [POLICY]
  - Existing in-app task queues and essential in-app notifications remain.
  - A local **Mock Email Outbox** (no SMTP or email provider) receives exactly one simulated email per **`NEW_ACCESS`** submission that requires the Manager's approval, sent to the beneficiary's responsible Manager, informing them that an employee is requesting access. A submission covering several tools produces one email.
  - No email when the Manager submits on behalf of a direct report (D3), because the Manager step is already satisfied.
  - No other emails: none for `EXTENSION` or `LEVEL_CHANGE`, and no reminder, escalation, Tool Owner, InfoSec, review, provisioning or revocation emails.
  - Emails only inform. The Manager must open AccessFlow and explicitly approve or reject; nothing is ever approved by email.
- **O3. API retries.** Failed Mock Tool Service operations retry automatically a limited number of times with increasing delay, then become a visible `FAILED` exception that can be retried manually.
- **O4. Manual and vendor tasks.** Pending tasks stay in the responsible Admin Group's Fulfillment Tasks queue until completed, cancelled or failed, showing status, responsible group, related request and time pending. Pending tasks are normal work, not exceptions. No automatic escalation timers.
- **O5. Withdrawal and rejection.** The requester or submitter may cancel pending items before provisioning starts. A rejection is final; resubmission creates a new request.
- **O6. Voluntary revocation.** An employee may give up their own access at any time, without approval, through the normal revocation workflow.
- **O7. Screens** — [POLICY] no additional screens beyond these:
  - **My Requests / My Access** — the employee's requests, approval progress, per-item fulfillment status, active grants, expiration dates and relevant failures.
  - **Approval Inbox** — decisions the Manager, Tool Owner or InfoSec must make, with waiting time. All decisions are manual.
  - **Fulfillment Tasks** — pending Manual and Vendor actions for the user's Admin Groups: record ticket reference, confirm completion, report failure.
  - **Exceptions** — only real problems: failed API, Manual or Vendor operations; unresolved security-critical revocations (e.g. revocation still pending after termination); stale or conflicting access states (D4 overlap, D5 stale grant, D10 orphan account); approval steps with no eligible approver (F2); open review findings (D8). Ordinary pending work is not listed.
  - **Audit Explorer** — who requested, approved, granted, revoked or confirmed each action; actual timestamps; point-in-time effective access (O1), distinguishing API-verified from self-reported results.
  - *Clarification (2026-10-28):* the five screens above are the main functional areas, not a strict limit on the number of pages. The application also provides UI access to HR employee management (creation, transfers, suspension, termination), tool and access-level configuration, quarterly review decisions and findings, baseline imports and orphan account management, and administrative settings and Super Admin functions. These are organized as pages, subpages or sections for usability, without separate applications or complicated navigation. The visual design comes from `design/`.
  - *Audit Explorer access — [APPROVED 2026-10-28]:*
    - **Super Admin and InfoSec:** company-wide.
    - **Auditor:** company-wide, **read-only** access to the Audit Explorer and evidence reports. The Auditor role grants no approval, fulfillment, administration or access-modification permission.
    - **Manager:** full historical access and audit evidence for their **current** direct and indirect reports only; former reports are excluded.
    - Everyone else: no Audit Explorer; their own data through My Requests / My Access.
    - Enforced in backend queries, not only in the UI.
- **O8. Background processing** — [POLICY] limited to business needs: API operation execution with bounded retries (O3), temporary access expiry (D6), scheduled terminations (D7), and review campaign start plus the day-7, day-14 and day-21 steps (D8). No other reminder or escalation jobs.

---

**Business discovery and the design simplification review are complete.**

---

## Architecture

### A1. Approved architecture direction — [APPROVED 2026-10-28]
- The main application is a **modular monolith** written in Go (CASE.md requires Go).
- The frontend is **React + TypeScript** (CASE.md requires React).
- The database is **PostgreSQL**.
- The **Mock Tool Service** is a separate container with **its own database**. AccessFlow communicates with it **only through HTTP**; it never reads mock state directly.
- API, Manual and Vendor fulfillment methods are all kept.
- Audit integrity, human-only approvals and verification requirements (O1, Fulfillment methods) are preserved by the design.

### A2. Docker Compose layout — [APPROVED 2026-10-28]
- Three services:
  - **`postgres`** — one PostgreSQL server with separate `accessflow` and `mocktools` databases, each with its own database user restricted to its own database. Persistent named volume.
  - **`app`** — one multi-stage image: Go backend, background jobs and the compiled React frontend served from the same origin.
  - **`mocktools`** — a small independent Go HTTP service, reachable only on the internal Docker network.
- Only `app` is exposed, on port **8080**.
- Migrations run automatically at startup. Health checks and startup dependencies ensure services start in order.
- `docker compose up` works on a clean machine with no manual setup steps.
- AccessFlow never queries the `mocktools` database; it uses only the Mock Tool Service's HTTP API.
- A Mock Tool Service error, timeout or unexpected response is never treated as success; it goes through the retry and exception workflow (O3).

### A3. Go libraries — [APPROVED 2026-10-28]
- **`go-chi/chi` v5** for HTTP routing and middleware.
- **`jackc/pgx` v5** with `pgxpool` and hand-written SQL for PostgreSQL access.
- **`pressly/goose` v3** with SQL migrations embedded in the binary, run automatically at startup (so `docker compose up` applies them).
- **`google/uuid`** for entity and operation identifiers.
- **`golang-jwt/jwt` v5** for JWT signing and validation, and **`golang.org/x/crypto`** for argon2id (added by A4).
- Standard library wherever practical: `net/http`, `encoding/json`, `log/slog`, `testing`; configuration from environment variables.
- SQL is explicit, readable and always parameterized. Related state changes and their audit events are written in one transaction.
- Database access is kept separate from business rules.
- Simple reusable input-validation helpers; no GORM, no unnecessary code generation, no validation or configuration frameworks.
- The Mock Tool Service uses the same lightweight stack.

### A4. Authentication — JWT in HttpOnly cookie — [APPROVED 2026-10-28]
**Tokens**
- After a successful login, a short-lived JWT (HS256 only; other algorithms rejected) is issued in an `HttpOnly`, `SameSite=Lax` cookie (`Secure` enabled by setting; off for plain-HTTP localhost). No refresh tokens, no Redis, no auth service.
- Claims: employee ID, token version, CSRF value, original login time, issued-at, expiry. Lifetime **30 minutes**; while the user is active, the server re-issues the cookie when less than half the lifetime remains. A re-issued token keeps the original login time, and its expiry never goes past **12 hours** after that login; then a new login is required. This is a re-issued access cookie, not a refresh token.
- No token denylist, refresh tokens or session table.
- The signing secret comes from an environment variable; if none is set, a random secret is generated on first start and stored in the `accessflow` database, so a clean `docker compose up` needs no setup.

**Every authenticated request**
- Validates signature and expiry, then loads the employee from PostgreSQL: status must be `ACTIVE` and the token version must match. Roles and admin group memberships are read from the database each time, never trusted from the token.
- `SUSPENDED` and `TERMINATED` users are denied immediately, regardless of token expiry.

**Token version**
- Each employee has a `token_version`. It is incremented on **logout**, password change, password reset, suspension, termination and reactivation, invalidating all existing tokens for that employee.

**CSRF**
- The CSRF value in the JWT is also set in a readable cookie; every state-changing request must send it in an `X-CSRF-Token` header matching the JWT claim. The `Origin` header is also checked.

**Passwords and bootstrap** (preserves F4 and D2)
- argon2id hashes (OWASP minimum parameters, per-password salt, parameters stored with the hash). Length 12–128, no composition rules.
- Temporary passwords: 16 random characters, shown once, hashed, single use, expire after 72 hours. A token issued from a temporary password allows only password change and logout.
- Generic login error; 5 consecutive failures lock the account for 15 minutes (checked at login, no job). Logins, failures, lockouts and password changes are audited.
- First start: if no Super Admin has ever existed, one is created from a configured email with a one-time temporary password. D2 last-resort recovery is a one-off command (`docker compose exec app …`), usable only when no active Super Admin exists, audited, never automatic.
- **Bootstrap credential delivery — [APPROVED 2026-10-28, refines the above]:** the temporary password is written to a private file on a persistent Docker volume (`/var/lib/accessflow/bootstrap-admin.txt`, mode 0600) and is **never printed in application logs**. The log states only how to read it (`docker compose exec app cat /var/lib/accessflow/bootstrap-admin.txt`). The file is removed after the initial password change; single use and 72-hour expiry still apply, and `recover-admin` issues a new one the same way. `docker compose up` still needs no manual setup. *Documented limitation:* anyone with access to the Docker host, the Docker daemon or the volume can read the file while it exists. *(History: previously "printed once in the app log"; changed because logs are often collected and retained.)*
- Seeded demo users (D12) share a local-only password from a setting with a documented Compose default; the seed is never used in production.

**Logout**
- Clearing the cookie alone would not revoke a token copied before logout. Therefore logout also increments `token_version`, which immediately invalidates every token for that user, including copied ones.
- *Accepted trade-off:* logging out in one browser signs the user out on all browsers and devices. Single-token revocation (a token-ID denylist) is not provided.

### A5. Identity records and history — [APPROVED 2026-10-28]
- Employees and users are never physically deleted in normal workflows. Account status is `ACTIVE`, `SUSPENDED` or `TERMINATED`.
- Employee identifiers are immutable. Requests, approvals, grants, revocations, role changes and audit events are retained and remain available to point-in-time queries.
- Rehire reactivates the same employee record (same identifier, history kept) with a new temporary password. No previous access is restored; new access requires the normal request and approval workflow (D7).
- Reactivation after suspension follows D7: expired, revoked or retired grants are never restored; others are resumed only through the normal workflow.

### A6. Background processing — [APPROVED 2026-10-28]
Two mechanisms inside the Go application; no Redis, RabbitMQ, generic job queue, separate cron container or other infrastructure.

**1. Operation runner** (API grant, revoke, suspend, resume)
- The `operations` table is the queue. Due operations are claimed with row locking and `SKIP LOCKED`, under a bounded lease so a crashed worker's operations are retried.
- **Priority:** termination- and suspension-related removals first, then other revocations and suspensions, then grants and resumes; oldest first within a priority.
- The Mock Tool Service HTTP call (5-second timeout) runs outside any database transaction and carries the operation ID for idempotency.
- After a successful call, the actual tool state is verified by status lookup. State transitions and audit events are recorded in one transaction.
- O3 retries: 5 attempts with backoff 30 s, 1 min, 2 min, 4 min; then `FAILED`, retryable manually from Exceptions.
- Manual and Vendor fulfillment remain human-managed tasks, not runner work.

**2. Periodic sweeps** (about once a minute, guarded by a PostgreSQL advisory lock)
- Temporary access expiry (D6), due scheduled terminations (D7), quarterly campaign creation and the day-7, day-14 and day-21 steps (D8).
- Each sweep is idempotent and catches up after downtime.
- Company timezone from a setting, default **Europe/Istanbul**.

### A7. Frontend — [APPROVED 2026-10-28; UI component library deferred]
- **Vite + React + TypeScript** (strict mode), **React Router** for navigation, **TanStack Query** for server state. No Redux or other global store.
- **UI component library: deferred.** The interface is designed separately with Claude Artifacts and delivered under `design/`. The component approach is chosen after reviewing those files: if the design uses Tailwind, shadcn/ui or another approach, its compatibility is evaluated and the existing design reused where practical. Only one UI library; no competing libraries or unnecessary rewrites of design components.
- The design is the visual and UX reference; `CASE.md` and `DECISIONS.md` remain authoritative for functional requirements.
- Library-independent conventions:
  - One small `fetch` wrapper: adds `X-CSRF-Token`; on `401` goes to login (or forced password change); shows clear `403` and validation errors.
  - Hand-written TypeScript types matching the API; no OpenAPI code generation.
  - One shared status-to-badge mapping so every screen shows item states identically; self-reported results always labelled.
  - Timestamps shown in the company timezone, with the timezone named.
  - The UI hides actions only for convenience; the server enforces every permission (A4).
  - Built assets embedded in the Go binary; unknown non-API paths return `index.html`.
  - Exact versions pinned in `package-lock.json`; Docker builds use `npm ci`.

### A8. Backend module layout — [APPROVED 2026-10-28]
- One Go module; modular monolith with domain-oriented packages:
  - `cmd/accessflow` (server, background work, one-off `recover-admin` command for D2) and `cmd/mocktools` (Mock Tool Service).
  - `internal/`: `platform` (DB pool and transaction helper, embedded migrations, config, HTTP helpers), `auth`, `people`, `catalog`, `requests`, `access` (grants, operations, runner, Mock Tool client, Manual/Vendor tasks, baseline import, orphan accounts), `reviews`, `audit`, `notify`, `sweeps`, `mocktools`.
  - `web/` for the React app.
- A package typically separates pure rules, SQL, transactional service methods and HTTP handlers (e.g. `rules.go`, `store.go`, `service.go`, `http.go`) — a suggested organization, not a mandatory template.
- Business rules are pure functions with explicit time parameters, unit-testable without a database or a simulated clock.
- Service methods own the transaction; operations affecting several domains share one transaction so state changes and audit events stay atomic. Transaction ownership stays explicit.
- Package dependencies are acyclic: `audit` and `notify` are leaves; `people`, `requests` and `reviews` may call `access`; `access` never calls back. Avoid tightly coupled domain services.
- Interfaces only where they add real value (the Mock Tool client; the transaction type). No repository interfaces, Clean/Hexagonal layering or extra service layers; no abstraction before it is needed.
- Typed business errors mapped to HTTP responses in one place; JSON APIs under `/api/v1`.
- Readability for a reviewing engineer takes priority over cleverness.

### A9. Integrity mechanisms — [APPROVED 2026-10-28]
**State transitions**
- Explicit allowed-transition rules (pure functions) for request items, approval steps, grants, operations and review items; statuses stored as text with database `CHECK` constraints. Screen labels (O1, Fulfillment methods) are derived from these states.
- API, Manual and Vendor operations share one `operations` table and one workflow.

**Audit history**
- One append-only `audit_events` table (time, actor or system, action, entity, subject employee, reason, details JSON), written in the same transaction as every state change.
- Runtime database user may only `INSERT`/`SELECT` audit events; a trigger also blocks `UPDATE`, `DELETE` and `TRUNCATE`. Migrations run under a separate owner user used only at startup (refines A2: the `accessflow` database has an owner user and a runtime user).
- Each transaction reads the database time once and uses it for rules, state changes and audit events.

**Concurrency and duplicates**
- Version columns on mutable rows; stale updates are rejected with `409 Conflict` (gives D9's first-decision-wins).
- A row lock on the employee serializes actions that change that person's access (submit, level change, suspend, terminate, transfer, rehire).
- Unique partial indexes: one open request per (person, tool, level); one open grant per (person, tool, level); one grant per approved request item; one open removal operation per grant; one open level change per (person, tool); one campaign per quarter; one action per campaign day step.
- Constraints are the last line of defence, not the whole answer:
  - **Create actions** (request submission, direct grant) carry a client-generated submission ID with a unique constraint; a repeated submission returns the original result instead of creating anything new.
  - **Transition actions** (approve, reject, confirm task, record ticket, retry, review decision) are conditional updates on the expected status and version; a repeat of an already-applied action returns the current state and creates no duplicate approval, grant, operation or audit event.
  - The UI disables action buttons while a request is in flight.
  - Mock Tool calls carry the operation ID (A6).

**Level changes (D4)**
- The new grant is a separate grant row linked to the old one; the per-(person, tool, level) constraints allow both to exist during the change.
- The old grant stays `ACTIVE` and untouched until the new grant is `ACTIVE`; the old grant's revoke operation is created in the same transaction that activates the new grant. If the new grant fails or is cancelled, the old grant is unaffected.
- Outside a level change, the rules (under the employee lock) reject a second level on the same tool.

**Evidence**
- Each activation and removal records its evidence type: `VERIFIED` (API only, enforced by a `CHECK` on the method), `SELF_REPORTED` (Manual/Vendor only; confirming actor required; ticket reference required for Vendor), or `IMPORTED` (source, file or service, import time and importing actor; D10). Manual and Vendor results are never `VERIFIED`.

**Point-in-time**
- Grants store `effective_from` / `effective_to`, set only on verified or confirmed activation/removal at the time it was recorded (O1). Suspensions and role/admin-group assignments are stored as intervals.
- "Who had access on date D" uses effective-access intervals, not approval status; removal state on D comes from the operation's audit events; approvers come from the request item's approval steps.
- Nothing is deleted or rewritten (A5).

No event sourcing, hash-chained audit or temporal database extensions.

### A10. Testing approach — [APPROVED 2026-10-28]
- **Unit tests** (table-driven) for pure rules: transitions, routing and fallback, separation of duties, stricter-wins, durations and the 90-day cap, D8 day steps, D4 sequencing, evidence labels.
- **Integration tests against real PostgreSQL**, run with `docker compose --profile test run --rm tests` (Docker only). Each test uses a fresh database cloned from a migrated template and connects as the runtime user, so privileges and the audit trigger are exercised. The real Mock Tool Service handler runs in-process behind a test-only fault-injection wrapper. Time is passed explicitly to rules and sweeps. Concurrency tests with `-race` and goroutines against the real database.
- **API scenario tests** for every CASE.md flow and risky rule (request → approval → API/Manual/Vendor fulfillment, retries and lease recovery, level change paths, expiry, transfer, termination priority and stale grants, suspension/resume, review day 7/14/21 and findings, point-in-time states, baseline import and orphans, security checks).
- **Frontend:** Vitest + React Testing Library for the fetch wrapper, status mapping and key forms. No Playwright suite; **browser-level smoke tests** of the critical journeys are run once the frontend exists.
- Checks: `gofmt`, `go vet`, `go test -race ./...`, `tsc --noEmit`, frontend tests. No coverage percentage target.

**All technical architecture decisions (A1–A10) are approved (2026-10-28).**

**Workflow clarification (2026-10-28):** Implementation proceeds milestone by milestone according to the approved plan under `docs/plans/`, with explicit user approval before starting each milestone. *(Replaces the earlier statement "Implementation proceeds phase by phase without further approval gates." No business or architecture decision changes.)*
