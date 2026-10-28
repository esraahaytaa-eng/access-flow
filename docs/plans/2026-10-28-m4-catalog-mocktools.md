# M4 — Catalog and Mock Tool Service: Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Status:** approved 2026-10-28 (scope and all listed calls). Branch `feat/catalog-requests`, shared with M5 (one PR, per the main plan's grouping). No push until M5 is reviewed, unless you decide otherwise.

**Goal:** a Super Admin configures tools and access levels with their approval steps. Policy changes are audited and reach InfoSec as in-app notifications. Every employee can browse the requestable catalog with their department's starter package marked. The Mock Tool Service stores accounts and per-level permissions and applies grant, revoke, suspend and resume idempotently by operation ID. AccessFlow has a tested HTTP client for it that treats every error, timeout or unexpected response as failure.

**Architecture:**
- `internal/catalog`, mounted under `/api/v1` behind `auth.RequireSession`, follows the `people` layout:
  - `rules.go` holds the pure checks;
  - `service.go` holds the transactions, with SQL inline;
  - `http.go` holds the handlers.
- `internal/notify` is a leaf package with in-app notifications (A8).
- `internal/mocktools` holds the Mock Tool Service handlers, store and its own embedded migrations, served by `cmd/mocktools` from its own database (A1, A2).
- `internal/access` starts with `ToolClient`, the one interface the architecture allows (A8). Its runner and grants come in M6.
- No new dependencies.

**Tech Stack:** Go 1.27, chi v5, pgx v5, goose v3, PostgreSQL 18 (as in M2–M3). The Mock Tool Service keeps `net/http`'s `ServeMux`, as it has today.

**Spec:** `CASE.md`, `DECISIONS.md` (F1, F2, F3, D4, the fulfillment methods, O2, A1, A2, A6, A8, A9, A10), the main plan `docs/plans/2026-10-28-implementation-plan.md` (M4 section, Global Constraints, Review Focus).

## Global Constraints

- Everything in the main plan's Global Constraints applies.
- Only a Super Admin changes the catalog. Everyone with a full session can read the requestable catalog (F2, F3).
- Approval steps belong to the access level. Manager is always required; Tool Owner and InfoSec are optional, in the fixed order Manager → Tool Owner → InfoSec (F2). Snapshotting at submission and "stricter wins" are M5. Marking grants "approved under earlier policy" is M11.
- Every catalog change is audited in the same transaction. Every policy change (a level's steps on creation or edit) also creates one in-app notification per active InfoSec member in that transaction (F2, O2). No emails (O2).
- Nothing is physically deleted. Retiring sets a status; removing a starter-package entry sets `removed_at` (A5). The runtime user keeps having no `DELETE` grant.
- Mutable rows carry `version`; a stale update returns `409 version_conflict` (A9).
- The Mock Tool Service is reached only over HTTP. AccessFlow never reads its database (A2).
- A Mock Tool error, timeout (5 s), non-200 status or malformed body is never success (A2, A6).
- Every Mock Tool operation names one (tool, account, level). Revoking one level never removes another level or the account (D4).
- API errors: `{"error": "<code>", "field"?: "<fieldName>"}` (A7).

## My calls, listed for you to check

Each call follows from F2, F3, D4, A2 or A9 and keeps the rules small. None changes an approved decision.

1. **Tool fields:**
   - name (unique, case-insensitive);
   - fulfillment method (`API`, `MANUAL` or `VENDOR`);
   - Tool Owner (an `ACTIVE` employee);
   - responsible admin group, required for every method. Manual and Vendor tasks go to that group; for API tools it is the responsible group shown with failures.

   There is no description field; names identify tools and levels.
2. **The fulfillment method cannot change after creation.** Changing it would leave pending and active grants under the wrong workflow. A different method means a new tool.
3. **Retiring is one-way and only stops new requests.** A level is requestable only when the level and its tool are both `ACTIVE`. Retiring a tool does not rewrite its levels. The effect on existing grants is not decided for M4: no grants exist yet, and D7 already treats "retired" grants as not restored.
4. **Policy notifications:** creating a level, or changing its Tool Owner or InfoSec flags, notifies every `ACTIVE` InfoSec member. Renaming and retiring are audited but not notified, because they are not approval-policy changes. Tool Owner changes are audited and not notified.
5. **Starter packages (F3):** an entry is (department, optional job title, access level), managed by a Super Admin.
   - It is recommended to employees whose department matches, case-insensitively, and whose job title matches when the entry has one.
   - Recommending changes nothing else: any employee may request any requestable level (F3).
   - Removing an entry sets `removed_at`, because the runtime user has no `DELETE`.
6. **Notifications API:** `GET /api/v1/notifications` lists only the caller's own notifications, newest first, at most 100. `POST /api/v1/notifications/{id}/read` marks one read; repeating it changes nothing. Both are needed for the InfoSec notification to be usable.
7. **Catalog visibility:**
   - everyone sees requestable tools and levels, with steps, method, owner name and admin group name;
   - Super Admin, InfoSec and Auditor also see retired ones and versions (for configuration and evidence).
8. **Mock Tool identifiers:** a tool is identified by the AccessFlow tool ID, a level by the AccessFlow level ID and an account by the employee's email. Renames never break the link, and the email is how real tools name accounts.
9. **Mock Tool semantics, per (tool, account, level):**
   - `GRANT` creates the account if needed and adds the level as `ACTIVE`.
   - `REVOKE` removes that level's permission and keeps the account.
   - `SUSPEND` and `RESUME` switch that level between `ACTIVE` and `SUSPENDED`. Both are rejected with `409 not_granted` when the level is not held.
   - A repeated operation ID with the same content returns the stored result and has no further effect. The same ID with different content returns `409 operation_id_reused`.
   - `GRANT` of a level already held as `ACTIVE`, and `REVOKE` of a level not held, succeed with no change. `GRANT` of a `SUSPENDED` level returns `409 level_suspended`: only `RESUME` brings it back, so a grant never reports success while access is not working. The desired state is what counts, so a retry after a lost response is safe.
10. **The Mock Tool Service has no authentication.** It is reachable only on the internal Docker network (A2). This is documented in the README as a limitation of the mock.
11. **The client is built and tested, not wired.** `access.HTTPToolClient` and its `MOCKTOOLS_URL` setting are first used by the M6 operation runner. Adding an unused config value now would be speculative.

## Review Focus

1. **Lost response after the tool applied the change:** the client sees an error, a retry with the same operation ID returns the stored result, and the tool state shows one permission. Pinned in Task 5.
2. **Per-level isolation:** an account holds Viewer and Admin; revoking Admin leaves Viewer and the account in place, and suspending Viewer leaves Admin `ACTIVE`. Pinned in Task 4.
3. **Policy-change evidence:** adding InfoSec to a level writes an `ACCESS_LEVEL_UPDATED` audit event with `{"steps": {"from": [...], "to": [...]}}` and one notification per active InfoSec member. Suspended, terminated or former InfoSec members get none. Pinned in Task 2.
4. **Retired but still listed:** a retired level, or any level of a retired tool, is absent from a plain employee's catalog and has `requestable: false` in the full view. Pinned in Task 3.
5. **Concurrent edits:** two edits of a level with the same version give one `200` and one `409 version_conflict`, with one audit event and one set of notifications. Pinned in Task 2.

## Files

```
internal/platform/migrations/00004_catalog.sql   tools, access_levels, starter_packages, notifications (Tasks 1–3)
internal/notify/notify.go                       ToRole, List, MarkRead (Task 2)
internal/notify/http.go                         GET /notifications, POST /notifications/{id}/read (Task 2)
internal/catalog/rules.go                       pure checks: canEdit, clean names, method, Steps (Task 1)
internal/catalog/service.go                     tools, levels, retire, catalog listing (Tasks 2–3)
internal/catalog/packages.go                    starter packages (Task 3)
internal/catalog/http.go                        handlers, Register (Tasks 2–3)
internal/mocktools/migrations/00001_mock.sql    accounts, permissions, operations (Task 4)
internal/mocktools/mocktools.go                 Migrations, Handler (Task 4)
internal/platform/db.go                         Migrate takes the migrations FS (Task 4)
cmd/mocktools/main.go                           migrate at start, serve Handler (Task 4)
internal/access/toolclient.go                   ToolClient, ToolOperation, ToolAccount, HTTPToolClient (Task 5)
internal/testutil/mocktools.go                  NewMockToolsDB, Faults (Tasks 4–5)
internal/testutil/fixtures.go                   CreateTool, CreateLevel, CreateGroup builders (Task 1)
internal/devfixture/devfixture.go               admin groups, four tools, starter package (Task 6)
cmd/accessflow/main.go                          mount catalog and notify routes (Tasks 2–3)
README.md, docs/plans/…                         API, mock service, status (Task 6)
```

---

### Task 1: Catalog schema, rules and test builders

**Interfaces — Produces:**
- Migration `00004_catalog.sql`:
  - `tools (id uuid PK DEFAULT uuidv7(), name text CHECK trimmed, non-empty, ≤ 100, fulfillment_method text CHECK IN ('API','MANUAL','VENDOR'), owner_id uuid NOT NULL → employees, admin_group_id uuid NOT NULL → admin_groups, status text NOT NULL DEFAULT 'ACTIVE' CHECK IN ('ACTIVE','RETIRED'), version int NOT NULL DEFAULT 1, seeded bool NOT NULL DEFAULT false, created_at, created_by → employees NULL)`, with a unique index on `lower(name)`.
  - `access_levels (id, tool_id → tools, name (same CHECK), requires_tool_owner bool NOT NULL, requires_infosec bool NOT NULL, status (as tools), version, created_at, created_by)`, with a unique index on `(tool_id, lower(name))`.
  - `starter_packages (id, department text NOT NULL, job_title text NULL, access_level_id → access_levels, created_at, created_by, removed_at timestamptz NULL, removed_by → employees NULL)`, with a unique index on `(lower(department), lower(coalesce(job_title, '')), access_level_id) WHERE removed_at IS NULL`.
  - `notifications (id, recipient_id → employees NOT NULL, kind text NOT NULL, entity text NOT NULL, entity_id uuid, details jsonb NOT NULL DEFAULT '{}', created_at, read_at timestamptz NULL)`, with an index on `(recipient_id, created_at DESC)`.
  - Grants: `SELECT, INSERT, UPDATE` on the four tables to `accessflow_app`.
- `catalog.Steps(requiresToolOwner, requiresInfosec bool) []string` returns, in fixed order, `["MANAGER"]`, plus `"TOOL_OWNER"` and `"INFOSEC"` when they are set.
- `canEdit(p auth.Principal) error` returns `403 forbidden` unless the actor holds `SUPER_ADMIN`.
- `cleanName(field, s string) (string, error)` trims and returns `name_required` or `value_too_long` (max 100) or `value_invalid` (control characters). It reuses the `people` text rule by moving `checkText` to `platform` (`platform.CheckText`); people keeps calling it.
- `testutil.CreateGroup(t, db, name) uuid.UUID`, `testutil.CreateTool(t, db, ToolSpec) Tool` and `testutil.CreateLevel(t, db, toolID, LevelSpec) uuid.UUID`, all by plain SQL like `CreateEmployee`. Defaults: method `API`, a new owner and a new group.

- [ ] **Step 1: Write failing tests**
  - `TestSteps` (table): `(false,false)` → `[MANAGER]`; `(true,false)` → `[MANAGER TOOL_OWNER]`; `(false,true)` → `[MANAGER INFOSEC]`; `(true,true)` → `[MANAGER TOOL_OWNER INFOSEC]`.
  - `TestCanEdit` (table): `SUPER_ADMIN` → nil. `INFOSEC`, `HR_MANAGER`, `AUDITOR`, no roles → `forbidden`.
  - `TestCatalogSchema` (`platform/identity_schema_test.go` style):
    - method `'SCRIPT'` → `23514`;
    - two tools named `Jira` and `jira` → `23505`;
    - a second open starter package entry for the same department, job title and level → `23505`, but allowed again after `removed_at` is set;
    - the runtime user cannot `DELETE` from any of the four tables → `42501`.
- [ ] **Step 2: Run and see them fail.** `docker compose --profile test run --rm tests go test -count=1 ./internal/catalog/ ./internal/platform/`
- [ ] **Step 3: Implement** the migration, `rules.go`, the move of `checkText` to `platform.CheckText`, and the builders.
- [ ] **Step 4: Run; all pass, including the `people` tests after the move.**

### Task 2: Tools and levels administration, policy notifications

**Interfaces — Consumes:** Task 1; `auth.Principal`, `audit.Record`, `platform.InTx`, `platform.DecodeJSON`.

**Interfaces — Produces:**
- `notify.ToRole(ctx, tx pgx.Tx, role, kind, entity string, entityID uuid.UUID, details map[string]any) error` inserts one row per employee who is `ACTIVE` and holds an open `role` assignment.
- `notify.List(ctx, pool, recipient uuid.UUID) ([]Notification, error)` returns the newest 100.
- `notify.MarkRead(ctx, pool, recipient, id uuid.UUID) error` returns `404` for someone else's notification (no existence leak) and nothing for one already read.
- `Notification{ID, Kind, Entity, EntityID, Details, CreatedAt, ReadAt}` with camelCase JSON.
- `catalog.Service` methods:
  - `NewService(pool)`;
  - `CreateTool(ctx, actor, NewTool) (Tool, error)`;
  - `UpdateTool(ctx, actor, id, ToolUpdate) (Tool, error)`;
  - `RetireTool(ctx, actor, id, version int) (Tool, error)`;
  - `CreateLevel(ctx, actor, toolID, NewLevel) (Level, error)`;
  - `UpdateLevel(ctx, actor, toolID, levelID, LevelUpdate) (Level, error)`;
  - `RetireLevel(ctx, actor, toolID, levelID, version int) (Level, error)`.

  `ToolUpdate` and `LevelUpdate` carry every editable field plus `Version`, and PUT requires all keys, as in M3.
- Routes:
  - `POST /api/v1/tools` `{name, fulfillmentMethod, ownerId, adminGroupId}` → 201;
  - `PUT /api/v1/tools/{id}` `{name, ownerId, adminGroupId, version}`;
  - `POST /api/v1/tools/{id}/retire` `{version}`;
  - `POST /api/v1/tools/{id}/levels` `{name, requiresToolOwner, requiresInfosec}` → 201;
  - `PUT /api/v1/tools/{id}/levels/{levelId}` `{name, requiresToolOwner, requiresInfosec, version}`;
  - `POST /api/v1/tools/{id}/levels/{levelId}/retire` `{version}`;
  - `GET /api/v1/notifications`, `POST /api/v1/notifications/{id}/read` → 204.
- Error codes:
  - `invalid_fulfillment_method` (field `fulfillmentMethod`);
  - `owner_not_active` (field `ownerId`);
  - `admin_group_not_found` (field `adminGroupId`);
  - `tool_name_taken` 409;
  - `level_name_taken` 409;
  - `tool_retired` 409 (adding or editing a level of a retired tool, or editing a retired tool);
  - `level_retired` 409;
  - `version_conflict` 409.
- Audit actions: `TOOL_CREATED`, `TOOL_UPDATED` (`{field: {from, to}}`), `TOOL_RETIRED`, `ACCESS_LEVEL_CREATED` (`{"steps": [...]}`), `ACCESS_LEVEL_UPDATED` (changes map; steps as `{"steps": {"from": [...], "to": [...]}}`) and `ACCESS_LEVEL_RETIRED`.
- Notification kind `APPROVAL_POLICY_CHANGED`, entity `access_level`, details `{"tool", "level", "steps": {"from", "to"}}`; `from` is null on creation.
- An edit that changes nothing returns `200` with no version bump, audit or notification (as in M3). Retiring twice returns the current state, with no second audit event.

- [ ] **Step 1: Write failing tests** (`internal/catalog/*_test.go`, `internal/notify/notify_test.go`, real PostgreSQL):
  - `TestCreateToolAndLevel`: a Super Admin creates `Jira` (API) and `Viewer` (no optional steps). The responses carry `steps: ["MANAGER"]`. There is one `TOOL_CREATED` and one `ACCESS_LEVEL_CREATED` audit event, and the active InfoSec member gets one `APPROVAL_POLICY_CHANGED`.
  - `TestToolValidation` (table): blank name, 101 characters, method `SCRIPT`, a terminated owner, an unknown group and a duplicate name in a different case each return their exact code. None of them creates a row or an audit event.
  - `TestAddingAStepNotifiesInfoSec`: `Viewer` → `requiresInfosec: true`. The audit details are `steps {from [MANAGER] to [MANAGER INFOSEC]}`. Two active InfoSec members get one notification each; a suspended InfoSec member and one whose role was removed get none.
  - `TestRemovingAStepIsAlsoAPolicyChange`: removing the step notifies InfoSec as well.
  - `TestRenameIsAuditedNotNotified`.
  - `TestFulfillmentMethodCannotChange`: a PUT body with `fulfillmentMethod` returns `400 invalid_json` (unknown field). The method stays unchanged.
  - `TestRetire`: retiring a level, then editing it → `409 level_retired`. Retiring a tool, then adding a level → `409 tool_retired`. Retiring again → 200 with one audit event in total.
  - `TestConcurrentLevelEditsWithTheSameVersion`: 10 goroutines → one 200 and the rest `409 version_conflict`, with one audit event and one notification per InfoSec member.
  - `TestCatalogPermissionMatrix`: every write route × (plain employee, HR Manager, InfoSec, Auditor, Super Admin). Only the Super Admin gets 2xx; everyone else gets `403 forbidden` with no row and no audit event.
  - `TestNotificationsAreOwnOnly`: listing shows only the caller's notifications. Marking someone else's → 404. Marking read twice → 204 both times, and `read_at` is set once.
- [ ] **Step 2: Run and see them fail.**
- [ ] **Step 3: Implement.**
  - Lock the tool row (`FOR UPDATE`) for tool edits, and also for level creation, so that retiring a tool and adding a level are serialized. Lock the level row for level edits.
  - Use M3's lock-then-read pattern.
  - Check that the owner is `ACTIVE` in the same transaction. The M9 follow-up about locking the manager row applies to the owner as well.
- [ ] **Step 4: Run; all pass.**

### Task 3: Requester catalog and starter packages

**Interfaces — Produces:**
- `catalog.Service.Catalog(ctx, viewer auth.Principal) ([]CatalogTool, error)`.
  - `CatalogTool{ID, Name, FulfillmentMethod, Owner{ID, FullName}, AdminGroup{ID, Name}, Status, Version, Levels []CatalogLevel}` and `CatalogLevel{ID, Name, Steps, Status, Version, Requestable, Recommended}`.
  - Full viewers (`SUPER_ADMIN`, `INFOSEC`, `AUDITOR`) get every tool and level.
  - Everyone else gets only requestable levels, and only tools with at least one requestable level, without `status` and `version`.
  - `Recommended` is true when an open starter package entry matches the viewer's department, and their job title if the entry names one. The comparison is case-insensitive.
  - Ordering is by `lower(name)`. One query plus one levels query; no query per tool.
- `catalog.Service.ListPackages(ctx, viewer)`, `AddPackage(ctx, actor, NewPackage{Department, JobTitle *string, AccessLevelID})` and `RemovePackage(ctx, actor, id)`.
  - Adding to a non-requestable level → `409 level_not_requestable`; adding a duplicate → `409 package_entry_exists`.
  - Removing sets `removed_at`/`removed_by`; removing an entry already removed changes nothing.
  - Audit actions: `STARTER_PACKAGE_ENTRY_ADDED` and `STARTER_PACKAGE_ENTRY_REMOVED`.
- Routes: `GET /api/v1/tools`; `GET /api/v1/starter-packages` (every viewer; read-only data); `POST /api/v1/starter-packages` and `DELETE /api/v1/starter-packages/{id}` → 204 (Super Admin only).

- [ ] **Step 1: Write failing tests**
  - `TestCatalogHidesRetired`: a plain employee does not see a retired level, nor any level of a retired tool, nor a tool whose levels are all retired. An Auditor sees all of them, with `requestable: false`.
  - `TestCatalogFieldsForPlainEmployees`: the JSON keys of a level are exactly `id, name, steps, requestable, recommended`.
  - `TestRecommendedByDepartmentAndJobTitle` (table):
    - an `Engineering` entry is recommended for `engineering` / any job title;
    - an `Engineering` + `Backend Developer` entry is recommended for `Engineering` / `backend developer`, but not for `Engineering` / `QA`;
    - a removed entry is never recommended;
    - an employee with no department gets no recommendations.
  - `TestStarterPackageAdministration`: duplicate → 409; a retired level → 409; remove twice → one audit event; non–Super Admin writes → 403.
- [ ] **Step 2: Run and see them fail.**
- [ ] **Step 3: Implement.** Mount `catalog.Register` and `notify` routes in `cmd/accessflow/main.go`, inside the `RequireSession` group, and add a 401 case to `main_test`.
- [ ] **Step 4: Run; all pass.**

### Task 4: Mock Tool Service

**Interfaces — Produces:**
- `mocktools.Migrations fs.FS` (embedded). `platform.Migrate` gains a parameter, becoming `Migrate(ctx, url string, migrations fs.FS) error`, and both binaries call it; it is not copied. Existing callers pass `platform.Migrations`.
- `mocktools.Handler(pool) http.Handler` with:
  - `POST /v1/operations` taking `{"operationId": uuid, "type": "GRANT"|"REVOKE"|"SUSPEND"|"RESUME", "tool": string, "account": string, "level": string}` and returning `200 {"operationId", "type", "tool", "account", "level", "result": "APPLIED"|"NO_CHANGE"}`;
  - `GET /v1/tools/{tool}/accounts/{account}` returning `200 {"tool", "account", "exists": bool, "levels": [{"level", "status": "ACTIVE"|"SUSPENDED"}]}`. An unknown account gives `exists: false` and `levels: []`, never 404, so "removed" and "wrong URL" cannot be confused;
  - `GET /healthz` (as today).
- Errors:
  - `400 invalid_json` and `400 invalid_operation` (a missing field, an unknown type, or a field over 254 characters);
  - `409 not_granted` (SUSPEND or RESUME of a level not held);
  - `409 level_suspended` (GRANT of a suspended level);
  - `409 operation_id_reused` (same ID, different content).
- Schema in `internal/mocktools/migrations/00001_mock.sql`:
  - `accounts (tool, account, created_at, PK (tool, account))`;
  - `permissions (tool, account, level, status CHECK IN ('ACTIVE','SUSPENDED'), PK (tool, account, level), FK → accounts)`;
  - `operations (operation_id uuid PK, type, tool, account, level, result, applied_at)`.

  The mock deletes a permission row on `REVOKE`. It is the simulated external tool, not an AccessFlow record, and AccessFlow's history lives in AccessFlow.
- Each operation runs in one transaction: insert into `operations` with `ON CONFLICT DO NOTHING`. If nothing was inserted, compare the stored row and return it, or `409` when it differs. Otherwise apply the effect and store its result.
- `cmd/mocktools/main.go` runs `platform.Migrate(ctx, DATABASE_URL, mocktools.Migrations)` before serving. The `mocktools` user owns its database, so no separate owner URL is needed.
- `testutil.NewMockToolsDB(t, migrations fs.FS) *pgxpool.Pool` creates a fresh database owned by `mocktools` through the admin URL, migrates it and drops it at cleanup. The migrations are passed in, which avoids an import cycle.

- [ ] **Step 1: Write failing tests** (`internal/mocktools/mocktools_test.go`, through `httptest` and the real handler):
  - `TestGrantCreatesAccountAndLevel`, then a lookup → `exists: true`, `levels [{L1 ACTIVE}]`.
  - `TestRevokeKeepsOtherLevelsAndTheAccount`: grant L1 and L2, revoke L2 → `[{L1 ACTIVE}]`, `exists: true`.
  - `TestSuspendAndResumeOneLevel`: suspending L1 leaves L2 `ACTIVE`. Resume brings it back. SUSPEND of an absent level → `409 not_granted`.
  - `TestRepeatedOperationIDHasNoExtraEffect`: the same GRANT sent twice gives the same body twice, with one permission row and one operation row. It is also repeated after a REVOKE with a different ID: the original GRANT ID returns its stored result and does **not** re-grant.
  - `TestReusedOperationIDWithDifferentContent` → 409.
  - `TestConcurrentSameOperation`: 10 goroutines send one ID → all get 200 and the same result, with one permission row.
  - `TestIdempotentDesiredState`: GRANT of an `ACTIVE` level and REVOKE of an absent one give `NO_CHANGE`. GRANT of a `SUSPENDED` level → `409 level_suspended`.
  - `TestInvalidOperations` (table): a missing account, an unknown type, a non-UUID ID, trailing JSON → 400.
  - `TestUnknownAccountLookup` → 200 `exists: false`.
- [ ] **Step 2: Run and see them fail.**
- [ ] **Step 3: Implement**, reusing `platform.DecodeJSON` and `platform.WriteError` for the same error format.
- [ ] **Step 4: Run; all pass.** `docker compose up` brings `mocktools` up healthy with its tables migrated; a second `up` also works.

### Task 5: AccessFlow's Mock Tool client and fault injection

**Interfaces — Produces:**
- In `internal/access/toolclient.go`:
  - `type ToolClient interface { Apply(ctx context.Context, op ToolOperation) (ToolResult, error); Account(ctx context.Context, tool, account string) (ToolAccount, error) }`;
  - `ToolOperation{ID uuid.UUID; Type, Tool, Account, Level string}`, `ToolResult{Result string}`, `ToolAccount{Exists bool; Levels []ToolLevel}` and `ToolLevel{Level, Status string}`;
  - `NewHTTPToolClient(baseURL string, timeout time.Duration) *HTTPToolClient`. Production passes `5 * time.Second`; tests pass a short timeout so they stay fast.
- The lookup path escapes the tool and account with `url.PathEscape`. Our email rule allows `/`, `?`, `#` and `%`, and an unescaped email would reach the wrong path.
- Every response other than 200 with a body that decodes (unknown fields rejected) and echoes the same operation ID, type, tool, account and level is an error. That covers 4xx, 5xx, timeouts, connection errors, malformed JSON, trailing data and a mismatched echo. `ToolResult.Result` must be `APPLIED` or `NO_CHANGE`, and a lookup's level status must be `ACTIVE` or `SUSPENDED`. Errors wrap the status code, so M6 can tell `409` from transient errors.
- `testutil.Faults` is an `http.Handler` wrapper around the real handler, with:
  - `FailNext(n int, status int)`;
  - `DelayNext(d time.Duration)`;
  - `MalformNext()` (replies `200 {"result": `);
  - `ApplyThenFail()` (lets the inner handler apply the change, then replies `500`: the lost response).

  It is safe for concurrent use.

- [ ] **Step 1: Write failing tests** (`internal/access/toolclient_test.go`, real mock handler on a real mock database behind `Faults`):
  - `TestApplyAndLookup` (table of accounts, including `a/b?c#d%e@x.com`): grant, then a lookup of the same account shows the level `ACTIVE`.
  - `TestClientTreatsFaultsAsErrors` (table): 500, 503, 409, a delay of 2× the timeout (which must also return within the timeout plus a small margin), malformed body, an echo with a different operation ID (a stub handler), an unknown result value, a closed server. Each one returns an error and never a result.
  - `TestRetryAfterALostResponse`: under `ApplyThenFail`, the first `Apply` errors and the retry with the same operation ID succeeds. The account shows one `ACTIVE` level.
- [ ] **Step 2: Run and see them fail.**
- [ ] **Step 3: Implement.** Use one `http.Client{Timeout: timeout}`, encode with `encoding/json`, and decode with `DisallowUnknownFields` plus an EOF check (as `platform.DecodeJSON`). Close response bodies, and read them through `io.LimitReader` (1 MiB).
- [ ] **Step 4: Run; all pass.**

### Task 6: Dev fixture, docs and Docker journey

- [ ] **Step 1: Write failing test** `TestDevFixtureCatalog`: after `Run`, these exist, each marked `seeded`:
  - the admin groups `IT Operations` (members: infosec1, manager) and `Vendor Desk` (member: hr);
  - Jira (API, owner `manager`): Viewer `[MANAGER]` and Admin `[MANAGER TOOL_OWNER INFOSEC]`;
  - GitHub (MANUAL, owner `manager`): Developer `[MANAGER TOOL_OWNER]`;
  - AWS (API, owner `infosec2`): Admin `[MANAGER TOOL_OWNER INFOSEC]`;
  - Payroll Portal (VENDOR, owner `hr`): User `[MANAGER TOOL_OWNER]`;
  - a starter package `Engineering` → Jira Viewer and GitHub Developer.

  A second `Run` creates nothing. An existing tool with the same name is left alone, as with employees in M3.
- [ ] **Step 2: Run and see it fail; implement through `people` and `catalog` services as the system actor; run and see it pass.** The fixture's employees need a department: dev1 and dev2 get `Engineering`.
- [ ] **Step 3: Docs.**
  - README: the catalog, starter package and notification API table; the Mock Tool Service API, its semantics and its no-auth limitation; M4 status.
  - Main plan: the M3 status becomes "merged as PR #3 (`fd5001c`)", and M4 links this plan.
- [ ] **Step 4: Full suite.** `docker compose --profile test run --rm tests` → exit 0 (gofmt, vet, race).
- [ ] **Step 5: Docker journey** on the throwaway `accessflow-m2check` project (port 18080; the user's volumes are untouched):
  - bootstrap and the password change;
  - `dev-fixture`;
  - a plain employee lists `/tools`: Jira Viewer and GitHub Developer are `recommended`, and the employee sees no versions;
  - the Super Admin adds InfoSec to Jira Viewer, and infosec1 sees one notification and marks it read;
  - retiring the Payroll Portal hides it from the employee, while the Auditor still sees it;
  - the `mocktools` container is healthy and its migration log line appears.

  The mock API is not published (A2); it is covered by Tasks 4–5. Tear down the throwaway stack afterwards.
- [ ] **Step 6: Reviews.** Superpowers `requesting-code-review` (independent subagent) and `ponytail-review`; fix the findings and re-run the suite.
- [ ] **Step 7: Report and ask for commit approval.** Proposed message: `feat: add tool catalog, approval steps, notifications and mock tool service`.

## Acceptance criteria (from the main plan)

- Catalog changes are audited; adding a step notifies InfoSec (Task 2).
- A repeated Mock Tool operation ID has no extra effect (Tasks 4, 5).
- Revoking one level never removes another level or the account (D4) (Task 4).
- Only a Super Admin edits the catalog; a retired level is not requestable; the client treats 5xx, timeouts and malformed bodies as errors (Tasks 2, 3, 5).

## Not in M4

- Requests, the approval snapshot, stricter-wins and routing (M5).
- The operation runner, retries, verification and the `MOCKTOOLS_URL` wiring (M6).
- Manual and Vendor tasks (M7).
- "Approved under earlier policy" (M11).
- Mock Tool account listing for orphans (M12).
- UI (carried into U1's catch-up).
