-- filename: 20261010085631135_api_consumer_usage_alerts.sql

-- ADR-849: consumer usage alerts. A plan lists percentages of its monthly
-- unit limit; the gateway's admission transaction records each threshold a
-- consumer crosses once per UTC month and enqueues a consumer.usage_threshold
-- webhook through the existing app webhook outbox.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE api_consumer_plans
  ADD COLUMN IF NOT EXISTS alert_thresholds_percent integer[] NOT NULL DEFAULT '{}';
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'api_consumer_plans_alert_thresholds_chk') THEN
    ALTER TABLE api_consumer_plans
      ADD CONSTRAINT api_consumer_plans_alert_thresholds_chk CHECK (
        cardinality(alert_thresholds_percent) <= 5
        AND 1 <= ALL (alert_thresholds_percent)
        AND 100 >= ALL (alert_thresholds_percent)
        AND (cardinality(alert_thresholds_percent) = 0 OR max_units_per_month > 0));
  END IF;
END $$;

CREATE TABLE IF NOT EXISTS api_consumer_usage_alerts (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id        uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id            uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    consumer_id       uuid NOT NULL REFERENCES api_consumers(id) ON DELETE CASCADE,
    plan_id           uuid NOT NULL,
    month_start       timestamptz NOT NULL,
    threshold_percent integer NOT NULL,
    limit_units       bigint NOT NULL,
    used_units        bigint NOT NULL,
    crossed_at        timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT api_consumer_usage_alerts_plan_fkey
      FOREIGN KEY (app_id, plan_id) REFERENCES api_consumer_plans(app_id, id) ON DELETE CASCADE,
    CONSTRAINT api_consumer_usage_alerts_month_chk
      CHECK (month_start = date_trunc('month', month_start AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'),
    CONSTRAINT api_consumer_usage_alerts_threshold_chk CHECK (threshold_percent BETWEEN 1 AND 100),
    CONSTRAINT api_consumer_usage_alerts_units_chk CHECK (limit_units > 0 AND used_units >= 0),
    CONSTRAINT api_consumer_usage_alerts_once_uniq UNIQUE (consumer_id, month_start, threshold_percent)
);
CREATE INDEX IF NOT EXISTS api_consumer_usage_alerts_lookup_idx
  ON api_consumer_usage_alerts (account_id, app_id, consumer_id, crossed_at DESC);

COMMENT ON COLUMN api_consumer_plans.alert_thresholds_percent IS
  'Percentages of max_units_per_month at which a consumer.usage_threshold webhook fires, once per consumer per UTC month.';
COMMENT ON TABLE api_consumer_usage_alerts IS
  'One row per consumer, UTC month, and plan alert threshold crossed at gateway admission; source of the consumer.usage_threshold webhook.';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS api_consumer_usage_alerts;
ALTER TABLE api_consumer_plans
  DROP CONSTRAINT IF EXISTS api_consumer_plans_alert_thresholds_chk;
ALTER TABLE api_consumer_plans
  DROP COLUMN IF EXISTS alert_thresholds_percent;
-- +goose StatementEnd
