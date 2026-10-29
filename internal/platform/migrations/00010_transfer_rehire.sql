-- +goose Up
-- Transfer (D5): the latest retention of a grant kept at a transfer, and the time a grant
-- activated after its transfer removal was requested (a stale grant incident). History of
-- every transfer is in its EMPLOYEE_TRANSFERRED audit event (A9).
ALTER TABLE grants
    ADD COLUMN retained_at timestamptz,
    ADD COLUMN retained_by uuid REFERENCES employees (id),
    ADD COLUMN retention_justification text CHECK (btrim(retention_justification) <> ''),
    ADD COLUMN stale_at timestamptz,
    ADD CONSTRAINT grants_retention CHECK ((retained_at IS NULL) = (retained_by IS NULL)
        AND (retained_at IS NULL) = (retention_justification IS NULL));

