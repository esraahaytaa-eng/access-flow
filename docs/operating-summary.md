# AccessFlow: Operating Summary

## How It Runs Each Week

**Automatic:** two background loops run inside the app; all other work starts with a person's action.
- The **operation runner** sends approved API changes to the tool and verifies each by reading it back; a failure is retried 4 times (30 s to 4 min apart), then listed in Exceptions. Leavers' removals go first.
- The **sweeps** (about once a minute, catching up after downtime) remove expired access, run scheduled terminations and take the quarterly review's steps.

**People:**

| Who | Does |
|---|---|
| Employee | Requests a tool and level with a reason and duration; follows My Requests and My Access; may give access up |
| Manager | Decides Manager steps in the Approval Inbox; requests and removes access for reports |
| Tool Owner | Decides Tool Owner steps; reviews the tool's access each quarter |
| Admin group | Works Manual and Vendor tasks: claims, records the vendor ticket, confirms or reports failure |
| InfoSec | Decides InfoSec steps; reassigns stuck steps; checks Exceptions daily (failures, orphans, findings) |
| HR Manager | Records joiners, movers and leavers on the day; keeps the department and position catalog |
| Super Admin | Configures tools, levels and steps; assigns roles; imports existing access; may override a step or grant directly (both reported) |
| Auditor (O7) | External and read-only: audit events (filter, CSV export), point-in-time access, approval evidence. No actions, other screens or evidence reports; holds no duty, and the role overrides any other |

## Key Operating Rules

- **Approvals (F2, D9):** each level sets its steps: Manager always, then Tool Owner and InfoSec if required, each by a different person; nobody approves, fulfils or reviews their own access. A blocked step is re-routed, never skipped; there is no delegation.
- **On behalf (D3):** only a manager above the beneficiary, an HR Manager or a Super Admin may request, extend or change access for someone else. A direct manager's submission counts as the Manager step, unless they are an HR Manager or Super Admin.
- **Duration and levels (D4, D6, D1):** one level per tool per person; a level change grants the new level before removing the old. Every item is Temporary (removed from 00:00 after its end date) or Until Revoked; extensions are re-approved. Direct grants last at most 90 days, never for oneself.
- **Movers and leavers (D5, D7):** a transfer pre-selects all access for removal; keeping any needs a justification. Termination removes everything first; suspension is reversible; a rehire restores nothing.
- **Super Admins (D2, D7):** the first is bootstrapped once with a one-time password. Promotion needs two people: a Super Admin nominates, another Super Admin (or, with none, InfoSec) authorizes. The last active Super Admin cannot be terminated (`409 last_super_admin`; a scheduled termination waits), but HR may suspend them at once as an emergency block: that leaves no active Super Admin, shows the recovery warning and notifies HR and InfoSec. In succession (at most one Super Admin not terminated), HR nominates, an independent InfoSec member or Super Admin authorizes, and the nominee accepts in their own account; then the termination can go through. A server command is the last resort, only while no Super Admin is active.
- **Quarterly reviews (D8):** tool owners keep or remove each grant. Day 7 reminds, day 14 escalates to InfoSec, and on day 21 every unanswered item becomes a security finding. **Access is never removed automatically.**
- **Fulfillment and evidence (O1, A9):** API results are verified against the tool; Manual and Vendor results are self-reported admin confirmations. Every change is written with its audit event; the audit log is append-only.
- **Existing access (D10):** imported with "approver unknown", then certified in the first review; accounts matching no employee are orphan findings.

Detailed business rules, exceptions, security decisions, architecture trade-offs and their rationale are documented in [DECISIONS.md](../DECISIONS.md).

## Assumptions and Boundaries

- **Implemented:** everything above, plus the onboarding workbook, account imports, Exceptions and the audit explorer.
- **Simulated:** API tools reach a local Mock Tool Service, not real GitHub or AWS. E-mail goes to a local Mock Email Outbox and is never sent; a Super Admin passes activation links on, and each reveal is audited.
- **Done by people:** hosting, HTTPS, secrets and backups; current HR data; exporting tool accounts; closing accounts in external tools, including the 14 former employees'. AccessFlow tracks those removals but cannot see into the tools.
- **Assumed:** HR is the source of truth for employees; tool accounts match corporate e-mails; every tool has an owner and an admin group; one timezone and one `app` instance.
- **Future:** real tool adapters, real e-mail, SSO and MFA.
