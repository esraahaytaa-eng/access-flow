# M13 + M14 — Audit Explorer and Super Admin succession

Branch `feat/audit-recovery`, one PR. Rules: CASE §5, O1, O7 (Audit Explorer access), D1, D2, D11, A4, A9. Design: `design/` §4.17.1, §4.17.6, §4.18, Approvals nominations.

## Dependencies

- M13 only reads what earlier milestones record: grant intervals and evidence, operations, approval steps, role and group intervals, suspensions, review campaigns, audit events. No new write paths.
- M14 adds the promotion and recovery writes; its events (`SUPER_ADMIN_NOMINATED`, `…_AUTHORIZED`, `ADMIN_RECOVERED`) appear in M13's explorer and privileged-roles report without extra work, so M13 goes first.

## M13 — Audit Explorer (read-only)

New package `internal/explorer` (read-only, imports no domain package; `requests` imports its scope helpers for the approval-evidence endpoint, which avoids a test import cycle).

- **Scope (one function, every query):** Super Admin, InfoSec, Auditor → company-wide; a Manager → their *current* direct and indirect reports (recursive on `manager_id`), events and grants of anyone else excluded, rows with no subject excluded; anyone else → `403`.
- `GET /audit/events` — filters: action group, actor, from/to (company-timezone dates), text; newest first; at most 1000 rows with `truncated`.
- `GET /audit/access?date=&employee=&tool=` — point-in-time at the end of that day: grants effective then (`effective_from`, or the import time for imported baseline; until `effective_to`), with the O1 state at that instant (active, suspended, revocation requested, revocation failed), evidence (`VERIFIED`, `SELF_REPORTED`, `IMPORTED`), approvers, plus removals in the 30 days before.
- `GET /audit/requests?q=` — approval evidence: request items in scope with their steps (reuses the request model and the UI timeline).
- Reports, company-wide scope only: `GET /audit/reports/privileged?date=` (roles and admin groups held on that date, since when, granted by), `/overrides` (overrides, direct grants and direct revokes), `/policy` (approval-step changes), `/leavers` (terminated employees' grants and removal evidence), `/quarter` (latest campaign: counts, day steps, carried-over findings).
- Exceptions completeness: every O7 kind is already listed (failed ops, security revocations, overlaps, stale grants, orphans, unroutable steps, review findings); checked, no change.
- **UI:** "Denetim kayıtları" page with tabs Olay geçmişi · Geçmiş erişim sorgusu · Onay kanıtı · Kanıt raporları (full scope), CSV downloads built in the browser from the loaded rows; Turkish labels for our audit actions; Manager and Auditor callouts.

## M14 — Super Admin succession and settings

In `internal/people` (roles live there); `recover-admin` in `auth` + `cmd`.

- **Promotion (D2):** a Super Admin nominates an active non-Super-Admin; one pending nomination per nominee; expires after 7 days (checked when read or decided, no new job: O8). Authorizer ≠ nominee and ≠ proposer; a Super Admin when another active one exists, otherwise an InfoSec member. Authorize adds `SUPER_ADMIN` (roles and groups kept); decline closes it.
- **Recovery:** the last-Super-Admin guard on suspend/terminate/schedule is lifted. With no active Super Admin the system is in `ADMIN_RECOVERY_REQUIRED` (derived, not stored). Then an HR Manager nominates (self allowed) and a different InfoSec member authorizes. HR Managers and InfoSec are notified when recovery starts. Access workflows keep running.
- Nominations are cancelled when the nominee or proposer is terminated. Decisions take one advisory lock, so concurrent authorizations stay consistent.
- `accessflow recover-admin <email>`: only when no active Super Admin exists; makes that active employee a Super Admin (or creates one), writes a one-time password to the bootstrap file, audits `ADMIN_RECOVERED`.
- `GET /admin/health` — active Super Admins, InfoSec members, recovery state; warning below two.
- **UI:** Settings → Roller: "Süper Yönetici öner", pending nominations, the two-Super-Admin warning; Settings → Sistem tab; nominations to authorize in the Approval Inbox (with nav count); a recovery banner, and the HR nomination on the Employees page in recovery mode.

## Tests (targeted, TDD)

- M13: point-in-time at the activation and removal instants; verified vs self-reported vs imported labels; requested and failed removals still held; privileged roles at a past date; a Manager sees current reports' full history but not former reports or others; Auditor company-wide and read-only; an employee without a role gets `403`.
- M14: nominee and proposer cannot authorize; InfoSec authorizes only when no other Super Admin; expired nominations cannot be decided; concurrent authorizations consistent; last Super Admin termination enters recovery and HR + InfoSec recover; `recover-admin` refused while a Super Admin is active; access workflows run in recovery mode.
- Frontend Vitest for the audit tabs, nominations and recovery; one Docker journey near the PR boundary; full suite in CI.
