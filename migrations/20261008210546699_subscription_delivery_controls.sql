-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS event_subscription_delivery_controls (
 subscription_id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 paused boolean NOT NULL DEFAULT false,
 rate_per_second integer NOT NULL DEFAULT 0 CHECK (rate_per_second BETWEEN 0 AND 100),
 window_started_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 window_count integer NOT NULL DEFAULT 0 CHECK (window_count >= 0),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX IF NOT EXISTS event_subscription_delivery_controls_app_idx ON event_subscription_delivery_controls(app_id);
CREATE OR REPLACE FUNCTION event_subscription_delivery_waiting_reason(account uuid, app uuid, subscription text, observed_at timestamptz)
RETURNS text LANGUAGE sql STABLE AS $$
SELECT coalesce((SELECT CASE WHEN c.paused THEN 'subscription_paused'
 WHEN c.rate_per_second>0 AND c.window_count>=c.rate_per_second AND c.window_started_at+interval '1 second'>observed_at
 THEN 'subscription_rate_limited' ELSE '' END
 FROM event_subscription_delivery_controls c JOIN apps a ON a.id=c.app_id AND a.account_id=c.account_id AND a.status<>'deleted' WHERE c.account_id=account AND c.app_id=app AND c.subscription_id::text=subscription),'');
$$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION event_subscription_delivery_waiting_reason(uuid,uuid,text,timestamptz);
DROP TABLE event_subscription_delivery_controls;
