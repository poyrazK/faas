-- filename: 20261017121242318_outbound_ai_usage_tags.sql
-- ADR-1436: AI gateway phase 14. AI calls name up to five key=value usage
-- tags. Settlement rolls usage up per tag and UTC day, with a bounded
-- number of tags per integration and day and a per-key overflow row; exact
-- tags can carry a daily cost limit that admission enforces with the live
-- reservations of leases carrying the tag. The request log keeps each
-- call's tags. Additive and replay-safe.

-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS outbound_ai_tag_usage_daily (
    integration_id uuid NOT NULL REFERENCES outbound_integrations(id) ON DELETE CASCADE,
    usage_date date NOT NULL,
    tag text NOT NULL,
    requests bigint NOT NULL DEFAULT 0,
    error_requests bigint NOT NULL DEFAULT 0,
    input_tokens bigint NOT NULL DEFAULT 0,
    output_tokens bigint NOT NULL DEFAULT 0,
    cost_millicents bigint NOT NULL DEFAULT 0,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (integration_id, usage_date, tag),
    CONSTRAINT outbound_ai_tag_usage_daily_tag_chk
        CHECK (tag ~ '^[a-z][a-z0-9_.-]{0,31}=([A-Za-z0-9._:@+/-]{1,64}|~other)$'),
    CONSTRAINT outbound_ai_tag_usage_daily_values_chk
        CHECK (requests >= 0 AND error_requests BETWEEN 0 AND requests AND input_tokens >= 0
               AND output_tokens >= 0 AND cost_millicents >= 0)
);
CREATE INDEX IF NOT EXISTS outbound_ai_tag_usage_daily_prune_idx ON outbound_ai_tag_usage_daily (usage_date);

CREATE TABLE IF NOT EXISTS outbound_ai_tag_limits (
    integration_id uuid NOT NULL REFERENCES outbound_integrations(id) ON DELETE CASCADE,
    tag text NOT NULL,
    daily_cost_limit_millicents bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (integration_id, tag),
    CONSTRAINT outbound_ai_tag_limits_tag_chk CHECK (tag ~ '^[a-z][a-z0-9_.-]{0,31}=[A-Za-z0-9._:@+/-]{1,64}$'),
    CONSTRAINT outbound_ai_tag_limits_cost_chk CHECK (daily_cost_limit_millicents BETWEEN 1 AND 1000000000000)
);

ALTER TABLE outbound_admission_leases ADD COLUMN IF NOT EXISTS ai_tags text[];
ALTER TABLE outbound_ai_request_log ADD COLUMN IF NOT EXISTS tags text[];

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_constraint
                   WHERE conname = 'outbound_admission_leases_ai_tags_chk' AND conrelid = 'outbound_admission_leases'::regclass) THEN
        ALTER TABLE outbound_admission_leases ADD CONSTRAINT outbound_admission_leases_ai_tags_chk
            CHECK (ai_tags IS NULL OR array_to_string(ai_tags, ',') ~
                   '^[a-z][a-z0-9_.-]{0,31}=[A-Za-z0-9._:@+/-]{1,64}(,[a-z][a-z0-9_.-]{0,31}=[A-Za-z0-9._:@+/-]{1,64}){0,4}$');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_constraint
                   WHERE conname = 'outbound_ai_request_log_tags_chk' AND conrelid = 'outbound_ai_request_log'::regclass) THEN
        ALTER TABLE outbound_ai_request_log ADD CONSTRAINT outbound_ai_request_log_tags_chk
            CHECK (tags IS NULL OR array_to_string(tags, ',') ~
                   '^[a-z][a-z0-9_.-]{0,31}=[A-Za-z0-9._:@+/-]{1,64}(,[a-z][a-z0-9_.-]{0,31}=[A-Za-z0-9._:@+/-]{1,64}){0,4}$');
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE outbound_ai_request_log DROP CONSTRAINT IF EXISTS outbound_ai_request_log_tags_chk, DROP COLUMN IF EXISTS tags;
ALTER TABLE outbound_admission_leases DROP CONSTRAINT IF EXISTS outbound_admission_leases_ai_tags_chk, DROP COLUMN IF EXISTS ai_tags;
DROP TABLE IF EXISTS outbound_ai_tag_limits;
DROP TABLE IF EXISTS outbound_ai_tag_usage_daily;
-- +goose StatementEnd
