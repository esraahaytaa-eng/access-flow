# Security audit — final QA (2026-10-30, fixes verified 2026-10-31)

**Scope:** an independent black-box and code audit of the whole application at baseline `5080f20` (PR #17), plus the fixes made on `qa/final-audit`.
**Environment:** isolated stack `accessflow-qa2` on port 8091, fresh demo seed, all 7 roles signed in with their own sessions. The user's demo on port 8080 was not touched.
**Method:**
- abuse cases per role through the real API, each checked against DECISIONS.md;
- a 29-endpoint × 7-role GET matrix;
- code reading for injection, XSS, token handling and audit integrity.

Evidence files (`matrix.txt`, `getmatrix.txt`, `lib.sh`, `xl/bomb400.xlsx`) were kept in a local working directory during the audit and are not part of the repository; their names are given for traceability only.

## 1. Findings

| ID | Sev | Title | Root cause | Status |
|---|---|---|---|---|
| F-1 | High | HR Manager can create a Super Admin while several exist: suspend the others, then nominate | Succession eligibility (`checkNominator`, `ReissueRoleLink`, `AcceptRole`) counts only ACTIVE Super Admins; HR may suspend Super Admins (D2 emergency revocation). Only the nominee is notified | **Fixed 2026-10-31** (approved D2 note): suspended Super Admins count, and an independent authorizer is required (R-2). Verified by regression tests and live on an isolated stack |
| F-2 | Medium | An 11 KB workbook made the preview allocate ~1.3 GiB (memory DoS; Super Admin only, CSRF-protected) | `excelize` `GetRows` pads each row to its last cell before the size limits ran | **Fixed** |
| F-3 | Low | A password typed into the email field was stored in `LOGIN_FAILED.details.email`, which Auditor, InfoSec and Super Admin can search and export | The submitted text was recorded for unknown emails | **Fixed** |
| F-4 | Low | No anti-framing, nosniff or referrer headers (clickjacking from same-site origins) | Nothing set them | **Fixed** |

### F-1 in detail (historical; fixed 2026-10-31)

**Original repro (SEC-180..183, 2026-10-30)**, starting with 3 active Super Admins:
1. HR nominates Deniz: 403.
2. HR suspends two of the Super Admins: 200 twice.
3. HR nominates Deniz again: 201 with `succession: true`.
4. Deniz accepts her own link and becomes Super Admin, "granted by" HR.

Only HR and Deniz were notified.

**History:** a first fix (counting non-terminated Super Admins) was reverted on 2026-10-30 because it changed approved D2 wording without approval and still let HR suspend every Super Admin first (RV-1). The user then approved the combined rule on 2026-10-31 (D2 note).

**Fix (2026-10-31):**
- Succession is open only while at most one Super Admin has not left; a suspended Super Admin still counts (`standingSuperAdmins`).
- HR only nominates. An InfoSec member or Super Admin who is neither the proposer nor the nominee must authorize (`authorized_by/at`, migration 00019) before any link exists; the nominee then accepts.
- Super Admins and InfoSec are notified at nomination and at acceptance.

**Verification:**
- Regression tests: `TestSuspendingSuperAdminsOpensNoHRSuccession`, `TestHRSuccessionNeedsAnIndependentAuthorizer`, `TestConcurrentSuccessionAuthorizations`, `TestCheckSuccessionAuthorizer`.
- Live re-run on an isolated stack: the original steps now end in `403 forbidden` at step 3 (section "Final fix round").

### F-2 fix
- `internal/onboarding/workbook.go` `checkSize` streams `f.Rows()` before any sheet is loaded.
- It refuses more than `2 × maxRows` rows (`too_many_rows`) and any row with a cell beyond column Z (`too_many_columns`, Turkish text "Z sütunundan sonra veri olamaz").
- Regression tests:
  - `TestSparseWideWorkbookIsRefusedCheaply`: the 400-row XFD file allocates under 64 MiB.
  - The workbook table case "a cell far to the right".

### F-3 fix
- `internal/auth/service.go`: the failed-login event stores the email only when `NormalizeEmail` accepts it; otherwise it stores `{"reason": "invalid_email"}`.
- Tests:
  - table cases including "a password typed as the email";
  - `TestFailedLoginAuditKeepsNoOverlongInput`.

### F-4 fix
- `cmd/accessflow/main.go` adds one root middleware that sets:
  - `X-Frame-Options: DENY`
  - `Content-Security-Policy: frame-ancestors 'none'`
  - `X-Content-Type-Options: nosniff`
  - `Referrer-Policy: no-referrer`
- It applies to the UI, Swagger and API. `TestRouter` asserts the headers on every route it checks.

## 2. Abuse cases

PASS means the server refused or behaved as the approved decisions say.

| ID | Role | Attempt | Expected | Result |
|---|---|---|---|---|
| SEC-001 | none | `GET /auth/me` without a cookie | 401 | PASS |
| SEC-002..004b | Employee / none | POST without CSRF; with another user's CSRF; cross-site POST; cross-site text/plain login | 403 | PASS |
| SEC-005..007 | Employee | JWT `alg:none`; JWT with `sub` swapped; cookie reused after logout | 401 | PASS |
| SEC-008 | new employee | session after HR suspends them | 401 | PASS |
| SEC-009 | none | password typed into the email field | not stored | FAIL → fixed (F-3) |
| SEC-020..040 | Employee | create employee, assign role, nominate, direct grant, create tool, approve, revoke another's grant, claim task, start campaign, CSV import, add department, edit self, suspend/reset manager, retry operation, admin group, starter package, mock import, dismiss orphan, override, reassign | 403/404 | PASS |
| SEC-041 | Employee | accept the Super Admin role with a forged token | 400, audited | PASS |
| SEC-042/043 | Employee / Manager | request on behalf of their own manager / a non-report | 403 `beneficiary_not_allowed` | PASS |
| SEC-050..056 | Super Admin | direct grant to self, own role, nominate self, reset self, edit self, own group; assign SUPER_ADMIN directly | 403 / 400 | PASS |
| SEC-060..072 | HR | assign role; reset SA/InfoSec; reset, edit or transfer self; suspend InfoSec; nominate self; direct grant; terminate or schedule the last SA; group member; approve | 403 / 409 | PASS |
| SEC-080 | Manager | request for a direct report | `APPROVED_BY_SUBMISSION` (D3); not for HR Managers or Super Admins since R-3 | PASS |
| SEC-090..092 | Owner / SA | owner requests own tool's Admin level and approves the owner step; SA approves a pool step without override | rerouted; 403; 403 | PASS |
| SEC-100..111 | InfoSec / Auditor | reassign to self or to the beneficiary; admin actions; Auditor writes | 400 / 403 | PASS |
| SEC-112..114 | Manager / Owner | revoke a non-report's grant; owner edits own tool or its steps | 404 / 403 | PASS |
| SEC-120..125 | HR, InfoSec, Auditor, Employee, SA | reveal an activation link; look for the token in audit, outbox and logs | 403/hidden; SA audited; token nowhere | PASS |
| SEC-130..136 | none | activate with weak/common/email-containing passwords, bogus or reused token, reveal after use | refused, audited | PASS |
| SEC-140/141 | HR | nominate with one Super Admin standing; a second succession | 201 (D2), no link until independently authorized (R-2 fix); 409 | PASS |
| SEC-142..150 | HR, SA, InfoSec, Employee, nominee | reveal or use the nominee's link; SA authorizes a succession; accept; old session; reuse | 404/400/409; ok; 401; 400 | PASS |
| SEC-160 | SA | reset another SA, reveal their link, sign in as them, authorize own nomination | should need a second person | PASS after fix (2026-10-31): `403 independent_super_admin_required` |
| SEC-170..176 | SA, Auditor, Employee, HR, SA-in-IT-Ops | review decisions outside their rights; direct grant, then claim own task | 403/404 | PASS |
| SEC-180..183 | HR | suspend 2 of 3 SAs, nominate, accept | blocked | PASS after fix (2026-10-31): `403 forbidden` |
| SEC-190..197 | various | another's notification or item; 70 KB body; SQL-style search; path traversal; reactivate or reset a suspended SA | refused | PASS |
| SEC-200 | HR | transfer an employee under themself, keeping all access | allowed (D5); their later request does not approve the Manager step (R-3 fix) | PASS |
| SEC-210 | SA | sparse-column workbook preview | refused cheaply | FAIL → fixed (F-2) |
| GET matrix | 7 roles | 29 list endpoints | matches O7, D2 and the directory rule | PASS |

## 3. Investigated, not reproduced

- **SQL injection:** none. Queries are parameterized, the explorer builder only renumbers placeholders, `Like()` escapes wildcards, ORDER BY and LIMIT are constants.
- **XSS:** none. The only `dangerouslySetInnerHTML` renders static icons. Swagger UI is read-only.
- **CSV/Excel formula injection:** guarded. Every CSV cell is quoted and `= + - @ \t \r` are prefixed. The import refuses formulas and macros.
- **Zip bomb:** bounded by the 16 MiB unzip limit and the 2 MiB upload cap. The sparse-row gap was F-2.
- **JWT and CSRF:** HS256 only; expiry and token version checked on every request; CSRF bound to the JWT; Origin and Sec-Fetch checks; HttpOnly, SameSite=Lax cookies.
- **Activation and role-acceptance tokens:**
  - 256-bit, stored as SHA-256 only.
  - The outbox copy is AES-GCM encrypted with a key kept off the database.
  - Single use, 24-hour expiry.
  - Never in logs, audit or email bodies.
  - Carried in the URL fragment, which is removed from history, with no-referrer set.
- **Passwords:** argon2id with the length, common-password and email rules. A dummy hash for unknown emails removes the timing oracle.
- **Audit log:** append-only. The app role cannot UPDATE, DELETE or TRUNCATE it, and the trigger forces `occurred_at`.
- **Docker:** distroless, non-root images. Only the app port is published.
- **IDOR:** none found on requests, grants, notifications, items, review items, tasks, operations, links or nominations.
- **Error leakage:** unknown errors become a generic 500; bodies are capped at 64 KB.

## 4. Risks tied to approved decisions

R-1, R-2, R-3 and R-7 were found on 2026-10-30 and fixed on 2026-10-31; their rows keep the original finding. R-4, R-5, R-6, R-8 and R-9 are accepted, documented residuals.

| ID | Sev | Decision | Risk (as found) | Status / mitigation |
|---|---|---|---|---|
| R-1 | High | F4 note 2026-10-30 (Super Admin reveals activation links) | One Super Admin can reset another Super Admin's or InfoSec member's password, reveal the link and sign in as them, defeating two-person rules. Detective only: `ACTIVATION_LINK_VIEWED`, `viewedBy`, the victim's lost password | **Fixed 2026-10-31** (F4 note): refused for one's own account and for a privileged link the revealer issued; other Super Admins notified. Residual: two colluding Super Admins |
| R-2 | High | D2 note 2026-10-30 | With one active Super Admin (the default), HR plus any employee can create a Super Admin with no SA/InfoSec notification | **Fixed 2026-10-31** (D2 note): an independent InfoSec member or Super Admin authorizes before the link; SAs and InfoSec notified at nomination and acceptance. Residual: HR, an authorizer and the nominee colluding |
| R-3 | Medium | D3 + D5 | HR transfers someone under themself, then submits for them: the Manager step is approved by submission | **Fixed 2026-10-31** (D3 note): not applied when the submitter is an HR Manager or Super Admin |
| R-4 | Medium | A4, known limitations | Anyone who knows an email can keep that account locked; the per-IP budget is shared behind Docker Desktop NAT | Already documented |
| R-5 | Low | A4 | The JWT signing secret is stored in the database (`settings.jwt_secret`): a database dump allows forging sessions | Keep it in a 0600 file like the mail key |
| R-6 | Info | A2/A4 | Fixed demo and DB passwords; `/api/v1/demo` lists demo emails (demo mode only) | Documented, local use only |
| R-7 | Info | Swagger (PR #16) | `/swagger/` and `openapi.json` are public without sign-in | **Fixed 2026-10-31** (A7 note): served only in demo mode, `404` otherwise |
| R-8 | Info | A9 | The app role can insert forged audit rows (any `actor_id`); append-only stops edits, not forgeries | Hash chain or log shipping |
| R-9 | Info | D7 + D2 | HR may cancel a Super Admin's termination that another Super Admin scheduled | Confirm intended |

## 5. Re-verification

- F-2, F-3 and F-4 are covered by the regression tests above.
- The final stack's headers were checked with `curl -D -` (see `release-readiness.md`).
- F-1, R-1, R-2, R-3 and R-7 were fixed on 2026-10-31 (see the rows above and `release-readiness.md`); R-4, R-5, R-6, R-8 and R-9 remain as stated.

## Final fix round: exploit re-run (2026-10-31)

Run live against an isolated stack (`afqa`, port 8091, upgraded from `main` with migrations 00018–00019), each role with its own session:

| Scenario | Result |
|---|---|
| R-2: HR nominates while one Super Admin stands → `succession: true`, no link in the nominee's outbox | PASS |
| R-2: HR reissues before authorization → `409 nomination_not_authorized` | PASS |
| R-2: HR proposer authorizes → `403 separation_of_duties`; the nominee authorizes → `403` | PASS |
| R-2: an independent InfoSec member authorizes → the link is sent; the nominee accepts; the existing Super Admin is notified at nomination and acceptance | PASS |
| R-1: the Super Admin who reset the InfoSec account reveals its link → `403 independent_super_admin_required` (API and UI) | PASS |
| R-1: another Super Admin reveals it → `200`; the first gets `PRIVILEGED_ACTIVATION_LINK_VIEWED` | PASS |
| F-1: HR suspends the second Super Admin, then nominates → `403 forbidden` | PASS |
| R-7: `/swagger/` outside demo mode → `404` (router test); in demo mode → `200` | PASS |

## Auditor restriction (2026-10-31)

**Finding (user decision, P0):** the Auditor role read the employee directory, point-in-time access, approval evidence, the evidence reports, Exceptions, reviews, imports and access-request e-mails, and an Auditor holding another role kept that role's permissions. For an external, independent auditor that exposed far more than the audit trail.

**Fix (DECISIONS.md O7 note 2026-10-31):**
- **Default deny** in `auth.RequireSession`: an account holding AUDITOR reaches only `GET /api/v1/audit/events`, `…/events/export` and `/audit/filters` (exact paths), whatever other roles it holds; every other business route answers `403 forbidden`. `/auth` (sign-in, sign-out, `me`, own password) is outside the gate. A new endpoint is refused to Auditors unless added to the allowlist.
- **Defence in depth:** the services no longer grant AUDITOR any read (directory, catalog, Exceptions, reviews, imports, nominations, health, outbox); point-in-time access, approval evidence and the reports refuse it in `explorer.ReportScope`.
- **Completeness:** the events are paged on the server with exact totals, a tool filter was added, and the CSV export streams every matching event (formula cells neutralised; a failure mid-stream aborts the connection instead of leaving a silently cut file). The filter choices come from the audit log, not the directory.
- **UI:** only "Denetim kayıtları"; every other route shows the access-denied page; no forbidden API call is made.

**Verification:** `TestAuditorReachesOnlyTheAuditEvents` (renamed `TestAuditorReachesOnlyTheAuditEvidence` by the amendment below) walks every registered API route as an Auditor and as an Auditor who is also Super Admin, InfoSec and HR Manager; live checks on an isolated stack (`execution-results.md`, AUD-R-01..08), 23 of 23 API and 4 of 4 browser checks pass. Rows SEC-100..111, SEC-120..125 and SEC-170..176 above remain valid (the Auditor is still refused there, now by the gate).

**Amendment (2026-10-31, user decision, O7 note "the Auditor also reads point-in-time access and approval evidence"):** with the events alone the Auditor could not answer CASE §5 directly. The allowlist now holds five exact `GET` routes: the three above plus `/audit/access` and `/audit/requests`, both company-wide and read-only. `explorer.ReportScope` is removed; the five evidence reports refuse `AUDITOR` in `requireFull`, whatever other roles the account holds. Approval steps now carry `decidedByName` (one `LEFT JOIN` on the step's decider), so the UI needs no directory. `TestAuditorReachesOnlyTheAuditEvidence` walks every route again (Auditor alone and with Super Admin, InfoSec and HR Manager): exactly the five return `200`, everything else `403 forbidden`. No write route, report or other role's permission changed.

**Residual (closed 2026-10-31):** an Auditor account holds no operational duty. `AUDITOR` is refused while the account has another role, group membership, owned tool, direct report, pending approval, open review item or nomination (`409 auditor_has_duties`), and those duties are refused for an Auditor (`409 employee_is_auditor`), so routed work cannot reach an Auditor. Only data written outside the API is unchecked; precedence still keeps such an account read-only.
