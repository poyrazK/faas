-- filename: 20261004094329557_financial_interval_plan_identity.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-530: align retained plan identity with the closed sampling interval.
-- Existing evidence and contracts remain immutable; pre-capture usage stays unpriced.
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
    -- Recorded activations apply to source intervals. In particular, the
    -- just-closed minute still uses its contract when the live plan changes.
    SELECT COALESCE((
      SELECT price.plan FROM financial_price_snapshots price
      WHERE price.account_id = NEW.account_id AND price.meter = 'compute'
        AND price.period_start <= NEW.minute AND price.period_end > NEW.minute
        AND price.effective_from <= NEW.minute
      ORDER BY price.effective_from DESC, price.recorded_at DESC, price.version
      LIMIT 1
    ), account.plan), jsonb_strip_nulls(jsonb_build_object(
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
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
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
-- +goose StatementEnd
