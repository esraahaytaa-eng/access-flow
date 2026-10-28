-- +goose Up
-- Identity: employees, passwords, roles and the generated JWT secret (A4, A5, A9).
-- The runtime user gets no DELETE: employees and their history are never physically deleted.

CREATE TABLE employees (
    id                 uuid        PRIMARY KEY DEFAULT uuidv7(),
    email              text        NOT NULL CHECK (email <> '' AND email = lower(btrim(email))),
    full_name          text        NOT NULL CHECK (btrim(full_name) <> ''),
    department         text,
    job_title          text,
    manager_id         uuid        REFERENCES employees (id),
    status             text        NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'SUSPENDED', 'TERMINATED')),
    token_version      integer     NOT NULL DEFAULT 0,
    failed_login_count integer     NOT NULL DEFAULT 0 CHECK (failed_login_count >= 0),
    locked_until       timestamptz,
    version            integer     NOT NULL DEFAULT 1,
    created_at         timestamptz NOT NULL DEFAULT now(),
    CHECK (manager_id <> id)
);
-- Emails are stored lowercase (CHECK above), so this index makes them unique regardless of case.
CREATE UNIQUE INDEX employees_email_key ON employees (email);

-- One credential per employee. A temporary password is valid until the first successful
-- change; employee ones also expire (expires_at), the bootstrap Super Admin's does not (F4).
CREATE TABLE password_credentials (
    employee_id   uuid        PRIMARY KEY REFERENCES employees (id),
    password_hash text        NOT NULL,
    is_temporary  boolean     NOT NULL,
    expires_at    timestamptz,
    updated_at    timestamptz NOT NULL DEFAULT now(),
    CHECK (is_temporary OR expires_at IS NULL)
);

-- Roles are intervals so point-in-time queries can tell who held them (A9, D11).
-- A NULL granted_by means the system (initial bootstrap).
CREATE TABLE role_assignments (
    id          uuid        PRIMARY KEY DEFAULT uuidv7(),
    employee_id uuid        NOT NULL REFERENCES employees (id),
    role        text        NOT NULL CHECK (role IN ('SUPER_ADMIN', 'INFOSEC', 'HR_MANAGER', 'AUDITOR')),
    valid_from  timestamptz NOT NULL DEFAULT now(),
    valid_to    timestamptz CHECK (valid_to IS NULL OR valid_to >= valid_from),
    granted_by  uuid        REFERENCES employees (id),
    revoked_by  uuid        REFERENCES employees (id)
);
CREATE UNIQUE INDEX role_assignments_one_open ON role_assignments (employee_id, role) WHERE valid_to IS NULL;

CREATE TABLE settings (
    key   text PRIMARY KEY,
    value text NOT NULL
);

ALTER TABLE audit_events
    ADD CONSTRAINT audit_events_actor_fkey FOREIGN KEY (actor_id) REFERENCES employees (id),
    ADD CONSTRAINT audit_events_subject_fkey FOREIGN KEY (subject_employee_id) REFERENCES employees (id);

GRANT SELECT, INSERT, UPDATE ON employees, password_credentials, role_assignments TO accessflow_app;
GRANT SELECT, INSERT ON settings TO accessflow_app;
