# AccessFlow: First 30 Days

## Where we start

- People ask for access in Slack or by messaging an admin.
- Each of the ~60 tools has its own admin, who adds people by hand.
- Nothing is written down: not who asked, who approved, or why.
- Last month's audit found 14 people who had left but still had accounts.
- There is no proof of a quarterly access review.

## Goal for day 30

1. Every new access request goes through AccessFlow.
2. Every existing account is either owned by an employee or removed.
3. The 14 leavers' accounts are gone.
4. A first review has started and can be shown to Audit.

## Week 1: Deploy, harden, set up the people who run it

| Step | Owner |
|---|---|
| Deploy with demo mode off: remove `ACCESSFLOW_DEMO`, `ACCESSFLOW_ENV` and `DEMO_PASSWORDS` from `compose.yaml`. The app then creates only the initial Super Admin, with a one-time password read via `/accessflow bootstrap-password`. | IT / platform |
| Put the app behind an HTTPS reverse proxy. AccessFlow serves plain HTTP on port 8080 and does not include TLS. Set `COOKIE_SECURE=true` and a 32-byte or longer `JWT_SECRET` from the company's secret store. | IT / platform |
| Change the fixed database passwords in `compose.yaml` before the database volume is first created. PostgreSQL applies them only once. | IT / platform |
| Set up daily PostgreSQL backups of the `accessflow` database and run one test restore. AccessFlow has no backup feature. | IT / platform |
| Run one `app` instance only; several instances are not tested. Keep `COMPANY_TIMEZONE` at Europe/Istanbul unless the company uses another zone. | IT / platform |
| The first Super Admin assigns at least two InfoSec members, the HR Managers and the Auditor, then nominates a second Super Admin. Promotion needs two people, so an InfoSec member authorizes while only one Super Admin exists (D2). | Super Admin, InfoSec |
| HR creates all managers first, then everyone else, department by department. | HR Manager |

**Loading employees.** Bulk CSV onboarding of employees is out of scope. HR creates each of the ~400 employees in the UI. Each one gets a temporary password that is shown once and expires after 72 hours (F4). At about 2 minutes per person this is two or three days of HR time. Hand passwords over per department, on a planned day, so few of them expire unused; HR can reset expired ones. If this is too slow, a small CSV import of employees is a separate change and needs approval first.

**Risks:** an HTTP-only deployment leaks cookies; the database passwords are left at their defaults; expired temporary passwords cause a support load.

## Week 2: Register tools, import existing access, remove leavers, start the review

| Step | Owner |
|---|---|
| For each tool, name a Tool Owner and an admin group. Define its levels and each level's approval steps (Manager; Tool Owner and InfoSec are optional). Start with the regulated, high-risk tools such as production cloud, payments and customer data. Policy changes notify InfoSec (F2). | Super Admin with InfoSec, Tool Owners |
| Choose each tool's fulfillment method carefully, because it cannot be changed after the tool is created. AccessFlow's API method talks only to the Mock Tool Service; there are no real connectors. So in the first 30 days real tools are **Manual** (the tool's admins work tasks in AccessFlow) or **Vendor** (a tracked ticket to the vendor). An API connector for a real tool is a later engineering project. | Super Admin, tool admins |
| Each tool admin exports their tool's accounts. Import them with the CSV import (`eposta;arac;seviye;hesap`, at most about 1,000 rows per file). Rejected rows come back in the import report; fix them and import again. Imports are idempotent (D10). | Tool admins, Super Admin |
| Accounts that match no employee become orphan findings. This is where the 14 leavers appear. InfoSec revokes each one, or dismisses it with a reason; every revoke becomes a removal task. | InfoSec, admin groups |
| Once the imports are done, ideally by day 10, a Super Admin starts the **certification campaign** by hand. Its day 7 sends a reminder, day 14 escalates to InfoSec, and day 21 falls around day 31 (D8, D10). | Super Admin |
| Configure starter packages per department, so managers see what to request for new joiners (F3). | Super Admin, managers |

**Risks:** emails in tools do not match corporate emails, creating false orphans. Check every orphan before revoking it. A shared or service account may break something if removed; dismiss it with a reason and record an owner outside AccessFlow.

## Week 3: Switch requests to AccessFlow

| Step | Owner |
|---|---|
| Announce the cutover date. From then on, tool admins grant access only through AccessFlow fulfillment tasks. The Slack access channel becomes read-only, with a pinned link to AccessFlow. | CISO / InfoSec lead, managers |
| Brief each group in about 30 minutes: managers (Approval Inbox, requesting on behalf of reports), Tool Owners (approvals, reviews), admin groups (Fulfillment Tasks), HR (transfers, termination). | InfoSec, HR |
| HR enters every transfer and leaver in AccessFlow on the day it happens. Future leavers can be scheduled (D5, D7). | HR Manager |
| Every day, InfoSec checks Exceptions: failed tasks, leaver removals, orphans, unroutable steps. | InfoSec |

**Risks:** admins keep granting from Slack out of habit. Mitigation: a spot check of tool accounts against AccessFlow, and a re-import. Re-importing marks any new unrecorded account as a baseline grant or an orphan. Another risk is approvers who are absent. There is no delegation, so InfoSec reassigns stuck steps (D9).

## Week 4: Close gaps, walk Audit through it

| Step | Owner |
|---|---|
| Follow up on unanswered review items before day 14. Use Bulk Keep only where it is allowed; flagged and InfoSec-level items are decided one by one. | Tool Owners, InfoSec |
| Close every removal for the 14 leavers. Manual and Vendor removals count only once confirmed. | Admin groups, InfoSec |
| Review AccessFlow's own privileged roles (Super Admin, InfoSec, HR Manager, Auditor, admin groups) from the Privileged roles report. This is a manual review outside the software (D11). | InfoSec, Auditor |
| Audit walkthrough: point-in-time access for a chosen date, the approvers on a sensitive grant, the leavers report, the overrides and direct grants report, the policy-change report and the quarter report. | Auditor, InfoSec |
| Write down the open items: tools not yet onboarded, a candidate first API connector, and whether employee CSV onboarding is needed. | InfoSec lead |

## Day 30: done means

- All active employees exist in AccessFlow with the correct manager. Spot check: 20 random people against HR's list.
- At least the high-risk tools, and ideally all ~60, are registered with an owner, an admin group, levels and an imported baseline. Every tool that is not yet registered has a date.
- The 14 leavers have no account left in any tool. Each removal is confirmed in AccessFlow (`REVOKED`) and appears in the leavers report.
- No orphan account is left undecided. Each one is revoked or dismissed with a reason.
- Since cutover, no access was granted outside AccessFlow. A spot-check re-import of 5 tools shows no new orphans or unrecorded grants.
- The certification campaign is running and at least 80% of its items are decided. Its day-21 step will auto-revoke unanswered items whose level requires InfoSec.
- The Auditor can answer "who had access to X on date D, and who approved it" for any registered tool, without help.
- At least two active Super Admins and two InfoSec members exist (no health warning). Backups have been restored once.

## Known limits during the rollout

- Manual and Vendor evidence is self-reported. AccessFlow records who confirmed and when, but cannot check the tool.
- Imported access shows "approver unknown" until it is reviewed.
- Without real connectors, removals depend on admins working their task queues on time. Exceptions shows what is still open.
