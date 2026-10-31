# Execution results — final QA (2026-10-30; fixes verified 2026-10-31)

Baseline: branch `feat/ux-simplification` @ `5080f20` (PR #17, since squash-merged into `main` as `6c65eeb`); audit work on the branch `qa/final-audit`.
Every case below was executed against a real running stack (browser over the Chrome DevTools Protocol, the real API with each role's own session, and SQL checks on the stack's PostgreSQL and Mock Tool Service databases). Nothing marked PASS was inferred.

**Environments** (isolated; the user's demo on port 8080 was never modified):

| Stack | Port | Used for | Seed |
|---|---|---|---|
| `accessflow-qa` | 8090 | QA part A (AUTH, EMP, REQ, APR, PRV, EXP, UI) | fresh demo seed |
| `accessflow-qa3` | 8093 | QA part B (LCY, SUC, REV, ORP, AUD, RBAC, OPS) | fresh demo seed |
| `accessflow-qa2` | 8091 | security abuse cases (see `security-audit.md`) | fresh demo seed |
| `accessflow-perf` | 8092 | performance at scale (see `performance-audit.md`) | 418 employees, 66 tools, 11,064 grants |

**Time-based states:** the app uses real time only (D12). To reach day 7/14/21, expiry or link expiry, rows were backdated directly in the isolated databases, and the real sweeps then ran. Each such case says so.

**Build note:** QA ran on the code at the start of the audit (baseline plus the first fixes). Every bug found was rechecked against the final tree. Fixed bugs were re-verified with regression tests and the retests in the "Re-verification" section at the end.

Status values: PASS, FAIL, PARTIAL, BLOCKED, NOT RUN. Evidence files (screenshots, scripts, CSVs) were kept in a local working directory during the audit and are not part of the repository; their names are given for traceability only. The counts below are the 2026-10-30 run, with 7 demo accounts; the eighth (`emre.kaya`, 2026-10-31) came later.

## Summary

| Part | Cases | PASS | FAIL | PARTIAL | BLOCKED | NOT RUN |
|---|---|---|---|---|---|---|
| A (8090) | 127 | 120 | 3 | 0 | 1 | 3 |
| B (8093) | 108 + 3 not run | 101 | 5 | 2 | 0 | 3 |

The failures are listed in `bug-report.md` (BUG-A-01..03, BUG-B-01..08) with their resolution.

The counts above are the **initial results** of the 2026-10-30 run and are kept as executed. Every initial FAIL or PARTIAL has since been fixed and re-verified; none is open:

| Case | Initial result | Fix | Regression test | Final verification |
|---|---|---|---|---|
| APR-12 | FAIL | BUG-A-03: Exceptions explains who must act (`party`) | `TestUnroutableStepSaysWhenTheCallerIsAParty`, admin/Exceptions UI test | PASS (2026-10-31) |
| APR-13 | BLOCKED in the browser | — (not a defect) | Go test (reassign to a non-InfoSec member: `400 assignee_not_infosec`) | PASS (automated) |
| PRV-08 | FAIL | BUG-A-01: Turkish headline, raw error collapsed | admin.test "explains a failed tool call in Turkish…" | PASS (retest 9/9) |
| PRV-12 | FAIL | BUG-A-02: one notification per person | `TestDirectGrantNotifications` | PASS (retest) |
| OPS-09 | FAIL | BUG-B-05: README recovery text | — (docs) | PASS; text updated again for the 2026-10-31 succession rule |
| OPS-10 | FAIL | BUG-B-06: api.md codes | — (docs) | PASS |
| RBAC-11 | FAIL | BUG-B-03: `/audit` guard | App.test "opens the audit page only to the roles that may use it" | PASS (retest) |
| LCY-15 | FAIL | BUG-B-01: leaver removals listed | `TestEmployeeGrants` | PASS (retest) |
| LCY-26 | PARTIAL | BUG-B-04: immediate termination offered, wait explained | employees.test | PASS (retest) |
| SUC-25 | PARTIAL | BUG-B-07: link cleared at once | `TestCancelledNominationClearsItsLinkAtOnce` | PASS |
| REV-17 | FAIL | BUG-B-02: "Onay ve denetim" label | reviews.test | PASS (retest) |
| REV-26 | FAIL | BUG-B-08: carried-over findings in evidence | `TestQuarterEvidenceListsCarriedOverFindings` | PASS (retest) |

The security cases SEC-160 and SEC-180..183 (R-1, F-1) are tracked in `security-audit.md`; both are blocked since 2026-10-31.

## Part A — authentication, employees, requests, approvals, provisioning, expiry, UI (8090)

Default precondition: fresh demo seed; signed in as the role named in the steps.

| ID | Feature | Ref | Preconditions | Steps | Expected | Actual | Status | Evidence |
|---|---|---|---|---|---|---|---|---|
| AUTH-01 | Login, 7 demo roles | A4, D12 | seed | POST /auth/login for each of the 7 accounts | 200 | 200 for all 7 | PASS | t0.mjs |
| AUTH-02 | Wrong password | A4 | seed | bad password for deniz | 401 invalid_credentials | 401 invalid_credentials | PASS | auth1 |
| AUTH-03 | Unknown email | A4 | seed | unknown email | same generic 401 | 401 invalid_credentials | PASS | auth1 |
| AUTH-04 | No session | A4 | none | GET /employees without a cookie | 401 | 401 authentication_required | PASS | auth1 |
| AUTH-05 | CSRF missing | A4 | session | POST without X-CSRF-Token | 403 | 403 csrf_failed | PASS | auth1 |
| AUTH-06 | CSRF wrong | A4 | session | logout with a wrong token | 403 | 403 csrf_failed | PASS | auth1 |
| AUTH-07 | Cross-origin | A4 | none | login with Origin evil.example + Sec-Fetch-Site cross-site | 403 | 403 cross_origin_request | PASS | auth1 |
| AUTH-08 | Activation issued | F4 (M18) | HR session | HR creates an employee | no password, a link, one email | 201, hasPassword=false, ACTIVATION_LINK_ISSUED, 1 ACTIVATION email | PASS | auth1, SQL |
| AUTH-09 | Who can reveal a link | F4, O2 | pending link | HR, employee and SA call GET activation-link | only SA, no-store, audited | HR 403, employee 403, SA 200 with Cache-Control no-store; ACTIVATION_LINK_VIEWED | PASS | auth2 |
| AUTH-10 | Login before activation | F4 | pending link | login with any password | 401 | 401 | PASS | auth2 |
| AUTH-11 | Password policy | A4 (M18) | pending link | activate with short, common, email-name, 129-char passwords | field errors; link stays usable | password_length, common_password, password_contains_email, password_length; link still usable | PASS | auth2 |
| AUTH-12 | Activation success | F4 | pending link | valid password | 204, nobody signed in | 204, no Set-Cookie | PASS | auth2 |
| AUTH-13 | Single use | F4 | used link | reuse the link, then reveal | 400; reveal 410 | 400 invalid_activation_link (ACTIVATION_FAILED reason used); reveal 410 | PASS | auth2, SQL |
| AUTH-14 | Login after activation | F4 | activated | login; GET /activations | 200; COMPLETED | 200, mustChangePassword=false; COMPLETED | PASS | auth2 |
| AUTH-15 | Token secrecy | F4, A4 | after AUTH-08..14 | search app logs, audit, outbox and API responses for the token | nowhere | not found anywhere; no passwords in logs | PASS | auth7 |
| AUTH-16 | Logout | A4 | session | logout, then reuse a copied cookie | 401 | 401 | PASS | auth3 |
| AUTH-17 | Password change | A4 | session | wrong current; same password; valid change | errors; other sessions end | invalid_current_password; password_unchanged; 200, own session 200, other 401 | PASS | auth3 |
| AUTH-18 | Lockout | A4 | account | 5 wrong passwords, then the correct one | locked | LOGIN_LOCKED; correct password 401 (reason locked) | PASS | auth3, SQL |
| AUTH-19 | Reset by HR | F4 (M18) | account with password | HR password-reset | password removed, new link | 204; 0 password rows; old password 401; new email | PASS | auth4 |
| AUTH-20 | Expired link | F4 | `activation_tokens.expires_at` backdated in the isolated DB | activate; reveal; list | 400 / 410 / EXPIRED | 400; 410; EXPIRED, reason RESET | PASS | auth4, auth5 |
| AUTH-21 | Reissue | F4 | locked account | SA reset, activate, login | works, clears lockout | 204, 204, login 200 | PASS | auth4 |
| AUTH-22 | Privileged and self reset | F4 | seed | HR→InfoSec, HR→SA, self (HR, SA), employee→other, terminated | refused | 403 privileged_reset_requires_super_admin ×2; 403 self_change_forbidden ×2; 403 forbidden; 400 employee_not_active | PASS | auth4 |
| AUTH-23 | Suspension ends session | D7, A4 | user signed in | HR suspends | 401 at once | session 401; login 401; after reactivation 200 | PASS | auth5 |
| AUTH-24 | Rate limit | A4 (M18) | none | 65 activate attempts | 429 from the 61st | 60×400 then 429 too_many_attempts, Retry-After 60; login also 429; existing session 200 | PASS | auth6 |
| AUTH-25 | UI mailbox scope | O2 (M18) | pending activation | HR opens E-posta kutusu | no activation email | not shown | PASS | ui5 |
| AUTH-26 | UI reveal | F4 | pending activation | SA opens the email, "Bağlantıyı göster" | link shown, audited once | `/sifre-belirle#<token>` shown; 1 view event | PASS | auth-ui-mailbox-reveal.png |
| AUTH-27 | UI Şifreni Belirle | F4 | link | common password; mismatched repeat; valid | Turkish errors; success; no cookie | "Bu parola çok yaygın…", "Parolalar eşleşmiyor.", "Parolanız belirlendi…", no cookie | PASS | auth-ui-setpassword*.png |
| AUTH-28 | UI reused link | F4 | used link | open it again | invalid message | "Bağlantı geçersiz, kullanılmış veya süresi dolmuş…" | PASS | auth-ui-link-reused.png |
| AUTH-29 | UI login | A4 | seed | wrong, then correct password | Turkish error; shell | "E-posta adresi veya parola hatalı."; shell loads | PASS | auth-ui-login-error.png |
| AUTH-30 | UI logout | A4 | session | user menu → Çıkış yap | login page; token invalid | login page; /me 401 | PASS | ui6 |
| AUTH-31 | UI CSRF | A4 | session | delete the csrf cookie, then revoke | Turkish error | "Güvenlik doğrulaması başarısız…" | PASS | auth-ui-csrf.png |
| AUTH-32 | UI session end | D7 | signed in | HR suspends; user navigates | back to login | back to login | PASS | auth-ui-suspended-session.png |
| AUTH-33 | Token lifetime | A4 | — | 30-minute reissue and 12-hour cap | — | — | NOT RUN | needs real elapsed time |
| EMP-01 | Create validation | F4, F3 | HR session | duplicate email (also case-only), invalid email, blank name, unknown/case-only department, unknown position, inactive/unknown manager, 500-char name | field errors | 409 email_taken ×2; invalid_email; full_name_required; unknown_department (+ expected "Engineering"); unknown_job_position; manager_inactive ×2; manager_not_found; value_too_long | PASS | emp1 |
| EMP-02 | Create permission | F4 | seed | manager and auditor create | 403 | 403 both | PASS | emp1 |
| EMP-03 | Edit | F4 | employee | change title; stale version; change department; missing field; self; terminated | rules held | 200; 409 version_conflict; 409 transfer_required; 400 field_required; 403 self_change_forbidden; 400 employee_terminated | PASS | emp1 |
| EMP-04 | Manager cycle | F4 | seed | put burak under his own report | 400 | 400 manager_cycle | PASS | emp1 |
| EMP-05 | Directory scope | F4 | seed | employee vs auditor list | limited vs full | employee: 6 fields, active only (18); auditor: 20 incl. terminated | PASS | emp1 |
| EMP-06 | Company catalog | F3 (M17) | SA | case-duplicate; create; retire; use retired; employee creates | rules held | 409 name_taken; 201; inactive; 400 retired_department; 403 | PASS | emp1 |
| EMP-07 | Workbook template | D10 (M17) | seed | SA and HR download | SA only | SA 200 xlsx; HR 403 | PASS | dl.mjs |
| EMP-08 | Workbook preview | D10 (M17) | workbook | HR preview; SA preview | SA only, no writes | HR 403; SA summary; DB unchanged | PASS | emp2 |
| EMP-09 | Workbook import | D10, F2 | good.xlsx | import | all-or-nothing | 201; 2 employees (no password, 2 activation emails); tool + 2 levels; 2 IMPORTED_BASELINE grants; APPROVAL_POLICIES_IMPORTED to each InfoSec member | PASS | emp2, SQL |
| EMP-10 | Re-import idempotent | D10 | after EMP-09 | import the same file | no duplicates | 201, all "existing", count unchanged | PASS | emp2 |
| EMP-11 | Bad workbook | D10 | bad.xlsx | preview/import | 400, nothing stored | 400 workbook_invalid listing all 8 problems; nothing stored | PASS | emp3 |
| EMP-12 | Bad files | A3 | — | non-xlsx; 3 MB; untouched template | refused | invalid_workbook; 413 file_too_large; sample_row | PASS | emp2 |
| EMP-13 | Differences are warnings | D10 (M17) | imported data | changed name, department, method, steps | warnings, no overwrite | employee_differs ×3, tool_differs, level_differs; DB unchanged | PASS | emp4 |
| EMP-14 | Starter packages | F3 | seed | HR add; SA add; duplicate; unknown department | SA only | 403; 201; 409 package_entry_exists; 400 unknown_department | PASS | misc1 |
| EMP-15 | UI create employee | F4 | HR | empty form; duplicate; valid | Turkish errors; success | field errors; "Bu e-posta adresi zaten kullanılıyor."; created with activation note, no password | PASS | emp-ui-*.png |
| EMP-16 | UI workbook | D10 | SA | upload bad.xlsx, then a good file | problems listed, import disabled; import works | "8 sorun…" in Turkish, no raw codes, button disabled; summary; "Excel içe aktarıldı" | PASS | emp-ui-workbook-*.png |
| REQ-01 | Duration | D6 | employee | yesterday; today (Istanbul); bad date; none; bad type | refused | expiry_not_in_future ×2; invalid_duration ×3 | PASS | req1 |
| REQ-02 | Justification | D6 | employee | blank; 5000 chars | refused | justification_required; value_too_long | PASS | req1 |
| REQ-03 | Items | API | employee | 0; 21; same tool twice; bad 2nd item | refused | items_required; too_many_items; duplicate_tool; level_not_found on items[1] | PASS | req1 |
| REQ-04 | Tool already held | D4 | holder | same or other level via /requests | 409 | 409 access_exists ×2 | PASS | req1 |
| REQ-05 | Self request | CASE §1 | employee | 2 items (temporary + until revoked) | Manager first; 1 email | 201; Manager pending → Tool Owner waiting; 1 email | PASS | req2 |
| REQ-06 | Idempotent submit | A9 | employee | repeat submissionId; another user reuses it | 200 original; 409 | 200 same; 409 submission_id_reused | PASS | req2 |
| REQ-07 | Duplicate open request | A9 | pending request | same tool again | 409 | 409 open_request_exists | PASS | req2 |
| REQ-08 | Request visibility | API | request | another user, the manager GET it | 404 | 404 ×2 | PASS | req2 |
| REQ-09 | On behalf not allowed | D3 note | seed | peer; manager of another team | 403 | 403 beneficiary_not_allowed ×2 | PASS | req2 |
| REQ-10 | On behalf, direct manager | D3, O2 | manager | burak for elif | step satisfied, no email | APPROVED_BY_SUBMISSION; 0 emails | PASS | req2 |
| REQ-11 | On behalf, indirect manager | D3 note | 2-level chain | burak for a report of deniz | normal Manager step | Manager step pending on deniz; 1 email | PASS | req2 |
| REQ-12 | On behalf, HR and SA | D3 note | seed | HR for mert; SA for qa.ind | normal Manager step | pending on the real manager; email sent | PASS | req2 |
| REQ-13 | Inactive beneficiary | API | seed | suspended; terminated | 400 | beneficiary_not_active ×2 | PASS | req2 |
| REQ-14 | Retired level | fulfillment | retired level | request it | 400 | level_not_requestable | PASS | misc1 |
| REQ-15 | Withdraw pending | O5 | pending | unrelated user; no reason; owner | 404; 400; withdrawn | 404; field_required; WITHDRAWN, steps CANCELLED | PASS | req3 |
| REQ-16 | Withdraw approved, unclaimed | O5 | approved Manual | withdraw | grant cancelled | WITHDRAWN; grant and task CANCELLED | PASS | req3 |
| REQ-17 | Withdraw after claim | O5 | claimed task | withdraw | 409 | 409 fulfillment_started | PASS | req3 |
| REQ-18 | UI new request | CASE §1 | employee | empty submit; keyboard combobox; short justification; submit | Turkish errors; detail page | "Araç seçin.", "Gerekçe en az 15…"; Enter picks; /requests/{id} with "Talebiniz gönderildi" | PASS | req-ui-*.png |
| REQ-19 | UI duplicate | A9 | pending request | same tool | blocked | "Salesforce için onay bekleyen bir talep var." | PASS | req-ui-duplicate.png |
| REQ-20 | UI withdraw | O5 | pending | "Talebi geri çek" | reason required; withdrawn | "Gerekçe en az 3 karakter olmalı."; WITHDRAWN | PASS | req-ui-withdrawn.png |
| REQ-21 | Mock email rules | O2 | after REQ-* | count emails by kind | only NEW_ACCESS with a person Manager step | 20 emails for 26 NEW_ACCESS; 0 for others and manager submissions | PASS | SQL |
| REQ-22 | 20-item boundary | API | — | exactly 20 items | — | — | NOT RUN | needs 20 distinct tools |
| APR-01 | Wrong approvers | F2 | pending step | other user, beneficiary, auditor approve | 403, recorded | 403 ×3; 3 APPROVAL_ATTEMPT_REJECTED | PASS | apr1 |
| APR-02 | Stale version | A9 | pending step | approve with an old version | 409 | 409 version_conflict | PASS | apr1 |
| APR-03 | Double click | A9, D9 | pending step | same approver twice in parallel | idempotent | 200, 200; 1 STEP_APPROVED | PASS | apr1 |
| APR-04 | Sequential chain | F2 | API level | Manager → Tool Owner | active and verified | ACTIVE/VERIFIED; permission in the mock DB | PASS | apr1 |
| APR-05 | Reject | F3, O5 | pending | no reason; blank; with reason | final | field_required; reason_required; REJECTED, later steps CANCELLED | PASS | apr1 |
| APR-06 | Rejection is final | D1 | rejected | override, approve | 409 | item_not_pending ×2 | PASS | apr1 |
| APR-07 | Reassign | D9 | pending | manager; to self; to beneficiary; to submitter; to suspended; no reason; valid | rules held | 403; self_assignment; separation_of_duties ×2; assignee_not_active; field_required; 200 STEP_REASSIGNED | PASS | apr2 |
| APR-08 | Override | D1 | pending | InfoSec; no reason; SA | SA only, recorded, notified | 403; 400; 200 SUPER_ADMIN_OVERRIDE; notifications; in overrides report | PASS | apr2 |
| APR-09 | Two InfoSec at once | D9 | pool step | parallel approvals | first wins | 200 and 409; 1 STEP_APPROVED + 1 APPROVAL_ATTEMPT_REJECTED | PASS | apr2 |
| APR-10 | Owner is beneficiary | F2 | owner | owner requests own tool | Tool Owner step to pool | pool; owner approving 403 | PASS | prv4 |
| APR-11 | Unroutable step | F2 | InfoSec requester | selin requests AWS Admin | listed in Exceptions | unroutable true; unroutableSteps=1 | PASS | apr3 |
| APR-12 | Resolving an unroutable step | F2, D1 | APR-11 | SA override; reassign; UI drawer | SA override resolves | 403 separation_of_duties ×2; UI offers no action | FAIL (initial; fixed and verified 2026-10-31, see the table above) | BUG-A-03; apr-ui-unroutable-no-action.png |
| APR-13 | Reassign InfoSec step to non-InfoSec | D9 | — | — | 400 assignee_not_infosec | only reassigner was a party (403) | BLOCKED | apr4 (covered by Go test) |
| APR-14 | Stricter wins | F2 | pending | add InfoSec step after submission | step added | INFOSEC appended; provisioned after approval; 2 notifications | PASS | misc1 |
| APR-15 | UI approval drawer | O7 | inbox | drawer; reject without reason; Escape; approve | works | waiting time; role=dialog aria-modal; reason error; Escape closes; approved | PASS | apr-ui-*.png |
| APR-16 | Inbox badges | O7 | seed | sidebar badge vs /approvals | equal | equal for all roles once loaded | PASS | ui-nav-*.png |
| PRV-01 | API verified state | O1, A1 | seed | compare grants with mock permissions | consistent | 0 mismatches | PASS | dat1 |
| PRV-02 | Manual task | fulfillment | Manual grant | non-member; SA non-member; confirm before claim; claim; 2nd member; non-claimer; ticket on manual; confirm | rules held | 403; 403; not_claimer; IN_PROGRESS; task_claimed; not_claimer; not_a_vendor_task; ACTIVE/SELF_REPORTED | PASS | prv1 |
| PRV-03 | Vendor task | fulfillment | Vendor grant | confirm no ticket; 101-char; blank; valid; confirm | rules held | ticket_required; value_too_long; ticket_ref_required; WAITING_VENDOR; ACTIVE | PASS | prv2 |
| PRV-04 | Manual failure and retry | O3, O4 | Manual task | fail no reason; fail; retry by employee/auditor; by member; finish | rules held | 400; FAILED in Exceptions; 403 ×2; PENDING unclaimed; REVOKED | PASS | prv1 |
| PRV-05 | SA retry of seeded failure | O3 | seed FAILED | retry | back to queue | PENDING | PASS | prv1 |
| PRV-06 | Own task | F2 | own task | open, claim | cannot act | canAct=false; 403 | PASS | prv4 |
| PRV-07 | Tool outage | O3, A2 | mocktools stopped; `next_attempt_at` fast-forwarded in the isolated DB | approve an API item | FAILED after 5 attempts | 1–5 then FAILED; listed; mock import 502 | PASS | prv3, t5 |
| PRV-08 | Failure text in UI | A7 | PRV-07 | open İstisnalar | Turkish text | raw Go error with internal host/IP | FAIL | BUG-A-01; prv-exceptions-infosec.png |
| PRV-09 | Retry permission in UI | fulfillment | failed op | InfoSec without group; Auditor | no retry; read-only | as expected | PASS | ui-exceptions-auditor.png |
| PRV-10 | UI retry | O3 | mocktools back | SA "Yeniden dene" | recovers | ACTIVE/VERIFIED; cleared | PASS | prv-exceptions-after-retry.png |
| PRV-11 | Direct grant validation | D1 | SA | no expiry; today; +90; self; suspended; InfoSec caller; no justification; +89 | rules held | expires_required; expires_in_past; duration_too_long; 403 SoD; beneficiary_not_active; 403; justification_required; +89 → ACTIVE | PASS | prv2 |
| PRV-12 | Direct grant notifications | D1 | PRV-11 | count | one per person | InfoSec member who is also the owner got 2 | FAIL | BUG-A-02 |
| PRV-13 | Manual direct grant | D1 | temporary IT Ops membership for admin (DB, closed after) | initiator claims; other member confirms | initiator blocked | 403 SoD; other member → ACTIVE | PASS | prv5 |
| PRV-14 | Voluntary revoke | O6 | grant | unrelated; employee; repeat | rules held | 404; REVOCATION_REQUESTED; repeat 200 | PASS | prv4 |
| PRV-15 | Manager revoke | D5 | report's grant | manager revokes | removed | REVOKED; mock gone | PASS | prv4 |
| PRV-16 | Orphan revoke via grants | D10 note | orphan | SA; employee | refused | 409 orphan_account; 404 | PASS | prv4 |
| PRV-17 | Retired before provisioning | fulfillment | pending | retire, then approve | cancelled | CANCELLED "access level retired" | PASS | misc3 |
| PRV-18 | UI tasks | O7 | Manual task | claim, confirm | done | "Görev üstlenildi"; "İşlem tamamlandı"; "Aktif · Beyana dayalı" | PASS | prv-ui-*.png |
| EXP-01 | Expiry | D6 | `grants.expires_on` backdated to yesterday | wait for sweep | removed, no notification | ~4 s: EXPIRED removal, REVOKED, mock gone, 0 notifications | PASS | exp1 |
| EXP-02 | End of day | D6 | `expires_on` = today (Istanbul) | wait ≥65 s | still active | ACTIVE | PASS | exp1 |
| EXP-03 | Change validation | D6, D4 | grant | earlier date; extend until-revoked; unrelated; other tool's level | refused | not_an_extension ×2; 404; level_of_another_tool | PASS | exp2, t6 |
| EXP-04 | Extension | D6 | grant | approve | only date changes | 2027-01-27 → 2027-02-27; no operation | PASS | exp2 |
| EXP-05 | Second change | A9 | open change | another | 409 | open_request_exists | PASS | exp2 |
| EXP-06 | Level change | D4 | Jira User | → Admin | new first, old removed after | Admin ACTIVE; User REVOKED "level change" | PASS | apr2 |
| EXP-07 | Approved after expiry date | D6 | `request_items.expires_on` backdated | final approval | cancelled | CANCELLED "expired before approval" | PASS | exp2 |
| EXP-08 | UI My Access | D6 | grant | open | expiry and extend | "27 Şub 2027", "Süre uzat" | PASS | exp-ui-myaccess.png |
| EXP-09 | UTC/Istanbul midnight gap | D6 | — | between 21:00 and 24:00 UTC | — | — | NOT RUN | real time only; covered by Go date tests |
| UI-01..07 | Navigation per role | O7 | 7 roles | sidebar | right menus | correct for all 7 | PASS | ui-nav-*.png |
| UI-08 | Direct URL guard | A4 | employee | /employees, /settings, /direct, /exceptions, /tools-admin | refused | "Bu sayfaya erişiminiz yok" | PASS | ui-guard-deniz-employees.png |
| UI-09 | 390 px | — | 4 roles | 16 pages | no overflow; menu button | 0 px overflow; labelled menu button | PASS | ui-390-*.png |
| UI-10 | 1366 px accessibility | — | 4 roles | labels, button names, alt | all met | 51/51 checks | PASS | ui7.out |
| UI-11 | Keyboard | — | — | Tab on login; combobox; Escape | usable | focus visible; Enter selects; Escape closes | PASS | ui-focus-login.png |
| UI-12 | Empty state | — | auditor | Taleplerim | empty message | "Bu görünümde talep yok" | PASS | ui-empty-requests.png |
| UI-13 | Server errors in Turkish | A7 | — | 6 error codes | Turkish | all Turkish, except the raw operation error (BUG-A-01) | PASS | PNGs |

## Part B — lifecycle, succession, reviews, orphans, audit, RBAC, operations (8093)

| ID | Feature | Ref | Preconditions | Steps | Expected | Actual | Status | Evidence |
|---|---|---|---|---|---|---|---|---|
| OPS-01 | Stack health | A2, D12 | qa3 up | compose ps; GET /health | healthy; only app published | app healthy on 8093; no host port for mocktools/postgres; 200 | PASS | compose ps |
| OPS-02 | Startup log | D12, A4 | fresh start | logs | migrations, demo warning, seed once | as expected; restart: no reseed | PASS | logs |
| OPS-03 | Seed matches README | README, D10, D12 | fresh seed | SQL | 18 employees, 6 tools, suspended + terminated, leftover account, orphans, running review | all present | PASS | SQL |
| OPS-04 | Demo accounts | README, D12 | — | GET /demo; log in all 7 | 7 accounts, no passwords | as expected; all 7 sign in | PASS | API |
| OPS-05 | Sign-in page | README | — | open / | accounts, no passwords, demo badge | as expected | PASS | ops-02-login.png |
| OPS-06 | Swagger read-only | A7 | — | /swagger/ | no Try-it-out, no validator, no external calls | supportedSubmitMethods [], validatorUrl null; data: URIs only | PASS | ops-01-swagger.png |
| OPS-07 | OpenAPI document | A7 | — | GET openapi.json | describes API | 87 paths, read-only | PASS | API |
| OPS-08 | Operating summary and rollout vs behaviour | CASE deliverables 2–3 | — | compare | match | match (day 21 = finding; SA revoke-only; D2 wording) | PASS | docs |
| OPS-09 | README recovery text | D2 note | — | README recovery section | HR nominates; nominee accepts 24 h link | old "InfoSec authorizes" text | FAIL | BUG-B-05 |
| OPS-10 | api.md status codes | api.md | — | nominate a terminated employee | doc = actual | doc 422, actual 400 | FAIL | BUG-B-06 |
| OPS-11 | Restart recovery | A6, O8 | campaign at day 21 while app stopped (started_at backdated) | stop, backdate, start | sweep catches up | "access review steps run changes:4", 2 findings, closed | PASS | logs, SQL |
| RBAC-01 | GET matrix, 20 endpoints × 7 roles | O7, A4 | sessions | GET each | per docs | as documented | PASS | rbac.mjs |
| RBAC-02 | Lifecycle writes | D7 | — | suspend/terminate/schedule/rehire/transfer per role | HR and SA only | HR/SA 409 (version reached); others 403 | PASS | rbac.mjs |
| RBAC-03 | Role assignment | D2 | — | POST roles per role | SA only | SA 204; others 403 | PASS | rbac.mjs |
| RBAC-04 | Campaign start | D8 | — | per role | SA only | SA 409 campaign_active; others 403 | PASS | rbac.mjs |
| RBAC-05 | Orphan actions | D10 | — | dismiss per role | InfoSec only | InfoSec 409 version; others 403 | PASS | rbac.mjs |
| RBAC-06 | Imports | D10 | — | mock import per role | SA only | SA 201; others 403 | PASS | rbac.mjs |
| RBAC-07 | Grant revoke | O6, D5, D1 | grant | per role | employee, manager, SA; others 404 | as expected | PASS | rbac.mjs |
| RBAC-08 | Operation retry | O3 | failed op | per role | SA or tool admin group | as expected | PASS | rbac.mjs |
| RBAC-09 | Manager audit scope | A4 | manager | /audit/events, /access, /requests | only current reports | exactly his 4 reports | PASS | scope.mjs |
| RBAC-10 | Employee UI guards | O7 | employee | nav; /exceptions, /employees | no admin nav; no-access page | as expected | PASS | output |
| RBAC-11 | /audit for non-auditors | O7 | employee without reports | open /audit | no-access page | page renders (API 403, no data) | FAIL | BUG-B-03; rbac-01-employee-audit.png |
| RBAC-12 | Unauthenticated | A4 | none | GET /employees | 401 | 401 | PASS | curl |
| RBAC-13 | CSRF | A4 | session, no header | POST /reviews/campaigns | 403 | 403 csrf_failed | PASS | curl |
| RBAC-14 | Cross-origin | A4 | — | cross-site login | 403 | 403 cross_origin_request | PASS | curl |
| RBAC-15 | Activation-link reveal | F4 | — | InfoSec reveal; HR /activations | 403 | 403, 403 | PASS | API |
| RBAC-16 | Auditor read-only | D11 | — | revoke, decide review, dismiss orphan | refused | 404, 403, 403 | PASS | API |
| RBAC-17 | SoD self actions | D7, D8 | — | HR suspends self; employee decides own item; SA nominates self | refused | 403 ×3 | PASS | API |
| RBAC-18 | HR on InfoSec holder | D7 | — | HR suspends InfoSec | 403 | privileged_reset_requires_super_admin | PASS | API |
| RBAC-19 | Owner without lifecycle rights | D7 | — | owner suspends/reactivates | 403 | 403 | PASS | API |
| LCY-01 | Suspend, API grant | D7 | Salesforce ACTIVE | HR suspend | SUSPENSION_REQUESTED → SUSPENDED/VERIFIED | as expected; mock SUSPENDED | PASS | t-lcy1 |
| LCY-02 | Suspend validation | D7 | — | empty reason; twice | 400; 409 | reason_required; employee_status | PASS | API |
| LCY-03 | Sessions end on suspension | D7 | signed in | HR suspends | 401 | /me 401; login 401 | PASS | t-lcy2 |
| LCY-04 | Manual grant suspended only once confirmed | D7 | Manual grant | suspend; view; IT Ops confirms | pending until confirmed | "Askıya alma bekleniyor" → "Askıda · Beyana dayalı" | PASS | lcy-02/03 PNGs |
| LCY-05 | Reactivate API + Manual | D7 | suspended | reactivate; confirm RESUME | ACTIVE; login works | as expected | PASS | t-lcy4 |
| LCY-06 | Suspension history | D7, A9 | — | intervals; point-in-time during suspension | kept | interval VERIFIED; PIT correct | PASS | SQL |
| LCY-07 | Reactivate permissions | D7 | — | owner reactivates | 403 | 403 | PASS | API |
| LCY-08 | Audit for suspend/reactivate | D7 | — | audit chain | full trail | all 6 events | PASS | SQL |
| LCY-09 | Transfer pre-selection UI | D5 | 2 grants | open Transfer | all "Kaldır" | as expected | PASS | lcy-05 PNG |
| LCY-10 | Keep needs justification | D5 | — | short text | error | "Korumak için en az 10 karakterlik gerekçe yazın." | PASS | t-lcy5 |
| LCY-11 | Transfer result | D5 | — | keep one, revoke one | as chosen; notified; audit | as expected | PASS | lcy-06 PNG, SQL |
| LCY-12 | Transfer API negatives | D5 | — | blank keep; same team; suspended; PUT with manager change | 400; 400; 409; 409 | as expected | PASS | t-lcy6 |
| LCY-13 | Job-title-only change | D5 | — | new title | audit only | EMPLOYEE_UPDATED; no operations | PASS | SQL |
| LCY-14 | Immediate termination UI | D7, CASE §3 | API + Manual grants | İşten çıkış → Hemen | session ends; priority removals | as expected; GitHub pending | PASS | lcy-07 PNG |
| LCY-15 | Leaver's pending removals on HR page | D7 | after LCY-14 | open the employee page | open removal listed or warning | "Açık erişimler (0)" | FAIL | BUG-B-01 |
| LCY-16 | Leaver removal in Exceptions | D10 note | — | InfoSec Exceptions | listed | listed, "Sırada" | PASS | orp-01 PNG |
| LCY-17 | Removal failed → retry → confirmed | O3, D7 | Manual removal | claim; fail; retry; confirm | states shown; cleared | as expected | PASS | lcy-08 PNG |
| LCY-18 | Scheduled termination validation/cancel | D7 | — | past date; schedule; cancel; cancel again | 400; 200; 400; 200; 409 | as expected; audited | PASS | t-lcy10 |
| LCY-19 | Scheduled termination by sweep | D7, A6 | +20 s | wait | terminated by SYSTEM | 28 s later; scheduledBy recorded | PASS | SQL |
| LCY-20 | Immediate overrides schedule | D7 | scheduled +1 d | terminate | 200, schedule cleared | as expected | PASS | API |
| LCY-21 | Concurrent terminations | D7 | — | 2 parallel | one wins | 409 vs 200; 1 event | PASS | t-lcy10 |
| LCY-22 | Rehire UI | D7, A5, F4 | terminated | rehire | ACTIVE, nothing restored, link only in outbox | as expected | PASS | lcy-09 PNG |
| LCY-23 | Rehire negative | D7 | active | rehire | 409 | employee_status | PASS | API |
| LCY-24 | Rehire keeps leaver removal open | D7 | open removal | rehire | still listed | as expected | PASS | API |
| LCY-25 | Rehired activation | F4 | — | reveal; activate; login | works | 204; 200 | PASS | API |
| LCY-26 | Immediate termination while scheduled | D7 | scheduled | open page | İşten çıkış available; waiting explained | only "Planlı çıkışı iptal et"; overdue wait unexplained | PARTIAL | BUG-B-04 |
| SUC-01 | Promotion two-person rule | D2 | nomination | proposer, auditor, HR authorize; stale | refused | 403 ×3; 409 | PASS | t-suc1 |
| SUC-02 | Concurrent authorize | D2 | — | 2 parallel InfoSec | one wins | 200 / 409; role granted | PASS | t-suc1 |
| SUC-03 | Nomination negatives | D2 | 2 SAs | HR succession; existing SA; self | 403; 409; 403 | as expected | PASS | t-suc1 |
| SUC-04 | HR terminates a non-last SA | D2 note | 2 SAs | terminate one | 200 | 200; warning true | PASS | t-suc2 |
| SUC-05 | Last SA: termination blocked | D2, D7 | last SA | terminate | 409 | last_super_admin | PASS | t-suc2 |
| SUC-06 | Last SA: scheduling blocked | D2 | — | schedule | 409 | as expected | PASS | t-suc2 |
| SUC-07 | Last SA: due schedule waits | D2 note | schedule set while 2 SAs | wait 110 s past due | not terminated | still ACTIVE | PASS | t-suc2 |
| SUC-08 | Last SA UI message | D2 | — | İşten çıkış | exact message; button disabled | as expected | PASS | suc-01 PNG |
| SUC-09 | HR emergency suspend | D2 note | — | suspend admin (UI) | sessions end; recovery recorded; notified | as expected | PASS | SQL |
| SUC-10 | Recovery banner | D2 | — | HR home | banner | shown | PASS | suc-02 PNG |
| SUC-11 | HR cannot reactivate the SA | D2 | — | API | 403; 409 | as expected | PASS | t-suc3 |
| SUC-12 | HR cannot nominate self/inactive | D2 | — | API + UI | refused, not listed | as expected | PASS | t-suc3 |
| SUC-13 | HR nominates; status only | D2 | — | UI | status, no link | as expected; no token in API | PASS | suc-03 PNG |
| SUC-14 | One succession at a time | D2 | open nomination | nominate another | 409 | nomination_pending | PASS | API |
| SUC-15 | Link only in nominee's outbox | D2 | — | HR outbox; HR/InfoSec reveal | none; 404 | as expected | PASS | API |
| SUC-16 | Token never stored/audited in clear | D2 | — | search | 0 | 0 | PASS | SQL |
| SUC-17 | Wrong account refused | D2 | other user | open link, accept | refused, audited | as expected | PASS | suc-04 PNG |
| SUC-18 | Reissue invalidates old link | D2 | — | nominee reissue; stale; HR reissue; old link | 403; 409; 200; refused | as expected | PASS | suc-05/06 PNGs |
| SUC-19 | Malformed token | D2 | — | accept "abc" | 400 | invalid_role_link | PASS | API |
| SUC-20 | Concurrent accept | D2 | — | 2 parallel | one wins | 200 / 400; 1 role row | PASS | t-suc4 |
| SUC-21 | Re-login after accept | D2 | — | /me; login | 401; then SA | as expected | PASS | t-suc4 |
| SUC-22 | Termination after acceptance | D2 | new SA | schedule +15 s | terminated | as expected | PASS | SQL |
| SUC-23 | Succession audit trail | D2 | — | audit query | all steps, no token | as expected | PASS | SQL |
| SUC-24 | Proposer no longer HR | D2 | HR role removed | accept | 400 | invalid_role_link, proposer_not_hr | PASS | t-suc6 |
| SUC-25 | Suspending nominee cancels | D2 | — | suspend nominee | CANCELLED; ciphertext cleared | cancelled; ciphertext kept | PARTIAL | BUG-B-07 |
| REV-01 | Review page wording | D8 note | campaign | owner page | new wording, Day 0/7/14/21 | as expected | PASS | rev-01 PNG |
| REV-02 | SA may only revoke | D8 | — | SA KEEP; UI | 403; no Keep | as expected | PASS | rev-14 PNG |
| REV-03 | Revoke needs reason | D8 | — | API; UI | 400; dialog stays | as expected | PASS | rev-10 PNG |
| REV-04 | Decision validation | D8 | — | invalid; stale; repeat | 400; 409; 409 | as expected | PASS | t-rev2 |
| REV-05 | Reviewer authorization | D8 | — | own item; auditor; InfoSec early; other owner | refused | as expected | PASS | t-rev2/5 |
| REV-06 | Individual keep | D8 | item | KEEP with reason | REVIEW_KEPT | as expected | PASS | SQL |
| REV-07 | Bulk keep skips flagged/InfoSec | D8 | — | API; UI checkboxes | skip | as expected | PASS | t-rev8 |
| REV-08 | Bulk keep positive | D8 | — | UI | kept | as expected; bulk=true | PASS | t-rev8 |
| REV-09 | Terminated employee keep refused | D8 note | terminated | API; bulk; UI | 409; skipped; no button | as expected | PASS | rev-03 PNG |
| REV-10 | Earlier keep does not stop removal | D8 note | kept item | terminate | removed; keep is history | as expected | PASS | rev-03 PNG |
| REV-11 | Removal states on review page | D8 note | — | pending; fail; retry+confirm | Kaldırma bekleniyor → başarısız → Kaldırıldı | exactly that; Kaldırıldı only after confirm | PASS | rev-11/12/13 PNGs |
| REV-12 | Day 7 reminder | D8 | `started_at` backdated 7d+5m | sweep | one reminder per item | 3 reminders; no duplicates after restart | PASS | SQL |
| REV-13 | Day 14 escalation | D8 | backdated 14d | sweep | InfoSec notified | as expected | PASS | SQL |
| REV-14 | Day 21 findings, no auto-revoke | D8 note | backdated 21d, restart | start app | findings; access unchanged | as expected; no REVOKED_NO_REVIEW | PASS | SQL, logs |
| REV-15 | Leaver item at day 21 | D8 | removal pending | day 21 | no finding; closes as already removed | as expected | PASS | SQL |
| REV-16 | Findings in Exceptions | D8 | — | InfoSec | listed with reviewer and age | as expected | PASS | rev-05 PNG |
| REV-17 | Findings filter label | D8, O7 | — | filter tabs | matching group | counted under "Onay" | FAIL | BUG-B-02 |
| REV-18 | Late decisions | D8 | findings | SA KEEP; InfoSec late KEEP; SA late REVOKE | 403; late; late + removal | as expected | PASS | t-rev5 |
| REV-19 | Concurrent decisions | D8 | finding | owner KEEP ∥ InfoSec REVOKE | one wins | as expected | PASS | SQL |
| REV-20 | Finding auto-closes | D8 | finding | SA direct revoke | ALREADY_REMOVED | as expected | PASS | SQL |
| REV-21 | Manual start in Settings | D8 | no campaign | Erişim denetimi başlat | started; 2nd blocked | as expected; no auto-removal wording | PASS | rev-08 PNG |
| REV-22 | Snapshot content | D8 | — | items | flags, InfoSec snapshot | as expected | PASS | t-rev7 |
| REV-23 | Catch-up of all steps | D8, A6 | backdated 21d at once | sweep | all steps in one round | as expected | PASS | SQL |
| REV-24 | Open findings not re-added | D8 | findings | start next campaign | not re-added; shown | as expected | PASS | rev-16 PNG |
| REV-25 | Evidence report + CSV | D8, CASE §5 | closed campaign | Kanıt raporları → CSV | counts, timeline, late flag | as expected | PASS | rev-07 PNG, CSV |
| REV-26 | Carried-over findings in later evidence | D8 | open findings from earlier campaign | open quarter evidence | listed and counted | 0, missing | FAIL | BUG-B-08 |
| ORP-01 | Mock import idempotent | D10 | imported | repeat | nothing re-imported | 0 imported, 6 skipped | PASS | API |
| ORP-02 | Orphan labels | D10 note | — | InfoSec UI | company vs external, source | as expected | PASS | orp-01 PNG |
| ORP-03 | Direct revoke of orphan refused | D10 note | — | SA; InfoSec | 409; 404 | as expected | PASS | t-orp2 |
| ORP-04 | Orphan actions InfoSec-only | D10 | — | SA revoke | 403 | 403 | PASS | t-orp2 |
| ORP-05 | Dismiss validation | D10 | — | blank; stale | 400; 409 | as expected | PASS | t-orp2 |
| ORP-06 | Dismiss with reason | D10 | — | dismiss; again | closed, account kept; 409 | as expected | PASS | SQL |
| ORP-07 | Orphan revoke workflow | D10 | — | InfoSec revokes | REVOKED in tool | as expected | PASS | SQL |
| ORP-08 | Failed op retry | O3 | FAILED | stale; IT Ops retry | 409; back to queue | as expected | PASS | API |
| ORP-09 | Failed task needs reason | O4 | — | empty reason | 400 | reason_required | PASS | API |
| ORP-10 | CSV import | D10 | — | mixed CSV | terminated rows removed at once; orphan; 3 rejected | as expected | PASS | SQL |
| ORP-11 | CSV re-import | D10 | — | same file | skipped | 0 imported, 3 skipped | PASS | API |
| ORP-12 | Unroutable steps listed | O7, F2 | — | — | — | not constructed here (covered by APR-11) | NOT RUN | — |
| AUD-01 | Event log | CASE §5 | auditor | Denetim kayıtları | read-only, events | as expected | PASS | aud-01 PNG |
| AUD-02 | Search + CSV | CASE §5 | — | search "Deniz", CSV | filtered CSV with BOM | 32 events, 33 lines | PASS | aud-events.csv |
| AUD-03 | Filters (API) | api.md | — | 6 filters | correct | as expected | PASS | API |
| AUD-04 | Append-only | A9 | — | UPDATE/DELETE as superuser; DELETE/TRUNCATE as app | blocked | trigger blocks; app role denied | PASS | psql |
| AUD-05 | Access at any date | O1, CASE §5 | — | PIT before/after termination; 29 Oct | correct states and approvers | as expected; bad `at` 400 | PASS | API |
| AUD-06 | Who approved it | CASE §5 | — | /audit/requests?q= | request, steps, timeline | as expected | PASS | API |
| AUD-07 | Privileged roles report | D11 | — | now; date before install | holders; empty | as expected | PASS | API |
| AUD-08 | Overrides, policy, leavers | D1, D10 | — | GET each | rows | as expected | PASS | API |
| AUD-09 | DB matches UI and audit | — | throughout | cross-check | consistent | consistent | PASS | above |
| — | Login rate limit (B) | A4 | — | — | — | covered by AUTH-24 | NOT RUN | shared budget |
| — | 24 h role/activation link expiry (B) | D2, F4 | — | — | — | covered by AUTH-20 and Go test `TestRoleLinkExpiresAndIsReplacedOnReissue` | NOT RUN | real time only |
| — | Owner's own access to InfoSec pool (D8) | D8 | — | — | — | not in seed; covered by Go tests | NOT RUN | — |

## Re-verification of the fixes (fresh stack `accessflow-final`, port 8094)

The stack was built from the final tree with empty volumes, then each fixed bug was repeated in headless Chrome (CDP) with SQL checks. Evidence: scratch directory `audit/retest/`.

| ID | Bug | Steps | Actual | Status | Evidence |
|---|---|---|---|---|---|
| RT-B01 | BUG-B-01 | HR terminates Deniz (Jira API + GitHub Manual) in the UI; open her page. Open Transfer for Elif, who has a grant being removed | "Açık erişimler (1)": GitHub · "Kaldırma talep edildi"; red callout "Kaldırılması tamamlanmamış erişimler var". Elif's transfer lists only the open Figma grant | PASS | b01*.png, b01.out, b01t.out |
| RT-B02 | BUG-B-02 | Campaign backdated 22 days; sweep opens 4 findings; InfoSec opens İstisnalar | Filter "Onay ve denetim (4)" lists the 4 findings | PASS | b02-infosec-flow-filter.png |
| RT-B03 | BUG-B-03 | /audit by URL as an employee without reports, a manager and the Auditor | Employee: only "Bu sayfaya erişiminiz yok" (a MutationObserver saw no flash of the page); manager and Auditor: "Denetim kayıtları" | PASS | b03a/b/c-*.png |
| RT-B04 | BUG-B-04 | HR schedules Zeynep's exit, then İşten çıkış → Hemen. Admin's `termination_at` set 5 min in the past via SQL | Both buttons shown; Zeynep TERMINATED. Overdue text shown exactly; admin still ACTIVE 7 min after the due time | PASS | b04-*.png |
| RT-B07 | BUG-B-07 | HR (API) suspends admin, nominates Hakan, suspends Hakan | Nomination CANCELLED and `sealed_token` NULL in the same second | PASS | b07.out |
| RT-B08 | BUG-B-08 | SA starts a new campaign; Auditor opens Çeyreklik kanıt paketi and the CSV | Tile "Önceki denetimlerden açık bulgu" = 4; CSV has 4 rows "Önceki denetimden devreden bulgu" + 1 new item | PASS | b08-*.png, b08-quarter.csv |
| RT-A01 | BUG-A-01 | mocktools stopped; two API operations fail after 5 attempts (`next_attempt_at` fast-forwarded); SA opens İstisnalar; mocktools restarted, retried | "Araç çağrısı başarısız oldu · 5 deneme"; raw error only inside the closed "Teknik ayrıntı"; both retries SUCCEEDED | PASS | a01-exceptions-*.png |
| RT-A02 | BUG-A-02 | HR moves Tuna under Emre (InfoSec and AWS owner); SA direct-grants AWS Console/ReadOnly in the UI | DIRECT_GRANT notifications: Emre 1, Selin 1 | PASS | a02-*.png, a02.out |
| RT-DR | RV-2 | SA, Doğrudan erişim → Erişimi kaldır, for employees with REVOCATION_FAILED and REVOCATION_REQUESTED grants | No "Kaldır" for either; open grants still offered | PASS | dr-*.png |

No console exceptions and no 5xx responses. One observation, not confirmed as a defect: İstisnalar sometimes showed its loading skeleton for 2–3 s (`GET /exceptions` 105–2,019 ms) while the full `-race` test suite was loading the same host.

## Final fix round: browser checks (2026-10-31)

These seven checks cover only the workflows changed in the final round; they do not replace the cases above.

Headless Chrome over CDP against the isolated `afqa` stack (scale data loaded, see `performance-audit.md` §9). Expected totals were read from the API at run time.

| Case | Result |
|---|---|
| Reviews: the Tool Owner pages through 6,000+ own items (counter, "Sonraki", page 2), then the findings view | PASS |
| Tasks: a group member pages through 2,503 queued tasks | PASS |
| Team: a manager pages through 3,004 reports and opens one report's access drawer | PASS |
| Exceptions: the Auditor sees "N açık inceleme bulgusunun ilk 100 tanesi" and follows the link to `/reviews?view=findings` | PASS |
| Mailbox (R-1): the Super Admin who reset the InfoSec account clicks "Bağlantıyı göster" and is refused with the independence message | PASS |
| Succession (R-2): HR opens the last Super Admin's termination, sends a nomination "Yetkilendirmeye gönder", sees "yetkilendirme bekleniyor"; no role link exists; the Super Admin sees "(İK ataması)" in Approvals | PASS |
| Swagger served in demo mode | PASS |

The first two script runs failed on the script's side (stale hard-coded totals; a suspended nominee picked from the seed); the script was corrected and the app was not changed. Screenshots are in the session scratch directory (`e2e/`). Not covered in the browser: the BUG-A-03 party callout (API and Vitest only), and the R-3 hint on the request form (Vitest only).

## Auditor restriction: live checks (2026-10-31)

DECISIONS.md O7 note 2026-10-31 makes the Auditor an external auditor who reads only the audit events. *(Amended later the same day: the Auditor also reads point-in-time access and approval evidence; see "Auditor evidence access: live checks" at the end of this file and [current rules](../../../DECISIONS.md#current-rules-at-a-glance).)* Earlier rows in this file that show an Auditor using the directory, point-in-time access, evidence reports, Exceptions or reviews (EMP-05, PRV-09, UI-12, RBAC-16, AUD-01, RT-B03, RT-B08, the Exceptions browser check) were correct under the earlier rule and are kept as **historical** evidence; they no longer describe current behaviour.

Isolated throwaway stack `afqa3` (port 8093, fresh volumes, fresh demo seed, removed afterwards). API checks with each demo role's own session (`roles.py`); browser checks in headless Chrome over CDP (`e2e-auditor.mjs`).

| ID | Case | Expected | Actual | Status |
|---|---|---|---|---|
| AUD-R-01 | Auditor: `GET /audit/events`, `?page=2`, `/audit/filters`, `/audit/events/export`, `/auth/me` | 200 | 200 ×5 | PASS |
| AUD-R-02 | Auditor: CSV export row count vs total | every event | rows = total | PASS |
| AUD-R-03 | Auditor: 44 business endpoints (27 GET, 17 POST: requests, on-behalf, directory, team, approvals incl. override/reassign, grants, tools, roles, groups, tasks, Exceptions, reviews, outbox, notifications, HR/admin actions, imports, point-in-time, evidence, five reports) | 403 | 403 ×44 | PASS |
| AUD-R-04 | Manager (burak.sahin) given AUDITOR by the Super Admin: team, approvals, own access; audit events; role removed again | 403 ×3, events 200 company-wide, access restored after removal | as expected | PASS |
| AUD-R-05 | Browser, Auditor: menu, landing page, paging, tool filter (Jira) | only "Denetim kayıtları"; `/` → `/audit`; no tabs, no request button or bell; totals match the API | as expected | PASS |
| AUD-R-06 | Browser, Auditor: typed URLs /access, /requests, /employees, /team, /approvals, /exceptions, /reviews, /settings, /mailbox, /request-new | "Bu sayfaya erişiminiz yok" | as expected ×10 | PASS |
| AUD-R-07 | Auditor sign-out, then events | 204, then 401 | 204, 401 | PASS |
| AUD-R-08 | Other six roles keep their access (employee, manager, owner, InfoSec, HR, Super Admin; 20 endpoints); employee still refused the audit events; browser: employee menu unchanged, InfoSec audit page keeps point-in-time and reports | 200 / 403 as before | as expected | PASS |

One first-run failure was a script error, not a defect: the list contained `GET /employees/{id}`, which is not a route (the router answers 405 for every caller); removed and re-run, 23 of 23 API checks pass, browser 4 of 4.

## Auditor evidence access: live checks (2026-10-31)

DECISIONS.md O7 note "the Auditor also reads point-in-time access and approval evidence" (2026-10-31) adds `GET /audit/access` and `GET /audit/requests` to the Auditor's allowlist; the evidence reports stay refused. AUD-R-05 ("no tabs") is superseded: the Auditor now sees three tabs.

Isolated throwaway stack `afaudit` (port 8099, fresh volumes, fresh demo seed, removed afterwards with its volumes); the demo on port 8080 was not touched (baseline before and after: 25 employees, migration 19, password hashes unchanged). API checks with the demo accounts' own sessions; browser checks in headless Chrome over CDP.

| ID | Case | Expected | Actual | Status |
|---|---|---|---|---|
| AUD-E-01 | Auditor: `GET /audit/events`, `…/export`, `/audit/filters`, `/audit/access`, `/audit/requests` | 200 | 200 ×5 | PASS |
| AUD-E-02 | Auditor: the five `/audit/reports/*`, and `/employees`, `/tools`, `/exceptions`, `/reviews`, `/approvals`, `/requests`, `/access`, `/email-outbox`, `/imports` | 403 forbidden | 403 ×14 | PASS |
| AUD-E-03 | Auditor: submit a request, approve, direct grant, revoke, create and update an employee, start a campaign | 403 forbidden | 403 ×7 | PASS |
| AUD-E-04 | CASE §5: point-in-time at a grant's activation instant, filtered by its tool (GitHub) | the holder with approvers; same answer as InfoSec | Deniz Kara, Developer; Yönetici: Burak Şahin, Araç sahibi: Can Öztürk; identical to InfoSec's response | PASS |
| AUD-E-05 | Approval evidence for that grant | each decided step names its decider | MANAGER Burak Şahin, TOOL_OWNER Can Öztürk (`decidedByName`) | PASS |
| AUD-E-06 | Deniz Kara gives up Jira (API, removal verified), then the Auditor asks about the instant before | gone now, listed as removed; still held at the past instant with its approver | removed now (VERIFIED); held at the past instant, Yönetici: Burak Şahin | PASS |
| AUD-E-07 | Browser, Auditor: Denetim kayıtları | menu "Denetim kayıtları" only; tabs Olay geçmişi, Geçmiş erişim sorgusu, Onay kanıtı; approvers shown; Araç and Çalışan filters filled; approval chain names deciders; `?tab=reports` falls back to the event log | as expected; API calls only `/auth/me`, `/demo`, `/audit/filters`, `/audit/events`, `/audit/access`, `/audit/requests` | PASS |

Two first-run failures were script errors, not defects: `GET /grants/mine` and `POST /review-campaigns` are not routes (404 for every caller). Replaced with `/access` and `/reviews/campaigns` and re-run: 30 of 30. A first try of AUD-E-06 used GitHub, a Manual tool: its removal stays `REVOCATION_REQUESTED` (still access, O1) until an admin confirms it, which is correct; the check was repeated with Jira (API).
