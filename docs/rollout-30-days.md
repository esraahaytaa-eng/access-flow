# AccessFlow: First 30 Days

## Where we start

- People ask for access in Slack or by messaging an admin; each of the ~60 tools has its own admin who adds people by hand.
- Nothing is written down: not who asked, who approved, or why.
- Last month's audit found 14 people who had left but still had accounts. There is no proof of a quarterly review.

## Goal for day 30

Every new access request goes through AccessFlow; every existing account is recorded and either owned by an employee or being removed; the first certification campaign is running; Audit can answer "who had access to what on a date, and who approved it" from AccessFlow.

## The order, and why it cannot change

Each step needs the one before it, and AccessFlow enforces this: a workbook row is rejected if its department, job position, manager, tool owner or admin group is not defined, and an access row is rejected if its employee, tool or level is not in AccessFlow or in the same workbook.

1. Secure deployment; Super Admin, InfoSec and HR administration.
2. Departments, job positions and admin groups defined.
3. The onboarding workbook template downloaded.
4. Employees, tools and existing access imported together from that one workbook.
5. Admin group members added.
6. Reconcile: the API tool read verifies reported access; rejected rows, warnings and orphans are resolved.
7. First certification campaign.
8. Pilot, then cutover of all new requests.
9. Monitoring of operations, exceptions and audit evidence.

## Week 1: Secure setup and the company catalog (steps 1–2)

| Step | Owner |
|---|---|
| Deploy with demo mode off (no `ACCESSFLOW_DEMO`, `ACCESSFLOW_ENV`, `DEMO_PASSWORDS`). Behind an HTTPS reverse proxy (AccessFlow serves plain HTTP on 8080) with `COOKIE_SECURE=true`. | IT / platform |
| Secrets: a `JWT_SECRET` of at least 32 bytes and new database passwords from the company secret store, set before the database volume is first created. | IT / platform |
| Backups: daily PostgreSQL backups; one restore tested into a separate environment before go-live. | IT / platform |
| One `app` instance; `COMPANY_TIMEZONE` set to the company's zone. | IT / platform |
| The first Super Admin signs in with the one-time password, assigns at least two InfoSec members, the HR Managers and the Auditor, and nominates a second Super Admin (a different InfoSec member authorizes it, D2). | Super Admin, InfoSec |
| HR defines the departments and job positions with the company's exact names (Çalışanlar → Departmanlar ve unvanlar, for example "Senior Backend Developer"). They grant nothing; they drive starter packages. | HR Manager |
| The Super Admin creates one admin group per team that fulfils tools today (for example "IT Operations", "Vendor Desk"). Members come later (step 5): they must be imported employees. | Super Admin |

**Risks:** an HTTP-only deployment leaks session cookies; default database passwords; an untested backup.

## Week 2: One workbook, then reconcile (steps 3–6)

| Step | Owner |
|---|---|
| The Super Admin downloads the template (Yönetim ayarları → İçe aktarma → Excel şablonunu indir). It has three sheets — **Employees**, **Tools**, **Existing Access** — and dropdowns with the catalog names, admin groups, fulfillment methods and approval steps. | Super Admin |
| HR fills **Employees** from its active-employee list (email, name, department, position, manager email). Each tool's owner, with InfoSec, fills **Tools**: one row per tool and access level, with the fulfillment method, owner email, admin group and approval steps. Each tool admin pastes the tool's accounts into **Existing Access** (employee email, tool, level, account). | HR, Tool Owners, InfoSec, tool admins |
| The Super Admin previews the file, fixes every listed problem (sheet, row, column, value and the defined value) and imports. The import is all or nothing: one problem stores nothing. It creates no passwords (each employee gets an activation link), no roles, admin group members, requests, approvals or tool operations. Existing employees and tools are never changed; differences are shown as warnings. InfoSec gets one notification for the new approval policies; every created record is audited individually. | Super Admin |
| Add each admin group's members (Yönetim ayarları → Yönetim grupları). Manual and Vendor removal tasks created by the import wait in the group's queue until then. | Super Admin |
| **Reconcile.** Run the API tool read (Araç servisinden içe aktar): it verifies the reported API access it finds ("Araçtan doğrulandı"), records access the workbook missed, and counts reported API access the tool does not hold. Check every orphan account (an account matching no employee) and every unverified grant before deciding it. Manual and Vendor access stays "reported": there is no tool to read. | Super Admin, InfoSec, tool admins |
| Configure starter packages per department and position (F3). | Super Admin, managers |

**The 14 former employees.** Finding and closing their real accounts in each tool is an **IT/InfoSec operational task**, not an AccessFlow feature. AccessFlow helps: an account whose e-mail matches no employee becomes an orphan finding from the tool read or a CSV account import (the workbook refuses unknown employees); an account of an employee recorded as terminated starts its removal at once. InfoSec revokes each orphan, which creates a removal task for the tool's admin group, and the confirmation is recorded.

**Risks:** e-mails in tools that do not match corporate e-mails create false orphans; shared or service accounts that would break something if removed (dismiss with a reason and record an owner outside AccessFlow); a wrong row in Existing Access for a Manual or Vendor tool is believed until certification, because no tool can verify it.

## Week 3: Certify and pilot (steps 7–8)

| Step | Owner |
|---|---|
| When the imports are reconciled (target: day 12), a Super Admin starts the **certification campaign**. Day 7 reminds, day 14 escalates to InfoSec, day 21 (about day 33) revokes unanswered InfoSec-level items and turns the rest into findings (D8, D10). | Super Admin |
| Employees set their own password through their activation link (valid 24 hours, single use), department by department on a planned day; the Super Admin follows the activation screen and sends new links to anyone pending or expired. With real e-mail (not part of this version) the links would go straight to the employees. | Super Admin, HR Manager |
| **Pilot:** one or two departments request all new access through AccessFlow; their tools' admins work only from AccessFlow tasks. Brief managers, tool owners and admin groups (about 30 minutes each). | InfoSec lead, pilot managers |
| Daily during the pilot: InfoSec checks Exceptions (failed tasks, unroutable steps, orphans, stale grants). | InfoSec |

**Risks:** approvers absent (no delegation; InfoSec reassigns stuck steps, D9); admins still granting from Slack.

## Week 4: Cutover and audit readiness (steps 8–9)

| Step | Owner |
|---|---|
| Cutover: all departments request through AccessFlow; the Slack access channel becomes read-only with a link. Tool admins grant only from AccessFlow tasks. | CISO / InfoSec lead |
| HR records every transfer and leaver in AccessFlow on the day it happens; future leavers are scheduled (D5, D7). | HR Manager |
| Monitoring: Exceptions daily; review progress twice a week; a tool read and a CSV account re-import of a few tools to catch access granted outside AccessFlow. | InfoSec, tool admins |
| Privileged roles: InfoSec and the Auditor review the Privileged roles report (a manual review outside the software, D11). | InfoSec, Auditor |
| Audit walkthrough: point-in-time access for a chosen date, approvers of a sensitive grant, and the leavers, overrides, policy and quarter reports. | Auditor, InfoSec |

## Day 30: done means

- **Setup:** HTTPS with secure cookies, secrets from the store, a tested restore, two active Super Admins and two InfoSec members (no health warning).
- **Catalog:** every department and job position in use is defined; every tool in scope has an owner, an admin group, levels and approval steps, and tools not yet registered have a date.
- **Employees:** every active employee is imported with the right department, position and manager (spot check: 20 people against HR's list).
- **Existing access:** every registered tool's accounts are imported; the API tool read has run after the workbook; no rejected row, unverified API grant or orphan is left undecided.
- **The 14 former employees:** IT/InfoSec confirms their accounts are closed in every tool, and each removal is confirmed in AccessFlow.
- **Certification:** the campaign is running with at least 80% of items decided.
- **Requests:** since cutover, no access was granted outside AccessFlow (a re-import of 5 tools shows no new orphans or unrecorded grants).
- **Audit:** the Auditor answers "who had access to X on date D, and who approved it" without help.

## Known limits during the rollout

- Manual and Vendor evidence is self-reported: AccessFlow records who confirmed and when, not the tool's state. The same holds for access reported in the workbook until a tool read verifies it (API tools only).
- Imported access shows "approver unknown" until it is certified.
- A manually started campaign delays the next quarterly one until it closes.
