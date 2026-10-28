-- +goose Up
-- Access requests and their approval steps (F2, D3, D6, D9, O5, A9).

-- One submission: one beneficiary, one or more items. Immutable once written.
CREATE TABLE requests (
    id             uuid        PRIMARY KEY DEFAULT uuidv7(),
    submission_id  uuid        NOT NULL UNIQUE, -- client-chosen: a repeat returns the original
    kind           text        NOT NULL DEFAULT 'NEW_ACCESS' CHECK (kind IN ('NEW_ACCESS')),
    beneficiary_id uuid        NOT NULL REFERENCES employees (id),
    submitted_by   uuid        NOT NULL REFERENCES employees (id),
    created_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (id, beneficiary_id) -- target of request_items' copy check
);
CREATE INDEX requests_beneficiary_idx ON requests (beneficiary_id);
CREATE INDEX requests_submitter_idx ON requests (submitted_by);

ALTER TABLE access_levels ADD CONSTRAINT access_levels_id_tool_key UNIQUE (id, tool_id);

-- One requested level. beneficiary_id and tool_id are copied from the request and level
-- for the open-item index (2026-10-28: one open item per person and tool).
CREATE TABLE request_items (
    id              uuid        PRIMARY KEY DEFAULT uuidv7(),
    request_id      uuid        NOT NULL REFERENCES requests (id),
    beneficiary_id  uuid        NOT NULL REFERENCES employees (id),
    tool_id         uuid        NOT NULL REFERENCES tools (id),
    access_level_id uuid        NOT NULL REFERENCES access_levels (id),
    justification   text        NOT NULL CHECK (btrim(justification) <> ''),
    duration_type   text        NOT NULL CHECK (duration_type IN ('TEMPORARY', 'UNTIL_REVOKED')),
    expires_on      date, -- a date in the company timezone (A6)
    status          text        NOT NULL DEFAULT 'PENDING_APPROVAL'
                                CHECK (status IN ('PENDING_APPROVAL', 'APPROVED', 'REJECTED', 'WITHDRAWN')),
    version         integer     NOT NULL DEFAULT 1,
    created_at      timestamptz NOT NULL DEFAULT now(),
    closed_at       timestamptz, -- when the status left PENDING_APPROVAL
    CHECK ((duration_type = 'TEMPORARY') = (expires_on IS NOT NULL)),
    -- the copies must match their sources: the open-item index relies on them
    FOREIGN KEY (request_id, beneficiary_id) REFERENCES requests (id, beneficiary_id),
    FOREIGN KEY (access_level_id, tool_id) REFERENCES access_levels (id, tool_id)
);
CREATE INDEX request_items_request_idx ON request_items (request_id);
CREATE UNIQUE INDEX request_items_one_open ON request_items (beneficiary_id, tool_id) WHERE status = 'PENDING_APPROVAL';

-- The item's steps, snapshotted at submission in the fixed order (F2). Only the first
-- undecided step is PENDING; it names one assignee or the InfoSec pool.
CREATE TABLE approval_steps (
    id           uuid        PRIMARY KEY DEFAULT uuidv7(),
    item_id      uuid        NOT NULL REFERENCES request_items (id),
    position     integer     NOT NULL,
    type         text        NOT NULL CHECK (type IN ('MANAGER', 'TOOL_OWNER', 'INFOSEC')),
    status       text        NOT NULL DEFAULT 'WAITING'
                             CHECK (status IN ('WAITING', 'PENDING', 'APPROVED', 'REJECTED', 'APPROVED_BY_SUBMISSION',
                                               'SUPER_ADMIN_OVERRIDE', 'CANCELLED')),
    assignee_id  uuid        REFERENCES employees (id),
    pool         text        CHECK (pool = 'INFOSEC'),
    routing_note text,
    opened_at    timestamptz,
    decided_by   uuid        REFERENCES employees (id),
    decided_at   timestamptz,
    reason       text,
    version      integer     NOT NULL DEFAULT 1,
    UNIQUE (item_id, position),
    CHECK (status <> 'PENDING' OR ((assignee_id IS NULL) <> (pool IS NULL)))
);
CREATE INDEX approval_steps_assignee_idx ON approval_steps (assignee_id) WHERE status = 'PENDING';

-- Mock Email Outbox (O2): no mail is sent; one row per qualifying submission.
CREATE TABLE email_outbox (
    id           uuid        PRIMARY KEY DEFAULT uuidv7(),
    recipient_id uuid        NOT NULL REFERENCES employees (id),
    request_id   uuid        NOT NULL UNIQUE REFERENCES requests (id),
    subject      text        NOT NULL,
    body         text        NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now()
);

GRANT SELECT, INSERT ON requests, email_outbox TO accessflow_app;
GRANT SELECT, INSERT, UPDATE ON request_items, approval_steps TO accessflow_app;
