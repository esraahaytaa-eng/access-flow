# AccessFlow: Operating Summary

## How it runs each week

**Automatic (O8, A6).** Nothing else runs on a timer.
- The **operation runner** sends approved API changes to the tool and verifies them with a status lookup. A failed call is retried 4 times (30 s to 4 min apart), then listed in Exceptions. Leavers' removals go first (O3, A6).
- The **sweeps** run about once a minute and catch up after downtime: expiry of temporary access at 00:00 after its expiry date (D6), scheduled terminations (D7), and the quarterly review's start and day-7, day-14 and day-21 steps (D8).

**People, in a normal week**
- **Employee:** requests access with a reason and a duration, follows it in My Requests / My Access, may give access up (O6).
- **Manager:** decides Manager steps in the Approval Inbox (one simulated email per new request, O2); requests for and revokes reports' access (D3, D5).
- **Tool Owner:** decides Tool Owner steps; reviews the tool's access each quarter.
- **InfoSec:** decides InfoSec steps, reassigns stuck ones (D9), works Exceptions.
- **HR Manager:** creates, transfers, suspends, terminates and rehires employees; keeps the department and job-position catalog (F3).
- **Admin groups:** work Manual and Vendor tasks: claim, record the vendor ticket, confirm or report failure.
- **Auditor:** an external, independent auditor: reads, filters (date, actor, action, tool, text) and exports the audit events, and nothing else; this role overrides any other the account holds, is given only to an account without operational duties, and an Auditor is given none (O7 note 2026-10-31). Point-in-time access and the evidence reports are run by InfoSec or a Super Admin.
- **Super Admin:** configures tools, levels and steps; assigns roles; may override a step or grant directly, both reported (D1); imports the onboarding workbook (D10); shares activation links from the Mock Email Outbox.

**Each quarter:** a review campaign of all active and suspended grants starts on the first day of the calendar quarter (D8).

## The rules it follows

- Access is a tool plus a level. Each level sets its steps: Manager (always), then Tool Owner and InfoSec (optional). A step added after submission is still required before provisioning (F1, F2).
- Each step needs a different person; nobody approves, fulfils or reviews their own access. Blocked steps are re-routed, never skipped or auto-approved (F2).
- A request by the beneficiary's direct manager counts as the Manager step (D3), unless that manager is an HR Manager or Super Admin: they can change reporting lines, so their Manager step goes to an independent approver. Requests on someone's behalf, including extensions and level changes, come from someone above them in the management chain, an HR Manager or a Super Admin (D3 notes).
- One level per person per tool. A level change grants the new level first, then removes the old one (D4).
- Every item is Temporary (with a date) or Until Revoked; extensions are re-approved (D6).
- Direct grants are at most 90 days, never for oneself (D1).
- Super Admin promotion (D2) has two paths. Routine: a Super Admin nominates and a different Super Admin (or, if none, an InfoSec member) authorizes. Succession, when at most one Super Admin has not left: an HR Manager nominates, an InfoSec member or Super Admin who is neither the proposer nor the nominee authorizes, and only then does the nominee get a 24-hour link and accept it, signed in as themself. Super Admins and InfoSec are notified.
- A link to a Super Admin's or InfoSec member's account is shown only to a Super Admin who did not issue it, and the other Super Admins are notified (F4).
- A transfer pre-selects all access for removal; keeping needs a justification (D5).
- Termination removes everything first; suspension is reversible; a rehire restores nothing (D7, A5).
- Reviews: reminder on day 7, InfoSec escalation on day 14; on day 21 every unanswered item becomes a security finding that InfoSec follows up in Exceptions; access is never removed automatically. A Super Admin may revoke, never keep (D8).
- No delegation; absent approvers' steps are reassigned (D9).
- Existing access is imported and certified; unmatched accounts are orphan findings. Access reported in the workbook or a CSV is labelled reported until the API tool read verifies it (D10).
- Departments and job positions come from a company catalog and grant nothing; a retired name stays on its holders (F3).
- API results are verified. Manual and Vendor results are labelled self-reported (Fulfillment methods, O1).
- The audit log is append-only, written with every change; nothing is deleted (A9, A5).
- Real system time only (D12).

## What we assumed

- One company timezone (default Europe/Istanbul) and one `app` instance (A6, A2).
- Each level's steps express its sensitivity; there are no sensitivity classes (F2).
- HR is the source of truth for joiners, movers and leavers (F4, D5, D7).
- Every tool has an owner and an admin group; tool accounts match corporate emails (D10).
- Notifications are in-app; the only email is simulated (O2).
- API tools are reached through a local Mock Tool Service (Fulfillment methods, A1).
- AccessFlow's own privileged roles get an evidence report, not a review campaign (D11).
- The UI is Turkish (A7); the demo sign-in labels and the demo catalog names are English.

## Current, planned, and operational

- **AccessFlow does today:** everything above, plus the company department and job-position catalog, and one Excel workbook that imports employees (without roles; each gets a 24-hour activation link to set their own password), tools with their levels and approval steps, and existing access together: previewed, all or nothing, audited per record, never overwriting existing records. Existing access also comes from the Mock Tool Service read or an account CSV. Imported access is baseline (no approval invented); unmatched accounts become orphans (D10).
- **Operational responsibilities (people, not the software):** secure hosting, HTTPS, secrets and backups; keeping HR data current; tool admins exporting accounts and working their tasks; IT/InfoSec investigating and closing accounts in external tools, including the 14 former employees' accounts. AccessFlow records and tracks these removals but cannot see into the tools.
- **Out of scope:** SSO, real e-mail (activation links go to a local Mock Email Outbox that a Super Admin reads), multi-factor authentication (planned), real tool connectors, and investigations of past activity in external tools.
