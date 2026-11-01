# Running AccessFlow: configuration, first sign-in and recovery

The [README](../README.md#quick-start) covers the local demo (`docker compose up --build`). This page covers the settings, what changes outside demo mode, and the server-side recovery commands. The decisions behind them are in [`DECISIONS.md`](../DECISIONS.md) (A2, A4, D2, F4).

## Demo mode

`compose.yaml` starts a local demo: `ACCESSFLOW_DEMO=true`, `ACCESSFLOW_ENV=local` and `DEMO_PASSWORDS`. On the first start, on an empty database, the server creates the demo company through the real services, at real time (D12). The documented accounts get the passwords from `DEMO_PASSWORDS`; the other demo employees get no password and no activation link. The seed runs once and records that it ran, so later starts change nothing: passwords changed in the app, created records and audit history stay.

The server refuses to start in demo mode unless `ACCESSFLOW_ENV=local`, `COOKIE_SECURE` and `JWT_SECRET` are unset, every documented account has a distinct valid password, and both database URLs point at the Compose `postgres` service with no query parameters (A4). A real deployment removes the three demo lines from `compose.yaml`.

If the log says `demo seed: the database holds data from an interrupted start or an earlier version`, start a separate project with its own fresh volumes. This leaves the existing data untouched:

```bash
docker compose down                   # keeps the existing volumes
docker compose -p accessflow-demo up  # fresh volumes named accessflow-demo_*
```

`docker compose down -v` deletes the data and is never needed for the demo.

## Configuration

All optional:

| Variable | Default | Meaning |
|---|---|---|
| `ACCESSFLOW_PORT` | `8080` | host port of the app (Compose only; the container port stays 8080) |
| `JWT_SECRET` | generated on first start and stored in the database | session signing secret, at least 32 bytes |
| `COOKIE_SECURE` | `false` | set `true` when served over HTTPS |
| `BOOTSTRAP_ADMIN_EMAIL` | `admin@accessflow.local` | the initial Super Admin |
| `BOOTSTRAP_FILE` | `/var/lib/accessflow/bootstrap-admin.txt` | where its one-time password is kept (the `appdata` volume) |
| `ACCESSFLOW_DEMO` | `false` (`true` in `compose.yaml`) | local demo mode: demo accounts and seed; refused outside the local setup |
| `ACCESSFLOW_ENV` | none (`local` in `compose.yaml`) | must be `local` for demo mode |
| `DEMO_PASSWORDS` | none (set in `compose.yaml`) | `account=password,…` for `admin`, `infosec`, `hr`, `auditor`, `manager`, `owner`, `cloud`, `employee` |
| `COMPANY_TIMEZONE` | `Europe/Istanbul` | the zone request expiry dates are in; an unknown zone stops startup |
| `MOCK_TOOLS_URL` | `http://mocktools:8081` (set in `compose.yaml`) | the Mock Tool Service the operation runner calls |
| `LOGIN_RATE_LIMIT` | `60` | sign-in and activation attempts per client address per minute (a local-demo default) |
| `MOCK_EMAIL_KEY_FILE` | `/var/lib/accessflow/mock-email.key` | the key encrypting pending activation links in the Mock Email Outbox (the `appdata` volume) |
| `GITHUB_TOKEN`, `GITHUB_REPOSITORY`, `GITHUB_TOOL_ID` | none | the optional GitHub sandbox adapter: all three or none (a partial setting stops startup). Only through `compose.github.yaml` and a git-ignored `.env.github` copied from [`.env.example`](../.env.example), in a separate Compose project; never in the default one. Employees' GitHub usernames are entered on requests, not configured. The tool catalog and the tool's page show whether the adapter serves it (*Gerçek GitHub API entegrasyonu aktif*, otherwise *Mock Tool Service kullanılıyor*) ([README](../README.md#optional-github-sandbox-adapter)) |

Database credentials are fixed local values in `compose.yaml` (A2). PostgreSQL applies them only when its volume is first created.

## First sign-in outside demo mode

Without the three demo lines, `app` creates only the initial Super Admin (`admin@accessflow.local`, or `BOOTSTRAP_ADMIN_EMAIL`), with a one-time password that is never logged. Read it with:

```bash
docker compose exec app /accessflow bootstrap-password
```

Sign in with it; the app asks for a new password first, and until then every other API call answers `403 password_change_required`. After the change the old password stops working and the file is deleted. Employees get activation links instead of passwords (F4).

## Account activation (Mock Email Outbox)

No e-mail is sent. To activate an employee: sign in as a Super Admin, open Yönetim ayarları → E-posta kutusu, open the activation e-mail and choose "Bağlantıyı göster" (the reveal is audited), then open the link, ideally in a private window, and set a password. Yönetim ayarları → Hesap etkinleştirme lists pending, expired, completed and missing activations and sends new links.

Links are stored only as SHA-256 hashes. The outbox keeps each pending link AES-256-GCM-encrypted with a key on the `appdata` volume (never in the database), so links survive restarts. If that key is lost while links are pending, links can be neither shown nor issued until you run:

```bash
docker compose exec app /accessflow reset-mail-key   # revokes the pending links, audited
docker compose restart app
```

Then send new links from the activation screen.

## Super Admin recovery

If no Super Admin is left, the app shows a recovery banner. An HR Manager nominates a successor, an InfoSec member or another Super Admin who is neither the proposer nor the nominee authorizes it, and only then does the nominee get a 24-hour link in their own mock outbox and accept the role, signed in as themself (D2). When that is impossible too (for example no active HR Manager is left), the last resort runs on the server:

```bash
docker compose exec app /accessflow recover-admin someone@company.com
docker compose exec app /accessflow bootstrap-password
```

It works only while no active Super Admin exists. The employee with that email becomes Super Admin (one is created if none exists) with a new one-time password; their sessions end and the change is audited as `ADMIN_RECOVERED`.

## Before real use

- HTTPS in front of the app, with `COOKIE_SECURE=true`; `JWT_SECRET` and database passwords from a secret store.
- Real e-mail delivery, so each activation link goes straight to its employee and the outbox reveal is removed.
- Real tool adapters behind `access.ToolClient` in place of the Mock Tool Service.
- A trusted-proxy setting before putting a reverse proxy in front: the sign-in rate limit uses the connection's address and ignores `X-Forwarded-For`.
- SSO or MFA (MFA for privileged roles is planned), metrics on the queue and sweeps, and tested backups.
