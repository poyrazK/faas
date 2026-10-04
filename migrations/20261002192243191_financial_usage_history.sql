-- filename: 20261002192243191_financial_usage_history.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-530. Retained deltas are committed atomically with canonical usage.
-- No historical prices or pre-migration usage are guessed/backfilled.
CREATE TABLE IF NOT EXISTS financial_usage_evidence (
  id bigserial PRIMARY KEY,
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  instance_id uuid NOT NULL,
  source_id text NOT NULL CHECK (length(source_id) BETWEEN 1 AND 512),
  meter text NOT NULL CHECK (meter IN ('compute', 'egress')),
  unit text NOT NULL CHECK (unit IN ('mb_seconds', 'interface_bytes')),
  quantity bigint NOT NULL CHECK (quantity > 0),
  source_start timestamptz NOT NULL,
  source_end timestamptz NOT NULL CHECK (source_end > source_start),
  plan text NOT NULL CHECK (plan IN ('free', 'hobby', 'pro', 'scale')),
  attribution jsonb NOT NULL CHECK (jsonb_typeof(attribution) = 'object'),
  observed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  price_version text CHECK (price_version IS NULL OR length(price_version) BETWEEN 1 AND 128),
  UNIQUE (account_id, source_id),
  CHECK ((meter = 'compute' AND unit = 'mb_seconds') OR (meter = 'egress' AND unit = 'interface_bytes'))
);
CREATE INDEX IF NOT EXISTS financial_usage_account_period_idx ON financial_usage_evidence(account_id, source_start, id);
CREATE INDEX IF NOT EXISTS financial_usage_instance_minute_idx ON financial_usage_evidence(instance_id, source_start, id);

CREATE TABLE IF NOT EXISTS financial_price_snapshots (
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  period_start timestamptz NOT NULL,
  period_end timestamptz NOT NULL CHECK (period_end > period_start),
  meter text NOT NULL CHECK (length(meter) BETWEEN 1 AND 64),
  version text NOT NULL CHECK (length(version) BETWEEN 1 AND 128),
  plan text NOT NULL CHECK (plan IN ('free', 'hobby', 'pro', 'scale')),
  effective_from timestamptz NOT NULL CHECK (effective_from >= period_start AND effective_from < period_end),
  delivery_mode text NOT NULL CHECK (delivery_mode IN ('live', 'shadow', 'off')),
  price jsonb NOT NULL CHECK (jsonb_typeof(price) = 'object'),
  recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (account_id, period_start, meter, version)
);

CREATE TABLE IF NOT EXISTS financial_evidence_coverage (
  singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
  retained_from timestamptz NOT NULL
);
INSERT INTO financial_evidence_coverage(singleton, retained_from) VALUES (true, clock_timestamp()) ON CONFLICT DO NOTHING;

CREATE TABLE IF NOT EXISTS financial_sampling_windows (
  minute timestamptz PRIMARY KEY CHECK (minute = date_trunc('minute', minute)),
  compute_complete boolean NOT NULL,
  egress_complete boolean NOT NULL,
  observed_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE OR REPLACE FUNCTION retain_financial_usage_evidence() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  previous_compute bigint := 0;
  previous_egress bigint := 0;
  captured_plan text;
  captured_attribution jsonb;
  delta bigint;
  meter_name text;
  unit_name text;
  cumulative bigint;
  captured_price text;
BEGIN
  IF TG_OP = 'UPDATE' THEN
    previous_compute := OLD.mb_seconds;
    previous_egress := OLD.net_tx_bytes;
  END IF;
  IF NEW.mb_seconds = previous_compute AND NEW.net_tx_bytes = previous_egress THEN RETURN NEW; END IF;
  IF NEW.mb_seconds < previous_compute OR NEW.net_tx_bytes < previous_egress THEN
    RAISE EXCEPTION 'financial usage corrections require retained adjustment lineage';
  END IF;
  -- Allocate evidence IDs in account commit order. Without this transaction
  -- lock, a lower sequence could commit after a paged reader fixed its head.
  PERFORM pg_advisory_xact_lock(hashtextextended('financial-evidence:' || NEW.account_id::text, 0));

  -- Preserve the first observation's identity even if the live workload is
  -- renamed, moved or deleted before later network observations arrive.
  SELECT plan, attribution INTO captured_plan, captured_attribution
    FROM financial_usage_evidence WHERE account_id = NEW.account_id
      AND instance_id = NEW.instance_id AND source_start = NEW.minute
    ORDER BY id LIMIT 1;
  IF NOT FOUND THEN
    SELECT account.plan, jsonb_strip_nulls(jsonb_build_object(
      'app_id', NEW.app_id::text, 'job_id', NEW.job_id::text,
      'project_id', app.project_id::text, 'deployment_id', instance.deployment_id::text,
      'environment_id', environment.id::text, 'name', COALESCE(app.slug, job.name)))
    INTO captured_plan, captured_attribution
    FROM accounts account
    LEFT JOIN apps app ON app.id = NEW.app_id AND app.account_id = account.id
    LEFT JOIN jobs job ON job.id = NEW.job_id AND job.account_id = account.id
    LEFT JOIN instances instance ON instance.id = NEW.instance_id AND instance.app_id = app.id
    LEFT JOIN deployments deployment ON deployment.id = instance.deployment_id AND deployment.app_id = app.id
    LEFT JOIN project_environments environment ON environment.account_id = account.id
      AND environment.project_id = app.project_id AND environment.slug = deployment.scope
    WHERE account.id = NEW.account_id;
    -- Preserve canonical usage behavior for orphan fixtures and late samples
    -- after account deletion without recreating erased financial history.
    IF NOT FOUND THEN RETURN NEW; END IF;
  END IF;
  FOR meter_name, unit_name, delta, cumulative IN
    SELECT 'compute', 'mb_seconds', NEW.mb_seconds - previous_compute, NEW.mb_seconds
    UNION ALL SELECT 'egress', 'interface_bytes', NEW.net_tx_bytes - previous_egress, NEW.net_tx_bytes
  LOOP
    IF delta > 0 THEN
      SELECT version INTO captured_price FROM financial_price_snapshots
        WHERE account_id = NEW.account_id AND meter = meter_name AND plan = captured_plan
          AND period_start <= NEW.minute AND period_end > NEW.minute AND effective_from <= NEW.minute
        ORDER BY effective_from DESC, recorded_at DESC, version LIMIT 1;
      INSERT INTO financial_usage_evidence(account_id, instance_id, source_id, meter, unit, quantity,
        source_start, source_end, plan, attribution, price_version)
      VALUES (NEW.account_id, NEW.instance_id,
        'usage:' || NEW.instance_id::text || ':' || extract(epoch FROM NEW.minute)::text || ':' || meter_name || ':' || cumulative::text,
        meter_name, unit_name, delta, NEW.minute, NEW.minute + interval '1 minute', captured_plan, captured_attribution, captured_price);
    END IF;
  END LOOP;
  RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS financial_usage_retention ON usage_minutes;
CREATE TRIGGER financial_usage_retention AFTER INSERT OR UPDATE ON usage_minutes
FOR EACH ROW EXECUTE FUNCTION retain_financial_usage_evidence();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS financial_usage_retention ON usage_minutes;
DROP FUNCTION IF EXISTS retain_financial_usage_evidence();
DROP TABLE IF EXISTS financial_price_snapshots;
DROP TABLE IF EXISTS financial_usage_evidence;
DROP TABLE IF EXISTS financial_evidence_coverage;
DROP TABLE IF EXISTS financial_sampling_windows;
-- +goose StatementEnd
