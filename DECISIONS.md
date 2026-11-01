# AccessFlow — Product Decisions

Business-rule decisions made during requirements discovery and the design simplification review, plus approved architecture decisions (section "Architecture"). Each decision keeps its history: a later change is a dated note under the rule it changes, and a replaced rule is marked *Superseded* with a pointer to the rule in force. [Current rules at a glance](#current-rules-at-a-glance) summarizes the rules in force today.

**Labels**
- **[CASE]** — explicitly required by `CASE.md`
- **[POLICY]** — our chosen product policy
- **[DEFAULT]** — proposed default stated during discovery and not objected to; may be revisited

---

## Current rules at a glance

*Added 2026-10-31 as a reading aid. It summarizes the rules in force after every dated note; the numbered decisions below stay authoritative.*

| Topic | Rule in force | Decisions |
|---|---|---|
| Access unit | A tool plus an access level. One level per person per tool; a level change grants the new level first, then removes the old one | F1, D4 |
| Approvals | Each level sets its steps: Manager (always), then Tool Owner and InfoSec (optional), decided in that order by different people. Nobody approves, fulfils or reviews their own access. A beneficiary's direct manager who submits satisfies the Manager step, unless that manager is an HR Manager or Super Admin. A blocked step is re-routed, never skipped; a step added before provisioning is still required | F2, D3, D9 |
| Credentials | Employees never receive a password from anyone: they get a single-use activation link, valid 24 hours, through the Mock Email Outbox, revealed by a Super Admin (audited); a privileged account's link only by an independent Super Admin. The bootstrap Super Admin and `recover-admin` use a one-time password file. Demo mode gives eight documented accounts fixed local passwords | F4 notes 2026-10-30 and 2026-10-31, A4, D12 |
| Duration and expiry | Every item is Temporary (an end date) or Until Revoked, for every level. Access stays through its end date and is removed from 00:00 the next day (company timezone). Extensions and Temporary → Until Revoked are approved again. Direct grants: at most 90 days | D6, D1 |
| Movers and leavers | A transfer pre-selects every grant for removal; keeping one needs a justification. Termination removes everything first and cancels pending work; suspension is reversible; a rehire restores nothing | D5, D7, A5 |
| Quarterly reviews | A campaign each calendar quarter: the tool owner keeps or removes each grant; reminder on day 7, InfoSec escalation on day 14, and on day 21 every unanswered item becomes an open security finding. **Nothing is removed automatically.** A Super Admin may only revoke; a leaver's access is never kept; a new tool owner takes over the open items | D8 notes 2026-10-30 and 2026-10-31 |
| Super Admin | One is bootstrapped. Routine promotion: a Super Admin nominates and a different Super Admin (or, with none, an InfoSec member) authorizes. Succession, while at most one Super Admin is not terminated: an HR Manager nominates, an independent InfoSec member or Super Admin authorizes, and the nominee accepts in their own account. The last Super Admin cannot be terminated until a successor accepts; `recover-admin` is the last resort | D2 notes 2026-10-30 and 2026-10-31, D7 |
| Auditor | An external, read-only auditor: lists, filters and exports the audit events, and reads point-in-time access and approval evidence company-wide; nothing else. `AUDITOR` overrides every other role, and an Auditor holds no operational duty. The evidence reports (quarter, privileged roles, overrides, policy, leavers) are run by InfoSec or a Super Admin | O7 notes 2026-10-31 |
| Existing access | Imported as baseline grants (approver unknown) from a tool read, a CSV or the onboarding workbook, then certified by a review campaign; accounts matching no employee are orphan findings for InfoSec | D10 |
| Evidence and audit | API results are verified by read-back; Manual and Vendor results are self-reported; imported access is labelled imported. Every change and its audit event commit in one transaction; the audit log is append-only | O1, A9 |
| Sign-in | argon2id; 5 wrong passwords lock an account for 15 minutes; 60 attempts a minute per client address; 30-minute sessions renewed up to 12 hours | A4 |
| Time | Real system time only; no simulated clocks | D12 |

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
- *Note — [APPROVED 2026-10-29] (M17):* access levels created by an onboarding workbook import send **one** consolidated InfoSec notification per import (`APPROVAL_POLICIES_IMPORTED`, with the file name and counts) instead of one per level. Every tool and level is still audited individually.
- **Stricter wins before provisioning:** immediately before provisioning, the item is checked against the level's current requirements. If a mandatory step was added after submission, that step is added to the item and must be decided by an authorized human through the normal approval workflow before access is granted. Removing a step never removes a step the item already required.
- **Policy changes** are recorded in the audit history, generate an in-app notification to InfoSec, and appear in the quarterly audit evidence.
- **Effect on existing grants:** when steps are added to a level, its existing active grants are marked **approved under earlier policy** and must be decided individually in the next review campaign (see D8). When steps are removed, existing grants are unaffected. Historical approvals are never rewritten.
- Each step on one item must be decided by a different person. The beneficiary never approves, fulfils or reviews their own item.
  - *Refinement — [APPROVED 2026-10-28]:* a submitter who requests on behalf of someone else may not decide any step on that item, except that the beneficiary's current direct manager's submission satisfies the Manager step (D3).
- A blocked step is re-routed visibly, never skipped. Tool Owner step → InfoSec. Manager step → manager's manager → InfoSec. InfoSec step → another InfoSec member. If no eligible person exists, the item is shown in Exceptions (a Super Admin override, D1, can resolve it).
  - *Note — [APPROVED 2026-10-31] (final QA, BUG-A-03):* when the caller is a party to an unroutable item (beneficiary, submitter or an earlier approver), Exceptions says so and names who may act (another InfoSec member's reassignment or another Super Admin's override) instead of offering a button the server would refuse. Separation of duties is unchanged.
- Approvals are always made by authorized humans; nothing is ever approved automatically.

*Rationale:* each level's approval steps express how sensitive it is. Requirements are configured centrally and audited, so the same request always receives the same chain — fixing "the same request gets different answers in different tools".
*Trade-off:* consistency across tools depends on Super Admin configuration rather than a fixed classification; mitigated by audit, InfoSec notification and quarterly evidence of policy changes.

### F3. No eligibility restrictions — [POLICY]
Departments and job titles only drive **recommended starter packages**. Any employee may request any active tool/level; the approval steps are the only control. Rejections require a reason, are recorded per item and never trigger provisioning.
*Trade-off:* simpler and more flexible; relies on approvers to reject inappropriate requests.
- *Note — [APPROVED 2026-10-29] (M17):* departments and job positions come from a **company catalog** kept by HR Managers and Super Admins: exact names, unique ignoring case, create and retire only (no rename, no deletion). They still grant nothing and carry no RBAC. Employee create, edit, transfer, rehire, starter packages and the workbook accept only active entries, named exactly; a case-only difference is refused with the defined spelling. A retired entry stays on its holders and in history. The migration copies the values already in use.

### F4. Initial credentials — [DEFAULT]
*Superseded for employees by the activation-link note (2026-10-30) below; the one-time password remains only for the bootstrap Super Admin and `recover-admin` (A4).* HR creates the employee; the system generates a one-time temporary password shown once, stored only as a hash, expiring and single-use, with a forced change on first login. Resets of privileged accounts (Super Admin, InfoSec) require a Super Admin other than the target. No real email or SSO.
*Trade-off:* HR briefly knows a working credential; mitigated by single use, expiry and audit of first login.
- *Note — [APPROVED 2026-10-29] (M17):* employees imported from the onboarding workbook get **no password**; they cannot sign in until HR issues one with the existing reset (the same 72-hour, single-use temporary password). The workbook never assigns application roles (D2, D11). *(The password part is superseded by the activation-link note of 2026-10-30: imported employees get a link.)*
- *Temporary password semantics — [APPROVED 2026-10-28]:* "single use" means a temporary password can be used for exactly one successful password change. Until then it stays valid for signing in, but every session from it may only change the password or sign out; after the change it is invalid. Employee temporary passwords also expire **72 hours** after issue, whichever comes first. The **bootstrap Super Admin's** temporary password does **not** expire; it stays valid until its first successful change (see A4, bootstrap credential delivery). No extra recovery mechanism is added for it. *(For employees, superseded by activation links, 2026-10-30; the bootstrap rule stands.)*
- *Employee administration — [APPROVED 2026-10-28]:* **HR Managers and Super Admins** may create and edit employees and reset passwords ("HR creates the employee" is extended to Super Admins so the bootstrap Super Admin can set up the organization without assigning HR Manager to themselves). Resets of privileged accounts (holding Super Admin or InfoSec) still require a Super Admin other than the target. Nobody resets their own password through an administrative action (they use password change). Only `ACTIVE` employees can be reset; `TERMINATED` employees cannot be edited (rehire, D7/A5). A manager must be `ACTIVE`, never the employee, and manager chains cannot form a cycle.
  - *Refinement — [APPROVED 2026-10-28]:* nobody edits their own employee record, including their own manager, department and job title (`self_change_forbidden`); another HR Manager or Super Admin makes the change. Otherwise a person could choose who approves their own requests.
- *Employee directory visibility — [APPROVED 2026-10-28]:* Super Admin, HR Manager, InfoSec and Auditor see every employee with status, roles, admin groups, pending password change and version; everyone else sees only `ACTIVE` employees with id, email, name, department, job title and manager. Enforced in the backend query. The Auditor role remains strictly read-only (O7).
  - *Note — [APPROVED 2026-10-31] (final QA, Auditor):* the Auditor no longer sees the directory; an Auditor reads only the audit events (O7 note 2026-10-31). *(Amended the same day: the Auditor also reads point-in-time access and approval evidence (O7 note "the Auditor also reads point-in-time access and approval evidence", 2026-10-31; [current rules](#current-rules-at-a-glance)). The directory stays closed to the Auditor.)*
- *Activation links — [APPROVED 2026-10-30] (M18; supersedes the M17 "no password" note and the temporary passwords for employees above):* an employee created by hand, imported from the workbook or rehired gets **no password** but a single-use **activation link** (a 256-bit random token; only its SHA-256 is stored), valid for **24 hours**, delivered **only** through the Mock Email Outbox (O2); no API response, log or audit event carries it. The link opens a "Şifreni Belirle" page where the employee chooses their password; it never signs anyone in, and the employee then signs in normally. A link is invalid once used, expired or replaced. The existing reset (same permissions: HR Manager or Super Admin, never oneself, a Super Admin or InfoSec account needs a Super Admin, only `ACTIVE` employees) now issues a new link instead of a temporary password: it **immediately** removes the current password, revokes earlier links, ends every session and clears a lockout; the UI warns about this and asks for confirmation. Only a **Super Admin** can reveal a link in the outbox, one message at a time, each reveal audited (`ACTIVATION_LINK_VIEWED`); `ACTIVATION_COMPLETED` records who revealed that link. A Super Admin sees pending, expired, completed and missing activations and can issue a new link. The bootstrap Super Admin and `recover-admin` keep their one-time password file (A4): no Super Admin exists yet to read the outbox. Temporary passwords issued before M18 keep their 72-hour expiry until used. *Trade-off:* while mail is simulated, a Super Admin who reveals a pending link could use it; the reveal is audited, the link is single-use, and the real employee then finds it used. Real e-mail would remove the reveal.
  - *Note — [APPROVED 2026-10-31] (final QA, R-1):* a link to an account holding **Super Admin or InfoSec** is revealed only by an **independent** active Super Admin: never by the Super Admin who issued it (the reset) or by the account's owner (`403 independent_super_admin_required`). Each such reveal notifies the other active Super Admins in the app (`PRIVILEGED_ACTIVATION_LINK_VIEWED`) and is audited with `privileged: true`. Ordinary employees' links are revealed as before. *Consequence:* while only one Super Admin exists, a link that Super Admin issued to an InfoSec member waits until a second Super Admin exists (or real e-mail).

---

## D1. Super Admin override and direct grants — [POLICY]
- Super Admin may override **any pending** approval step (Manager, Tool Owner, InfoSec) on any access level, with a mandatory reason and no InfoSec co-sign.
- Recorded as `SUPER_ADMIN_OVERRIDE` with the actual Super Admin's identity, the original step and its assignee, reason and timestamp. It never appears as the original approver's decision.
- Super Admin may **directly grant and revoke**, with justification. A direct action skips only the approval steps; fulfillment uses the tool's normal method (see Fulfillment methods): API through the Mock Tool Service with status verification; Manual through a task for the tool's administration group; Vendor through a tracked ticket with a manually recorded outcome. Manual and Vendor outcomes remain labelled self-reported.
- For Manual and Vendor direct actions, the initiating Super Admin may not complete or confirm the fulfillment; a different authorized admin must.
- Direct grants are always **temporary, maximum 90 days**, measured from the direct grant's creation time — the only duration limit in the system. No additional reminders or escalations apply.
  - *Refinement — [APPROVED 2026-10-28, M8 plan]:* with end-of-day expiry (D6), a direct grant's access ends at 00:00 on the day after its expiration date, and that moment may not be later than creation + 90 days, so the latest allowed date is today + 89. A direct grant may be extended only through the normal approval workflow (D6).
- Never for the Super Admin's own access; no self-approval.
- [DEFAULT] An override cannot reverse a rejection; it applies only to live items. A direct grant is a normal access item (`DIRECT_GRANT`). InfoSec, the beneficiary's manager and the bypassed approver are notified in-app. Overrides and direct grants appear in a dedicated report included in quarterly evidence.

*Trade-off:* one Super Admin can grant any access alone. Controls are detective (visibility, notification, review), not preventive — which makes D2 the key control.

## D2. Super Admin promotion and succession — [POLICY]
- Exactly one Super Admin is bootstrapped at initial setup.
- Promotion is two-person: a Super Admin proposes, a **different** Super Admin authorizes; if only one exists, a different InfoSec member authorizes. The nominee never authorizes. Existing roles and groups are preserved.
- Other application roles and admin group memberships are assigned by a Super Admin, with audit.
- *Note — [APPROVED 2026-10-29] (M17):* the onboarding workbook only **references** existing admin groups by name (a tool's fulfilling group). Groups are created in the UI before the import and their members are added in the UI afterwards; the workbook never creates a group, adds a member or assigns a role. A new tool's owner must be an active employee (existing or in the same workbook) and is shown in the preview before import.
  - *Refinement — [APPROVED 2026-10-28]:* nobody changes their own roles or admin group memberships; only `ACTIVE` employees receive roles or memberships; removal closes the interval, nothing is deleted. Admin groups are created by a Super Admin with a unique name and are create-only for now (no rename or retirement).
- *Note — [APPROVED 2026-10-29]:* until the recovery below exists (M14), the last active Super Admin cannot be suspended, terminated or scheduled for termination (`409 last_super_admin`; see D7). M14 lifts this guard. *(Superseded: the 2026-10-30 note below restores the guard for termination.)*
- *Superseded by the 2026-10-30 and 2026-10-31 notes below: the last Super Admin cannot be terminated, and an HR Manager nominates, an independent InfoSec member or Super Admin authorizes, and the nominee accepts.* When the last Super Admin is terminated, termination proceeds immediately (login disabled, sessions ended) and the system enters `ADMIN_RECOVERY_REQUIRED`. An HR Manager nominates (self allowed); a different InfoSec member authorizes.
- **Last resort:** if no eligible authorizer exists, recovery reuses the initial Super Admin bootstrap mechanism. It is available only when no active Super Admin exists, and it is recorded in the audit history.
- A health warning shows whenever fewer than two active Super Admins exist. Concurrent cases cannot leave inconsistent state.
- [DEFAULT] Open nominations expire (e.g. 7 days) and are cancelled if the nominee or initiator is terminated. Recovery mode blocks only Super Admin-only configuration; access workflows keep running.
- *Note — [APPROVED 2026-10-30] (UX simplification): HR owns the last Super Admin's succession.* Supersedes the 2026-10-29 M14 lifting of the guard and the HR-nominates / InfoSec-authorizes recovery above.
  - **Guard:** the last active Super Admin cannot be terminated, scheduled for termination or terminated by the sweep (`409 last_super_admin`: "Bu çalışan sistemin son Süper Yöneticisidir. İşten çıkış işlemini tamamlamak için önce yeni bir Süper Yönetici atanmalıdır."). A due scheduled termination waits. Serialized by the lifecycle lock.
  - **Emergency revocation:** an HR Manager may suspend a Super Admin at once (sessions end, privileged use blocked, tool access suspended through the normal workflow and shown suspended only once confirmed), and may schedule or complete their termination; reactivating a Super Admin and resetting their password still need a Super Admin, and an account that also holds InfoSec still needs a Super Admin. Suspending the last one records `ADMIN_RECOVERY_REQUIRED`.
  - **Succession:** while at most one Super Admin is active, an HR Manager (not a Super Admin) nominates an active employee other than themself; the outgoing Super Admin's approval is not needed. The nominee gets a single-use link valid for 24 hours in their own Mock Email Outbox (only the token's SHA-256 is stored, on the nomination, apart from activation tokens; the encrypted copy is cleared on use, reissue or expiry). Only the nominee, signed in as themself, accepts; their password is unchanged and their sessions end so they sign in again with the role. HR sees the status and reissues an expired or lost link, never the link itself. HR and the nominee are the two people. Nomination, link issue, acceptance (and refused attempts), role assignment, suspension and termination are audited without the token.
  - **One succession at a time:** a second succession nomination is `409 nomination_pending` while one is open (an expired one is closed first). A link can be reissued or accepted only while at most one Super Admin is active (`409 super_admins_active`), and accepted only while the HR proposer is still an active HR Manager. Suspending or terminating someone cancels the open nominations they proposed or received.
  - Ordinary promotions keep the two-person rule above. The `recover-admin` command stays the last resort while no Super Admin is active.
- *Note — [APPROVED 2026-10-31] (final QA, F-1 and R-2; refines the 2026-10-30 note above):*
  - **Suspended Super Admins count (F-1):** HR's succession is open only while at most one Super Admin is **not terminated** (active or suspended), instead of "at most one active". HR keeps emergency suspension of any Super Admin, but suspending them no longer opens a succession. With no active Super Admin and two or more suspended ones, `recover-admin` is the way back. Link reissue and acceptance use the same count (`409 super_admins_active`).
  - **Independent authorization (R-2):** HR nominates; an **InfoSec member or an active Super Admin** who is neither HR's proposer nor the nominee authorizes (or declines) from the approval inbox; only then does the nominee receive the single-use 24-hour link, and the nominee accepts as before. HR and the nominee are no longer enough. The nomination stores the authorizer (`authorized_by`, `authorized_at`, migration 00019) apart from the acceptance; a link without an authorization is refused (`SUPER_ADMIN_ROLE_ACCEPT_FAILED`, reason `not_authorized`). HR reissues a link only after authorization (`409 nomination_not_authorized` before).
  - **Notifications:** every active InfoSec member and Super Admin (other than HR's proposer and the nominee) is notified of HR's nomination, and again, with the proposer, when the role is accepted. Nomination, authorization or decline, link issue, acceptance and refused attempts stay audited without the token.


## D3. On-behalf requests — [POLICY]
When the beneficiary's **current direct manager** submits a request for them, the submission satisfies the Manager step, recorded as `APPROVED_BY_SUBMISSION`. Any other submitter triggers the normal Manager step. Remaining steps still apply with distinct people.
*Rationale:* a separate approval click by the same person adds no control and slows onboarding.
- *Note — [APPROVED 2026-10-30, user instruction (UX simplification)]:* only the beneficiary themself, someone above them in the management chain (direct or indirect manager), an HR Manager or a Super Admin may submit a request for them; anyone else gets `403 beneficiary_not_allowed`. Previously the server accepted any active beneficiary from any employee and only the UI limited the choice.
- *Note — [APPROVED 2026-10-31] (final QA, R-3 and ENG-006):* (a) a direct manager who is also an **HR Manager or Super Admin** no longer satisfies the Manager step by submitting: both roles can set reporting lines, so moving someone under themself and submitting would make them their own approver. Their submission triggers the normal Manager step, which separation of duties routes to someone else (manager's manager, then InfoSec). (b) Extensions and level changes on behalf of someone else (`POST /grants/{id}/change`) follow the same rule as new requests: only someone above the beneficiary in the management chain, an HR Manager or a Super Admin; anyone else gets `404 not_found`, as for a grant they cannot see.

## D4. One access level per tool — [POLICY]
- At most one active level per person per tool in the settled state.
- A different level is a `LEVEL_CHANGE`, approved under the **new level's approval steps** and provisioned/verified normally.
- Requesting a level already held does not create a duplicate grant; an extension is offered where appropriate.
- Every operation identifies the specific **(tool, account, access level)**. The Mock Tool Service stores permissions per level, so revoking one level never removes another level or the account.
- Every level change grants and verifies (API) or confirms (Manual/Vendor) the new level first; only then is the revocation of the old level created.
- If granting the new level fails, the old level is preserved and nothing is revoked; the failure is visible and retryable (O3).
- If revoking the old level fails, both levels are held and an **overlap exception** is shown until removal is verified or confirmed.
  - *Refinement — [APPROVED 2026-10-28, M8 plan]:* the overlap is shown from the old removal's first failed attempt until that removal is verified or confirmed: the old grant has an open removal with an error recorded, and the grant that replaces it is `ACTIVE`, whatever the removal's reason. A removal that is merely pending is ordinary work and not listed (O7).
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
- *Refinement — [APPROVED 2026-10-29] (M10):* when the manager changes, pending requests whose Manager step the previous manager already approved are cancelled ("transferred"); the employee submits a new request for the new manager. A Manager step still pending is re-routed to the new manager. A department-only transfer re-routes no Manager step.
- *Refinement — [APPROVED 2026-10-29] (M10 plan):*
  - **What HR reviews:** every open grant not already being removed (provisioning, pending fulfillment, active, failed, and suspended while its resume is on its way). A grant created after HR read the list is removed with the rest. Only `ACTIVE` employees are transferred; a suspended one is reactivated first.
  - **Revoke everything** means nothing kept: every reviewed grant is removed and every pending request is cancelled ("transferred"); no step is re-routed.
  - **Retention** is stored on the grant (when, by whom, the justification), shown as `retainedOnTransfer`; the new manager gets one in-app notification listing the kept items. Each transfer's history is in its `EMPLOYEE_TRANSFERRED` audit event.
  - **Stale grant:** a grant that activates while its transfer removal is open is marked stale and audited as `STALE_GRANT_INCIDENT`; the queued removal then runs, and the grant is listed in Exceptions until the removal is verified or confirmed.
  - A manager or department change goes through the transfer only; editing an employee changes the job title alone.
- *Refinement — [APPROVED 2026-10-29] (M10 review):* "revoke everything" applies only when the transfer removed at least one grant. A person with nothing to review (no open grants) keeps their pending requests; on a new manager a pending Manager step is re-routed and a request the previous manager approved is cancelled, as above.

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
- *Refinement — [APPROVED 2026-10-28, M8 plan]:* **expiry is end of day.** A temporary grant stays valid through its whole expiration date in the company timezone; it becomes due for removal at 00:00 on the next day (an expiration date of 30 November is removed from 1 December, 00:00 Europe/Istanbul). The expiry job requests removal for open grants whose date is before today, with reason `EXPIRED`, and never sends notifications.
- *Refinement — [APPROVED 2026-10-28, M8 plan]:* **an item approved after its expiration date** (the date passed while it waited) is cancelled at final approval ("expired before approval"); no grant is created.
- *Refinement — [APPROVED 2026-10-28, M8 plan]:* **extensions.** An extension (a later date, or Until Revoked) applies to a temporary `ACTIVE` grant and only changes its expiration date at final approval, with no tool call; the access stays active meanwhile and expiry is never held back by a pending extension. An extension approved after the grant was closed or became due is cancelled ("the access has ended"). Closing a grant (revocation, expiry, cancellation) cancels its pending extension or level change in the same transaction. A direct grant (D1) may be extended only through this normal workflow, including to Until Revoked; the 90-day limit applies to the direct grant itself.

*Trade-off:* privileged permissions (e.g. AWS Admin) may be held Until Revoked after a single approval chain. For these grants expiry provides no protection; removal relies on quarterly reviews (with day-21 auto-revocation for unanswered InfoSec-approved items, D8; *superseded, see the note below*), manager and HR actions, and termination. Accepted for simplicity and to avoid duration configuration.
*Note — [APPROVED 2026-10-30] (UX simplification):* day 21 no longer auto-revokes (see the D8 note); unanswered privileged items become security findings that InfoSec follows up.

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

- *Note — [APPROVED 2026-11-01]:* on the optional GitHub sandbox tool, which has no suspended state, `SUSPEND` removes the access and `RESUME` invites again; the grant is active only once the invitation is accepted and Read verified (A1 note 2026-11-01).

- *Refinement — [APPROVED 2026-10-29] (M9):*
  - **Suspension state:** a grant is stored as `SUSPENDED` only once its `SUSPEND` is verified or confirmed; until then it stays `ACTIVE` and shows `SUSPENSION_REQUESTED` or `SUSPENSION_FAILED`. Resume mirrors it (`RESUME_REQUESTED`, `RESUME_FAILED`). Suspension intervals are kept as history (A9).
  - **Grants in flight:** at suspension a `GRANT` never sent is cancelled; one sent or failed continues and is suspended in the transaction that activates it. A level change's old grant is suspended and its replacement follows the same rule. Termination removes every open grant, the replacement included. A failed `SUSPEND` stays in Exceptions until verified or confirmed.
    - *Note — [APPROVED 2026-10-29] (M9 review):* termination also cancels a claimed Manual or Vendor `GRANT` task, so the leaver's removal task is ready at once; an ordinary revoke still lets it finish (M7).
  - **Who acts:** HR Managers and Super Admins suspend, reactivate, terminate and schedule; a target holding Super Admin or InfoSec needs a Super Admin; nobody acts on themselves.
  - **Roles and memberships:** termination closes them (audited); suspension keeps them.
  - **Last Super Admin (with D2):** the last active Super Admin cannot be suspended, terminated or scheduled for termination; a Super Admin whose termination is scheduled does not count. The check is serialized across concurrent actions. *(Rule in force: the 2026-10-30 note below; termination is guarded, an HR Manager may suspend.)*
    - *Note — [2026-10-29] (M14, per the approved D2 note):* the guard is lifted; terminating the last active Super Admin proceeds and starts D2 recovery. *(Superseded by the next note.)*
    - *Note — [APPROVED 2026-10-30]:* the guard is back for termination (immediate, scheduled and the sweep) until a successor accepts; HR Managers may suspend, schedule and terminate a Super Admin (see D2).
- *Refinement — [APPROVED 2026-10-29] (M10 plan):* leaver removals (priority 0) keep running after a rehire and stay in the security revocations list until verified or confirmed. A rehire restores no grant, role, admin group membership or request.

## D8. Quarterly access reviews — [CASE] quarterly review with chasing, [POLICY] the rules
- A campaign starts every 3 months and covers all active and suspended grants, temporary and Until Revoked.
- Tool Owners decide **Keep** or **Revoke** per item.
- **Day 7:** one in-app reminder to the reviewer for each unanswered item. No other reminder schedules.
- **Day 14:** all unanswered items escalate to InfoSec; authorized InfoSec reviewers may decide directly.
- *Superseded 2026-10-30 (note below): day 21 revokes nothing; every unanswered item becomes an open finding.* **Day 21:** unanswered items whose access level **requires InfoSec approval** (per the level's configuration when the campaign starts) are revoked automatically through the normal workflow, reason `REVOKED_NO_REVIEW`. Using the current configuration also covers imported grants, which have no approval snapshot.
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

- *Note — [APPROVED 2026-10-30] (UX simplification):*
  - **Day 21 no longer revokes.** Every unanswered item, InfoSec level or not, becomes an open finding; a missed deadline never removes an active or suspended employee's access. The quarterly campaign, the day-7 reminder, the day-14 escalation, the findings and the evidence report are unchanged. Past `REVOKED_NO_REVIEW` closures stay in history.
  - **A leaver's access is never kept.** Keep on an item whose employee is terminated is refused (`409 terminated_access_not_kept`; bulk Keep skips it). An earlier Keep stays as audit history only: Keep never authorizes access, and termination removes every grant through the normal workflow regardless of it. The review page shows the access's removal state next to the decision ("Kaldırma bekleniyor", "Kaldırma başarısız", "Kaldırıldı").
  - **Wording:** the actions are "Erişim devam etsin" and "Erişimi kaldır"; a Keep shows as "Erişimin devamı onaylandı" with who and when. The page is "Erişim Kontrolü"; a campaign is titled "Erişim Denetimi — <year> <n>. Çeyrek".

*Rationale:* CASE.md requires owners to be chased; auto-revoking InfoSec-approved access enforces least privilege where risk is highest, while findings for other items avoid mass disruption without hiding non-response. *(Written for the original day-21 rule; since the 2026-10-30 note every unanswered item is a finding, and the trade-off below applies to all of them.)*
*Trade-off:* unreviewed access to levels not requiring InfoSec can persist until InfoSec acts; it is visible, but not removed.
- *Note — [APPROVED 2026-10-31] (final review, P2-2): a tool owner change moves the tool's undecided review items to the new owner.* "Tool Owners decide" (above) means the current owner: before this, an item kept the reviewer chosen at campaign start, so a former owner could still decide it and the new one could not until day 14. "Items are snapshotted at start" still fixes which grants a campaign covers; it never fixed the reviewer.
  - In the owner change's transaction (`catalog.UpdateTool`, which locks the tool), every open item of the tool (no decision, not closed; open findings included) is routed again with the campaign-start rule (`reviews.Reviewer`): to the new owner, or to the InfoSec pool when it is the new owner's own access or the new owner is a Super Admin. Each move is audited (`REVIEW_ITEM_REROUTED`, from and to reviewer); the new owner gets one in-app notification per campaign.
  - Decided items keep their reviewer, decision and evidence. Each open item is locked and re-read before it moves, so a decision committed meanwhile wins and that item is left as decided.

- *Note — [APPROVED 2026-11-01] (final review): a tool owner change also moves the tool's pending Tool Owner approval steps.* In the same transaction as the review items above (`catalog.UpdateTool` → `requests.ToolOwnerChanged`), every pending Tool Owner step of the tool not assigned to the new owner is routed again with the F2 rule (the new owner, or the InfoSec pool when they are excluded) and recorded as `STEP_REROUTED` with reason `tool owner changed`. Before, the former owner could still approve.
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
  - *Note — [APPROVED 2026-10-30] (UX simplification):* superseded by the D8 note: on day 21 every unanswered item becomes a security finding; nothing is revoked automatically.
- Seed data includes valid employee access, a terminated employee with an existing account, and an orphan account.

*Rationale:* governance must cover the access most likely to be wrong — access that predates the system.
*Trade-off:* imported access has no approval evidence; the certification campaign provides the first recorded decision.
- *Extension — [APPROVED 2026-10-29] (M17): onboarding workbook as a third source.* A Super Admin may import one `.xlsx` workbook with three sheets (Employees, Tools, Existing Access), previewed first and applied all or nothing in one transaction, under the same import lock as the other imports. Existing employees, tools and levels are never overwritten; differences are warnings. Unknown employees in Existing Access are rejected (orphans still come from the tool read and the account CSV).
  - **API-tool rows are accepted** (the original rule read API tools only from the Mock Tool Service). They become the same `IMPORTED_BASELINE` grants as every other import: no provisioning operation, no request, no approval history, approver unknown.
  - **Reported vs verified.** Such grants are *reported*: `tool_verified_at` stays empty. The tool read sets it when it records a grant or finds an already-imported one (`GRANT_VERIFIED_BY_TOOL`), and reports how many reported API grants of the tools read the tool does not hold (`unverified`). The UI labels imported access "reported" or "verified by the tool". The tool read itself is unchanged otherwise, and the account CSV still refuses API tools.
  - **Duplicates and conflicts.** Each (tool, account, level) exists once, whichever source imported it first (the existing unique index); a repeat is skipped, an account already imported for another employee or already removed is a warning, and the same account claimed twice in one file is a problem.
  - **Security implications.** (1) The import is trusted input from a Super Admin: a wrong row creates baseline access that grants nothing in the tool but shows as held until certified. (2) For an API tool the account must be empty or the employee's email (`api_account_mismatch`), because the tool read and removals address API accounts by email; another account would let a later automatic removal revoke someone else's real access. (3) Removing a reported API grant the tool does not hold is harmless (the tool answers "no change" and the lookup confirms it is gone); suspending one fails and appears in Exceptions. (4) Manual and Vendor rows cannot be verified by any tool; the certification campaign is their first check.
  - *Rationale:* one file lets a company onboard in dependency order without a separate API-only path, while verification keeps reported and observed access apart.
- *Note — [APPROVED 2026-10-30, user instruction (UX simplification)]:* the Exceptions screen groups a departed employee's remaining access (open `TERMINATION` removals) and orphan accounts as **"Sahipsiz ve Şüpheli Hesaplar"**, each with its source and why it was flagged. An orphan is labelled from its account as a hint only: a company account with no employee record (its email domain is used by employees), possibly external (a vendor or agency domain), or needing manual investigation (not an email address). Nothing is assigned or removed automatically; InfoSec still revokes or dismisses with a reason. A direct revoke (`POST /grants/{id}/revoke`) of an orphan's grant is `409 orphan_account` for a Super Admin and `404` for others (it used to fail with a 500); orphans are handled only through their finding.

- *Note — [APPROVED 2026-11-01] (final review): an open orphan account blocks requests for the same account.* A request, direct grant or level change for a tool is refused (`409 orphan_account_exists`, field `accessLevelId`) while an open, undismissed orphan grant of that tool has the beneficiary's e-mail as its account. Otherwise the new grant would share the account in the tool with the orphan (a read-back would see the orphan's access, and removing either would end both). InfoSec revokes (or dismisses) the orphan first; orphans are still never assigned automatically.
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
- *Development fixture — [APPROVED 2026-10-28]:* a small development fixture (M3) runs only when explicitly invoked (`docker compose exec app /accessflow dev-fixture`), never automatically; it is idempotent, creates records through the real services as the system actor, and marks them seed-originated.
- *Note — [APPROVED 2026-10-29] (demo seed, M15; supersedes the development fixture):* in demo mode (A4) the demo seed runs automatically at startup, once: only on a database that holds just the demo Super Admin, and it records completion so later starts change nothing. A database with other data and no completion record (an interrupted first start, or one from an earlier version) is refused with the reset instruction (`docker compose down -v`) rather than completed by guesswork. It acts only through the real services, at real time, as the seeded people whose role the action needs (for example a manager approves, an admin group member confirms a task), so every audit event is real; employees are marked seed-originated. Legacy tool accounts for the baseline import are created through the Mock Tool Service's own operations API, as a tool admin would have before AccessFlow. Data that only elapsed time produces (late or day-21 review findings, retries exhausted over hours) is not seeded. Outside demo mode nothing is seeded; the `dev-fixture` command is removed.
  - *Note — [APPROVED 2026-10-30] (M18):* the seven documented demo accounts keep their permanent passwords; every other seeded employee gets no credential and no activation link (a Super Admin can send one), so the outbox holds no activation email after the first start.
  - *Note — [2026-10-31, user instruction] (submission readiness):* an eighth documented demo account, `emre.kaya@accessflow.local` (`cloud` in `DEMO_PASSWORDS`), shows the cloud tool owner: the existing owner of AWS Console, who is also the second InfoSec member and an IT Operations member. No role, group, tool or seed record changes; one more documented account gets a fixed local password. The "seven" accounts above and the A4 demo-mode note's list are now eight.
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
- *Note — [APPROVED 2026-11-01]:* one API tool can optionally be served by the GitHub sandbox adapter instead of the Mock Tool Service; its results are verified by reading GitHub back (A1 note 2026-11-01).

**Item status** — each request item shows its own progress, independently of other items in the same submission. Approval and fulfillment are separate; an approved item is never shown as active access until fulfillment is verified or confirmed.

| Method | After all approvals | While in progress | Done | Failure |
|---|---|---|---|---|
| **API** | Grant operation sent automatically to the Mock Tool Service | `PROVISIONING` (including bounded retries, O3) | `ACTIVE / VERIFIED` after a successful status lookup | `FAILED`, retryable by an authorized admin |
| **Manual** | Fulfillment task for the responsible Admin Group | `PENDING_FULFILLMENT` | `ACTIVE / SELF_REPORTED` when an authorized admin confirms; who and when recorded | Admin reports failure → `FAILED` |
| **Vendor** | Fulfillment task for the responsible Admin Group; admin records the vendor ticket reference | `PENDING_FULFILLMENT` until the ticket reference is recorded, then `WAITING_VENDOR` | `ACTIVE / SELF_REPORTED` when the admin records the vendor's confirmation; never claimed as independently verified | Admin reports failure → `FAILED` |

Revocation, suspension and resume follow the same pattern and labels (see O1, D7).

- *Refinement — [APPROVED 2026-10-28]:* **retired before provisioning.** An approved item whose access level or tool was retired is never sent to a tool. It is checked at final approval (together with stricter wins, F2) and again when the runner claims the grant operation. The item, or its grant and operation, become `CANCELLED` with the reason ("access level retired" or "tool retired"), recorded in the audit history and shown to the requester, who may request another level.
- *Refinement — [APPROVED 2026-10-28]:* **grant lifecycle.**
  - An approved item gets one grant and its grant operation in the same transaction as the final approval.
  - Grant statuses: `PROVISIONING`, `PENDING_FULFILLMENT` (Manual/Vendor), `ACTIVE`, `FAILED`, `REVOCATION_REQUESTED`, `REVOCATION_FAILED`, `REVOKED`, `CANCELLED`.
  - A grant is open until it is `REVOKED` or `CANCELLED`. A `FAILED` grant stays open, because the tool stores permissions per level and revoking an old failed grant would remove a newer one's access. While a grant for a tool is open, a new request for that tool is rejected (D4).
  - Removal of a grant that was queued and never attempted cancels it without a tool call. Otherwise a revoke operation runs after any grant operation in flight.
  - A failed operation is retried manually by a Super Admin or a member of the tool's administration group, never by the grant's own employee. A retry starts a new round of the O3 attempts with the same operation ID.
  - Exceptions are readable by InfoSec, Super Admins and Auditors.
    - *Note — [APPROVED 2026-10-31] (final QA, Auditor):* no longer by Auditors (O7 note 2026-10-31).
- *Refinement — [APPROVED 2026-10-28, found during M6 implementation]:* **safe fulfillment.** These tighten the two refinements above:
  - **Order at final approval:** the retired check runs first. A retired level or tool cancels the item before any stricter-policy step is added (F2), so nobody is asked to approve an item that must be cancelled.
  - **"Never sent to the tool"** means the operation has never been claimed, recorded as its first attempt time, not "0 attempts". A manual retry resets the attempt count, and an earlier attempt may have reached the tool even though its answer was lost.
  - **Once a grant operation may have reached the tool, retirement no longer cancels it.** It is completed and verified, so no permission is left in the tool unrecorded; removing it is a normal revocation. The runner's retired check therefore applies only to grant operations never sent.
  - **A grant attempt that fails after removal was requested is cancelled, not retried.** The verified removal takes over, so a retried grant can never re-apply access after the removal.
  - **One lock order** for every fulfillment change: employee, then operations, then grant (approval, revoke, retry and the runner), so they cannot deadlock.
    - *Note — [APPROVED 2026-10-31] (final QA):* the lock order holds for fulfillment changes. A few rare races outside it (a submission racing a tool-owner change, a person-level action racing a request on the same person) can still deadlock; PostgreSQL aborts one transaction, which wrote nothing, and `platform.InTx` retries it, at most three attempts in all, only on a deadlock or serialization failure. The lock order and every transaction boundary are unchanged.
- *Refinement — [APPROVED 2026-10-28, M7 plan]:* **Manual and Vendor tasks.**
  - A task is the Manual or Vendor operation itself, in the responsible admin group's queue. **Only active members of the tool's admin group** claim and work it (record the vendor ticket, confirm, report failure); never the grant's employee (F2), and never the initiator of a direct grant or direct revoke (D1). A Super Admin who is not a member cannot work a task; retrying a `FAILED` task stays open to Super Admins and group members.
  - A task whose claimer is no longer an active member of the group can be taken over by another member. There is no separate release action.
  - Claiming a grant task never sent checks retirement, as the runner does: a retired level or tool cancels the grant (retired before provisioning).
  - A retry returns the task to the queue unclaimed and without a ticket reference; the earlier reference stays in the audit history.
  - `WAITING_VENDOR` is a display status (a Vendor grant task with a recorded ticket), not a stored grant status; the grant status list above is unchanged.
  - Suspend and resume tasks come with suspension (D7, M9).
- *Refinement — [APPROVED 2026-10-28, M7 plan]:* **direct-grant notifications (D1).** A direct grant notifies all active InfoSec members, the beneficiary's manager and, when the level requires a Tool Owner step, the tool owner (the bypassed approver). A direct revoke is audited only.

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
  - *Note — [APPROVED 2026-10-30] (M18):* a second kind of mock email: one **activation** email per issued activation link (F4), to the employee. Only Super Admins see these emails; InfoSec and Auditor keep seeing access-request emails only. The Mock Email Outbox is a local simulation, not suitable for production.
    - *Note — [APPROVED 2026-10-31] (final QA, Auditor):* Auditors no longer see the Mock Email Outbox (O7 note 2026-10-31).
  - *Clarification — [APPROVED 2026-10-31] (architecture rationale): why a Mock Email Outbox, and where its boundary is.*
    - **Need.** Onboarding, rehire and account recovery need a way to hand an employee a credential they choose themselves (F4), and O2 asks Managers to be told about access requests. CASE.md requires no e-mail provider, and `docker compose up` must work with no setup and no credentials (A2, D12).
    - **Alternatives considered.** A real provider (SMTP or an API) needs credentials, a domain and network access and makes tests depend on delivery. A Mailpit/MailHog container adds a service and shows every message to whoever opens it, which is the same trust boundary with less control. Returning the link in the API response or the log would put a credential where it is copied and retained. All were rejected for the case.
    - **What it is.** An `email_outbox` table in the application database, written in the same transaction as the business change, shown in the app (Yönetim ayarları → E-posta kutusu). Activation e-mails are seen only by Super Admins, access-request e-mails by Super Admins and InfoSec, and a Super Admin nominee's role-acceptance e-mail (D2) only by its recipient.
    - **Activation links** (`auth.IssueActivation`): 32 random bytes (base64url); only the SHA-256 is stored (`activation_tokens.token_hash`, unique). Valid 24 hours, one use; issuing a new link revokes earlier ones, removes the current password, ends every session (`token_version`) and clears a lockout. The outbox keeps the link AES-256-GCM-encrypted, bound to the e-mail and employee, with a key in a file on the `appdata` volume (never in PostgreSQL); the ciphertext is cleared when the link is used, replaced or expires. `POST /auth/activate` checks the link again under the employee's lock, is rate-limited with sign-in, and never signs anyone in.
    - **Audit.** `ACTIVATION_LINK_ISSUED`, `ACTIVATION_LINK_VIEWED` (who, `privileged`), `ACTIVATION_COMPLETED` (with `viewedBy`: every Super Admin who revealed that link) and `ACTIVATION_FAILED` (used, replaced, expired or inactive).
    - **Privileged safeguards** (R-1, F4 note above): nobody reveals their own link; a Super Admin or InfoSec account's link is revealed only by an active Super Admin who did not issue it, and the other Super Admins are notified.
    - **Boundary.** The reveal is a local-demonstration stand-in for delivery, not a production e-mail security model: a Super Admin who reveals an ordinary employee's pending link (HR Managers included) could use it. The audit trail and the employee finding the link used make that visible; it does not prevent it. A real deployment sends each link directly to its employee through a controlled provider and removes the reveal; the outbox table can then become the delivery queue, written in the same transaction as today.
  - *Note — [APPROVED 2026-10-31] (user decision): the Mock Email Outbox is a Super Admin screen.* Supersedes "access-request e-mails by Super Admins and InfoSec" above: only Super Admins see the outbox, its access-request and activation e-mails and the menu item; InfoSec, HR Managers, managers, Tool Owners, admin-group members and employees are refused (`403 forbidden` on `GET /email-outbox`). The one exception keeps D2 succession working: a Super Admin nominee, who is not yet a Super Admin, sees only their own role-acceptance e-mail while its nomination is open and unexpired, and the menu item while it waits (`roleOffer` on `/auth/me`). An Auditor is refused whatever other roles the account holds (O7). The reveal rules (F4, R-1) are unchanged. Approvals are unaffected: the access-request e-mail only informs, and every decision is made in the Approval Inbox.
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
      - *Note — [APPROVED 2026-10-31] (final QA, user decision): the Auditor is an external, independent auditor and reads only the audit events.* Supersedes "Audit Explorer and evidence reports" above. *(Amended by the submission-readiness note at the end of this item: point-in-time access and approval evidence are also allowed; the evidence reports stay refused.)*
        - **Allowed:** listing the audit events company-wide, filtering them by date, actor, action, tool and text, paging through every matching event with its exact total, and exporting every matching event as one CSV file (streamed by the server, not only the loaded rows). The filter choices (actors seen in the log, tools) come with the audit log, not from the employee directory.
        - **Refused (`403 forbidden`), on every other business endpoint:** requests (own or on behalf), My Requests and My Access, the employee directory and employee or team access, approvals, reassignment and overrides, grants, revokes, extensions and level changes, tools, roles, groups and policies, fulfillment tasks, Exceptions, reviews, the Mock Email Outbox and notifications, HR, InfoSec and Super Admin actions, point-in-time access, approval evidence and the evidence reports. Sign-in, sign-out, `GET /auth/me` and the own password change stay available.
        - **Mixed roles:** `AUDITOR` takes precedence. An account holding it is treated as an Auditor only, whatever other roles, management lines, tool ownership or admin-group memberships it has.
        - **Enforcement:** default deny in the API's session middleware (`auth.RequireSession`), with an exact allowlist of three `GET` routes, so a new endpoint is refused to Auditors unless it is added there. The services also no longer grant the Auditor any read beyond the events (defence in depth). The UI shows only "Denetim kayıtları" and refuses other screens, for convenience only.
        - **Unchanged:** every other role's permissions, the audit log and its append-only protection. The Privileged roles report (D11) still lists who holds the Auditor role, for Super Admin and InfoSec.
        - *Note — [APPROVED 2026-10-31] (final QA, user decision): an Auditor account holds no operational duty.* Precedence alone left work an Auditor cannot act on (a Manager or Tool Owner step, a review item, a fulfillment task) waiting for InfoSec, so the API now keeps the two apart in both directions, in the same transaction as the change:
          - **Becoming an Auditor:** `AUDITOR` is refused (`409 auditor_has_duties`) while the account has another open role, an admin-group membership, an active tool it owns, a non-terminated direct report, a `PENDING` approval step assigned to it, an open review item it reviews, or a pending Super Admin nomination. `field` names the first such duty (`roles`, `adminGroups`, `toolOwnership`, `directReports`, `pendingApprovals`, `pendingReviews`, `superAdminNomination`), to remove, transfer or decide first.
          - **Duties for an Auditor:** refused (`409 employee_is_auditor`, `field` naming the input): another role (`role`), an admin-group membership (`employeeId`), tool ownership on create or update, the workbook included (`ownerId`), being a manager on create, import, transfer or rehire (`managerId`), a step reassignment (`assigneeId`) and a Super Admin nomination (`nomineeId`). Routed Manager, Tool Owner and review work follows the manager line and tool ownership, so it cannot reach an Auditor; InfoSec pool steps need `INFOSEC`.
          - Precedence stays as a second line of defence (for example for data written outside the API). Removing `AUDITOR` restores nothing; duties are given again through the normal actions.
        - *Note — [APPROVED 2026-10-31] (submission readiness, user decision): the Auditor also reads point-in-time access and approval evidence.* With the events alone, CASE §5 ("who had access to a tool on a date, and who approved it") needed a manual reconstruction. Amends "Refused" and "Enforcement" above:
          - **Allowed in addition:** `GET /audit/access` (point-in-time access at an instant or the end of a date, with each grant's state, evidence and approvers, and the removals in the 30 days before) and `GET /audit/requests` (approval evidence: each request item's approval chain with who decided each step), both company-wide. The allowlist is now these five exact `GET` routes.
          - **Still refused:** the evidence reports (`/audit/reports/*`), every action and every other screen, whatever other roles the account holds. The services refuse the reports to `AUDITOR` themselves (defence in depth).
          - **UI:** "Denetim kayıtları" shows the Auditor three tabs: Olay geçmişi, Geçmiş erişim sorgusu and Onay kanıtı; Kanıt raporları stays hidden. The filter choices come from the audit data (tools from the audit filters, people from the access held or removed on the chosen date), not from the directory or catalog. Each approval step carries the decider's name (`decidedByName`), so no directory is needed.
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
- *Clarification — [APPROVED 2026-10-31] (architecture rationale): why a Mock Tool Service.*
  - **Need.** The case is access governance: requesting, approving, provisioning, verifying and removing access, with evidence. Real GitHub, Jira, AWS or vendor integrations would need credentials, provider-specific permission models and APIs, paid or rate-limited accounts and network access, and would make the demo and tests depend on systems outside the repository.
  - **What it simulates.** One small Go service (`internal/mocktools`, own `mocktools` database, reached only over HTTP, A2) holding accounts and per-level permissions for every API tool: `POST /v1/operations` applies `GRANT`, `REVOKE`, `SUSPEND` or `RESUME`, idempotent by operation ID (a repeat returns the stored result; the same ID with other content is `409 operation_id_reused`); invalid transitions are refused (`409 level_suspended`, `not_granted`); `GET /v1/tools/{tool}/accounts/{account}` returns the held state; `GET /v1/tools/{tool}/accounts` lists a tool's accounts for the D10 baseline read.
  - **How AccessFlow uses it.** The runner (A6) sends the operation, then reads the account back; only the expected state counts as success (O1 "verified"). A `409` fails at once (retrying cannot help, O3); a timeout, `5xx` or malformed answer is retried with backoff, then `FAILED` in Exceptions with a manual retry. The tool read imports what the tool holds, verifies spreadsheet-reported API access and reports accounts matching no employee as orphans. Failures are injected in tests by an HTTP fault proxy (`testutil.Faults`: status codes, delays, malformed bodies, applied-then-failed); the running demo has no failure switch.
  - **Boundary.** The runner depends on `access.ToolClient` (`Apply`, `Account`); the baseline read also uses the HTTP client's account list. A real provider adapter implements those calls with the provider's API and maps its errors to "rejected" (final) or "transient" (retried); approval, routing, audit, lifecycle and evidence logic do not change. No real adapter exists. *(2026-11-01: except the optional GitHub sandbox adapter, A1 note below.)*
  - **Manual and Vendor** tools never reach the service: an admin-group member claims a task and confirms the outcome (Vendor with a ticket reference). That evidence is **self-reported**, labelled as such everywhere (O1), and is not equivalent to an API read-back: AccessFlow records who confirmed what and when; it cannot see the tool.
  - **Trade-off.** The mock validates AccessFlow's workflow, idempotency, retry, verification and reconciliation; it says nothing about the security or behaviour of real SaaS platforms (their permission models, eventual consistency, rate limits, partial failures).
- *Note — [APPROVED 2026-11-01] (optional GitHub sandbox adapter, Phase A: code and automated tests only):* one real adapter beside the Mock Tool Service, which stays the default.
  - **Scope.** Off unless `GITHUB_TOKEN`, `GITHUB_REPOSITORY`, `GITHUB_TOOL_ID` and `GITHUB_USERS` are all set (any partial setting stops the start). *(Amended 2026-11-01, below: `GITHUB_USERS` is removed; the three other settings remain, and the tool must also use GitHub usernames.)* Then the one API tool `GITHUB_TOOL_ID` names goes to GitHub and every other tool to the Mock Tool Service. It manages Read (`pull`) on one private sandbox repository only; the start refuses the AccessFlow repository itself, a public repository, a repository the token cannot read and a tool that is missing or not an API tool. Its settings are given only to a separate Compose project (`compose.github.yaml`, `.env.github`), never to the default one. No new dependency; the token is sent only in the `Authorization` header and is redacted from every error, audit detail and log.
  - **Ownership.** AccessFlow records what it created in its own ledger (`github_access`, per grant, never deleted). A grant is refused (`409`, Exceptions) while the account already has any access or pending invitation, so AccessFlow never takes over access it did not grant. GitHub is changed only for a grant recorded there: a removal without a record, a permission changed by hand (for example raised to Write) and access that remains through a team or the organisation are rejections for a person, never removed automatically.
  - **Invitations.** GitHub sends and e-mails the invitation itself. Until GitHub shows it accepted and the permission reads `read`, the grant stays `PROVISIONING`, never `ACTIVE` or verified; the runner checks every minute without using the retry budget and audits `INVITATION_PENDING` once. Not accepted within 7 days (GitHub's own lifetime), or declined: `FAILED`, the invitation cancelled. A removal before acceptance (revoke, expiry, review, transfer, offboarding) cancels the waiting `GRANT` at once (existing D5/D6 rule) and its `REVOKE` deletes the invitation; an invitation accepted in that instant is removed with it, and the read-back must show no access and no invitation.
  - **Suspension (D7) on a tool without a suspended state.** `SUSPEND` removes the collaborator (fail-safe) and is verified as "no access"; the ledger records it as `SUSPENDED`. `RESUME` sends a new invitation; the grant becomes `ACTIVE` only once it is accepted and Read is verified. Offboarding a suspended employee ends the record without another GitHub change.
  - **Trade-offs.** The person's GitHub login is mapped in configuration (`GITHUB_USERS`), not stored on the employee *(amended 2026-11-01, below: the login is entered on the request and stored with GitHub's user id)*; no baseline import (D10) reads GitHub; a level change on the GitHub tool is refused (one live record per account). Residual races are documented in `docs/known-limitations.md`.
- *Amendment — [APPROVED 2026-11-01] (tool-specific external account identity; replaces `GITHUB_USERS`):*
  - **Why in AccessFlow, not configuration.** Which GitHub account receives access is part of what is requested and approved. A login in an environment variable was invisible to the requester, Manager and Tool Owner, absent from the audit trail, changeable by whoever edits the deployment and not tied to one request. It is now entered on the request (or the Super Admin's direct grant), checked by the server and recorded with the decision.
  - **Why per tool.** Each tool names accounts its own way. A tool has an optional, fixed **account identity** (`tools.account_kind`): empty means the employee's email (every existing and demo tool, the Mock Tool Service, Manual and Vendor, all unchanged); `GITHUB_LOGIN` means a GitHub username and is allowed only for an API tool (`account_kind_requires_api`), since only the adapter acts on it. It is chosen when the tool is created and never changes, because grants already recorded depend on it. No generic identity framework: one kind exists because one adapter needs it.
  - **Username and numeric id.** GitHub logins can be renamed, and a freed login can be claimed by someone else. The request stores the login as GitHub spells it and GitHub's immutable numeric user id (`request_items.account`, `account_ref`), both copied to the grant (`grants.account`, `account_ref`) and the adapter's ledger (`github_access.account`, `user_id`). Before every change and every read-back the adapter reads the user by id; if the login no longer matches (renamed) or the user no longer exists, it changes nothing and fails the operation with an explanation in Exceptions. A removal is therefore never reported done while the renamed account may still hold the access; a person checks the repository and resolves it.
  - **Validation.** On submit and direct grant the server requires the field for a `GITHUB_LOGIN` tool (`account_required`), refuses it for an email tool (`account_not_allowed`), checks GitHub's username format without a call (`account_invalid`), and reads `GET /users/{login}`: unknown is `account_not_found`, an organisation or bot is `account_not_allowed`. The lookup runs before the database transaction, so no lock waits on GitHub. Without the adapter, or when GitHub cannot be reached, the request is refused with `503 account_check_unavailable`; nothing is recorded unchecked. *(Clarified 2026-11-01: a repeated `submissionId` returns the saved request before any lookup (A9), so a retry never needs GitHub.)*
  - **When it becomes immutable.** At submission. A trigger refuses any later change of the account or its id on items and grants (and of a tool's account identity). A different account means a new request, approved again. A level change or extension copies the held grant's account; it cannot name another one.
  - **Approval, audit, revocation.** Approvers see "GitHub hesabı: @login · GitHub kimliği id" in the inbox, the decision drawer and the request; My Access and the employee page show it on the grant. `REQUEST_SUBMITTED` and `DIRECT_GRANT_CREATED` record `account` and `accountRef`, as do the direct-grant notices. Every operation, removal and read-back uses the stored login and id, never a value from configuration.
  - **How this differs from the Mock Tool Service.** Mock tools are addressed by the employee's email and need no lookup. A GitHub tool's account is a real external identity that exists before AccessFlow, may be renamed and is checked live. The workbook import and the CSV import refuse access rows for such a tool (`account_kind_tool`, `api_tool`), and the tool read skips it: such access is only ever requested.
  - **Sandbox limitations and risks.** The lookup proves the account exists, not that the requester owns it; the person proves it by accepting the invitation from their GitHub account. A typo naming someone else's existing account sends that person the invitation; approvers see the login, and access starts only on acceptance. A rename after the grant leaves the operation for a person, by design. The adapter remains a sandbox tool: one repository, Read only, one tool, not production-ready (`docs/known-limitations.md`).
- *Note — [APPROVED 2026-11-01] (integration status and GitHub errors):* a Super Admin reads `GET /api/v1/integrations/github` (`{enabled, toolId}`; no GitHub call, no token, repository or other setting); each API tool's page shows *Gerçek GitHub API entegrasyonu aktif* for the adapter's tool and *Mock Tool Service kullanılıyor* otherwise, decided by the backend, never by the tool's name. A GitHub rate limit on the username lookup is `429 account_check_rate_limited`; it and `503 account_check_unavailable` name the `account` field. Stored operation errors keep only GitHub's status code (`github status 422`); GitHub's reply text goes to the server log, and the UI explains each failure in Turkish. No connectivity-test action: the start-up checks remain the only GitHub check outside operations. *(Amended 2026-11-01, approved: any signed-in employee may read the status, so the tool catalog card shows the same label to requesters; the answer stays `{enabled, toolId}` and names only a tool the catalog already lists.)*
- *Note — 2026-11-01 (live verification, no rule changed):* one live run on 2026-11-01 against `accessflow-sandbox/demo-project` with one test account confirmed the decisions above on the main path: the account lookup stored `esrahayta` with its numeric id, the grant stayed `PROVISIONING` while the invitation was pending, became `ACTIVE / VERIFIED` only once GitHub showed it accepted with `read`, and a direct revoke removed the collaborator and was verified (`REVOKED / VERIFIED`). See docs/known-limitations.md for what was not run live.

### A2. Docker Compose layout — [APPROVED 2026-10-28]
- Three services:
  - **`postgres`** — one PostgreSQL server with separate `accessflow` and `mocktools` databases, each with its own database user restricted to its own database. Persistent named volume.
  - **`app`** — one multi-stage image: Go backend, background jobs and the compiled React frontend served from the same origin.
  - **`mocktools`** — a small independent Go HTTP service, reachable only on the internal Docker network.
- Only `app` is exposed, on port **8080**. *(Approved 2026-10-30: the host port can be changed with `ACCESSFLOW_PORT`, default 8080, for machines where 8080 is taken; the container port stays 8080.)*
- Migrations run automatically at startup. Health checks and startup dependencies ensure services start in order.
- `docker compose up` works on a clean machine with no manual setup steps.
- AccessFlow never queries the `mocktools` database; it uses only the Mock Tool Service's HTTP API.
- A Mock Tool Service error, timeout or unexpected response is never treated as success; it goes through the retry and exception workflow (O3).
- *Local database credentials — [APPROVED 2026-10-28]:* the PostgreSQL users use simple, fixed local credentials written in `compose.yaml`. No secret-management infrastructure; `docker compose up` needs no configuration. Postgres is not published to the host. *Documented limitation:* the credentials are in the repository and are applied only when the database volume is first created.
- *Test database — [APPROVED 2026-10-28]:* tests use a separate, disposable PostgreSQL service (`postgres-test`, in-memory storage) that exists only under the `test` Compose profile. Test data never touches the application database, and the administrative connection that creates test databases is used only there. The three runtime services above are unchanged.

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
- *Note — [APPROVED 2026-10-29] (M17):* **`xuri/excelize` v2** reads and writes the onboarding workbook (`.xlsx`). It runs with decompression limits; macros, formulas and encrypted files are refused, and nothing in a workbook is evaluated.

### A4. Authentication — JWT in HttpOnly cookie — [APPROVED 2026-10-28]
**Tokens**
- After a successful login, a short-lived JWT (HS256 only; other algorithms rejected) is issued in an `HttpOnly`, `SameSite=Lax` cookie (`Secure` enabled by setting; off for plain-HTTP localhost). No refresh tokens, no Redis, no auth service.
- Claims: employee ID, token version, CSRF value, original login time, issued-at, expiry. Lifetime **30 minutes**; while the user is active, the server re-issues the cookie when less than half the lifetime remains. A re-issued token keeps the original login time, and its expiry never goes past **12 hours** after that login; then a new login is required. This is a re-issued access cookie, not a refresh token.
- No token denylist, refresh tokens or session table.
- The signing secret comes from an environment variable; if none is set, a random secret is generated on first start and stored in the `accessflow` database, so a clean `docker compose up` needs no setup.

**Every authenticated request**
- Validates signature and expiry, then loads the employee from PostgreSQL: status must be `ACTIVE` and the token version must match. Roles and admin group memberships are read from the database each time, never trusted from the token.
- `SUSPENDED` and `TERMINATED` users are denied immediately, regardless of token expiry.
- *Note — [APPROVED 2026-10-31] (final review, P2-3):* the session check reads roles before the action takes any lock, and role changes lock the employee (`people.RemoveRole`). An action that needs a role re-reads the actor's status **and roles** after its own locks (`access.CurrentActor`), so a role removed while the request waited no longer counts: InfoSec decisions on pool approval steps, Super Admin overrides, reassignments, review decisions (InfoSec on pool or escalated items, Super Admin Revoke), orphan revoke and dismiss, and the Super Admin retry of a failed operation. Admin-group membership for fulfillment tasks was already read under the lock. Regression test: `TestCriticalActionsSeeARoleRemovalCommittedWhileWaiting`.

**Token version**
- Each employee has a `token_version`. It is incremented on **logout**, password change, password reset, suspension, termination and reactivation, invalidating all existing tokens for that employee.

**CSRF**
- The CSRF value in the JWT is also set in a readable cookie; every state-changing request must send it in an `X-CSRF-Token` header matching the JWT claim. The `Origin` header is also checked.
  - *Implementation — [APPROVED 2026-10-28]:* the Origin check uses Go's standard `net/http.CrossOriginProtection` (Go 1.25+), which rejects unsafe cross-origin browser requests using `Sec-Fetch-Site` or `Origin` versus `Host`. It applies to every unsafe `/api` request, including login, in addition to the `X-CSRF-Token` check on authenticated requests.

**Passwords and bootstrap** (preserves F4 and D2)
- argon2id hashes (OWASP minimum parameters, per-password salt, parameters stored with the hash). Length 12–128, no composition rules.
- Temporary passwords: 16 random characters, shown once, hashed, single use, expire after 72 hours. A token issued from a temporary password allows only password change and logout. *(Employees get activation links instead since M18, F4 note 2026-10-30; the bootstrap Super Admin's one-time password remains.)*
  - *Refinement — [APPROVED 2026-10-28]:* see F4, temporary password semantics. The 72-hour expiry applies to employee temporary passwords; the bootstrap Super Admin's does not expire.
- Generic login error; 5 consecutive failures lock the account for 15 minutes (checked at login, no job). Logins, failures, lockouts and password changes are audited.
- First start: if no Super Admin has ever existed, one is created from a configured email with a one-time temporary password. D2 last-resort recovery is a one-off command (`docker compose exec app …`), usable only when no active Super Admin exists, audited, never automatic.
- **Bootstrap credential delivery — [APPROVED 2026-10-28, refines the above]:** the temporary password is written to a private file on a persistent Docker volume (`/var/lib/accessflow/bootstrap-admin.txt`, mode 0600) and is **never printed in application logs**. The log states only how to read it (`docker compose exec app /accessflow bootstrap-password`; see the 2026-10-28 note below). The file is removed after the initial password change; single use and 72-hour expiry still apply, and `recover-admin` issues a new one the same way. *(Refined 2026-10-28: the bootstrap password does not expire; it stays valid until the first successful password change, after which it is invalid and the file is deleted. `recover-admin` remains only the D2 last-resort recovery when no active Super Admin exists.)* *(Approved 2026-10-28: the distroless image (A2) has no `cat`, so the file is read with `docker compose exec app /accessflow bootstrap-password`, a subcommand of the app binary like `healthcheck`; the log names that command. History: originally `docker compose exec app cat /var/lib/accessflow/bootstrap-admin.txt`.)* `docker compose up` still needs no manual setup. *Documented limitation:* anyone with access to the Docker host, the Docker daemon or the volume can read the file while it exists. *(History: previously "printed once in the app log"; changed because logs are often collected and retained.)*
- Seeded demo users (D12) share a local-only password from a setting with a documented Compose default; the seed is never used in production. *(Superseded by the demo-mode note below.)*
- *Note — [APPROVED 2026-10-29] (demo mode, M15):* a local demo mode (`ACCESSFLOW_DEMO=true`, set only in `compose.yaml`; off by default) gives the evaluator ready-to-use accounts. It fails closed: the app refuses to start in demo mode unless `ACCESSFLOW_ENV=local`, secure cookies are off, both database URLs use the Compose `postgres` host (with no query parameters) and `JWT_SECRET` is unset (generated). In demo mode the initial Super Admin and one documented account per main role (InfoSec, HR Manager, Auditor, manager, tool owner, employee) get distinct permanent passwords from Compose environment defaults, with no forced change and no bootstrap file; every other seeded employee gets an unusable random password. Demo mode is shown on every page and in the startup log. With demo mode off, bootstrap is unchanged (one-time password in the file). This replaces the shared seed password above.

- *Note — [APPROVED 2026-10-30] (M18):* **new passwords** (activation and password change) must also not be in an embedded common-password list (SecLists, filtered to 12–128 characters, compared ignoring case) and must not equal the email or contain its name part (4+ characters); the demo passwords are not checked against the list. **Login rate limiting:** at most 10 attempts per minute per client address (*raised to 60 by the next note*), shared by `POST /auth/login` and `POST /auth/activate`, in addition to the account lockout; `429 too_many_attempts` with `Retry-After`. The address is the connection's own (IPv6 grouped by /64); `X-Forwarded-For` is ignored because the app is exposed directly, so a reverse proxy would need a trusted-proxy setting. At most 10,000 addresses are tracked; when full of live entries, new addresses are refused (fail closed). **Mock email key:** the outbox keeps an activation token AES-256-GCM-encrypted (bound to its email and employee) with a 32-byte key in `/var/lib/accessflow/mock-email.key` (`MOCK_EMAIL_KEY_FILE`, mode 0600, `appdata` volume), never in PostgreSQL, so a database dump reveals no link and links survive restarts. The ciphertext is cleared when the link is used, replaced or expires (sweeps). The key is created only when the file is absent and no encrypted link exists; a missing key while links exist, or an unreadable or damaged file, is never replaced: links can then be neither shown (`410`) nor issued (`503 mock_email_unavailable`) until `docker compose exec app /accessflow reset-mail-key` (refused while the key is valid) revokes every open link, clears the ciphertexts, audits `MOCK_EMAIL_KEY_RESET` and writes a new key. The runtime user may now delete `password_credentials` rows (a reset removes the password) and clear `email_outbox.sealed_token`; activation tokens, emails, employees and audit events stay append-only. A password and a mock ciphertext are credentials, not business records. **MFA (planned, not implemented):** two-step verification with an authenticator app, mandatory for privileged roles first, is a future change; the UI shows it only as "Planlanıyor".
- *Note — [APPROVED 2026-10-30] (M18, rate limit raised):* the per-address limit is **60 attempts per minute** (was 10), still shared by login and activation, configurable with `LOGIN_RATE_LIMIT` (a positive whole number). Reason: in the local Docker Desktop setup every browser reaches the app through the same address, so one budget covers everyone switching between demo accounts and 10 was too tight for a demo. The per-account lockout (5 wrong passwords, 15 minutes) is unchanged and remains the protection for a single account. 60 is a local-demo default; any other environment must review it together with a trusted-proxy setting.

**Logout**
- Clearing the cookie alone would not revoke a token copied before logout. Therefore logout also increments `token_version`, which immediately invalidates every token for that user, including copied ones.
- *Accepted trade-off:* logging out in one browser signs the user out on all browsers and devices. Single-token revocation (a token-ID denylist) is not provided.

### A5. Identity records and history — [APPROVED 2026-10-28]
- Employees and users are never physically deleted in normal workflows. Account status is `ACTIVE`, `SUSPENDED` or `TERMINATED`.
- Employee identifiers are immutable. Requests, approvals, grants, revocations, role changes and audit events are retained and remain available to point-in-time queries.
- Rehire reactivates the same employee record (same identifier, history kept) with a new temporary password (*an activation link since M18, note below*). No previous access is restored; new access requires the normal request and approval workflow (D7).
  - *Refinement — [APPROVED 2026-10-29] (M10 merge review):* a rehire is also refused (`owns_tools`) while the person still owns any tool (termination does not change ownership). A Super Admin reassigns those tools to an active owner first, so a rehired person never silently regains Tool Owner approvals.
  - *Refinement — [APPROVED 2026-10-29] (M10 review):* a rehire is refused (`has_direct_reports`) while anyone not terminated still reports to the person. HR transfers those reports to another manager first, so a rehired manager never silently regains their former reports' approvals, team view or revoke rights.
  - *Refinement — [APPROVED 2026-10-29] (M10 plan):* HR Managers and Super Admins rehire a `TERMINATED` employee with a reason and a new manager, department and job title, under the same rules as other person-level actions (D7); the temporary password is shown once (*superseded by the M18 note below*).
  - *Note — [APPROVED 2026-10-30] (M18):* a rehire issues an activation link (F4) instead of a temporary password; nothing is shown to the person who rehires.
- Reactivation after suspension follows D7: expired, revoked or retired grants are never restored; others are resumed only through the normal workflow.

### A6. Background processing — [APPROVED 2026-10-28]
Two mechanisms inside the Go application; no Redis, RabbitMQ, generic job queue, separate cron container or other infrastructure.

**1. Operation runner** (API grant, revoke, suspend, resume)
- The `operations` table is the queue. Due operations are claimed with row locking and `SKIP LOCKED`, under a bounded lease so a crashed worker's operations are retried.
- **Priority:** termination- and suspension-related removals first, then other revocations and suspensions, then grants and resumes; oldest first within a priority.
  - *Clarification — [APPROVED 2026-10-29] (M9):* "first" means the removals of a terminated employee (priority 0). Every other removal, including a removal made during a suspension or at reactivation (expired or retired access), and every `SUSPEND` is priority 1; grants and `RESUME` are priority 2. The Manual and Vendor task queue is listed in the same order.
- The Mock Tool Service HTTP call (5-second timeout) runs outside any database transaction and carries the operation ID for idempotency.
- After a successful call, the actual tool state is verified by status lookup. State transitions and audit events are recorded in one transaction.
- O3 retries: 5 attempts with backoff 30 s, 1 min, 2 min, 4 min; then `FAILED`, retryable manually from Exceptions.
- Manual and Vendor fulfillment remain human-managed tasks, not runner work.
- *Clarification — [APPROVED 2026-10-31] (architecture rationale): why PostgreSQL is also the operation queue.*
  - Access state, operations and audit events live in one database, so an approval, its grant, its `GRANT` operation and their audit events commit in one transaction: there is no window where the grant exists without its operation or the reverse, and no outbox/relay to build.
  - Workers claim with `FOR UPDATE … SKIP LOCKED` in a short transaction and take a 30-second lease with a fresh token; the tool call runs outside any transaction; the result is recorded only if the lease token still matches, so a stale or crashed worker cannot overwrite a newer outcome. Partial unique indexes allow one `GRANT` per grant and one open removal or suspension at a time; the grant and operation state machines decide what a late or repeated result may still change; the operation ID makes the tool call idempotent; success needs the read-back (A1 clarification).
  - At about 400 employees, 60 tools and thousands of grants, the queue sees at most hundreds of operations a day (bursts at offboarding or a review). A broker would add a service to run, monitor and secure, and a second source of truth to reconcile, for no capability the case needs.
  - **A broker becomes justified** when: operations reach many thousands per minute or need many independent consumers; several services or teams consume access events (SIEM, HR feed, data platform); per-provider throughput isolation or delivery to systems outside this database is required; or the operations table's polling becomes a measurable load. Even then the transactional write would stay, with an outbox relaying committed events to the broker.

**2. Periodic sweeps** (about once a minute, guarded by a PostgreSQL advisory lock)
- Temporary access expiry (D6), due scheduled terminations (D7), quarterly campaign creation and the day-7, day-14 and day-21 steps (D8).
- Each sweep is idempotent and catches up after downtime.
- Company timezone from a setting, default **Europe/Istanbul**.

### A7. Frontend — [APPROVED 2026-10-28; UI component library deferred]
- **Vite + React + TypeScript** (strict mode), **React Router** for navigation, **TanStack Query** for server state. No Redux or other global store.
- **UI component library: deferred.** The interface is designed separately with Claude Artifacts and delivered under `design/`. The component approach is chosen after reviewing those files: if the design uses Tailwind, shadcn/ui or another approach, its compatibility is evaluated and the existing design reused where practical. Only one UI library; no competing libraries or unnecessary rewrites of design components.
- The design is the visual and UX reference; `CASE.md` and `DECISIONS.md` remain authoritative for functional requirements.
- **Localization — [APPROVED 2026-10-28]:** the frontend supports **English and Turkish**. Every screen, form, status label, validation message, navigation item, modal and notification is available in both languages. It is built in U1 and in every later UI slice, not in backend milestones.
  - *Change — [APPROVED 2026-10-29, user instruction at the start of U1]:* the UI is **Turkish-only**, matching the approved design in `design/`. The English translation is dropped; every user-facing string is Turkish. API error codes stay language-neutral, so English can be added later without backend changes.
  - *UI component library — [DECIDED 2026-10-29, U1]:* no component library. The design in `design/` is plain CSS with custom properties, so its stylesheet and component markup are reused directly; no Tailwind or shadcn/ui, and no new UI dependency.
  - *API consequence — [APPROVED 2026-10-28, implemented in M2]:* API errors return a stable machine-readable code, `{"error": "<code>", "field"?: "<fieldName>"}` (for example `{"error":"invalid_credentials"}`; `"field"` only for validation errors), never display text; the frontend maps codes to both languages.
- Library-independent conventions:
  - One small `fetch` wrapper: adds `X-CSRF-Token`; on `401` goes to login (or forced password change); shows clear `403` and validation errors.
  - Hand-written TypeScript types matching the API; no OpenAPI code generation.
  - One shared status-to-badge mapping so every screen shows item states identically; self-reported results always labelled.
  - Timestamps shown in the company timezone, with the timezone named.
  - The UI hides actions only for convenience; the server enforces every permission (A4).
  - Built assets embedded in the Go binary; unknown non-API paths return `index.html`.
    - *Refinement — [APPROVED 2026-10-28]:* missing files under `/assets/` (the build output) return `404` instead of `index.html`, so a stale cached bundle fails clearly rather than as HTML. Client routes such as `/dashboard` still return `index.html`; unknown `/api/*` paths return JSON `404`.
  - Exact versions pinned in `package-lock.json`; Docker builds use `npm ci`.
  - *API documentation — [APPROVED 2026-10-30, user instruction]:* a read-only **Swagger UI** at `/swagger/`, served from the embedded build like the app. The OpenAPI 3.1 document (`web/public/swagger/openapi.json`) is hand-written, still with no code generation; a router test fails when it lists a route that does not exist or misses one. Swagger UI comes from one pinned npm package, **`swagger-ui-dist`** (its prebuilt files are copied into the build; it pulls in `@scarf/scarf`, whose install analytics are turned off in `package.json`). "Try it out" and the external validator are disabled, so the page never calls the API or sends the document elsewhere.
    - *Note — [APPROVED 2026-10-31] (final QA, R-7):* `/swagger/` (the page and `openapi.json`) is served only in the local demo setup (`ACCESSFLOW_DEMO`); otherwise it answers `404`. The OpenAPI document stays in the repository.

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
- Unique partial indexes: one open request per (person, tool, level) — *refined [APPROVED 2026-10-28]: one open request item per (person, tool), whatever the level, so two levels of one tool cannot be pending and approved together (D4); changing an existing level stays a `LEVEL_CHANGE`*; one open grant per (person, tool, level); one grant per approved request item; one open removal operation per grant; one open level change per (person, tool); one campaign per quarter; one action per campaign day step.
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
- *Note — 2026-10-29:* closely related milestones are planned, approved and delivered as one group on one branch and one pull request (for example M13 + M14). Development uses targeted tests; the full suite runs in GitHub CI on the pull request's final head, which must be green before merge.
