# Known limitations

Every known limitation of the current version, with the decision it follows from where there is one. The most important ones are summarized in the [README](../README.md#important-limitations).

## Current version

- Database credentials are fixed local values in `compose.yaml` (a local case-study setup, DECISIONS.md A2). PostgreSQL applies them only when its volume is first created; to change them, remove the volume (`docker compose down -v`).
- There is no login rate limiting beyond the lockout (DECISIONS.md A4): 5 wrong passwords lock an account for 15 minutes, so anyone who knows an email can keep that account locked. Every attempt is audited.
- One `app` instance is assumed (A2). Initial Super Admin creation is safe across processes (advisory lock), but running several instances is not tested.
- `app` keeps the migration (owner) connection string in its environment for its whole lifetime, although it uses it only at startup. Separating it would need a one-shot migration service; with fixed local credentials it adds no protection here.
- A grant whose level or tool is retired after it reached the tool stays active until removed (retirement does not revoke held access).
- Manual and Vendor evidence is self-reported: AccessFlow records who confirmed and when (and the vendor ticket), but cannot check the tool. A task claimed once (or retried after a claim) is finished even if its level is retired meanwhile (safe fulfillment).
- Someone who holds two recipient roles (for example a manager who is also InfoSec) gets one notification per role for a direct grant or a Super Admin override.
- Expiry is checked once a minute, so access can last up to a minute past midnight after its expiry date, plus the time the removal takes (a Manual or Vendor removal waits for its task).
- In a narrow case a deadlock can occur. PostgreSQL then fails one of the two (a 500), and it can be repeated. The cases:
  - a removal of a grant, or a person-level action, racing with a brand-new request for that person;
  - the same, racing with an approval by that person on another item of the same beneficiary;
  - a person-level action racing with that person, or someone a step is re-routed to, acting on another employee's grant or record (a task, a retry, a revoke);
  - a reactivation racing with a tool change that makes the person its owner.
- While a level change's old removal has failed (an overlap), the new grant cannot be extended (`409 access_exists`: the old grant still counts as held) and a failed new grant blocks another change of the old one (`409 change_in_progress`) until an admin resolves the exception.
- A task released by a suspended or terminated claimer still shows that claimer until another member takes it over.
- Termination cancels a claimed Manual or Vendor grant task so the leaver's removal is never held up. A claimer who was already granting in the tool gets `409` on confirm and must check the tool and undo what they applied; AccessFlow cannot see that access (self-reported evidence).
- A transfer removes every grant HR did not keep, including one created after HR read the list, and any beyond the first 200 listed.
- A claimed Manual or Vendor grant task can still be confirmed after a transfer; the grant then counts as a stale grant until its removal task is confirmed.
- Replacing a departed manager means one transfer per report (revoke by default, or keep with justifications); a suspended report must be reactivated first. A transfer changes the person's own manager only, not their reports'.
- The deadlock cases above also apply to transfers.
- A resume in flight when a transfer removes a suspended grant briefly restores the access in the tool before the queued removal runs; it is not flagged as a stale grant (only a grant activating is).
- A former manager with a suspended report cannot be rehired until that report is reactivated and transferred; reactivating resumes the report's access in the tools until the transfer's removals run. A terminated former report keeps the rehired person as their recorded manager (history only).
- The rehire check looks at direct reports only: an active employee whose manager is a terminated direct report of the rehired person sends Manager steps to the rehired person as skip-level until HR transfers them.
- Re-routed Manager steps send no notification; the new manager sees them in the Approval Inbox.
- Person-level actions (suspend, reactivate, transfer, rehire, terminate, schedule) are serialized by one database lock; they are rare HR actions, so they simply wait for each other.
- An approved extension cannot be withdrawn (it is already in force); give the access up with revoke instead.
- Withdrawing an approved item sets its `closed_at` to the withdrawal time; the approval time stays in its steps and the audit log.
- The UI is Turkish only (DECISIONS.md A7). It loads the Geist font from Google Fonts; offline, the browser falls back to a system font.
- Saving a tool in the UI sends one call per change (the tool, then each level); if one fails, the earlier ones stay saved and the page shows the error.
- A review round (quarterly start, day steps) runs in one transaction; a campaign of many thousands of grants would hold it longer. Lists show the first 2000 review items and 500 orphans.
- A quarterly campaign does not start while a manual one runs; it starts when that one closes, later in the quarter.
- The CSV import is sent as one request body (at most 64 KiB, about a thousand rows); split larger files.
- A level the tool itself holds suspended for an active employee is imported as `ACTIVE`; AccessFlow does not resume it.
- The mock import reads the tool's accounts before its transaction; an account removed from the tool in that moment can still be imported as held, until a review or orphan action removes it.
- An import records what the tool really holds, so an employee may end up with two levels of one tool (D4 is enforced for requests, not for imported reality); a review or a level change resolves it.
- A dismissed orphan account stays `ACTIVE` with no owner and is not part of later review campaigns (D8 reviews employees' grants); the dismissal and its reason stay in the audit log.
- New system reasons or routing notes added in the backend need a Turkish text in `web/src/i18n/reasons.ts`; until then the UI shows the English text.
- The UI has no screens for lists the API does not offer yet: decided approvals, closed tasks and the direct-grant log.
- The audit explorer lists at most 1000 events per query (narrow the filters), 1000 rows in the override and policy reports, and the newest 100 requests; CSV export downloads the rows loaded in the browser. A manager's scope follows the current reporting lines (including terminated reports still linked), not past ones.
- Point-in-time state is derived from the audit log and suspension intervals; imported access counts as held from its import time with no known approver.
- Only a Super Admin may end a Super Admin's employment, so the last Super Admin leaves through a termination scheduled by another Super Admin, or by suspending or terminating each other. Recovery mode then needs an HR Manager and a different InfoSec member, or `recover-admin` on the server.
- `recover-admin` writes the one-time password to the same file as the first start; a still-unused first-start password in it is replaced.
- The Mock Tool Service has no authentication: it is reachable only on the internal Docker network (DECISIONS.md A2). It is a stand-in for real tools, not a model of their security.

## Planned changes

- **Employee CSV onboarding (M17, planned, not implemented yet).** Today HR creates employees one at a time in the UI. M17 adds company-defined department and job-position catalogs and a validated CSV import of active employees (with preview and audit), followed by the existing access import; see [`docs/plans/`](plans/).

## Out of scope

- Self-service account activation and real e-mail: HR issues one-time passwords (DECISIONS.md F4, O2).
- SSO.
- Real tool connectors: API tools are reached through the Mock Tool Service only (DECISIONS.md A1).
- Investigating past employee activity in external tools. AccessFlow answers who had access in AccessFlow on a date (point-in-time); finding and closing accounts held outside it is an IT/InfoSec task.
