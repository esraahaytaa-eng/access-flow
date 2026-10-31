# Test matrix and requirements traceability — final QA (2026-10-30, updated 2026-10-31)

How each CASE.md requirement is covered:
- **Automated:** Go tests run against real PostgreSQL (A10) and frontend tests (Vitest + Testing Library).
- **Manual:** browser and API cases on isolated stacks; results are in `execution-results.md`.
- **Security:** abuse cases; see `security-audit.md`.
- **Performance:** measurements at scale; see `performance-audit.md`.

Go test names below are real functions, mostly in `internal/requests/*_test.go`, which drive the HTTP API end to end. Manual IDs refer to `execution-results.md`.

## 1. Traceability: CASE.md → decisions → tests

| CASE.md requirement | Decisions | Automated tests (examples) | Manual / security cases | Result |
|---|---|---|---|---|
| **§1 Requests:** someone asks for access, says why and for how long | D3, D4, D6, A9 | `TestSubmitJiraViewerNeedsOnlyManager`, `TestInvalidSubmissions`, `TestTemporaryDurationIsStored`, `TestRepeatedSubmissionIDReturnsTheOriginal`, `TestOpenItemPerToolIsUnique`, `TestConcurrentSubmissionsForOneTool`, `TestOnBehalfNeedsAManagerHROrSuperAdmin`, `TestWithdrawBeforeDecision`; NewRequest.test, `TestChangeOnBehalfFollowsTheManagementChain` (ENG-006), `TestHRCannotApproveBySubmissionAfterMovingSomeoneUnderThemself` (R-3), `TestSubmissionSurvivesADeadlockWithAToolOwnerChange` | REQ-01..22, SEC-042/043 | PASS (REQ-22 not run: needs 20 tools) |
| **§2 Approvals:** the right people, depending on tool and sensitivity | F2, D1, D9, O5 | `TestAWSAdminNeedsThreeDifferentPeople`, `TestManagerWhoIsToolOwnerGoesToInfoSec`, `TestBeneficiaryCannotApprove`, `TestRejectNeedsAReasonAndIsFinal`, `TestConcurrentApprovals`, `TestStricterWinsAddsAStepBeforeProvisioning`, `TestReassignPermissionMatrix`, `TestOverrideRecordsTheSuperAdmin`, `TestOverrideAndReassignSeeASuspensionCommittedWhileWaiting`, `TestUnroutableStepListedAndResolvedByOverride`, `TestUnroutableStepSaysWhenTheCallerIsAParty` (BUG-A-03), `TestOverrideNotifiesEachPersonOnce` | APR-01..16, SEC-090..101 | PASS. APR-12 (BUG-A-03) failed on 2026-10-30 and is fixed and verified (2026-10-31); APR-13 was not executable in the browser and is covered by a Go test |
| **§3a Granting:** access is added in the tool (API, user screen, vendor ticket) | Fulfillment methods, O1, O3, O4 | `TestFinalApprovalCreatesGrantAndOperation`, `TestManualTaskEndToEnd`, `TestVendorTaskNeedsATicket`, `TestTaskFailureAndRetry`, `TestRetryPermissionMatrix`, `TestDirectGrantAPI`, `TestDirectGrantNotifications`, runner tests in `internal/access` | PRV-01..18, SEC-175/176 | PASS (PRV-08, PRV-12 failed, now fixed: BUG-A-01, BUG-A-02) |
| **§3b Removing on expiry** | D6, A6 | `TestExpirySweepRemovesTemporaryAccess`, `TestExpirySweepIsIdempotent`, `TestExpiryAndExtensionSerialize`, `TestItemApprovedAfterItsExpiry`, `TestExtensionRules` | EXP-01..09 | PASS (EXP-09 not run: real time only, covered by date unit tests) |
| **§3c Removing on team change** | D5 | `TestTransferRevokesByDefault`, `TestTransferKeeps`, `TestTransferKeepNeedsJustification`, `TestTransferManagerStepMovesToNewManager`, `TestConcurrentTransfersCannotCreateACycle`, `TestEmployeeGrants` | LCY-09..13, SEC-200 | PASS (R-3 fixed 2026-10-31: no approval by submission for HR Managers and Super Admins) |
| **§3d Removing on leaving** (incl. "14 leavers still had accounts") | D7, D10, D12 | `TestTerminateRemovesEverything`, `TestTerminationRemovalsRunFirst`, `TestScheduledTerminationRuns`, `TestImmediateTerminationOverridesSchedule`, `TestRehireRestoresNothing`, `TestRehireKeepsLeaverRemovalsVisible`, `TestMockImportMatchesByEmailAndIsIdempotent`, `TestCSVImportForManualTools`, `TestOrphanAccountsAreRevokedOrDismissed`, `TestFailedOrphanRemovalCanBeRetried` | LCY-14..26, ORP-01..12, AUD-08 (leavers report) | PASS; LCY-15 (BUG-B-01) and LCY-26 (BUG-B-04) fixed |
| Suspension and reactivation | D7 | `TestSuspendAPI`, `TestSuspendManualAndVendor`, `TestReactivateAPI`, `TestSuspendConcurrently`, `TestSuspensionGapIsVisibleAndActionable` | LCY-01..08, AUTH-23, AUTH-32 | PASS |
| **§4 Quarterly reviews:** owners confirm who keeps access; owners who don't reply are chased | D8 (+ note 2026-10-30) | `TestCampaignSnapshotAndRouting`, `TestReviewDecisions`, `TestBulkKeepSkipsItemsDecidedIndividually`, `TestDayStepsRunOnceAndUseTheSnapshot` (day 7 reminder, day 14 escalation, day 21 finding), `TestOpenFindingIsCarriedNotReAdded`, `TestTerminatedEmployeeAccessCannotBeKept`, `TestQuarterlyCampaignStartsOncePerQuarter`, `TestGrantBeingRemovedIsNeitherDecidedNorAFinding` | REV-01..26, SEC-170..173 | PASS; REV-17 (BUG-B-02) and REV-26 (BUG-B-08) fixed |
| **§5 Proof:** who had access to what on any date, and who approved it | O1, D11, A9 | `TestPointInTimeAtActivationAndRemoval`, `TestPointInTimeEvidenceLabels`, `TestApprovalEvidenceInScope`, `TestAuditExplorerScope`, `TestAuditEventFilters`, `TestPrivilegedRolesAtAPastDate`, `TestEvidenceReports`, `TestQuarterEvidenceListsCarriedOverFindings`, audit append-only tests in `internal/audit`; audit.test | AUD-01..09, RBAC-09, RBAC-11 | PASS; RBAC-11 (BUG-B-03) fixed |
| Super Admin promotion and succession | D2 (+ note 2026-10-30) | `TestSuperAdminPromotionIsTwoPerson`, `TestHRSuccessionOfTheLastSuperAdmin`, `TestRoleLinkExpiresAndIsReplacedOnReissue`, `TestConcurrentRoleAcceptance`, `TestConcurrentTerminationsKeepASuperAdmin`, `TestSuccessionOnlyWhileTheLastSuperAdminLeaves`, `TestCancelledNominationClearsItsLinkAtOnce`, `TestSuspendingSuperAdminsOpensNoHRSuccession` (F-1), `TestHRSuccessionNeedsAnIndependentAuthorizer`, `TestConcurrentSuccessionAuthorizations` (R-2) | SUC-01..25, SEC-140..150, SEC-180..183 | PASS (F-1/R-2 fixed 2026-10-31; SEC-180..183 and the R-2 exploits blocked live) |
| Sign-in, activation, passwords, sessions | F4, A4, M18 | `TestActivationSetsThePasswordWithoutSigningIn`, `TestOnlyASuperAdminRevealsActivationLinks`, `TestExpiredReplacedAndInactiveLinksFail`, `TestValidateNewPassword`, `TestLoginIsRateLimitedPerAddress`, `TestRegisterFailure`, `TestFailedLoginAuditKeepsNoOverlongInput`, `TestRouter` (security headers), `TestPrivilegedActivationLinkNeedsAnIndependentSuperAdmin` (R-1) | AUTH-01..33, SEC-001..009, SEC-120..136 | PASS (AUTH-33 not run: needs 12 h); SEC-009 (F-3) fixed; R-1 fixed 2026-10-31 (SEC-160 blocked) |
| Onboarding workbook and company catalog | D10, F3 (M17) | `TestValidWorkbook`, `TestProblems`, `TestWarnings`, `TestReimportCountsExisting`, `TestSparseWideWorkbookIsRefusedCheaply` | EMP-01..16, SEC-210 | PASS; SEC-210 (F-2) fixed |
| Roles and screens per role | O7, A4 | `TestReassignPermissionMatrix`, `TestRevokePermissionMatrix`, `TestRetryPermissionMatrix`, `TestSuspendPermissions`, `TestTaskPermissions`; App.test guards | UI-01..13, RBAC-01..19, security GET matrix 29×7 | PASS |
| **Stack:** Go + React, any DB, starts with one command | A1, A2 | `TestHashedAssetsAreCachedAndThePageIsNot`, `TestRouter`; CI suite | OPS-01..07; clean `docker compose up` on a fresh project (`release-readiness.md`) | PASS |
| **Deliverable 1:** code with a short README | — | — | OPS-03, OPS-04, OPS-09 (BUG-B-05, fixed) | PASS after fix |
| **Deliverable 2:** one page on the weekly run, rules and assumptions | — | — | OPS-08 (`docs/operating-summary.md` matches behaviour) | PASS |
| **Deliverable 3:** first 30 days | — | — | OPS-08 (`docs/rollout-30-days.md`) | PASS |
| Deliverable 1: 5-minute recording | — | — | made by the user, after release | NOT RUN (by instruction) |
| Scale: 400 people, 60 tools | — | — | `performance-audit.md` | PASS: paged lists (PERF-004) verified at 12,005 review items; other Exceptions lists report exact totals (2026-10-31, `performance-audit.md` §9) |

## 2. Test inventory per area

| Area | Automated (Go) | Automated (UI) | Manual cases |
|---|---|---|---|
| Requests and on-behalf | `submit_test.go`, `actions_test.go` | NewRequest.test, requests tests | REQ-01..22 |
| Approvals | `decide_test.go`, `actions_test.go`, `rules_test.go` (table-driven routing) | approvals tests | APR-01..16 |
| Provisioning and tasks | `fulfillment_test.go`, `tasks_test.go`, `exceptions_test.go`, `internal/access/*_test.go` (runner, leases, lock order) | admin.test (Exceptions), tasks tests | PRV-01..18 |
| Expiry and changes | `expiry_test.go`, `change_test.go`, `levelchange_test.go` | MyAccess tests | EXP-01..09 |
| Lifecycle | `lifecycle_test.go`, `transfer_test.go`, `schedule_test.go`, `rehire_test.go` | employees.test | LCY-01..26 |
| Succession | `succession_test.go`, `internal/people/*_test.go` | employees.test, RoleAccept tests | SUC-01..25 |
| Reviews | `reviews_test.go`, `internal/reviews/*_test.go` | reviews.test | REV-01..26 |
| Orphans and imports | `imports_test.go`, `internal/onboarding/*_test.go` | Settings tests | ORP-01..12, EMP-07..16 |
| Audit and evidence | `audit_test.go`, `internal/explorer`, `internal/audit` | audit.test | AUD-01..09 |
| Auth | `internal/auth/*_test.go` | login.test | AUTH-01..33 |
| Permissions | permission matrix tests above | App.test | RBAC-01..19, SEC-* |
| Platform | `cmd/accessflow` (router, headers, OpenAPI JSON), `internal/webui`, `internal/platform` | — | OPS-01..11 |

## 3. Regression tests added by this audit

| Test | Guards |
|---|---|
| `TestFailedOrphanRemovalCanBeRetried` | ENG-001 |
| `TestRunnerPausesWhileARetiredGrantsEmployeeIsBusy` | ENG-003 |
| `TestOverrideAndReassignSeeASuspensionCommittedWhileWaiting` | ENG-005 |
| `TestSparseWideWorkbookIsRefusedCheaply`, workbook case "a cell far to the right" | F-2 |
| `TestFailedLoginAuditKeepsNoOverlongInput`, login-failure table cases | F-3 |
| `TestRouter` header assertions | F-4 |
| `TestSignInIsNotBlockedByRowsReferencingTheEmployee` | PERF-001 (sign-in lock) |
| `TestHashedAssetsAreCachedAndThePageIsNot` | PERF-009 |
| `TestEmployeeGrants` (updated) | BUG-B-01 |
| `TestQuarterEvidenceListsCarriedOverFindings`; audit.test "carried over" | BUG-B-08 |
| `TestCancelledNominationClearsItsLinkAtOnce` | BUG-B-07 |
| `TestDirectGrantNotifications` (extended) | BUG-A-02 |
| App.test "opens the audit page only to the roles that may use it" | BUG-B-03 |
| admin.test "explains a failed tool call in Turkish…" | BUG-A-01 |
| admin.test "offers direct removal only for grants not already being removed" | RV-2 |
| employees.test "offers immediate termination while one is scheduled…" | BUG-B-04 |
| NewRequest.test "anyone below them in the chain"; login.test code-point length | ENG-002, ENG-008 |
| audit.test "pages a large point-in-time list by 40 rows" | PERF-006 |
| `TestSuspendingSuperAdminsOpensNoHRSuccession`, `TestCheckSuccessionAuthorizer` | F-1 |
| `TestHRSuccessionNeedsAnIndependentAuthorizer`, `TestConcurrentSuccessionAuthorizations`; succession.test | R-2 |
| `TestPrivilegedActivationLinkNeedsAnIndependentSuperAdmin`; succession.test Mailbox notice | R-1 |
| `TestHRCannotApproveBySubmissionAfterMovingSomeoneUnderThemself`; NewRequest.test R-3 hint | R-3 |
| `TestChangeOnBehalfFollowsTheManagementChain` | ENG-006 |
| `TestUnroutableStepSaysWhenTheCallerIsAParty`; Exceptions UI party callout | BUG-A-03 |
| `TestReviewItemsArePagedCompletely`, `TestFulfillmentTasksArePagedCompletely`, `TestTeamIsPagedCompletely`, `TestPageParam` | PERF-004 |
| `TestExceptionsCountEveryCappedList`, `TestHeldGrantsComeBeforeAnyHistory`; admin.test "counts every open record of a capped list" | FR-5 |
| `TestSwaggerIsServedOnlyInDemoMode` | R-7 |
| `TestSubmissionSurvivesADeadlockWithAToolOwnerChange`, `TestInTxRetriesOnlyDeadlocksAndOnlyThreeTimes` | FR-3 |
| `TestOverrideNotifiesEachPersonOnce` | FR-4 |
| `TestEveryErrorCodeIsTranslated` (new codes) | FR-1 |
| `TestAuditorReachesOnlyTheAuditEvents` (every API route walked, Auditor and Auditor + Super Admin + InfoSec + HR Manager); `TestAuditorPagesFiltersAndExportsEveryEvent`; App.test "gives an Auditor only the audit events, even with other roles"; audit.test "shows an Auditor only the event log…" (×2) | AUD-R (O7 note 2026-10-31) |

### Auditor restriction (2026-10-31)

The Auditor is now an external auditor who reads only the audit events (DECISIONS.md O7 note 2026-10-31). Rows and manual cases above that show an Auditor reading the directory, point-in-time access, approval evidence, the evidence reports, Exceptions, reviews, imports or the outbox (for example EMP-05, PRV-09, UI-12, RBAC-16, AUD-01, RT-B03, RT-B08 and the Exceptions browser check) record the earlier rule and are **historical**. The tests now asserting the new rule:

| Requirement | Automated | Manual / live |
|---|---|---|
| Sign in, sign out, `me` | `TestAuditorReachesOnlyTheAuditEvents` | AUD-R-01, AUD-R-07 |
| List, filter (date, actor, action, tool, text), page every event | `TestAuditorPagesFiltersAndExportsEveryEvent` (230 events over 3 pages; tool, actor, date filters); audit.test Auditor cases | AUD-R-01, AUD-R-05 |
| CSV export of every matching event, formulas neutralised | `TestAuditorPagesFiltersAndExportsEveryEvent` (231 rows) | AUD-R-02 |
| Every other endpoint refused (requests, on-behalf, own access, directory, team, approvals, grants, tools, roles, groups, tasks, Exceptions, reviews, outbox, HR/InfoSec/admin actions, point-in-time, evidence, reports) | `TestAuditorReachesOnlyTheAuditEvents` (every route); updated `TestRevokePermissionMatrix`, `TestOnlyTheAssigneeDecides`, `TestPermissionMatrix`, `TestDirectoryVisibility`, `TestAuditExplorerScope`, Exceptions tests | AUD-R-03 |
| No operational duty on an Auditor account, both directions (O7 note 2026-10-31): AUDITOR refused with each open duty named; roles, group, tool owner (create, update), manager (create, transfer), reassignment and nomination refused for an Auditor | `TestAuditorRoleRefusedWhileDutiesAreOpen` (7 duties), `TestAuditorGetsNoOperationalDuty` (8 paths); errors.test (Turkish text per duty) | AUD-D |
| Mixed roles: AUDITOR takes precedence | `TestAuditorReachesOnlyTheAuditEvents` (Auditor + Super Admin + InfoSec + HR Manager); App.test (Auditor + Super Admin) | AUD-R-04 |
| Direct URL access in the UI refused, no forbidden API call | App.test (12 URLs) | AUD-R-06 |
| Other roles keep their permissions | `TestAuditorReachesOnlyTheAuditEvents` (Super Admin control), the full suite | AUD-R-08 |
