# Performance audit — final QA (2026-10-30)

**Question:** does AccessFlow hold up at the scale in CASE.md (about 400 people, about 60 tools)? Which bottlenecks are proven, and which only theoretical?

**Environment:** isolated stack `accessflow-perf` on port 8092, on the same Docker VM as the other stacks (8 CPUs). It was loaded through the real endpoints, mostly the onboarding workbook import and the API.

**Measurement caveat:**
- The first runs (2026-10-30) shared the host with other stacks and with `go test -race` runs (up to 880% CPU during the after-measurements).
- Absolute endpoint latencies in those runs vary 2–20× between runs.
- Reliable from those runs: lock waits measured in tens of seconds, and EXPLAIN A/B pairs run back to back.
- The **isolated re-run (2026-10-31, §8)** supersedes their endpoint numbers. No test workload ran during it (every other container stayed under 12% CPU, recorded at the start and end of each run).

Scripts and raw outputs were kept in a local working directory during the audit and are not part of the repository; their names are given for traceability only. The fixes are covered by the Go and Vitest suites, which run in CI. Current figures at the case's scale (400 employees, 60 tools, 314,000 audit events) are in [`scale-security-review.md`](scale-security-review.md) §B; this report records the findings, the fixes and their before/after measurements.

**Roles in the tables are as on 2026-10-30.** The Auditor then also read point-in-time access and the evidence reports. Since the O7 notes of 2026-10-31 the Auditor reads the audit events, point-in-time access and approval evidence; the evidence reports are for InfoSec and Super Admins. The point-in-time and approval-evidence figures below therefore still apply to the Auditor; no query changed.

## 1. Dataset

| What | Count |
|---|---|
| Employees | 418 (400 generated + 18 demo), imported through the onboarding workbook |
| Tools / levels | 66 / 188: API, Manual and Vendor methods, all four step policies |
| Grants | 11,064 at the "before" point; 12,965 after the second import round (the earlier "13,964" was a miscount); 14,880 after the isolated import re-run (12,106 held now, as seen by point-in-time) |
| Requests / items / steps | 352 / 1,373 / 2,735, decided through the API by 50 leads, 10 directors and InfoSec |
| Operations | 1,511 |
| Review campaign | 8,170 items (before); 11,966 items (after) |
| Audit events | 25,760 (before); 39,800 (after) |

**How the data was made:**
- Sign-in passwords were copied to 60 approvers by SQL.
- Campaign `started_at` was backdated by SQL so the real sweep ran each step.
- `pg_stat_statements` was enabled.
- Everything else went through the product.

## 2. Findings: proven bottlenecks

| ID | Sev | Bottleneck | Evidence (before) | Change | After |
|---|---|---|---|---|---|
| PERF-001 | High | Review sweep steps ran one statement per item inside one transaction, holding locks that sign-in needs | Day 7: transaction ≥ 39.6 s, **sign-in waited 38.7 s**. Day 21: transaction ≥ 107.8 s (~57k statements), **sign-in waited 138.8 s** (`probe-day7.txt`, `probe-day21.txt`) | `reviews/sweep.go`: day 7 is one `INSERT … SELECT` of reminders; day 21 is one `UPDATE … RETURNING` feeding one audit `INSERT … SELECT`. The per-item removal lock is gone: a finding no longer removes access (D8 note), and the lock inverted the lock order. Sign-in now locks `FOR NO KEY UPDATE`, which no longer conflicts with foreign-key share locks from audit and notification inserts | Day 7: transaction ≤ 1.8 s, sign-in max **786 ms**. Day 21 (11,966 items, more than before): transaction ≤ 3.4 s, sign-in max **1,008 ms** (`probe-day7-after.txt`, `probe-day21-after.txt`). Measured under heavy host load. **Isolated re-run** (11,966 items): day 21 transaction ≈ 1 s, sign-in p50 51 / p95 70 / max 147 ms (n=66). Day 7, two clean runs: sign-in max 105 and 212 ms (n=65 each). A first isolated day-7 run had one 14–22 s stall of every endpoint, `/auth/me` included, right after the tick committed. It did not recur in two repeats, and the cause is undetermined (§8) |
| PERF-002 | Medium | Import ran an unindexable `lower(email)` lookup per row and locked each named employee until commit | wb3 (1,950 rows): 47.0 s, an employee's sign-in waited 43.8 s; lookup = seq scan, 1.57 ms | `access/imports.go`: `WHERE email = lower($1)` (emails are stored lower-case), so the unique index is used | Lookup: index scan, 0.15 ms; wb4 (1,950 rows) 37.4 s under heavier load. **Isolated re-run**, wb5 (1,950 rows): 5.8 s = 3.0 ms/row (before, quiet host: 3.9 ms/row, wb1). Mean lookup 0.046 ms (was 0.43 ms). A named employee's sign-in waited 5.4 s, the whole import. **The lock window stays by design**: an all-or-nothing import must hold its employees. Batching was not done (onboarding is rare) |
| PERF-003 | Medium | Sidebar badges refetched the full review list and Exceptions (~3.5 MB at scale) on every page change and window focus | `nav.ts` with React Query defaults (staleTime 0) | The badge observers use `staleTime: 30 s` (`badgeStaleTime`); the pages keep 0, so they stay fresh | At most one badge refetch per 30 s (code-verified: staleTime is per observer in React Query v5) |
| PERF-005 | Low–Medium | The review-items query misestimated a join (41 rows estimated, 8,170 actual) because `review_campaigns` is never analyzed (4 rows a year) | 240–560 ms with nested loops | `reviews/service.go` `Page` filters by the active campaign's id instead of joining on `closed_at` | Back-to-back pairs: old 475 / 459 / 7,655 / 1,906 ms, new **163 / 304 / 759 / 369 ms**; correct estimate, hash joins |
| PERF-006 | Medium | Point-in-time access rendered every held grant (8,310 rows, ~50k table cells) | 3.5 MB response, slow render | `Audit.tsx` shows 40 rows plus "Daha fazla göster (N)", reset on filter change; CSV still exports all rows | Render bounded. **The payload is unchanged** (12,106 grants = 5.2 MB at the end); a server-side page is a scope decision (see PERF-004) |
| PERF-007 | Medium | The audit log list had no time index: the cost grows linearly with the log | 311k events (rolled-back growth test): 870 ms | Migration `00018_audit_recent_index.sql`: `(occurred_at DESC, id DESC)`. It only adds an index: no data change, quick at this size | **5.5 ms** at the same size; `q=perf` 15.4 ms (`explain/audit_events_growth_after.out`) |
| PERF-010 | Medium | PostgreSQL JIT compiled the point-in-time query on every call. Its correlated subqueries push the planner cost (≈560k) past `jit_above_cost`, `jit_optimize_above_cost` and `jit_inline_above_cost`. Found in the isolated re-run | `EXPLAIN ANALYZE`: JIT 4,042 ms of 4,603 ms (`explain/audit_access_iso.out`). Plain query, back-to-back pairs: jit on 1,840 / 1,853 / 1,994 ms, jit off **210 / 256 / 224 ms**. `/audit/access` (Auditor, 12,106 grants, n=20): p50 3,212, p95 5,394, max 5,682 ms. 12 concurrent: `/auth/me` p50 1.5 s | `platform.NewPool` opens the app pool with `jit=off` (`internal/platform/db.go`, used by `cmd/accessflow/main.go`). Regression test `TestNewPoolTurnsJITOff`: red with the line disabled (`jit = "on"`), green with it | `/audit/access` p50 **566**, p95 640, max 858 ms (n=20). `?date=` 601 / 1,002 / 1,032 ms. `/requests` p50 79 → 44 ms and `/audit/requests` 69 → 56 ms (n=50 each; their item query also paid about 8 ms of JIT). No statement JIT-compiles any more (`pg_stat_statements.jit_functions = 0`). 12 concurrent: `/auth/me` p50 340 ms, max 2.5 s |
| PERF-009 | Low | Hashed SPA assets were served without caching headers | No `Cache-Control`, no validators: a 530 KB bundle downloaded again on reload | `webui.go`: `assets/` gets `public, max-age=31536000, immutable`; `index.html` and SPA routes get `no-cache` | Confirmed with curl on the perf stack; `TestHashedAssetsAreCachedAndThePageIsNot` |

## 3. Not changed in the first round (status as of 2026-10-31 noted per row)

| ID | Item | Reason |
|---|---|---|
| PERF-004 | Silent row caps: `/reviews` and `/exceptions` `reviewFindings` return at most 2,000 rows; `/fulfillment-tasks` and `/access/team` at most 200; no `truncated` flag. **All were hit at this scale** (8,170 items; 390 pending tasks; a lead with 200+ team grants) | A correctness-at-scale issue, not a speed one. **Fixed 2026-10-31:** server-side paging, see §9 |
| PERF-008 | Campaign start inserts items one by one: 5.0 s for 8,170 items, 79 s for 11,966 under heavy load. Isolated, 11,966 items: **6.8 s, 7.5 s and 17.6 s** (n=3; 2.6 s of that is SQL, the rest round trips; the cause of the 17.6 s outlier was not determined) | Once a quarter; it takes only foreign-key share locks, which no longer block sign-in after PERF-001. All three isolated day-7 and day-21 probes ran right after a campaign start with sign-in under 250 ms. `CopyFrom` is the fix if the wait in the Super Admin's browser is not acceptable. **Open to the user**: no change without approval, since it was earlier listed as not needed **Accepted 2026-10-31** for this case study (about 8.8 s at 12,005 grants in §9). |
| — | Workbook import lock window (PERF-002 remainder) | Required by all-or-nothing semantics; onboarding is rare |
| — | `/audit/access` unfiltered payload: 5.2 MB at 12k grants; p50 566 ms isolated after PERF-010 (it was 1–4.6 s, mostly JIT) | Filtering by tool or employee is fast (13–40 ms). Fine for 1–3 Auditors; a set-based rewrite or server paging only if the unfiltered default must stay |

## 4. Theoretical (measured as fine at this scale)

- **Runner claim poll:** 3 ms on a quiet host, linear in grants.
- **`http.Server` timeouts:** only `ReadHeaderTimeout` is set. No measured problem on a local, single-tenant deployment.
- **Review page bulk selection:** O(n²) `includes`, about 4M comparisons at 2,000 rows. Minor.
- **Bundle size:** 530 KB raw, 154 KB gzipped, single chunk. No splitting needed.

## 5. Investigated and fine

- No N+1 queries: every list endpoint runs 1–12 statements, whatever the row count.
- Every per-row probe in the slow plans is an index scan. The only missing index was PERF-007.
- Employees directory (418 rows): 10 ms. Lifecycle calls: about 100 ms with ~20 grants. Runner: 54 removals/s.
- Memory: the app peaked at 209 MiB under 12 concurrent 3.5 MB responses, then settled at 143 MiB, with no growth trend. There are no per-request goroutines, and the login rate-limit map is bounded.
- Startup: migrations take 1 s; the app listens 1.2 s after start; the demo seed is done after 4.5 s.

## 6. Endpoint latencies (first runs, 2026-10-30; superseded by §8)

| Endpoint (role) | p50 ms | Size | Rows |
|---|---|---|---|
| `/employees` (admin) | 10–13 | 159 KB | 418 |
| `/tools` (admin) | 5–8 | 49 KB | 66 |
| `/access/team` (lead) | 9–15 | 81 KB | 142 |
| `/approvals` (InfoSec) | 7–23 | 19 KB | 10 |
| `/exceptions` (admin, before findings) | 12–67 | 75 KB | — |
| `/reviews` (InfoSec, campaign of 8,170) | 312–1,477 | 1.65 MB | 2,000 (cap) |
| `/audit/events` (admin) | 40–137 | 400 KB | 1,000 (cap, `truncated`) |
| `/audit/access` (Auditor, unfiltered) | 442–2,072 | 3.5 MB | 8,310 |
| `/audit/access?employee=` | 18–60 | 9 KB | 21 |
| `/audit/reports/quarter` (Auditor) | 16–367 | 1.7 MB | 8,170 |
| `/auth/me` | 4–6 | 0.2 KB | — |

Full table: `results-scale.json`. Two caveats on these first runs: the script's p95 was the maximum (§8.1), and the host was shared.

## 7. Remaining performance risks

1. **The row caps (PERF-004):** fixed on 2026-10-31 with server-side paging (§9); the remaining Exceptions caps report exact totals.
2. **Unfiltered point-in-time access** grows linearly with grants.
3. **Workbook import** holds employee locks for its whole duration. A large import delays those employees' sign-in (5.4 s for 1,950 rows isolated; tens of seconds on a loaded host).
4. **Unexplained one-off stall:** after one isolated day-7 reminder tick, every endpoint stalled for 14–22 s. No lock waits or long transaction were seen, but the sampler then ran from the host and may itself have stalled. Two repeats with an in-container sampler were clean. Watch for it. If it recurs, sample `pg_stat_activity` and `docker stats` inside the VM.

## 8. Isolated re-run and measurement integrity (2026-10-31)

### 8.1 What went wrong in the earlier measurement
- **The interrupted run (exit 144).** The "after" endpoint run (`node measure.mjs 20 after`, started inside a shell wrapper) was ended by the auditor's own `pkill -f "measure.mjs 20 after"`. That pattern matched the node process **and** its parent shell, whose command line contained the same text. Both got SIGTERM, so the wrapper never ran its closing `docker stats` (`measure-after-hostload.txt` has its start line only). The task runner reported 144 for the killed wrapper. A plain-shell reproduction of the same `pkill -f` pattern gives 143 (SIGTERM). So the stop was deliberate, not a crash, but the run is **incomplete**:
  - 22 of 45 endpoints were printed; `results-after.json` was never written.
  - The follow-up run (`measure-after2.txt`, n=10) was also stopped, after 11 endpoints.
  - Neither counts as a result. Both are superseded by §8.3.
- **Processes:** after the stop, no `measure`, `probe`, `pool_probe`, sampler or seed process was left on the host. The perf database had no leftover sessions; it was later found stopped, with its volumes intact.
- **p95 was really the maximum.** `measure.mjs` computed p95 as the value at index `floor(0.95·n)`. With n=20 that is the largest value, so every earlier "p95" equals "max".
  - Fixed: nearest-rank `ceil(0.95·n)−1`.
  - The re-run uses n=50, with n=20 for the 0.5–4 s point-in-time calls.
  - With n=20 the p95 rests on one sample: treat it as indicative.
- **Statement counts were inflated** by the operation runner and the sweeps polling in the background: the earlier "admin `/employees`: 10 statements" was really 2. Fixed: the count is now the minimum over 3 calls.

### 8.2 Admin `/employees`
The elevated figure (p50 46.6 ms, p95 351 ms, 10 statements, `measure-after-part1.txt`) came from the loaded host and background statements. It is not a defect. Isolated, n=100, sequential, 418 employees:

| Role | p50 | p95 | max | Statements |
|---|---|---|---|---|
| admin | 10.7 ms | 17.3 ms | 39.4 ms | 2 |
| HR | 11.1 ms | 15.9 ms | 22.8 ms | 2 |
| employee | 8.7 ms | 10.4 ms | 14.5 ms | 2 |

### 8.3 Isolated endpoint results
**Setup:**
- Sequential: concurrency 1, after 2 warm-up calls.
- Dataset: 418 employees, 66 tools, 14,880 grants (12,106 held), 54k audit events, an 11,966-item campaign.
- Raw files: `results-iso-*.json`, `measure-iso-after.txt`.

**Before/after columns:**
- "Before" = earlier runs: the old code on the shared host, with p95 equal to max.
- "Isolated, JIT on" = the current code without PERF-010.
- "Isolated, JIT off" = with PERF-010.

| Endpoint (role) | n | Before p50 / max | Isolated, JIT on: p50 / p95 / max | Isolated, JIT off: p50 / p95 / max |
|---|---|---|---|---|
| `/audit/access` (Auditor, 12,106 grants, 5.2 MB) | 20 | 1,818 / 7,374 (8,310 grants) | 3,212 / 5,394 / 5,682 | **566 / 640 / 858** |
| `/audit/access?date=` (Auditor) | 20 | 1,166 / 3,581 | 4,216 / 5,697 / 5,868 | **601 / 1,002 / 1,032** |
| `/audit/access` (director, 1,175 grants) | 20 | 185 / 277 | 110 / 149 / 169 | 89 / 127 / 301 |
| `/requests` (admin) | 50 | 343 / 3,101 | 79 / 176 / 224 | **44 / 67 / 116** |
| `/audit/requests` (Auditor) | 50 | 134 / 458 | 69 / 193 / 241 | 56 / 92 / 177 |
| `/reviews` (InfoSec, 2,000 of 11,966) | 50 | 1,477 / 3,418 | 256 / 398 / 746 | 190 / 272 / 323 |
| `/reviews` (Super Admin) | 50 | 414 / 3,825 | 122 / 170 / 223 | 118 / 160 / 173 |
| `/reviews` (owner, 556 items) | 50 | 43 / 138 (293 items) | 81 / 146 / 239 | 47 / 84 / 104 |
| `/audit/events` (admin) | 50 | 137 / 309 | 25 / 50 / 97 | — |
| `/audit/events?q=perf` | 50 | 196 / 391 | 28 / 50 / 59 | — |
| `/exceptions` (admin, 2,000 findings, 1.8 MB) | 50 | 443 / 873 (n=10) | 117 / 178 / 228 | — |
| `/employees` (admin) | 50 | 10 / 18 | 11 / 19 / 22 | — |

Every other endpoint in `measure-iso-after.txt` (n=50) has p50 ≤ 30 ms and p95 ≤ 72 ms with 1–8 statements. The exceptions are the capped lists (PERF-004) and `/audit/reports/quarter`: 212 / 297 / 405 ms, 2.7 MB, 11,971 rows, no cap.

### 8.4 Write paths and lock waits (isolated)

| Probe | Before | After |
|---|---|---|
| Day-21 sweep: sign-in wait | 138.8 s | max 147 ms (n=66) |
| Day-7 sweep: sign-in wait | 38.7 s | max 105 / 212 ms in 2 runs (n=65 each); one earlier run stalled 14–22 s, not reproduced (§7.4) |
| Workbook import, 1,950 rows | 47.0 s, loaded host | 5.8 s, 3.0 ms/row; a named employee's sign-in waits the import out (5.4 s), by design |
| Campaign start, 11,966 items | 79 s, loaded host | 6.8 / 7.5 / 17.6 s (PERF-008, accepted 2026-10-31) |
| 12 concurrent `/audit/access` + `/auth/me` | `/auth/me` p50 1.5 s, max 7.2 s | `/auth/me` p50 340 ms, max 2.5 s; the heavy calls themselves p50 2.5 s (CPU-bound stress far beyond 1–3 Auditors) |

## 9. PERF-004 paging at scale (2026-10-31)

Isolated stack `afqa` (no other workload): 12,005 review items in one campaign (3,000 reports × 4 imported grants, plus the seed), 2,500 open findings, 2,503 manual tasks, a manager with 3,004 direct reports. Every page was walked through the API with real sessions; each row was seen exactly once and the rows reached equal `total`.

| List (role) | Rows / pages | Page time p50 / max |
|---|---|---|
| `/reviews?view=all` (Super Admin) | 12,005 / 121 | 137 / 355 ms |
| `/reviews?view=mine` (Tool Owner, all own items) | 6,003 / 61 | 75 / 314 ms |
| `/reviews?view=findings` (Auditor) | 2,500 / 26 | 47 / 74 ms |
| `/fulfillment-tasks?tab=queue` (group member) | 2,503 / 26 | 62 / 78 ms |
| `/access/team` (manager) | 3,004 / 31 | 23 / 38 ms |
| `/exceptions` (Auditor): `reviewFindingsTotal` 2,500, 100 listed | — | 65 ms |
| Searches (review, task, team) over the whole list | exact single match | 12–51 ms |

`/access/team/{id}` answers `404` for anyone but the report's manager. Campaign start over 12,005 grants took 8.8 s (PERF-008, accepted). Still capped without paging: orphans (500), failed operations, unroutable steps and overlaps (200 each), and 200 grants per employee (documented in `docs/known-limitations.md`). JIT stays off (PERF-010, `TestNewPoolTurnsJITOff`).

## 10. Follow-up (2026-10-31)

PERF-011: the paged audit log (§9) had stopped using the PERF-007 index; it counted every matching event inside the page query. Fixed, with before/after numbers, in [scale-security-review.md](scale-security-review.md).
