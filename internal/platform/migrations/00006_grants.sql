-- +goose Up
-- Grants and the operations that provision and remove them (A6, A9, O1, O3).

ALTER TABLE request_items DROP CONSTRAINT request_items_status_check,
    ADD CONSTRAINT request_items_status_check
        CHECK (status IN ('PENDING_APPROVAL', 'APPROVED', 'REJECTED', 'WITHDRAWN', 'CANCELLED')),
    ADD COLUMN closed_reason text; -- why the system cancelled it, e.g. "access level retired"

-- One grant per approved item. Open until REVOKED or CANCELLED; requested or failed
-- removals still count as held access (O1).
CREATE TABLE grants (
    id                      uuid        PRIMARY KEY DEFAULT uuidv7(),
    request_item_id         uuid        NOT NULL UNIQUE REFERENCES request_items (id),
    employee_id             uuid        NOT NULL REFERENCES employees (id),
    tool_id                 uuid        NOT NULL REFERENCES tools (id),
    access_level_id         uuid        NOT NULL REFERENCES access_levels (id),
    method                  text        NOT NULL CHECK (method IN ('API', 'MANUAL', 'VENDOR')),
    status                  text        NOT NULL CHECK (status IN ('PROVISIONING', 'PENDING_FULFILLMENT', 'ACTIVE', 'FAILED',
                                                       'REVOCATION_REQUESTED', 'REVOCATION_FAILED', 'REVOKED', 'CANCELLED')),
    activation_evidence     text        CHECK (activation_evidence IN ('VERIFIED', 'SELF_REPORTED')),
    effective_from          timestamptz, -- set only when a verified or confirmed activation is recorded
    effective_to            timestamptz, -- likewise for the removal
    expires_on              date,        -- a date in the company timezone (A6)
    closed_reason           text,        -- why it was revoked or cancelled
    revocation_requested_by uuid        REFERENCES employees (id),
    version                 integer     NOT NULL DEFAULT 1,
    created_at              timestamptz NOT NULL DEFAULT now(),
    CHECK (activation_evidence <> 'VERIFIED' OR method = 'API'), -- A9: Manual and Vendor are never verified
    FOREIGN KEY (access_level_id, tool_id) REFERENCES access_levels (id, tool_id)
);
CREATE UNIQUE INDEX grants_one_open ON grants (employee_id, tool_id, access_level_id) WHERE status NOT IN ('REVOKED', 'CANCELLED');
CREATE INDEX grants_employee_idx ON grants (employee_id);

-- The queue (A6). id is the operation ID sent to the tool; a retry re-sends it.
-- Priority: 0 termination/suspension removals, 1 other removals, 2 grants.
CREATE TABLE operations (
    id              uuid        PRIMARY KEY DEFAULT uuidv7(),
    grant_id        uuid        NOT NULL REFERENCES grants (id),
    type            text        NOT NULL CHECK (type IN ('GRANT', 'REVOKE')),
    reason          text        NOT NULL CHECK (btrim(reason) <> ''),
    priority        integer     NOT NULL CHECK (priority BETWEEN 0 AND 2),
    status          text        NOT NULL DEFAULT 'PENDING'
                                CHECK (status IN ('PENDING', 'IN_PROGRESS', 'SUCCEEDED', 'FAILED', 'CANCELLED')),
    attempts        integer     NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    lease_until     timestamptz,
    lease_token     uuid, -- a new one per claim: only its holder may record the result
    first_attempt_at timestamptz, -- NULL: never sent, so it may be cancelled without a tool call
    last_error      text,
    version         integer     NOT NULL DEFAULT 1,
    created_at      timestamptz NOT NULL DEFAULT now(),
    completed_at    timestamptz -- when it became SUCCEEDED, FAILED or CANCELLED; cleared by a retry
);
CREATE INDEX operations_grant_idx ON operations (grant_id);
CREATE UNIQUE INDEX operations_one_grant ON operations (grant_id) WHERE type = 'GRANT';
CREATE UNIQUE INDEX operations_one_open_revoke ON operations (grant_id) WHERE type = 'REVOKE' AND status IN ('PENDING', 'IN_PROGRESS', 'FAILED');
CREATE INDEX operations_claim_idx ON operations (status, priority, created_at);

GRANT SELECT, INSERT, UPDATE ON grants, operations TO accessflow_app;
