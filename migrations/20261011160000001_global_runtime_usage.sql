-- +goose Up
-- +goose StatementBegin
-- ADR-798: retained original issuance is the billing authority. Do not join
-- current targets, current registry generations, or current account plans.
CREATE OR REPLACE VIEW global_runtime_billing_issued AS
SELECT owner.account_id, app.slug AS app_slug, allocation.global_app_id,
       allocation.allocation_id, target.snapshot->>'CellID' AS cell_id,
       target.issued_at, target.released_at,
       jsonb_build_object(
         'IssuerID', issuer.issuer_id::text, 'GlobalAppID', allocation.global_app_id::text,
         'OwnerID', allocation.owner_id::text, 'AllocationID', allocation.allocation_id::text,
         'Generation', allocation.generation,
         'Binding', jsonb_build_object('CellID',target.snapshot->>'CellID','Region',target.snapshot->>'Region',
           'CellGeneration',target.snapshot->'CellGeneration','RegionalAccountID',target.snapshot->>'RegionalAccountID',
           'RegionalOrgID',COALESCE(target.snapshot->>'RegionalOrgID',''),'RegionalAppID',target.snapshot->>'RegionalAppID',
           'RegionalAppSlug',target.snapshot->>'RegionalAppSlug','APIBase',target.snapshot->>'APIBase'),
         'Policy', jsonb_build_object('id',allocation.policy_id,'plan',allocation.plan,
           'concurrency',allocation.max_concurrency,'peak_concurrency',allocation.max_peak_concurrency),
         'Concurrency',target.concurrency,'RolloutConcurrency',target.rollout_concurrency,
         'IssuedAtMicros',(extract(epoch FROM target.issued_at)*1000000)::bigint
       ) AS grant_claims
FROM global_runtime_quota_allocations allocation
JOIN global_apps app ON app.id=allocation.global_app_id AND app.owner_id=allocation.owner_id
JOIN global_directory_owners owner ON owner.id=allocation.owner_id
JOIN global_runtime_quota_targets target USING(global_app_id,allocation_id)
LEFT JOIN global_runtime_quota_issuer issuer ON true
WHERE owner.account_id IS NOT NULL AND owner.org_id IS NULL AND target.issued_at IS NOT NULL;

CREATE TABLE IF NOT EXISTS global_runtime_usage_minutes (
  global_app_id uuid NOT NULL CHECK(global_app_id <> '00000000-0000-0000-0000-000000000000'),
  allocation_id uuid NOT NULL CHECK(allocation_id <> '00000000-0000-0000-0000-000000000000'),
  cell_id uuid NOT NULL CHECK(cell_id <> '00000000-0000-0000-0000-000000000000'),
  minute timestamptz NOT NULL CHECK(isfinite(minute) AND minute=date_trunc('minute',minute) AND minute > '1970-01-01Z'),
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE CHECK(account_id <> '00000000-0000-0000-0000-000000000000'),
  app_slug text NOT NULL CHECK(length(app_slug) BETWEEN 3 AND 40 AND app_slug ~ '^[a-z0-9]([a-z0-9-]*[a-z0-9])?$'),
  billing_instance_id uuid NOT NULL CHECK(billing_instance_id <> '00000000-0000-0000-0000-000000000000'),
  usage_claims jsonb NOT NULL CHECK(jsonb_typeof(usage_claims)='object' AND octet_length(usage_claims::text)<=4096),
  envelope jsonb NOT NULL CHECK(jsonb_typeof(envelope)='object' AND octet_length(envelope::text)<=8192),
  imported_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(imported_at)),
  PRIMARY KEY(global_app_id,allocation_id,cell_id,minute),
  UNIQUE(billing_instance_id,minute)
);
DROP TRIGGER IF EXISTS protect_global_runtime_usage ON global_runtime_usage_minutes;
CREATE TRIGGER protect_global_runtime_usage BEFORE UPDATE OR DELETE ON global_runtime_usage_minutes
FOR EACH ROW EXECUTE FUNCTION protect_financial_history();
DROP TRIGGER IF EXISTS protect_global_runtime_usage_truncate ON global_runtime_usage_minutes;
CREATE TRIGGER protect_global_runtime_usage_truncate BEFORE TRUNCATE ON global_runtime_usage_minutes
FOR EACH STATEMENT EXECUTE FUNCTION refuse_regional_runtime_quota_truncate();

-- The canonical usage insert runs the existing financial retention trigger.
-- Preserve the original allocation's plan and global app attribution, even
-- after plan changes or regional app collection. Price lookup stays integer.
CREATE OR REPLACE FUNCTION capture_global_runtime_financial_identity() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE receipt global_runtime_usage_minutes%ROWTYPE;
BEGIN
  IF NEW.corrects_source_id IS NOT NULL THEN RETURN NEW; END IF;
  SELECT * INTO receipt FROM global_runtime_usage_minutes
    WHERE billing_instance_id=NEW.instance_id AND minute=NEW.source_start;
  IF NOT FOUND THEN RETURN NEW; END IF;
  IF NEW.account_id<>receipt.account_id OR NEW.meter<>'compute' OR NEW.quantity<>(receipt.usage_claims->>'MBSeconds')::bigint THEN
    RAISE EXCEPTION 'global runtime financial source conflicts' USING ERRCODE='23514';
  END IF;
  NEW.plan := receipt.usage_claims->'Grant'->'Policy'->>'plan';
  NEW.attribution := jsonb_build_object('app_id',receipt.global_app_id::text,'name',receipt.app_slug);
  SELECT version INTO NEW.price_version FROM financial_price_snapshots
    WHERE account_id=NEW.account_id AND meter=NEW.meter AND plan=NEW.plan
      AND period_start<=NEW.source_start AND period_end>NEW.source_start AND effective_from<=NEW.source_start
    ORDER BY effective_from DESC,recorded_at DESC,version LIMIT 1;
  RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS aa_global_runtime_financial_identity ON financial_usage_evidence;
CREATE TRIGGER aa_global_runtime_financial_identity BEFORE INSERT ON financial_usage_evidence
FOR EACH ROW EXECUTE FUNCTION capture_global_runtime_financial_identity();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
  IF EXISTS(SELECT 1 FROM global_runtime_usage_minutes) THEN
    RAISE EXCEPTION 'cannot downgrade retained global runtime billing evidence';
  END IF;
END $$;
DROP TRIGGER IF EXISTS aa_global_runtime_financial_identity ON financial_usage_evidence;
DROP FUNCTION IF EXISTS capture_global_runtime_financial_identity();
DROP TABLE IF EXISTS global_runtime_usage_minutes;
DROP VIEW IF EXISTS global_runtime_billing_issued;
-- +goose StatementEnd
