-- +goose Up
-- Super Admin succession (D2): a two-person promotion, and recovery when no active Super
-- Admin is left (ADMIN_RECOVERY_REQUIRED, derived, never stored). A nomination is decided
-- once; an open one expires after 7 days without a job (checked when read or decided) and
-- is cancelled when its nominee or proposer leaves.

CREATE TABLE super_admin_nominations (
    id          uuid        PRIMARY KEY DEFAULT uuidv7(),
    nominee_id  uuid        NOT NULL REFERENCES employees (id),
    proposed_by uuid        NOT NULL REFERENCES employees (id),
    recovery    boolean     NOT NULL, -- proposed by an HR Manager while no Super Admin was active
    status      text        NOT NULL DEFAULT 'PENDING'
                            CHECK (status IN ('PENDING', 'AUTHORIZED', 'DECLINED', 'EXPIRED', 'CANCELLED')),
    created_at  timestamptz NOT NULL,
    expires_at  timestamptz NOT NULL,
    decided_by  uuid        REFERENCES employees (id), -- NULL when it expired or was cancelled
    decided_at  timestamptz,
    reason      text,
    version     integer     NOT NULL DEFAULT 1,
    CHECK ((status = 'PENDING') = (decided_at IS NULL)),
    CHECK ((status IN ('AUTHORIZED', 'DECLINED')) = (decided_by IS NOT NULL)),
    CHECK (decided_by <> nominee_id AND decided_by <> proposed_by) -- two people, never the nominee (D2)
);
CREATE UNIQUE INDEX super_admin_nominations_one_open ON super_admin_nominations (nominee_id) WHERE status = 'PENDING';

GRANT SELECT, INSERT, UPDATE ON super_admin_nominations TO accessflow_app;
