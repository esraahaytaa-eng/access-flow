-- +goose Up
-- The GitHub sandbox adapter's ledger (A1 note 2026-11-01): what AccessFlow itself created in
-- the sandbox repository, per grant. A removal touches GitHub only for a grant recorded here,
-- so access AccessFlow did not grant is never removed. Rows are never deleted: REMOVED ends one.

CREATE TABLE github_access (
    grant_id      uuid        PRIMARY KEY REFERENCES grants (id),
    repository    text        NOT NULL,
    account       text        NOT NULL, -- the employee's email, as operations name the account
    login         text        NOT NULL,
    level_id      uuid        NOT NULL REFERENCES access_levels (id),
    -- INVITING: an invitation may be on its way; INVITED: sent, not yet accepted;
    -- ACTIVE: Read verified; SUSPENDED: removed for a suspension (D7); REMOVED: ended.
    state         text        NOT NULL CHECK (state IN ('INVITING', 'INVITED', 'ACTIVE', 'SUSPENDED', 'REMOVED')),
    invitation_id bigint,     -- the invitation AccessFlow sent; only this one is ever cancelled
    invited_at    timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    CHECK ((state = 'INVITED') <= (invitation_id IS NOT NULL AND invited_at IS NOT NULL))
);
-- One GitHub account and one employee are managed under at most one live grant per repository.
CREATE UNIQUE INDEX github_access_one_login ON github_access (repository, lower(login)) WHERE state <> 'REMOVED';
CREATE UNIQUE INDEX github_access_one_account ON github_access (repository, lower(account)) WHERE state <> 'REMOVED';

GRANT SELECT, INSERT, UPDATE ON github_access TO accessflow_app;
