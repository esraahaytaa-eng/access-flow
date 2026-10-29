-- +goose Up
-- The Audit Explorer (M13) reads the append-only log per grant (point-in-time state, who
-- activated or removed it) and per employee (a Manager's scope); without these every lookup
-- scans the whole log.
CREATE INDEX audit_events_entity ON audit_events (entity_id, action);
CREATE INDEX audit_events_subject ON audit_events (subject_employee_id, occurred_at);

-- Imported grants revoked before the M13 fix kept no end time, so point-in-time showed them
-- held for ever; their GRANT_REVOKED event records when the removal was proven.
UPDATE grants g SET effective_to = (SELECT max(a.occurred_at) FROM audit_events a WHERE a.action = 'GRANT_REVOKED' AND a.entity_id = g.id)
WHERE g.import_id IS NOT NULL AND g.status = 'REVOKED' AND g.effective_to IS NULL;
