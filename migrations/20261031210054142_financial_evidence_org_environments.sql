-- filename: 20261031210054142_financial_evidence_org_environments.sql
-- migration-compat: expand
--
-- Replaces two function bodies; no table or column changes. The previous
-- release runs unchanged: it never writes organization budgets, and the
-- evidence it records only gains an environment_id it previously missed.

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- ADR-20261031211500: an organization that owns its resources is billed for
-- its apps, so their usage evidence is recorded under the organization. The
-- environment join compared the environment's account (the member who created
-- it) with that organization and never matched, so organization evidence
-- carried no environment_id and environment budgets read nothing. An
-- environment is unique per project and slug; the account check keeps a
-- personal environment from attributing another account's evidence. Existing
-- evidence is immutable and keeps its recorded attribution.
CREATE OR REPLACE FUNCTION public.retain_financial_usage_evidence() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
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
    FROM billing_resource_accounts account
    LEFT JOIN apps app ON app.id = NEW.app_id AND (app.account_id = account.id OR app.org_id = account.id)
    LEFT JOIN jobs job ON job.id = NEW.job_id AND job.account_id = account.id
    LEFT JOIN instances instance ON instance.id = NEW.instance_id AND instance.app_id = app.id
    LEFT JOIN deployments deployment ON deployment.id = instance.deployment_id AND deployment.app_id = app.id
    LEFT JOIN project_environments environment ON environment.project_id = app.project_id
      AND environment.slug = deployment.scope
      AND (environment.account_id = account.id OR app.org_id = account.id)
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

-- A preview stop belongs to the subject that pays for the preview: its
-- organization when that owns its resources, else its account. The policy's
-- environment was checked against that subject when it was written, and an
-- environment's account is only the member who created it.
CREATE OR REPLACE FUNCTION public.financial_preview_command_held(target_app uuid, target_deployment uuid, observed_at timestamp with time zone) RETURNS boolean
    LANGUAGE sql STABLE
    AS $$
 SELECT EXISTS (
  SELECT 1 FROM apps a
  LEFT JOIN orgs payer_org ON payer_org.id=a.org_id AND payer_org.resource_ownership_enabled AND NOT payer_org.personal_org
  JOIN financial_budget_stops s ON s.account_id = COALESCE(payer_org.id, a.account_id)
  JOIN financial_budget_policies p ON p.account_id=s.account_id AND p.id=s.policy_id AND p.revision=s.revision
  LEFT JOIN deployments d ON d.id=target_deployment AND d.app_id=a.id
  LEFT JOIN deployment_runtime_environment_owners owner ON owner.deployment_id=d.id
  LEFT JOIN project_environments environment ON environment.id=nullif(p.spec->'scope'->>'id','')::uuid
  WHERE a.id=target_app AND a.preview_of_slug IS NOT NULL AND a.preview_of_slug!=''
   AND p.deleted_at IS NULL AND p.spec->>'enabled'='true'
   AND s.requested_at<=observed_at AND (s.resume_rule='manual' OR s.period_end>observed_at)
   AND CASE p.spec->'scope'->>'kind'
    WHEN 'account' THEN true
    WHEN 'app' THEN (p.spec->'scope'->>'id')::uuid=a.id
    WHEN 'project' THEN (p.spec->'scope'->>'id')::uuid=a.project_id
    WHEN 'environment' THEN environment.project_id=a.project_id
      AND (owner.environment_id=environment.id OR owner.deployment_id IS NULL AND
        (environment.slug=CASE WHEN coalesce(nullif(d.scope,''),'default')='default' THEN 'production' ELSE d.scope END))
    ELSE false END
 )
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
CREATE OR REPLACE FUNCTION public.retain_financial_usage_evidence() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
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
  PERFORM pg_advisory_xact_lock(hashtextextended('financial-evidence:' || NEW.account_id::text, 0));
  SELECT plan, attribution INTO captured_plan, captured_attribution
    FROM financial_usage_evidence WHERE account_id = NEW.account_id
      AND instance_id = NEW.instance_id AND source_start = NEW.minute
    ORDER BY id LIMIT 1;
  IF NOT FOUND THEN
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
    FROM billing_resource_accounts account
    LEFT JOIN apps app ON app.id = NEW.app_id AND (app.account_id = account.id OR app.org_id = account.id)
    LEFT JOIN jobs job ON job.id = NEW.job_id AND job.account_id = account.id
    LEFT JOIN instances instance ON instance.id = NEW.instance_id AND instance.app_id = app.id
    LEFT JOIN deployments deployment ON deployment.id = instance.deployment_id AND deployment.app_id = app.id
    LEFT JOIN project_environments environment ON environment.account_id = account.id
      AND environment.project_id = app.project_id AND environment.slug = deployment.scope
    WHERE account.id = NEW.account_id;
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
CREATE OR REPLACE FUNCTION public.financial_preview_command_held(target_app uuid, target_deployment uuid, observed_at timestamp with time zone) RETURNS boolean
    LANGUAGE sql STABLE
    AS $$
 SELECT EXISTS (
  SELECT 1 FROM apps a
  JOIN financial_budget_stops s ON s.account_id = a.account_id
  JOIN financial_budget_policies p ON p.account_id=s.account_id AND p.id=s.policy_id AND p.revision=s.revision
  LEFT JOIN deployments d ON d.id=target_deployment AND d.app_id=a.id
  LEFT JOIN deployment_runtime_environment_owners owner ON owner.deployment_id=d.id
  LEFT JOIN project_environments environment ON environment.id=nullif(p.spec->'scope'->>'id','')::uuid
  WHERE a.id=target_app AND a.preview_of_slug IS NOT NULL AND a.preview_of_slug!=''
   AND p.deleted_at IS NULL AND p.spec->>'enabled'='true'
   AND s.requested_at<=observed_at AND (s.resume_rule='manual' OR s.period_end>observed_at)
   AND CASE p.spec->'scope'->>'kind'
    WHEN 'account' THEN true
    WHEN 'app' THEN (p.spec->'scope'->>'id')::uuid=a.id
    WHEN 'project' THEN (p.spec->'scope'->>'id')::uuid=a.project_id
    WHEN 'environment' THEN environment.account_id=a.account_id AND environment.project_id=a.project_id
      AND (owner.environment_id=environment.id OR owner.deployment_id IS NULL AND
        (environment.slug=CASE WHEN coalesce(nullif(d.scope,''),'default')='default' THEN 'production' ELSE d.scope END))
    ELSE false END
 )
$$;
-- +goose StatementEnd
