-- filename: 20261017140000003_inbound_migration_reservations.sql
-- +goose Up

ALTER TABLE migration_leases ADD COLUMN IF NOT EXISTS resume_fenced boolean NOT NULL DEFAULT false;

CREATE TABLE IF NOT EXISTS migration_admissions (
 id uuid PRIMARY KEY,
 instance_id uuid NOT NULL,
 source_node_id uuid NOT NULL,
 destination_node_id uuid NOT NULL,
 source_wake_id uuid NOT NULL,
 destination_wake_id uuid NOT NULL,
 destination_generation bigint NOT NULL CHECK(destination_generation>0),
 lease_token text NOT NULL CHECK(lease_token<>''),
 mem_storage_key text NOT NULL CHECK(mem_storage_key<>''),
 vmstate_storage_key text NOT NULL CHECK(vmstate_storage_key<>''),
 request_digest text NOT NULL CHECK(request_digest ~ '^[0-9a-f]{64}$'),
 ram_mb integer NOT NULL CHECK(ram_mb>0),
 cpu_millicores integer NOT NULL CHECK(cpu_millicores>0),
 vcpu integer NOT NULL CHECK(vcpu>0),
 expires_at timestamptz NOT NULL,
 phase text NOT NULL DEFAULT 'reserved' CHECK(phase IN ('reserved','adopting','adopted','cleaning','cleaned','committed')),
 active_lease_token text GENERATED ALWAYS AS (CASE WHEN phase NOT IN ('cleaned','committed') THEN lease_token ELSE NULL END) STORED REFERENCES migration_leases(lease_token),
 adoption_receipt bytea NOT NULL DEFAULT '' CHECK(octet_length(adoption_receipt)<=65536),
 cleanup_proof text NOT NULL DEFAULT '' CHECK(octet_length(cleanup_proof)<=1024),
 CHECK(source_node_id<>destination_node_id AND source_wake_id<>destination_wake_id),
 CHECK(phase NOT IN ('adopted','committed') OR octet_length(adoption_receipt)>0),
 CHECK((phase='cleaned')=(cleanup_proof<>'')),
 UNIQUE(instance_id,lease_token,destination_node_id)
);
CREATE UNIQUE INDEX IF NOT EXISTS migration_admissions_one_pending ON migration_admissions(instance_id) WHERE phase NOT IN ('cleaned','committed');
CREATE INDEX IF NOT EXISTS migration_admissions_destination_pending ON migration_admissions(destination_node_id,id) WHERE phase NOT IN ('cleaned','committed');
CREATE INDEX IF NOT EXISTS migration_admissions_owned_wake ON migration_admissions(instance_id,destination_node_id,destination_wake_id) WHERE phase='committed';

-- +goose StatementBegin
DO $replay$
BEGIN
 IF to_regprocedure(format('%I.migration_admission_node_usage(uuid,uuid)',current_schema())) IS NULL THEN
CREATE FUNCTION migration_admission_node_usage(target uuid,omit_instance uuid DEFAULT NULL) RETURNS TABLE(ram bigint,cpu bigint,vcpu bigint) LANGUAGE sql AS $$
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
 END IF;
END
$replay$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $replay$
BEGIN
 IF to_regprocedure(format('%I.guard_migration_admission()',current_schema())) IS NULL THEN
CREATE FUNCTION guard_migration_admission() RETURNS trigger LANGUAGE plpgsql AS $$
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
 END IF;
END
$replay$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $replay$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid='migration_admissions'::regclass AND tgname='migration_admission_guard') THEN
CREATE TRIGGER migration_admission_guard BEFORE INSERT OR UPDATE OR DELETE ON migration_admissions FOR EACH ROW EXECUTE FUNCTION guard_migration_admission();
 END IF;
END
$replay$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $replay$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid='migration_admissions'::regclass AND tgname='service_capacity_before') THEN
CREATE TRIGGER service_capacity_before BEFORE INSERT OR UPDATE OR DELETE ON migration_admissions FOR EACH STATEMENT EXECUTE FUNCTION service_capacity_before_write();
 END IF;
END
$replay$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $replay$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid='migration_admissions'::regclass AND tgname='service_capacity_after') THEN
CREATE TRIGGER service_capacity_after AFTER INSERT OR UPDATE OR DELETE ON migration_admissions FOR EACH STATEMENT EXECUTE FUNCTION service_capacity_after_write();
 END IF;
END
$replay$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $replay$
BEGIN
 IF to_regprocedure(format('%I.guard_instance_migration_admission()',current_schema())) IS NULL THEN
CREATE FUNCTION guard_instance_migration_admission() RETURNS trigger LANGUAGE plpgsql AS $$
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
 END IF;
END
$replay$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $replay$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid='instances'::regclass AND tgname='instance_migration_admission_guard') THEN
CREATE TRIGGER instance_migration_admission_guard BEFORE INSERT OR DELETE OR UPDATE OF node_id,state,wake_id,lease_token,app_id,deployment_id,ram_mb,capacity_ram_mb,capacity_cpu_millicores,capacity_vcpu ON instances FOR EACH ROW EXECUTE FUNCTION guard_instance_migration_admission();
 END IF;
END
$replay$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $replay$
BEGIN
 IF to_regprocedure(format('%I.consume_migration_admission()',current_schema())) IS NULL THEN
CREATE FUNCTION consume_migration_admission() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE changed bigint;
BEGIN
 IF NEW.node_id IS DISTINCT FROM OLD.node_id AND EXISTS(
  SELECT 1 FROM migration_admissions WHERE instance_id=NEW.id AND source_node_id=OLD.node_id AND destination_node_id=NEW.node_id AND lease_token=NEW.lease_token
 ) THEN
  UPDATE migration_admissions SET phase='committed' WHERE instance_id=NEW.id AND source_node_id=OLD.node_id AND destination_node_id=NEW.node_id AND source_wake_id=OLD.wake_id AND destination_wake_id=NEW.wake_id AND lease_token=NEW.lease_token AND phase='adopted';
  GET DIAGNOSTICS changed = ROW_COUNT;
  IF changed<>1 THEN RAISE EXCEPTION 'migration admission was not consumed' USING ERRCODE='23514',CONSTRAINT='migration_admission_current'; END IF;
 END IF;
 RETURN NEW;
END $$;
 END IF;
END
$replay$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $replay$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid='instances'::regclass AND tgname='instance_migration_admission_consume') THEN
CREATE TRIGGER instance_migration_admission_consume AFTER UPDATE OF node_id ON instances FOR EACH ROW EXECUTE FUNCTION consume_migration_admission();
 END IF;
END
$replay$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $replay$
BEGIN
 IF to_regprocedure(format('%I.guard_node_migration_admissions()',current_schema())) IS NULL THEN
CREATE FUNCTION guard_node_migration_admissions() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (TG_OP='DELETE' OR NEW.lifecycle IN ('maintenance','retired')) AND EXISTS(SELECT 1 FROM migration_admissions WHERE destination_node_id=OLD.id AND phase NOT IN ('cleaned','committed')) THEN
  RAISE EXCEPTION 'node has unresolved incoming migrations' USING ERRCODE='23514',CONSTRAINT='migration_admission_current';
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END $$;
 END IF;
END
$replay$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $replay$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid='compute_nodes'::regclass AND tgname='node_migration_admission_guard') THEN
CREATE TRIGGER node_migration_admission_guard BEFORE UPDATE OF lifecycle OR DELETE ON compute_nodes FOR EACH ROW EXECUTE FUNCTION guard_node_migration_admissions();
 END IF;
END
$replay$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $replay$
BEGIN
 IF to_regprocedure(format('%I.guard_source_migration_admissions()',current_schema())) IS NULL THEN
CREATE FUNCTION guard_source_migration_admissions() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (TG_OP='DELETE' OR NEW.lease_token IS DISTINCT FROM OLD.lease_token) AND EXISTS(SELECT 1 FROM migration_admissions WHERE instance_id=OLD.instance_id AND lease_token=OLD.lease_token AND phase NOT IN ('cleaned','committed')) THEN
  RAISE EXCEPTION 'source lease has unresolved incoming migrations' USING ERRCODE='23514',CONSTRAINT='migration_admission_current';
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 IF NEW.lease_token IS DISTINCT FROM OLD.lease_token THEN NEW.resume_fenced=false; END IF;
 RETURN NEW;
END $$;
 END IF;
END
$replay$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $replay$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid='migration_leases'::regclass AND tgname='source_migration_admission_guard') THEN
CREATE TRIGGER source_migration_admission_guard BEFORE UPDATE OR DELETE ON migration_leases FOR EACH ROW EXECUTE FUNCTION guard_source_migration_admissions();
 END IF;
END
$replay$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $replay$
BEGIN
 IF to_regprocedure(format('%I.migration_source_node_usage(uuid,boolean)',current_schema())) IS NULL THEN
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
 END IF;
END
$replay$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN IF EXISTS(SELECT 1 FROM migration_admissions) THEN RAISE EXCEPTION 'migration admissions and receipts must be preserved'; END IF; END $$;
-- +goose StatementEnd
DROP TRIGGER source_migration_admission_guard ON migration_leases;
DROP TRIGGER node_migration_admission_guard ON compute_nodes;
DROP TRIGGER instance_migration_admission_consume ON instances;
DROP TRIGGER instance_migration_admission_guard ON instances;
DROP FUNCTION guard_source_migration_admissions();
DROP FUNCTION guard_node_migration_admissions();
DROP FUNCTION consume_migration_admission();
DROP FUNCTION guard_instance_migration_admission();
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
ALTER TABLE migration_leases DROP COLUMN resume_fenced;
DROP TABLE migration_admissions;
DROP FUNCTION guard_migration_admission();
DROP FUNCTION migration_admission_node_usage(uuid,uuid);
