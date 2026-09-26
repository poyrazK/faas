-- filename: 20260926094619939_manifest_edge_rule_ownership.sql

-- +goose Up
-- +goose StatementBegin
ALTER TABLE edge_rules
    ADD COLUMN IF NOT EXISTS manifest_key text;

CREATE UNIQUE INDEX IF NOT EXISTS edge_rules_app_manifest_key_uidx
    ON edge_rules (app_id, manifest_key)
    WHERE manifest_key IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS edge_rules_app_manifest_key_uidx;
ALTER TABLE edge_rules DROP COLUMN IF EXISTS manifest_key;
-- +goose StatementEnd
