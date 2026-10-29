-- +goose Up
-- Suspension and termination (D7, A9).

-- SUSPENDED is stored only once the SUSPEND is verified or confirmed; a requested or
-- failed suspension or resume is read from the grant's open operation.
ALTER TABLE grants DROP CONSTRAINT grants_status_check,
    ADD CONSTRAINT grants_status_check CHECK (status IN ('PROVISIONING', 'PENDING_FULFILLMENT', 'ACTIVE', 'FAILED', 'SUSPENDED',
                                                         'REVOCATION_REQUESTED', 'REVOCATION_FAILED', 'REVOKED', 'CANCELLED'));

-- Priority: 0 termination removals, 1 other removals and suspensions, 2 grants and resumes (A6).
ALTER TABLE operations DROP CONSTRAINT operations_type_check,
    ADD CONSTRAINT operations_type_check CHECK (type IN ('GRANT', 'REVOKE', 'SUSPEND', 'RESUME'));
-- One waiting SUSPEND or RESUME per grant; an IN_PROGRESS one may sit before it.
CREATE UNIQUE INDEX operations_one_open_suspension ON operations (grant_id)
    WHERE type IN ('SUSPEND', 'RESUME') AND status IN ('PENDING', 'FAILED');

-- When each grant was suspended in the tool, from the verified or confirmed SUSPEND to the
-- RESUME or removal (A9: point-in-time).
CREATE TABLE grant_suspensions (
    id             uuid        PRIMARY KEY DEFAULT uuidv7(),
    grant_id       uuid        NOT NULL REFERENCES grants (id),
    effective_from timestamptz NOT NULL,
    effective_to   timestamptz CHECK (effective_to IS NULL OR effective_to >= effective_from),
    evidence       text        NOT NULL CHECK (evidence IN ('VERIFIED', 'SELF_REPORTED'))
);
CREATE UNIQUE INDEX grant_suspensions_one_open ON grant_suspensions (grant_id) WHERE effective_to IS NULL;
GRANT SELECT, INSERT, UPDATE ON grant_suspensions TO accessflow_app;

-- A scheduled termination (D7): run by the sweep at termination_at.
ALTER TABLE employees
    ADD COLUMN termination_at timestamptz,
    ADD COLUMN termination_scheduled_by uuid REFERENCES employees (id),
    ADD COLUMN termination_reason text CHECK (btrim(termination_reason) <> ''),
    ADD CONSTRAINT employees_termination_schedule CHECK ((termination_at IS NULL) = (termination_scheduled_by IS NULL)
        AND (termination_at IS NULL) = (termination_reason IS NULL));
CREATE INDEX employees_termination_idx ON employees (termination_at) WHERE termination_at IS NOT NULL;
