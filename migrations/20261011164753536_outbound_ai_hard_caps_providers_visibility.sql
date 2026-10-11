-- filename: 20261011164753536_outbound_ai_hard_caps_providers_visibility.sql
-- ADR-984: admission-time AI reservations, per-app per-minute limits and
-- guardrails, four more AI providers, OpenAI-format routes, extra provider
-- keys, budget alerts and the opt-in AI request log. Additive and replay-safe.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE outbound_integrations
    ADD COLUMN IF NOT EXISTS ai_request_log_enabled boolean NOT NULL DEFAULT false;
ALTER TABLE outbound_integrations DROP CONSTRAINT IF EXISTS outbound_integrations_ai_provider_chk;
ALTER TABLE outbound_integrations ADD CONSTRAINT outbound_integrations_ai_provider_chk
    CHECK (ai_provider IS NULL OR ai_provider IN ('openai', 'anthropic', 'gemini', 'azure_openai', 'bedrock', 'vertex'));

-- A lease carries the reservation of the call it admits, so in-flight AI
-- calls count against hard caps and disappear with the lease.
ALTER TABLE outbound_admission_leases
    ADD COLUMN IF NOT EXISTS binding_app_id uuid,
    ADD COLUMN IF NOT EXISTS ai_reserved_tokens bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS ai_reserved_cost_millicents bigint NOT NULL DEFAULT 0;

ALTER TABLE outbound_app_bindings
    ADD COLUMN IF NOT EXISTS ai_requests_per_minute integer,
    ADD COLUMN IF NOT EXISTS ai_tokens_per_minute bigint,
    ADD COLUMN IF NOT EXISTS ai_allowed_models text[],
    ADD COLUMN IF NOT EXISTS ai_max_output_tokens integer;

ALTER TABLE outbound_app_binding_usage
    ADD COLUMN IF NOT EXISTS minute_window_start timestamptz,
    ADD COLUMN IF NOT EXISTS minute_request_count integer NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS minute_ai_token_count bigint NOT NULL DEFAULT 0;

ALTER TABLE outbound_ai_routes
    ADD COLUMN IF NOT EXISTS format text NOT NULL DEFAULT 'native';

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_constraint
                   WHERE conname = 'outbound_integrations_ai_request_log_chk' AND conrelid = 'outbound_integrations'::regclass) THEN
        ALTER TABLE outbound_integrations ADD CONSTRAINT outbound_integrations_ai_request_log_chk
            CHECK (NOT ai_request_log_enabled OR ai_provider IS NOT NULL);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_constraint
                   WHERE conname = 'outbound_admission_leases_ai_reservation_chk' AND conrelid = 'outbound_admission_leases'::regclass) THEN
        ALTER TABLE outbound_admission_leases ADD CONSTRAINT outbound_admission_leases_ai_reservation_chk
            CHECK (ai_reserved_tokens >= 0 AND ai_reserved_cost_millicents >= 0);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_constraint
                   WHERE conname = 'outbound_app_bindings_ai_guardrails_chk' AND conrelid = 'outbound_app_bindings'::regclass) THEN
        ALTER TABLE outbound_app_bindings ADD CONSTRAINT outbound_app_bindings_ai_guardrails_chk
            CHECK ((ai_requests_per_minute IS NULL OR ai_requests_per_minute BETWEEN 1 AND 100000)
                   AND (ai_tokens_per_minute IS NULL OR ai_tokens_per_minute BETWEEN 1 AND 1000000000)
                   AND (ai_max_output_tokens IS NULL OR ai_max_output_tokens BETWEEN 1 AND 1000000)
                   AND (ai_allowed_models IS NULL
                        OR (cardinality(ai_allowed_models) BETWEEN 1 AND 32
                            AND array_position(ai_allowed_models, NULL) IS NULL)));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_constraint
                   WHERE conname = 'outbound_app_binding_usage_minute_chk' AND conrelid = 'outbound_app_binding_usage'::regclass) THEN
        ALTER TABLE outbound_app_binding_usage ADD CONSTRAINT outbound_app_binding_usage_minute_chk
            CHECK (minute_request_count >= 0 AND minute_ai_token_count >= 0);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_constraint
                   WHERE conname = 'outbound_ai_routes_format_chk' AND conrelid = 'outbound_ai_routes'::regclass) THEN
        ALTER TABLE outbound_ai_routes ADD CONSTRAINT outbound_ai_routes_format_chk
            CHECK (format IN ('native', 'openai'));
    END IF;
END$$;

-- Extra sealed provider keys for one integration. The primary credential
-- stays in outbound_integration_credentials; values are write-only.
CREATE TABLE IF NOT EXISTS outbound_integration_credential_keys (
    id                   uuid PRIMARY KEY,
    integration_id       uuid NOT NULL REFERENCES outbound_integrations (id) ON DELETE CASCADE,
    account_id           uuid NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    label                text NOT NULL,
    authorization_sealed bytea NOT NULL,
    created_at           timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT outbound_integration_credential_keys_label_chk
        CHECK (label ~ '^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$'),
    CONSTRAINT outbound_integration_credential_keys_sealed_chk
        CHECK (octet_length(authorization_sealed) BETWEEN 1 AND 65536),
    CONSTRAINT outbound_integration_credential_keys_label_key UNIQUE (integration_id, label)
);
DROP TRIGGER IF EXISTS outbound_credential_key_owner_guard ON outbound_integration_credential_keys;
CREATE TRIGGER outbound_credential_key_owner_guard
    BEFORE INSERT OR UPDATE OF integration_id, account_id, authorization_sealed ON outbound_integration_credential_keys
    FOR EACH ROW EXECUTE FUNCTION guard_outbound_credential_owner();

-- Leaving customer-sealed managed auth drops every sealed key, not only the
-- primary credential.
CREATE OR REPLACE FUNCTION purge_inactive_outbound_credential() RETURNS trigger LANGUAGE plpgsql AS $fn$
BEGIN
    IF OLD.credential_source = 'customer_sealed'
       AND (NEW.credential_source <> 'customer_sealed' OR NEW.provider_auth_mode <> 'managed') THEN
        DELETE FROM outbound_integration_credentials WHERE integration_id = NEW.id;
        DELETE FROM outbound_integration_credential_keys WHERE integration_id = NEW.id;
    END IF;
    RETURN NEW;
END;
$fn$;

-- One row per budget, period and threshold the first time settlement
-- reaches it; the source of the outbound.ai_budget.threshold webhook.
CREATE TABLE IF NOT EXISTS outbound_ai_budget_alerts (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    integration_id    uuid NOT NULL REFERENCES outbound_integrations (id) ON DELETE CASCADE,
    account_id        uuid NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    budget            text NOT NULL,
    app_id            uuid,
    period_start      date NOT NULL,
    threshold_percent smallint NOT NULL,
    limit_value       bigint NOT NULL,
    used_value        bigint NOT NULL,
    crossed_at        timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT outbound_ai_budget_alerts_budget_chk
        CHECK (budget IN ('daily_tokens', 'app_daily_tokens', 'daily_cost', 'monthly_cost')),
    CONSTRAINT outbound_ai_budget_alerts_app_chk CHECK ((budget = 'app_daily_tokens') = (app_id IS NOT NULL)),
    CONSTRAINT outbound_ai_budget_alerts_threshold_chk CHECK (threshold_percent IN (80, 100)),
    CONSTRAINT outbound_ai_budget_alerts_values_chk CHECK (limit_value >= 1 AND used_value >= 0)
);
CREATE UNIQUE INDEX IF NOT EXISTS outbound_ai_budget_alerts_once_idx
    ON outbound_ai_budget_alerts (integration_id, budget,
        COALESCE(app_id, '00000000-0000-0000-0000-000000000000'::uuid), period_start, threshold_percent);
CREATE INDEX IF NOT EXISTS outbound_ai_budget_alerts_recent_idx
    ON outbound_ai_budget_alerts (integration_id, crossed_at DESC);

-- Opt-in per-call metadata; never prompts, completions or headers.
CREATE TABLE IF NOT EXISTS outbound_ai_request_log (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    integration_id      uuid NOT NULL REFERENCES outbound_integrations (id) ON DELETE CASCADE,
    created_at          timestamptz NOT NULL DEFAULT now(),
    caller              text NOT NULL,
    route_id            uuid,
    model               text NOT NULL,
    status              smallint NOT NULL,
    outcome             text NOT NULL,
    input_tokens        bigint NOT NULL DEFAULT 0,
    cached_input_tokens bigint NOT NULL DEFAULT 0,
    cache_write_tokens  bigint NOT NULL DEFAULT 0,
    output_tokens       bigint NOT NULL DEFAULT 0,
    cost_millicents     bigint NOT NULL DEFAULT 0,
    duration_ms         integer NOT NULL,
    ttfb_ms             integer,
    attempts            smallint NOT NULL,
    CONSTRAINT outbound_ai_request_log_caller_chk CHECK (length(caller) BETWEEN 1 AND 64),
    CONSTRAINT outbound_ai_request_log_model_chk CHECK (length(model) BETWEEN 1 AND 128 AND model ~ '^[ -~]+$'),
    CONSTRAINT outbound_ai_request_log_status_chk CHECK (status BETWEEN 100 AND 599),
    CONSTRAINT outbound_ai_request_log_outcome_chk CHECK (outcome IN ('known', 'unknown', 'error')),
    CONSTRAINT outbound_ai_request_log_values_chk
        CHECK (input_tokens >= 0 AND cached_input_tokens >= 0 AND cache_write_tokens >= 0 AND output_tokens >= 0
               AND cost_millicents >= 0 AND duration_ms >= 0 AND (ttfb_ms IS NULL OR ttfb_ms >= 0)
               AND attempts BETWEEN 0 AND 100)
);
CREATE INDEX IF NOT EXISTS outbound_ai_request_log_recent_idx
    ON outbound_ai_request_log (integration_id, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS outbound_ai_request_log_prune_idx
    ON outbound_ai_request_log (created_at);
-- +goose StatementEnd

-- Preserve the webhook vocabulary installed by earlier producers.
-- +goose StatementBegin
DO $$
DECLARE definition text;
BEGIN
 SELECT pg_get_constraintdef(oid) INTO definition FROM pg_constraint
 WHERE conrelid='app_webhook_event_outbox'::regclass AND conname='app_webhook_event_outbox_event_chk';
 IF definition IS NULL THEN RAISE EXCEPTION 'missing webhook event constraint'; END IF;
 IF position('outbound.ai_budget.threshold' in definition) > 0 THEN RETURN; END IF;
 definition:=regexp_replace(definition,' NOT VALID$','');
 ALTER TABLE app_webhook_event_outbox DROP CONSTRAINT app_webhook_event_outbox_event_chk;
 EXECUTE format('ALTER TABLE app_webhook_event_outbox ADD CONSTRAINT app_webhook_event_outbox_event_chk CHECK ((%s) OR event IN (%L))',substring(definition from 8 for length(definition)-8),'outbound.ai_budget.threshold');
END $$;
-- +goose StatementEnd

-- +goose Down
-- Keep the webhook vocabulary so queued deliveries remain dispatchable.
-- +goose StatementBegin
DROP TABLE IF EXISTS outbound_ai_request_log;
DROP TABLE IF EXISTS outbound_ai_budget_alerts;
CREATE OR REPLACE FUNCTION purge_inactive_outbound_credential() RETURNS trigger LANGUAGE plpgsql AS $fn$
BEGIN
    IF OLD.credential_source = 'customer_sealed'
       AND (NEW.credential_source <> 'customer_sealed' OR NEW.provider_auth_mode <> 'managed') THEN
        DELETE FROM outbound_integration_credentials WHERE integration_id = NEW.id;
    END IF;
    RETURN NEW;
END;
$fn$;
DROP TABLE IF EXISTS outbound_integration_credential_keys;
ALTER TABLE outbound_ai_routes
    DROP CONSTRAINT IF EXISTS outbound_ai_routes_format_chk,
    DROP COLUMN IF EXISTS format;
ALTER TABLE outbound_app_binding_usage
    DROP CONSTRAINT IF EXISTS outbound_app_binding_usage_minute_chk,
    DROP COLUMN IF EXISTS minute_ai_token_count,
    DROP COLUMN IF EXISTS minute_request_count,
    DROP COLUMN IF EXISTS minute_window_start;
ALTER TABLE outbound_app_bindings
    DROP CONSTRAINT IF EXISTS outbound_app_bindings_ai_guardrails_chk,
    DROP COLUMN IF EXISTS ai_max_output_tokens,
    DROP COLUMN IF EXISTS ai_allowed_models,
    DROP COLUMN IF EXISTS ai_tokens_per_minute,
    DROP COLUMN IF EXISTS ai_requests_per_minute;
ALTER TABLE outbound_admission_leases
    DROP CONSTRAINT IF EXISTS outbound_admission_leases_ai_reservation_chk,
    DROP COLUMN IF EXISTS ai_reserved_cost_millicents,
    DROP COLUMN IF EXISTS ai_reserved_tokens,
    DROP COLUMN IF EXISTS binding_app_id;
UPDATE outbound_integrations SET ai_provider = NULL, ai_daily_token_limit = NULL,
       ai_daily_cost_limit_millicents = NULL, ai_monthly_cost_limit_millicents = NULL
 WHERE ai_provider IN ('gemini', 'azure_openai', 'bedrock', 'vertex');
ALTER TABLE outbound_integrations
    DROP CONSTRAINT IF EXISTS outbound_integrations_ai_request_log_chk,
    DROP CONSTRAINT IF EXISTS outbound_integrations_ai_provider_chk,
    DROP COLUMN IF EXISTS ai_request_log_enabled;
ALTER TABLE outbound_integrations ADD CONSTRAINT outbound_integrations_ai_provider_chk
    CHECK (ai_provider IS NULL OR ai_provider IN ('openai', 'anthropic'));
-- +goose StatementEnd
