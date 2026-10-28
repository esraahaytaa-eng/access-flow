-- +goose Up
-- Migrations run as accessflow_owner; the app runs as accessflow_app with only the grants below (A9).
REVOKE ALL ON SCHEMA public FROM PUBLIC;
GRANT USAGE ON SCHEMA public TO accessflow_app;

-- actor_id and subject_employee_id get foreign keys to employees in M2. A NULL actor is the system.
CREATE TABLE audit_events (
    id                  uuid        PRIMARY KEY DEFAULT uuidv7(),
    occurred_at         timestamptz NOT NULL,
    actor_id            uuid,
    action              text        NOT NULL CHECK (action <> ''),
    entity_type         text        NOT NULL CHECK (entity_type <> ''),
    entity_id           uuid,
    subject_employee_id uuid,
    reason              text,
    details             jsonb       NOT NULL DEFAULT '{}'
);

GRANT SELECT, INSERT ON audit_events TO accessflow_app;

-- +goose StatementBegin
-- Audit time is always the real transaction time; a caller cannot backdate an event (D12, O1).
CREATE FUNCTION audit_events_set_time() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    NEW.occurred_at := now();
    RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER audit_events_set_time
    BEFORE INSERT ON audit_events
    FOR EACH ROW EXECUTE FUNCTION audit_events_set_time();

-- +goose StatementBegin
-- Append-only, also for the owner: privileges stop the runtime user, this trigger stops everyone else.
CREATE FUNCTION audit_events_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'audit_events is append-only (% blocked)', TG_OP;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER audit_events_append_only
    BEFORE UPDATE OR DELETE OR TRUNCATE ON audit_events
    FOR EACH STATEMENT EXECUTE FUNCTION audit_events_append_only();
