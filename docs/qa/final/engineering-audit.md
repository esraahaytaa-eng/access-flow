# Engineering audit — final QA (2026-10-30, updated 2026-10-31)

**Scope:** the whole codebase at baseline `5080f20`: Go backend (`cmd/`, `internal/`), React UI (`web/src`), migrations and tests.
**Question:** is the code correct, maintainable and still as simple as A8 asks ("no speculative abstractions, generic engines, repository interfaces or extra layers")?

**Method:**
- Reading every package against DECISIONS.md, following callers.
- `go vet`; `staticcheck`; a dead-code scan (`deadcode`); `tsc --noEmit`; the frontend tests.
- Targeted Go tests in Docker for each suspected bug.

## 1. Architecture assessment

| Area | Assessment |
|---|---|
| Layering | One package per business area (`access`, `requests`, `people`, `reviews`, `onboarding`, `explorer`, `auth`, `catalog`), each with its service, HTTP handlers and SQL together. There are no repository interfaces or generic engines. Business rules are pure functions with explicit time (A8, A10). Still appropriate |
| Data access | Explicit, parameterized SQL through pgx. Every state change writes its audit event in the same transaction (`platform.InTx` + `audit.Record`); this was spot-checked on every write path touched by this audit |
| Concurrency | The lifecycle advisory lock serializes who-is-active changes. Row lock order is employee → operations → grant. `access.StillActive` re-checks the actor under the caller's locks (D7). Optimistic `version` on every user-facing write. The runner uses leases and backoff. Sound; ENG-003 and ENG-005 were the only gaps found |
| Background work | One runner and one sweep loop, no per-request goroutines. Sweeps catch up after downtime (QA OPS-11, REV-23) |
| Frontend | Pages plus a small UI kit, TanStack Query for server state, no global store. Permission checks in the UI mirror the server and are for convenience only |
| Dependencies | Only those approved in A3/A7. Nothing added by this audit |

**Verdict:** the architecture is still appropriately simple. The fixes in this audit added no new layers, interfaces or dependencies. The only new functions are `checkSize` (workbook size check, F-2) and `AuditGuard` (route guard reusing the existing `useRoleFlags`). The review sweep got shorter: its per-item loops became single set-based statements (PERF-001).

**Final round (2026-10-31):** still no new layers or dependencies. The additions are a bounded deadlock retry inside the existing `platform.InTx` (three attempts, deadlock and serialization failures only), one shared `platform.PageParam` for server-side paging, a `count(*) OVER ()` total on the capped Exceptions queries, and one additive migration (00019) for succession authorization.

## 2. Findings and resolution

| ID | Sev | Category | Finding | Resolution |
|---|---|---|---|---|
| ENG-001 | Medium | Bug | `POST /operations/{id}/retry` on a failed **orphan-account** removal returned 500: `Retry` looked up the grant's employee, and an orphan has none (`pgx.ErrNoRows`) | Fixed: a missing employee is tolerated (`internal/access/operations.go`). `TestFailedOrphanRemovalCanBeRetried` |
| ENG-002 | Medium | UI/server mismatch | New Request offered only direct reports as beneficiaries; the server (D3 note) allows anyone below the submitter in the chain | Fixed: the UI walks the management chain, with a cycle guard (`NewRequest.tsx`). NewRequest.test "anyone below them in the chain" |
| ENG-003 | Low | Reliability | The runner's "employee busy" branch reported work found, so it looped without pausing while the lock was held | Fixed: the operation stays PENDING and the runner pauses (`runner.go`). `TestRunnerPausesWhileARetiredGrantsEmployeeIsBusy` |
| ENG-004 | Low | Dead code | Leftovers of the superseded InfoSec-authorized recovery in `people/succession.go` (`errRecoveryEnded`, the recovery parameter of `checkAuthorizer`, a `recovery AND NOT succession` clause, the `recovery_ended` i18n) | **Kept on purpose:** security-critical code, low value to remove now. The demo database has no legacy recovery rows. Worth removing in a separate refactor commit |
| ENG-005 | Low | Correctness (D7) | `Decide` had an inline copy of the still-active check; `Override` and `Reassign` lacked it, so an actor suspended while waiting for the lock could still act | Fixed: all three call `access.StillActive` after taking the step lock. `TestOverrideAndReassignSeeASuspensionCommittedWhileWaiting` |
| ENG-006 | Low | Inconsistency | Change requests (extend, level change) on behalf follow "direct manager only" (`requests/change.go`); new requests follow the D3 chain/HR/SA rule (`checkOnBehalf`) | **Fixed 2026-10-31** (D3 note): `Change` uses `checkOnBehalf`; `TestChangeOnBehalfFollowsTheManagementChain` |
| ENG-007 | Low | Duplication | The open-grant status list was repeated in `access/expiry.go`, `access/grants.go` and `catalog/service.go` | `grants.go` reuses `openStatuses`. `catalog` (another package) has a comment pointing to it, rather than exporting a constant for one use |
| ENG-008 | Low | Bug | The password form counted UTF-16 units; the server counts code points, so some passwords were wrongly refused | Fixed (`ChangePassword.tsx`). login.test |
| ENG-009 | Low | Docs | `docs/known-limitations.md` described the old recovery | Fixed |
| ENG-010 | Low | Stale comments | `toolclient.go`, `requests/rules.go`, `access/tasks.go`, `reviews/service.go` | Fixed |
| ENG-011 | Low | Dead code | `auth.RequireRole` is used only by tests | Kept: a documented, tested guard helper; removing it brings no benefit |
| ENG-012 | Low | Lint | `staticcheck` SA4006 (unused values) in `lifecycle_test.go` and `reviews_test.go` | Fixed; staticcheck is clean |
| ENG-013 | Low | Dead code | Unused `AccessLevel` type in `web/src/api/types.ts` | Removed |

## 3. Principles check (DRY, KISS, SOLID as they apply to this codebase)

- **DRY:** the remaining duplications are small and deliberate:
  - SQL fragments repeated across packages instead of a shared query layer (A8).
  - The "removal in progress" status pair is filtered in three UI places (EmployeeDetail transfer, the terminate count, DirectGrant).

  A shared helper was considered and rejected: each place has a different purpose, and the server enforces the rule.
- **KISS:** no generic engines.
  - The approval routing is a fixed set of step types (D1, F2).
  - The sweep is a list of plain functions.
  - This audit replaced per-item loops with single set-based statements (PERF-001). That is simpler and faster.
- **Single responsibility:** `requests/decide.go` (decide, override, reassign) and `onboarding/rules.go` are large but cohesive. Splitting them was considered and rejected: it would only move code.
- **Interfaces:** only at real boundaries: the tool client (API tools versus mock tools) and the clock-free rule functions. No speculative ones.

## 4. Security and reliability implications of the code findings

- ENG-005 closed a small separation-of-duties gap: a suspended approver acting during a lock wait.
- ENG-001 and ENG-003 were reliability bugs: a 500 on a supported action, and CPU spin while a lock was held.
- ENG-006 was fixed on 2026-10-31. The remaining open items are the documented security residuals in `security-audit.md`, not code defects.

## 5. Improvements considered and rejected as unnecessary

- Merging the `eligibleInfoSec` SQL variants: they serve different callers with different locking.
- A shared "actor" helper across packages: each package's version is three lines.
- A `CHECK` constraint for `SELF_REPORTED` evidence: the state machine already enforces it, and a constraint migration would touch existing data.
- An index on `lower(email)`: emails are stored lower-case (CHECK). Querying `email = lower($1)` uses the existing unique index (PERF-002).
- `CopyFrom` for campaign start (PERF-008): the start is a once-a-quarter operation and no longer blocks sign-in.
- Code-splitting the 530 KB bundle (154 KB gzipped): not needed for an internal tool.

## 6. Tooling results (final tree)

| Check | Result |
|---|---|
| `gofmt -l` | clean |
| `go vet ./...` | clean |
| `staticcheck ./...` | clean (after ENG-012) |
| `tsc --noEmit` | clean |
| Frontend tests | see `release-readiness.md` |
| Backend tests with `-race` | see `release-readiness.md` |
