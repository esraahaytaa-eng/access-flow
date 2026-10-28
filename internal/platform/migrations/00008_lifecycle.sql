-- +goose Up
-- Extensions and level changes of a held grant (D4, D6, A9).

-- An EXTENSION or LEVEL_CHANGE has one item, which names the grant it acts on (the
-- service requires it for those kinds).
ALTER TABLE requests DROP CONSTRAINT requests_kind_check,
    ADD CONSTRAINT requests_kind_check CHECK (kind IN ('NEW_ACCESS', 'DIRECT_GRANT', 'EXTENSION', 'LEVEL_CHANGE'));
ALTER TABLE request_items ADD COLUMN grant_id uuid REFERENCES grants (id);
CREATE INDEX request_items_grant_idx ON request_items (grant_id) WHERE status = 'PENDING_APPROVAL';

-- A level change's new grant names the grant it replaces: at most one open replacement each.
ALTER TABLE grants ADD COLUMN replaces_grant_id uuid REFERENCES grants (id);
CREATE UNIQUE INDEX grants_one_open_change ON grants (replaces_grant_id) WHERE status NOT IN ('REVOKED', 'CANCELLED');
