-- filename: 20261002064233200_mirror_slot_leases.sql

-- +goose Up
-- +goose StatementBegin
-- Fleet-wide mirror concurrency reservations. Each gateway replica holds one
-- short-lived reservation from before scheduler admission until the shadow VM
-- has been parked. Expiry reclaims permits after a gateway process exits.
CREATE TABLE IF NOT EXISTS mirror_slot_leases (
    lease_id       uuid        PRIMARY KEY,
    mirror_rule_id uuid        NOT NULL REFERENCES mirror_rules(id) ON DELETE CASCADE,
    expires_at     timestamptz NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT mirror_slot_leases_expiry_after_creation
        CHECK (expires_at > created_at)
);

CREATE INDEX IF NOT EXISTS mirror_slot_leases_rule_expiry_idx
    ON mirror_slot_leases (mirror_rule_id, expires_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS mirror_slot_leases;
-- +goose StatementEnd
