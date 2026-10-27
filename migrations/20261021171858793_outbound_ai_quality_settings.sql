-- filename: 20261021171858793_outbound_ai_quality_settings.sql
-- ADR 20261021171712: integrations may sample answered calls for a judge
-- model (a per-day row bounds those evaluations) and turn on drift alerts,
-- one row per integration, model, metric and UTC day. The integration
-- check is added NOT VALID; 20261021171859791 validates it. Additive and
-- replay-safe.

-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
ALTER TABLE outbound_integrations
    ADD COLUMN IF NOT EXISTS ai_online_eval_percent smallint,
    ADD COLUMN IF NOT EXISTS ai_online_eval_judge_integration_id uuid,
    ADD COLUMN IF NOT EXISTS ai_online_eval_model text,
    ADD COLUMN IF NOT EXISTS ai_online_eval_rubric text,
    ADD COLUMN IF NOT EXISTS ai_quality_alerts boolean NOT NULL DEFAULT false;

CREATE TABLE IF NOT EXISTS outbound_ai_online_evaluation_days (
    integration_id uuid NOT NULL REFERENCES outbound_integrations(id) ON DELETE CASCADE,
    usage_date     date NOT NULL,
    evaluations    bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (integration_id, usage_date),
    CONSTRAINT outbound_ai_online_evaluation_days_count_chk CHECK (evaluations >= 0)
);

-- One row per integration, model, metric and UTC day the drift check
-- first finds a change; rates are basis points and scores hundredths.
CREATE TABLE IF NOT EXISTS outbound_ai_quality_alerts (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    integration_id uuid NOT NULL REFERENCES outbound_integrations(id) ON DELETE CASCADE,
    account_id     uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    usage_date     date NOT NULL,
    model          text NOT NULL,
    metric         text NOT NULL,
    current_value  bigint NOT NULL,
    baseline_value bigint NOT NULL,
    current_count  bigint NOT NULL,
    baseline_count bigint NOT NULL,
    detected_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT outbound_ai_quality_alerts_metric_chk
        CHECK (metric IN ('truncated', 'refused', 'empty', 'invalid_json', 'tool_error', 'bad_feedback', 'evaluation_score')),
    CONSTRAINT outbound_ai_quality_alerts_model_chk CHECK (length(model) BETWEEN 1 AND 200),
    CONSTRAINT outbound_ai_quality_alerts_values_chk
        CHECK (current_value >= 0 AND baseline_value >= 0 AND current_count >= 0 AND baseline_count >= 0)
);
CREATE UNIQUE INDEX IF NOT EXISTS outbound_ai_quality_alerts_once_idx
    ON outbound_ai_quality_alerts (integration_id, usage_date, model, metric);
CREATE INDEX IF NOT EXISTS outbound_ai_quality_alerts_recent_idx
    ON outbound_ai_quality_alerts (integration_id, detected_at DESC);
CREATE INDEX IF NOT EXISTS outbound_ai_quality_alerts_prune_idx ON outbound_ai_quality_alerts (detected_at);

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_constraint
                   WHERE conname = 'outbound_integrations_ai_quality_chk' AND conrelid = 'outbound_integrations'::regclass) THEN
        ALTER TABLE outbound_integrations ADD CONSTRAINT outbound_integrations_ai_quality_chk
            CHECK (((ai_online_eval_percent IS NULL
                    AND ai_online_eval_judge_integration_id IS NULL AND ai_online_eval_model IS NULL AND ai_online_eval_rubric IS NULL)
                    OR (ai_provider IS NOT NULL AND ai_online_eval_percent BETWEEN 1 AND 100
                        AND ai_online_eval_judge_integration_id IS NOT NULL
                        AND length(ai_online_eval_model) BETWEEN 1 AND 200
                        AND (ai_online_eval_rubric IS NULL OR octet_length(ai_online_eval_rubric) BETWEEN 1 AND 1000)))
                   AND (NOT ai_quality_alerts OR ai_provider IS NOT NULL)) NOT VALID;
    END IF;

END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS outbound_ai_quality_alerts;
DROP TABLE IF EXISTS outbound_ai_online_evaluation_days;
ALTER TABLE outbound_integrations DROP CONSTRAINT IF EXISTS outbound_integrations_ai_quality_chk,
    DROP COLUMN IF EXISTS ai_quality_alerts, DROP COLUMN IF EXISTS ai_online_eval_rubric,
    DROP COLUMN IF EXISTS ai_online_eval_model, DROP COLUMN IF EXISTS ai_online_eval_judge_integration_id,
    DROP COLUMN IF EXISTS ai_online_eval_percent;
-- +goose StatementEnd
