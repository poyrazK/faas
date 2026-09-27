-- +goose Up
ALTER TABLE outbound_admission_state
    ADD COLUMN IF NOT EXISTS provider_cooldown_until timestamptz,
    ADD COLUMN IF NOT EXISTS provider_cooldown_policy_revision bigint NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE outbound_admission_state
    DROP COLUMN IF EXISTS provider_cooldown_policy_revision,
    DROP COLUMN IF EXISTS provider_cooldown_until;
