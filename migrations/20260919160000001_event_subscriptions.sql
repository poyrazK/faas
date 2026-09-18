-- EPIC #1278 / Workstream B: durable internal event subscriptions.
-- The publish ingress and matcher are tenant-scoped; this table is the
-- durable app binding that lets deploys install matcher declarations without
-- coupling the scheduler to the source archive.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS event_subscriptions (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id  UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id      UUID NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    source      TEXT NOT NULL,
    type        TEXT NOT NULL,
    filter      JSONB NOT NULL DEFAULT '{}'::jsonb,
    enabled     BOOLEAN NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT event_subscriptions_source_len_chk CHECK (char_length(source) BETWEEN 1 AND 256),
    CONSTRAINT event_subscriptions_type_len_chk CHECK (char_length(type) BETWEEN 1 AND 256),
    CONSTRAINT event_subscriptions_filter_object_chk CHECK (jsonb_typeof(filter) = 'object')
);

CREATE INDEX IF NOT EXISTS event_subscriptions_account_idx
    ON event_subscriptions (account_id, app_id, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS event_subscriptions_enabled_idx
    ON event_subscriptions (app_id, source, type)
    WHERE enabled;
CREATE UNIQUE INDEX IF NOT EXISTS event_subscriptions_identity_uniq
    ON event_subscriptions (app_id, source, type, filter);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS event_subscriptions_enabled_idx;
DROP INDEX IF EXISTS event_subscriptions_account_idx;
DROP TABLE IF EXISTS event_subscriptions;
-- +goose StatementEnd
