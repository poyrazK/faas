-- filename: 20261016100514072_outbound_ai_evaluations_access_tokens_batches.sql
-- ADR-1360: AI gateway phase 9. Evaluations replay captured calls on a
-- route's candidate target and judge both answers; apid inserts one,
-- outboundd claims it and writes the items. Access tokens let callers
-- outside Gregale act as one app; apid writes hashes, outboundd reads them.
-- Batches keep a created batch's reservation until outboundd settles it.
-- Evaluation calls settle under their own caller. Additive and replay-safe.

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
ALTER TABLE outbound_ai_usage_daily DROP CONSTRAINT IF EXISTS outbound_ai_usage_daily_caller_chk;
ALTER TABLE outbound_ai_usage_daily ADD CONSTRAINT outbound_ai_usage_daily_caller_chk
    CHECK (caller IN ('runs', 'replay', 'moderation', 'evaluation')
           OR caller ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$');

CREATE TABLE IF NOT EXISTS outbound_ai_evaluations (
    id uuid PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    route_id uuid NOT NULL REFERENCES outbound_ai_routes(id) ON DELETE CASCADE,
    position smallint NOT NULL,
    judge_integration_id uuid NOT NULL,
    judge_model text NOT NULL,
    sample_size smallint NOT NULL,
    max_loss_rate smallint NOT NULL,
    canary jsonb,
    state text NOT NULL DEFAULT 'pending',
    verdict text,
    error_code text,
    samples integer NOT NULL DEFAULT 0,
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
    CONSTRAINT outbound_ai_evaluations_config_chk
        CHECK (position BETWEEN 2 AND 5 AND sample_size BETWEEN 1 AND 50 AND max_loss_rate BETWEEN 0 AND 100
               AND length(judge_model) BETWEEN 1 AND 128 AND judge_model ~ '^[ -~]+$'
               AND (canary IS NULL OR (jsonb_typeof(canary) = 'object' AND octet_length(canary::text) <= 1024))),
    CONSTRAINT outbound_ai_evaluations_state_chk
        CHECK (state IN ('pending', 'running', 'done', 'failed')
               AND (verdict IS NULL OR verdict IN ('pass', 'fail'))
               AND (state = 'done') = (verdict IS NOT NULL)
               AND (state = 'failed') = (error_code IS NOT NULL)
               AND (error_code IS NULL OR (length(error_code) BETWEEN 1 AND 64 AND error_code ~ '^[a-z0-9_]+$'))),
    CONSTRAINT outbound_ai_evaluations_counts_chk
        CHECK (samples BETWEEN 0 AND 50 AND wins >= 0 AND ties >= 0 AND losses >= 0 AND candidate_errors >= 0
               AND judge_errors >= 0 AND wins + ties + losses + candidate_errors <= samples
               AND baseline_cost_millicents >= 0 AND candidate_cost_millicents >= 0
               AND baseline_ttfb_ms_total >= 0 AND baseline_ttfb_calls >= 0
               AND candidate_ttfb_ms_total >= 0 AND candidate_ttfb_calls >= 0)
);
CREATE INDEX IF NOT EXISTS outbound_ai_evaluations_claim_idx ON outbound_ai_evaluations (state, created_at);
CREATE INDEX IF NOT EXISTS outbound_ai_evaluations_route_idx ON outbound_ai_evaluations (route_id, created_at DESC);

CREATE TABLE IF NOT EXISTS outbound_ai_evaluation_items (
    evaluation_id uuid NOT NULL REFERENCES outbound_ai_evaluations(id) ON DELETE CASCADE,
    ordinal smallint NOT NULL,
    log_id uuid NOT NULL,
    verdict text NOT NULL,
    candidate_status smallint,
    candidate_cost_millicents bigint NOT NULL DEFAULT 0,
    candidate_ttfb_ms integer,
    baseline_cost_millicents bigint NOT NULL DEFAULT 0,
    baseline_ttfb_ms integer,
    baseline_output_text text NOT NULL DEFAULT '',
    candidate_output_text text NOT NULL DEFAULT '',
    PRIMARY KEY (evaluation_id, ordinal),
    CONSTRAINT outbound_ai_evaluation_items_chk
        CHECK (ordinal BETWEEN 0 AND 49
               AND verdict IN ('win', 'tie', 'loss', 'candidate_error', 'judge_error')
               AND (candidate_status IS NULL OR candidate_status BETWEEN 100 AND 599)
               AND candidate_cost_millicents >= 0 AND baseline_cost_millicents >= 0
               AND (candidate_ttfb_ms IS NULL OR candidate_ttfb_ms >= 0)
               AND (baseline_ttfb_ms IS NULL OR baseline_ttfb_ms >= 0)
               AND octet_length(baseline_output_text) <= 4096 AND octet_length(candidate_output_text) <= 4096)
);

CREATE TABLE IF NOT EXISTS outbound_ai_access_tokens (
    id uuid PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    name text NOT NULL,
    token_hash bytea NOT NULL,
    token_prefix text NOT NULL,
    integration_ids uuid[] NOT NULL DEFAULT '{}',
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz,
    revoked_at timestamptz,
    CONSTRAINT outbound_ai_access_tokens_name_chk CHECK (name ~ '^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$'),
    CONSTRAINT outbound_ai_access_tokens_secret_chk
        CHECK (octet_length(token_hash) = 32 AND token_prefix ~ '^ggai_[A-Za-z0-9_-]{7}$'),
    CONSTRAINT outbound_ai_access_tokens_scope_chk
        CHECK (cardinality(integration_ids) <= 32 AND expires_at > created_at)
);
CREATE UNIQUE INDEX IF NOT EXISTS outbound_ai_access_tokens_hash_idx ON outbound_ai_access_tokens (token_hash);
-- A revoked token's name can be reused.
CREATE UNIQUE INDEX IF NOT EXISTS outbound_ai_access_tokens_name_idx ON outbound_ai_access_tokens (app_id, name)
    WHERE revoked_at IS NULL;

-- A batch's reservation counts in admission until it is settled.
CREATE TABLE IF NOT EXISTS outbound_ai_batches (
    id uuid PRIMARY KEY,
    integration_id uuid NOT NULL REFERENCES outbound_integrations(id) ON DELETE CASCADE,
    account_id uuid NOT NULL,
    caller text NOT NULL,
    binding_app_id uuid,
    provider text NOT NULL,
    provider_batch_id text,
    request_path text NOT NULL,
    request_query text NOT NULL DEFAULT '',
    model text NOT NULL,
    requests integer NOT NULL,
    reserved_tokens bigint NOT NULL,
    reserved_cost_millicents bigint NOT NULL,
    state text NOT NULL DEFAULT 'pending',
    outcome text,
    settled_tokens bigint,
    settled_cost_millicents bigint,
    checks integer NOT NULL DEFAULT 0,
    next_check_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now(),
    settled_at timestamptz,
    CONSTRAINT outbound_ai_batches_values_chk
        CHECK (provider IN ('openai', 'azure_openai', 'anthropic')
               AND (caller IN ('runs') OR caller ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$')
               AND (provider_batch_id IS NULL OR (length(provider_batch_id) BETWEEN 1 AND 128 AND provider_batch_id ~ '^[A-Za-z0-9_-]+$'))
               AND length(request_path) BETWEEN 1 AND 2048 AND length(request_query) <= 2048
               AND length(model) BETWEEN 1 AND 128 AND model ~ '^[ -~]+$'
               AND requests >= 0 AND reserved_tokens >= 0 AND reserved_cost_millicents >= 0 AND checks >= 0),
    CONSTRAINT outbound_ai_batches_state_chk
        CHECK ((state = 'pending' AND provider_batch_id IS NULL AND settled_at IS NULL)
               OR (state = 'open' AND provider_batch_id IS NOT NULL AND settled_at IS NULL)
               OR (state = 'settled' AND settled_at IS NOT NULL AND outcome IN ('known', 'unknown', 'error')
                   AND settled_tokens >= 0 AND settled_cost_millicents >= 0))
);
CREATE INDEX IF NOT EXISTS outbound_ai_batches_reserved_idx ON outbound_ai_batches (integration_id) WHERE state <> 'settled';
CREATE INDEX IF NOT EXISTS outbound_ai_batches_poll_idx ON outbound_ai_batches (next_check_at) WHERE state = 'open';
CREATE INDEX IF NOT EXISTS outbound_ai_batches_prune_idx ON outbound_ai_batches (settled_at) WHERE state = 'settled';
CREATE UNIQUE INDEX IF NOT EXISTS outbound_ai_batches_provider_idx
    ON outbound_ai_batches (integration_id, provider_batch_id) WHERE provider_batch_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS outbound_ai_batches;
DROP TABLE IF EXISTS outbound_ai_access_tokens;
DROP TABLE IF EXISTS outbound_ai_evaluation_items;
DROP TABLE IF EXISTS outbound_ai_evaluations;
DELETE FROM outbound_ai_usage_daily WHERE caller = 'evaluation';
ALTER TABLE outbound_ai_usage_daily DROP CONSTRAINT IF EXISTS outbound_ai_usage_daily_caller_chk;
ALTER TABLE outbound_ai_usage_daily ADD CONSTRAINT outbound_ai_usage_daily_caller_chk
    CHECK (caller IN ('runs', 'replay', 'moderation') OR caller ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$');
-- +goose StatementEnd
