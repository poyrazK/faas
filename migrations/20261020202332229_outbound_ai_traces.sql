-- filename: 20261020202332229_outbound_ai_traces.sql
-- ADR 20261020202035: AI traces group the calls of one agent task. Each
-- traced call adds to its trace's row; a per-day row bounds the new traces
-- an integration records. Integrations may cap each trace's calls, cost and
-- consecutive identical tool requests; leases record their call's trace so
-- calls in flight count, and the request log keeps each call's trace and
-- requested tools. Checks on existing tables are added NOT VALID;
-- 20261020202333289 validates them and indexes the request log by trace.
-- Additive and replay-safe.

-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS outbound_ai_traces (
    integration_id      uuid NOT NULL REFERENCES outbound_integrations(id) ON DELETE CASCADE,
    trace_id            text NOT NULL,
    caller              text NOT NULL,
    end_user            text,
    first_at            timestamptz NOT NULL DEFAULT now(),
    last_at             timestamptz NOT NULL DEFAULT now(),
    calls               bigint NOT NULL DEFAULT 0,
    error_calls         bigint NOT NULL DEFAULT 0,
    usage_unknown_calls bigint NOT NULL DEFAULT 0,
    input_tokens        bigint NOT NULL DEFAULT 0,
    output_tokens       bigint NOT NULL DEFAULT 0,
    cost_millicents     bigint NOT NULL DEFAULT 0,
    tool_calls          bigint NOT NULL DEFAULT 0,
    tool_signature      bytea,
    repeated_tool_calls bigint NOT NULL DEFAULT 0,
    models              text[] NOT NULL DEFAULT '{}',
    PRIMARY KEY (integration_id, trace_id),
    CONSTRAINT outbound_ai_traces_trace_id_chk CHECK (trace_id ~ '^[A-Za-z0-9._:@+=/|-]{1,64}$'),
    CONSTRAINT outbound_ai_traces_caller_chk CHECK (length(caller) BETWEEN 1 AND 64),
    CONSTRAINT outbound_ai_traces_end_user_chk CHECK (end_user IS NULL OR end_user ~ '^[A-Za-z0-9._:@+=/|-]{1,64}$'),
    CONSTRAINT outbound_ai_traces_values_chk
        CHECK (calls >= 0 AND error_calls BETWEEN 0 AND calls AND usage_unknown_calls BETWEEN 0 AND calls
               AND input_tokens >= 0 AND output_tokens >= 0 AND cost_millicents >= 0 AND tool_calls >= 0
               AND repeated_tool_calls BETWEEN 0 AND calls AND first_at <= last_at),
    CONSTRAINT outbound_ai_traces_tool_signature_chk CHECK (tool_signature IS NULL OR octet_length(tool_signature) = 16),
    CONSTRAINT outbound_ai_traces_models_chk CHECK (cardinality(models) <= 8)
);
CREATE INDEX IF NOT EXISTS outbound_ai_traces_recent_idx ON outbound_ai_traces (integration_id, last_at DESC);
CREATE INDEX IF NOT EXISTS outbound_ai_traces_prune_idx ON outbound_ai_traces (last_at);

CREATE TABLE IF NOT EXISTS outbound_ai_trace_days (
    integration_id uuid NOT NULL REFERENCES outbound_integrations(id) ON DELETE CASCADE,
    usage_date     date NOT NULL,
    traces         bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (integration_id, usage_date),
    CONSTRAINT outbound_ai_trace_days_traces_chk CHECK (traces >= 0)
);

ALTER TABLE outbound_integrations
    ADD COLUMN IF NOT EXISTS ai_trace_max_calls bigint,
    ADD COLUMN IF NOT EXISTS ai_trace_max_cost_millicents bigint,
    ADD COLUMN IF NOT EXISTS ai_trace_max_repeated_tool_calls bigint;
ALTER TABLE outbound_admission_leases ADD COLUMN IF NOT EXISTS ai_trace_id text;
ALTER TABLE outbound_ai_request_log
    ADD COLUMN IF NOT EXISTS trace_id text,
    ADD COLUMN IF NOT EXISTS tool_calls text[];

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_constraint
                   WHERE conname = 'outbound_integrations_ai_trace_limits_chk' AND conrelid = 'outbound_integrations'::regclass) THEN
        ALTER TABLE outbound_integrations ADD CONSTRAINT outbound_integrations_ai_trace_limits_chk
            CHECK ((ai_trace_max_calls IS NULL OR (ai_provider IS NOT NULL AND ai_trace_max_calls BETWEEN 1 AND 1000000))
                   AND (ai_trace_max_cost_millicents IS NULL
                        OR (ai_provider IS NOT NULL AND ai_trace_max_cost_millicents BETWEEN 1 AND 1000000000000))
                   AND (ai_trace_max_repeated_tool_calls IS NULL
                        OR (ai_provider IS NOT NULL AND ai_trace_max_repeated_tool_calls BETWEEN 1 AND 1000))) NOT VALID;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_constraint
                   WHERE conname = 'outbound_admission_leases_ai_trace_id_chk' AND conrelid = 'outbound_admission_leases'::regclass) THEN
        ALTER TABLE outbound_admission_leases ADD CONSTRAINT outbound_admission_leases_ai_trace_id_chk
            CHECK (ai_trace_id IS NULL OR ai_trace_id ~ '^[A-Za-z0-9._:@+=/|-]{1,64}$') NOT VALID;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_constraint
                   WHERE conname = 'outbound_ai_request_log_trace_chk' AND conrelid = 'outbound_ai_request_log'::regclass) THEN
        ALTER TABLE outbound_ai_request_log ADD CONSTRAINT outbound_ai_request_log_trace_chk
            CHECK ((trace_id IS NULL OR trace_id ~ '^[A-Za-z0-9._:@+=/|-]{1,64}$')
                   AND (tool_calls IS NULL OR cardinality(tool_calls) <= 16)) NOT VALID;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE outbound_ai_request_log DROP CONSTRAINT IF EXISTS outbound_ai_request_log_trace_chk,
    DROP COLUMN IF EXISTS tool_calls, DROP COLUMN IF EXISTS trace_id;
ALTER TABLE outbound_admission_leases DROP CONSTRAINT IF EXISTS outbound_admission_leases_ai_trace_id_chk,
    DROP COLUMN IF EXISTS ai_trace_id;
ALTER TABLE outbound_integrations DROP CONSTRAINT IF EXISTS outbound_integrations_ai_trace_limits_chk,
    DROP COLUMN IF EXISTS ai_trace_max_repeated_tool_calls, DROP COLUMN IF EXISTS ai_trace_max_cost_millicents,
    DROP COLUMN IF EXISTS ai_trace_max_calls;
DROP TABLE IF EXISTS outbound_ai_trace_days;
DROP TABLE IF EXISTS outbound_ai_traces;
-- +goose StatementEnd
