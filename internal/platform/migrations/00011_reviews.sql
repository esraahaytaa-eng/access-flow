-- +goose Up
-- Quarterly access reviews (D8). A campaign snapshots the open grants at its start; each
-- item is decided Keep or Revoke, or is closed by the day-21 step or by its grant ending.
-- Decisions, escalations and non-response actions are also audit events (A9).

-- F2: steps were added to the grant's level after it was approved; a review decides it.
ALTER TABLE grants ADD COLUMN earlier_policy_at timestamptz;

CREATE TABLE review_campaigns (
    id           uuid        PRIMARY KEY DEFAULT uuidv7(),
    kind         text        NOT NULL CHECK (kind IN ('QUARTERLY', 'MANUAL')),
    quarter      text,                              -- "2026-Q4" for a quarterly campaign
    started_at   timestamptz NOT NULL,
    started_by   uuid        REFERENCES employees (id), -- NULL: the quarterly sweep
    reminded_at  timestamptz,                       -- the day-7 step ran
    escalated_at timestamptz,                       -- the day-14 step ran
    closed_at    timestamptz,                       -- the day-21 step ran
    CHECK ((kind = 'QUARTERLY') = (quarter IS NOT NULL))
);
CREATE UNIQUE INDEX review_campaigns_one_per_quarter ON review_campaigns (quarter);
CREATE UNIQUE INDEX review_campaigns_one_active ON review_campaigns ((true)) WHERE closed_at IS NULL;

CREATE TABLE review_items (
    id               uuid        PRIMARY KEY DEFAULT uuidv7(),
    campaign_id      uuid        NOT NULL REFERENCES review_campaigns (id),
    grant_id         uuid        NOT NULL REFERENCES grants (id),
    reviewer_id      uuid        REFERENCES employees (id), -- NULL: the InfoSec pool
    routing_note     text,                                  -- why it went to the pool
    requires_infosec boolean     NOT NULL,                  -- the level's steps at campaign start (D8 day 21)
    flags            text[]      NOT NULL DEFAULT '{}',     -- reasons to decide it individually
    decision         text        CHECK (decision IN ('KEEP', 'REVOKE')),
    decided_by       uuid        REFERENCES employees (id),
    decided_at       timestamptz,
    decision_reason  text,
    late             boolean     NOT NULL DEFAULT false,    -- decided on an open finding, after day 21
    finding_at       timestamptz,                           -- day 21 left it unanswered
    closed_reason    text        CHECK (closed_reason IN ('REVOKED_NO_REVIEW', 'ALREADY_REMOVED')),
    closed_at        timestamptz,
    version          integer     NOT NULL DEFAULT 1,
    UNIQUE (campaign_id, grant_id),
    CHECK ((decision IS NULL) = (decided_by IS NULL) AND (decision IS NULL) = (decided_at IS NULL)),
    CHECK ((closed_reason IS NULL) = (closed_at IS NULL)),
    CHECK (decision IS NULL OR closed_reason IS NULL),
    CHECK (decision <> 'REVOKE' OR btrim(decision_reason) <> '')
);
-- An open finding stays the one work item of its grant (D8): never a second one.
CREATE UNIQUE INDEX review_items_one_open_finding ON review_items (grant_id)
    WHERE finding_at IS NOT NULL AND decision IS NULL AND closed_reason IS NULL;
CREATE INDEX review_items_campaign_idx ON review_items (campaign_id);

GRANT SELECT, INSERT, UPDATE ON review_campaigns, review_items TO accessflow_app;
