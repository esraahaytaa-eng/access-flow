#!/bin/sh
# Browser smoke tests (e2e/*.spec.ts) against a fresh, separate Compose project.
# It never touches the default project (localhost:8080): own project name, own volumes, a host
# port Docker picks, and everything it created is removed afterwards, also on failure.
set -eu
cd "$(dirname "$0")"
project=accessflow-e2e
export ACCESSFLOW_PORT=0

cleanup() { docker compose -p "$project" --profile e2e down -v --remove-orphans >/dev/null 2>&1 || true; }
trap cleanup EXIT
trap 'exit 130' INT TERM

cleanup # a fresh start even if an earlier run was killed
docker compose -p "$project" up --build --wait --quiet-pull app
docker compose -p "$project" --profile e2e run --rm e2e
