-- +goose Up
-- HR's succession nomination is authorized by an independent InfoSec member or active Super
-- Admin before the nominee's link is sent (R-2, D2 note 2026-10-31). The authorization is kept
-- apart from decided_by, which records the nominee's acceptance. Additive only: no existing row
-- changes. The acceptance check is NOT VALID so earlier accepted successions stay as they were;
-- it applies to every row written from now on.

ALTER TABLE super_admin_nominations
    ADD COLUMN authorized_by uuid REFERENCES employees (id),
    ADD COLUMN authorized_at timestamptz,
    ADD CHECK ((authorized_by IS NULL) = (authorized_at IS NULL)),
    ADD CHECK (authorized_by <> proposed_by AND authorized_by <> nominee_id); -- two people besides the nominee
ALTER TABLE super_admin_nominations
    ADD CONSTRAINT super_admin_nominations_accepted_authorized CHECK (status <> 'ACCEPTED' OR authorized_by IS NOT NULL) NOT VALID;
