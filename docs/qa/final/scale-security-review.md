# Scale, performance and security review (2026-10-31)

**Question:** can AccessFlow reliably handle the case's workload: 400 employees and 60 tools, with Excel onboarding, provisioning, access requests, approvals, audit logging and quarterly reviews?

**Answer:** yes, with the limitations in §D.

**Method:** every number below was measured on a disposable stack through the real HTTP API and real PostgreSQL. One problem was found and fixed (PERF-011). No security vulnerability was confirmed.

## Test conditions

| | |
|---|---|
| Hardware | MacBook, 8 CPUs, 16 GB RAM. Docker Desktop VM: 8 CPUs, 7.75 GB. No container CPU or memory limits |
| Stack | Disposable Compose project `afperf` on port 8095, with its own volumes, built from `qa/final-audit` (`da05025` plus the PERF-011 change). PostgreSQL 18.6 with `pg_stat_statements`, `log_lock_waits` and `deadlock_timeout=200ms` (the default is 1 s). The app pool uses the default settings with JIT off |
| Load generator | Python 3.9 (stdlib) on the same host, with real sessions (cookie and CSRF token). App CPU and RSS were sampled every 100 ms from `/proc` of the app process; PostgreSQL CPU from its cgroup; statement counts and times from `pg_stat_statements` (reset per scenario) |
| Host noise | The host was shared with the 8080 demo and with desktop apps. Load average ranged from 5 to 42, with one spike above 100 from a macOS `ditto` copy. Runs during that spike were discarded and repeated. Tail latencies (p99, max) of the concurrent runs are sensitive to this noise; medians and the sequential runs much less so |
| Data, through the product | Onboarding workbook import: 400 employees in 8 departments (with a manager hierarchy), 60 tools (15 API, 30 Manual, 15 Vendor) with 180 levels across all four approval-step policies, and 4,668 existing grants (8 core tools for about 92% of staff, plus 2–7 others) |
| Data, by SQL (disposable stack only) | The generated users were given the demo employee password, because the activation flow needs one link per user. One review campaign's `started_at` was backdated so the real sweep closed it. 300,000 audit events were copied from real events and spread over 365 days (`audit_events_set_time` was disabled for that one insert) |
| Demo on 8080 | Not touched. Before and after: 25 employees, 378 audit events, migration version 19 and the same password-hash fingerprint |

"Sensitivity" is not a tool field in AccessFlow. It is expressed through each level's approval steps (`Manager` … `Manager + Tool Owner + InfoSec`), which the workbook sets.

## A. Security findings

No exploitable vulnerability was confirmed in this round. Every probe ran live against the disposable stack.

| ID | Area | Probe | Result |
|---|---|---|---|
| S-01 | Workbook XXE | Sheet XML with a `file:///etc/passwd` external entity | The entity is not expanded; the cell is empty (`field_required`); the file content never appears |
| S-02 | Entity expansion | Billion-laughs DOCTYPE | Not expanded. 178 ms; RSS flat at 72.7 MiB |
| S-03 | Zip bomb | 213 KB upload that unzips to 200 MB | `invalid_workbook` in 20 ms; RSS flat |
| S-04 | Sparse columns | Cells in column XFD on 900 rows | `too_many_columns` in 120 ms (F-2 still fixed) |
| S-05 | Macros, non-xlsx | `xl/vbaProject.bin`; an OLE `.xls` header | `macros_not_allowed`; `invalid_workbook` |
| S-06 | Size limits | 3 MB upload; 1,001 employee rows | `413 file_too_large`; `too_many_rows` |
| S-07 | Formula injection | Names `=HYPERLINK(…)`, `@SUM(…)*cmd|…`, `+1-1` | Stored as plain text (they are data). Every CSV export neutralizes them: the server audit export (`TestAuditorPagesFiltersAndExportsEveryEvent`) and the UI CSV (`web/src/ui/csv.ts`). The template writes strings, never formulas |
| S-08 | Control characters, long text | Bidi override and bell characters; a 5,000-character name | Refused (`field_required`, `invalid_text`); nothing written |
| S-09 | Import authorization | Preview, import, template, CSV and mock import as HR, InfoSec, Auditor, Employee, Tool Owner and Manager | `403` for all. `/imports/mock` answers `400` before `403` for a non-JSON body (body validation runs first; no effect) |
| S-10 | CSRF | Workbook import without the CSRF header | `403 csrf_failed` |
| S-11 | Race: duplicate request | 12 different submissions for the same tool at the same moment | 1 × `201`, 11 × `409 open_request_exists`; 1 open item |
| S-12 | Race: idempotency | The same `submissionId` 12 times at once | 1 × `201`, 11 × `200` with the same request; 1 row in the database |
| S-13 | Race: revocation | The employee, their manager and a Super Admin revoke one grant (6 calls at once) | All `200`; exactly 1 removal operation and 1 audit event |
| S-14 | Race: suspension vs approval | HR suspends an employee while 4 of their items are approved | Approved grants are cancelled before reaching the tool; 0 active unsuspended grants. PostgreSQL detected 4 deadlocks, the documented "person-level action racing a request on the same person" case (DECISIONS.md, note of 2026-10-31). `InTx` retried them: no `500` |
| S-15 | Race: concurrent imports | The same 50-employee workbook imported 3 times at once | Serialized by the import lock: one creates 50 employees and 50 grants, the other two skip all. No duplicate employees, grants or activation links |
| S-16 | Race: approval and task claims | Both InfoSec members race for every pool step; 4 group members race for 207 tasks | 150 steps and 45 claims lost with `409`; 0 steps decided twice; 0 vendor tasks confirmed without a ticket |
| S-17 | IDOR | A lead reads, revokes or audits another lead's report; an employee reads another's request or notification, decides another's review item, reads audit or point-in-time data | `404` or `403`, or an empty result in the caller's scope |
| S-18 | Data exposure | Scanned 11 privileged and ordinary responses for hash, token, secret or password values; the directory as an employee; app logs | None found. The employee directory shows id, email, name, department, job title and manager. The logs hold no credentials |
| S-19 | Session and headers | Login response | Session cookie `HttpOnly; SameSite=Lax` (`Secure` with `COOKIE_SECURE` over HTTPS); `X-Frame-Options: DENY`, `nosniff`, `no-referrer`, `frame-ancestors 'none'` |
| S-20 | Mock Tool Service | Published ports | Only the app's 8080 (here 8095) is published; Mock Tool Service and PostgreSQL are unreachable from the host. Its URL comes only from the environment |

**Invariants after every load phase:** 0 duplicate open grants, 0 grants without an audit event, 0 decided steps without an audit event, 0 approved items without a grant and 0 items with two grants.

**Hypothetical risks (not exploited):**
- A full audit export costs about 15–21 s of app CPU at 314k events. Only Super Admin, InfoSec, Auditor and managers (their own scope) can request it.
- The risks R-4 to R-9 in `security-audit.md` are unchanged.

## B. Benchmark results

All latencies are in ms unless marked otherwise; the queries behind each row are in the scripts listed under "Reproducing".

| Scenario | Data volume | Duration | Throughput | p95 | Result |
|---|---|---|---|---|---|
| Workbook preview (parse + validate, no writes) | 400 employees, 180 tool levels, 4,668 access rows | 418 ms (DB 0.8 ms) | 12,600 rows/s | single request | PASS |
| Broken workbook | The same plus 23 duplicate, invalid and conflicting rows | 442 ms preview; import `400`, nothing written | — | single request | PASS (all 23 problems reported, in 17 categories) |
| Employee import | 400 employees with managers and activation links | 4.86 s (DB 1.38 s, 11,245 statements) | 82 employees/s | single request | PASS |
| Tool import | 60 tools: 57 new, 3 existing, compared; 179 new levels | 0.84 s (DB 0.20 s) | 213 levels/s | single request | PASS |
| Access import | 4,668 grants | 17.1 s (DB 5.4 s, 56,105 statements); app CPU 10.1 s, RSS ≤ 47 MiB | 273 rows/s | single request | PASS |
| Re-import (idempotency) | The whole workbook again; the access sheet again | 5.0 s and 5.1 s; 0 created, 4,668 skipped | 920 rows/s | single request | PASS |
| Import under concurrent use | Access re-import while 20 users browse | 28.5 s. Browsing p95 191; submissions p95 232; **a named employee's sign-in waited 28.1 s** | — | — | PASS with limitation (§D) |
| Concurrent requests | 150 employees × 2 items, 20 in parallel | 1.44 s, 0 errors | 104 requests/s | 282 (p99 296, max 305) | PASS |
| Concurrent approvals | 535 decisions over 3 step types, 24 approvers in parallel | 4.5 s, 0 left pending | 118 decisions/s | 101 (p99 149); inbox 131 | PASS |
| Manual and Vendor fulfillment | 207 tasks, 4 group members racing | 3.2 s | 65 tasks/s | claim 31, confirm 32 | PASS |
| Revocation and runner drain | 1,200 revocations (322 API, 608 Manual, 270 Vendor), 20 in parallel | 4.05 s; all 322 API removals sent in 8.8 s, 0 failed | 296 revocations/s; runner about 60 calls/s once the burst ended | 95 (p99 120) | PASS |
| Quarterly review start | 3,776 grants | 3.19 s (15,209 statements) | 1,180 items/s | single request | PASS |
| Quarterly review processing | 3,668 decisions plus 34 bulk keeps, 40 reviewers, 24 in parallel | 20.3 s | 181 decisions/s | decide 133 (p99 183); page 208 | PASS (6 InfoSec-pool items not walked by the script) |
| Audit search | 313,950 events, n=50 per query | Unfiltered p50 37 ms after PERF-011 (was 522); filters 27–141 ms; deepest page (offset 300k) p50 446 ms | — | Unfiltered 53; filters 33–204; text search 1,000–1,480 | PASS (text search is a full scan, §D) |
| Audit export | 313,950 events, 98.5 MB CSV | 11.5–18.3 s (one 41 s run under host load 40+) | about 20,000 rows/s | — | PASS: streamed, RSS flat at 39–45 MiB |
| Pages at the final volume | 471 employees, 63 tools, 5,034 grants, 3,776 review items, 314k events, n=50 sequential | Employee pages p50 4–7; directory 10; tools 7; team 5; approvals 5–11; exceptions 10; reviews 57–61; point-in-time unfiltered 188 (2.1 MB) | — | 5–92 for every page; `/audit/access` unfiltered 246 | PASS |
| Mixed load, realistic | 50 users, 1 s think time, 16 endpoints, 60 s | 2,687 requests, 0 errors | 43.5 requests/s | 523 (p50 33, p99 1,285, max 3.5 s) | PASS |
| Mixed load, saturation | 20 workers, no think time, 60 s | 3,716 requests, 0 errors | 60–80 requests/s | 690–980 (p50 200, max 5.9 s) | PASS: PostgreSQL CPU-bound at 4.7 of 8 cores |

**Connection pool and locks:** at most 8–14 app connections, counting the sweeps' pool; at most 12 active. At most 1 lock wait per sample; the only deadlocks were the 4 in S-14, all retried. No statement over 250 ms except the heavy InfoSec pages under saturation: `/reviews?view=all` 0.6–1.2 s and `/audit/access` 1.1–2.7 s.

**Background worker:** the operation runner sent 322 API removals in 8.8 s, about 60 calls/s once the request burst ended. Provisioning of 100 API grants finished while the approvals were still running.

## C. Before/after: PERF-011, audit log page

**Root cause.** Server paging (2026-10-31) computed the total with `count(*) OVER ()` in the page query. The window count made PostgreSQL join and sort every matching event, details included, before the `LIMIT`. At 314k events that meant an 86 MB external merge sort on disk per page view. The `(occurred_at DESC, id DESC)` index from PERF-007 was no longer used.

**Fix** (`internal/explorer/service.go`, `Events`). The total is a separate `count(*)`, and the page walks the index and stops. Both run in one read-only repeatable-read transaction, so they still see one snapshot. A text search keeps the single-pass window count: it reads every event anyway, and a separate page query was slower there (0.96 s plus a 0.44 s count, against 0.47 s).

**Regression test:** `TestAuditPageReadsOnlyItsPage` (`internal/explorer/service_test.go`). It records the SQL `Events` sends, runs it with `EXPLAIN ANALYZE` against real PostgreSQL holding 5,000 events, and requires the page query to read at most `offset + 100` events. Before the fix it failed ("read 5000 audit events, want at most 100"); after it passes. The text-search path and the totals are checked too.

**SQL alone, warm cache, 313,950 events (window count → separate count + page):**

| Filter | Before | After (count + page) |
|---|---|---|
| none, page 1 | 723 ms (2,876 cold) | 25 + 0.6 ms |
| none, offset 4,900 | 908 ms | 25 + 8.5 ms |
| action | 355 ms | 28 + 2.5 ms |
| actor | 353 ms | 30 + 3.2 ms |
| tool | 340 ms | 80 + 2.3 ms |
| dates (one month) | 67 ms | 15 + 0.4 ms |
| text (`q`) | 465 ms | unchanged path (split would be 961 + 441 ms) |

**HTTP, Auditor, n=50 (p50 / p95 / max):**

| Query | Before | After |
|---|---|---|
| `/audit/events` | 522 / 686 / 1,022 | **37 / 53 / 73** |
| `?page=50` | 767 / 842 / 959 | 44 / 66 / 124 |
| `?page=3000` (offset 300k) | 792 / 1,047 / 1,106 | 446 / 617 / 719 |
| `?action=GRANT_IMPORTED,STEP_APPROVED` (122k) | 262 / 366 / 461 | 56 / 73 / 83 |
| `?actor=` (158k) | 475 / 599 / 760 | 37 / 45 / 48 |
| `?tool=` (20k) | 406 / 570 / 805 | 141 / 204 / 272 |
| `?from=&to=` (25k) | 93 / 136 / 204 | 27 / 33 / 36 |
| Manager scope (140k) | 358 / 444 / 463 | 41 / 55 / 134 |
| `?q=Jira` (unchanged path) | 795 / 1,050 / 1,134 | 723–1,045 / 1,027–2,966 under host load 20+; same code as before |

## D. Verdict

**AccessFlow handles 400 employees and 60 tools reliably.**

- Onboarding the whole company from one workbook takes about 23 s across the three sheets (17 s of it the access sheet), and re-importing it changes nothing.
- Every daily flow answers in well under 0.3 s at p95 under concurrent use, with no errors, duplicates, lost audit events or inconsistent states. That covers requests, approvals, fulfillment, revocation and reviews.
- A quarterly review of about 3,800 grants starts in about 3 s and is decided by 40 reviewers in 20 s of machine time.
- An audit log of 314k events pages in tens of milliseconds and exports as a stream in under 20 s. For scale: onboarding plus one full request and review cycle in this run wrote about 14,000 events.

**Remaining limitations, all documented in `docs/known-limitations.md`:**

1. **An import locks every employee it names until it commits.** Measured: 17 s for 4,668 rows on a quiet host and 28.5 s while 20 users browse. A named employee's sign-in waits the whole import out; browsing and request submission are not affected. This is the PERF-002 trade-off accepted earlier, and the measured 6.1 ms per row is inside the documented 4–20 ms range. Onboarding is a rare rollout action, done before employees sign in.
   - *Not changed without approval.* The smaller fix is to lock only the employees whose row creates a grant, so a re-import that skips everything locks no one. It changes accepted, concurrency-sensitive import code.
2. **Audit text search reads every event.** About 0.5–1 s at 300k events, while the other filters stay under about 0.2 s. A trigram index would fix it if search becomes frequent. Newly documented.
3. **Imports are round-trip bound:** about 5 statements per new access row. Batching would speed up onboarding, but it is a one-off action.
4. **The heavy InfoSec and Auditor pages slow down only under saturation.** Unfiltered point-in-time access (2.1 MB at 3.5k held grants) and the full review list are fast alone (188 and 57 ms) but take 1–3 s when the database is CPU-saturated by 20 workers with no think time. That is far beyond a two-person InfoSec team.
5. **Absolute numbers come from a shared laptop.** A dedicated host would show lower tails; the medians and the relative before/after results hold.

## Reproducing

The scripts are not in the repository. They lived in this session's scratch directory, `…/scratchpad/perf/`:
- `gen/main.go`: the workbook generator.
- `malwb.py`: the hostile workbooks.
- `imports.py`, `load.py` (`requests`, `approvals`, `fulfil`, `revoke`, `review` phases), `pages.py`, `mixed.py` and `import_under_load.py`: the benchmarks.
- `sec_wb.py`, `sec_race.py`, `sec_import_race.py` and `sec_idor.py`: the security probes.
- `results/*.json`: the raw outputs.

Commands, in order:

```sh
ACCESSFLOW_PORT=8095 docker compose -p afperf -f compose.yaml -f override.yaml up -d --build --wait   # override: pg_stat_statements, lock logging
docker compose --profile test run --rm --no-deps -T -v $PWD/perf:/perf tests sh -c 'cp -r /src /tmp/w && mkdir /tmp/w/cmd/perfgen && cp /perf/gen/main.go /tmp/w/cmd/perfgen/ && cd /tmp/w && go run ./cmd/perfgen /perf/out'
python3 imports.py out preview:wb-broken.xlsx import:wb-broken.xlsx preview:wb-all.xlsx import:wb-employees.xlsx import:wb-tools.xlsx import:wb-access.xlsx import:wb-all.xlsx import:wb-access.xlsx
python3 load.py requests 150 20 && python3 load.py approvals 24 && python3 load.py fulfil && python3 load.py revoke 1200 20 && python3 load.py review 24
python3 pages.py 50 && python3 mixed.py 50 60 1 && python3 mixed.py 20 60 && python3 import_under_load.py
docker compose -p afperf down -v   # disposable project only
```

Regression test: `docker compose --profile test run --rm tests go test -race -count=1 -run TestAuditPageReadsOnlyItsPage ./internal/explorer/`
