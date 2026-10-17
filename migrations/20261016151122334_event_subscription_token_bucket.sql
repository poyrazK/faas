-- +goose Up
-- +goose StatementBegin
ALTER TABLE event_subscription_delivery_controls
 ADD COLUMN IF NOT EXISTS rate_enabled boolean NOT NULL DEFAULT false,
 ADD COLUMN IF NOT EXISTS rate_interval_ms bigint NOT NULL DEFAULT 1000 CHECK(rate_interval_ms BETWEEN 10 AND 3600000),
 ADD COLUMN IF NOT EXISTS rate_burst bigint NOT NULL DEFAULT 1 CHECK(rate_burst BETWEEN 1 AND 100),
 ADD COLUMN IF NOT EXISTS rate_credit_ms bigint NOT NULL DEFAULT 1000 CHECK(rate_credit_ms BETWEEN 0 AND rate_interval_ms*rate_burst),
 ADD COLUMN IF NOT EXISTS rate_refilled_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(rate_refilled_at)),
 ADD COLUMN IF NOT EXISTS rate_revision bigint NOT NULL DEFAULT 0 CHECK(rate_revision>=0),
 ADD COLUMN IF NOT EXISTS rate_sequence bigint NOT NULL DEFAULT 0 CHECK(rate_sequence>=0);
CREATE TABLE IF NOT EXISTS event_subscription_rate_commands (
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 subscription_id uuid NOT NULL REFERENCES event_subscription_delivery_controls(subscription_id) ON DELETE CASCADE,
 command_id uuid NOT NULL,
 sequence bigint NOT NULL CHECK(sequence>0),
 request jsonb NOT NULL CHECK(jsonb_typeof(request)='object'),
 receipt jsonb NOT NULL CHECK(jsonb_typeof(receipt)='object'),
 recorded_at timestamptz NOT NULL CHECK(isfinite(recorded_at)),
 retain_until timestamptz NOT NULL CHECK(isfinite(retain_until) AND retain_until>recorded_at),
 PRIMARY KEY(subscription_id,command_id), UNIQUE(subscription_id,sequence)
);
CREATE INDEX IF NOT EXISTS event_subscription_rate_commands_expiry_idx ON event_subscription_rate_commands(retain_until);

CREATE OR REPLACE FUNCTION event_subscription_delivery_waiting_reason(account uuid, app uuid, subscription text, observed_at timestamp with time zone) RETURNS text
    LANGUAGE sql STABLE
    AS $$
SELECT coalesce((SELECT CASE WHEN c.paused THEN 'subscription_paused'
 WHEN b.state_data->>'state'='open' AND (b.state_data->>'cooldown_until')::timestamptz>observed_at THEN 'circuit_open'
 WHEN b.state_data->>'state'='half_open' AND ((coalesce(b.state_data->>'probe_token','')<>'' AND (b.state_data->>'probe_until')::timestamptz>observed_at) OR (b.state_data->>'next_probe_at')::timestamptz>observed_at) THEN 'circuit_probe_wait'
 WHEN b.state_data->>'state'='draining' AND (b.state_data->>'recovery_window')::timestamptz+interval '1 second'>observed_at
 AND (b.state_data->>'recovery_count')::integer>=least((b.policy->>'recovery_max_rate_per_second')::integer,
 power(2,least(7,greatest(0,floor(extract(epoch FROM observed_at-(b.state_data->>'changed_at')::timestamptz)/10))))::integer) THEN 'circuit_recovery_rate_limited'
 WHEN c.rate_enabled AND (c.rate_refilled_at>observed_at OR c.rate_credit_ms+greatest(0,floor(extract(epoch FROM observed_at-c.rate_refilled_at)*1000))<c.rate_interval_ms) THEN 'subscription_rate_limited'
 WHEN c.rate_per_second>0 AND c.window_count>=c.rate_per_second AND c.window_started_at+interval '1 second'>observed_at THEN 'subscription_rate_limited' ELSE '' END
 FROM event_subscription_delivery_controls c JOIN apps a ON a.id=c.app_id AND a.account_id=c.account_id AND a.status<>'deleted'
 LEFT JOIN event_subscription_circuit_breakers b ON b.subscription_id=c.subscription_id AND b.account_id=c.account_id AND b.app_id=c.app_id
 WHERE c.account_id=account AND c.app_id=app AND c.subscription_id::text=subscription),'');
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE event_subscription_rate_commands;
CREATE OR REPLACE FUNCTION event_subscription_delivery_waiting_reason(account uuid, app uuid, subscription text, observed_at timestamp with time zone) RETURNS text
    LANGUAGE sql STABLE
    AS $$
SELECT coalesce((SELECT CASE WHEN c.paused THEN 'subscription_paused'
 WHEN b.state_data->>'state'='open' AND (b.state_data->>'cooldown_until')::timestamptz>observed_at THEN 'circuit_open'
 WHEN b.state_data->>'state'='half_open' AND ((coalesce(b.state_data->>'probe_token','')<>'' AND (b.state_data->>'probe_until')::timestamptz>observed_at) OR (b.state_data->>'next_probe_at')::timestamptz>observed_at) THEN 'circuit_probe_wait'
 WHEN b.state_data->>'state'='draining' AND (b.state_data->>'recovery_window')::timestamptz+interval '1 second'>observed_at
 AND (b.state_data->>'recovery_count')::integer>=least((b.policy->>'recovery_max_rate_per_second')::integer,
 power(2,least(7,greatest(0,floor(extract(epoch FROM observed_at-(b.state_data->>'changed_at')::timestamptz)/10))))::integer) THEN 'circuit_recovery_rate_limited'
 WHEN c.rate_per_second>0 AND c.window_count>=c.rate_per_second AND c.window_started_at+interval '1 second'>observed_at THEN 'subscription_rate_limited' ELSE '' END
 FROM event_subscription_delivery_controls c JOIN apps a ON a.id=c.app_id AND a.account_id=c.account_id AND a.status<>'deleted'
 LEFT JOIN event_subscription_circuit_breakers b ON b.subscription_id=c.subscription_id AND b.account_id=c.account_id AND b.app_id=c.app_id
 WHERE c.account_id=account AND c.app_id=app AND c.subscription_id::text=subscription),'');
$$;
ALTER TABLE event_subscription_delivery_controls DROP COLUMN rate_enabled, DROP COLUMN rate_interval_ms, DROP COLUMN rate_burst, DROP COLUMN rate_credit_ms, DROP COLUMN rate_refilled_at, DROP COLUMN rate_revision, DROP COLUMN rate_sequence;
-- +goose StatementEnd
