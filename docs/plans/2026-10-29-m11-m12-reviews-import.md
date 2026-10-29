# M11 + M12 — Quarterly reviews, baseline import and orphan accounts

Branch `feat/reviews-import`, one PR. Rules: D8, D10, F2 (earlier-policy flag), O7, O8. Layout A8: `internal/reviews` (campaigns, items, findings, day steps) calls `internal/access` (removal, import, orphans); `access` never calls back.

## Dependencies between the two

- The certification campaign (D10) is an ordinary campaign started by a Super Admin after an import, so M11 comes first and M12 only adds grants it snapshots.
- Imported grants have no request, no approval and no activation date. The removal path, the runner and the review snapshot must accept them (`request_item_id` NULL, no GRANT operation).
- Review flags include `IMPORTED_BASELINE`; Exceptions lists open review findings (M11) and orphan accounts (M12).

## Batches

**A. Reviews backend (M11)**
- Migration `00011`: `review_campaigns`, `review_items`; `grants.earlier_policy_at` (F2).
- Pure rules (table tests): day-step due checks, quarter of a time, reviewer routing, bulk eligibility, who may Keep or Revoke.
- Start: the quarterly sweep (each calendar quarter that began after installation, once) and a Super Admin manual start; one active campaign at a time. The snapshot covers ACTIVE and SUSPENDED employee grants, excluding grants with an open finding.
- Routing: the tool owner reviews; the owner's own access, an inactive owner or a Super Admin owner go to the InfoSec pool (any InfoSec member except the person).
- Decisions: Keep / Revoke with version; Revoke needs a reason and goes through the normal removal (`REVIEW`). Super Admin is Revoke-only. Decisions after day 21 are late. Bulk Keep skips the items that must be decided individually.
- Sweep: day 7 reminder per open item, day 14 escalation, day 21 auto-revoke (`REVOKED_NO_REVIEW`, using the snapshot) or open finding; then close the campaign. Items whose grant ended close as "already removed" (findings too). Each step runs once; it catches up after downtime.
- F2: adding steps to a level flags its open grants `earlier_policy_at`; a review decision clears it.
- API: `GET /reviews`, `POST /reviews/campaigns`, `POST /review-items/{id}/decide`, `POST /review-items/bulk-keep`; Exceptions gains `reviewFindings`.

**B. Import and orphans backend (M12)**
- Migration `00012`: `imports` (report); `grants.import_id`, `grants.account`; `request_item_id` and `employee_id` nullable (an orphan is an imported grant with no employee).
- Mock Tool Service: `GET /v1/tools/{tool}/accounts`.
- Import from the mock (API tools) and from CSV (`eposta;arac;seviye;hesap`, Manual/Vendor tools). Email match is case-insensitive; idempotent per (tool, account, level). Unknown tool/level rows are listed; a terminated employee's account is removed at once; a suspended employee's account is suspended; unmatched accounts become orphans.
- Removal, the runner and tasks use `grants.account` when set and accept imported grants without a GRANT operation.
- Orphans: InfoSec revokes (normal removal) or dismisses with a reason. Exceptions gains `orphanAccounts`.
- API: `POST /imports/mock`, `POST /imports/csv`, `GET /imports`, `POST /orphans/{id}/revoke`, `POST /orphans/{id}/dismiss`.

**C. UI** — Reviews page, Settings tabs "İnceleme takvimi" (manual start) and "İçe aktarma", Exceptions cards for findings and orphans, nav entry and counts, Turkish texts.

**D. Delivery** — README, plan status, one Docker journey, review, report.

## Tests (targeted; full suite in CI on the PR head)
Each day step exactly once; day 21 uses the snapshot; an open finding is not re-added; Super Admin Keep rejected; bulk Keep skips flagged items; late decision recorded; already-removed closure; idempotent import; terminated account revoked; orphan revoke and dismiss; malformed CSV rejected safely; unknown tool/level listed.

## Status (2026-10-29)
Batches A–C implemented on `feat/reviews-import`, with targeted backend and frontend tests green; full suite runs in CI on the PR head. Seed data with a terminated employee's account and an orphan stays in M15 (master plan). The certification campaign is a manual campaign started from Settings after an import.
