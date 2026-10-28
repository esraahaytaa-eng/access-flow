-- +goose Up
-- People administration (M3): the shared email rule, admin groups and the seed marker.

-- Mirrors auth.NormalizeEmail: printable ASCII without spaces or a second "@", at most 254
-- characters. ASCII only, so Go and PostgreSQL agree on lowercasing (M2 review follow-up).
ALTER TABLE employees
    ADD CONSTRAINT employees_email_format CHECK (length(email) <= 254 AND email ~ '^[!-?A-~]+@[!-?A-~]+$');

-- Admin groups administer Manual and Vendor tools (M4, M7). Create-only for now (D2, 2026-10-28).
CREATE TABLE admin_groups (
    id         uuid        PRIMARY KEY DEFAULT uuidv7(),
    name       text        NOT NULL CHECK (btrim(name) = name AND name <> '' AND length(name) <= 100),
    created_at timestamptz NOT NULL DEFAULT now(),
    created_by uuid        REFERENCES employees (id) -- NULL: the system (dev fixture)
);
CREATE UNIQUE INDEX admin_groups_name_key ON admin_groups (lower(name));

-- Memberships are intervals, like roles (A9).
CREATE TABLE admin_group_memberships (
    id          uuid        PRIMARY KEY DEFAULT uuidv7(),
    group_id    uuid        NOT NULL REFERENCES admin_groups (id),
    employee_id uuid        NOT NULL REFERENCES employees (id),
    valid_from  timestamptz NOT NULL DEFAULT now(),
    valid_to    timestamptz CHECK (valid_to IS NULL OR valid_to >= valid_from),
    granted_by  uuid        REFERENCES employees (id),
    revoked_by  uuid        REFERENCES employees (id)
);
CREATE UNIQUE INDEX admin_group_memberships_one_open ON admin_group_memberships (group_id, employee_id) WHERE valid_to IS NULL;

GRANT SELECT, INSERT, UPDATE ON admin_groups, admin_group_memberships TO accessflow_app;

-- Seed-originated records (D12): set only by the dev fixture and, later, the demo seed.
ALTER TABLE employees ADD COLUMN seeded boolean NOT NULL DEFAULT false;
