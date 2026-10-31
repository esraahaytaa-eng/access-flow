# Release readiness — final QA (2026-10-30, fix rounds 2026-10-31)

## Verdict: **CONDITIONAL**

Every confirmed defect and every open security and scale finding is fixed, and the Auditor is restricted to the audit events (P0, below) with regression tests: F-1/R-2, R-1, R-3, R-7, ENG-006, BUG-A-03 and PERF-004 in the first fix round, then a deadlock that answered `500` (FR-3), a duplicate override notification (FR-4) and the silent row caps (FR-5) in the final engineering round. The local checks pass on the final tree. PERF-008 (campaign start, about 9 s) is accepted (Q7). Nothing is committed yet.

Conditions for PASS:
1. **CI green on the pull request.** The work is uncommitted; GitHub CI has not run on it.
2. **The remaining residuals accepted as documented risks:** R-4, R-5, R-6, R-8 and R-9; collusion by two Super Admins (R-1) or by HR, an authorizer and the nominee (R-2). All are listed in `docs/known-limitations.md`.

## Auditor restriction (2026-10-31, P0, user decision)

The Auditor is an external auditor who reads only the audit events (DECISIONS.md O7 note 2026-10-31): default deny in the API session middleware with a three-route allowlist, AUDITOR taking precedence over every other role, service-level reads removed, paged events with exact totals and a tool filter, a server-streamed CSV export of every matching event, and an events-only UI. Details: `security-audit.md` "Auditor restriction"; live results: `execution-results.md` AUD-R-01..08.

| Check (final tree, after the Auditor change) | Result |
|---|---|
| `gofmt -l cmd internal`, `go vet ./...`, `staticcheck ./...` (latest) | clean |
| `go test -race -count=1 ./...` (Docker, PostgreSQL), once | all 16 test packages pass, no data races |
| Targeted RBAC tests (`TestAuditorReachesOnlyTheAuditEvents`, `TestAuditorPagesFiltersAndExportsEveryEvent`, the updated permission tests) | pass (also inside the full run) |
| Vitest; `tsc --noEmit`; `vite build` | 20 files, 180 tests pass; clean; builds |
| Live seven-role API check, isolated stack `afqa3` (removed afterwards) | 23 of 23 PASS (one script error in the first run, fixed: a non-existent route) |
| Browser, isolated stack | 4 of 4 PASS |
| Permission-regression review of the diff | every Auditor change only removes AUDITOR; no other role's check changed |
| Migrations | no schema change; 00018 and 00019 unchanged |
| GitHub CI | NOT RUN (uncommitted) |

**Auditor duty exclusivity (2026-10-31, after the table above).** An Auditor account holds no operational duty, in both directions (O7 note 2026-10-31; `bug-report.md` AUD-D). Re-run on the final tree: `gofmt`, `go vet`, `staticcheck` clean; `go test -race -count=1 ./...` all 16 test packages pass; Vitest 20 files, 182 tests pass; `tsc --noEmit` clean; `vite build` builds. The live mixed-role step above (AUDITOR assigned to the demo manager) predates this rule and would now be refused with `auditor_has_duties` (`directReports`); the live and browser checks were NOT RUN again.

## Final engineering round (2026-10-31)

| Item | Fix | Regression test |
|---|---|---|
| FR-3: a submission racing a tool-owner change deadlocked and answered `500` | `platform.InTx` retries a transaction aborted as a deadlock (`40P01`) or serialization failure (`40001`), at most three attempts in all; every retried closure resets the state it writes; DECISIONS.md note 2026-10-31 | `TestSubmissionSurvivesADeadlockWithAToolOwnerChange` (real `40P01`; exactly one item and one audit event), `TestInTxRetriesOnlyDeadlocksAndOnlyThreeTimes` |
| FR-4: a manager who is also InfoSec got two notifications for an override | the role notification excludes the people already told | `TestOverrideNotifiesEachPersonOnce` |
| FR-5: Exceptions lists capped at 200 (500 orphans) without a total; grant lists capped at 200 could hide a held grant behind history | exact `totals` for every capped list, a UI notice when rows are not listed, held grants ordered first | `TestExceptionsCountEveryCappedList` (230 / 520 rows), `TestHeldGrantsComeBeforeAnyHistory`, Vitest "counts every open record of a capped list" |

The other deadlock races listed in `docs/known-limitations.md` use the same retry and were not reproduced one by one.

| Check (final tree) | Result |
|---|---|
| `gofmt -l cmd internal` | clean (one alignment finding in `internal/requests/change.go` fixed first) |
| `go vet ./...` | clean |
| `staticcheck ./...` (latest, Go 1.27.3, in the tests container) | clean |
| `go test -race -count=1 ./...` (Docker, PostgreSQL), once | all 16 test packages pass, no data races |
| Targeted concurrency tests, `-count=5` (deadlock submission, succession authorizations, `InTx` retry) | pass |
| Vitest | 20 files, 177 tests pass |
| `tsc --noEmit`; `vite build` | clean; builds |
| Migrations | no schema change in this round; 00018 and 00019 unchanged |
| Browser check of the Exceptions totals notice | NOT RUN (needs more than 200 records; covered by Vitest and the API test) |
| GitHub CI | NOT RUN (uncommitted) |

## First fix round (2026-10-31)

### Checklist

| Priority | Item | Status | Evidence |
|---|---|---|---|
| P0 | F-1: suspended Super Admins count for HR succession | Done | `standingSuperAdmins`; `TestSuspendingSuperAdminsOpensNoHRSuccession` (includes `recover-admin`); live `403` |
| P0 | R-2: HR nominates → independent InfoSec or Super Admin authorizes → only then the 24 h link → nominee accepts; privileged users notified | Done | migration 00019 (`authorized_by/at`, additive, `NOT VALID` check for history); `TestHRSuccessionNeedsAnIndependentAuthorizer`, `TestConcurrentSuccessionAuthorizations`, `TestCheckSuccessionAuthorizer`; live 9/9 |
| P0 | R-1: no reveal of one's own link or of a privileged link one issued; other Super Admins notified | Done | `TestPrivilegedActivationLinkNeedsAnIndependentSuperAdmin`; live 4/4 and in the browser |
| P0 | Dated D2/F4 notes | Done | `DECISIONS.md` F4 and D2 notes 2026-10-31 |
| P1 | PERF-004: server-side paging for reviews, findings, tasks, team | Done | 3 API tests at 230 rows; walked at 12,005 items (`performance-audit.md` §9); browser 4/4 |
| P1 | R-3: no approval by submission for HR Managers and Super Admins | Done | `TestHRCannotApproveBySubmissionAfterMovingSomeoneUnderThemself`; UI hint test; D3 note |
| P1 | ENG-006: on-behalf extension and level change follow the chain | Done | `TestChangeOnBehalfFollowsTheManagementChain`; D3 note |
| P1 | BUG-A-03: actionable path without bypassing separation of duties | Done | `party` in Exceptions, `TestUnroutableStepSaysWhenTheCallerIsAParty`, UI test; F2 note |
| P2 | PERF-001..010 and JIT off preserved | Done | full suite incl. `TestNewPoolTurnsJITOff`; no performance code reverted |
| P2 | Swagger not served outside demo mode | Done | `TestSwaggerIsServedOnlyInDemoMode`; A7 note |
| P2 | Audit evidence, carried-over findings, leavers' removal visibility | Verified | `TestQuarterEvidenceListsCarriedOverFindings`, `TestOpenFindingIsCarriedNotReAdded`, lifecycle/rehire `securityRevocations` tests (full suite); Exceptions screenshot |
| P2 | Misleading UI text and contradictory docs | Done | recovery banner, Mailbox notice (Vitest); `README.md`, `docs/api.md`, `openapi.json`, `docs/known-limitations.md` |

### Verification after the first fix round

| Check | Result |
|---|---|
| `gofmt -l cmd internal`, `go vet ./...` | clean (one gofmt finding in new code fixed first) |
| `go test -race ./...` (Docker, PostgreSQL), once | 15 of 16 packages passed; `internal/webui` failed: three new error codes had no Turkish text (FR-1). Fixed; `internal/webui` re-run passes. No Go code changed after the run |
| Vitest | 20 files, 176 tests pass |
| `tsc --noEmit`; `vite build` | clean; builds |
| Migration safety | `main` stack (migration 17) with data and a legacy accepted succession, upgraded in place → 19; employees, audit events and the legacy row intact; the new check rejects new unauthorized acceptances |
| Clean `docker compose up` (fresh project `afqa2`, empty volumes) | one command; all services healthy; only the app port published; demo seed served |
| Exploits F-1 / R-1 / R-2 (live, isolated) | 15 of 15 blocked or behaving as required |
| Pagination at scale | every row reachable once, totals exact; page p50 23–137 ms, max 355 ms |
| Browser (headless Chrome, isolated) | 7 of 7 PASS |
| Focused diff review (Ponytail review skill, inline; no subagents) | no must-fix findings |
| `staticcheck` | not run in this round |

### Counts (first fix round)

| PASS | FAIL | BLOCKED | NOT RUN |
|---|---|---|---|
| 22 | 0 open (1 found and fixed: FR-1) | 0 | 4: GitHub CI, `staticcheck`, Swagger `404` against a running non-demo stack (router test only), real-time-elapsed cases (24 h expiry, midnight gap; Go tests with explicit times cover them) |

PASS counts the 12 checklist items and 10 verification checks: gofmt/vet, the race suite (after the FR-1 fix and the `internal/webui` re-run), Vitest, `tsc` + build, migration safety, clean start, exploits, scale, browser and review.

The localhost:8080 demo, its accounts, database, audit history and volumes were not touched. All destructive and scale work ran in the throwaway projects `afqa` and `afqa2`, which were removed afterwards.

---

## Earlier audit (2026-10-30), kept for history

The previous verdict was **NOT READY: release blocked by F-1**, with open items F-1/R-2, R-1, BUG-A-03 and PERF-004. All are now fixed (above).

## 1. Baseline and branch (as on 2026-10-30)

- Audit baseline: branch `feat/ux-simplification` @ `5080f20`, **PR #17**. The PR is open and mergeable, and both CI checks pass ("Go checks and tests", "Build images").
- **Dependency:** the audit work is on local branch `qa/final-audit`, created from that branch. It must be merged after PR #17, or rebased onto `main` once PR #17 is squash-merged.
- All audit changes were uncommitted in the working tree. PR #17 was squash-merged into `main` on 2026-10-31 (`6c65eeb`); the audit work stays on `qa/final-audit`.

## 2. What changed (summary)

| Group | Items |
|---|---|
| Security fixes | F-2 workbook memory DoS, F-3 password text in the failed-login audit, F-4 security headers |
| Functional fixes | BUG-B-01 leaver removals visible, BUG-B-02 Exceptions label, BUG-B-03 `/audit` guard, BUG-B-04 scheduled termination actions and explanation, BUG-B-07 cancelled link cleared at once, BUG-B-08 carried-over findings in evidence, BUG-A-01 Turkish tool-error text, BUG-A-02 duplicate notification, RV-2 direct-revoke list |
| Engineering fixes | ENG-001 orphan retry 500, ENG-002 on-behalf chain in UI, ENG-003 runner spin, ENG-005 still-active check on override and reassign, ENG-007/008/009/010/012/013 |
| Performance fixes | PERF-001 set-based review sweep and non-blocking sign-in lock, PERF-002 indexed import lookup, PERF-003 badge refetch, PERF-005 review query, PERF-006 paged point-in-time list, PERF-007 audit time index (migration 00018, index only), PERF-009 asset caching, PERF-010 JIT off for the app pool (point-in-time 3.2 s → 0.57 s p50) |
| Docs | README recovery, `docs/api.md` codes and succession wording, `openapi.json` (`too_many_columns`, carried findings), `docs/known-limitations.md` (caps, sweep, security residuals) |
| Reverted | The F-1 succession rule change: it changed approved D2 wording and did not close the path (RV-1) |

Database: one new migration, `00018_audit_recent_index.sql`, which only adds an index. No destructive change, and existing data is untouched. No change to CASE.md or DECISIONS.md. No new dependency.

## 3. Final verification

| Check | Result |
|---|---|
| Backend: `go test -race ./...` (Docker, PostgreSQL) | all 15 packages pass, no data races (about 16 min; run on the final tree after the heavy load had stopped) |
| `gofmt -l`, `go vet ./...`, `staticcheck ./...` | clean |
| Frontend: Vitest | 20 files, 168 tests, all pass |
| Frontend: `tsc --noEmit` | clean |
| Frontend: `vite build` | builds (the only notice is react-router's known `"use client"` directive message) |
| Clean `docker compose up` on a fresh project (`accessflow-final`, empty volumes) | migrations applied, demo seed complete, all three services healthy, only the app port published, `/health` 200 |
| Security headers on `/`, `/api/v1/health`, `/swagger/`; caching of `/assets/*` vs `/` | present; `immutable` vs `no-cache` |
| PERF-010 (2026-10-31): `TestNewPoolTurnsJITOff` | red with the fix disabled (`jit = "on"`), green with it; `go test -race ./internal/platform/ ./cmd/accessflow/` pass, `gofmt -l cmd internal` clean, `go vet ./cmd/... ./internal/platform/` clean (isolated compose project `accessflow-perftest`) |
| Full suite after PERF-010 (2026-10-31) | `gofmt -l` and `go vet ./...` clean; `go test -race ./...`: all 15 packages pass, no data races |
| Isolated performance re-run (2026-10-31, no competing test workload) | `performance-audit.md` §8. The earlier interrupted run (exit 144: stopped by the auditor's own `pkill`, incomplete) and its p95-equals-max flaw are documented there and superseded |
| Concurrency and performance after the fixes | sign-in wait during the review sweep: 38.7 s → 0.79 s (day 7), 138.8 s → 1.0 s (day 21); see `performance-audit.md` |
| Browser retest of the fixed bugs (fresh stack) | 9 of 9 PASS (`execution-results.md`, "Re-verification") |
| Independent code and security review of the final diff | done; 2 confirmed findings (RV-1 reverted, RV-2 fixed), 1 test restored (RV-3), 1 accepted (RV-4) |

## 4. Final section

### Confirmed issues fixed
F-2, F-3, F-4; BUG-A-01, BUG-A-02; BUG-B-01 to BUG-B-08 (B-05 and B-06 are docs); ENG-001, 002, 003, 005, 007, 008, 009, 010, 012, 013; PERF-001, 002, 003, 005, 006, 007, 009, 010; RV-2, RV-3; TF-1 (test timing).

### Suspected issues not reproduced
- **SQL injection, XSS, CSV/formula injection, zip bombs, JWT forgery, CSRF bypass, IDOR, token leakage, password timing oracle, audit edits, container privileges:** all investigated, none reproduced (`security-audit.md` §3).
- **TF-2:** the `onboarding` package, `TestRunnerRecordsUnderTheEmployeeLock` and `TestToolReadVerifiesReportedAccess` each failed once in a full run while the performance load test drove the host to 130–880% CPU. Repeated in isolation with `-race` (15× and 5×): all passed. The final full run passed.
- **RV-4:** a day-21 finding racing a just-committed revoke. Traced: it closes itself when the removal completes. Not a defect.

### Improvements rejected as unnecessary
- Removing the dead recovery branches (ENG-004): security-core code, low value now.
- Removing `auth.RequireRole` (ENG-011).
- A shared "removal in progress" UI helper.
- Merging the InfoSec eligibility SQL.
- A `lower(email)` index (the existing unique index serves `email = lower($1)`).
- Batching the workbook import.
- Bundle code-splitting.
- Server-side `http.Server` timeouts.
- A `CHECK` for `SELF_REPORTED`.

Reasons are in `engineering-audit.md` §5 and `performance-audit.md` §3–4.

### Remaining limitations and why (as on 2026-10-30)
- **Release blocker (fixed 2026-10-31):** F-1/R-2.
- **Product decisions** (section 5, resolved 2026-10-31): R-1, R-3, ENG-006, BUG-A-03, PERF-004 fixed; PERF-008 accepted.
- **Documented risks by decision:**
  - R-4: lockout DoS by email; documented.
  - R-5: session secret stored in the database.
  - R-7: public Swagger (fixed 2026-10-31: served only in the demo setup).
  - R-8: the app role can insert forged audit rows.
  - R-9: HR can cancel a Super Admin's scheduled termination.
  - All are in `docs/known-limitations.md` and `security-audit.md`.
- **Scale:**
  - Unfiltered point-in-time access grows linearly (5.2 MB at 12k grants).
  - A workbook import locks the employees it names until it commits.
- **Not run:**
  - Cases that need real elapsed time: token lifetime 12 h, the UTC/Istanbul midnight gap, 24 h link expiry in the browser. Each is covered by Go tests with explicit times or by backdating.
  - A 20-item request in the browser.
  - The demo video, by instruction.

### Is the architecture still appropriately simple?
Yes. There are no new layers, interfaces, generic engines or dependencies. The sweep became shorter (set-based statements instead of per-item loops). One index-only migration was added. See `engineering-audit.md` §1.

## 5. Decisions needed from the user

| # | Topic | Recommendation |
|---|---|---|
| Q1 | **Resolved 2026-10-31.** F-1 / R-2: HR-owned succession can create a Super Admin with no Super Admin or InfoSec involvement, also by first suspending Super Admins | (a) Notify every active Super Admin and InfoSec member when HR nominates and when the nominee accepts. This is the smallest, keeps D2 as approved and makes misuse visible at once. Optionally also (b): count suspended Super Admins, so HR cannot suspend their way into a succession, and leave "none active" to `recover-admin`. That changes the D2 wording |
| Q2 | **Resolved 2026-10-31.** R-1: a Super Admin can reveal another Super Admin's or InfoSec member's activation link | Refuse the reveal for SUPER_ADMIN/INFOSEC targets (a second Super Admin must hand it over), or at least notify the other Super Admins |
| Q3 | **Resolved 2026-10-31.** R-3: HR transfers an employee under themself, then their own request satisfies the Manager step | Skip `APPROVED_BY_SUBMISSION` when the submitter holds HR Manager (the normal Manager step then applies) |
| Q4 | **Resolved 2026-10-31.** ENG-006: extension and level-change requests on behalf allow only the direct manager; new requests allow the chain, HR and Super Admin (D3 note) | Align change requests with the D3 note for consistency |
| Q5 | **Resolved 2026-10-31.** BUG-A-03: an unroutable step when the only Super Admin is a party to the request | Explain it in the Exceptions entry ("no eligible resolver: needs a Super Admin who is not part of this request") instead of offering nothing, and document that a second Super Admin, or the requester withdrawing, resolves it |
| Q6 | **Resolved 2026-10-31.** PERF-004: 2,000-item review and findings lists, 200-row task and team lists, no `truncated` flag | Add a `truncated` flag and a visible notice now (small); real paging later |
| Q7 | **Accepted 2026-10-31.** PERF-008: starting a campaign over 11,966 grants takes 6.8–17.6 s (isolated, n=3), one INSERT per item | Accept for now (quarterly; it no longer blocks sign-in), or approve `CopyFrom` (small change, existing campaign tests cover it) |
