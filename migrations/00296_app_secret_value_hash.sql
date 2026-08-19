-- +goose Up
-- +goose StatementBegin
ALTER TABLE app_secrets
    ADD COLUMN value_hash text;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE app_secrets
    ADD CONSTRAINT app_secrets_value_hash_shape
    CHECK (value_hash IS NULL OR length(value_hash) <= 16);
-- +goose StatementEnd

-- +goose Down
-- Forward-only migration; Down intentionally empty per the
-- 00217 + 00229 + 00206 forward-only precedent. The
-- app_secrets.value_hash column is added by this migration and
-- is required by the env-diff matrix endpoint (GET
-- /v1/apps/{slug}/env-diff, ADR-117 PR-C); rolling back the
-- column would silently strip the value-equality discriminator
-- from every active customer without a coordinated
-- apid-rollback to a pre-PR-C image. The only safe rollback is
-- the operator's release-bundle install of the prior image,
-- which carries the matching pre-PR-C apid binary and rejects
-- the column at startup.