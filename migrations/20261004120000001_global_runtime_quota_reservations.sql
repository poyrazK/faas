-- ADR-486: durable capacity accounting; limits are installed from pkg/api/limits.go.
-- +goose Up
-- +goose StatementBegin
CREATE TABLE global_runtime_quota_policies (
 policy_id text NOT NULL CHECK(policy_id ~ '^[0-9a-f]{64}$'),
 plan text NOT NULL CHECK(plan IN ('free','hobby','pro','scale')),
 max_concurrency integer NOT NULL CHECK(max_concurrency>0),
 max_peak_concurrency integer NOT NULL CHECK(max_peak_concurrency>=max_concurrency),
 PRIMARY KEY(policy_id,plan),
 UNIQUE(policy_id,plan,max_concurrency,max_peak_concurrency)
);
ALTER TABLE global_apps ADD COLUMN desired_runtime_quota_generation bigint NOT NULL DEFAULT 0 CHECK(desired_runtime_quota_generation BETWEEN 0 AND 9007199254740991);
CREATE TABLE global_runtime_quota_allocations (
 global_app_id uuid NOT NULL REFERENCES global_apps(id) ON DELETE CASCADE CHECK(global_app_id<>'00000000-0000-0000-0000-000000000000'),
 owner_id uuid NOT NULL REFERENCES global_directory_owners(id) ON DELETE CASCADE CHECK(owner_id<>'00000000-0000-0000-0000-000000000000'),
 actor_id uuid NOT NULL CHECK(actor_id<>'00000000-0000-0000-0000-000000000000'),
 allocation_id uuid NOT NULL CHECK(allocation_id<>'00000000-0000-0000-0000-000000000000'),
 generation bigint NOT NULL CHECK(generation BETWEEN 1 AND 9007199254740991),
 policy_id text NOT NULL CHECK(policy_id ~ '^[0-9a-f]{64}$'),
 plan text NOT NULL CHECK(plan IN ('free','hobby','pro','scale')),
 max_concurrency integer NOT NULL CHECK(max_concurrency>0),
 max_peak_concurrency integer NOT NULL CHECK(max_peak_concurrency>=max_concurrency),
 shares jsonb NOT NULL CHECK(jsonb_typeof(shares)='array' AND jsonb_array_length(shares)>0),
 rollout_region text NOT NULL CHECK(rollout_region ~ '^[a-z][a-z0-9-]+$'),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(created_at)),
 retirement_id uuid CHECK(retirement_id<>'00000000-0000-0000-0000-000000000000'),
 retired_at timestamptz CHECK(isfinite(retired_at)),
 CHECK((retirement_id IS NULL)=(retired_at IS NULL)),
 PRIMARY KEY(global_app_id,allocation_id), UNIQUE(global_app_id,generation),
 FOREIGN KEY(policy_id,plan,max_concurrency,max_peak_concurrency) REFERENCES global_runtime_quota_policies(policy_id,plan,max_concurrency,max_peak_concurrency)
);
CREATE TABLE global_runtime_quota_targets (
 global_app_id uuid NOT NULL CHECK(global_app_id<>'00000000-0000-0000-0000-000000000000'),
 allocation_id uuid NOT NULL CHECK(allocation_id<>'00000000-0000-0000-0000-000000000000'),
 region text NOT NULL CHECK(region ~ '^[a-z][a-z0-9-]+$'),
 concurrency integer NOT NULL CHECK(concurrency>0),
 rollout_concurrency integer NOT NULL CHECK(rollout_concurrency>=0),
 snapshot jsonb NOT NULL CHECK(jsonb_typeof(snapshot)='object' AND snapshot ?& ARRAY['CellID','Region','CellGeneration','RegionalAccountID','RegionalAppID','RegionalAppSlug','GlobalAppID','APIBase'] AND (snapshot->>'Region') IS NOT DISTINCT FROM region AND (snapshot->>'GlobalAppID') IS NOT DISTINCT FROM global_app_id::text),
 issued_at timestamptz CHECK(isfinite(issued_at)),
 released_at timestamptz CHECK(isfinite(released_at)),
 drain_confirmation jsonb CHECK(jsonb_typeof(drain_confirmation)='object'),
 PRIMARY KEY(global_app_id,allocation_id,region),
 FOREIGN KEY(global_app_id,allocation_id) REFERENCES global_runtime_quota_allocations(global_app_id,allocation_id) ON DELETE CASCADE
);

CREATE FUNCTION lock_global_runtime_quota_tenant(wanted_owner uuid) RETURNS text LANGUAGE plpgsql AS $$
DECLARE root global_directory_owners%ROWTYPE; current_plan text;
BEGIN
 PERFORM assert_global_directory_scope();
 SELECT * INTO root FROM global_directory_owners WHERE id=wanted_owner;
 IF NOT FOUND THEN RAISE EXCEPTION 'quota owner missing' USING ERRCODE='23503'; END IF;
 IF root.org_id IS NOT NULL THEN
  SELECT plan INTO current_plan FROM orgs WHERE id=root.org_id AND NOT personal_org AND status='active' AND NOT deleted_pending FOR SHARE;
 ELSE
  SELECT plan INTO current_plan FROM accounts WHERE id=root.account_id AND status='active' AND abuse_hold_at IS NULL AND email_verified_at IS NOT NULL FOR SHARE;
 END IF;
 IF NOT FOUND THEN RAISE EXCEPTION 'quota owner inactive' USING ERRCODE='23514',CONSTRAINT='global_directory_owner_inactive'; END IF;
 PERFORM 1 FROM global_directory_owners WHERE id=wanted_owner FOR UPDATE;
 IF NOT FOUND THEN RAISE EXCEPTION 'quota owner missing' USING ERRCODE='23503'; END IF;
 RETURN current_plan;
END;
$$;
CREATE FUNCTION lock_global_runtime_quota_owner(wanted_owner uuid,actor uuid) RETURNS text LANGUAGE plpgsql AS $$
DECLARE root global_directory_owners%ROWTYPE; current_plan text;
BEGIN
 SELECT * INTO root FROM global_directory_owners WHERE id=wanted_owner;
 IF NOT FOUND THEN RAISE EXCEPTION 'quota owner missing' USING ERRCODE='23503'; END IF;
 IF root.org_id IS NOT NULL THEN
  PERFORM lock_global_org_target_owner(wanted_owner,actor);
  SELECT plan INTO current_plan FROM orgs WHERE id=root.org_id;
 ELSE
  IF root.account_id IS DISTINCT FROM actor THEN RAISE EXCEPTION 'quota actor forbidden' USING ERRCODE='23514',CONSTRAINT='global_directory_owner_forbidden'; END IF;
  current_plan:=lock_global_runtime_quota_tenant(wanted_owner);
 END IF;
 RETURN current_plan;
END;
$$;

CREATE FUNCTION protect_global_runtime_quota_policy() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 PERFORM assert_global_directory_scope();
 IF TG_OP<>'INSERT' THEN RAISE EXCEPTION 'quota policy immutable' USING ERRCODE='23514',CONSTRAINT='global_runtime_quota_identity'; END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER global_runtime_quota_policy_identity BEFORE INSERT OR UPDATE OR DELETE ON global_runtime_quota_policies FOR EACH ROW EXECUTE FUNCTION protect_global_runtime_quota_policy();
CREATE TRIGGER global_runtime_quota_policy_truncate BEFORE TRUNCATE ON global_runtime_quota_policies FOR EACH STATEMENT EXECUTE FUNCTION protect_global_runtime_quota_policy();

CREATE FUNCTION protect_global_runtime_quota_allocation() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE current_plan text; app global_apps%ROWTYPE;
BEGIN
 PERFORM assert_global_directory_scope();
 IF TG_OP='TRUNCATE' THEN RAISE EXCEPTION 'quota allocations retained' USING ERRCODE='23514',CONSTRAINT='global_runtime_quota_identity'; END IF;
 IF TG_OP='DELETE' THEN
  IF EXISTS(SELECT 1 FROM global_apps WHERE id=OLD.global_app_id) OR EXISTS(SELECT 1 FROM global_runtime_quota_targets WHERE global_app_id=OLD.global_app_id AND allocation_id=OLD.allocation_id AND issued_at IS NOT NULL AND released_at IS NULL) THEN
   RAISE EXCEPTION 'quota allocation retained' USING ERRCODE='23514',CONSTRAINT='global_runtime_quota_identity';
  END IF;
  RETURN OLD;
 END IF;
 IF TG_OP='UPDATE' THEN
  IF (to_jsonb(NEW)-'retirement_id'-'retired_at') IS DISTINCT FROM (to_jsonb(OLD)-'retirement_id'-'retired_at') OR (OLD.retirement_id IS NOT NULL AND (NEW.retirement_id IS DISTINCT FROM OLD.retirement_id OR NEW.retired_at IS DISTINCT FROM OLD.retired_at)) THEN
   RAISE EXCEPTION 'quota allocation immutable' USING ERRCODE='23514',CONSTRAINT='global_runtime_quota_identity';
  END IF;
  RETURN NEW;
 END IF;
 current_plan:=lock_global_runtime_quota_owner(NEW.owner_id,NEW.actor_id);
 IF current_plan<>NEW.plan THEN RAISE EXCEPTION 'quota plan changed' USING ERRCODE='23514',CONSTRAINT='global_directory_owner_plan_changed'; END IF;
 SELECT * INTO app FROM global_apps WHERE id=NEW.global_app_id AND owner_id=NEW.owner_id FOR UPDATE;
 IF NOT FOUND OR NEW.generation<>app.desired_runtime_quota_generation+1 THEN RAISE EXCEPTION 'quota generation conflict' USING ERRCODE='23514',CONSTRAINT='global_runtime_quota_identity'; END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER global_runtime_quota_allocation_identity BEFORE INSERT OR UPDATE OR DELETE ON global_runtime_quota_allocations FOR EACH ROW EXECUTE FUNCTION protect_global_runtime_quota_allocation();
CREATE TRIGGER global_runtime_quota_allocation_truncate BEFORE TRUNCATE ON global_runtime_quota_allocations FOR EACH STATEMENT EXECUTE FUNCTION protect_global_runtime_quota_allocation();

CREATE FUNCTION populate_global_runtime_quota_targets() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE steady bigint; peak bigint; share_count bigint; target_count bigint; overlap_slots integer; share record;
BEGIN
 SELECT max_peak_concurrency-max_concurrency INTO overlap_slots FROM global_runtime_quota_policies WHERE policy_id=NEW.policy_id AND plan=NEW.plan;
 SELECT count(*),sum(concurrency) INTO share_count,steady FROM jsonb_to_recordset(NEW.shares) AS s(region text,concurrency integer);
 SELECT count(*) INTO target_count FROM global_app_targets WHERE global_app_id=NEW.global_app_id;
 IF share_count<>target_count OR share_count<>(SELECT count(DISTINCT region) FROM jsonb_to_recordset(NEW.shares) AS s(region text,concurrency integer)) OR NOT EXISTS(SELECT 1 FROM jsonb_to_recordset(NEW.shares) AS s(region text,concurrency integer) WHERE region=NEW.rollout_region) OR EXISTS(SELECT 1 FROM jsonb_to_recordset(NEW.shares) AS s(region text,concurrency integer) WHERE concurrency IS NULL OR concurrency<1 OR concurrency>NEW.max_concurrency OR region IS NULL OR NOT EXISTS(SELECT 1 FROM global_app_targets t WHERE t.global_app_id=NEW.global_app_id AND t.region=s.region)) THEN
  RAISE EXCEPTION 'quota shares must cover selected regions' USING ERRCODE='23514',CONSTRAINT='global_runtime_quota_identity';
 END IF;
 peak:=steady+overlap_slots;
 SELECT steady+coalesce(sum(concurrency),0),peak+coalesce(sum(concurrency+rollout_concurrency),0) INTO steady,peak FROM global_runtime_quota_targets WHERE global_app_id=NEW.global_app_id AND released_at IS NULL;
 IF steady>NEW.max_concurrency THEN RAISE EXCEPTION 'quota exhausted' USING ERRCODE='23514',CONSTRAINT='global_runtime_quota_limit',DETAIL='concurrency:'||NEW.max_concurrency||':'||steady; END IF;
 IF peak>NEW.max_peak_concurrency THEN RAISE EXCEPTION 'quota peak exhausted' USING ERRCODE='23514',CONSTRAINT='global_runtime_quota_limit',DETAIL='peak:'||NEW.max_peak_concurrency||':'||peak; END IF;
 PERFORM 1 FROM global_directory_cells c JOIN global_app_targets t ON t.cell_id=c.id WHERE t.global_app_id=NEW.global_app_id ORDER BY c.id FOR SHARE OF c;
 IF EXISTS(SELECT 1 FROM global_app_targets t JOIN global_directory_cells c ON c.id=t.cell_id WHERE t.global_app_id=NEW.global_app_id AND (NOT c.enabled OR c.generation<>t.cell_generation)) THEN
  RAISE EXCEPTION 'quota binding stale' USING ERRCODE='23514',CONSTRAINT='global_directory_stale_cell';
 END IF;
 FOR share IN SELECT * FROM jsonb_to_recordset(NEW.shares) AS s(region text,concurrency integer) LOOP
  INSERT INTO global_runtime_quota_targets(global_app_id,allocation_id,region,concurrency,rollout_concurrency,snapshot)
   SELECT NEW.global_app_id,NEW.allocation_id,t.region,share.concurrency,CASE WHEN t.region=NEW.rollout_region THEN overlap_slots ELSE 0 END,
   jsonb_build_object('CellID',t.cell_id,'Region',t.region,'CellGeneration',t.cell_generation,'RegionalAccountID',t.regional_account_id,'RegionalOrgID',coalesce(t.regional_org_id::text,''),'RegionalAppID',t.regional_app_id,'RegionalAppSlug',t.regional_app_slug,'GlobalAppID',t.global_app_id,'VerifiedAt',t.verified_at,'APIBase',c.api_base,'CellEnabled',true,'RegistryGeneration',c.generation)
   FROM global_app_targets t JOIN global_directory_cells c ON c.id=t.cell_id WHERE t.global_app_id=NEW.global_app_id AND t.region=share.region;
 END LOOP;
 UPDATE global_apps SET desired_runtime_quota_generation=NEW.generation WHERE id=NEW.global_app_id;
 RETURN NEW;
END;
$$;
CREATE TRIGGER global_runtime_quota_allocate AFTER INSERT ON global_runtime_quota_allocations FOR EACH ROW EXECUTE FUNCTION populate_global_runtime_quota_targets();

CREATE FUNCTION protect_global_runtime_quota_head() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  IF NEW.desired_runtime_quota_generation<>0 THEN RAISE EXCEPTION 'initial quota head must be zero' USING ERRCODE='23514',CONSTRAINT='global_runtime_quota_identity'; END IF;
  RETURN NEW;
 END IF;
 IF NEW.desired_runtime_quota_generation IS DISTINCT FROM OLD.desired_runtime_quota_generation AND (NEW.desired_runtime_quota_generation<>OLD.desired_runtime_quota_generation+1 OR NOT EXISTS(SELECT 1 FROM global_runtime_quota_allocations WHERE global_app_id=NEW.id AND generation=NEW.desired_runtime_quota_generation)) THEN
  RAISE EXCEPTION 'quota head immutable' USING ERRCODE='23514',CONSTRAINT='global_runtime_quota_identity';
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER global_runtime_quota_head BEFORE INSERT OR UPDATE ON global_apps FOR EACH ROW EXECUTE FUNCTION protect_global_runtime_quota_head();

CREATE FUNCTION protect_global_runtime_quota_target() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE allocation global_runtime_quota_allocations%ROWTYPE; requested integer; overlap_slots integer; proof jsonb;
BEGIN
 PERFORM assert_global_directory_scope();
 IF TG_OP='TRUNCATE' THEN RAISE EXCEPTION 'quota targets retained' USING ERRCODE='23514',CONSTRAINT='global_runtime_quota_identity'; END IF;
 IF TG_OP='DELETE' THEN
  IF EXISTS(SELECT 1 FROM global_runtime_quota_allocations WHERE global_app_id=OLD.global_app_id AND allocation_id=OLD.allocation_id) THEN RAISE EXCEPTION 'quota target retained' USING ERRCODE='23514',CONSTRAINT='global_runtime_quota_identity'; END IF;
  RETURN OLD;
 END IF;
 SELECT * INTO allocation FROM global_runtime_quota_allocations WHERE global_app_id=NEW.global_app_id AND allocation_id=NEW.allocation_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'quota allocation missing' USING ERRCODE='23503'; END IF;
 IF TG_OP='INSERT' THEN
  SELECT concurrency INTO requested FROM jsonb_to_recordset(allocation.shares) AS s(region text,concurrency integer) WHERE region=NEW.region;
  overlap_slots:=CASE WHEN allocation.rollout_region=NEW.region THEN allocation.max_peak_concurrency-allocation.max_concurrency ELSE 0 END;
  IF NOT FOUND OR NEW.concurrency IS DISTINCT FROM requested OR NEW.rollout_concurrency<>overlap_slots OR NEW.issued_at IS NOT NULL OR NEW.released_at IS NOT NULL OR NEW.drain_confirmation IS NOT NULL THEN RAISE EXCEPTION 'quota target shape conflicts' USING ERRCODE='23514',CONSTRAINT='global_runtime_quota_identity'; END IF;
  IF NOT EXISTS(SELECT 1 FROM global_app_targets t JOIN global_directory_cells c ON c.id=t.cell_id WHERE t.global_app_id=NEW.global_app_id AND t.region=NEW.region AND c.enabled AND c.generation=t.cell_generation AND NEW.snapshot->>'CellID'=t.cell_id::text AND (NEW.snapshot->>'CellGeneration')::bigint=t.cell_generation AND NEW.snapshot->>'RegionalAccountID'=t.regional_account_id::text AND coalesce(NEW.snapshot->>'RegionalOrgID','')=coalesce(t.regional_org_id::text,'') AND NEW.snapshot->>'RegionalAppID'=t.regional_app_id::text AND NEW.snapshot->>'RegionalAppSlug'=t.regional_app_slug AND NEW.snapshot->>'APIBase'=c.api_base) THEN RAISE EXCEPTION 'quota snapshot conflicts' USING ERRCODE='23514',CONSTRAINT='global_directory_stale_cell'; END IF;
  RETURN NEW;
 END IF;
 IF (to_jsonb(NEW)-'issued_at'-'released_at'-'drain_confirmation') IS DISTINCT FROM (to_jsonb(OLD)-'issued_at'-'released_at'-'drain_confirmation') OR (OLD.issued_at IS NOT NULL AND NEW.issued_at IS DISTINCT FROM OLD.issued_at) OR (OLD.released_at IS NOT NULL AND (NEW.released_at IS DISTINCT FROM OLD.released_at OR NEW.drain_confirmation IS DISTINCT FROM OLD.drain_confirmation)) THEN RAISE EXCEPTION 'quota target immutable' USING ERRCODE='23514',CONSTRAINT='global_runtime_quota_identity'; END IF;
 IF NEW.issued_at IS NOT NULL AND OLD.issued_at IS NULL AND allocation.retired_at IS NOT NULL THEN RAISE EXCEPTION 'quota retired' USING ERRCODE='23514',CONSTRAINT='global_runtime_quota_retired'; END IF;
 IF NEW.issued_at IS NOT NULL AND OLD.issued_at IS NULL THEN
  IF lock_global_runtime_quota_tenant(allocation.owner_id)<>allocation.plan THEN RAISE EXCEPTION 'quota plan changed' USING ERRCODE='23514',CONSTRAINT='global_directory_owner_plan_changed'; END IF;
  PERFORM 1 FROM global_apps WHERE id=NEW.global_app_id FOR UPDATE;
  PERFORM 1 FROM global_directory_cells WHERE id=(NEW.snapshot->>'CellID')::uuid FOR SHARE;
  IF NOT EXISTS(SELECT 1 FROM global_app_targets t JOIN global_directory_cells c ON c.id=t.cell_id WHERE t.global_app_id=NEW.global_app_id AND t.region=NEW.region AND c.enabled AND c.generation=t.cell_generation AND NEW.snapshot->>'CellID'=t.cell_id::text AND (NEW.snapshot->>'CellGeneration')::bigint=t.cell_generation AND NEW.snapshot->>'RegionalAccountID'=t.regional_account_id::text AND coalesce(NEW.snapshot->>'RegionalOrgID','')=coalesce(t.regional_org_id::text,'') AND NEW.snapshot->>'RegionalAppID'=t.regional_app_id::text AND NEW.snapshot->>'RegionalAppSlug'=t.regional_app_slug AND NEW.snapshot->>'APIBase'=c.api_base) THEN RAISE EXCEPTION 'quota snapshot conflicts' USING ERRCODE='23514',CONSTRAINT='global_directory_stale_cell'; END IF;
 END IF;
 IF NEW.drain_confirmation IS DISTINCT FROM OLD.drain_confirmation AND NEW.released_at IS NULL THEN RAISE EXCEPTION 'quota drain must release target' USING ERRCODE='23514',CONSTRAINT='global_runtime_quota_identity'; END IF;
 IF NEW.released_at IS NOT NULL AND OLD.released_at IS NULL THEN
  IF allocation.retired_at IS NULL THEN RAISE EXCEPTION 'quota not retired' USING ERRCODE='23514',CONSTRAINT='global_runtime_quota_identity'; END IF;
  IF NEW.issued_at IS NULL THEN
   IF NEW.drain_confirmation IS NOT NULL THEN RAISE EXCEPTION 'unissued quota has no drain receipt' USING ERRCODE='23514',CONSTRAINT='global_runtime_quota_identity'; END IF;
  ELSE
   proof:=NEW.drain_confirmation;
   IF proof IS NULL OR proof->>'AllocationID' IS DISTINCT FROM allocation.allocation_id::text OR proof->>'RetirementID' IS DISTINCT FROM allocation.retirement_id::text OR (proof->>'Generation')::bigint IS DISTINCT FROM allocation.generation OR proof->>'APIBase' IS DISTINCT FROM NEW.snapshot->>'APIBase' OR (proof->>'AdmissionFrozen')::boolean IS DISTINCT FROM true OR (proof->>'CountedInstances')::integer IS DISTINCT FROM 0 OR ((proof->'Binding')-'RegionalOrgID') IS DISTINCT FROM (NEW.snapshot-'GlobalAppID'-'VerifiedAt'-'APIBase'-'CellEnabled'-'RegistryGeneration'-'RegionalOrgID') OR coalesce(proof->'Binding'->>'RegionalOrgID','') IS DISTINCT FROM coalesce(NEW.snapshot->>'RegionalOrgID','') THEN RAISE EXCEPTION 'quota drain identity conflicts' USING ERRCODE='23514',CONSTRAINT='global_runtime_quota_ack_identity'; END IF;
  END IF;
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER global_runtime_quota_target_identity BEFORE INSERT OR UPDATE OR DELETE ON global_runtime_quota_targets FOR EACH ROW EXECUTE FUNCTION protect_global_runtime_quota_target();
CREATE TRIGGER global_runtime_quota_target_truncate BEFORE TRUNCATE ON global_runtime_quota_targets FOR EACH STATEMENT EXECUTE FUNCTION protect_global_runtime_quota_target();

CREATE FUNCTION reserve_global_runtime_quota(wanted_owner uuid,actor uuid,wanted_slug text,wanted_allocation uuid,expected_plan text,wanted_policy text,wanted_shares jsonb,wanted_rollout text) RETURNS uuid LANGUAGE plpgsql AS $$
DECLARE app global_apps%ROWTYPE; old global_runtime_quota_allocations%ROWTYPE; policy global_runtime_quota_policies%ROWTYPE; current_plan text;
BEGIN
 current_plan:=lock_global_runtime_quota_owner(wanted_owner,actor);
 IF current_plan<>expected_plan THEN RAISE EXCEPTION 'quota plan changed' USING ERRCODE='23514',CONSTRAINT='global_directory_owner_plan_changed'; END IF;
 SELECT * INTO app FROM global_apps WHERE owner_id=wanted_owner AND slug=wanted_slug FOR UPDATE;
 IF NOT FOUND THEN RETURN NULL; END IF;
 SELECT * INTO old FROM global_runtime_quota_allocations WHERE global_app_id=app.id AND allocation_id=wanted_allocation;
 IF FOUND THEN
  IF old.plan<>expected_plan OR old.policy_id<>wanted_policy OR old.shares<>wanted_shares OR old.rollout_region<>wanted_rollout THEN RAISE EXCEPTION 'quota request changed' USING ERRCODE='23514',CONSTRAINT='global_runtime_quota_identity'; END IF;
  RETURN app.id;
 END IF;
 SELECT * INTO policy FROM global_runtime_quota_policies WHERE policy_id=wanted_policy AND plan=expected_plan;
 IF NOT FOUND THEN RAISE EXCEPTION 'quota policy missing' USING ERRCODE='23503'; END IF;
 INSERT INTO global_runtime_quota_allocations(global_app_id,owner_id,actor_id,allocation_id,generation,policy_id,plan,max_concurrency,max_peak_concurrency,shares,rollout_region)
  VALUES(app.id,wanted_owner,actor,wanted_allocation,app.desired_runtime_quota_generation+1,policy.policy_id,policy.plan,policy.max_concurrency,policy.max_peak_concurrency,wanted_shares,wanted_rollout);
 RETURN app.id;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM global_runtime_quota_allocations) OR EXISTS(SELECT 1 FROM global_apps WHERE desired_runtime_quota_generation<>0) THEN RAISE EXCEPTION 'cannot downgrade retained global runtime quota allocations'; END IF;
END $$;
DROP FUNCTION reserve_global_runtime_quota(uuid,uuid,text,uuid,text,text,jsonb,text);
DROP TRIGGER global_runtime_quota_head ON global_apps;
DROP FUNCTION protect_global_runtime_quota_head();
DROP TABLE global_runtime_quota_targets;
DROP FUNCTION protect_global_runtime_quota_target();
DROP TABLE global_runtime_quota_allocations;
DROP FUNCTION populate_global_runtime_quota_targets();
DROP FUNCTION protect_global_runtime_quota_allocation();
DROP TABLE global_runtime_quota_policies;
DROP FUNCTION protect_global_runtime_quota_policy();
DROP FUNCTION lock_global_runtime_quota_owner(uuid,uuid);
DROP FUNCTION lock_global_runtime_quota_tenant(uuid);
ALTER TABLE global_apps DROP COLUMN desired_runtime_quota_generation;
-- +goose StatementEnd
