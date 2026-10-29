# AccessFlow: First 30 Days

## Where we start

- People ask for access in Slack or by messaging an admin; each of the ~60 tools has its own admin who adds people by hand.
- Nothing is written down: not who asked, who approved, or why.
- Last month's audit found 14 people who had left but still had accounts. There is no proof of a quarterly review.

## Goal for day 30

Every new access request goes through AccessFlow; every existing account is recorded and either owned by an employee or being removed; the first certification campaign is running; Audit can answer "who had access to what on a date, and who approved it" from AccessFlow.

## The order, and why it cannot change

Each step needs the one before it, and AccessFlow enforces this: an employee row is rejected if its department, job position or manager is not defined yet, and an access row is rejected if its employee, tool or level is not.

1. Secure deployment; Super Admin, InfoSec and HR administration.
2. Departments and job positions defined.
3. Tools registered with owners, admin groups, access levels and approval steps.
4. Active employees imported from CSV.
5. Their existing tool access imported from a second CSV.
6. Rejected rows and mismatches reconciled.
7. First certification campaign.
8. Pilot, then cutover of all new requests.
9. Monitoring of operations, exceptions and audit evidence.

**Prerequisite:** steps 2 and 4 need M17 (department and job-position catalogs, employee CSV onboarding). M17 is planned and **not implemented yet**; the existing-access import (step 5) and everything else here exist today.

## Week 1: Secure setup and the company catalog (steps 1–2)

| Step | Owner |
|---|---|
| Deploy with demo mode off (no `ACCESSFLOW_DEMO`, `ACCESSFLOW_ENV`, `DEMO_PASSWORDS`). Behind an HTTPS reverse proxy (AccessFlow serves plain HTTP on 8080) with `COOKIE_SECURE=true`. | IT / platform |
| Secrets: a `JWT_SECRET` of at least 32 bytes and new database passwords from the company secret store, set before the database volume is first created. | IT / platform |
| Backups: daily PostgreSQL backups; one restore tested into a separate environment before go-live. | IT / platform |
| One `app` instance; `COMPANY_TIMEZONE` set to the company's zone. | IT / platform |
| The first Super Admin signs in with the one-time password, assigns at least two InfoSec members, the HR Managers and the Auditor, and nominates a second Super Admin (a different InfoSec member authorizes it, D2). | Super Admin, InfoSec |
| HR defines the departments and job positions with the company's exact names (for example "Senior Backend Developer"). They grant nothing; they drive starter packages and must exist before employees are imported. | HR Manager |
| Start registering tools (Week 2 finishes it): the regulated, high-risk tools first. | Super Admin, InfoSec |

**Risks:** an HTTP-only deployment leaks session cookies; default database passwords; an untested backup.

## Week 2: Tools, employees, existing access, reconcile (steps 3–6)

| Step | Owner |
|---|---|
| Register every tool: owner, admin group, levels and each level's approval steps (Manager; Tool Owner and InfoSec optional). Real tools are **Manual** or **Vendor** for now (no real connectors); the method cannot change after creation. Policy changes notify InfoSec (F2). | Super Admin, Tool Owners, InfoSec |
| HR exports active employees and imports them from CSV (`eposta;ad_soyad;departman;unvan;yonetici_eposta`): preview first, then import. A row with an unknown department or position (spelled differently from the catalog) is rejected with its line and the defined value; HR fixes the file or defines the entry, then retries. No tool access and no passwords are created. *(M17, planned)* | HR Manager |
| Each tool admin exports the tool's accounts; the Super Admin imports them (`eposta;arac;seviye;hesap`, up to ~1,000 rows per file). Each becomes baseline access: recorded as already held, never provisioned again, approver "unknown: existed before AccessFlow" (D10). Rows with an unknown tool or level are rejected with their line. | Tool admins, Super Admin |
| Reconcile: fix and re-import rejected rows (both imports are repeatable); check every orphan account (an account matching no employee) before deciding it. | HR, tool admins, InfoSec |
| Configure starter packages per department and position (F3). | Super Admin, managers |

**The 14 former employees.** Finding and closing their real accounts in each tool is an **IT/InfoSec operational task**, not an AccessFlow feature. AccessFlow helps: their accounts appear in the access import as orphan findings (or, if HR records them as terminated, as removals started at once); InfoSec revokes each, which creates a removal task for the tool's admin group, and the confirmation is recorded.

**Risks:** e-mails in tools that do not match corporate e-mails create false orphans; shared or service accounts that would break something if removed (dismiss with a reason and record an owner outside AccessFlow).

## Week 3: Certify and pilot (steps 7–8)

| Step | Owner |
|---|---|
| When the imports are reconciled (target: day 12), a Super Admin starts the **certification campaign**. Day 7 reminds, day 14 escalates to InfoSec, day 21 (about day 33) revokes unanswered InfoSec-level items and turns the rest into findings (D8, D10). | Super Admin |
| HR hands out one-time passwords per department on a planned day, with the existing reset (shown once, valid 72 hours). | HR Manager |
| **Pilot:** one or two departments request all new access through AccessFlow; their tools' admins work only from AccessFlow tasks. Brief managers, tool owners and admin groups (about 30 minutes each). | InfoSec lead, pilot managers |
| Daily during the pilot: InfoSec checks Exceptions (failed tasks, unroutable steps, orphans, stale grants). | InfoSec |

**Risks:** approvers absent (no delegation; InfoSec reassigns stuck steps, D9); admins still granting from Slack.

## Week 4: Cutover and audit readiness (steps 8–9)

| Step | Owner |
|---|---|
| Cutover: all departments request through AccessFlow; the Slack access channel becomes read-only with a link. Tool admins grant only from AccessFlow tasks. | CISO / InfoSec lead |
| HR records every transfer and leaver in AccessFlow on the day it happens; future leavers are scheduled (D5, D7). | HR Manager |
| Monitoring: Exceptions daily; review progress twice a week; a re-import of a few tools to catch access granted outside AccessFlow. | InfoSec, tool admins |
| Privileged roles: InfoSec and the Auditor review the Privileged roles report (a manual review outside the software, D11). | InfoSec, Auditor |
| Audit walkthrough: point-in-time access for a chosen date, approvers of a sensitive grant, and the leavers, overrides, policy and quarter reports. | Auditor, InfoSec |

## Day 30: done means

- **Setup:** HTTPS with secure cookies, secrets from the store, a tested restore, two active Super Admins and two InfoSec members (no health warning).
- **Catalog:** every department and job position in use is defined; every tool in scope has an owner, an admin group, levels and approval steps, and tools not yet registered have a date.
- **Employees:** every active employee is imported with the right department, position and manager (spot check: 20 people against HR's list).
- **Existing access:** every registered tool's accounts are imported; no rejected row and no orphan is left undecided.
- **The 14 former employees:** IT/InfoSec confirms their accounts are closed in every tool, and each removal is confirmed in AccessFlow.
- **Certification:** the campaign is running with at least 80% of items decided.
- **Requests:** since cutover, no access was granted outside AccessFlow (a re-import of 5 tools shows no new orphans or unrecorded grants).
- **Audit:** the Auditor answers "who had access to X on date D, and who approved it" without help.

## Known limits during the rollout

- Manual and Vendor evidence is self-reported: AccessFlow records who confirmed and when, not the tool's state.
- Imported access shows "approver unknown" until it is certified.
- A manually started campaign delays the next quarterly one until it closes.
