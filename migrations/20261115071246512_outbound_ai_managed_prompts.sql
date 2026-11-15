-- filename: 20261114181043624_outbound_ai_managed_prompts.sql
-- ADR 20261114180740: managed AI prompts. apid writes prompts, versions,
-- canaries and evaluations; outboundd reads them per call, adds settled
-- attempts to per-version minute rows, rolls canaries back and runs
-- evaluations. A version stores its route or integration without a foreign
-- key, so deleting either never fails because of a prompt.
--
-- The request log gains the prompt and version of a prompt call. Its CHECK
-- is added NOT VALID and validated in its own statement, and its index is
-- built concurrently, so request-log writes never wait. Every step is
-- guarded, so a rerun after a partial failure converges.
-- migration-compat: expand
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS outbound_ai_prompts (
    id uuid PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    name text NOT NULL,
    description text NOT NULL DEFAULT '',
    live_version integer,
    previous_live_version integer,
    latest_version integer NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT outbound_ai_prompts_name_key UNIQUE (account_id, name),
    CONSTRAINT outbound_ai_prompts_chk CHECK (
        name ~ '^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$'
        AND octet_length(description) <= 280
        AND latest_version BETWEEN 0 AND 2147483647
        AND (live_version IS NULL OR live_version BETWEEN 1 AND latest_version)
        AND (previous_live_version IS NULL OR previous_live_version BETWEEN 1 AND latest_version))
);

CREATE TABLE IF NOT EXISTS outbound_ai_prompt_versions (
    prompt_id uuid NOT NULL REFERENCES outbound_ai_prompts(id) ON DELETE CASCADE,
    version integer NOT NULL,
    messages jsonb NOT NULL,
    defaults jsonb NOT NULL DEFAULT '{}'::jsonb,
    params jsonb NOT NULL DEFAULT '{}'::jsonb,
    route_id uuid,
    integration_id uuid,
    model text,
    note text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (prompt_id, version),
    CONSTRAINT outbound_ai_prompt_versions_chk CHECK (
        version >= 1
        AND jsonb_typeof(messages) = 'array' AND jsonb_array_length(messages) BETWEEN 1 AND 32
        AND jsonb_typeof(defaults) = 'object' AND jsonb_typeof(params) = 'object'
        AND octet_length(messages::text) + octet_length(defaults::text) + octet_length(params::text) <= 196608
        AND ((route_id IS NULL) <> (integration_id IS NULL))
        AND (integration_id IS NULL OR model IS NOT NULL)
        AND (model IS NULL OR (length(model) BETWEEN 1 AND 128 AND model ~ '^[ -~]+$'))
        AND octet_length(note) <= 280)
);

CREATE TABLE IF NOT EXISTS outbound_ai_prompt_canaries (
    prompt_id uuid PRIMARY KEY REFERENCES outbound_ai_prompts(id) ON DELETE CASCADE,
    version integer NOT NULL,
    percent smallint NOT NULL,
    max_error_rate_increase smallint NOT NULL,
    max_latency_increase_percent integer,
    min_calls integer NOT NULL,
    window_minutes smallint NOT NULL,
    state text NOT NULL DEFAULT 'active',
    reason text,
    started_at timestamptz NOT NULL DEFAULT now(),
    decided_at timestamptz,
    CONSTRAINT outbound_ai_prompt_canaries_config_chk CHECK (
        version >= 1 AND percent BETWEEN 1 AND 50 AND max_error_rate_increase BETWEEN 0 AND 100
        AND (max_latency_increase_percent IS NULL OR max_latency_increase_percent BETWEEN 1 AND 1000)
        AND min_calls BETWEEN 10 AND 100000 AND window_minutes BETWEEN 5 AND 1440),
    CONSTRAINT outbound_ai_prompt_canaries_state_chk CHECK (
        (state = 'active' AND reason IS NULL AND decided_at IS NULL)
        OR (state = 'rolled_back' AND reason IN ('error_rate', 'latency') AND decided_at IS NOT NULL))
);

-- No foreign key, as outbound_ai_route_target_minutes: a settlement never
-- fails because the prompt was deleted meanwhile.
CREATE TABLE IF NOT EXISTS outbound_ai_prompt_version_minutes (
    prompt_id uuid NOT NULL,
    version integer NOT NULL,
    minute timestamptz NOT NULL,
    calls bigint NOT NULL DEFAULT 0,
    errors bigint NOT NULL DEFAULT 0,
    ttfb_calls bigint NOT NULL DEFAULT 0,
    ttfb_ms_total bigint NOT NULL DEFAULT 0,
    cost_millicents bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (prompt_id, version, minute),
    CONSTRAINT outbound_ai_prompt_version_minutes_chk CHECK (
        version >= 1 AND calls >= 0 AND errors BETWEEN 0 AND calls AND ttfb_calls BETWEEN 0 AND calls
        AND ttfb_ms_total >= 0 AND cost_millicents >= 0)
);
CREATE INDEX IF NOT EXISTS outbound_ai_prompt_version_minutes_prune_idx ON outbound_ai_prompt_version_minutes (minute);

CREATE TABLE IF NOT EXISTS outbound_ai_prompt_evaluations (
    id uuid PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    prompt_id uuid NOT NULL REFERENCES outbound_ai_prompts(id) ON DELETE CASCADE,
    baseline_version integer NOT NULL,
    candidate_version integer NOT NULL,
    cases jsonb NOT NULL,
    judge_integration_id uuid NOT NULL,
    judge_model text NOT NULL,
    max_loss_rate smallint NOT NULL,
    canary jsonb,
    state text NOT NULL DEFAULT 'pending',
    verdict text,
    error_code text,
    case_count integer NOT NULL,
    wins integer NOT NULL DEFAULT 0,
    ties integer NOT NULL DEFAULT 0,
    losses integer NOT NULL DEFAULT 0,
    candidate_errors integer NOT NULL DEFAULT 0,
    judge_errors integer NOT NULL DEFAULT 0,
    baseline_cost_millicents bigint NOT NULL DEFAULT 0,
    candidate_cost_millicents bigint NOT NULL DEFAULT 0,
    baseline_ttfb_ms_total bigint NOT NULL DEFAULT 0,
    baseline_ttfb_calls integer NOT NULL DEFAULT 0,
    candidate_ttfb_ms_total bigint NOT NULL DEFAULT 0,
    candidate_ttfb_calls integer NOT NULL DEFAULT 0,
    canary_started boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    started_at timestamptz,
    finished_at timestamptz,
    CONSTRAINT outbound_ai_prompt_evaluations_config_chk CHECK (
        baseline_version >= 1 AND candidate_version >= 1 AND baseline_version <> candidate_version
        AND jsonb_typeof(cases) = 'array' AND jsonb_array_length(cases) = case_count AND case_count BETWEEN 1 AND 50
        AND octet_length(cases::text) <= 1048576
        AND max_loss_rate BETWEEN 0 AND 100 AND length(judge_model) BETWEEN 1 AND 128 AND judge_model ~ '^[ -~]+$'
        AND (canary IS NULL OR (jsonb_typeof(canary) = 'object' AND octet_length(canary::text) <= 1024))),
    CONSTRAINT outbound_ai_prompt_evaluations_counts_chk CHECK (
        wins >= 0 AND ties >= 0 AND losses >= 0 AND candidate_errors >= 0 AND judge_errors >= 0
        AND wins + ties + losses + candidate_errors + judge_errors <= case_count
        AND baseline_cost_millicents >= 0 AND candidate_cost_millicents >= 0
        AND baseline_ttfb_ms_total >= 0 AND baseline_ttfb_calls >= 0
        AND candidate_ttfb_ms_total >= 0 AND candidate_ttfb_calls >= 0),
    CONSTRAINT outbound_ai_prompt_evaluations_state_chk CHECK (
        state IN ('pending', 'running', 'done', 'failed')
        AND (verdict IS NULL OR verdict IN ('pass', 'fail'))
        AND ((state = 'done') = (verdict IS NOT NULL))
        AND ((state = 'failed') = (error_code IS NOT NULL))
        AND ((state = 'pending') = (started_at IS NULL))
        AND (error_code IS NULL OR (length(error_code) BETWEEN 1 AND 64 AND error_code ~ '^[a-z0-9_]+$')))
);
CREATE INDEX IF NOT EXISTS outbound_ai_prompt_evaluations_prompt_idx
    ON outbound_ai_prompt_evaluations (prompt_id, created_at DESC);
CREATE INDEX IF NOT EXISTS outbound_ai_prompt_evaluations_pending_idx
    ON outbound_ai_prompt_evaluations (created_at) WHERE state IN ('pending', 'running');

CREATE TABLE IF NOT EXISTS outbound_ai_prompt_evaluation_items (
    evaluation_id uuid NOT NULL REFERENCES outbound_ai_prompt_evaluations(id) ON DELETE CASCADE,
    case_index smallint NOT NULL,
    verdict text NOT NULL,
    baseline_status smallint,
    candidate_status smallint,
    baseline_cost_millicents bigint NOT NULL DEFAULT 0,
    candidate_cost_millicents bigint NOT NULL DEFAULT 0,
    baseline_ttfb_ms integer,
    candidate_ttfb_ms integer,
    baseline_output_text text NOT NULL DEFAULT '',
    candidate_output_text text NOT NULL DEFAULT '',
    PRIMARY KEY (evaluation_id, case_index),
    CONSTRAINT outbound_ai_prompt_evaluation_items_chk CHECK (
        case_index BETWEEN 0 AND 49
        AND verdict IN ('win', 'tie', 'loss', 'candidate_error', 'judge_error')
        AND (baseline_status IS NULL OR baseline_status BETWEEN 100 AND 599)
        AND (candidate_status IS NULL OR candidate_status BETWEEN 100 AND 599)
        AND baseline_cost_millicents >= 0 AND candidate_cost_millicents >= 0
        AND (baseline_ttfb_ms IS NULL OR baseline_ttfb_ms >= 0) AND (candidate_ttfb_ms IS NULL OR candidate_ttfb_ms >= 0)
        AND octet_length(baseline_output_text) <= 4096 AND octet_length(candidate_output_text) <= 4096)
);

ALTER TABLE outbound_ai_request_log
    ADD COLUMN IF NOT EXISTS prompt_id uuid,
    ADD COLUMN IF NOT EXISTS prompt_version integer;
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'outbound_ai_request_log'::regclass
                AND conname = 'outbound_ai_request_log_prompt_chk') THEN
  ALTER TABLE outbound_ai_request_log ADD CONSTRAINT outbound_ai_request_log_prompt_chk
    CHECK ((prompt_id IS NULL) = (prompt_version IS NULL) AND (prompt_version IS NULL OR prompt_version >= 1)) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE outbound_ai_request_log VALIDATE CONSTRAINT outbound_ai_request_log_prompt_chk;
RESET lock_timeout;

SET statement_timeout = 0;
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS outbound_ai_request_log_prompt_idx;
CREATE INDEX CONCURRENTLY outbound_ai_request_log_prompt_idx
    ON outbound_ai_request_log (prompt_id, prompt_version, created_at) WHERE prompt_id IS NOT NULL;
RESET lock_timeout;
RESET statement_timeout;

-- +goose Down
SET statement_timeout = 0;
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS outbound_ai_request_log_prompt_idx;
ALTER TABLE outbound_ai_request_log
    DROP CONSTRAINT IF EXISTS outbound_ai_request_log_prompt_chk,
    DROP COLUMN IF EXISTS prompt_version,
    DROP COLUMN IF EXISTS prompt_id;
DROP TABLE IF EXISTS outbound_ai_prompt_evaluation_items;
DROP TABLE IF EXISTS outbound_ai_prompt_evaluations;
DROP TABLE IF EXISTS outbound_ai_prompt_version_minutes;
DROP TABLE IF EXISTS outbound_ai_prompt_canaries;
DROP TABLE IF EXISTS outbound_ai_prompt_versions;
DROP TABLE IF EXISTS outbound_ai_prompts;
RESET lock_timeout;
RESET statement_timeout;
