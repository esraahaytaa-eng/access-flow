-- +goose Up
-- Tool-specific external accounts (DECISIONS.md A1 note 2026-11-01). A tool with an account
-- kind names its accounts by something other than the employee's email: GITHUB_LOGIN is a
-- GitHub username. The request (or direct grant) records the account and its numeric
-- reference, GitHub's immutable user id, and the grant copies both. They are fixed once
-- recorded, so approvers, the audit trail and every later removal see the same account.
ALTER TABLE tools ADD COLUMN account_kind text CHECK (account_kind IN ('GITHUB_LOGIN'));

ALTER TABLE request_items
    ADD COLUMN account     text,
    ADD COLUMN account_ref text,
    ADD CONSTRAINT request_items_account_ref CHECK (account_ref IS NULL OR account IS NOT NULL);
ALTER TABLE grants
    ADD COLUMN account_ref text, -- grants.account exists since 00012
    ADD CONSTRAINT grants_account_ref CHECK (account_ref IS NULL OR account IS NOT NULL);

-- +goose StatementBegin
CREATE FUNCTION external_account_fixed() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION '%: the external account is fixed once recorded', TG_TABLE_NAME;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER tools_account_kind_fixed BEFORE UPDATE ON tools FOR EACH ROW
    WHEN (OLD.account_kind IS DISTINCT FROM NEW.account_kind) EXECUTE FUNCTION external_account_fixed();
CREATE TRIGGER request_items_account_fixed BEFORE UPDATE ON request_items FOR EACH ROW
    WHEN (OLD.account IS DISTINCT FROM NEW.account OR OLD.account_ref IS DISTINCT FROM NEW.account_ref)
    EXECUTE FUNCTION external_account_fixed();
CREATE TRIGGER grants_account_fixed BEFORE UPDATE ON grants FOR EACH ROW
    WHEN (OLD.account IS DISTINCT FROM NEW.account OR OLD.account_ref IS DISTINCT FROM NEW.account_ref)
    EXECUTE FUNCTION external_account_fixed();

-- The GitHub sandbox adapter's ledger: what AccessFlow itself created in the sandbox
-- repository, per grant. A removal touches GitHub only for a grant recorded here, so access
-- AccessFlow did not grant is never removed. Rows are never deleted: REMOVED ends one.
CREATE TABLE github_access (
    grant_id      uuid        PRIMARY KEY REFERENCES grants (id),
    repository    text        NOT NULL,
    account       text        NOT NULL, -- the GitHub login, as the grant recorded it
    user_id       bigint      NOT NULL, -- GitHub's immutable user id (the grant's account_ref)
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
-- One GitHub account is managed under at most one live grant per repository.
CREATE UNIQUE INDEX github_access_one_account ON github_access (repository, lower(account)) WHERE state <> 'REMOVED';
CREATE UNIQUE INDEX github_access_one_user ON github_access (repository, user_id) WHERE state <> 'REMOVED';

GRANT SELECT, INSERT, UPDATE ON github_access TO accessflow_app;
