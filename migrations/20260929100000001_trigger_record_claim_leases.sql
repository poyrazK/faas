-- +goose Up
ALTER TABLE trigger_records
    ADD COLUMN claim_generation BIGINT NOT NULL DEFAULT 0
        CHECK (claim_generation >= 0),
    ADD COLUMN claim_expires_at TIMESTAMPTZ;

-- Older schedulers could leave claimed rows without an ownership deadline.
UPDATE trigger_records SET claim_expires_at = now() WHERE state = 'claimed';

CREATE INDEX trigger_records_claim_expiry_idx
    ON trigger_records (trigger_id, claim_expires_at)
    WHERE state = 'claimed';

-- +goose Down
DROP INDEX IF EXISTS trigger_records_claim_expiry_idx;
ALTER TABLE trigger_records
    DROP COLUMN claim_expires_at,
    DROP COLUMN claim_generation;
