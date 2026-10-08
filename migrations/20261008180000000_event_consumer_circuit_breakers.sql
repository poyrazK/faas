-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION valid_event_circuit_policy(p jsonb) RETURNS boolean LANGUAGE sql IMMUTABLE AS $$
 SELECT CASE WHEN p IS NULL OR jsonb_typeof(p)<>'object' OR NOT(p ?& ARRAY['failure_threshold_pct','min_samples','window_seconds','cooldown_seconds','probe_successes','recovery_max_rate_per_second','recovery_seconds']) THEN false
 WHEN EXISTS(SELECT 1 FROM jsonb_each(p) e WHERE e.key IN ('failure_threshold_pct','min_samples','window_seconds','cooldown_seconds','probe_successes','recovery_max_rate_per_second','recovery_seconds') AND jsonb_typeof(e.value)<>'number') THEN false
 ELSE (p->>'failure_threshold_pct')::numeric>0 AND (p->>'failure_threshold_pct')::numeric<=100
 AND (p->>'min_samples')::numeric BETWEEN 1 AND 10000 AND (p->>'min_samples')::numeric%1=0
 AND (p->>'window_seconds')::numeric BETWEEN 1 AND 3600 AND (p->>'window_seconds')::numeric%1=0
 AND (p->>'cooldown_seconds')::numeric BETWEEN 1 AND 3600 AND (p->>'cooldown_seconds')::numeric%1=0
 AND (p->>'probe_successes')::numeric BETWEEN 1 AND 20 AND (p->>'probe_successes')::numeric%1=0
 AND (p->>'recovery_max_rate_per_second')::numeric BETWEEN 1 AND 100 AND (p->>'recovery_max_rate_per_second')::numeric%1=0
 AND (p->>'recovery_seconds')::numeric BETWEEN 1 AND 3600 AND (p->>'recovery_seconds')::numeric%1=0 END;
$$;
CREATE TABLE event_subscription_circuit_breakers (
 subscription_id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 policy jsonb NOT NULL CHECK(valid_event_circuit_policy(policy)),
 state_data jsonb NOT NULL CHECK(jsonb_typeof(state_data)='object' AND state_data ? 'state' AND state_data->>'state' IS NOT NULL AND state_data->>'state' IN ('closed','open','half_open','draining')),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX event_subscription_circuit_breakers_app_idx ON event_subscription_circuit_breakers(app_id);

CREATE OR REPLACE FUNCTION event_subscription_delivery_waiting_reason(account uuid, app uuid, subscription text, observed_at timestamptz)
RETURNS text LANGUAGE sql STABLE AS $$
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
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION event_subscription_delivery_waiting_reason(account uuid, app uuid, subscription text, observed_at timestamptz)
RETURNS text LANGUAGE sql STABLE AS $$
SELECT coalesce((SELECT CASE WHEN c.paused THEN 'subscription_paused'
 WHEN c.rate_per_second>0 AND c.window_count>=c.rate_per_second AND c.window_started_at+interval '1 second'>observed_at
 THEN 'subscription_rate_limited' ELSE '' END
 FROM event_subscription_delivery_controls c JOIN apps a ON a.id=c.app_id AND a.account_id=c.account_id AND a.status<>'deleted' WHERE c.account_id=account AND c.app_id=app AND c.subscription_id::text=subscription),'');
$$;
DROP TABLE event_subscription_circuit_breakers;
DROP FUNCTION valid_event_circuit_policy(jsonb);
-- +goose StatementEnd
