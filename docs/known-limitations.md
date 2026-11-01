# Known limitations

Every known limitation of the current version, with the decision it follows from where there is one. The material ones come first; the rest are grouped by topic, ending with rare edge cases documented for completeness. The README summarizes them in [Known Limitations](../README.md#known-limitations).

## Material limitations

The limits a reviewer should know first; each is explained in its section below.

- **No real integrations by default.** API tools are reached through the Mock Tool Service (the optional [GitHub sandbox adapter](#github-sandbox-adapter-optional) manages Read on one sandbox repository), Manual and Vendor results are self-reported, and e-mail goes to a local Mock Email Outbox where a Super Admin reveals activation links (audited). See [Integrations and evidence](#integrations-and-evidence) and [Setup and security](#setup-and-security).
- **A local case-study setup:** fixed database credentials in `compose.yaml`, plain HTTP, one `app` instance, no SSO and no MFA yet. See [Setup and security](#setup-and-security).
- **Separation of duties stops one person, not collusion,** and the session secret and the audit-insert right live with the application database. See the accepted security residuals under [Setup and security](#setup-and-security).
- **The external Auditor does not see the evidence reports** (quarter, privileged roles, overrides, policy, leavers); it reads the audit events, point-in-time access and approval evidence. See [Audit, point-in-time and the Auditor](#audit-point-in-time-and-the-auditor).
- **Workbook-reported access is believed as reported** until the API tool read or the first review checks it, and an import locks the employees it names until it commits. See [Onboarding workbook](#onboarding-workbook).
- **Some lists show their first rows** (with exact totals), and the audit text search reads every event. See [Lists and performance](#lists-and-performance) and [Audit, point-in-time and the Auditor](#audit-point-in-time-and-the-auditor).
- The UI is Turkish only.

## Details

### Setup and security

- Database credentials are fixed local values in `compose.yaml` (a local case-study setup, DECISIONS.md A2). PostgreSQL applies them only when its volume is first created; to change them, remove the volume (`docker compose down -v`).
- Sign-in is limited per account (5 wrong passwords lock it for 15 minutes) and per client address (60 attempts a minute by default, `LOGIN_RATE_LIMIT`, shared with activation links; locally all browsers share Docker's address, so this budget is shared too), DECISIONS.md A4. Anyone who knows an email can still keep that account locked. The address is the connection's own: behind a reverse proxy every client would share the proxy's address until a trusted-proxy setting is added, and Docker Desktop's port forwarding may show all clients as one address.
- **Mock Email Outbox (M18): a local stand-in for e-mail.** No e-mail is sent; a Super Admin reveals an employee's activation link and passes it on, so a Super Admin could use a pending link. Every reveal is audited, and `ACTIVATION_COMPLETED` names who revealed the link used. Nobody reveals their own link, and a Super Admin's or InfoSec member's link is revealed only by a Super Admin who did not issue it, with the other Super Admins notified (F4 note 2026-10-31); two Super Admins acting together could still take over such an account. Pending links are encrypted with a key on the `appdata` volume until used, replaced or expired (single use, 24 hours), so someone with that volume and the database could read them. Real e-mail delivery removes the reveal. If the key file is lost while links are pending, no link can be shown or issued until `docker compose exec app /accessflow reset-mail-key` (then `docker compose restart app`) and new links are sent.
- New passwords are checked against an offline list of common passwords, not against breached-password services (no external calls).
- The session signing secret, when generated, is stored in the database (A4); someone with full read access to the database could forge sessions.
- One `app` instance is assumed (A2). Initial Super Admin creation is safe across processes (advisory lock), but running several instances is not tested.
- `app` keeps the migration (owner) connection string in its environment for its whole lifetime, although it uses it only at startup. Separating it would need a one-shot migration service; with fixed local credentials it adds no protection here.
- The UI is Turkish only (DECISIONS.md A7), apart from the English demo sign-in labels; names in the company catalog are shown as defined. It loads the Inter font from Google Fonts; offline, the browser falls back to a system font.
- **Accepted security residuals** (details and history in `docs/qa/final/security-audit.md`):
  - Succession (D2) needs three people: an HR Manager, an independent InfoSec member or Super Admin, and the nominee. If all three collude they can create a Super Admin; Super Admins and InfoSec are notified at nomination and acceptance.
  - Two Super Admins together can issue and reveal an activation link of another privileged account (see the Mock Email Outbox above); the others are notified.
  - An HR Manager or Super Admin who moves an employee under themself sees that employee on their team; approval still comes from someone else (D3 note 2026-10-31).
  - The session signing secret is stored in the database; a database dump allows forging sessions.
  - The audit log is append-only, but the application's database role can insert rows, so a compromised application could add forged events.
  - The API documentation (`/swagger/`) is served only in the local demo setup, where it is readable without signing in (A7 note 2026-10-31).

### Integrations and evidence

- Manual and Vendor evidence is self-reported: AccessFlow records who confirmed and when (and the vendor ticket), but cannot check the tool. A task claimed once (or retried after a claim) is finished even if its level is retired meanwhile (safe fulfillment).
- The Mock Tool Service has no authentication: it is reachable only on the internal Docker network (DECISIONS.md A2). It is a stand-in for real tools, not a model of their security.
- A level the tool itself holds suspended for an active employee is imported as `ACTIVE`; AccessFlow does not resume it.
- The mock import reads the tool's accounts before its transaction; an account removed from the tool in that moment can still be imported as held, until a review or orphan action removes it.
- An import records what the tool really holds, so an employee may end up with two levels of one tool (D4 is enforced for requests, not for imported reality); a review or a level change resolves it.

### GitHub sandbox adapter (optional)

- Off by default and limited to Read on one private sandbox repository (DECISIONS.md A1 note 2026-11-01). The automated tests use a fake GitHub server. One live run on 2026-11-01 against `accessflow-sandbox/demo-project` with one test account verified the main path: request, Manager and Tool Owner approval, a Read invitation (`PROVISIONING`), acceptance detected by the runner (`ACTIVE / VERIFIED`, collaborator with `read`), and a Super Admin direct revoke (collaborator removed, `REVOKED / VERIFIED`). Suspension, resume, expiry, a declined invitation, renames and GitHub outages were not run live; only the automated tests cover them.
- **GitHub's own error text is not shown.** A failed operation keeps only GitHub's status code (`github status 422`) and the UI's Turkish explanation; GitHub's reply is in the server log only, so diagnosing an unusual refusal needs that log.
- **Audit details are thin for GitHub steps.** `INVITATION_PENDING` carries no details and `GRANT_ACTIVATED` / `GRANT_REVOKED` only the evidence and operation id: the invitation id, GitHub login and verified permission are in the `github_access` ledger and on the grant, reached through the operation, not in the audit event itself.
- **Ownership is AccessFlow's own record** (`github_access`). Two narrow races remain: a permission set by hand between the "no access yet" check and the invitation could be changed to Read by that invitation; and after AccessFlow cancels its invitation, Read found on the account is treated as that invitation accepted in the same moment and removed. A `read` invitation sent by hand to the same login while AccessFlow's own grant is still being invited is taken for AccessFlow's. Do not manage the sandbox's collaborators by hand while AccessFlow does.
- An invitation is checked every minute until it is accepted (one API call a page of pending invitations), well within GitHub's 5,000 requests an hour for a sandbox. One accepted while its employee is suspended gives Read until the next check, which removes it.
- **The GitHub account is entered on the request** and stored with GitHub's numeric user id (DECISIONS.md A1 amendment 2026-11-01). The lookup proves the account exists, not that the requester owns it: a mistyped login that belongs to someone else would send that person the invitation (approvers see the login; access starts only when the invitation is accepted). The account cannot be changed once submitted; another account needs a new request.
- **A renamed or deleted GitHub account stops the adapter.** Before each change it reads the user by id; on a rename, or a user that no longer exists, it changes nothing and the operation fails in Exceptions, including a removal, which then stays open while the renamed account may still hold Read. A person checks the repository and removes the access there; AccessFlow does not follow renames.
- Requesting the GitHub tool needs the adapter and GitHub reachable: otherwise the request is refused (`503 account_check_unavailable`, or `429 account_check_rate_limited` at GitHub's rate limit). A repeat of a submission that was already saved returns it without a lookup, so it does not need GitHub. The username lookups share the token's GitHub rate limit with the runner and are not rate-limited per user (once per level per submission); an employee submitting in a loop could delay GitHub operations until the hourly limit resets. A level change on the GitHub tool is refused by the adapter (one live record per account), and neither the D10 tool read nor the workbook or CSV imports cover a tool whose accounts are GitHub usernames.
- The start fails closed if GitHub is unreachable; while the adapter is on, GitHub outages show as retried, then failed, operations in Exceptions.

### Audit, point-in-time and the Auditor

- The audit events are paged on the server (100 a page, exact totals) and their CSV export streams every matching event; new events arriving while someone pages push older ones to later pages (a row may show twice, none is skipped). The text search reads every event (no text index): at 314,000 events about 0.45 s in the database and a median of 0.7–1.0 s through the API, with p95 up to 3.0 s under heavy host load; the other filters stay under about 0.2 s. The override and policy reports show at most 1000 rows and approval evidence the newest 100 requests; those CSV exports download the rows loaded in the browser. A manager's scope follows the current reporting lines (including terminated reports still linked), not past ones.
- **External Auditor (O7 notes 2026-10-31).** The Auditor reads the audit events, point-in-time access and approval evidence company-wide; the evidence reports are for InfoSec and Super Admins. Its point-in-time filters list the people who held or lost access on the chosen date (there is no directory for it), so a person can be picked only on a date they appear. An Auditor account holds no operational duty: the API refuses `AUDITOR` while the account has another role, group membership, owned tool, direct report, pending approval step, open review item or pending Super Admin nomination (`409 auditor_has_duties`, naming the duty), and refuses each of those duties for an Auditor (`409 employee_is_auditor`). Duties must be handed over before the role is given; nothing moves them automatically. Data written outside the API (SQL, fixtures) is not checked; there the role's precedence still keeps the account read-only. The Auditor sees no notifications or mock e-mails, and the role-change history is visible to it only as audit events (the Privileged roles report is for InfoSec and Super Admins). The audit CSV uses action codes and English column names, not the UI's Turkish labels.
- Point-in-time state is derived from the audit log and suspension intervals; imported access counts as held from its import time with no known approver.
- **The revocation reason is not in its audit event (all tools).** `GRANT_REVOCATION_REQUESTED` records who asked and how (`by`), but the reason text is stored on the grant (`closed_reason`), not in the event; the audit log alone does not show why access was removed.

### Lists and performance

- A review round (quarterly start, day steps) runs in one transaction. The day steps are single set-based statements (about 3 s for 12,000 items, without blocking sign-in). Starting a campaign inserts its items one by one: about 9 s for 12,000 grants, an accepted trade-off for a quarterly action.
- Lists: review items, open findings, the task queue and the team list are paged on the server (100 a page, exact totals, search over the whole list). The other Exceptions lists show their first rows, most urgent and oldest first (200 each; 500 orphans), with the exact total of each and a notice when rows are not shown; resolving a record makes room for the next. Grant lists (My Access, an employee's page, a team member's access) show 200 rows, every grant not yet ended first; older history beyond that is in the audit explorer.
- The CSV import is sent as one request body (at most 64 KiB, about a thousand rows); split larger files.

### Onboarding workbook

- **Onboarding workbook (M17).**
  - Access reported in the workbook (or a CSV) is believed as reported. For API tools the tool read verifies it later and counts what the tool does not hold; for Manual and Vendor tools nothing can verify it, so the certification campaign is the first check. A removal of a reported API grant the tool does not hold completes harmlessly (the tool answers "no change"); a suspension of it fails and appears in Exceptions.
  - For an API tool, Existing Access accepts only an empty account or the employee's email, because the tool read matches API accounts by email.
  - Existing Access has no Tool or Level dropdowns (a tool can be defined in the same file); the preview names the problem and the defined values.
  - Re-importing never updates existing employees, tools, levels or approval steps; differences are warnings, and changes go through the normal screens.
  - Departments, job positions and admin groups cannot be renamed; retire and create instead. The workbook only names admin groups; their members are added in the UI. Imported employees have no password until they use their activation link.
  - The file is at most 2 MiB with at most 1,000 employee, 2,000 tool-level and 5,000 access rows. Formulas, macros and encrypted files are refused. The import runs in one transaction and waits for, or blocks, the other access imports.
  - Unknown employees in Existing Access are rejected, not reported as orphans; orphan accounts come from the tool read or the account CSV.
  - An import locks every employee it names until it commits, so their sign-ins and approvals wait for it (4–20 ms per access row, depending on load); it is a rare rollout action.
  - The tool read's `unverified` count covers every active API tool; a tool created by a workbook import that commits while a read waits is counted although that read did not reach it. Run the read again.

### Rare workflow edge cases

- A grant whose level or tool is retired after it reached the tool stays active until removed (retirement does not revoke held access).
- Someone who is both a Super Admin and an InfoSec member gets succession notifications once per role. Direct grants and overrides notify each person once.
- Expiry is checked once a minute, so access can last up to a minute past midnight after its expiry date, plus the time the removal takes (a Manual or Vendor removal waits for its task).
- A few rare races take two locks in opposite orders, so PostgreSQL can abort one transaction as a deadlock: a submission or task claim racing a tool change that makes that person the tool's owner; a removal or person-level action (including a transfer) racing a new request or an approval for the same person; a person-level action racing that person acting on someone else's record. The aborted transaction wrote nothing and is retried, up to three attempts in all, so the action completes (`platform.InTx`, 2026-10-31). Only a transaction aborted three times in a row would answer `500`. The submission/owner case is reproduced in `TestSubmissionSurvivesADeadlockWithAToolOwnerChange`; the others use the same retry and were not reproduced one by one.
- While a level change's old removal has failed (an overlap), the new grant cannot be extended (`409 access_exists`: the old grant still counts as held) and a failed new grant blocks another change of the old one (`409 change_in_progress`) until an admin resolves the exception.
- A task released by a suspended or terminated claimer still shows that claimer until another member takes it over.
- Termination cancels a claimed Manual or Vendor grant task so the leaver's removal is never held up. A claimer who was already granting in the tool gets `409` on confirm and must check the tool and undo what they applied; AccessFlow cannot see that access (self-reported evidence).
- A transfer removes every grant HR did not keep, including one created after HR read the list. The transfer list shows up to 200 open grants (one level per tool keeps this far above real use); any beyond it would be removed, never kept.
- A claimed Manual or Vendor grant task can still be confirmed after a transfer; the grant then counts as a stale grant until its removal task is confirmed.
- Replacing a departed manager means one transfer per report (revoke by default, or keep with justifications); a suspended report must be reactivated first. A transfer changes the person's own manager only, not their reports'.
- A resume in flight when a transfer removes a suspended grant briefly restores the access in the tool before the queued removal runs; it is not flagged as a stale grant (only a grant activating is).
- A former manager with a suspended report cannot be rehired until that report is reactivated and transferred; reactivating resumes the report's access in the tools until the transfer's removals run. A terminated former report keeps the rehired person as their recorded manager (history only).
- The rehire check looks at direct reports only: an active employee whose manager is a terminated direct report of the rehired person sends Manager steps to the rehired person as skip-level until HR transfers them.
- Re-routed Manager steps send no notification; the new manager sees them in the Approval Inbox.
- Person-level actions (suspend, reactivate, transfer, rehire, terminate, schedule) are serialized by one database lock; they are rare HR actions, so they simply wait for each other.
- An approved extension cannot be withdrawn (it is already in force); give the access up with revoke instead.
- Withdrawing an approved item sets its `closed_at` to the withdrawal time; the approval time stays in its steps and the audit log.
- Saving a tool in the UI sends one call per change (the tool, then each level); if one fails, the earlier ones stay saved and the page shows the error.
- A quarterly campaign does not start while a manual one runs; it starts when that one closes, later in the quarter.
- A dismissed orphan account stays `ACTIVE` with no owner and is not part of later review campaigns (D8 reviews employees' grants); the dismissal and its reason stay in the audit log.
- New system reasons or routing notes added in the backend need a Turkish text in `web/src/i18n/reasons.ts`; until then the UI shows the English text.
- The UI has no screens for lists the API does not offer yet: decided approvals, closed tasks and the direct-grant log.
- The last active Super Admin cannot be terminated until a successor nominated by HR, independently authorized, has accepted the role (D2 notes 2026-10-30 and 2026-10-31); a due scheduled termination of the last one waits, and the employee page says why. An open succession can expire or be reissued, not cancelled. `recover-admin` on the server stays the last resort.
- `recover-admin` writes the one-time password to the same file as the first start; a still-unused first-start password in it is replaced.

## Planned changes

- Multi-factor authentication with an authenticator app, mandatory for privileged roles (Super Admin, InfoSec) first. The user menu shows it as "Planlanıyor"; nothing is implemented or stored.

## Out of scope

- Real e-mail delivery: activation links and the access-request notice go to the local Mock Email Outbox only (DECISIONS.md F4, O2). Real delivery would send each link to its employee and remove the outbox's reveal.
- SSO.
- Real tool connectors: API tools are reached through the Mock Tool Service only (DECISIONS.md A1).
- Investigating past employee activity in external tools. AccessFlow answers who had access in AccessFlow on a date (point-in-time); finding and closing accounts held outside it is an IT/InfoSec task.
