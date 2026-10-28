-- +goose Up
-- Tools and their access levels; approval steps belong to the level (F1, F2).
CREATE TABLE tools (
    id                 uuid        PRIMARY KEY DEFAULT uuidv7(),
    name               text        NOT NULL CHECK (btrim(name) = name AND name <> '' AND length(name) <= 100),
    fulfillment_method text        NOT NULL CHECK (fulfillment_method IN ('API', 'MANUAL', 'VENDOR')),
    owner_id           uuid        NOT NULL REFERENCES employees (id),
    admin_group_id     uuid        NOT NULL REFERENCES admin_groups (id),
    status             text        NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'RETIRED')),
    version            integer     NOT NULL DEFAULT 1,
    seeded             boolean     NOT NULL DEFAULT false, -- created by the dev fixture (D12)
    created_at         timestamptz NOT NULL DEFAULT now(),
    created_by         uuid        REFERENCES employees (id) -- NULL: the system (dev fixture)
);
CREATE UNIQUE INDEX tools_name_key ON tools (lower(name));

-- Manager is always required; Tool Owner and InfoSec are the optional steps (F2).
CREATE TABLE access_levels (
    id                  uuid        PRIMARY KEY DEFAULT uuidv7(),
    tool_id             uuid        NOT NULL REFERENCES tools (id),
    name                text        NOT NULL CHECK (btrim(name) = name AND name <> '' AND length(name) <= 100),
    requires_tool_owner boolean     NOT NULL,
    requires_infosec    boolean     NOT NULL,
    status              text        NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'RETIRED')),
    version             integer     NOT NULL DEFAULT 1,
    created_at          timestamptz NOT NULL DEFAULT now(),
    created_by          uuid        REFERENCES employees (id)
);
CREATE UNIQUE INDEX access_levels_name_key ON access_levels (tool_id, lower(name));

-- Recommended levels per department, optionally narrowed to a job title (F3).
-- Removal sets removed_at; nothing is deleted (A5).
CREATE TABLE starter_packages (
    id              uuid        PRIMARY KEY DEFAULT uuidv7(),
    department      text        NOT NULL CHECK (btrim(department) = department AND department <> ''),
    job_title       text        CHECK (btrim(job_title) = job_title AND job_title <> ''),
    access_level_id uuid        NOT NULL REFERENCES access_levels (id),
    created_at      timestamptz NOT NULL DEFAULT now(),
    created_by      uuid        REFERENCES employees (id),
    removed_at      timestamptz,
    removed_by      uuid        REFERENCES employees (id)
);
CREATE UNIQUE INDEX starter_packages_open_key ON starter_packages (lower(department), lower(coalesce(job_title, '')), access_level_id)
    WHERE removed_at IS NULL;

-- In-app notifications (O2); one row per recipient.
CREATE TABLE notifications (
    id           uuid        PRIMARY KEY DEFAULT uuidv7(),
    recipient_id uuid        NOT NULL REFERENCES employees (id),
    kind         text        NOT NULL,
    entity       text        NOT NULL,
    entity_id    uuid,
    details      jsonb       NOT NULL DEFAULT '{}',
    created_at   timestamptz NOT NULL DEFAULT now(),
    read_at      timestamptz
);
CREATE INDEX notifications_recipient_idx ON notifications (recipient_id, created_at DESC);

GRANT SELECT, INSERT, UPDATE ON tools, access_levels, starter_packages, notifications TO accessflow_app;

