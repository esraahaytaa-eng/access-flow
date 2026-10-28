#!/bin/sh
# Runs once, when the PostgreSQL volume is first created (docker-entrypoint-initdb.d).
# User names are fixed; migrations grant schema and table privileges to accessflow_app by name.
set -eu

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname postgres \
  -v owner_pw="$ACCESSFLOW_OWNER_PASSWORD" \
  -v app_pw="$ACCESSFLOW_APP_PASSWORD" \
  -v mock_pw="$MOCKTOOLS_DB_PASSWORD" <<'SQL'
-- Each user may connect only to its own database.
REVOKE CONNECT ON DATABASE postgres FROM PUBLIC;
REVOKE CONNECT ON DATABASE template1 FROM PUBLIC;

CREATE ROLE accessflow_owner LOGIN PASSWORD :'owner_pw';
CREATE ROLE accessflow_app LOGIN PASSWORD :'app_pw';
CREATE ROLE mocktools LOGIN PASSWORD :'mock_pw';

CREATE DATABASE accessflow OWNER accessflow_owner;
REVOKE ALL ON DATABASE accessflow FROM PUBLIC;
GRANT CONNECT ON DATABASE accessflow TO accessflow_app;

CREATE DATABASE mocktools OWNER mocktools;
REVOKE ALL ON DATABASE mocktools FROM PUBLIC;
SQL
