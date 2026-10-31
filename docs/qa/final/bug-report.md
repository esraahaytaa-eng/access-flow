# Bug report — final QA (2026-10-30, final fix round 2026-10-31)

Sources: manual QA part A (`BUG-A-*`), manual QA part B (`BUG-B-*`), the security audit (`F-*`), the engineering audit (`ENG-*`), the performance audit (`PERF-*`, see `performance-audit.md`), the independent review of the final diff (`RV-*`), and the test runs (`TF-*`).
Every fix is on branch `qa/final-audit`. Each has a regression test that was seen failing before the fix, except where the table says otherwise. Every finding listed here is fixed or explicitly accepted; none is open.

Severity: High = security or data integrity broken in a reachable path; Medium = wrong behaviour a user relies on, or a reliability problem; Low = misleading or cosmetic, with a workaround.

## Functional bugs from manual QA

| ID | Sev | Summary | Root cause | Fix | Regression test | Status |
|---|---|---|---|---|---|---|
| BUG-B-01 | Medium | A leaver's unfinished removals disappear from the HR employee page ("Açık erişimler (0)"), so the "Kaldırılması tamamlanmamış erişimler var" warning never shows | `GET /employees/{id}/grants` used the transfer filter (open statuses only), which hides `REVOCATION_REQUESTED`/`REVOCATION_FAILED` | The endpoint returns every grant not yet `REVOKED`/`CANCELLED` (`internal/access/grants.go`). The transfer modal and direct revoke still offer only open grants; the terminate count excludes grants already being removed | `TestEmployeeGrants`, `TestTransferKeeps` (updated); admin.test "offers direct removal only for grants not already being removed" | Fixed |
| BUG-B-02 | Low | Open review findings are counted under the "Onay" filter in İstisnalar | The FLOW group holds unroutable steps and review findings, but its label named only approvals | Label is now "Onay ve denetim (N)" (`Exceptions.tsx`) | reviews.test "lets InfoSec dismiss an orphan…" asserts the label. *Not watched failing first: the label changed before the assertion was added* | Fixed |
| BUG-B-03 | Low | `/audit` opened by URL renders an empty audit page for an employee with no reports (the API correctly returns 403) | The `/audit` route had no guard | `AuditGuard` (`App.tsx`) uses the same rule as the navigation (`nav.ts` `audit`). It waits for the directory before deciding, so a manager never sees a "no access" flash | App.test "opens the audit page only to the roles that may use it, managers included" | Fixed |
| BUG-B-04 | Low (PARTIAL) | While a termination is scheduled, the page offers only "Planlı çıkışı iptal et", not an immediate termination. An overdue schedule of the last Super Admin is not explained | Header showed one button or the other; the callout always said "will run at the set time" | Both buttons are shown. When the time has passed, the callout explains the wait: last active Super Admin → waits for a successor (D2); otherwise "çıkış birkaç dakika içinde otomatik işlenecek" | employees.test "offers immediate termination while one is scheduled…" | Fixed |
| BUG-B-05 | Medium (docs) | README recovery section still described the superseded "HR nominates, InfoSec authorizes" recovery | Not updated with the D2 note of 2026-10-30 | Rewritten: HR nominates, the nominee accepts a 24-hour link signed in as themself; `recover-admin` fallback example updated | — (docs) | Fixed |
| BUG-B-06 | Low (docs) | `docs/api.md` documented `422 employee_not_active` for nominations; the server returns 400 | Docs drift | Codes corrected; succession wording aligned with D2 | — (docs; `openapi.json` was already right) | Fixed |
| BUG-B-07 | Low | Cancelling a nomination (nominee or proposer suspended or terminated) left the link's encrypted copy in the outbox until the next sweep (≤ 1 min) | `AfterLeaving` cancelled nominations but did not clear `email_outbox.sealed_token`; the reveal already refused cancelled links, so nothing could be revealed | Cleared in the same transaction (`internal/people/succession.go`) | `TestCancelledNominationClearsItsLinkAtOnce` | Fixed |
| BUG-B-08 | Medium | The quarterly evidence package omits findings still open from an earlier campaign; after a new campaign starts they are invisible in evidence | `LatestQuarter` selected only the latest campaign's items, but open findings are not re-snapshotted (by design, REV-24) | Open findings of earlier campaigns are listed with `carriedOver: true` and counted in `carriedFindings` (not in `total`); UI tile "Önceki denetimlerden açık bulgu"; CSV note "Önceki denetimden devreden bulgu" | `TestQuarterEvidenceListsCarriedOverFindings`; audit.test "shows open findings carried over…" | Fixed |
| BUG-A-01 | Low | İstisnalar shows the raw Go error of a failed tool call (`dial tcp 172.18.0.3…`) as the headline | `lastError` was rendered as is | Automated failures (`attempts > 0`) show "Araç çağrısı başarısız oldu · N deneme"; the raw error stays under a collapsed "Teknik ayrıntı" for the tool admin. A person's own failure reason (Manual/Vendor task) is still shown as written | admin.test "explains a failed tool call in Turkish…" | Fixed |
| BUG-A-02 | Low | A person bypassed by a direct grant in two ways (e.g. InfoSec and the beneficiary's manager) got two DIRECT_GRANT notifications | Separate inserts per recipient group | One `INSERT … SELECT` over the union of recipients (`internal/requests/direct.go`) | `TestDirectGrantNotifications` (extended) | Fixed |
| BUG-A-03 | Low | An unroutable step cannot be resolved when the only Super Admin is already a party to the request (override and reassign both refuse with separation of duties); the drawer offers no action | Separation of duties (F2, D1) correctly blocks the only possible resolver | **Fixed 2026-10-31** (F2 note): Exceptions returns `party` and the UI explains that another InfoSec member or Super Admin must act; separation of duties unchanged | `TestUnroutableStepSaysWhenTheCallerIsAParty`, `Exceptions` UI test | Fixed |

## Security findings (see `security-audit.md`)

| ID | Sev | Summary | Status |
|---|---|---|---|
| F-1 | High | HR can reach a succession by suspending Super Admins until at most one is active, then appoint a Super Admin with an accomplice | **Fixed 2026-10-31** (approved D2 note): suspended Super Admins count (`standingSuperAdmins`), and an independent InfoSec member or Super Admin must authorize before any link is sent (R-2). Regression: `TestSuspendingSuperAdminsOpensNoHRSuccession`, `TestHRSuccessionNeedsAnIndependentAuthorizer`; blocked live on an isolated stack |
| F-2 | Medium | An 11 KB sparse workbook made the preview allocate ~1.3 GiB | Fixed: `checkSize` streams rows and refuses data beyond column Z or too many rows before loading the sheet (`too_many_columns`). `TestSparseWideWorkbookIsRefusedCheaply` (< 64 MiB) |
| F-3 | Low | A password typed into the email field was stored in the LOGIN_FAILED audit event | Fixed: invalid input is recorded only as `reason: invalid_email`, never the text. `TestFailedLoginAuditKeepsNoOverlongInput` and table cases |
| F-4 | Low | No anti-framing / nosniff / referrer headers | Fixed: root middleware sets `X-Frame-Options: DENY`, `Content-Security-Policy: frame-ancestors 'none'`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`; asserted in `TestRouter` |

## Engineering findings (see `engineering-audit.md`)

| ID | Sev | Summary | Status |
|---|---|---|---|
| ENG-001 | Medium | Retrying a failed orphan-account removal returned 500 | Fixed; `TestFailedOrphanRemovalCanBeRetried` |
| ENG-002 | Medium | New Request UI offered only direct reports, the server allows the whole chain (D3 note) | Fixed; NewRequest.test "anyone below them in the chain" |
| ENG-003 | Low | Runner spun in a tight loop while a retired grant's employee was busy | Fixed; `TestRunnerPausesWhileARetiredGrantsEmployeeIsBusy` |
| ENG-005 | Low | Override and Reassign did not re-check the actor is still active under the lock (D7) | Fixed; `TestOverrideAndReassignSeeASuspensionCommittedWhileWaiting` |
| ENG-008 | Low | Password length counted UTF-16 units in the UI, code points on the server | Fixed; login.test |
| ENG-006 | Low | Change requests on behalf follow "direct manager only", new requests follow the D3 chain rule | **Fixed 2026-10-31** (D3 note): change requests use `checkOnBehalf`; `TestChangeOnBehalfFollowsTheManagementChain` |
| others | Low | ENG-004, 007, 009–013 | See `engineering-audit.md` |

## Findings from the independent review of the final diff

| ID | Sev | Summary | Status |
|---|---|---|---|
| RV-1 | High | The F-1 fix changed approved D2 wording ("at most one Super Admin is active") without approval, and HR could still suspend every Super Admin and then nominate | Historical: the first F-1 change was reverted on 2026-10-30; the approved fix landed on 2026-10-31 (F-1) |
| RV-2 | Medium | After BUG-B-01, Doğrudan kaldırma offered "Kaldır" on a `REVOCATION_FAILED` grant; the server treats that as a no-op, yet the UI said "Kaldırma başlatıldı" | Fixed in `DirectGrant.tsx`; admin.test "offers direct removal only for grants not already being removed" (seen failing) |
| RV-3 | Low | `TestEmployeeGrants` lost its `retainedOnTransfer == nil` check | Restored |
| RV-4 | Low (plausible) | The set-based day-21 close no longer takes the removal lock, so a revoke committing at the same moment can still get a finding | Accepted: the finding closes itself as "already removed" when the removal completes (QA REV-20); the old lock inverted the lock order. Documented |

## Test reliability

| ID | Summary | Root cause | Status |
|---|---|---|---|
| TF-1 | `TestLoginIsRateLimitedPerAddress` failed in a full run ("61st attempt: 200") | Fixed one-minute window on the real clock; 60 hashed failed logins took longer than a minute on a loaded host | Fixed: the HTTP test uses `LoginRateLimit: 5`; the window logic stays unit-tested with explicit times |
| TF-2 | `onboarding`, `TestRunnerRecordsUnderTheEmployeeLock`, `TestToolReadVerifiesReportedAccess` failed once each in full runs | All runs overlapped the performance agent driving the host to 130–880% CPU; fixed 5 s waits and tool-call timeouts | Not reproduced in isolation (15× and 5× with `-race`) or in the final full `-race` run on a quieter host |

## Final fix round (2026-10-31)

| ID | Severity | Summary | Status |
|---|---|---|---|
| F-1 | High | HR suspends Super Admins to open a succession | Fixed (see above) |
| R-1 | High | A Super Admin reveals a privileged account's activation link they issued | Fixed: `403 independent_super_admin_required` for one's own account or a privileged link one issued; the other Super Admins get `PRIVILEGED_ACTIVATION_LINK_VIEWED` (F4 note). `TestPrivilegedActivationLinkNeedsAnIndependentSuperAdmin` |
| R-2 | High | HR plus the nominee create a Super Admin with nobody else involved | Fixed: independent authorization before the link (migration 00019), Super Admins and InfoSec notified at nomination and acceptance. `TestHRSuccessionNeedsAnIndependentAuthorizer`, `TestConcurrentSuccessionAuthorizations` |
| R-3 | Medium | HR (or a Super Admin) moves someone under themself, then their submission approves the Manager step | Fixed: no `APPROVED_BY_SUBMISSION` for HR Managers and Super Admins (D3 note). `TestHRCannotApproveBySubmissionAfterMovingSomeoneUnderThemself` |
| R-7 | Info | Swagger public | Fixed: served only in demo mode, `404` otherwise (A7 note). `TestSwaggerIsServedOnlyInDemoMode` |
| PERF-004 | Medium | Silent 2,000/200 caps | Fixed: server-side paging (100 a page, exact totals, deterministic order, role scope) for reviews, findings, tasks and team. `TestReviewItemsArePagedCompletely`, `TestFulfillmentTasksArePagedCompletely`, `TestTeamIsPagedCompletely`; verified at 12,005 review items |
| FR-1 | Low | Found by the full suite: `invalid_page`, `invalid_tab`, `invalid_view` had no Turkish text | Fixed; `TestEveryErrorCodeIsTranslated` passes |
| FR-2 | Low | Misleading UI text: the recovery banner and the Mailbox notice described the old succession and reveal rules | Fixed, with Vitest assertions |
| FR-3 | Low | A transaction PostgreSQL aborted as a deadlock (documented lock-order races) answered `500` | Fixed 2026-10-31: `platform.InTx` retries a deadlock or serialization failure, at most three attempts; closures reset their outputs per attempt. `TestSubmissionSurvivesADeadlockWithAToolOwnerChange` (seen failing with `40P01` → 500), `TestInTxRetriesOnlyDeadlocksAndOnlyThreeTimes` Cause: Two actions take the same two locks in opposite orders (e.g. a submission and a tool change making the submitter the owner). |
| FR-4 | Low | A manager or bypassed assignee who is also InfoSec got two notifications for one override | Fixed 2026-10-31: `notifyOverride` excludes them. `TestOverrideNotifiesEachPersonOnce` (seen failing: 2) Cause: The InfoSec role notification did not exclude people notified in person. |
| FR-5 | Medium | Exceptions lists capped at 200 (500 orphans) gave no total, so counts understated open records; an employee's held grant could fall behind 200 newer history rows | Fixed 2026-10-31: exact `totals` per list and a notice when rows are not shown; grant lists put grants not yet ended first. `TestExceptionsCountEveryCappedList`, `TestHeldGrantsComeBeforeAnyHistory`, admin.test "counts every open record of a capped list" |
| AUD-D | Medium (workflow correctness) | An Auditor account could also own tools, manage people, sit in admin groups or hold pending approvals and reviews, which it cannot act on, so that work waited for InfoSec | Fixed 2026-10-31 (O7 note): `AUDITOR` refused while a duty is open (`409 auditor_has_duties`, `field` names it); duties refused for an Auditor (`409 employee_is_auditor`). `TestAuditorRoleRefusedWhileDutiesAreOpen`, `TestAuditorGetsNoOperationalDuty` |
| AUD-R | High (user decision, P0) | The Auditor, meant as an external read-only auditor, could read the directory, point-in-time access, evidence reports, Exceptions, reviews, imports and request e-mails, and kept any other role's permissions; the audit list stopped at 1000 events and its CSV held only the loaded rows | Fixed 2026-10-31 (O7 note): default deny for AUDITOR in the session middleware (three audit-event routes), precedence over other roles, paged events with totals, tool filter, streamed full CSV export, events-only UI. `TestAuditorReachesOnlyTheAuditEvents`, `TestAuditorPagesFiltersAndExportsEveryEvent`; live AUD-R-01..08 *(Amended later on 2026-10-31: the Auditor also reads point-in-time access and approval evidence (O7 note "the Auditor also reads point-in-time access and approval evidence", 2026-10-31; [current rules](../../../DECISIONS.md#current-rules-at-a-glance)). The evidence reports stay refused; the route-walk test is now `TestAuditorReachesOnlyTheAuditEvidence`.)* |

