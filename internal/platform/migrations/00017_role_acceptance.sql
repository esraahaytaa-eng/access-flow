-- +goose Up
-- Super Admin succession owned by HR (D2 note 2026-10-30). When the last active Super Admin
-- leaves, an HR Manager nominates a successor, who accepts through a single-use link valid for
-- 24 hours, signed in as themself: HR and the nominee are the two people. Only the SHA-256 of
-- the link's token is stored, on the nomination, apart from password activation tokens; the
-- Mock Email Outbox keeps the raw token encrypted for the nominee until it is used, replaced or
-- expires.

ALTER TABLE super_admin_nominations
    ADD COLUMN succession boolean NOT NULL DEFAULT false, -- by HR, accepted by the nominee
    ADD COLUMN token_hash bytea UNIQUE CHECK (length(token_hash) = 32),
    DROP CONSTRAINT super_admin_nominations_status_check,
    DROP CONSTRAINT super_admin_nominations_check1,
    DROP CONSTRAINT super_admin_nominations_check2,
    ADD CHECK (status IN ('PENDING', 'AUTHORIZED', 'DECLINED', 'ACCEPTED', 'EXPIRED', 'CANCELLED')),
    ADD CHECK ((status IN ('AUTHORIZED', 'DECLINED', 'ACCEPTED')) = (decided_by IS NOT NULL)),
    -- Two people, never the nominee, except that a succession nominee accepts their own.
    ADD CHECK (decided_by <> proposed_by AND (decided_by <> nominee_id OR status = 'ACCEPTED')),
    ADD CHECK (status <> 'ACCEPTED' OR (succession AND decided_by = nominee_id));

ALTER TABLE email_outbox
    ADD COLUMN nomination_id uuid REFERENCES super_admin_nominations (id),
    DROP CONSTRAINT email_outbox_kind_check,
    DROP CONSTRAINT email_outbox_check2,
    ADD CHECK (kind IN ('ACCESS_REQUEST', 'ACTIVATION', 'ROLE_ACCEPTANCE')),
    ADD CHECK ((kind = 'ROLE_ACCEPTANCE') = (nomination_id IS NOT NULL)),
    ADD CHECK (sealed_token IS NULL OR kind IN ('ACTIVATION', 'ROLE_ACCEPTANCE'));
CREATE INDEX email_outbox_nomination_idx ON email_outbox (nomination_id) WHERE nomination_id IS NOT NULL;
