# AccessFlow: First 30 Days

*A proposed operational rollout for a 400-person company with about 60 tools, not a completed deployment.* Below, **exists** means it works in this version (and in the demo), **proposed** means a step the organization carries out, and **future** means development not built yet. Business rules: [`DECISIONS.md`](../DECISIONS.md#current-rules-at-a-glance); setup commands: [`deployment.md`](deployment.md).

## Where we start

- People ask for access in Slack or by messaging an admin; each tool's admin adds people by hand.
- Nothing is written down: not who asked, who approved, or why.
- Last month's audit found 14 people who had left but still had accounts. There is no proof of a quarterly review.

## Goal for day 30

- Every new access request goes through AccessFlow.
- Every active employee is in AccessFlow.
- Every existing account in the registered tools is recorded: it either belongs to an active employee or is an open finding with an owner.
- The first certification campaign is running.
- The Auditor can answer "who had access to what on a date, and who approved it" from AccessFlow without help.

## Before day 1: prerequisites

- **Secure hosting** (proposed):
  - HTTPS in front of the app, with `COOKIE_SECURE=true`;
  - secrets from the company secret store;
  - daily PostgreSQL backups and one tested restore;
  - demo mode off.

  Details: [`deployment.md`](deployment.md#before-real-use).
- **Activation delivery** (future, with a manual workaround):
  - Employees activate their accounts through a one-time link. This version sends no e-mail.
  - Until real e-mail delivery is added, a Super Admin opens each link in the Mock Email Outbox and passes it to the employee through a trusted channel. Every reveal is audited.
  - That works for a pilot; for 400 people, real e-mail delivery should come first.
- **Tool connectors** (future):
  - The API method talks to the local Mock Tool Service; an optional, sandbox-only GitHub adapter manages Read on one private repository (off by default, [README](../README.md#optional-github-sandbox-adapter)).
  - Every production tool is registered as **Manual** or **Vendor** until a real adapter exists (see [API tools](#api-tools-today-and-in-production)).

## The first administrator (bootstrap)

This already exists; no new bootstrap feature is needed.

- **Every start:** database migrations run and create only the schema.
- **First start outside demo mode:** exactly one account is created, the initial Super Admin (`BOOTSTRAP_ADMIN_EMAIL`).
  - It gets a random one-time password, which is never logged.
  - The password is written to a file on the app's data volume, readable only with server access (`docker compose exec app /accessflow bootstrap-password`).
  - The first sign-in forces a password change; the file is then deleted.
- **Runs once:** the bootstrap runs only if no Super Admin has *ever* existed, so restarts change nothing.
- **Nothing else:** it creates no other employee, role, department or tool.
- **Demo data:** the demo accounts and sample data exist only in demo mode, which refuses to start outside the local setup.
- **Employees** come later from HR, through the onboarding workbook or one by one. Each gets an activation link and no password.
- **Recovery** if every Super Admin is lost: the HR-proposed, InfoSec-authorized succession, with the server-side `recover-admin` command as the last resort ([`deployment.md`](deployment.md#super-admin-recovery)).

**Operational notes:**
- Read the one-time password and sign in right after the first start: the password does not expire until it is changed.
- Use the first administrator's own corporate e-mail.
- The account is named "Initial Super Admin", and this version offers no way to rename an employee.

## Identity: active employees only, and the 14 leavers

- **HR supplies the current list of active employees.**
  - It is the source of employee identity for onboarding.
  - The workbook creates every listed employee as active; former employees are not listed and are not created.
- **Existing tool accounts are recorded separately.**
  - The workbook's Existing Access sheet takes only accounts of employees in AccessFlow or in the same workbook.
  - All other accounts are imported per tool: by a tool read (API tools) or a CSV account import (Manual and Vendor tools; Yönetim ayarları → İçe aktarma).
  - An account that matches no employee becomes an **orphan finding**.
- **An orphan is not presumed to be a leaver.** It may be a former employee, a service or shared account, or an e-mail that differs from the corporate one. InfoSec investigates each one, and both actions are audited:
  - *Revoke* starts the normal removal: a task for the tool's admin group, or the API operation.
  - *Dismiss* closes the finding with a reason, for example a service account with a named owner.
- **The 14 leavers are a legacy security problem, not 14 employees to create.**
  - Finding and closing their accounts in each tool is an **IT/InfoSec task**.
  - AccessFlow records the findings, who decided them and the removal evidence. For Manual and Vendor tools that evidence is the admin's own confirmation.
  - AccessFlow cannot close an account in a system it does not control.

## API tools: today and in production

**Today:** AccessFlow → operation runner (a background loop in the app) → Mock Tool Service.

**A possible production integration** (future; today only a sandbox adapter exists, managing Read on one private repository with the GitHub username entered on the request): AccessFlow → operation runner → **GitHub adapter** → GitHub REST API.

What the adapter would do:
- After the final approval, add the employee to the authorized GitHub organization or team, then read the membership back to verify it.
- On revocation, remove the membership and verify the removal.

What AccessFlow already does for every API operation:
- It retries, verifies and audits each operation, and lists failures in Exceptions.
- An adapter would plug in behind the existing `access.ToolClient` interface.

A real adapter needs:
- a least-privilege credential from the secret store;
- a mapping from the corporate e-mail to the GitHub user;
- handling of the provider's errors and rate limits;
- a periodic read to reconcile differences.

Until then, GitHub and every other tool use the Manual or Vendor process.

## Order that matters

AccessFlow enforces these dependencies:
- A workbook row is rejected if its department, job position, manager, tool owner or admin group is not defined.
- An access row is rejected if its employee, tool or level is unknown.
- An admin group member must be an existing employee.
- A review campaign covers only recorded grants.

Communication, training and champions have no such dependency; they can run in parallel.

## Week 1: Secure setup and catalog

| Step | Owner |
|---|---|
| Deploy with demo mode off, behind HTTPS, with secrets from the store; test a restore into a separate environment. | IT / platform |
| First sign-in with the one-time password; change it. Create the few people needed now (Çalışanlar → Yeni çalışan): HR Managers, at least two InfoSec members and the Auditor. Each activates through their link. Assign their roles. | Super Admin |
| Nominate a second Super Admin; an InfoSec member authorizes the nomination (the nominator never does, D2). Write down the recovery procedure and who may run the server command. | Super Admin, InfoSec |
| Define the departments and job positions with the company's exact names (Çalışanlar → Departmanlar ve unvanlar). They grant nothing; they drive starter packages. | HR Manager |
| Create one admin group per team that fulfils tools today (Yönetim ayarları → Yönetim grupları). List each tool's owner, admin group, levels and approval steps. | Super Admin, InfoSec |
| Name one **champion** per department (first contact for questions). Announce the change company-wide (see [Communication and training](#communication-and-training)). | CISO / InfoSec lead |

**Risks:**
- an HTTP-only deployment leaks session cookies;
- an untested backup;
- the Auditor account given another duty (AccessFlow refuses this, O7).

## Week 2: Import, reconcile, prepare

| Step | Owner |
|---|---|
| Download the workbook template (Yönetim ayarları → İçe aktarma → Excel şablonunu indir). HR fills **Employees** from the active-employee list. Tool owners, with InfoSec, fill **Tools** (method, owner, admin group, approval steps per level). Tool admins fill **Existing Access** for active employees. | HR, Tool Owners, InfoSec, tool admins |
| Preview and fix every listed problem, then import. The import is all or nothing and creates no passwords, roles, requests or tool operations. Existing employees and tools are never changed; differences show as warnings. | Super Admin |
| Add the admin group members. Until then nobody can work the group's Manual and Vendor tasks. | Super Admin |
| **Reconcile:** import each tool's account list (a CSV import for Manual and Vendor tools; the tool read for API tools). Assign every orphan to an InfoSec owner with a due date. Check the reported access before the review. | InfoSec, tool admins |
| Configure starter packages per department and position (F3). | Super Admin, managers |
| Train HR, the approvers (managers, tool owners, InfoSec) and the admin groups. | InfoSec lead |

**Risks:**
- tool e-mails that differ from corporate ones (false orphans);
- service accounts whose removal would break something (dismiss with a reason and a named owner);
- a wrong Existing Access row for a Manual or Vendor tool, believed until certification.

## Week 3: Pilot and first certification

| Step | Owner |
|---|---|
| **Pilot:** one or two departments activate their accounts and request all new access through AccessFlow; their tools' admins work only from AccessFlow tasks. Short employee training first. | Pilot managers, champions |
| Walk at least one request per method end to end: request → approvals → fulfillment (the admin's confirmation with a ticket reference, or a reported failure) → My Access → the audit records. Then one removal and one transfer. | InfoSec lead, pilot admins |
| Start the first access review (Erişim Kontrolü) once the imports are reconciled, at the latest by day 15. Day 7 reminds the reviewers, day 14 escalates to InfoSec, and day 21 turns every unanswered item into a security finding. Nothing is removed automatically (D8). | Super Admin |
| Daily during the pilot: InfoSec checks Exceptions (failed tasks, unroutable steps, orphans, stale grants). Collect feedback at the end of the week and fix process problems before cutover. | InfoSec, champions |

**Risks:**
- approvers absent: there is no delegation, so InfoSec reassigns stuck steps (D9);
- admins still granting from Slack.

## Week 4: Cutover and audit readiness

| Step | Owner |
|---|---|
| Train and activate the remaining departments. | Champions, Super Admin |
| **Cutover:** all new access requests go through AccessFlow. The Slack access channel becomes read-only with a link; admins grant only from AccessFlow tasks. | CISO / InfoSec lead |
| HR records every joiner, transfer, suspension and leaver in AccessFlow on the day it happens; future leavers are scheduled (D5, D7). | HR Manager |
| Monitor daily: pending approvals, failed and open tasks, Exceptions, review progress. Re-import a few tools' account lists (CSV or tool read) to catch access granted outside AccessFlow. | InfoSec, tool admins |
| Privileged roles: InfoSec reviews the Privileged roles report (a manual review outside the software, D11). | InfoSec |
| Support desk open for the first two weeks after cutover (see below). | InfoSec lead |
| **Audit walkthrough:** the Auditor works alone in Denetim kayıtları. Geçmiş erişim sorgusu shows who held a chosen tool on a past date, with its approvers. Onay kanıtı shows each request's approval chain. The event history is filtered and exported as CSV. InfoSec adds the evidence reports (quarterly package, privileged roles, overrides, policy changes, leavers), which the Auditor does not open. | Auditor, InfoSec |

## Communication and training

Short role-based sessions, recorded for anyone who misses them, with one quick-reference page per role (proposed; written by the rollout team).

| Audience | Format | Covers |
|---|---|---|
| Everyone | Announcement from the CISO (week 1), reminder before cutover | Why Slack requests stop: no record of who asked, approved or removed, and an audit finding; the cutover date; where to get help |
| Employees | 15 min, before their department starts | Activating the account and setting a password (the link is valid 24 hours, single use); requesting a tool and level, with a reason and a duration; following My Requests and My Access; giving access up |
| Managers | 30 min | Approving or rejecting with a reason; requesting for and removing access of reports; the team's access; transfers pre-select access for removal |
| Tool owners | 30 min | Each level's approval steps; Tool Owner approvals; the quarterly review of their tool's access |
| Admin groups | 45 min | Claiming Manual and Vendor tasks, recording the vendor ticket, confirming or reporting a failure; why the confirmation is evidence |
| HR | 45 min | Joiners, transfers, suspensions, leavers (on the day; scheduled for future dates); the department and position catalog |
| InfoSec | 60 min | Exceptions, orphan findings (revoke or dismiss), reassignment, review escalations and findings, the evidence reports |
| Auditor | 30 min orientation | Read-only Denetim kayıtları: past-date access with approvers, approval evidence, event filters and CSV export |

**Support:**
- One named contact in the InfoSec team, and a help channel for questions only (not requests), during weeks 3 to 6.
- Champions collect frequent questions into the quick-reference pages.

## If AccessFlow is down

AccessFlow has no emergency feature; this is a proposed procedure.
- **Emergency only:** a request that cannot wait goes to InfoSec. InfoSec records it in the incident ticket: the reason, the approver, the tool admin who acts and an end date.
- **No return to Slack approvals:** during the outage only InfoSec authorizes, and only emergencies.
- **Leavers:** IT disables the accounts directly; HR records the termination as soon as AccessFlow is back.
- **After restoration:**
  - Each temporary action is entered through the normal workflows: requested and approved with the incident reference as justification, or removed.
  - Re-importing the affected tools' account lists shows anything still unrecorded. Access found that way shows "approver unknown" until certified.
  - Nothing is reconciled automatically.

## Day 30: done means

- **Setup:**
  - HTTPS with secure cookies, secrets from the store, a tested restore;
  - two active Super Admins and two InfoSec members (no health warning);
  - the recovery procedure written down.
- **Employees:** every active employee is imported with the right department, position and manager (spot check: 20 people against HR's list). No former employee is imported.
- **Catalog:** every tool in scope has an owner, an admin group, levels and approval steps; tools not yet registered have a date.
- **Existing access:** every registered tool's accounts are imported. Every orphan and every rejected row has an InfoSec owner and is either decided or has a due date.
- **The 14 leavers:**
  - IT/InfoSec owns closing their accounts in each tool and reports which are confirmed closed.
  - Each removal tracked in AccessFlow has its evidence.
- **Training:**
  - every department is trained;
  - at least 90% of active employees have activated their accounts;
  - every approver and admin-group member is trained.
- **Pilot:** completed, with one end-to-end request per method and its audit trail checked.
- **Requests:** since cutover, all new access goes through AccessFlow (a re-import of 5 tools shows no new orphans or unrecorded grants).
- **Certification:**
  - the campaign is running with at least 80% of items decided;
  - each open item has a named reviewer;
  - escalated items have an InfoSec owner.
- **Audit:** the Auditor answers "who had access to X on date D, and who approved it" from AccessFlow alone, and exports the matching events.

## Known limits and open assumptions

- **Self-reported evidence:**
  - Manual and Vendor evidence is self-reported: AccessFlow records who confirmed and when, not the tool's state.
  - Workbook-reported access is believed until a tool read (API tools) or the review checks it.
- Imported access shows "approver unknown" until it is certified.
- A company-wide rollout needs real e-mail delivery for activation links (future); until then a Super Admin passes each link on by hand.
- There are no production tool adapters yet (only the sandbox GitHub adapter); every production tool starts as Manual or Vendor.
- A manually started campaign delays the next quarterly one until it closes.
- **Assumed:**
  - HR's list is complete and current;
  - tool accounts can be exported;
  - corporate e-mails identify employees across tools.
