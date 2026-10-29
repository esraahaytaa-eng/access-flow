-- +goose Up
-- Access that existed before AccessFlow (D10): imported from the Mock Tool Service or a CSV
-- file as grants with no request, no approval and no activation date. An account that
-- matches no employee is an orphan: an imported grant with no employee, which InfoSec
-- revokes through the normal removal or dismisses with a reason.

CREATE TABLE imports (
    id          uuid        PRIMARY KEY DEFAULT uuidv7(),
    source      text        NOT NULL CHECK (source IN ('MOCK', 'CSV')),
    file_name   text        CHECK ((source = 'CSV') = (file_name IS NOT NULL)),
    imported_by uuid        NOT NULL REFERENCES employees (id),
    imported_at timestamptz NOT NULL,
    report      jsonb       NOT NULL -- counts and the rejected rows
);

ALTER TABLE grants
    ALTER COLUMN request_item_id DROP NOT NULL,
    ALTER COLUMN employee_id DROP NOT NULL,
    ADD COLUMN import_id uuid REFERENCES imports (id) DEFERRABLE INITIALLY DEFERRED, -- the report is written last
    ADD COLUMN account text,                 -- the tool's account identifier, when not the employee's email
    ADD COLUMN orphan_dismissed_at timestamptz,
    ADD COLUMN orphan_dismissed_by uuid REFERENCES employees (id),
    ADD COLUMN orphan_dismiss_reason text,
    ADD CONSTRAINT grants_origin CHECK ((request_item_id IS NULL) <> (import_id IS NULL)),
    ADD CONSTRAINT grants_holder CHECK (employee_id IS NOT NULL OR (import_id IS NOT NULL AND account IS NOT NULL)),
    ADD CONSTRAINT grants_dismissal CHECK ((orphan_dismissed_at IS NULL) = (orphan_dismissed_by IS NULL)
        AND (orphan_dismissed_at IS NULL) = (orphan_dismiss_reason IS NULL) AND (orphan_dismissed_at IS NULL OR employee_id IS NULL));
-- Idempotent imports: a (tool, account, level) is imported once.
CREATE UNIQUE INDEX grants_imported_once ON grants (tool_id, lower(account), access_level_id) WHERE import_id IS NOT NULL;

GRANT SELECT, INSERT ON imports TO accessflow_app;
