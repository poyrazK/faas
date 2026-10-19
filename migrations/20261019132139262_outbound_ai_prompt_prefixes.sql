-- filename: 20261019132139262_outbound_ai_prompt_prefixes.sql
-- ADR 20261019132139: prompt prefixes seen by any outboundd, so automatic
-- prompt caching marks a prefix on its first repeat across replicas. Rows
-- hold a SHA-256 of the integration, model and prefix, never prompt text,
-- and outboundd prunes them hourly. Additive and replay-safe.

-- migration-compat: expand
-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS outbound_ai_prompt_prefixes (
    integration_id   uuid NOT NULL REFERENCES outbound_integrations(id) ON DELETE CASCADE,
    prefix_hash      bytea NOT NULL,
    seen_at          timestamptz NOT NULL DEFAULT now(),
    previous_seen_at timestamptz,
    PRIMARY KEY (integration_id, prefix_hash),
    CONSTRAINT outbound_ai_prompt_prefixes_hash_chk CHECK (octet_length(prefix_hash) = 32),
    CONSTRAINT outbound_ai_prompt_prefixes_seen_chk CHECK (previous_seen_at IS NULL OR previous_seen_at <= seen_at)
);
CREATE INDEX IF NOT EXISTS outbound_ai_prompt_prefixes_seen_at_idx ON outbound_ai_prompt_prefixes (seen_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS outbound_ai_prompt_prefixes;
-- +goose StatementEnd
