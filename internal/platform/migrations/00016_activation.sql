-- +goose Up
-- Account activation links (F4, A4, M18). Only the SHA-256 of a link's token is stored. The
-- Mock Email Outbox keeps the raw token encrypted with a key outside the database, only until
-- the link is used, replaced or expires; the email row itself stays.

CREATE TABLE activation_tokens (
    id          uuid        PRIMARY KEY DEFAULT uuidv7(),
    employee_id uuid        NOT NULL REFERENCES employees (id),
    token_hash  bytea       NOT NULL UNIQUE CHECK (length(token_hash) = 32),
    reason      text        NOT NULL CHECK (reason IN ('CREATED', 'IMPORTED', 'REHIRED', 'RESET')),
    issued_by   uuid        REFERENCES employees (id), -- NULL: the system
    created_at  timestamptz NOT NULL,
    expires_at  timestamptz NOT NULL,
    used_at     timestamptz,
    revoked_at  timestamptz,
    CHECK (used_at IS NULL OR revoked_at IS NULL)
);
CREATE UNIQUE INDEX activation_tokens_one_open ON activation_tokens (employee_id) WHERE used_at IS NULL AND revoked_at IS NULL;

ALTER TABLE email_outbox
    ALTER COLUMN request_id DROP NOT NULL,
    ADD COLUMN kind          text NOT NULL DEFAULT 'ACCESS_REQUEST' CHECK (kind IN ('ACCESS_REQUEST', 'ACTIVATION')),
    ADD COLUMN activation_id uuid UNIQUE REFERENCES activation_tokens (id),
    ADD COLUMN sealed_token  bytea,
    ADD CHECK ((kind = 'ACCESS_REQUEST') = (request_id IS NOT NULL)),
    ADD CHECK ((kind = 'ACTIVATION') = (activation_id IS NOT NULL)),
    ADD CHECK (sealed_token IS NULL OR kind = 'ACTIVATION');
CREATE INDEX email_outbox_sealed_idx ON email_outbox (activation_id) WHERE sealed_token IS NOT NULL;

GRANT SELECT, INSERT, UPDATE ON activation_tokens TO accessflow_app;
GRANT UPDATE (sealed_token) ON email_outbox TO accessflow_app; -- the app only ever clears it (not enforced by the database)
GRANT DELETE ON password_credentials TO accessflow_app;        -- a reset removes the current password
