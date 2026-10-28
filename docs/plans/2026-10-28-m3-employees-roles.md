# M3 — Employees, Roles and Development Fixture: Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Status:** approved 2026-10-28 (decision 1: recommended option; all listed calls approved; recorded in DECISIONS.md F4, D2, D12). Branch `feat/identity` (shared PR with M2, no push until M3 is reviewed).

**Goal:** HR creates and edits employees and resets passwords; a Super Admin manages roles and admin groups; a small opt-in development fixture makes the API usable by hand; the three M2 review follow-ups are closed.

**Architecture:** a new `internal/people` package (A8: `rules.go` pure checks, `service.go` transactions with SQL inline like `auth`, `http.go` handlers) mounted under `/api/v1` behind `auth.RequireSession`. `auth` keeps owning credentials and the session principal and gains admin-group memberships. `internal/devfixture` calls the `people` service. No new dependencies.

**Tech Stack:** Go 1.27, chi v5, pgx v5, goose v3, PostgreSQL 18 (as M2).

**Spec:** `CASE.md`, `DECISIONS.md` (F4, D2, D5 job-title rule, D12, A4, A5, A8, A9, A10), the approved plan `docs/plans/2026-10-28-implementation-plan.md` (M3 section and the M2 review follow-ups).

## Global Constraints

- Everything in the main plan's Global Constraints applies.
- A state change and its audit event are written in one transaction; employees, roles and memberships are never physically deleted. Removal closes an interval (`valid_to`, `revoked_by`) (A5, A9).
- Roles and memberships are read from the database on every request (A4); a removed role or membership applies on the next request.
- Temporary passwords: 16 characters, shown once in the create/reset response only (`Cache-Control: no-store`), never logged or audited, 72-hour expiry, the session may only change the password (F4).
- Resets of privileged accounts (holding `SUPER_ADMIN` or `INFOSEC`) need a Super Admin other than the target (F4). Reset increments `token_version` (A4).
- Role and group changes are made by a Super Admin, with audit (D2). `SUPER_ADMIN` itself is not assignable here: two-person promotion is M14 (D2).
- Mutable rows carry `version`; a stale update returns `409 version_conflict` (A9).
- In M3 a department or manager change only updates the record and audits it. The D5 transfer workflow (revocation pre-selection) is M10. A job-title-only change stays audit-only (D5).
- API errors: `{"error": "<code>", "field"?: "<fieldName>"}` (A7).

## Decisions needed before implementation

1. **Who creates employees, edits them and resets passwords.** F4 says "HR creates the employee". But right after `docker compose up` the bootstrap Super Admin is the only employee, so nobody could be made HR Manager without the Super Admin first giving themselves the role.
   - **Recommended:** HR Manager **and** Super Admin may create and edit employees and reset passwords. Privileged-reset and self rules still apply.
   - **Alternative:** HR Manager only, and a Super Admin may assign roles to themselves. This weakens decision 2 and gives no extra control.
2. **My calls, listed for you to check** (each follows from D1/D2/F4 and keeps the rules small):
   - **No self-changes:** an actor never changes their own roles or group memberships and never resets their own password (they use password change instead). This matches D1's "never for the Super Admin's own access".
   - **Active targets only:** reset, role and membership changes need the target to be `ACTIVE` (suspension and termination effects are M9). Editing a `TERMINATED` employee is rejected (rehire is M10).
   - **Manager must be active:** a new manager must be `ACTIVE`, must not be the employee, and must not create a cycle.
   - **Directory visibility:**
     - Super Admin, HR Manager, InfoSec and Auditor see every employee with every field: status, roles, groups, whether a password change is pending, version.
     - Everyone else sees only `ACTIVE` employees with id, email, name, department, job title and manager (needed later to pick beneficiaries and approvers).
   - **Admin groups:** created by a Super Admin with a unique name. Renaming and retiring are not in M3; groups are referenced by tools in M4.
   - **Dev fixture is opt-in:** it runs only through `docker compose exec app /accessflow dev-fixture` and never runs automatically. Its users share the local-only `SEED_PASSWORD` (Compose default `accessflow-local-demo`), as A4 describes for seeded users.

## Review Focus

1. **Self-escalation:** an actor targeting themselves (role, group, reset) gets `403 self_change_forbidden` and no audit event. Pinned in Task 4–6 tests.
2. **Lost updates:** two edits with the same `version`. Exactly one succeeds; the other gets `409 version_conflict`. Pinned in Task 3.
3. **Manager cycles under concurrency:** A→B and B→A submitted at the same time. One succeeds, the other gets `manager_cycle`. Pinned in Task 3.
4. **Temporary-password leaks:** the password appears only in the one response. It is never in logs or audit details, the response carries `Cache-Control: no-store`, and the old password and other sessions stop working after a reset. Pinned in Tasks 2 and 4.
5. **Directory leaks:** a plain employee sees no terminated or suspended employees, no roles and no versions. Pinned in Task 2.

## Files

```
internal/platform/http.go                 DecodeJSON rejects trailing data            (Task 1)
internal/platform/migrations/00003_people.sql  email CHECK, admin groups, seeded flag (Tasks 1, 6)
internal/auth/rules.go                    NormalizeEmail                              (Task 1)
internal/auth/service.go                  login/bootstrap use NormalizeEmail; temporary_password_expired;
                                          Principal.AdminGroups                        (Tasks 1, 6)
internal/testutil/http.go                 Client, Response, RequireStatus (moved from auth tests)
internal/testutil/fixtures.go             EmployeeSpec gains Password, Manager; testutil.Password(Hash)
internal/people/rules.go                  pure permission and field checks
internal/people/service.go                transactions + SQL
internal/people/http.go                   handlers, Routes
internal/devfixture/devfixture.go         Run(ctx, svc, password)
cmd/accessflow/main.go                    mount people routes; `dev-fixture` subcommand
compose.yaml, README.md, docs/plans/…     SEED_PASSWORD; API and fixture docs; status
```

---

### Task 1: M2 follow-ups: email rule, expired temporary password, trailing JSON

**Interfaces — Produces:**
- `auth.NormalizeEmail(s string) (string, error)`, which:
  - trims ASCII whitespace and lowercases;
  - accepts only printable ASCII without spaces, exactly one `@` with non-empty local and domain parts, and at most 254 characters;
  - on failure returns `platform.ValidationError("email", "invalid_email")`.
- Error code `temporary_password_expired` (400, field `currentPassword`).

- [ ] **Step 1: Write failing tests**
  - `TestNormalizeEmail` (table, `auth/rules_test.go`): `" Admin@Example.COM "` → `"admin@example.com"`; rejected: `""`, `"a"`, `"a@"`, `"@b"`, `"a@b@c"`, `"a b@c.d"`, `"a\t@c.d"`, `"admİn@x.com"`, a 255-character address. A 254-character address is accepted.
  - `TestEmployeesEmailCheckMatchesTheGoRule` (`platform/identity_schema_test.go`): inserting `"admİn@x.com"`, `"a\tb@x.com"` and a 255-character email fails with SQL state `23514`.
  - `TestBootstrapRejectsAnInvalidEmail`: `Bootstrap(ctx, "admİn@x.com")` returns an error and creates no employee.
  - `TestLoginWithAnInvalidEmailLooksUnknown`: `"a\tb@x.com"` → 401 `invalid_credentials`, `LOGIN_FAILED` with reason `unknown_email`.
  - `TestExpiredTemporaryPasswordMidSession`: sign in with a temporary password, then set `expires_at = now() - interval '1 minute'`. Password change with the correct current password → 400 `temporary_password_expired` (field `currentPassword`). `failed_login_count` stays 0. `PASSWORD_CHANGE_FAILED` is recorded with details `{"reason":"temporary_password_expired"}`.
  - `TestDecodeJSONRejectsTrailingData` (`platform/http_test.go`): `{"a":1} junk` and `{"a":1}{"a":2}` → `invalid_json`; `{"a":1}\n` → ok.
- [ ] **Step 2: Run and see them fail.** `docker compose --profile test run --rm tests go test -count=1 ./internal/auth/ ./internal/platform/`
- [ ] **Step 3: Implement**
  - `NormalizeEmail` in `auth/rules.go`. `login` calls it: an invalid email is handled like an unknown one (dummy verify, capped email in the audit row). `Bootstrap` returns a wrapped error naming `BOOTSTRAP_ADMIN_EMAIL`.
  - Migration `00003_people.sql`: `ALTER TABLE employees ADD CONSTRAINT employees_email_format CHECK (length(email) <= 254 AND email ~ '^[!-?A-~]+@[!-?A-~]+$')`. The class is printable ASCII without `@`.
  - `changePassword`: a matching but expired credential records the reason, calls no `registerFailure`, and returns `errTemporaryPasswordExpired`.
  - `DecodeJSON`: after `Decode`, require `dec.Decode(&struct{}{}) == io.EOF`.
- [ ] **Step 4: Run and see them pass.** Same command.
- [ ] **Step 5: Mark the three follow-ups done** in the main plan's M2 follow-up list.

### Task 2: People package: create employee and the directory

**Interfaces**
- **Consumes:** `auth.Principal`, `auth.RequireSession`, `(*auth.Service).Hash(ctx, pw) (string, error)` (new: `HashPassword` behind the M2 hash-slot limit), `auth.NewTemporaryPassword`, `auth.TemporaryPasswordExpiry`, `auth.NormalizeEmail`, `audit.Record`, `platform.InTx`.
- **Produces:**
  - `people.NewService(pool *pgxpool.Pool, authSvc *auth.Service) *Service` and `(*Service).Routes() http.Handler`, mounted at `/api/v1` inside a `RequireSession` group in `main.go`.
  - `people.NewEmployee{Email, FullName string; Department, JobTitle *string; ManagerID *uuid.UUID; SeedPassword string}`. `SeedPassword` is set only by `devfixture` and is not part of the HTTP body.
  - `(*Service).CreateEmployee(ctx, actor auth.Principal, in NewEmployee) (Employee, string, error)`: the second return value is the temporary password, empty when `SeedPassword` was used.
  - `people.SystemActor`: a principal with `uuid.Nil` ID and the `SUPER_ADMIN` and `HR_MANAGER` roles, used only by `devfixture`. An actor with `uuid.Nil` is audited as the system (`ActorID` nil).
  - Pure rules: `canManageEmployees(p) bool` (Super Admin or HR Manager, pending decision 1) and `seesFullDirectory(p) bool` (Super Admin, HR Manager, InfoSec, Auditor).
- **Test support:**
  - Move the M2 cookie client (`client`, `response`, `requireStatus`) to `internal/testutil/http.go` as `Client`, `Response`, `RequireStatus`, and use it from `auth` tests. It does not import `auth`.
  - Add `testutil.Password` and a precomputed `testutil.PasswordHash`. `EmployeeSpec` gains `Password bool` and `ManagerID *uuid.UUID`.
  - `TestTestutilPasswordHashMatches` in `auth` keeps the constant from drifting.

**Endpoints**
- `POST /api/v1/employees` `{email, fullName, department?, jobTitle?, managerId?}` → `201 {employee, temporaryPassword}`, with `Cache-Control: no-store`.
- `GET /api/v1/employees` → `{employees: [...]}`, ordered by full name. There is no pagination (400 employees); add a `shortcut:` comment.
- **Full view:** `{id, email, fullName, department, jobTitle, managerId, status, roles, adminGroups: [{id, name}], mustChangePassword, version, createdAt}`.
- **Basic view:** `{id, email, fullName, department, jobTitle, managerId}`, active employees only.
- **Validation codes:**
  - `invalid_email`;
  - `full_name_required` (blank after trimming);
  - `value_too_long` with its field (full name over 200 characters; department or job title over 100);
  - `manager_not_found`, `manager_inactive` (field `managerId`).
- **Conflict:** `409 email_taken`, from the unique index.

- [ ] **Step 1: Write failing tests** (`people/service_test.go`, real auth and people routes on one test server):
  - `TestHRCreatesAnEmployeeWhoMustChangeThePassword`: an HR Manager creates `" New.Person@Example.com "` and gets 201. The stored email is lowercased. The temporary password is 16 characters and signs in with `mustChangePassword: true`. `expires_at` is about 72 hours away. There is one `EMPLOYEE_CREATED` event whose actor is the HR Manager and whose details do not contain the password. The response has `Cache-Control: no-store`.
  - `TestSuperAdminCanCreateEmployees`: 201. This test is deleted if decision 1 goes the other way.
  - `TestCreateEmployeeValidation` (table): every code above. The same email twice in different case → 409 `email_taken`.
  - `TestOnlyHRAndSuperAdminCreateEmployees`: plain employee, InfoSec and Auditor get 403 `forbidden` and no employee is created.
  - `TestDirectoryVisibility`:
    - HR, InfoSec, Auditor and Super Admin see a suspended and a terminated employee with roles and version;
    - a plain employee sees only active employees, and their JSON objects have exactly the six basic keys.
- [ ] **Step 2: Run and see them fail.** `docker compose --profile test run --rm tests go test -count=1 ./internal/people/`
- [ ] **Step 3: Implement** `rules.go`, `service.go` (`CreateEmployee` and `ListEmployees`, one transaction for the employee, credential and audit) and `http.go`; mount it in `main.go`.
- [ ] **Step 4: Run and see them pass**, including `./internal/auth/` after the helper move.

### Task 3: Edit employee: manager, department, job title

**Interfaces — Produces:**
- `(*Service).UpdateEmployee(ctx, actor auth.Principal, id uuid.UUID, in EmployeeUpdate) (Employee, error)`.
- `EmployeeUpdate{Version int; ManagerID *uuid.UUID; Department, JobTitle *string}`. It is a full replacement of these three fields; null clears one.
- `PUT /api/v1/employees/{id}` → `200` with the full view.

- [ ] **Step 1: Write failing tests**
  - `TestUpdateEmployee`: changing the department and manager returns 200 and increments `version`. One `EMPLOYEE_UPDATED` event records only the changed fields as `{"field": {"from": …, "to": …}}`. A job-title-only change records the same kind of event. An unchanged body returns 200 with no event and no version bump.
  - `TestUpdateEmployeeRejects`, each with its error code:
    - a stale version → 409 `version_conflict`;
    - an unknown id → 404 `not_found`;
    - a terminated target → 400 `employee_terminated`;
    - the employee as their own manager → 400 `manager_cycle`;
    - an indirect cycle (A→B→C, then C→A) → `manager_cycle`;
    - an inactive manager → `manager_inactive`;
    - a non-HR actor → 403.
  - `TestConcurrentManagerChangesCannotCreateACycle`: 10 rounds. Each round runs A.manager=B and B.manager=A in parallel goroutines. Exactly one succeeds and the other returns `manager_cycle`.
- [ ] **Step 2: Run and see them fail.**
- [ ] **Step 3: Implement.** The transaction:
  1. takes `pg_advisory_xact_lock(hashtext('accessflow manager tree'))` whenever the manager changes;
  2. locks the target row with `FOR UPDATE`;
  3. checks the version;
  4. checks for a cycle with a recursive CTE from the new manager upwards;
  5. updates the record and records the audit event.
- [ ] **Step 4: Run and see them pass.**

### Task 4: Password reset

**Interfaces — Produces:**
- `(*Service).ResetPassword(ctx, actor auth.Principal, id uuid.UUID) (string, error)`.
- Pure rule: `checkReset(actor auth.Principal, target uuid.UUID, targetRoles []string) error`.
- `POST /api/v1/employees/{id}/password-reset` → `200 {temporaryPassword}`, with `Cache-Control: no-store`.

- [ ] **Step 1: Write failing tests**
  - `TestCheckReset` (table, pure):
    - an HR Manager may reset a plain employee, an HR Manager or an Auditor;
    - an HR Manager may not reset an InfoSec member or a Super Admin (`privileged_reset_requires_super_admin`);
    - a Super Admin may reset an InfoSec member or another Super Admin;
    - nobody may reset themselves (`self_change_forbidden`);
    - other roles get `forbidden`.
  - `TestResetPassword`:
    - the target's existing session gets 401 on the next request;
    - the old password fails, and the new one signs in with `mustChangePassword: true`;
    - a locked target can sign in after the reset;
    - a `PASSWORD_RESET` event is recorded without the password;
    - a suspended target → 400 `employee_not_active`.
- [ ] **Step 2: Run and see them fail.**
- [ ] **Step 3: Implement.** One transaction:
  1. lock the target;
  2. load its open roles and status;
  3. run `checkReset`;
  4. upsert the credential (temporary, 72 hours);
  5. update the employee: `token_version + 1`, `failed_login_count = 0`, `locked_until = NULL`;
  6. record the audit event.

  Hashing runs before the transaction. Hash work goes through `(*auth.Service).Hash` from Task 2.
- [ ] **Step 4: Run and see them pass.**

### Task 5: Roles

**Interfaces — Produces:**
- `(*Service).AssignRole(ctx, actor, employeeID uuid.UUID, role string) error`.
- `(*Service).RemoveRole(ctx, actor, employeeID uuid.UUID, role string) error`.
- Pure rule: `checkRoleChange(actor auth.Principal, target uuid.UUID, role string) error`. Roles: `INFOSEC`, `HR_MANAGER`, `AUDITOR`.
- Endpoints:
  - `POST /api/v1/employees/{id}/roles {role}` → `204`;
  - `DELETE /api/v1/employees/{id}/roles/{role}` → `204`.

- [ ] **Step 1: Write failing tests**
  - `TestCheckRoleChange` (table):
    - only a Super Admin may change roles;
    - `SUPER_ADMIN` and unknown roles → 400 `invalid_role` (field `role`);
    - self → 403 `self_change_forbidden`.
  - `TestAssignAndRemoveRole`:
    - assigning gives the target access to an InfoSec-only probe route on the next request;
    - assigning again → 204 with no new row and no new event;
    - removing → the probe answers 403 on the next request, `valid_to` and `revoked_by` are set and the row still exists;
    - removing again → 204 with no event;
    - `ROLE_ASSIGNED` and `ROLE_REMOVED` record actor and role;
    - an inactive target → `employee_not_active`.
  - `TestConcurrentRoleAssignmentCreatesOneRow`: 10 parallel assignments of the same role give one open row and one `ROLE_ASSIGNED`.
- [ ] **Step 2: Run and see them fail.**
- [ ] **Step 3: Implement.** One transaction locks the target employee row (A9 employee lock), checks status and the open interval, then inserts or closes and audits.
- [ ] **Step 4: Run and see them pass.**

### Task 6: Admin groups

**Interfaces — Produces:**
- Migration `00003_people.sql`:
  - `admin_groups(id uuid PK uuidv7(), name text NOT NULL CHECK (btrim(name) <> '' AND length(name) <= 100), created_at, created_by uuid REFERENCES employees)`, with a unique index on `lower(name)`;
  - `admin_group_memberships(id, group_id, employee_id, valid_from, valid_to, granted_by, revoked_by)`, with one open membership per (group, employee);
  - the runtime user gets SELECT, INSERT and UPDATE on both, and no DELETE.
- `auth.Group{ID uuid.UUID; Name string}` and `auth.Principal.AdminGroups []Group`, loaded in `loadPrincipal` (open memberships only, ordered by name). No group-check helper yet; M7 (fulfillment tasks) is its first user.
- Endpoints:
  - `GET /api/v1/admin-groups` → `{groups: [{id, name, members?}]}`; members only for the full-directory roles;
  - `POST /api/v1/admin-groups {name}` → `201` (Super Admin only), `409 group_name_taken`;
  - `POST /api/v1/admin-groups/{id}/members {employeeId}` → `204`;
  - `DELETE /api/v1/admin-groups/{id}/members/{employeeId}` → `204`.

  Same self, active-target, idempotency and audit rules as roles: `ADMIN_GROUP_CREATED`, `ADMIN_GROUP_MEMBER_ADDED`, `ADMIN_GROUP_MEMBER_REMOVED`.

- [ ] **Step 1: Write failing tests**
  - `TestAdminGroupSchema`: the runtime user cannot delete groups or memberships (SQL state `42501`), and there is one open membership per pair.
  - `TestAdminGroups`:
    - create, then a duplicate name in other case → 409;
    - adding a member shows up in that member's `/me` response as `adminGroups` on the next request, and removing them takes it away on the next request;
    - self → 403;
    - a non-Super Admin → 403;
    - repeated add or remove → 204 with no new event.
  - `TestMeShowsAdminGroups`: the `/me` body gains `adminGroups: [{id, name}]`.
- [ ] **Step 2: Run and see them fail.**
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run and see them pass.**

### Task 7: Development fixture

**Interfaces — Produces:**
- `devfixture.Run(ctx context.Context, svc *people.Service, password string) ([]string, error)`. It returns the emails it created and skips any email that already exists.
- Subcommand: `docker compose exec app /accessflow dev-fixture`, reading `SEED_PASSWORD`.
- Migration: `employees.seeded boolean NOT NULL DEFAULT false`.
- People created:
  - `hr@accessflow.local` (HR Manager);
  - `infosec1@` and `infosec2@` (InfoSec);
  - `auditor@` (Auditor);
  - `manager@` (Engineering);
  - `dev1@` and `dev2@`, reporting to `manager@`.

  All are created through `CreateEmployee` and `AssignRole` with `SystemActor`, have permanent passwords and `seeded = true`, and their audit events carry `{"seed": true}` at real time.

- [ ] **Step 1: Write failing tests**
  - `TestDevFixtureIsIdempotent`:
    - the first run creates 7 employees and the role rows, and each employee signs in with the password;
    - the second run creates nothing, adds no audit events and returns an empty list;
    - all 7 have `seeded = true`;
    - no event has an actor.
  - `TestDevFixtureRejectsAShortPassword`: `password_length`, nothing created.
- [ ] **Step 2: Run and see them fail.**
- [ ] **Step 3: Implement**, plus the subcommand and `SEED_PASSWORD: accessflow-local-demo` in compose (documented as local-only, A4).
- [ ] **Step 4: Run and see them pass.**

### Task 8: Permission matrix, docs and verification

- [ ] **Step 1: Write `TestPermissionMatrix`** (`people/permissions_test.go`, table). Every M3 endpoint × {plain employee, HR Manager, InfoSec, Auditor, Super Admin} gives the expected status. In particular, the Auditor role alone gets 403 on every write.
- [ ] **Step 2: Run the full suite:** `docker compose --profile test run --rm tests`, expecting gofmt, vet and race all clean.
- [ ] **Step 3: Docs.**
  - README: Employees and Administration API table, the dev fixture, `SEED_PASSWORD`.
  - Main plan: M3 status, follow-ups closed.
  - DECISIONS.md: a dated note only for decision 1, once you decide it.
- [ ] **Step 4: Docker journey**, as a separate compose project on another port, with only its own volumes removed afterwards:
  1. the bootstrap admin creates an HR Manager;
  2. the HR Manager signs in and changes the password, then creates an employee;
  3. a password reset;
  4. a role and a group change taking effect on the next request;
  5. `dev-fixture` run twice.
- [ ] **Step 5: Reviews.** Independent Superpowers code review subagent plus Ponytail review; fix the findings; re-run everything.
- [ ] **Step 6: Report and ask for commit approval.** Proposed message: `feat: add employee management, roles, admin groups and dev fixture`.

## Not in M3 (where it goes)

- Transfer effects of a department or manager change (D5) → M10.
- Suspension, termination and rehire → M9, M10.
- Super Admin promotion, recovery and `recover-admin` → M14.
- Renaming or retiring admin groups, editing email or full name, directory search and pagination: not required by CASE or DECISIONS. Proposed only if a later milestone needs them.
- The UI slice (Employees, Administration) → U1 catch-up.
