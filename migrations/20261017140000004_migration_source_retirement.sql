-- filename: 20261017140000004_migration_source_retirement.sql
-- +goose Up

CREATE TABLE migration_sources (
 lease_token text PRIMARY KEY CHECK(lease_token<>'' AND octet_length(lease_token)<=128),
 instance_id uuid NOT NULL,
 source_node_id uuid NOT NULL,
 source_wake_id uuid NOT NULL,
 native_generation uuid NOT NULL,
 physical_binding text NOT NULL CHECK(physical_binding ~ '^[0-9a-f]{64}$'),
 ram_mb integer NOT NULL CHECK(ram_mb>0),
 cpu_millicores integer NOT NULL CHECK(cpu_millicores>0),
 vcpu integer NOT NULL CHECK(vcpu>0),
 phase text NOT NULL DEFAULT 'bound' CHECK(phase IN ('bound','retiring','retired','resumed')),
 proof text NOT NULL DEFAULT '' CHECK(octet_length(proof)<=1024),
 active_lease_token text GENERATED ALWAYS AS (CASE WHEN phase IN ('bound','retiring') THEN lease_token ELSE NULL END) STORED REFERENCES migration_leases(lease_token),
 CHECK((phase IN ('retired','resumed'))=(proof<>''))
);
CREATE UNIQUE INDEX migration_sources_one_pending ON migration_sources(instance_id,source_node_id) WHERE phase IN ('bound','retiring');
CREATE INDEX migration_sources_recovery ON migration_sources(source_node_id,lease_token) WHERE phase IN ('bound','retiring');
CREATE INDEX migration_sources_resumed_wake ON migration_sources(instance_id,source_node_id,source_wake_id) WHERE phase='resumed';
CREATE INDEX migration_leases_source_recovery ON migration_leases(source_node_id,lease_token);

-- +goose StatementBegin
CREATE FUNCTION guard_migration_source() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE i instances%ROWTYPE; l migration_leases%ROWTYPE; n compute_nodes%ROWTYPE;
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'migration source receipts must be retained' USING ERRCODE='23514',CONSTRAINT='migration_source_current'; END IF;
 IF TG_OP='INSERT' THEN
  SELECT * INTO n FROM compute_nodes WHERE id=NEW.source_node_id FOR SHARE;
  IF NOT FOUND OR n.lifecycle IN ('maintenance','retired') THEN
   RAISE EXCEPTION 'migration source node is closed' USING ERRCODE='23514',CONSTRAINT='migration_source_current';
  END IF;
  PERFORM pg_advisory_xact_lock(1852793957,hashtext(NEW.source_node_id::text));
  SELECT * INTO i FROM instances WHERE id=NEW.instance_id FOR UPDATE;
  IF NOT FOUND OR i.node_id<>NEW.source_node_id OR i.wake_id<>NEW.source_wake_id
   OR i.state NOT IN ('waking','cold_booting','running','draining','snapshotting','migrating','warm','frozen') THEN
   RAISE EXCEPTION 'migration source wake changed' USING ERRCODE='23514',CONSTRAINT='migration_source_current';
  END IF;
  SELECT * INTO l FROM migration_leases WHERE lease_token=NEW.lease_token FOR UPDATE;
  IF NOT FOUND OR l.instance_id<>NEW.instance_id OR l.source_node_id<>NEW.source_node_id::text OR l.resume_fenced OR NEW.phase<>'bound' OR NEW.proof<>''
   OR NEW.ram_mb<greatest(i.capacity_ram_mb,i.ram_mb+(SELECT overhead_mb FROM service_capacity_policy WHERE singleton))
   OR NEW.cpu_millicores<i.capacity_cpu_millicores OR NEW.vcpu<i.capacity_vcpu THEN
   RAISE EXCEPTION 'migration source lease changed' USING ERRCODE='23514',CONSTRAINT='migration_source_current';
  END IF;
  RETURN NEW;
 END IF;
 IF (to_jsonb(NEW)-ARRAY['phase','proof','active_lease_token']) IS DISTINCT FROM (to_jsonb(OLD)-ARRAY['phase','proof','active_lease_token']) THEN
  RAISE EXCEPTION 'migration source identity is immutable' USING ERRCODE='23514',CONSTRAINT='migration_source_current';
 END IF;
 IF NEW.phase=OLD.phase THEN
  IF NEW.proof<>OLD.proof THEN RAISE EXCEPTION 'migration source receipt changed' USING ERRCODE='23514',CONSTRAINT='migration_source_current'; END IF;
  RETURN NEW;
 END IF;
 IF NOT ((OLD.phase='bound' AND NEW.phase IN ('retiring','resumed','retired')) OR (OLD.phase='retiring' AND NEW.phase='retired')) THEN
  RAISE EXCEPTION 'invalid migration source transition' USING ERRCODE='23514',CONSTRAINT='migration_source_current';
 END IF;
 IF OLD.phase='bound' THEN
  SELECT * INTO l FROM migration_leases WHERE lease_token=NEW.lease_token FOR UPDATE;
  IF NEW.phase='resumed' AND (NOT l.resume_fenced OR EXISTS(SELECT 1 FROM migration_admissions WHERE lease_token=NEW.lease_token AND phase<>'cleaned')) THEN
   RAISE EXCEPTION 'migration source resume is not fenced' USING ERRCODE='23514',CONSTRAINT='migration_source_current';
  END IF;
  IF NEW.phase='resumed' AND NOT EXISTS(SELECT 1 FROM instances WHERE id=NEW.instance_id AND node_id=NEW.source_node_id AND wake_id=NEW.source_wake_id AND state IN ('running','draining')) THEN
   RAISE EXCEPTION 'migration source has no serving owner to resume' USING ERRCODE='23514',CONSTRAINT='migration_source_current';
  END IF;
  IF NEW.phase='retired' AND NOT l.resume_fenced THEN
   RAISE EXCEPTION 'migration source retirement is not fenced' USING ERRCODE='23514',CONSTRAINT='migration_source_current';
  END IF;
  IF NEW.phase='retiring' THEN
   IF EXISTS(SELECT 1 FROM migration_admissions WHERE lease_token=NEW.lease_token AND phase NOT IN ('cleaned','committed'))
    OR EXISTS(SELECT 1 FROM instances WHERE id=NEW.instance_id AND node_id=NEW.source_node_id AND wake_id=NEW.source_wake_id AND state IN ('waking','cold_booting','running','draining','snapshotting','migrating','warm','frozen')) THEN
    RAISE EXCEPTION 'migration source still owns its wake' USING ERRCODE='23514',CONSTRAINT='migration_source_current';
   END IF;
  END IF;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER migration_source_guard BEFORE INSERT OR UPDATE OR DELETE ON migration_sources FOR EACH ROW EXECUTE FUNCTION guard_migration_source();
CREATE TRIGGER service_capacity_before BEFORE INSERT OR UPDATE OR DELETE ON migration_sources FOR EACH STATEMENT EXECUTE FUNCTION service_capacity_before_write();
CREATE TRIGGER service_capacity_after AFTER INSERT OR UPDATE OR DELETE ON migration_sources FOR EACH STATEMENT EXECUTE FUNCTION service_capacity_after_write();

-- Ownership commit has already consumed its incoming receipt. A suppressed or
-- failed source transition must roll back that entire ownership transaction.
-- +goose StatementBegin
CREATE FUNCTION commit_migration_source_retirement() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE changed bigint;
BEGIN
 IF NEW.node_id IS DISTINCT FROM OLD.node_id AND EXISTS(SELECT 1 FROM migration_sources WHERE instance_id=OLD.id AND source_node_id=OLD.node_id AND source_wake_id=OLD.wake_id AND phase='bound') THEN
  UPDATE migration_sources SET phase='retiring' WHERE instance_id=OLD.id AND source_node_id=OLD.node_id AND source_wake_id=OLD.wake_id AND lease_token=NEW.lease_token AND phase='bound';
  GET DIAGNOSTICS changed = ROW_COUNT;
  IF changed<>1 THEN RAISE EXCEPTION 'migration source retirement was not recorded' USING ERRCODE='23514',CONSTRAINT='migration_source_current'; END IF;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER instance_source_retirement_commit AFTER UPDATE OF node_id ON instances FOR EACH ROW EXECUTE FUNCTION commit_migration_source_retirement();

-- +goose StatementBegin
CREATE FUNCTION guard_node_migration_sources() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (TG_OP='DELETE' OR NEW.lifecycle IN ('maintenance','retired')) AND (EXISTS(SELECT 1 FROM migration_sources WHERE source_node_id=OLD.id AND phase IN ('bound','retiring')) OR EXISTS(SELECT 1 FROM migration_leases WHERE source_node_id=OLD.id::text)) THEN
  RAISE EXCEPTION 'node has unresolved source migrations' USING ERRCODE='23514',CONSTRAINT='migration_source_current';
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER node_migration_source_guard BEFORE UPDATE OF lifecycle OR DELETE ON compute_nodes FOR EACH ROW EXECUTE FUNCTION guard_node_migration_sources();

-- A pending source is counted once while its original live row still represents
-- it. After movement it remains ordinary physical usage, independent of routing.
-- +goose StatementBegin
CREATE FUNCTION migration_source_node_usage(target uuid,legacy boolean DEFAULT false) RETURNS TABLE(ram bigint,cpu bigint,vcpu bigint) LANGUAGE sql AS $$
 SELECT coalesce(sum(CASE WHEN i.id IS NULL THEN r.ram_mb ELSE greatest(0,r.ram_mb-greatest(i.ram_mb+p.overhead_mb,CASE WHEN NOT legacy OR owned.present THEN i.capacity_ram_mb ELSE 0 END)) END),0)::bigint,
  coalesce(sum(CASE WHEN i.id IS NULL THEN r.cpu_millicores ELSE greatest(0,r.cpu_millicores-greatest(coalesce(nullif(a.cpu_millicores,0),p.startup_cpu),CASE WHEN NOT legacy OR owned.present THEN i.capacity_cpu_millicores ELSE CASE WHEN i.startup_cpu_boost_until>now() THEN p.startup_cpu ELSE 0 END END)) END),0)::bigint,
  coalesce(sum(CASE WHEN i.id IS NULL THEN r.vcpu ELSE greatest(0,r.vcpu-i.capacity_vcpu) END),0)::bigint
 FROM migration_sources r CROSS JOIN service_capacity_policy p
 LEFT JOIN instances i ON r.phase='bound' AND i.id=r.instance_id AND i.node_id=r.source_node_id AND i.wake_id=r.source_wake_id
  AND (i.state IN ('waking','cold_booting','running','draining','warm') OR NOT legacy AND i.state IN ('snapshotting','migrating','frozen'))
 LEFT JOIN apps a ON a.id=i.app_id
 LEFT JOIN LATERAL (SELECT EXISTS(SELECT 1 FROM migration_admissions d WHERE d.phase='committed' AND d.instance_id=i.id AND d.destination_node_id=i.node_id AND d.destination_wake_id=i.wake_id) OR EXISTS(SELECT 1 FROM migration_sources s WHERE s.phase='resumed' AND s.instance_id=i.id AND s.source_node_id=i.node_id AND s.source_wake_id=i.wake_id) AS present) owned ON true
 WHERE p.singleton AND r.source_node_id=target AND r.phase IN ('bound','retiring');
$$;
-- +goose StatementEnd

-- Reservations share node-completion serialization even before physical binding.
-- +goose StatementBegin
CREATE FUNCTION guard_source_migration_lease_node() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE n compute_nodes%ROWTYPE;
BEGIN
 IF TG_OP='UPDATE' AND EXISTS(SELECT 1 FROM migration_sources WHERE lease_token=OLD.lease_token) AND (
  (NEW.lease_token,NEW.instance_id,NEW.source_node_id) IS DISTINCT FROM (OLD.lease_token,OLD.instance_id,OLD.source_node_id)
  OR NEW.created_at IS DISTINCT FROM OLD.created_at OR NOT OLD.pending AND NEW.pending
  OR OLD.mem_storage_key<>'' AND NEW.mem_storage_key<>OLD.mem_storage_key
  OR OLD.vmstate_storage_key<>'' AND NEW.vmstate_storage_key<>OLD.vmstate_storage_key
 ) THEN RAISE EXCEPTION 'source lease identity and cleanup handles must be retained' USING ERRCODE='23514',CONSTRAINT='migration_source_current'; END IF;
 IF NEW.source_node_id<>'' THEN
  SELECT * INTO n FROM compute_nodes WHERE id::text=NEW.source_node_id FOR SHARE;
  IF NOT FOUND OR n.lifecycle IN ('maintenance','retired') THEN
   RAISE EXCEPTION 'migration source node is closed' USING ERRCODE='23514',CONSTRAINT='migration_source_current';
  END IF;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER source_migration_lease_node_guard BEFORE INSERT OR UPDATE OF instance_id,source_node_id,lease_token,mem_storage_key,vmstate_storage_key ON migration_leases FOR EACH ROW EXECUTE FUNCTION guard_source_migration_lease_node();
CREATE TRIGGER service_capacity_before BEFORE INSERT OR UPDATE OR DELETE ON migration_leases FOR EACH STATEMENT EXECUTE FUNCTION service_capacity_before_write();
CREATE TRIGGER service_capacity_after AFTER INSERT OR UPDATE OR DELETE ON migration_leases FOR EACH STATEMENT EXECUTE FUNCTION service_capacity_after_write();

-- The definitions extended below are restored verbatim by Down.

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_migration_admission() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE n compute_nodes%ROWTYPE; source_instance instances%ROWTYPE; used record;
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'migration admission receipts must be retained' USING ERRCODE='23514',CONSTRAINT='migration_admission_current'; END IF;
 IF TG_OP='INSERT' THEN
  IF NEW.phase<>'reserved' OR NEW.adoption_receipt<>''::bytea OR NEW.cleanup_proof<>'' OR NEW.expires_at<=clock_timestamp() THEN
   RAISE EXCEPTION 'invalid initial migration admission' USING ERRCODE='23514',CONSTRAINT='migration_admission_current';
  END IF;
  SELECT * INTO n FROM compute_nodes WHERE id=NEW.destination_node_id FOR SHARE;
  IF NOT FOUND OR n.lifecycle NOT IN ('active','recovering') OR n.lifecycle_generation<>NEW.destination_generation THEN
   RAISE EXCEPTION 'stale migration destination' USING ERRCODE='23514',CONSTRAINT='instance_node_admission_current';
  END IF;
  PERFORM pg_advisory_xact_lock(1852793957,hashtext(NEW.destination_node_id::text));
  SELECT * INTO source_instance FROM instances WHERE id=NEW.instance_id FOR UPDATE;
  IF NOT FOUND OR source_instance.node_id<>NEW.source_node_id OR source_instance.wake_id<>NEW.source_wake_id OR source_instance.state<>'migrating' OR source_instance.lease_token IS DISTINCT FROM NEW.lease_token
   OR NEW.ram_mb<greatest(source_instance.ram_mb+(SELECT overhead_mb FROM service_capacity_policy WHERE singleton),source_instance.capacity_ram_mb)
   OR NEW.cpu_millicores<source_instance.capacity_cpu_millicores OR NEW.vcpu<source_instance.capacity_vcpu THEN
   RAISE EXCEPTION 'migration source or resource shape changed' USING ERRCODE='23514',CONSTRAINT='migration_admission_current';
  END IF;
  IF EXISTS(SELECT 1 FROM migration_admissions WHERE instance_id=NEW.instance_id AND phase NOT IN ('cleaned','committed')) THEN
   RAISE EXCEPTION 'instance already has an incoming migration' USING ERRCODE='23514',CONSTRAINT='migration_admission_current';
  END IF;
  PERFORM 1 FROM migration_leases l WHERE l.instance_id=NEW.instance_id AND l.lease_token=NEW.lease_token
   AND l.source_node_id=NEW.source_node_id::text AND NOT l.pending AND NOT l.resume_fenced AND l.lease_expires_at>clock_timestamp() AND NEW.expires_at<=l.lease_expires_at
   AND l.mem_storage_key=NEW.mem_storage_key AND l.vmstate_storage_key=NEW.vmstate_storage_key FOR SHARE;
  IF NOT FOUND THEN RAISE EXCEPTION 'migration source lease is not current' USING ERRCODE='23514',CONSTRAINT='migration_admission_current'; END IF;
  PERFORM 1 FROM migration_sources WHERE lease_token=NEW.lease_token AND instance_id=NEW.instance_id AND source_node_id=NEW.source_node_id AND source_wake_id=NEW.source_wake_id AND phase='bound' FOR SHARE;
  IF NOT FOUND THEN RAISE EXCEPTION 'migration source physical binding is unavailable' USING ERRCODE='23514',CONSTRAINT='migration_source_current'; END IF;
  SELECT * INTO used FROM migration_admission_node_usage(NEW.destination_node_id);
  IF (n.admission_ceiling_mb>0 AND used.ram+NEW.ram_mb>n.admission_ceiling_mb) OR
     (n.vpcpus>0 AND used.cpu+NEW.cpu_millicores>n.vpcpus::bigint*1000) OR
     (n.vcpu_budget>0 AND used.vcpu+NEW.vcpu>n.vcpu_budget) THEN
   RAISE EXCEPTION 'migration destination capacity exhausted' USING ERRCODE='23514',CONSTRAINT='migration_admission_capacity';
  END IF;
  RETURN NEW;
 END IF;
 IF (to_jsonb(NEW)-ARRAY['phase','adoption_receipt','cleanup_proof','active_lease_token']) IS DISTINCT FROM (to_jsonb(OLD)-ARRAY['phase','adoption_receipt','cleanup_proof','active_lease_token'])
  OR (OLD.adoption_receipt<>''::bytea AND OLD.adoption_receipt IS DISTINCT FROM NEW.adoption_receipt)
  OR (OLD.cleanup_proof<>'' AND OLD.cleanup_proof IS DISTINCT FROM NEW.cleanup_proof) THEN
  RAISE EXCEPTION 'migration admission identity and receipts are immutable' USING ERRCODE='23514',CONSTRAINT='migration_admission_current';
 END IF;
 IF NEW.phase=OLD.phase THEN
  IF NEW.adoption_receipt IS DISTINCT FROM OLD.adoption_receipt OR NEW.cleanup_proof IS DISTINCT FROM OLD.cleanup_proof THEN RAISE EXCEPTION 'migration receipt requires its phase transition' USING ERRCODE='23514',CONSTRAINT='migration_admission_current'; END IF;
  RETURN NEW;
 END IF;
 IF NOT ((OLD.phase='reserved' AND NEW.phase IN ('adopting','cleaned')) OR
         (OLD.phase='adopting' AND NEW.phase IN ('adopted','cleaning')) OR
         (OLD.phase='adopted' AND NEW.phase IN ('cleaning','committed')) OR
         (OLD.phase='cleaning' AND NEW.phase='cleaned')) THEN
  RAISE EXCEPTION 'invalid migration admission transition' USING ERRCODE='23514',CONSTRAINT='migration_admission_current';
 END IF;
 IF NEW.phase='adopting' THEN
  SELECT * INTO n FROM compute_nodes WHERE id=NEW.destination_node_id FOR SHARE;
  IF NOT FOUND OR n.lifecycle NOT IN ('active','recovering') OR n.lifecycle_generation<>NEW.destination_generation OR NEW.expires_at<=clock_timestamp() THEN
   RAISE EXCEPTION 'stale migration destination' USING ERRCODE='23514',CONSTRAINT='instance_node_admission_current';
  END IF;
 END IF;
 IF OLD.phase='reserved' AND NEW.phase='cleaned' AND NEW.cleanup_proof<>'no_dispatch' THEN
  RAISE EXCEPTION 'unstarted cancellation requires no-dispatch proof' USING ERRCODE='23514',CONSTRAINT='migration_admission_current';
 END IF;
 IF NEW.phase='committed' AND NOT EXISTS(SELECT 1 FROM instances owned WHERE owned.id=NEW.instance_id AND owned.node_id=NEW.destination_node_id AND owned.wake_id=NEW.destination_wake_id AND owned.lease_token=NEW.lease_token AND owned.state='running') THEN
  RAISE EXCEPTION 'migration admission has no committed owner' USING ERRCODE='23514',CONSTRAINT='migration_admission_current';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_instance_migration_admission() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE r migration_admissions%ROWTYPE; n compute_nodes%ROWTYPE; used record; pending boolean;
BEGIN
 IF TG_OP IN ('UPDATE','DELETE') THEN
  SELECT * INTO r FROM migration_admissions WHERE instance_id=OLD.id AND phase NOT IN ('cleaned','committed') FOR UPDATE;
  IF FOUND THEN
   IF TG_OP='DELETE' THEN
    RAISE EXCEPTION 'instance has unresolved incoming migration' USING ERRCODE='23514',CONSTRAINT='migration_admission_current';
   END IF;
   IF (NEW.node_id,NEW.wake_id,NEW.state,NEW.lease_token,NEW.app_id,NEW.deployment_id) IS DISTINCT FROM (OLD.node_id,OLD.wake_id,OLD.state,OLD.lease_token,OLD.app_id,OLD.deployment_id)
    AND NOT (r.phase='adopted' AND OLD.node_id=r.source_node_id AND OLD.wake_id=r.source_wake_id AND NEW.node_id=r.destination_node_id AND NEW.wake_id=r.destination_wake_id AND NEW.state='running' AND NEW.lease_token IS NOT DISTINCT FROM r.lease_token AND NEW.app_id IS NOT DISTINCT FROM OLD.app_id AND NEW.deployment_id IS NOT DISTINCT FROM OLD.deployment_id) THEN
    RAISE EXCEPTION 'source has unresolved incoming migration' USING ERRCODE='23514',CONSTRAINT='migration_admission_current';
   END IF;
  END IF;
  IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 END IF;
 IF TG_OP='UPDATE' AND NEW.node_id IS DISTINCT FROM OLD.node_id THEN
  SELECT * INTO r FROM migration_admissions WHERE instance_id=OLD.id AND source_node_id=OLD.node_id AND destination_node_id=NEW.node_id AND lease_token=OLD.lease_token FOR UPDATE;
  IF FOUND THEN
   SELECT * INTO n FROM compute_nodes WHERE id=NEW.node_id FOR SHARE;
   IF r.phase<>'adopted' OR OLD.wake_id<>r.source_wake_id OR NEW.wake_id<>r.destination_wake_id OR NEW.state<>'running' OR n.lifecycle_generation<>r.destination_generation THEN
    RAISE EXCEPTION 'migration adoption is not current' USING ERRCODE='23514',CONSTRAINT='migration_admission_current';
   END IF;
   NEW.capacity_ram_mb=greatest(NEW.capacity_ram_mb,r.ram_mb);
   NEW.capacity_cpu_millicores=greatest(NEW.capacity_cpu_millicores,r.cpu_millicores);
   NEW.capacity_vcpu=greatest(NEW.capacity_vcpu,r.vcpu);
  END IF;
 END IF;
 IF NEW.state NOT IN ('waking','cold_booting','running','draining','snapshotting','migrating','warm','frozen') THEN RETURN NEW; END IF;
 IF TG_OP='UPDATE' AND NEW.node_id=OLD.node_id AND OLD.state IN ('waking','cold_booting','running','draining','snapshotting','migrating','warm','frozen')
  AND NEW.ram_mb<=OLD.ram_mb AND NEW.capacity_ram_mb<=OLD.capacity_ram_mb AND NEW.capacity_cpu_millicores<=OLD.capacity_cpu_millicores AND NEW.capacity_vcpu<=OLD.capacity_vcpu THEN RETURN NEW; END IF;
 SELECT EXISTS(SELECT 1 FROM migration_admissions WHERE destination_node_id=NEW.node_id AND phase NOT IN ('cleaned','committed')) OR EXISTS(SELECT 1 FROM migration_sources WHERE source_node_id=NEW.node_id AND phase IN ('bound','retiring')) INTO pending;
 IF NOT pending THEN RETURN NEW; END IF;
 SELECT * INTO n FROM compute_nodes WHERE id=NEW.node_id FOR SHARE;
 PERFORM pg_advisory_xact_lock(1852793957,hashtext(NEW.node_id::text));
 SELECT * INTO used FROM migration_admission_node_usage(NEW.node_id,NEW.id);
 IF (n.admission_ceiling_mb>0 AND used.ram+greatest(NEW.capacity_ram_mb,NEW.ram_mb+(SELECT overhead_mb FROM service_capacity_policy WHERE singleton))>n.admission_ceiling_mb)
  OR (n.vpcpus>0 AND used.cpu+NEW.capacity_cpu_millicores>n.vpcpus::bigint*1000)
  OR (n.vcpu_budget>0 AND used.vcpu+NEW.capacity_vcpu>n.vcpu_budget) THEN
  RAISE EXCEPTION 'capacity reserved for incoming migration' USING ERRCODE='23514',CONSTRAINT='migration_admission_capacity';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION migration_admission_node_usage(target uuid,omit_instance uuid DEFAULT NULL) RETURNS TABLE(ram bigint,cpu bigint,vcpu bigint) LANGUAGE sql AS $$
 SELECT coalesce(sum(u.ram),0)::bigint,coalesce(sum(u.cpu),0)::bigint,coalesce(sum(u.vcpu),0)::bigint FROM (
  SELECT greatest(i.capacity_ram_mb,i.ram_mb+p.overhead_mb)::bigint AS ram,
   greatest(i.capacity_cpu_millicores,coalesce(nullif(a.cpu_millicores,0),p.startup_cpu))::bigint AS cpu,
   greatest(i.capacity_vcpu,CASE WHEN i.app_id IS NULL THEN 1 ELSE coalesce((p.plan_vcpus->>ac.plan)::integer,4) END)::bigint AS vcpu
  FROM instances i LEFT JOIN apps a ON a.id=i.app_id LEFT JOIN accounts ac ON ac.id=a.account_id CROSS JOIN service_capacity_policy p
  WHERE p.singleton AND i.node_id=target AND (omit_instance IS NULL OR i.id<>omit_instance)
   AND i.state IN ('waking','cold_booting','running','draining','snapshotting','migrating','warm','frozen')
  UNION ALL SELECT r.ram_mb::bigint,r.cpu_millicores::bigint,r.vcpu::bigint FROM migration_admissions r
  WHERE r.destination_node_id=target AND r.phase NOT IN ('cleaned','committed')
   AND (omit_instance IS NULL OR r.instance_id<>omit_instance)
   AND NOT EXISTS(SELECT 1 FROM instances i WHERE i.id=r.instance_id AND i.node_id=r.destination_node_id AND i.wake_id=r.destination_wake_id AND i.state='running')
  UNION ALL SELECT * FROM migration_source_node_usage(target)
 ) u;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.service_capacity_snapshot() RETURNS jsonb
    LANGUAGE sql
    SET jit TO 'off'
    AS $$
WITH policy AS (SELECT * FROM service_capacity_policy WHERE singleton),
eligible AS (
    SELECT a.id, a.ram_mb, a.cpu_millicores, a.manifest,
           coalesce((p.plan_vcpus->>ac.plan)::integer, 4) AS vcpu
      FROM apps a JOIN accounts ac ON ac.id=a.account_id CROSS JOIN policy p
     WHERE a.status IN ('active','evicted_cold') AND a.manifest->>'execution_mode'='service'
), scopes AS (
    SELECT id AS app_id, 'default'::text AS scope FROM eligible
    UNION
    SELECT d.app_id, d.scope FROM deployments d JOIN eligible a ON a.id=d.app_id
     WHERE d.status IN ('pending','building','imaging','snapshotting','live')
), declaration AS (
    SELECT s.app_id::text || ':' || s.scope AS key,
           greatest(0, coalesce((a.manifest->'service_replicas'->>'desired')::integer,1)) AS desired,
           CASE WHEN EXISTS (SELECT 1 FROM deployments d WHERE d.app_id=a.id AND d.scope=s.scope AND d.status='live')
                  AND (EXISTS (SELECT 1 FROM deployments d WHERE d.app_id=a.id AND d.scope=s.scope AND d.status IN ('pending','building','imaging','snapshotting'))
                    OR (SELECT count(*) FROM deployments d WHERE d.app_id=a.id AND d.scope=s.scope AND d.status='live')>1)
                THEN 1 ELSE 0 END AS surge,
           a.ram_mb+p.overhead_mb+coalesce((
               SELECT max((SELECT coalesce(sum(greatest(0,(sc->>'ram_mb')::integer)),0)
                             FROM jsonb_array_elements(coalesce(d.sidecars,'[]'::jsonb)) sc))
                 FROM deployments d WHERE d.app_id=a.id AND d.scope=s.scope
                   AND d.status IN ('pending','building','imaging','snapshotting','live')),0) AS ram,
           greatest(a.cpu_millicores,p.startup_cpu)+coalesce((
               SELECT max((SELECT coalesce(sum(greatest(0,coalesce(nullif(greatest(0,(sc->>'cpu_millicores')::integer),0),p.startup_cpu))),0)
                             FROM jsonb_array_elements(coalesce(d.sidecars,'[]'::jsonb)) sc))
                 FROM deployments d WHERE d.app_id=a.id AND d.scope=s.scope
                   AND d.status IN ('pending','building','imaging','snapshotting','live')),0) AS cpu,
           a.vcpu
      FROM scopes s JOIN eligible a ON a.id=s.app_id CROSS JOIN policy p
), demand AS (
    SELECT key, CASE WHEN desired=0 THEN 0 ELSE desired+surge END AS count, desired, ram, cpu, vcpu
      FROM declaration
), resident AS (
    SELECT i.node_id, a.id::text || ':' || coalesce(d.scope,'default') AS key,
           greatest(i.capacity_ram_mb,i.ram_mb+p.overhead_mb+coalesce((SELECT sum(greatest(0,(sc->>'ram_mb')::integer))
                           FROM jsonb_array_elements(coalesce(d.sidecars,'[]'::jsonb)) sc),0)) AS ram,
           greatest(i.capacity_cpu_millicores,greatest(coalesce(a.cpu_millicores,p.startup_cpu),p.startup_cpu)+coalesce((
               SELECT sum(greatest(0,coalesce(nullif(greatest(0,(sc->>'cpu_millicores')::integer),0),p.startup_cpu)))
                 FROM jsonb_array_elements(coalesce(d.sidecars,'[]'::jsonb)) sc),0)) AS cpu,
           greatest(i.capacity_vcpu,CASE WHEN i.app_id IS NULL THEN 1 ELSE coalesce((p.plan_vcpus->>ac.plan)::integer,4) END) AS vcpu,
           (i.state<>'warm' AND i.mode IN ('normal','service') AND a.manifest->>'execution_mode'='service'
              AND EXISTS (SELECT 1 FROM demand x WHERE x.key=a.id::text || ':' || coalesce(d.scope,'default') )) AS service
      FROM instances i LEFT JOIN apps a ON a.id=i.app_id LEFT JOIN accounts ac ON ac.id=a.account_id
      LEFT JOIN deployments d ON d.id=i.deployment_id CROSS JOIN policy p
     WHERE i.state IN ('waking','cold_booting','running','draining','warm','snapshotting','migrating')
    UNION ALL
    SELECT r.destination_node_id,NULL::text,r.ram_mb::bigint,r.cpu_millicores::bigint,r.vcpu::bigint,false
      FROM migration_admissions r WHERE r.phase NOT IN ('cleaned','committed')
       AND NOT EXISTS(SELECT 1 FROM instances i WHERE i.id=r.instance_id AND i.node_id=r.destination_node_id AND i.wake_id=r.destination_wake_id AND i.state='running')
    UNION ALL
    SELECT n.id,NULL::text,u.ram,u.cpu,u.vcpu,false FROM compute_nodes n CROSS JOIN LATERAL migration_source_node_usage(n.id) u
      WHERE u.ram>0 OR u.cpu>0 OR u.vcpu>0
), shape AS (
    SELECT greatest(coalesce((SELECT max(ram) FROM demand WHERE count>0),0),coalesce((SELECT max(ram) FROM resident WHERE service),0),1) AS ram,
           greatest(coalesce((SELECT max(cpu) FROM demand WHERE count>0),0),coalesce((SELECT max(cpu) FROM resident WHERE service),0),1) AS cpu,
           greatest(coalesce((SELECT max(vcpu) FROM demand WHERE count>0),0),coalesce((SELECT max(vcpu) FROM resident WHERE service),0),1) AS vcpu
), other_usage AS (
    SELECT node_id, sum(ram)::bigint AS ram, sum(cpu)::bigint AS cpu, sum(vcpu)::bigint AS vcpu
      FROM resident WHERE NOT coalesce(service,false) GROUP BY node_id
), service_usage AS (
    SELECT node_id, count(*)::bigint AS count FROM resident WHERE service GROUP BY node_id
), nodes AS (
    SELECT n.id, n.name, coalesce(u.ram,0) AS other_ram, coalesce(u.cpu,0) AS other_cpu, coalesce(u.vcpu,0) AS other_vcpu,
           coalesce(s.count,0) AS service_count,
           (coalesce(u.ram,0)<=n.admission_ceiling_mb AND coalesce(u.cpu,0)<=n.vpcpus::bigint*1000*p.cpu_overcommit AND coalesce(u.vcpu,0)<=n.vcpu_budget) AS ordinary_fit,
           greatest(0,least((n.admission_ceiling_mb-coalesce(u.ram,0))/sh.ram,
                       (n.vpcpus::bigint*1000*p.cpu_overcommit-coalesce(u.cpu,0))/sh.cpu,
                       (n.vcpu_budget-coalesce(u.vcpu,0))/sh.vcpu))::bigint AS slots
      FROM compute_nodes n CROSS JOIN policy p CROSS JOIN shape sh
      LEFT JOIN other_usage u ON u.node_id=n.id LEFT JOIN service_usage s ON s.node_id=n.id
     WHERE n.lifecycle='active' AND n.admission_ceiling_mb>0 AND n.vpcpus>0 AND n.vcpu_budget>0
       AND n.last_heartbeat_at>=clock_timestamp()-make_interval(secs=>p.heartbeat_seconds)
), totals AS (
    SELECT count(*) AS healthy_nodes, coalesce(sum(slots),0) AS fleet_slots,
           coalesce(sum(slots)-max(slots),0) AS failover_slots,
           coalesce(bool_and(service_count<=slots AND ordinary_fit),true) AS placements_fit FROM nodes
), required AS (SELECT coalesce(sum(count),0) AS count, coalesce(sum(desired),0) AS desired FROM demand),
actual_targets AS (SELECT key,count(*) AS count FROM resident WHERE service GROUP BY key),
projection AS (
    SELECT jsonb_build_object(
        'enabled',p.enabled,
        'state',CASE WHEN NOT p.enabled THEN 'disabled'
                     WHEN t.healthy_nodes>=2 AND t.placements_fit AND r.count<=t.failover_slots
                       AND NOT EXISTS (SELECT 1 FROM actual_targets a JOIN demand d ON d.key=a.key WHERE a.count>d.count) THEN 'protected'
                     WHEN t.placements_fit AND r.count<=t.fleet_slots THEN 'degraded' ELSE 'needs_hardware' END,
        'healthy_nodes',t.healthy_nodes,'desired_replicas',r.desired,'reserved_replicas',r.count,
        'replica_ram_mb',sh.ram,'replica_cpu_millicores',sh.cpu,'replica_vcpu',sh.vcpu,
        'fleet_slots',t.fleet_slots,'failover_slots',t.failover_slots,'placements_fit',t.placements_fit,
        'demands',coalesce((SELECT jsonb_object_agg(key,jsonb_build_object('count',count,'ram',ram,'cpu',cpu,'vcpu',vcpu)) FROM demand),'{}'::jsonb),
        'other',coalesce((SELECT jsonb_object_agg(id::text,jsonb_build_object('ram',other_ram,'cpu',other_cpu,'vcpu',other_vcpu)) FROM nodes),'{}'::jsonb),
        'placement',coalesce((SELECT jsonb_object_agg(id::text,jsonb_build_object('slots',slots,'used',service_count)) FROM nodes),'{}'::jsonb),
        'service_usage',coalesce((SELECT jsonb_object_agg(node_id::text,count) FROM service_usage),'{}'::jsonb),
        'resident',coalesce((SELECT jsonb_object_agg(node_id::text,cost) FROM (SELECT node_id,jsonb_build_object('ram',sum(ram),'cpu',sum(cpu),'vcpu',sum(vcpu)) AS cost FROM resident GROUP BY node_id) u),'{}'::jsonb),
        'actual',coalesce((SELECT jsonb_object_agg(key,count) FROM actual_targets),'{}'::jsonb)
    ) AS data FROM policy p CROSS JOIN totals t CROSS JOIN required r CROSS JOIN shape sh
) SELECT data FROM projection;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION guard_instance_migration_source() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE r migration_sources%ROWTYPE;
BEGIN
 IF NEW.state='migrating' AND (OLD.state,OLD.lease_token) IS DISTINCT FROM (NEW.state,NEW.lease_token) THEN
  SELECT * INTO r FROM migration_sources WHERE lease_token=NEW.lease_token FOR SHARE;
  IF FOUND THEN
   IF r.phase<>'bound' OR r.instance_id<>NEW.id OR r.source_node_id<>NEW.node_id OR r.source_wake_id<>NEW.wake_id THEN
    RAISE EXCEPTION 'migration source dispatch authority ended' USING ERRCODE='23514',CONSTRAINT='migration_source_current';
   END IF;
   PERFORM 1 FROM migration_leases WHERE lease_token=r.lease_token AND NOT resume_fenced AND lease_expires_at>clock_timestamp() FOR SHARE;
   IF NOT FOUND THEN RAISE EXCEPTION 'migration source dispatch lease ended' USING ERRCODE='23514',CONSTRAINT='migration_source_current'; END IF;
  END IF;
 END IF;
 SELECT * INTO r FROM migration_sources WHERE instance_id=OLD.id AND source_node_id=OLD.node_id AND source_wake_id=OLD.wake_id AND phase='bound' FOR UPDATE;
 IF NOT FOUND THEN RETURN NEW; END IF;
 IF NEW.node_id IS DISTINCT FROM OLD.node_id OR NEW.wake_id IS DISTINCT FROM OLD.wake_id THEN
  IF NEW.node_id=OLD.node_id OR NEW.state<>'running' OR NEW.lease_token IS DISTINCT FROM r.lease_token OR NOT EXISTS(
   SELECT 1 FROM migration_admissions WHERE instance_id=OLD.id AND lease_token=r.lease_token AND source_node_id=r.source_node_id AND source_wake_id=r.source_wake_id AND destination_node_id=NEW.node_id AND destination_wake_id=NEW.wake_id AND phase='adopted'
  ) THEN RAISE EXCEPTION 'migration source physical owner cannot change' USING ERRCODE='23514',CONSTRAINT='migration_source_current'; END IF;
 END IF;
 NEW.capacity_ram_mb=greatest(NEW.capacity_ram_mb,r.ram_mb);
 NEW.capacity_cpu_millicores=greatest(NEW.capacity_cpu_millicores,r.cpu_millicores);
 NEW.capacity_vcpu=greatest(NEW.capacity_vcpu,r.vcpu);
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER instance_migration_source_guard BEFORE UPDATE ON instances FOR EACH ROW EXECUTE FUNCTION guard_instance_migration_source();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN IF EXISTS(SELECT 1 FROM migration_sources) THEN RAISE EXCEPTION 'migration source receipts must be preserved'; END IF; END $$;
-- +goose StatementEnd
DROP TRIGGER service_capacity_before ON migration_leases;
DROP INDEX migration_leases_source_recovery;
DROP TRIGGER service_capacity_after ON migration_leases;
DROP TRIGGER source_migration_lease_node_guard ON migration_leases;
DROP FUNCTION guard_source_migration_lease_node();
DROP TRIGGER instance_source_retirement_commit ON instances;
DROP TRIGGER instance_migration_source_guard ON instances;
DROP TRIGGER node_migration_source_guard ON compute_nodes;
DROP FUNCTION commit_migration_source_retirement();
DROP FUNCTION guard_instance_migration_source();
DROP FUNCTION guard_node_migration_sources();

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_migration_admission() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE n compute_nodes%ROWTYPE; source_instance instances%ROWTYPE; used record;
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'migration admission receipts must be retained' USING ERRCODE='23514',CONSTRAINT='migration_admission_current'; END IF;
 IF TG_OP='INSERT' THEN
  IF NEW.phase<>'reserved' OR NEW.adoption_receipt<>''::bytea OR NEW.cleanup_proof<>'' OR NEW.expires_at<=clock_timestamp() THEN
   RAISE EXCEPTION 'invalid initial migration admission' USING ERRCODE='23514',CONSTRAINT='migration_admission_current';
  END IF;
  SELECT * INTO n FROM compute_nodes WHERE id=NEW.destination_node_id FOR SHARE;
  IF NOT FOUND OR n.lifecycle NOT IN ('active','recovering') OR n.lifecycle_generation<>NEW.destination_generation THEN
   RAISE EXCEPTION 'stale migration destination' USING ERRCODE='23514',CONSTRAINT='instance_node_admission_current';
  END IF;
  PERFORM pg_advisory_xact_lock(1852793957,hashtext(NEW.destination_node_id::text));
  SELECT * INTO source_instance FROM instances WHERE id=NEW.instance_id FOR UPDATE;
  IF NOT FOUND OR source_instance.node_id<>NEW.source_node_id OR source_instance.wake_id<>NEW.source_wake_id OR source_instance.state<>'migrating' OR source_instance.lease_token IS DISTINCT FROM NEW.lease_token
   OR NEW.ram_mb<greatest(source_instance.ram_mb+(SELECT overhead_mb FROM service_capacity_policy WHERE singleton),source_instance.capacity_ram_mb)
   OR NEW.cpu_millicores<source_instance.capacity_cpu_millicores OR NEW.vcpu<source_instance.capacity_vcpu THEN
   RAISE EXCEPTION 'migration source or resource shape changed' USING ERRCODE='23514',CONSTRAINT='migration_admission_current';
  END IF;
  IF EXISTS(SELECT 1 FROM migration_admissions WHERE instance_id=NEW.instance_id AND phase NOT IN ('cleaned','committed')) THEN
   RAISE EXCEPTION 'instance already has an incoming migration' USING ERRCODE='23514',CONSTRAINT='migration_admission_current';
  END IF;
  PERFORM 1 FROM migration_leases l WHERE l.instance_id=NEW.instance_id AND l.lease_token=NEW.lease_token
   AND l.source_node_id=NEW.source_node_id::text AND NOT l.pending AND NOT l.resume_fenced AND l.lease_expires_at>clock_timestamp() AND NEW.expires_at<=l.lease_expires_at
   AND l.mem_storage_key=NEW.mem_storage_key AND l.vmstate_storage_key=NEW.vmstate_storage_key FOR SHARE;
  IF NOT FOUND THEN RAISE EXCEPTION 'migration source lease is not current' USING ERRCODE='23514',CONSTRAINT='migration_admission_current'; END IF;
  SELECT * INTO used FROM migration_admission_node_usage(NEW.destination_node_id);
  IF (n.admission_ceiling_mb>0 AND used.ram+NEW.ram_mb>n.admission_ceiling_mb) OR
     (n.vpcpus>0 AND used.cpu+NEW.cpu_millicores>n.vpcpus::bigint*1000) OR
     (n.vcpu_budget>0 AND used.vcpu+NEW.vcpu>n.vcpu_budget) THEN
   RAISE EXCEPTION 'migration destination capacity exhausted' USING ERRCODE='23514',CONSTRAINT='migration_admission_capacity';
  END IF;
  RETURN NEW;
 END IF;
 IF (to_jsonb(NEW)-ARRAY['phase','adoption_receipt','cleanup_proof','active_lease_token']) IS DISTINCT FROM (to_jsonb(OLD)-ARRAY['phase','adoption_receipt','cleanup_proof','active_lease_token'])
  OR (OLD.adoption_receipt<>''::bytea AND OLD.adoption_receipt IS DISTINCT FROM NEW.adoption_receipt)
  OR (OLD.cleanup_proof<>'' AND OLD.cleanup_proof IS DISTINCT FROM NEW.cleanup_proof) THEN
  RAISE EXCEPTION 'migration admission identity and receipts are immutable' USING ERRCODE='23514',CONSTRAINT='migration_admission_current';
 END IF;
 IF NEW.phase=OLD.phase THEN
  IF NEW.adoption_receipt IS DISTINCT FROM OLD.adoption_receipt OR NEW.cleanup_proof IS DISTINCT FROM OLD.cleanup_proof THEN RAISE EXCEPTION 'migration receipt requires its phase transition' USING ERRCODE='23514',CONSTRAINT='migration_admission_current'; END IF;
  RETURN NEW;
 END IF;
 IF NOT ((OLD.phase='reserved' AND NEW.phase IN ('adopting','cleaned')) OR
         (OLD.phase='adopting' AND NEW.phase IN ('adopted','cleaning')) OR
         (OLD.phase='adopted' AND NEW.phase IN ('cleaning','committed')) OR
         (OLD.phase='cleaning' AND NEW.phase='cleaned')) THEN
  RAISE EXCEPTION 'invalid migration admission transition' USING ERRCODE='23514',CONSTRAINT='migration_admission_current';
 END IF;
 IF NEW.phase='adopting' THEN
  SELECT * INTO n FROM compute_nodes WHERE id=NEW.destination_node_id FOR SHARE;
  IF NOT FOUND OR n.lifecycle NOT IN ('active','recovering') OR n.lifecycle_generation<>NEW.destination_generation OR NEW.expires_at<=clock_timestamp() THEN
   RAISE EXCEPTION 'stale migration destination' USING ERRCODE='23514',CONSTRAINT='instance_node_admission_current';
  END IF;
 END IF;
 IF OLD.phase='reserved' AND NEW.phase='cleaned' AND NEW.cleanup_proof<>'no_dispatch' THEN
  RAISE EXCEPTION 'unstarted cancellation requires no-dispatch proof' USING ERRCODE='23514',CONSTRAINT='migration_admission_current';
 END IF;
 IF NEW.phase='committed' AND NOT EXISTS(SELECT 1 FROM instances owned WHERE owned.id=NEW.instance_id AND owned.node_id=NEW.destination_node_id AND owned.wake_id=NEW.destination_wake_id AND owned.lease_token=NEW.lease_token AND owned.state='running') THEN
  RAISE EXCEPTION 'migration admission has no committed owner' USING ERRCODE='23514',CONSTRAINT='migration_admission_current';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_instance_migration_admission() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE r migration_admissions%ROWTYPE; n compute_nodes%ROWTYPE; used record; pending boolean;
BEGIN
 IF TG_OP IN ('UPDATE','DELETE') THEN
  SELECT * INTO r FROM migration_admissions WHERE instance_id=OLD.id AND phase NOT IN ('cleaned','committed') FOR UPDATE;
  IF FOUND THEN
   IF TG_OP='DELETE' THEN
    RAISE EXCEPTION 'instance has unresolved incoming migration' USING ERRCODE='23514',CONSTRAINT='migration_admission_current';
   END IF;
   IF (NEW.node_id,NEW.wake_id,NEW.state,NEW.lease_token,NEW.app_id,NEW.deployment_id) IS DISTINCT FROM (OLD.node_id,OLD.wake_id,OLD.state,OLD.lease_token,OLD.app_id,OLD.deployment_id)
    AND NOT (r.phase='adopted' AND OLD.node_id=r.source_node_id AND OLD.wake_id=r.source_wake_id AND NEW.node_id=r.destination_node_id AND NEW.wake_id=r.destination_wake_id AND NEW.state='running' AND NEW.lease_token IS NOT DISTINCT FROM r.lease_token AND NEW.app_id IS NOT DISTINCT FROM OLD.app_id AND NEW.deployment_id IS NOT DISTINCT FROM OLD.deployment_id) THEN
    RAISE EXCEPTION 'source has unresolved incoming migration' USING ERRCODE='23514',CONSTRAINT='migration_admission_current';
   END IF;
  END IF;
  IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 END IF;
 IF TG_OP='UPDATE' AND NEW.node_id IS DISTINCT FROM OLD.node_id THEN
  SELECT * INTO r FROM migration_admissions WHERE instance_id=OLD.id AND source_node_id=OLD.node_id AND destination_node_id=NEW.node_id AND lease_token=OLD.lease_token FOR UPDATE;
  IF FOUND THEN
   SELECT * INTO n FROM compute_nodes WHERE id=NEW.node_id FOR SHARE;
   IF r.phase<>'adopted' OR OLD.wake_id<>r.source_wake_id OR NEW.wake_id<>r.destination_wake_id OR NEW.state<>'running' OR n.lifecycle_generation<>r.destination_generation THEN
    RAISE EXCEPTION 'migration adoption is not current' USING ERRCODE='23514',CONSTRAINT='migration_admission_current';
   END IF;
   NEW.capacity_ram_mb=greatest(NEW.capacity_ram_mb,r.ram_mb);
   NEW.capacity_cpu_millicores=greatest(NEW.capacity_cpu_millicores,r.cpu_millicores);
   NEW.capacity_vcpu=greatest(NEW.capacity_vcpu,r.vcpu);
  END IF;
 END IF;
 IF NEW.state NOT IN ('waking','cold_booting','running','draining','snapshotting','migrating','warm','frozen') THEN RETURN NEW; END IF;
 IF TG_OP='UPDATE' AND NEW.node_id=OLD.node_id AND OLD.state IN ('waking','cold_booting','running','draining','snapshotting','migrating','warm','frozen')
  AND NEW.ram_mb<=OLD.ram_mb AND NEW.capacity_ram_mb<=OLD.capacity_ram_mb AND NEW.capacity_cpu_millicores<=OLD.capacity_cpu_millicores AND NEW.capacity_vcpu<=OLD.capacity_vcpu THEN RETURN NEW; END IF;
 SELECT EXISTS(SELECT 1 FROM migration_admissions WHERE destination_node_id=NEW.node_id AND phase NOT IN ('cleaned','committed')) INTO pending;
 IF NOT pending THEN RETURN NEW; END IF;
 SELECT * INTO n FROM compute_nodes WHERE id=NEW.node_id FOR SHARE;
 PERFORM pg_advisory_xact_lock(1852793957,hashtext(NEW.node_id::text));
 SELECT * INTO used FROM migration_admission_node_usage(NEW.node_id,NEW.id);
 IF (n.admission_ceiling_mb>0 AND used.ram+greatest(NEW.capacity_ram_mb,NEW.ram_mb+(SELECT overhead_mb FROM service_capacity_policy WHERE singleton))>n.admission_ceiling_mb)
  OR (n.vpcpus>0 AND used.cpu+NEW.capacity_cpu_millicores>n.vpcpus::bigint*1000)
  OR (n.vcpu_budget>0 AND used.vcpu+NEW.capacity_vcpu>n.vcpu_budget) THEN
  RAISE EXCEPTION 'capacity reserved for incoming migration' USING ERRCODE='23514',CONSTRAINT='migration_admission_capacity';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION migration_admission_node_usage(target uuid,omit_instance uuid DEFAULT NULL) RETURNS TABLE(ram bigint,cpu bigint,vcpu bigint) LANGUAGE sql AS $$
 SELECT coalesce(sum(u.ram),0)::bigint,coalesce(sum(u.cpu),0)::bigint,coalesce(sum(u.vcpu),0)::bigint FROM (
  SELECT greatest(i.capacity_ram_mb,i.ram_mb+p.overhead_mb)::bigint AS ram,
   greatest(i.capacity_cpu_millicores,coalesce(nullif(a.cpu_millicores,0),p.startup_cpu))::bigint AS cpu,
   greatest(i.capacity_vcpu,CASE WHEN i.app_id IS NULL THEN 1 ELSE coalesce((p.plan_vcpus->>ac.plan)::integer,4) END)::bigint AS vcpu
  FROM instances i LEFT JOIN apps a ON a.id=i.app_id LEFT JOIN accounts ac ON ac.id=a.account_id CROSS JOIN service_capacity_policy p
  WHERE p.singleton AND i.node_id=target AND (omit_instance IS NULL OR i.id<>omit_instance)
   AND i.state IN ('waking','cold_booting','running','draining','snapshotting','migrating','warm','frozen')
  UNION ALL SELECT r.ram_mb::bigint,r.cpu_millicores::bigint,r.vcpu::bigint FROM migration_admissions r
  WHERE r.destination_node_id=target AND r.phase NOT IN ('cleaned','committed')
   AND (omit_instance IS NULL OR r.instance_id<>omit_instance)
   AND NOT EXISTS(SELECT 1 FROM instances i WHERE i.id=r.instance_id AND i.node_id=r.destination_node_id AND i.wake_id=r.destination_wake_id AND i.state='running')
 ) u;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.service_capacity_snapshot() RETURNS jsonb
    LANGUAGE sql
    SET jit TO 'off'
    AS $$
WITH policy AS (SELECT * FROM service_capacity_policy WHERE singleton),
eligible AS (
    SELECT a.id, a.ram_mb, a.cpu_millicores, a.manifest,
           coalesce((p.plan_vcpus->>ac.plan)::integer, 4) AS vcpu
      FROM apps a JOIN accounts ac ON ac.id=a.account_id CROSS JOIN policy p
     WHERE a.status IN ('active','evicted_cold') AND a.manifest->>'execution_mode'='service'
), scopes AS (
    SELECT id AS app_id, 'default'::text AS scope FROM eligible
    UNION
    SELECT d.app_id, d.scope FROM deployments d JOIN eligible a ON a.id=d.app_id
     WHERE d.status IN ('pending','building','imaging','snapshotting','live')
), declaration AS (
    SELECT s.app_id::text || ':' || s.scope AS key,
           greatest(0, coalesce((a.manifest->'service_replicas'->>'desired')::integer,1)) AS desired,
           CASE WHEN EXISTS (SELECT 1 FROM deployments d WHERE d.app_id=a.id AND d.scope=s.scope AND d.status='live')
                  AND (EXISTS (SELECT 1 FROM deployments d WHERE d.app_id=a.id AND d.scope=s.scope AND d.status IN ('pending','building','imaging','snapshotting'))
                    OR (SELECT count(*) FROM deployments d WHERE d.app_id=a.id AND d.scope=s.scope AND d.status='live')>1)
                THEN 1 ELSE 0 END AS surge,
           a.ram_mb+p.overhead_mb+coalesce((
               SELECT max((SELECT coalesce(sum(greatest(0,(sc->>'ram_mb')::integer)),0)
                             FROM jsonb_array_elements(coalesce(d.sidecars,'[]'::jsonb)) sc))
                 FROM deployments d WHERE d.app_id=a.id AND d.scope=s.scope
                   AND d.status IN ('pending','building','imaging','snapshotting','live')),0) AS ram,
           greatest(a.cpu_millicores,p.startup_cpu)+coalesce((
               SELECT max((SELECT coalesce(sum(greatest(0,coalesce(nullif(greatest(0,(sc->>'cpu_millicores')::integer),0),p.startup_cpu))),0)
                             FROM jsonb_array_elements(coalesce(d.sidecars,'[]'::jsonb)) sc))
                 FROM deployments d WHERE d.app_id=a.id AND d.scope=s.scope
                   AND d.status IN ('pending','building','imaging','snapshotting','live')),0) AS cpu,
           a.vcpu
      FROM scopes s JOIN eligible a ON a.id=s.app_id CROSS JOIN policy p
), demand AS (
    SELECT key, CASE WHEN desired=0 THEN 0 ELSE desired+surge END AS count, desired, ram, cpu, vcpu
      FROM declaration
), resident AS (
    SELECT i.node_id, a.id::text || ':' || coalesce(d.scope,'default') AS key,
           greatest(i.capacity_ram_mb,i.ram_mb+p.overhead_mb+coalesce((SELECT sum(greatest(0,(sc->>'ram_mb')::integer))
                           FROM jsonb_array_elements(coalesce(d.sidecars,'[]'::jsonb)) sc),0)) AS ram,
           greatest(i.capacity_cpu_millicores,greatest(coalesce(a.cpu_millicores,p.startup_cpu),p.startup_cpu)+coalesce((
               SELECT sum(greatest(0,coalesce(nullif(greatest(0,(sc->>'cpu_millicores')::integer),0),p.startup_cpu)))
                 FROM jsonb_array_elements(coalesce(d.sidecars,'[]'::jsonb)) sc),0)) AS cpu,
           greatest(i.capacity_vcpu,CASE WHEN i.app_id IS NULL THEN 1 ELSE coalesce((p.plan_vcpus->>ac.plan)::integer,4) END) AS vcpu,
           (i.state<>'warm' AND i.mode IN ('normal','service') AND a.manifest->>'execution_mode'='service'
              AND EXISTS (SELECT 1 FROM demand x WHERE x.key=a.id::text || ':' || coalesce(d.scope,'default') )) AS service
      FROM instances i LEFT JOIN apps a ON a.id=i.app_id LEFT JOIN accounts ac ON ac.id=a.account_id
      LEFT JOIN deployments d ON d.id=i.deployment_id CROSS JOIN policy p
     WHERE i.state IN ('waking','cold_booting','running','draining','warm','snapshotting','migrating')
    UNION ALL
    SELECT r.destination_node_id,NULL::text,r.ram_mb::bigint,r.cpu_millicores::bigint,r.vcpu::bigint,false
      FROM migration_admissions r WHERE r.phase NOT IN ('cleaned','committed')
       AND NOT EXISTS(SELECT 1 FROM instances i WHERE i.id=r.instance_id AND i.node_id=r.destination_node_id AND i.wake_id=r.destination_wake_id AND i.state='running')
), shape AS (
    SELECT greatest(coalesce((SELECT max(ram) FROM demand WHERE count>0),0),coalesce((SELECT max(ram) FROM resident WHERE service),0),1) AS ram,
           greatest(coalesce((SELECT max(cpu) FROM demand WHERE count>0),0),coalesce((SELECT max(cpu) FROM resident WHERE service),0),1) AS cpu,
           greatest(coalesce((SELECT max(vcpu) FROM demand WHERE count>0),0),coalesce((SELECT max(vcpu) FROM resident WHERE service),0),1) AS vcpu
), other_usage AS (
    SELECT node_id, sum(ram)::bigint AS ram, sum(cpu)::bigint AS cpu, sum(vcpu)::bigint AS vcpu
      FROM resident WHERE NOT coalesce(service,false) GROUP BY node_id
), service_usage AS (
    SELECT node_id, count(*)::bigint AS count FROM resident WHERE service GROUP BY node_id
), nodes AS (
    SELECT n.id, n.name, coalesce(u.ram,0) AS other_ram, coalesce(u.cpu,0) AS other_cpu, coalesce(u.vcpu,0) AS other_vcpu,
           coalesce(s.count,0) AS service_count,
           (coalesce(u.ram,0)<=n.admission_ceiling_mb AND coalesce(u.cpu,0)<=n.vpcpus::bigint*1000*p.cpu_overcommit AND coalesce(u.vcpu,0)<=n.vcpu_budget) AS ordinary_fit,
           greatest(0,least((n.admission_ceiling_mb-coalesce(u.ram,0))/sh.ram,
                       (n.vpcpus::bigint*1000*p.cpu_overcommit-coalesce(u.cpu,0))/sh.cpu,
                       (n.vcpu_budget-coalesce(u.vcpu,0))/sh.vcpu))::bigint AS slots
      FROM compute_nodes n CROSS JOIN policy p CROSS JOIN shape sh
      LEFT JOIN other_usage u ON u.node_id=n.id LEFT JOIN service_usage s ON s.node_id=n.id
     WHERE n.lifecycle='active' AND n.admission_ceiling_mb>0 AND n.vpcpus>0 AND n.vcpu_budget>0
       AND n.last_heartbeat_at>=clock_timestamp()-make_interval(secs=>p.heartbeat_seconds)
), totals AS (
    SELECT count(*) AS healthy_nodes, coalesce(sum(slots),0) AS fleet_slots,
           coalesce(sum(slots)-max(slots),0) AS failover_slots,
           coalesce(bool_and(service_count<=slots AND ordinary_fit),true) AS placements_fit FROM nodes
), required AS (SELECT coalesce(sum(count),0) AS count, coalesce(sum(desired),0) AS desired FROM demand),
actual_targets AS (SELECT key,count(*) AS count FROM resident WHERE service GROUP BY key),
projection AS (
    SELECT jsonb_build_object(
        'enabled',p.enabled,
        'state',CASE WHEN NOT p.enabled THEN 'disabled'
                     WHEN t.healthy_nodes>=2 AND t.placements_fit AND r.count<=t.failover_slots
                       AND NOT EXISTS (SELECT 1 FROM actual_targets a JOIN demand d ON d.key=a.key WHERE a.count>d.count) THEN 'protected'
                     WHEN t.placements_fit AND r.count<=t.fleet_slots THEN 'degraded' ELSE 'needs_hardware' END,
        'healthy_nodes',t.healthy_nodes,'desired_replicas',r.desired,'reserved_replicas',r.count,
        'replica_ram_mb',sh.ram,'replica_cpu_millicores',sh.cpu,'replica_vcpu',sh.vcpu,
        'fleet_slots',t.fleet_slots,'failover_slots',t.failover_slots,'placements_fit',t.placements_fit,
        'demands',coalesce((SELECT jsonb_object_agg(key,jsonb_build_object('count',count,'ram',ram,'cpu',cpu,'vcpu',vcpu)) FROM demand),'{}'::jsonb),
        'other',coalesce((SELECT jsonb_object_agg(id::text,jsonb_build_object('ram',other_ram,'cpu',other_cpu,'vcpu',other_vcpu)) FROM nodes),'{}'::jsonb),
        'placement',coalesce((SELECT jsonb_object_agg(id::text,jsonb_build_object('slots',slots,'used',service_count)) FROM nodes),'{}'::jsonb),
        'service_usage',coalesce((SELECT jsonb_object_agg(node_id::text,count) FROM service_usage),'{}'::jsonb),
        'resident',coalesce((SELECT jsonb_object_agg(node_id::text,cost) FROM (SELECT node_id,jsonb_build_object('ram',sum(ram),'cpu',sum(cpu),'vcpu',sum(vcpu)) AS cost FROM resident GROUP BY node_id) u),'{}'::jsonb),
        'actual',coalesce((SELECT jsonb_object_agg(key,count) FROM actual_targets),'{}'::jsonb)
    ) AS data FROM policy p CROSS JOIN totals t CROSS JOIN required r CROSS JOIN shape sh
) SELECT data FROM projection;
$$;
-- +goose StatementEnd
DROP TABLE migration_sources;
DROP FUNCTION guard_migration_source();
DROP FUNCTION migration_source_node_usage(uuid,boolean);
