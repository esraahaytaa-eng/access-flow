# M17 — Company catalog and one-workbook onboarding (employees, tools, existing access)

> Status: **approved 2026-10-29**: decisions 1–5 and 7 as proposed; decision 6 changed — API-tool rows are accepted as reported baseline (§6a). Branch `feat/employee-onboarding`, from `main` at `f740267`. Supersedes the two-CSV and the two-sheet versions of this plan. Still in force from the earlier approval: two flat catalogs (departments, job positions), create and retire only, exact spelling, imported employees without a password, English demo catalog names, Turkish UI, English demo-account labels.

**Goal:** a company with about 400 employees and 60 tools defines its departments, job positions and admin groups, downloads one Excel workbook, fills in its employees, its tools with their levels, owners, admin groups and approval steps, and its existing access, previews the whole workbook, and imports it in one transaction — without creating employees or tools one by one, without provisioning existing access again and without inventing approvals.

## Setup order (enforced by the data)

1. Secure deployment; the first Super Admin, InfoSec members, HR Managers, Auditor (existing; system roles are **never** assigned by the workbook).
2. In AccessFlow: departments and job positions (new catalog) and **admin groups by name** (existing, create-only) — ⚑2.
3. **Download the Excel template**: its dropdowns are built from step 2.
4. Fill in **Employees**, **Tools**, **Existing Access**.
5. **Upload → Önizle → İçe Aktar**: validated as a whole; applied in dependency order in one transaction (employees and managers → tools, levels, owners, admin groups → existing access), or nothing is written.
6. In AccessFlow: add the admin groups' members (the imported employees now exist) — ⚑2.
7. Reconcile (fix the workbook and re-import; orphan accounts through the per-tool account CSV and the tool read — ⚑5), certify, pilot, cutover.

## How it fits the current model

| Concept | Today | After M17 |
|---|---|---|
| Department, job position | free text on `employees` and `starter_packages` | same text columns, foreign key to new `departments` / `job_positions`; new values must name an active entry |
| What they control | starter package recommendations (F3); department change = transfer (D5) | **unchanged**: they grant nothing; no RBAC engine, no position→permission mapping |
| System roles | `role_assignments`, Super Admin only, D2 | unchanged; the workbook has no role column and assigns none |
| Admin groups | Super Admin creates by name (create-only) and adds active members, never oneself (D2) | unchanged; the workbook only **references** existing groups; membership is never read from Excel |
| Tool ownership | Super Admin sets an active employee as owner (`catalog.CreateTool`) | the same rule, applied to the workbook by the Super Admin who imports it; every new owner is listed in the preview |
| Approval steps | per level: Manager always, Tool Owner and InfoSec optional (F2) | the same, as one dropdown per level row |
| Existing access | D10: tool read (API tools), account CSV (Manual/Vendor) | unchanged, plus the Existing Access sheet through the same D10 code |

## Architecture

### 1. Catalog (migration `00015_onboarding.sql`, `internal/people/positions.go`)

- `departments` and `job_positions` (`id, name, active, created_at, created_by`); `CHECK (btrim(name) = name AND name <> '')`, unique on `lower(name)`, unique on `name` as the foreign-key target.
- The migration copies every distinct existing value from `employees` and `starter_packages`, then adds the foreign keys; no existing row can fail them.
- HR Manager and Super Admin: list, create, retire. Audited `DEPARTMENT_CREATED`, `DEPARTMENT_RETIRED`, `JOB_POSITION_CREATED`, `JOB_POSITION_RETIRED`. No delete, no rename.
- Holders of a retired value keep it. Every write that sets a value (employee create/edit, transfer, rehire, starter package, workbook) requires an active entry and locks it `FOR SHARE`; retire takes `FOR UPDATE`. Errors `unknown_department`, `retired_department`, `unknown_job_position`, `retired_job_position`; a case-only difference carries the defined spelling.

### 2. Excel library ⚑1 (new dependency)

**Recommendation: `github.com/xuri/excelize/v2` v2.11.0** (BSD-3, pure Go).

- **Why:** the template needs data validations and a hidden sheet; the parser must accept files saved by Excel, LibreOffice and Google Sheets (shared and inline strings, rich-text runs, sparse rows). The standard-library alternative is about 300 lines of zip/XML code to own and test, where a bug silently imports wrong owners or access.
- **Cost:** six new modules (`richardlehane/mscfb`, `richardlehane/msoleps`, `tiendc/go-deepcopy`, `xuri/efp`, `xuri/nfp`, `golang.org/x/net`), checked with `go get` in the test container. Before adding: release at least two weeks old, `govulncheck` clean.
- **Unsafe-file controls (ours):** body capped at 2 MiB (route-specific; JSON stays at 64 KiB); must start with a zip signature (rejects encrypted/OLE files and `.xls`); `xl/vbaProject.bin` or a macro content type rejected; excelize `UnzipSizeLimit` 16 MiB / `UnzipXMLSizeLimit` 8 MiB; at most 1,000 employee, 2,000 tool-level and 5,000 access rows; only the three named sheets read; a formula cell is rejected (`formula_not_allowed`), never evaluated; every value passes `platform.CheckText`.

### 3. The workbook (`GET /api/v1/imports/workbook/template`, Super Admin)

Three visible sheets, exact English headers in this order, bold frozen header row:

| Sheet | Columns (one row per …) | Dropdowns (from AccessFlow) |
|---|---|---|
| **Employees** (employee) | Email · Full Name · Department · Job Position · Manager Email | Department, Job Position (active entries) |
| **Tools** (tool + access level; a tool with 3 levels has 3 rows) | Tool · Fulfillment Method · Tool Owner Email · Admin Group · Access Level · Approval Steps | Fulfillment Method (`Manual`, `Vendor`, `API`), Admin Group (existing groups), Approval Steps (`Manager`, `Manager + Tool Owner`, `Manager + InfoSec`, `Manager + Tool Owner + InfoSec`) |
| **Existing Access** (employee + tool + level) | Employee Email · Tool · Access Level · Account | none — see below |

- A hidden `Lists` sheet holds the dropdown values, written exactly as stored (UTF-8, Turkish characters).
- **Existing Access has no Tool/Level dropdown** ⚑3: most tools are being defined in the same workbook, so they are not configured values yet. A dropdown built from the Tools sheet would list each tool once per level and needs Excel-365-only functions (`UNIQUE`/`FILTER`) for a level list that follows the tool. Instead each column has an input message ("a Tool and Access Level from the Tools sheet, or a tool already in AccessFlow"), and the server's error for a wrong value lists the tool's defined levels. The two-sheet design's dependent dropdown is dropped.
- Tool-level fields repeated on each level row must be identical (`conflicting_tool`); Account defaults to the employee's email; Manager Email and Tool Owner Email may name a row of the Employees sheet or an existing employee.
- **Sample rows:** one grey row per sheet with catalog values that exist, names `EXAMPLE – replace this row`, emails `@example.invalid`, tool `Example Tool`. The import rejects any row with an `@example.invalid` email or the tool `Example Tool` as `sample_row`. No real employee, credential or access data in the template.
- File `accessflow-onboarding-YYYY-MM-DD.xlsx`.

### 4. Validation (pure, table-driven: `internal/onboarding/rules.go`)

`Validate(Workbook, Snapshot) Plan` — the parsed rows and a snapshot of the catalogs, groups, tools and existing employees in; out: what to create (employees manager-first, tools, levels, holdings), what to skip with its warnings, and every problem `{sheet, row, column, value, reason, expected}`. No database, no clock. **Any problem blocks the import; warnings do not.**

- **Workbook:** `invalid_workbook`, `missing_sheet`, `invalid_header`, `too_many_rows`, `empty_workbook`, `formula_not_allowed`.
- **Employees:** `field_required`, `invalid_email`, `text_too_long`, `sample_row`, `unknown_/retired_department`, `unknown_/retired_job_position`, `duplicate_employee`, `conflicting_employee` (both rows named), `self_manager`, `unknown_manager`, `inactive_manager`, `manager_cycle` (every row of the cycle). Managers resolve regardless of row order; created in topological order.
- **Existing employee** (email already in AccessFlow): never changed. Skipped with a warning listing each differing field and their status ⚑4.
- **Tools:** `field_required`, `invalid_fulfillment_method`, `invalid_approval_steps`, `unknown_admin_group` (exact name; case-only difference shows the defined one), `unknown_owner` (neither a row nor an existing employee), `inactive_owner` (existing suspended or terminated employee), `conflicting_tool` (method, owner or group differ between a tool's rows), `duplicate_level`, `sample_row`, `retired_tool` (a retired tool keeps its name; reviving it is not an import).
- **Existing tool** (name already in AccessFlow): its method, owner and admin group are never changed; differences are warnings. A level it already has is skipped; different approval steps are a warning (changing steps is an F2 policy change, done in the UI where InfoSec is notified and earlier grants are flagged). A level it lacks is created.
- **Existing Access:** `field_required`, `invalid_email`, `sample_row`, `unknown_employee` (⚑5), `unknown_tool`, `unknown_level` / `retired_level` (`expected` = the tool's levels), `api_account_mismatch` (§6a), `duplicate_access` (same employee twice) and `conflicting_access` (same tool, account and level for two employees), both rows named. Against AccessFlow: already imported for the same holder → skipped; imported for another holder or as an orphan → skipped with warning `held_by_other` (never reassigned); imported before and since removed → skipped with warning `previously_removed` (access re-granted outside AccessFlow). Several rows per employee and several levels of one tool are allowed.

### 5. Preview and import (`internal/onboarding`, Super Admin only)

A new package, because the import spans three existing ones: it calls `people`, `catalog` and `access`, none of which imports the others' onboarding parts (checked: `people`, `catalog` and `access` import only `audit`, `auth`, `notify`, `platform`, `mocktools`; no cycle).

- `POST /api/v1/imports/workbook/preview?fileName=…` and `POST /api/v1/imports/workbook?fileName=…`: raw `.xlsx` body, CSRF header, 2 MiB limit. Stateless: the import resends the file.
- **Preview**: snapshot from the pool, validate, write nothing; `200` with the summary — employees to create / skip, tools and levels to create / skip, **tool owners to be assigned (tool → person)**, access to import / already present, warnings, problems.
- **Import** — one `platform.InTx`:
  1. `pg_advisory_xact_lock(access.ImportLockKey)`: workbook, CSV and tool-read imports run one at a time.
  2. Snapshot again inside the transaction (catalog entries, admin groups, tools, levels `FOR SHARE`; existing employees by email `FOR SHARE`); validate again; any problem → `400` with the list, rolled back.
  3. **Employees**, managers first: `ACTIVE`, **no `password_credentials` row** (sign-in impossible until HR issues the one-time password with the existing reset, F4), no role, `EMPLOYEE_CREATED` `{importId, row}`.
  4. **Tools and levels** through the same code as `catalog.CreateTool` / `CreateLevel` (owner active, group exists, name unique, method fixed), `TOOL_CREATED` / `LEVEL_CREATED` `{importId, row}`.
  5. **Existing access** through the D10 code (§6).
  6. The `imports` row (source `WORKBOOK`, file name, report) and one `WORKBOOK_IMPORTED` summary event; one InfoSec notification for the import's approval policies ⚑7.
  7. A unique violation from a concurrent UI create (same email or tool name) rolls back and answers `409 retry`; the retry skips it.
- **Re-import**: employees by email, tools by name, levels by (tool, name), access by the D10 rule (same tool, account and level imported before, or the employee holds the level). The same workbook twice creates nothing the second time.

### 6. Reuse without changing behaviour

- **D10** (`internal/access/imports.go`): `importHoldings` today opens its own transaction. Its per-holding loop moves into `RecordHoldings(ctx, tx, now, actor, *ImportReport, []Holding) error` (skip rule, `IMPORTED_BASELINE` grant `ACTIVE` with `import_id` and `account`, `GRANT_IMPORTED` with approver "unknown: existed before AccessFlow", terminated → removal, suspended → suspended, no employee → orphan); `importHoldings` keeps its transaction, lock and `imports` row. No operations, requests or approvals for imported grants. The existing D10 tests stay unchanged and green.
- **Catalog** (`internal/catalog/service.go`): `CreateTool` and `CreateLevel` keep their permission check and transaction and call new `CreateToolTx` / `CreateLevelTx(ctx, tx, now, actor, …)` holding the current body (checks, insert, audit). `CreateLevelTx` takes a flag to skip the per-level InfoSec notification when the caller sends one summary (⚑7). Existing catalog tests unchanged.
- **People**: a tx-level `InsertImportedEmployee(ctx, tx, actor, …)` (insert, audit; no credential); `CreateEmployee` is unchanged.
- Migration: `grants.tool_verified_at timestamptz` (§6a); `imports.source` gains `'WORKBOOK'`; the file-name check becomes `(source IN ('CSV','WORKBOOK')) = (file_name IS NOT NULL)`.

### 6a. API-tool access from the workbook (D10 extension, approved 2026-10-29)

- Existing Access accepts API, Manual and Vendor tools. Every row becomes the same `IMPORTED_BASELINE` grant: no operation, no request, no approval, approver unknown.
- **Reported vs verified:** a new `grants.tool_verified_at`. The tool read (`ImportMock`) sets it on the grants it records, and on an already-imported grant of the same tool, account and level when it finds that account in the tool (`GRANT_VERIFIED_BY_TOOL`, audited). Workbook and CSV grants start unverified. The tool read's report adds `verified` and `unverified` (reported API grants of the tools read that the tool does not hold). The UI shows "Excel ile bildirildi" / "Araçtan doğrulandı" on imported grants; Manual and Vendor grants stay reported (no tool to read).
- **Account safety:** for an API tool the Account must be empty or the employee's email (`api_account_mismatch` otherwise) — the tool read matches API accounts to employees the same way, and a wrong account would make a later automatic removal revoke someone else's real access.
- **A reported grant the tool does not hold:** removal sends REVOKE, the tool answers `NO_CHANGE`, the lookup confirms it is gone — safe. A suspension of it fails (`not_granted`) and shows in Exceptions for InfoSec. Running the tool read after the workbook import (rollout step) verifies the real ones and counts the rest as unverified.
- Unchanged: `ImportMock` behaviour apart from the verification; the CSV import still rejects API tools (`api_tool`).

### 7. Authorization summary

| Action | Who | Note |
|---|---|---|
| Departments/positions: create, retire | HR Manager, Super Admin | as employee administration (F4) |
| Template download, preview, import | Super Admin only | tools are Super Admin-only today (`canEdit`); employees would allow HR, but one workbook needs one authority |
| System roles | never from the workbook | D2, D11 unchanged |
| Admin group creation and membership | Super Admin in the UI only | D2; the workbook can only name an existing group |
| Tool owner | the importing Super Admin, as in the UI | owner must be an active employee; shown in the preview before confirming |

The import does not widen anyone's rights: every value it accepts is one the importing Super Admin could already set by hand, under the same checks.

### 8. UI (Turkish, existing patterns; no redesign)

- Çalışanlar page → **"Departmanlar ve unvanlar"** dialog (list, add, retire). *Implementation note (2026-10-29):* moved from a Yönetim ayarları tab because that page is Super Admin-only and HR Managers manage the catalog; the Çalışanlar page is open to exactly HR Managers and Super Admins.
- Department and job position pickers in the employee, transfer, rehire and starter package forms (a retired current value stays shown).
- İçe aktarma tab, a new first card (Super Admin): "Excel Şablonunu İndir", "Excel Yükle", "Önizle", "İçe Aktar" (enabled after a clean preview of the same file); summary counts per sheet, the tool-owner list, problems table (Sayfa, Satır, Sütun, Değer, Sorun, Beklenen) and warnings. The tool-read and CSV cards stay. `api/client.ts` gets a raw-body post and a blob download.
- Demo sign-in in English: "Demo Accounts", "Demo Environment", "Select an account. Passwords are listed in the README.", roles "Super Admin", "Information Security", "HR Manager", "Auditor", "Manager", "Tool Owner & IT Operations", "Employee". The rest stays Turkish; no i18n infrastructure.

### 9. Demo seed (English catalog names)

- Departments: Engineering, Human Resources, Information Security, Compliance, Finance, Sales, IT Operations, Management. Positions: Backend Developer, Senior Backend Developer, Frontend Developer, DevOps Engineer, Product Designer, Engineering Manager, HR Manager, Security Analyst, Cloud Security Engineer, Auditor, IT Operations Lead, Finance Manager, Payroll Specialist, Accountant, Sales Manager, Sales Representative, Enterprise Account Executive, Sales Assistant. Admin groups: IT Operations, Vendor Desk.
- Levels in English (GitHub → Developer, Maintainer, Admin; Jira → User, Project Admin; Figma → Viewer, Editor; …). People's names stay Turkish. The seven demo accounts, passwords, roles and every scenario stay; their tests are updated for the renamed values. The seed keeps using the services directly (it does not go through a workbook).

## Implementation notes (2026-10-30, found during M17 and its review)

- **excelize version.** v2.11.0 has four govulncheck findings our code reaches (GO-2026-6684, -6685, -6688, -6689: an unbounded `GetRows` loop, array-formula recursion, CFB panics). No tagged release fixes them yet, so `go.mod` pins the upstream commit `v2.11.1-0.20261003002531-6258dcebc4e2` (2026-10-03), which fixes them and GO-2026-6695; govulncheck v1.9.0 then reports no reachable vulnerability. Move to the first tagged release that contains these fixes.
- **Unzip limits.** `UnzipXMLSizeLimit` is set to the same 16 MiB as `UnzipSizeLimit`, not 8 MiB: in excelize it is the size above which a sheet is spilled to a temporary file, not a rejection limit, so equal limits keep every sheet in memory.
- **Macros** are detected by package part (`xl/vbaProject.bin`, `xl/macrosheets/`), not by content type: those parts hold the macro code; a content type without them carries nothing executable, and nothing in a workbook is ever executed or served back.
- **Raw rows.** A sheet whose raw row count exceeds twice its row limit (a stray cell far below the data) is refused with `too_many_rows`; formulas are checked only in non-empty cells.
- **Reason code** for an over-long or control-character value is `invalid_text`, the code the rest of the API uses, not `text_too_long`.
- **Audit.** The import is one `ACCESS_IMPORTED` event with `source: WORKBOOK` and `created` counts, not a separate `WORKBOOK_IMPORTED`; `EMPLOYEE_CREATED` carries the import id, sheet and row; tool and level events are the normal `TOOL_CREATED` / `ACCESS_LEVEL_CREATED`.
- **Packages.** `catalog` imports `people` for `CheckPlacement` (no cycle).
- **Migration** marks grants from earlier tool reads as verified at their import time, so existing tool-read access is not shown as reported after upgrade.
- **Duplicates.** The same employee with the same tool and level twice in Existing Access is `duplicate_access`, whatever the account, because the D10 code would keep only the first.

## Docs

DECISIONS.md dated notes (F2 bulk policy notification, F3 catalog, F4 imported employees without password, D2 admin groups referenced not populated, D10 workbook source); README; `docs/operating-summary.md`; `docs/rollout-30-days.md` in the order *secure deployment → departments, positions and admin groups → download the template → import employees, tools and existing access together → add admin group members → reconcile → certify → pilot → cutover*, the 14 former employees' external cleanup an IT/InfoSec task; `docs/api.md`; `docs/known-limitations.md` (no Tool/Level dropdown on Existing Access; API-tool access via the tool read; no catalog rename; existing tools and employees are not updated by re-import); `docs/qa-checklist.md` (open the template in Excel and LibreOffice).

## Tests (targeted while developing; full suite in PR CI)

- **Rules, table-driven:** every problem reason; case-only mismatch shows the defined spelling; managers in any order, self-manager, 2- and 3-cycles; owner from the Employees sheet, from existing employees, inactive owner; a tool with several levels, conflicting tool rows, duplicate level; invalid approval steps; existing tool and employee mismatches as warnings; several accesses per employee; access to a tool defined in the same workbook; unknown level lists the tool’s levels; API-tool access accepted only with an empty account or the employee’s email (`api_account_mismatch`); sample rows; formula cells and Excel 4.0 macro sheets refused.
- **Template (read back with excelize):** three visible sheets plus hidden `Lists`; exact headers; dropdowns cover active entries only (retired absent); Turkish characters survive; sample rows contain only `example.invalid` data.
- **Parsing:** not a zip, OLE/encrypted, macro workbook, zip bomb, missing sheet, wrong header, formula cell, too many rows, shared and inline strings, trailing empty rows ignored.
- **PostgreSQL / API scenarios:** migration copies values and adds keys; catalog permissions, retire keeps holders, retired value rejected everywhere; preview writes nothing; import creates employees, tools, levels and baseline grants together; a bad Existing Access row leaves no employee or tool (rollback); a fault injected after the tools are inserted leaves nothing; a value retired between preview and import is caught; re-import creates nothing; existing tool/employee not changed, warnings reported; owner can be a new employee; workbook cannot assign a role or a group member; no `password_credentials` row, sign-in refused until a reset; no operations, requests or approvals; terminated employee's access → removal (D10); only Super Admin; audit events and the single InfoSec notification; D10 and catalog tests unchanged and green.
- **Vitest:** catalog tab, pickers, workbook card (preview summary, owner list, problems table, import enabled only after a clean preview), English demo labels.

## Decisions to approve (⚑)

1. **excelize v2.11.0** (six transitive modules). Alternative: hand-written stdlib zip/XML.
2. **Admin groups:** created by name in AccessFlow before the template, referenced by a dropdown, members added in the UI after the import. The workbook never creates groups or memberships. Alternative: the workbook creates missing groups (empty) by name — saves ~10 clicks, but a typo creates a stray group whose tasks nobody can work.
3. **No Tool/Level dropdown on Existing Access** (reason in §3); the server validates and lists the right levels.
4. **Existing employee or tool that differs = warning, skipped, never overwritten.** Alternative: block until the row matches.
5. **Access rows must name a known employee**, so orphan accounts come from the per-tool account CSV and the tool read during reconcile (how the 14 former employees' accounts surface).
6. **Changed by the user:** API-tool access rows accepted as reported baseline, distinguished from tool-verified access (§6a).
7. **One InfoSec notification per import** summarizing the approval steps created, instead of one per level (F2 today notifies per level: ~200 notifications for 60 tools). Each level is still audited individually.

## Size and risk

About 3,000–3,500 lines including tests. Risks: refactoring `CreateTool`/`CreateLevel` and the D10 import touches tested code (kept behaviour-neutral, tests unchanged); a three-sheet workbook makes errors cross sheets (an access row fails because its tool row failed — reported once, at the source row, with the dependent rows referring to it); demo renames ripple through demo and UI tests; free-text forms become pickers.
