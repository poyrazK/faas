-- filename: 20261006204529632_global_runtime_quota_delivery_outbox.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-647: original public authority survives response loss and collection.
CREATE TABLE global_runtime_quota_deliveries (
 id uuid PRIMARY KEY CHECK(id<>'00000000-0000-0000-0000-000000000000'),
 global_app_id uuid NOT NULL CHECK(global_app_id<>'00000000-0000-0000-0000-000000000000'),
 owner_id uuid NOT NULL CHECK(owner_id<>'00000000-0000-0000-0000-000000000000'),
 global_app_slug text NOT NULL CHECK(length(global_app_slug) BETWEEN 3 AND 40 AND global_app_slug ~ '^[a-z0-9][a-z0-9-]*[a-z0-9]$'),
 allocation_id uuid NOT NULL CHECK(allocation_id<>'00000000-0000-0000-0000-000000000000'),
 region text NOT NULL CHECK(length(region) BETWEEN 1 AND 32),
 purpose text NOT NULL CHECK(purpose IN ('grant','retirement')),
 consent_id uuid CHECK(consent_id<>'00000000-0000-0000-0000-000000000000'),
 issuer_id uuid NOT NULL CHECK(issuer_id<>'00000000-0000-0000-0000-000000000000'),
 grant_claims jsonb NOT NULL CHECK(jsonb_typeof(grant_claims)='object'),
 envelope jsonb NOT NULL,
 retirement_id uuid CHECK(retirement_id<>'00000000-0000-0000-0000-000000000000'),
 retired_at timestamptz CHECK(isfinite(retired_at)),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(created_at)),
 status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','leased','accepted','blocked')),
 attempts integer NOT NULL DEFAULT 0 CHECK(attempts>=0),
 next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(next_attempt_at)),
 claim_id uuid CHECK(claim_id<>'00000000-0000-0000-0000-000000000000'),
 claim_until timestamptz CHECK(isfinite(claim_until)),
 accepted_at timestamptz CHECK(isfinite(accepted_at)),
 receipt jsonb CHECK(jsonb_typeof(receipt)='object'),
 last_code text NOT NULL DEFAULT '' CHECK(last_code IN ('','uncertain','backend','consent','owner','binding','retired','trust')),
 UNIQUE(global_app_id,allocation_id,region,purpose),
 CHECK((purpose='grant')=(consent_id IS NOT NULL)),
 CHECK((purpose='retirement')=(retirement_id IS NOT NULL) AND (retirement_id IS NULL)=(retired_at IS NULL)),
 CHECK((status='leased')=(claim_id IS NOT NULL) AND (claim_id IS NULL)=(claim_until IS NULL)),
 CHECK((status='accepted')=(accepted_at IS NOT NULL) AND (accepted_at IS NULL)=(receipt IS NULL)),
 CHECK(status<>'accepted' OR last_code=''),
 CHECK(status<>'blocked' OR last_code IN ('consent','owner','binding','retired','trust')),
 CHECK((jsonb_typeof(envelope)='object' AND envelope ?& ARRAY['key_id','payload','signature']
  AND envelope-ARRAY['key_id','payload','signature']='{}'::jsonb
  AND jsonb_typeof(envelope->'key_id')='string' AND envelope->>'key_id' ~ '^[0-9a-f]{64}$'
  AND jsonb_typeof(envelope->'payload')='string' AND length(envelope->>'payload') BETWEEN 1 AND 5462 AND envelope->>'payload' ~ '^[A-Za-z0-9_-]+$'
  AND jsonb_typeof(envelope->'signature')='string' AND length(envelope->>'signature')=86 AND envelope->>'signature' ~ '^[A-Za-z0-9_-]+$') IS TRUE)
);
CREATE INDEX global_runtime_quota_deliveries_due ON global_runtime_quota_deliveries(owner_id,next_attempt_at,id) WHERE status IN ('pending','leased');
CREATE INDEX global_runtime_quota_deliveries_app ON global_runtime_quota_deliveries(global_app_id,status);
CREATE TABLE global_runtime_quota_delivery_owners (
 owner_id uuid PRIMARY KEY CHECK(owner_id<>'00000000-0000-0000-0000-000000000000'),
 last_claimed_at timestamptz NOT NULL DEFAULT '1970-01-01 00:00:00+00' CHECK(isfinite(last_claimed_at))
);

-- The original issued snapshot, never a mutable current regional binding.
CREATE FUNCTION global_runtime_quota_delivery_grant(app_id uuid,allocation uuid,wanted_region text,issuer uuid) RETURNS jsonb LANGUAGE sql STABLE AS $$
 SELECT jsonb_build_object('IssuerID',issuer::text,'GlobalAppID',a.global_app_id::text,'OwnerID',a.owner_id::text,
  'AllocationID',a.allocation_id::text,'Generation',a.generation,
  'Binding',jsonb_build_object('CellID',t.snapshot->'CellID','Region',t.snapshot->'Region','CellGeneration',t.snapshot->'CellGeneration',
   'RegionalAccountID',t.snapshot->'RegionalAccountID','RegionalOrgID',coalesce(t.snapshot->>'RegionalOrgID',''),
   'RegionalAppID',t.snapshot->'RegionalAppID','RegionalAppSlug',t.snapshot->'RegionalAppSlug','APIBase',t.snapshot->'APIBase'),
  'Policy',jsonb_build_object('id',a.policy_id,'plan',a.plan,'concurrency',a.max_concurrency,'peak_concurrency',a.max_peak_concurrency),
  'Concurrency',t.concurrency,'RolloutConcurrency',t.rollout_concurrency,
  'IssuedAtMicros',(extract(epoch FROM t.issued_at)*1000000)::bigint)
 FROM global_runtime_quota_allocations a JOIN global_runtime_quota_targets t USING(global_app_id,allocation_id)
 WHERE a.global_app_id=app_id AND a.allocation_id=allocation AND t.region=wanted_region AND t.issued_at IS NOT NULL;
$$;

CREATE FUNCTION check_global_quota_delivery_scope(j global_runtime_quota_deliveries) RETURNS void LANGUAGE plpgsql AS $$
DECLARE current_plan text; original jsonb; c global_runtime_consents%ROWTYPE;
BEGIN
 IF j.purpose='retirement' THEN RETURN; END IF;
 current_plan:=lock_global_runtime_quota_tenant(j.owner_id);
 PERFORM 1 FROM global_apps WHERE id=j.global_app_id AND owner_id=j.owner_id AND slug=j.global_app_slug FOR UPDATE;
 IF NOT FOUND THEN RAISE EXCEPTION 'quota delivery app unavailable' USING ERRCODE='23514',CONSTRAINT='global_quota_delivery_binding'; END IF;
 PERFORM 1 FROM global_runtime_quota_allocations q WHERE q.global_app_id=j.global_app_id AND q.allocation_id=j.allocation_id
  AND q.owner_id=j.owner_id AND q.retired_at IS NULL AND q.plan=current_plan FOR UPDATE;
 IF NOT FOUND THEN RAISE EXCEPTION 'quota delivery allocation changed' USING ERRCODE='23514',CONSTRAINT='global_quota_delivery_owner'; END IF;
 original:=global_runtime_quota_delivery_grant(j.global_app_id,j.allocation_id,j.region,j.issuer_id);
 IF original IS NULL OR original IS DISTINCT FROM j.grant_claims THEN RAISE EXCEPTION 'quota delivery original grant differs' USING ERRCODE='23514',CONSTRAINT='global_quota_delivery_binding'; END IF;
 SELECT * INTO c FROM global_runtime_consents WHERE id=j.consent_id AND owner_id=j.owner_id AND global_app_slug=j.global_app_slug;
 IF NOT FOUND OR c.status<>'active' OR c.global_app_id<>j.global_app_id OR c.issuer_id<>j.issuer_id OR c.binding IS DISTINCT FROM j.grant_claims->'Binding' THEN
  RAISE EXCEPTION 'quota delivery consent unavailable' USING ERRCODE='23514',CONSTRAINT='global_runtime_consent_unavailable';
 END IF;
 -- Includes selected/enabled exact binding, current tenant and expiry after waits.
 PERFORM check_global_runtime_consent_scope(c.owner_id,c.global_app_id,c.global_app_slug,c.issuer_id,c.binding,c.expires_at);
END; $$;

CREATE FUNCTION guard_global_runtime_quota_delivery() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE a global_runtime_quota_allocations%ROWTYPE; original jsonb; issued timestamptz;
BEGIN
 PERFORM assert_global_directory_scope();
 IF TG_OP IN ('DELETE','TRUNCATE') THEN RAISE EXCEPTION 'quota delivery identity retained' USING ERRCODE='23514',CONSTRAINT='global_quota_delivery_identity'; END IF;
 IF TG_OP='INSERT' THEN
  PERFORM pg_advisory_xact_lock(hashtextextended('gregale-global-runtime-consent:'||NEW.global_app_id::text,0));
  PERFORM 1 FROM global_runtime_quota_issuer WHERE issuer_id=NEW.issuer_id;
  IF NOT FOUND THEN RAISE EXCEPTION 'quota delivery issuer changed' USING ERRCODE='23514',CONSTRAINT='global_quota_delivery_identity'; END IF;
  SELECT q.* INTO a FROM global_runtime_quota_allocations q JOIN global_apps p ON p.id=q.global_app_id AND p.owner_id=q.owner_id
   WHERE q.global_app_id=NEW.global_app_id AND q.owner_id=NEW.owner_id AND q.allocation_id=NEW.allocation_id AND p.slug=NEW.global_app_slug FOR UPDATE OF q,p;
  IF NOT FOUND THEN RAISE EXCEPTION 'quota delivery allocation unavailable' USING ERRCODE='23514',CONSTRAINT='global_quota_delivery_identity'; END IF;
  original:=global_runtime_quota_delivery_grant(NEW.global_app_id,NEW.allocation_id,NEW.region,NEW.issuer_id);
  IF original IS NULL OR original IS DISTINCT FROM NEW.grant_claims OR NEW.grant_claims->'Binding'->>'RegionalOrgID'<>'' THEN
   RAISE EXCEPTION 'quota delivery original grant conflicts' USING ERRCODE='23514',CONSTRAINT='global_quota_delivery_identity';
  END IF;
  IF NEW.status<>'pending' OR NEW.attempts<>0 OR NEW.claim_id IS NOT NULL OR NEW.claim_until IS NOT NULL OR NEW.receipt IS NOT NULL OR NEW.accepted_at IS NOT NULL OR NEW.last_code<>'' THEN
   RAISE EXCEPTION 'quota delivery must start pending' USING ERRCODE='23514',CONSTRAINT='global_quota_delivery_identity';
  END IF;
  IF NEW.purpose='grant' THEN
   SELECT issued_at INTO issued FROM global_runtime_quota_targets WHERE global_app_id=NEW.global_app_id AND allocation_id=NEW.allocation_id AND region=NEW.region;
   IF issued<transaction_timestamp() THEN RAISE EXCEPTION 'legacy issued quota has no delivery identity' USING ERRCODE='23514',CONSTRAINT='global_quota_delivery_legacy'; END IF;
   PERFORM check_global_quota_delivery_scope(NEW);
  ELSIF NEW.retirement_id IS DISTINCT FROM a.retirement_id OR NEW.retired_at IS DISTINCT FROM a.retired_at OR a.retired_at IS NULL THEN
   RAISE EXCEPTION 'quota delivery retirement differs' USING ERRCODE='23514',CONSTRAINT='global_quota_delivery_identity';
  END IF;
  IF (SELECT count(*) FROM global_runtime_quota_deliveries WHERE global_app_id=NEW.global_app_id AND status IN ('pending','leased'))>=1024 THEN
   RAISE EXCEPTION 'quota delivery bound reached' USING ERRCODE='23514',CONSTRAINT='global_quota_delivery_limit';
  END IF;
  NEW.created_at:=clock_timestamp();NEW.next_attempt_at:=NEW.created_at;
  INSERT INTO global_runtime_quota_delivery_owners(owner_id) VALUES(NEW.owner_id) ON CONFLICT DO NOTHING;
 ELSE
  IF (to_jsonb(NEW)-ARRAY['status','attempts','next_attempt_at','claim_id','claim_until','accepted_at','receipt','last_code'])
   IS DISTINCT FROM (to_jsonb(OLD)-ARRAY['status','attempts','next_attempt_at','claim_id','claim_until','accepted_at','receipt','last_code']) THEN
   RAISE EXCEPTION 'quota delivery original identity immutable' USING ERRCODE='23514',CONSTRAINT='global_quota_delivery_identity';
  END IF;
  IF OLD.status='accepted' AND NEW IS DISTINCT FROM OLD THEN RAISE EXCEPTION 'accepted quota delivery retained' USING ERRCODE='23514',CONSTRAINT='global_quota_delivery_identity'; END IF;
  IF NEW.status='leased' THEN
   IF OLD.status NOT IN ('pending','leased') OR (OLD.status='leased' AND OLD.claim_until>clock_timestamp())
    OR NEW.claim_id IS NULL OR NEW.claim_id IS NOT DISTINCT FROM OLD.claim_id OR NEW.claim_until<=clock_timestamp() OR NEW.claim_until>clock_timestamp()+interval '120 seconds'
    OR NEW.attempts<>least(OLD.attempts::bigint+1,2147483647) THEN
    RAISE EXCEPTION 'quota delivery claim conflicts' USING ERRCODE='23514',CONSTRAINT='global_quota_delivery_claim';
   END IF;
  ELSIF OLD.status='leased' THEN
   -- Evaluated after the row lock, including a finish that waited past expiry.
   IF OLD.claim_until<=clock_timestamp() OR NEW.attempts<>OLD.attempts THEN RAISE EXCEPTION 'quota delivery claim expired' USING ERRCODE='23514',CONSTRAINT='global_quota_delivery_claim'; END IF;
  ELSIF NEW.status='pending' AND OLD.status IN ('pending','blocked') THEN
   IF OLD.status='blocked' THEN
    PERFORM pg_advisory_xact_lock(hashtextextended('gregale-global-runtime-consent:'||NEW.global_app_id::text,0));
    IF (SELECT count(*) FROM global_runtime_quota_deliveries WHERE global_app_id=NEW.global_app_id AND status IN ('pending','leased'))>=1024 THEN
     RAISE EXCEPTION 'quota delivery bound reached' USING ERRCODE='23514',CONSTRAINT='global_quota_delivery_limit';
    END IF;
   END IF;
   PERFORM check_global_quota_delivery_scope(NEW);
   IF NEW.attempts<>OLD.attempts THEN RAISE EXCEPTION 'quota delivery attempts immutable' USING ERRCODE='23514',CONSTRAINT='global_quota_delivery_identity'; END IF;
  ELSIF NEW IS DISTINCT FROM OLD THEN RAISE EXCEPTION 'quota delivery transition conflicts' USING ERRCODE='23514',CONSTRAINT='global_quota_delivery_identity'; END IF;
 END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER global_runtime_quota_delivery_guard BEFORE INSERT OR UPDATE OR DELETE ON global_runtime_quota_deliveries FOR EACH ROW EXECUTE FUNCTION guard_global_runtime_quota_delivery();
CREATE TRIGGER global_runtime_quota_delivery_truncate BEFORE TRUNCATE ON global_runtime_quota_deliveries FOR EACH STATEMENT EXECUTE FUNCTION guard_global_runtime_quota_delivery();

CREATE FUNCTION guard_global_quota_delivery_owner() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 PERFORM assert_global_directory_scope();
 IF TG_OP IN ('DELETE','TRUNCATE') OR TG_OP='UPDATE' AND (NEW.owner_id IS DISTINCT FROM OLD.owner_id OR NEW.last_claimed_at<OLD.last_claimed_at) THEN
  RAISE EXCEPTION 'quota delivery fairness cursor retained' USING ERRCODE='23514',CONSTRAINT='global_quota_delivery_identity';
 END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER global_quota_delivery_owner_guard BEFORE INSERT OR UPDATE OR DELETE ON global_runtime_quota_delivery_owners FOR EACH ROW EXECUTE FUNCTION guard_global_quota_delivery_owner();
CREATE TRIGGER global_quota_delivery_owner_truncate BEFORE TRUNCATE ON global_runtime_quota_delivery_owners FOR EACH STATEMENT EXECUTE FUNCTION guard_global_quota_delivery_owner();

CREATE FUNCTION claim_global_runtime_quota_deliveries(batch integer,lease_seconds integer) RETURNS SETOF global_runtime_quota_deliveries LANGUAGE plpgsql AS $$
DECLARE o record; j global_runtime_quota_deliveries%ROWTYPE; stamp timestamptz;
BEGIN
 PERFORM assert_global_directory_scope();
 IF batch<1 OR batch>2 OR lease_seconds<1 OR lease_seconds>120 THEN RAISE EXCEPTION 'quota delivery claim bounds' USING ERRCODE='23514',CONSTRAINT='global_quota_delivery_claim'; END IF;
 FOR o IN SELECT owner_id FROM global_runtime_quota_delivery_owners p WHERE EXISTS(
  SELECT 1 FROM global_runtime_quota_deliveries d WHERE d.owner_id=p.owner_id AND d.next_attempt_at<=clock_timestamp()
   AND (d.status='pending' OR d.status='leased' AND d.claim_until<=clock_timestamp()))
  ORDER BY last_claimed_at,owner_id LIMIT batch FOR UPDATE SKIP LOCKED LOOP
  SELECT * INTO j FROM global_runtime_quota_deliveries WHERE owner_id=o.owner_id AND next_attempt_at<=clock_timestamp()
   AND (status='pending' OR status='leased' AND claim_until<=clock_timestamp()) ORDER BY next_attempt_at,id LIMIT 1 FOR UPDATE SKIP LOCKED;
  IF FOUND THEN
   stamp:=clock_timestamp();
   UPDATE global_runtime_quota_deliveries SET status='leased',claim_id=gen_random_uuid(),claim_until=stamp+make_interval(secs=>lease_seconds),attempts=least(attempts::bigint+1,2147483647)::integer WHERE id=j.id RETURNING * INTO j;
   UPDATE global_runtime_quota_delivery_owners SET last_claimed_at=stamp WHERE owner_id=o.owner_id;
   RETURN NEXT j;
  END IF;
 END LOOP;
END; $$;

CREATE FUNCTION assert_global_quota_delivery_claim(wanted_id uuid,wanted_claim uuid) RETURNS void LANGUAGE plpgsql AS $$
DECLARE j global_runtime_quota_deliveries%ROWTYPE;
BEGIN
 SELECT * INTO j FROM global_runtime_quota_deliveries WHERE id=wanted_id FOR UPDATE;
 IF NOT FOUND OR j.status<>'leased' OR j.claim_id IS DISTINCT FROM wanted_claim OR j.claim_until<=clock_timestamp() THEN
  RAISE EXCEPTION 'quota delivery claim expired or replaced' USING ERRCODE='23514',CONSTRAINT='global_quota_delivery_claim';
 END IF;
END; $$;

CREATE FUNCTION finish_global_quota_delivery(wanted_id uuid,wanted_claim uuid,outcome text,code text,ack jsonb,retry_seconds integer) RETURNS void LANGUAGE plpgsql AS $$
BEGIN
 PERFORM assert_global_directory_scope();
 PERFORM assert_global_quota_delivery_claim(wanted_id,wanted_claim);
 IF outcome NOT IN ('pending','accepted','blocked') OR retry_seconds<30 OR retry_seconds>300 THEN RAISE EXCEPTION 'quota delivery outcome invalid' USING ERRCODE='23514',CONSTRAINT='global_quota_delivery_identity'; END IF;
 IF (outcome='accepted' AND (code<>'' OR ack IS NULL)) OR (outcome='pending' AND (code NOT IN ('uncertain','backend') OR ack IS NOT NULL))
  OR (outcome='blocked' AND (code NOT IN ('consent','owner','binding','retired','trust') OR ack IS NOT NULL)) THEN
  RAISE EXCEPTION 'quota delivery outcome details invalid' USING ERRCODE='23514',CONSTRAINT='global_quota_delivery_identity';
 END IF;
 UPDATE global_runtime_quota_deliveries SET status=outcome,last_code=code,receipt=ack,
  accepted_at=CASE WHEN outcome='accepted' THEN clock_timestamp() ELSE NULL END,
  next_attempt_at=CASE WHEN outcome='pending' THEN clock_timestamp()+make_interval(secs=>retry_seconds) ELSE next_attempt_at END,
  claim_id=NULL,claim_until=NULL WHERE id=wanted_id;
END; $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 LOCK TABLE global_runtime_quota_deliveries IN ACCESS EXCLUSIVE MODE;
 IF EXISTS(SELECT 1 FROM global_runtime_quota_deliveries) THEN RAISE EXCEPTION 'cannot downgrade retained runtime quota deliveries'; END IF;
END; $$;
DROP FUNCTION finish_global_quota_delivery(uuid,uuid,text,text,jsonb,integer);
DROP FUNCTION assert_global_quota_delivery_claim(uuid,uuid);
DROP FUNCTION claim_global_runtime_quota_deliveries(integer,integer);
DROP TRIGGER global_quota_delivery_owner_guard ON global_runtime_quota_delivery_owners;
DROP TRIGGER global_quota_delivery_owner_truncate ON global_runtime_quota_delivery_owners;
DROP FUNCTION guard_global_quota_delivery_owner();
DROP TRIGGER global_runtime_quota_delivery_guard ON global_runtime_quota_deliveries;
DROP TRIGGER global_runtime_quota_delivery_truncate ON global_runtime_quota_deliveries;
DROP FUNCTION guard_global_runtime_quota_delivery();
DROP FUNCTION check_global_quota_delivery_scope(global_runtime_quota_deliveries);
DROP TABLE global_runtime_quota_delivery_owners;
DROP TABLE global_runtime_quota_deliveries;
DROP FUNCTION global_runtime_quota_delivery_grant(uuid,uuid,text,uuid);
-- +goose StatementEnd
