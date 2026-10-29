# M15 + M16 — Demo-ready start, CI speed and delivery

Branch: `feat/demo-delivery` (one PR). Out of scope: employee CSV onboarding, activation emails, SSO, historical employee activity investigations.

## Approved decisions (2026-10-29; recorded as dated notes in DECISIONS.md A4 and D12)

1. **Demo mode, local only, fail closed.** `ACCESSFLOW_DEMO=true` is set only in `compose.yaml`; the binary defaults to off. The app refuses to start in demo mode unless the whole configuration is the local one:
   - an explicit `ACCESSFLOW_ENV=local` (any other or missing value refuses);
   - `COOKIE_SECURE` off;
   - database URLs pointing at the Compose `postgres` host;
   - `JWT_SECRET` unset (generated).

   Every page shows a "Demo" badge, and the startup log says demo mode is on.
2. **Demo accounts.** A ready-to-use demo Super Admin and one documented account per main role: InfoSec, HR Manager, Auditor, a manager, a tool owner and an employee. Each has its own password, documented in the README and taken from Compose environment defaults. All other seeded employees get unusable random passwords, so they cannot sign in. With demo mode off, bootstrap is unchanged: a one-time password in the file, changed at first sign-in.
3. **Seed.** It runs automatically and only once (idempotent) on a clean `docker compose up`. It goes through the real services at real time, marked seed-originated, with no fabricated audit history, no simulated clock and no "run jobs now" control (D12 unchanged). Ruling 2026-10-29: the `dev-fixture` command is removed; its shared `SEED_PASSWORD` would contradict decision 2, and the seed covers everything it created (D12 note).

## M15 — Demo start and login

**Tasks**
1. Demo mode config, fail-closed guard and demo accounts. Tests:
   - config table test: on/off, and refusal for every non-local setting (missing or other `ACCESSFLOW_ENV`, `COOKIE_SECURE`, a non-Compose DB host, a set `JWT_SECRET`);
   - only the documented accounts can sign in;
   - auth test: the demo admin can sign in without a forced change, and a second start changes nothing (idempotent);
   - with demo off, behavior is unchanged (one-time file).
2. Extend `internal/devfixture` into the demo seed, run at startup in demo mode (`internal/demo`; the `dev-fixture` command is removed, see the ruling above). Through the real services it creates:
   - about 20 employees in 4 departments, all roles, 2 InfoSec members, managers and skip-levels;
   - 6 tools across the three fulfillment methods, with levels, owners and admin groups;
   - requests in every state: pending at each step, approved, rejected, withdrawn;
   - active grants verified by the API runner, a Manual task to claim, a Vendor task with a ticket;
   - a temporary grant near expiry;
   - a suspended and a terminated employee, whose removals produce security revocations;
   - a baseline import with orphan accounts;
   - one running review campaign with items to decide;
   - a scheduled termination.

   Not seeded: exceptions that only real time produces, such as day-21 review findings and failures after 5 retries over hours. They are listed in the README. Whether the Mock Tool Service can fail an operation naturally (unknown account, for example) is checked in task 2; if it can, a failed operation is seeded, otherwise it is documented.

   Tests: running the seed twice gives the same counts, every seeded record is marked seed-originated, and no audit event is dated before the run.
3. Login redesign, with creative freedom: an elegant, minimal internal-enterprise sign-in. The whole left marketing panel and all promotional text are removed. No new dependency; existing components and tokens only. In demo mode a small notice lists the demo accounts, read from a new public `GET /api/v1/demo` that answers 404 when demo mode is off. Tests: Vitest for sign-in, validation, the server error and the demo notice shown or hidden; the existing auth tests stay unchanged.
4. README: "Run" becomes `docker compose up` → open http://localhost:8080 → the demo accounts table. It also covers demo mode and its guard, and what the seed contains.

**Acceptance:** on a clean machine `docker compose up` → sign in as the documented Super Admin without manual steps. Each role's main screens show realistic data. A restart creates no duplicates.

## M16 — CI speed and final delivery

**Tasks**
1. **CI speed.** Baseline: the last 3 `main` runs took 7–9 minutes, about 9 of which is `internal/requests`, running 214 tests serially. Steps:
   - measure per-package and the slowest tests (`go test -json`);
   - add `t.Parallel()` where safe: each test already gets its own template-cloned database. Tests that use shared things (the mock-tools listener, the env, the lock-wait helper that counts lock waits in its own database) are fixed or kept serial;
   - check the runner/drain loops for fixed sleeps and replace them with condition waits;
   - cache Docker layers and Go module/build caches in GitHub Actions (`actions/cache`, buildx GHA cache), and run `docker compose build` and the test DB start in parallel where possible;
   - tune `postgres-test` if waits dominate (`max_connections`, `shared_buffers`).

   `-race`, every test and the coverage stay. Measure before and after and report both. The target is 3–5 minutes; if a floor remains, say what it is and why.
2. **Real-browser QA** of every role's main flow on a clean `docker compose up`, including the demo badge, the login and the recovery banner. If no browser tool is available, I deliver a reproducible manual checklist and state clearly what was not executed.
3. **Security checks:** `govulncheck`, `npm audit --omit=dev`, the cookie and CSRF settings, no secrets in the repo or image, the demo guard, and the image runs as non-root. Findings are fixed or documented.
4. **Docs:**
   - README accuracy pass;
   - the one-page operating summary (weekly run, rules, assumptions) and the 30-day rollout plan (CASE deliverables);
   - ~~a five-minute demo walkthrough script for your recording~~. Deferred 2026-10-29 at your request: no video preparation or recording-specific work until the application is finished and verified; only a short list of demonstration scenarios is kept.
5. **Final checks:** `ponytail-audit` once over the whole repo, every CASE deliverable checked, and one final Docker journey on a clean project.

**Acceptance:** CI is green and faster, with numbers before and after. Clean setup, smoke test and security checks are reported. All CASE deliverables are present.

## Testing cadence

Targeted TDD per task. Relevant package tests at task boundaries. One Docker journey near each PR boundary. The full suite runs in CI on the final PR head. No repeated full local suites. One code review per milestone group, not per task.
