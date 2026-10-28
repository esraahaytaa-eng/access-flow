-- +goose Up
-- Manual and Vendor operations are fulfillment tasks worked by the tool's admin group (O4, O7, A9).

ALTER TABLE operations
    ADD COLUMN claimed_by uuid REFERENCES employees (id), -- the admin working the task; on a finished task, who confirmed or failed it
    ADD COLUMN ticket_ref text CHECK (btrim(ticket_ref) = ticket_ref AND ticket_ref <> '' AND length(ticket_ref) <= 100); -- the vendor ticket

-- A Super Admin's direct grant (D1): one item, approved at once, with no approval steps.
ALTER TABLE requests DROP CONSTRAINT requests_kind_check,
    ADD CONSTRAINT requests_kind_check CHECK (kind IN ('NEW_ACCESS', 'DIRECT_GRANT'));
