-- +goose Up
-- The audit explorer lists the newest events first (ORDER BY occurred_at DESC, id DESC LIMIT n).
-- Without this index the list sorts the whole log: measured 870 ms at about 311,000 events,
-- 6.3 ms with it (performance audit PERF-007, 2026-10-30). Additive only.
CREATE INDEX audit_events_recent ON audit_events (occurred_at DESC, id DESC);
