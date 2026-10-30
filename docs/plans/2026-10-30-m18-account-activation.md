# M18 — Account activation links, password policy and login rate limiting

> Status: **approved 2026-10-30** with three refinements (reset warning, minimal demo seed, fail-safe mail key), folded in below. Branch `feat/account-activation`, from `main` at `7bb3fef`. Scope fixed by the user on 2026-10-30: activation links (not temporary passwords) for new employees, Mock Email Outbox as the only delivery channel, local demo only, no real email, external services or production infrastructure. Production limits go to README.md.

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:executing-plans (or subagent-driven-development). Steps use `- [ ]`. TDD for every task: failing test first, see it fail for the right reason, minimal code, green, refactor.

**Goal:** an employee created by hand, by the Excel import or by rehire receives a single-use, 24-hour activation link in the Mock Email Outbox, sets their own password on a dedicated "Şifreni Belirle" page, then signs in normally; a Super Admin monitors and reissues activations; login gains per-address rate limiting and new passwords are checked against common passwords.

**Architecture:** a 256-bit random token per issuance; the database keeps only its SHA-256 hash (`activation_tokens`). The mock email row keeps the raw token **encrypted** (AES-256-GCM, Go standard library) with a key stored in a file on the `appdata` volume, never in Postgres, so links survive app restarts while database dumps reveal nothing. The ciphertext is cleared on use, reissue and expiry. Activation never signs anyone in.

**Tech stack:** Go standard library (`crypto/rand`, `crypto/sha256`, `crypto/aes`, `crypto/cipher`, `embed`), existing pgx/chi/React. **No new dependency.**

## Design

### Flow

1. **Issue** (create, import, rehire, reissue): in the caller's transaction, `auth.IssueActivation` revokes the employee's open token, deletes their password credential (if any), increments `token_version` (ends sessions), clears a lockout, inserts the token hash (expires `now + 24h`), inserts one `ACTIVATION` mock email whose `sealed_token` is the encrypted raw token, and audits `ACTIVATION_LINK_ISSUED` (no token). A failed transaction leaves nothing.
2. **Deliver (mock):** a Super Admin opens the outbox (Settings) and presses "Bağlantıyı göster" on an activation email. `GET /api/v1/email-outbox/{id}/activation-link` (Super Admin only, `Cache-Control: no-store`) decrypts it only if the token is still open, unexpired and the employee `ACTIVE`; every reveal is audited `ACTIVATION_LINK_VIEWED`. The UI shows a clickable link `<origin>/sifre-belirle#<token>`.
3. **Activate:** the public "Şifreni Belirle" page reads the token from the URL fragment (never sent to the server or in a Referer), removes it from the address bar at once, and posts `{token, newPassword}` to `POST /api/v1/auth/activate`. In one transaction: lock the token row, check open/unexpired/employee `ACTIVE`, set the permanent password (argon2id), mark the token used, clear `sealed_token`, increment `token_version`, clear lockout, audit `ACTIVATION_COMPLETED`. **No session cookie is set.** The page tells the employee to sign in.
4. **Sign in** through the normal login.

### Data (migration `00016_activation.sql`)

```sql
CREATE TABLE activation_tokens (
    id          uuid        PRIMARY KEY DEFAULT uuidv7(),
    employee_id uuid        NOT NULL REFERENCES employees (id),
    token_hash  bytea       NOT NULL UNIQUE CHECK (length(token_hash) = 32),  -- SHA-256 of the raw token
    reason      text        NOT NULL CHECK (reason IN ('CREATED', 'IMPORTED', 'REHIRED', 'RESET')),
    issued_by   uuid        REFERENCES employees (id),                        -- NULL: system (demo seed)
    created_at  timestamptz NOT NULL,
    expires_at  timestamptz NOT NULL,
    used_at     timestamptz,
    revoked_at  timestamptz,
    CHECK (used_at IS NULL OR revoked_at IS NULL)
);
CREATE UNIQUE INDEX activation_tokens_one_open ON activation_tokens (employee_id) WHERE used_at IS NULL AND revoked_at IS NULL;

ALTER TABLE email_outbox
    ALTER COLUMN request_id DROP NOT NULL,
    ADD COLUMN kind          text  NOT NULL DEFAULT 'ACCESS_REQUEST' CHECK (kind IN ('ACCESS_REQUEST', 'ACTIVATION')),
    ADD COLUMN activation_id uuid  UNIQUE REFERENCES activation_tokens (id),
    ADD COLUMN sealed_token  bytea,
    ADD CHECK ((kind = 'ACCESS_REQUEST') = (request_id IS NOT NULL)),
    ADD CHECK ((kind = 'ACTIVATION') = (activation_id IS NOT NULL)),
    ADD CHECK (sealed_token IS NULL OR kind = 'ACTIVATION');

GRANT SELECT, INSERT, UPDATE ON activation_tokens TO accessflow_app;
GRANT UPDATE (sealed_token) ON email_outbox TO accessflow_app;  -- only to clear it
GRANT DELETE ON password_credentials TO accessflow_app;         -- a reset removes the current password (⚑6)
```

Status is derived, never stored: `COMPLETED` (used), `REPLACED` (revoked), `EXPIRED` (open, `expires_at <= now`), `PENDING` (open, unexpired); `NO_LINK` for an `ACTIVE` employee with no password and no open or used token (never linked, or last link revoked by `reset-mail-key`).

### Mock delivery key (fail-safe, refinement 3)

`MOCK_EMAIL_KEY_FILE` (default `/var/lib/accessflow/mock-email.key`, on the existing `appdata` volume), 32 random bytes, mode 0600. AES-256-GCM, random nonce, additional data = outbox id ‖ employee id, so a ciphertext copied to another row fails. At startup:

| Key file | Encrypted links in the database (`sealed_token IS NOT NULL`) | Result |
|---|---|---|
| present, 32 bytes | any | used |
| absent | none | created (first start, or every link already used, expired or cleared) |
| absent | some | **not created**; mock email unavailable |
| unreadable or wrong size | any | **never overwritten**; mock email unavailable |

While unavailable the app still runs and logs one error naming the recovery command (no secret in it). Reveal answers `410 activation_link_unavailable`; issuing a link (create, import, rehire, reissue) answers `503 mock_email_unavailable` and changes nothing. The UI explains: "Etkinleştirme bağlantısı görüntülenemiyor; yeni bir bağlantı gönderin" / "Mock e-posta anahtarı kullanılamıyor; yönetici `reset-mail-key` komutunu çalıştırmalı". A key that decrypts nothing (replaced by hand) also gives `410`, never a 500.

Recovery is explicit, never automatic: `docker compose exec app /accessflow reset-mail-key` refuses if the current file is a readable valid key; otherwise, in one transaction, it revokes every open activation token, clears every `sealed_token` and audits `MOCK_EMAIL_KEY_RESET {revokedLinks}` with no actor, then writes a new key file (the app picks it up on restart, which the command tells the operator). A Super Admin then reissues the links from the activation screen, where those employees show as needing a new link. `docker compose up` still needs no setup.

### Password rules (new passwords only: activation and password change)

12–128 characters (unchanged), plus: not in an embedded common-password list (`internal/auth/common_passwords.txt`, lowercase, the SecLists top-100k list filtered to 12–128 characters, source and licence noted in the file header; checked case-insensitively), not equal to the email and not containing its local part when that is at least 4 characters. Demo passwords from `DEMO_PASSWORDS` are not checked against the list (they still pass the length rule as today).

### Login rate limiting

A fixed window of **10 attempts per minute per client address**, shared by `POST /auth/login` and `POST /auth/activate`, in addition to the account lockout. The key is `r.RemoteAddr` only (IPv4 address; IPv6 /64); **`X-Forwarded-For` is ignored** because the app is exposed directly. Over the limit: `429 too_many_attempts` with `Retry-After`; one `slog.Warn` per address per window, no audit event. At most 10 000 tracked addresses; expired windows are pruned, and if still full new addresses are refused (fail closed). `auth.Options.LoginRateLimit` (0 = the default) lets tests set their own.
*Amended 2026-10-30 (approved):* the default is **60** attempts per minute, configurable with `LOGIN_RATE_LIMIT` (DECISIONS.md A4 note).
`shortcut: trusts the socket address only, add a trusted-proxy setting if a reverse proxy is ever put in front`.

### Authorization (backend-enforced)

| Action | Who | Unchanged rules kept |
|---|---|---|
| Create, import, rehire | HR Manager, Super Admin | as today (M17, D7) |
| Reissue (`POST /employees/{id}/password-reset`, now **204**) | HR Manager, Super Admin | `CheckAccountAction`: never self; Super Admin or InfoSec targets need a Super Admin; target must be `ACTIVE` |
| Reveal link (`GET /email-outbox/{id}/activation-link`) | **Super Admin only** | session must be current and not temporary (existing middleware) |
| Activation list (`GET /activations`) | **Super Admin only** | |
| Outbox list | Super Admin sees all; InfoSec and Auditor see only `ACCESS_REQUEST` (as today) | no list ever contains a token |
| Activate | the link holder, no session; same-origin check (`CrossOriginProtection`) and rate limit | |

Bootstrap Super Admin and `recover-admin` are unchanged (one-time password in the bootstrap file): no Super Admin exists yet to read the outbox.

### Secrets never leave through

API responses other than the reveal endpoint (create, rehire and reset no longer return `temporaryPassword`), audit events (only token ids and expiry), logs (the token is never formatted; errors carry no request body), the outbox `body`/`subject` (the body says a link is waiting, without it), React Query caches (the reveal is fetched into component state only). `<meta name="referrer" content="no-referrer">` in `web/index.html`.

### Super Admin impersonation (accepted for the local demo)

While mail is simulated, the outbox reader can use a pending link. Limits: Super Admin only, every reveal audited, single use and 24 h, and `ACTIVATION_COMPLETED` records the ids of Super Admins who revealed that link, so "revealed by X, then completed" is visible in the Audit Explorer; the real employee finds the link used and asks for a new one. Permanent passwords are never retrievable. With real email the reveal endpoint is removed (README, future considerations).

### Decisions to approve (⚑)

1. **Reset** of an already-activated employee now sends an activation link and **immediately removes the current password** and sessions (before: a 72-hour temporary password). Same permissions. The UI always asks for confirmation; when the employee has a password (directory field `hasPassword`) the dialog warns: "Bu çalışanın mevcut parolası ve tüm açık oturumları hemen geçersiz olur. Yeni bağlantı 24 saat geçerlidir ve tek kullanımlıktır." (refinement 1). Temporary passwords remain only for the bootstrap Super Admin and `recover-admin`.
2. HR Managers keep create/import/rehire/reissue, but only a **Super Admin** can see the links.
3. **Demo filler employees** (refinement 2) are created with **no password and no link**: `NewEmployee.Seed` marks a seed-created employee, which gets `SeedPassword` as a permanent password if set and otherwise nothing; seeded employees never get a link. The outbox stays empty after first start, the filler employees show as `NO_LINK` on the activation screen (a Super Admin can send one to demonstrate the flow), and the seven demo accounts keep their permanent passwords. Imported, hand-created and rehired employees always get a link.
4. The page is titled **"Şifreni Belirle"** as requested, although the rest of the UI says "Parola".
5. Existing temporary passwords in an existing database keep working until they expire or are changed; no data migration.
6. The app gets `DELETE` on `password_credentials` and `UPDATE (sealed_token)` on `email_outbox`: a credential and a mock ciphertext are not business records; employees, tokens, emails and audit stay append-only.
7. Rate limit 10/min per address, shared by login and activate, fail closed at 10 000 addresses.

## Global constraints

- Token: 32 bytes from `crypto/rand`, base64url without padding (43 characters); stored as SHA-256; lifetime **24 hours**; single use.
- Password: 12–128 characters (runes), common-list and email checks on new passwords only.
- Rate limit: 10 attempts / 60 s / address; IPv6 keyed by /64; `X-Forwarded-For` ignored.
- No new Go or npm dependency. SQL parameterized; every state change and its audit event in one transaction.
- Turkish UI copy fixed by the user: page title "Şifreni Belirle"; menu item "Google Authenticator ile iki adımlı doğrulama"; its message "Bu özellik geliştirme aşamasındadır."
- Errors (new): `invalid_activation_link` (400, every token failure, one generic answer), `activation_link_unavailable` (410), `mock_email_unavailable` (503), `common_password`, `password_contains_email` (field `newPassword`), `too_many_attempts` (429).
- Audit actions (new): `ACTIVATION_LINK_ISSUED {activationId, reason, expiresAt}`, `ACTIVATION_LINK_VIEWED {activationId}`, `ACTIVATION_COMPLETED {activationId, viewedBy}`, `ACTIVATION_FAILED {reason}` (only when the token matched a row). `PASSWORD_RESET` is no longer written; reissue is `ACTIVATION_LINK_ISSUED{reason: RESET}`.

## Review focus

1. A link opened while another user (e.g. the Super Admin) is signed in in the same browser: the page still works and does not touch that session → frontend test.
2. Two concurrent activations with the same token: exactly one succeeds → API test with `testutil.WhileLocked`.
3. A weak password submitted with a valid token: field error, token still usable → API test.
4. Reissue while the old link is open on the employee's screen: the old one fails with the generic error → API test.
5. Import that fails validation midway: no token, email or audit row remains → import test.

---

### Task 1: Password policy

**Files:** Modify `internal/auth/rules.go`, `internal/auth/service.go` (`changePassword`); Create `internal/auth/common_passwords.txt`; Test `internal/auth/rules_test.go`, `internal/auth/service_test.go`.

**Produces:** `func ValidateNewPassword(pw, email string) error` (length, common list, email rules; `ValidatePassword` stays for the demo passwords).

- [ ] Table test `TestValidateNewPassword`: 11 chars → `password_length`; 12 chars random → nil; 129 → `password_length`; `"Password1234"` and its upper-case form → `common_password`; `"ayse.yilmaz-2026!"` for `ayse.yilmaz@x.com` → `password_contains_email`; local part `"ab"` (< 4) not checked.
- [ ] API test: `/auth/password` with a common password → 400 field `newPassword`, code `common_password`; the demo passwords from `compose.yaml` still sign in.
- [ ] Run red, implement, run green: `docker compose --profile test run --rm tests go test ./internal/auth/ -run 'ValidateNewPassword|Password'`.

### Task 2: Login rate limiting

**Files:** Create `internal/auth/ratelimit.go`; Modify `internal/auth/service.go` (`Options.LoginRateLimit`), `internal/auth/http.go` (`handleLogin`); Test `internal/auth/ratelimit_test.go`, `internal/auth/service_test.go`.

**Produces:** `type attemptLimiter`; `func (l *attemptLimiter) allow(key string, now time.Time) (bool, time.Duration)`; `func clientKey(remoteAddr string) string`; `(*Service).limited(w, r) bool` used by login and (Task 3) activate.

- [ ] Table tests: `clientKey("192.0.2.1:5000") == "192.0.2.1"`; `"[2001:db8:1:2:3::4]:5000"` and `"[2001:db8:1:2:ffff::1]:1"` give the same key; 10 allowed then denied with retry > 0; allowed again after 60 s; at capacity with only live windows a new key is denied, with expired ones it is pruned and allowed.
- [ ] API test: 11th login from one address → 429, `Retry-After` set, even with the right password; another address → normal answer; varying `X-Forwarded-For` does not reset the count; existing lockout tests still pass.
- [ ] Red, implement, green.

### Task 3: Activation core (migration, key, issue, reveal, activate)

**Files:** Create `internal/platform/migrations/00016_activation.sql`, `internal/auth/activation.go`, `internal/auth/mailkey.go`, `internal/auth/mailkey_cmd.go` (or in `recover.go`'s style); Modify `internal/auth/http.go` (route `POST /activate`), `internal/auth/service.go` (`Options.MailKey`), `internal/platform/config.go` (`MOCK_EMAIL_KEY_FILE`), `cmd/accessflow/main.go` (load key, register routes); Test `internal/auth/activation_test.go`, `internal/auth/mailkey_test.go`, `internal/platform/identity_schema_test.go`.

**Produces:**
- `func LoadMailKey(ctx context.Context, pool *pgxpool.Pool, path string) ([]byte, error)` — the startup table above; returns `nil, nil` when unavailable (the service then answers 410/503) and an error only for a database failure.
- `func ResetMailKey(ctx context.Context, pool *pgxpool.Pool, path string) (int, error)` — the recovery command, wired in `cmd/accessflow` as `reset-mail-key`.
- `func (s *Service) IssueActivation(ctx context.Context, tx pgx.Tx, now time.Time, actor *uuid.UUID, employeeID uuid.UUID, reason string) error`
- `func (s *Service) RegisterActivations(r chi.Router)` — `GET /email-outbox/{id}/activation-link`, `GET /activations` (Task 5), mounted behind `RequireSession`.
- `const ActivationLifetime = 24 * time.Hour`.

- [ ] Schema test: the app role cannot `DELETE` from `activation_tokens`/`email_outbox`, cannot `UPDATE email_outbox.body`, can clear `sealed_token`; two open tokens for one employee violate `activation_tokens_one_open`.
- [ ] Unit tests: seal/open round-trip; open with another row's additional data fails.
- [ ] Key tests (real Postgres): absent + no sealed rows → created 0600, 32 bytes, same key next start; absent + sealed rows → no file created, reveal 410, create employee 503 with no employee, token or email written; wrong-size or unreadable file → left untouched, same answers; `reset-mail-key` refuses with a valid key, otherwise revokes open tokens, clears ciphertexts, audits `MOCK_EMAIL_KEY_RESET` and writes a key, after which (new service) issuing works.
- [ ] API tests (real Postgres), each asserting the raw token never appears in `audit_events.details`, `email_outbox.subject/body`, or the captured `slog` output:
  - issue → one token row (hash only), one `ACTIVATION` email with `sealed_token`, `ACTIVATION_LINK_ISSUED`; employee's sessions ended and password row gone.
  - reveal: Super Admin 200 + `no-store` + `ACTIVATION_LINK_VIEWED`; HR Manager, InfoSec, Auditor, employee 403; anonymous 401; bootstrap Super Admin with a temporary password 403 `password_change_required`.
  - activate: 200, **no `Set-Cookie`**, `/me` still 401; normal login then succeeds; same token again → 400 `invalid_activation_link`; `sealed_token` is NULL and reveal → 410.
  - expired (set `expires_at` in the past with the owner connection) → activate 400, reveal 410; reissue → old token 400, old `sealed_token` NULL, new one works; suspended employee → 400.
  - weak password with a valid token → field error, token still usable (Review focus 3); concurrent use → one success (Review focus 2).
  - restart: a new `auth.Service` with the same key file reveals the link; with a different key → 410, not 500.
  - activate shares the rate limit with login.
- [ ] Red, implement, green.

### Task 4: Wire every credential path to activation links

**Files:** Modify `internal/people/service.go` (`CreateEmployee`, `ResetPassword` → `IssueActivationLink`, remove `issueTemporaryPassword`), `internal/people/lifecycle.go` (`Rehire`), `internal/people/http.go`, `internal/onboarding/service.go` (+ `NewService(pool, authSvc)`), `internal/demo/demo.go`, `cmd/accessflow/main.go`; remove now-unused employee temporary-password code in `internal/auth/rules.go`. Test `internal/people/reset_test.go`, `internal/people/employees_test.go`, `internal/onboarding/import_test.go`, `internal/demo/demo_test.go`.

**Produces:** `Employee.HasPassword bool` (full-directory roles only, like `MustChangePassword`); `CreateEmployee(...) (Employee, error)`, `Rehire(...) (Employee, error)`, `IssueActivationLink(ctx, actor, id) error`; HTTP create/rehire return `{employee}`, reset returns 204.

- [ ] Tests: create (`reason CREATED`), rehire (`REHIRED`, old password row removed), reset (`RESET`) each issue one link and their responses contain no token or password; reset rules unchanged (HR on InfoSec → 403 `privileged_reset_requires_super_admin`, self → 403, terminated → refused); reset of an activated employee → old password no longer signs in, sessions end.
- [ ] Import test: a workbook with 3 employees → 3 tokens, 3 activation emails, 3 `ACTIVATION_LINK_ISSUED` (`reason IMPORTED`); a workbook failing at the access sheet → no token, email or audit row (Review focus 5).
- [ ] Demo test: the seven accounts sign in with `DEMO_PASSWORDS`; every filler employee has no `password_credentials` row and no token; the outbox has no `ACTIVATION` row after seeding.
- [ ] Red, implement, green.

### Task 5: Activation list, outbox kinds, expiry cleanup

**Files:** Modify `internal/auth/activation.go` (`GET /activations`), `internal/requests/service.go` (`Outbox`: `kind`, `linkAvailable`, kind filter), `internal/sweeps/sweeps.go` (clear expired `sealed_token`); Test `internal/auth/activation_test.go`, `internal/requests/*_test.go`, `internal/sweeps/*_test.go`.

**Produces:** `GET /api/v1/activations` → `{activations: [{employee:{id,fullName,email}, status, reason, createdAt, expiresAt, completedAt}]}`, latest token per `ACTIVE` employee plus `NO_LINK` rows; `func ClearExpiredActivationLinks(ctx, pool) (int, error)`.

- [ ] Tests: statuses `PENDING`, `EXPIRED`, `COMPLETED`, `NO_LINK` (an M17-imported employee without a token); non–Super Admin → 403; InfoSec and Auditor outbox lists contain no `ACTIVATION` rows; no list response contains a token; the sweep clears expired ciphertexts and leaves open ones.
- [ ] Red, implement, green.

### Task 6: Frontend

**Files:** Create `web/src/pages/SetPassword.tsx`; Modify `web/src/App.tsx` (public `/sifre-belirle` before the session check), `web/src/pages/Settings.tsx` (outbox kinds, "Bağlantıyı göster" with clickable link, "Hesap etkinleştirme" table with status filter and "Yeni bağlantı gönder"), `web/src/pages/EmployeeDetail.tsx`, `web/src/pages/Employees.tsx` (reset/reissue always confirmed, with the password-and-sessions warning when `hasPassword`; remove password modals, toast "Etkinleştirme bağlantısı Mock Email Outbox'a gönderildi"), `web/src/Shell.tsx` (MFA item with "Planlanıyor" badge → modal "Bu özellik geliştirme aşamasındadır."), `web/src/i18n/errors.ts`, `web/src/i18n/audit.ts`, `web/src/api/types.ts`, `web/index.html` (referrer meta); Tests in `web/src/pages/*.test.tsx`, `web/src/App.test.tsx`, MSW fixtures.

- [ ] Tests: `/sifre-belirle#tok` posts `{token: "tok", newPassword}`, the hash is removed from the URL before submit, success shows a "Giriş yap" link and sets no principal; works while a session exists (Review focus 1); `invalid_activation_link` and `common_password` messages shown; Settings reveal renders `<a href=".../sifre-belirle#…" rel="noreferrer">`; activation table filters and reissues; employee create/reset/rehire show the toast, no password; reissue for an employee with a password shows the warning and sends nothing until confirmed; `mock_email_unavailable` and `activation_link_unavailable` show their explanations; the MFA item shows the message and nothing else.
- [ ] `npm run test`, `npx tsc --noEmit`, `npm run build`.

### Task 7: Documentation

**Files:** `DECISIONS.md` (dated notes: F4 activation links, 24 h, outbox only, Super Admin reveal, M17 "no password" note superseded; A4 password rules, rate limit, mail key, grants; A5 rehire link; O2 second email kind; M15 demo filler; planned MFA, mandatory for privileged roles later), `README.md` (activation flow; **"Mock email delivery is for local demonstration only and is not suitable for production"**; production limitations and future considerations: real email delivery replaces the reveal endpoint, trusted-proxy setting for rate limiting, MFA, JWT secret in Postgres, breached-password checks, impersonation by the outbox reader), `docs/known-limitations.md`, `docs/api.md`, `docs/operating-summary.md` (onboarding wording).

- [ ] Review each changed statement against the code.

### Finish

- [ ] Full suite in Docker and CI; `gofmt`, `go vet`, `go test -race ./...`, frontend checks.
- [ ] Superpowers code review + Ponytail review of the branch diff; fix; re-run.
- [ ] Verify each requirement (1–14 and the security rules of 2026-10-30) one by one; report; ask before commit, push and PR.

## Size and risk

Medium: one migration, ~4 backend files changed substantially, one new page. Risks: the API change to create/rehire/reset (frontend updated in the same milestone); the common-password list's licence (checked when the file is added; if unclear, a smaller self-written list of common patterns is used instead and reported); the rate limiter on Docker Desktop may see every client as one address (harmless for a single-user demo).
