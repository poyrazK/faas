-- filename: 20261015091747396_outbound_ai_canaries_log_export_moderation.sql
-- ADR-1340: AI gateway phase 8. A route can run one canary target, judged
-- from per-minute rows per target that settlement writes while the canary
-- runs. Integrations can export settled app calls to the app's log drains
-- through an outbox the gateways read, and can add a moderation check that
-- runs through another of the account's AI integrations. Moderation calls
-- settle under their own caller. Additive and replay-safe.

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
ALTER TABLE outbound_integrations
    ADD COLUMN IF NOT EXISTS ai_log_export boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS ai_moderation_action text NOT NULL DEFAULT 'off',
    ADD COLUMN IF NOT EXISTS ai_moderation_kind text,
    ADD COLUMN IF NOT EXISTS ai_moderation_integration_id uuid REFERENCES outbound_integrations(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS ai_moderation_model text,
    ADD COLUMN IF NOT EXISTS ai_moderation_categories text[] NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS ai_moderation_on_error text NOT NULL DEFAULT 'allow';
-- Moderation calls settle under their own caller.
ALTER TABLE outbound_ai_usage_daily DROP CONSTRAINT IF EXISTS outbound_ai_usage_daily_caller_chk;
ALTER TABLE outbound_ai_usage_daily ADD CONSTRAINT outbound_ai_usage_daily_caller_chk
    CHECK (caller IN ('runs', 'replay', 'moderation') OR caller ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$');

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_constraint
                   WHERE conname = 'outbound_integrations_ai_log_export_chk' AND conrelid = 'outbound_integrations'::regclass) THEN
        ALTER TABLE outbound_integrations ADD CONSTRAINT outbound_integrations_ai_log_export_chk
            CHECK (NOT ai_log_export OR ai_provider IS NOT NULL);
    END IF;
    -- A deleted moderation integration leaves the check configured without
    -- one; the gateway then treats every check as failed (on_error decides).
    IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_constraint
                   WHERE conname = 'outbound_integrations_ai_moderation_chk' AND conrelid = 'outbound_integrations'::regclass) THEN
        ALTER TABLE outbound_integrations ADD CONSTRAINT outbound_integrations_ai_moderation_chk
            CHECK (ai_moderation_action IN ('off', 'flag', 'block')
                   AND ai_moderation_on_error IN ('allow', 'block')
                   AND (ai_moderation_kind IS NULL OR ai_moderation_kind IN ('openai_moderation', 'classifier'))
                   AND (ai_moderation_model IS NULL
                        OR (length(ai_moderation_model) BETWEEN 1 AND 128 AND ai_moderation_model ~ '^[ -~]+$'))
                   AND ai_moderation_categories <@ ARRAY['prompt_injection', 'harassment', 'hate', 'self_harm',
                                                         'sexual', 'violence', 'illicit']::text[]
                   AND (ai_moderation_action = 'off'
                        OR (ai_provider IS NOT NULL AND ai_moderation_kind IS NOT NULL
                            AND cardinality(ai_moderation_categories) BETWEEN 1 AND 7
                            AND (ai_moderation_kind = 'openai_moderation' OR ai_moderation_model IS NOT NULL)
                            AND (ai_moderation_kind = 'classifier'
                                 OR NOT ('prompt_injection' = ANY (ai_moderation_categories))))));
    END IF;
END
$$;

-- One canary per route, judged against the route's first target.
CREATE TABLE IF NOT EXISTS outbound_ai_route_canaries (
    route_id uuid PRIMARY KEY REFERENCES outbound_ai_routes(id) ON DELETE CASCADE,
    position smallint NOT NULL,
    percent smallint NOT NULL,
    max_error_rate_increase smallint NOT NULL,
    max_latency_increase_percent integer,
    min_calls integer NOT NULL,
    window_minutes smallint NOT NULL,
    state text NOT NULL DEFAULT 'active',
    reason text,
    started_at timestamptz NOT NULL DEFAULT now(),
    decided_at timestamptz,
    CONSTRAINT outbound_ai_route_canaries_config_chk
        CHECK (position BETWEEN 2 AND 5 AND percent BETWEEN 1 AND 50
               AND max_error_rate_increase BETWEEN 0 AND 100
               AND (max_latency_increase_percent IS NULL OR max_latency_increase_percent BETWEEN 1 AND 1000)
               AND min_calls BETWEEN 10 AND 100000 AND window_minutes BETWEEN 5 AND 1440),
    CONSTRAINT outbound_ai_route_canaries_state_chk
        CHECK ((state = 'active' AND reason IS NULL AND decided_at IS NULL)
               OR (state = 'rolled_back' AND reason IN ('error_rate', 'latency') AND decided_at IS NOT NULL))
);

-- Settled route attempts per target and UTC minute while a canary runs.
-- No foreign key: settlement writes these rows, and a route deleted under a
-- settlement must not fail it. apid deletes a route's rows with the route
-- and the hourly prune removes old ones.
CREATE TABLE IF NOT EXISTS outbound_ai_route_target_minutes (
    route_id uuid NOT NULL,
    position smallint NOT NULL,
    minute timestamptz NOT NULL,
    calls bigint NOT NULL DEFAULT 0,
    errors bigint NOT NULL DEFAULT 0,
    ttfb_calls bigint NOT NULL DEFAULT 0,
    ttfb_ms_total bigint NOT NULL DEFAULT 0,
    cost_millicents bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (route_id, position, minute),
    CONSTRAINT outbound_ai_route_target_minutes_values_chk
        CHECK (position BETWEEN 1 AND 5 AND calls >= 0 AND errors BETWEEN 0 AND calls
               AND ttfb_calls BETWEEN 0 AND calls AND ttfb_ms_total >= 0 AND cost_millicents >= 0)
);
CREATE INDEX IF NOT EXISTS outbound_ai_route_target_minutes_prune_idx ON outbound_ai_route_target_minutes (minute);

-- Export lines for the calling app's log drains. The gateways read rows
-- past their cursor once they are older than the visibility horizon.
CREATE TABLE IF NOT EXISTS outbound_ai_log_exports (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    app_id uuid NOT NULL,
    account_id uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    line text NOT NULL,
    attributes jsonb NOT NULL DEFAULT '{}'::jsonb,
    CONSTRAINT outbound_ai_log_exports_line_chk CHECK (octet_length(line) BETWEEN 2 AND 4096),
    CONSTRAINT outbound_ai_log_exports_attributes_chk
        CHECK (jsonb_typeof(attributes) = 'object' AND octet_length(attributes::text) <= 2048)
);
CREATE INDEX IF NOT EXISTS outbound_ai_log_exports_app_idx ON outbound_ai_log_exports (app_id, id);
CREATE INDEX IF NOT EXISTS outbound_ai_log_exports_prune_idx ON outbound_ai_log_exports (created_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS outbound_ai_log_exports;
DROP TABLE IF EXISTS outbound_ai_route_target_minutes;
DROP TABLE IF EXISTS outbound_ai_route_canaries;
DELETE FROM outbound_ai_usage_daily WHERE caller = 'moderation';
ALTER TABLE outbound_ai_usage_daily DROP CONSTRAINT IF EXISTS outbound_ai_usage_daily_caller_chk;
ALTER TABLE outbound_ai_usage_daily ADD CONSTRAINT outbound_ai_usage_daily_caller_chk
    CHECK (caller IN ('runs', 'replay') OR caller ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$');
ALTER TABLE outbound_integrations
    DROP CONSTRAINT IF EXISTS outbound_integrations_ai_moderation_chk,
    DROP CONSTRAINT IF EXISTS outbound_integrations_ai_log_export_chk,
    DROP COLUMN IF EXISTS ai_moderation_on_error,
    DROP COLUMN IF EXISTS ai_moderation_categories,
    DROP COLUMN IF EXISTS ai_moderation_model,
    DROP COLUMN IF EXISTS ai_moderation_integration_id,
    DROP COLUMN IF EXISTS ai_moderation_kind,
    DROP COLUMN IF EXISTS ai_moderation_action,
    DROP COLUMN IF EXISTS ai_log_export;
-- +goose StatementEnd
