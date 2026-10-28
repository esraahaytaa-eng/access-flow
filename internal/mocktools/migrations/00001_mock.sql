-- +goose Up
-- The Mock Tool Service stands in for the internal tools (A1, A2). It keeps accounts and
-- permissions per (tool, account, level), so revoking one level never touches another or
-- the account (D4). It is a simulated external system: AccessFlow keeps the history.
CREATE TABLE accounts (
    tool       text        NOT NULL,
    account    text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tool, account)
);

CREATE TABLE permissions (
    tool       text        NOT NULL,
    account    text        NOT NULL,
    level      text        NOT NULL,
    status     text        NOT NULL CHECK (status IN ('ACTIVE', 'SUSPENDED')),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tool, account, level),
    FOREIGN KEY (tool, account) REFERENCES accounts (tool, account)
);

-- One row per operation ID: a repeat returns the stored result instead of acting again.
CREATE TABLE operations (
    operation_id uuid        PRIMARY KEY,
    type         text        NOT NULL CHECK (type IN ('GRANT', 'REVOKE', 'SUSPEND', 'RESUME')),
    tool         text        NOT NULL,
    account      text        NOT NULL,
    level        text        NOT NULL,
    result       text        CHECK (result IN ('APPLIED', 'NO_CHANGE')),
    applied_at   timestamptz NOT NULL DEFAULT now()
);
