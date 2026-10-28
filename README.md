# AccessFlow

Access requests, approvals, granting and removal, quarterly reviews and audit proof for internal tools.
Requirements: [`CASE.md`](CASE.md). Rules and architecture: [`DECISIONS.md`](DECISIONS.md). Plan: [`docs/plans/`](docs/plans/).

> Status: walking skeleton (milestone M1). Services, migrations, the append-only audit log and the test setup work; business features arrive in later milestones.

## Run

```bash
docker compose up
```

Open http://localhost:8080. Health: `GET /api/v1/health`. No other setup is needed.

## Services

| Service | Role | Exposed |
|---|---|---|
| `app` | Go server: JSON API under `/api/v1` and the embedded React build (background jobs arrive in later milestones) | port 8080 |
| `mocktools` | Mock Tool Service, own database, reached by `app` only over HTTP | internal only |
| `postgres` | PostgreSQL 18 with the `accessflow` and `mocktools` databases | internal only |

`app` runs migrations at startup as `accessflow_owner` and then serves as `accessflow_app`, which may only insert into and read `audit_events`; a trigger also blocks `UPDATE`, `DELETE` and `TRUNCATE` and sets each event's time to the real transaction time.

## Tests

Go is not needed on the host; tests run only in Docker:

```bash
docker compose --profile test run --rm tests
```

This runs `gofmt`, `go vet` and `go test -race` against a disposable in-memory PostgreSQL (`postgres-test`). Each test gets its own database cloned from a migrated template, connected as the runtime user. The frontend type check (`tsc --noEmit`) runs in the image build.

**CI:** GitHub Actions (`.github/workflows/ci.yml`) runs `docker compose build` (Go build, frontend type check and build) and the test command above on every pull request to `main` and every push to `main`. Results are on the pull request and in the repository's Actions tab. A PR is merged only when CI is green on its latest commit.

`postgres-test` keeps running between test runs and holds its data in memory. To reset it (after changing `deploy/postgres/init.sh`, or to free the templates and databases left by changed migrations or interrupted runs): `docker compose --profile test down`.

## Layout

```
cmd/accessflow      server (and `healthcheck` probe)
cmd/mocktools       Mock Tool Service
internal/platform   config, DB pool and transactions, migrations, HTTP helpers
internal/audit      append-only audit writer
internal/webui      embedded web build and SPA fallback
internal/testutil   test databases
web/                React + TypeScript app (placeholder until the design arrives)
deploy/postgres     database and user setup, run on first start
```

## Known limitations

- Database credentials are fixed local values in `compose.yaml` (a local case-study setup, DECISIONS.md A2). PostgreSQL applies them only when its volume is first created; to change them, remove the volume (`docker compose down -v`).
- `app` keeps the migration (owner) connection string in its environment for its whole lifetime, although it uses it only at startup. Separating it would need a one-shot migration service; with fixed local credentials it adds no protection here.
