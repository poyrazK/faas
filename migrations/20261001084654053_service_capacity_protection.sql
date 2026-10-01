-- filename: 20261001084654053_service_capacity_protection.sql
-- ADR-422: preserve bare-metal service recovery capacity at the database boundary.
-- +goose Up
CREATE TABLE IF NOT EXISTS service_capacity_policy (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    enabled boolean NOT NULL DEFAULT false,
    overhead_mb integer NOT NULL DEFAULT 8 CHECK (overhead_mb > 0),
    cpu_overcommit integer NOT NULL DEFAULT 8 CHECK (cpu_overcommit > 0),
    startup_cpu integer NOT NULL DEFAULT 1000 CHECK (startup_cpu > 0),
    heartbeat_seconds integer NOT NULL DEFAULT 90 CHECK (heartbeat_seconds > 0),
    plan_vcpus jsonb NOT NULL DEFAULT '{"free":2,"hobby":2,"pro":2,"scale":4}'
);
INSERT INTO service_capacity_policy(singleton) VALUES (true) ON CONFLICT DO NOTHING;

ALTER TABLE instances ADD COLUMN IF NOT EXISTS capacity_ram_mb bigint NOT NULL DEFAULT 0 CHECK (capacity_ram_mb>=0);
ALTER TABLE instances ADD COLUMN IF NOT EXISTS capacity_cpu_millicores bigint NOT NULL DEFAULT 0 CHECK (capacity_cpu_millicores>=0);
ALTER TABLE instances ADD COLUMN IF NOT EXISTS capacity_vcpu integer NOT NULL DEFAULT 0 CHECK (capacity_vcpu>=0);

-- Legacy guest vCPU may predate a plan downgrade. Reserve the largest plan's
-- topology until those instances are released. New admissions capture the
-- exact plan and immutable deployment shape below.
UPDATE instances i SET capacity_ram_mb=i.ram_mb+p.overhead_mb+coalesce((
           SELECT sum(greatest(0,(sc->>'ram_mb')::integer)) FROM deployments d,
             LATERAL jsonb_array_elements(coalesce(d.sidecars,'[]'::jsonb)) sc WHERE d.id=i.deployment_id),0),
       capacity_cpu_millicores=p.startup_cpu+coalesce((
           SELECT sum(greatest(0,coalesce(nullif(greatest(0,(sc->>'cpu_millicores')::integer),0),p.startup_cpu))) FROM deployments d,
             LATERAL jsonb_array_elements(coalesce(d.sidecars,'[]'::jsonb)) sc WHERE d.id=i.deployment_id),0),
       capacity_vcpu=CASE WHEN i.app_id IS NULL THEN 1 ELSE (SELECT max(value::integer) FROM jsonb_each_text(p.plan_vcpus)) END
  FROM service_capacity_policy p
 WHERE p.singleton AND (i.capacity_ram_mb=0 OR i.capacity_cpu_millicores=0 OR i.capacity_vcpu=0);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION capture_instance_capacity() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE p service_capacity_policy%ROWTYPE; main_cpu integer; guest_vcpu integer; side_ram bigint; side_cpu bigint;
BEGIN
    IF TG_OP='UPDATE' THEN
        NEW.capacity_ram_mb:=greatest(OLD.capacity_ram_mb,NEW.capacity_ram_mb);
        NEW.capacity_cpu_millicores:=greatest(OLD.capacity_cpu_millicores,NEW.capacity_cpu_millicores);
        NEW.capacity_vcpu:=greatest(OLD.capacity_vcpu,NEW.capacity_vcpu);
        RETURN NEW;
    END IF;
    SELECT * INTO STRICT p FROM service_capacity_policy WHERE singleton;
    SELECT greatest(a.cpu_millicores,p.startup_cpu),coalesce((p.plan_vcpus->>ac.plan)::integer,4)
      INTO main_cpu,guest_vcpu FROM apps a JOIN accounts ac ON ac.id=a.account_id WHERE a.id=NEW.app_id;
    SELECT coalesce(sum(greatest(0,(sc->>'ram_mb')::integer)),0),
           coalesce(sum(greatest(0,coalesce(nullif(greatest(0,(sc->>'cpu_millicores')::integer),0),p.startup_cpu))),0)
      INTO side_ram,side_cpu FROM deployments d,
        LATERAL jsonb_array_elements(coalesce(d.sidecars,'[]'::jsonb)) sc WHERE d.id=NEW.deployment_id;
    NEW.capacity_ram_mb:=greatest(NEW.capacity_ram_mb,NEW.ram_mb+p.overhead_mb+side_ram);
    NEW.capacity_cpu_millicores:=greatest(NEW.capacity_cpu_millicores,coalesce(main_cpu,p.startup_cpu)+side_cpu);
    NEW.capacity_vcpu:=greatest(NEW.capacity_vcpu,coalesce(guest_vcpu,1));
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS capture_instance_capacity ON instances;
CREATE TRIGGER capture_instance_capacity BEFORE INSERT OR UPDATE OF capacity_ram_mb,capacity_cpu_millicores,capacity_vcpu ON instances FOR EACH ROW EXECUTE FUNCTION capture_instance_capacity();

-- This snapshot includes internal admission inputs. Only the typed, aggregate
-- projection is exposed by apid; raw app IDs never leave the operator store.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION service_capacity_snapshot() RETURNS jsonb
LANGUAGE sql VOLATILE AS $$
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

-- The exclusive singleton lock serializes protected writes across accounts,
-- schedulers and direct SQL. Disabled writers take a shared lock so enabling
-- cannot race a statement that began with protection disabled.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION service_capacity_before_write() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE is_enabled boolean; baseline_key text;
BEGIN
    LOOP
        SELECT enabled INTO is_enabled FROM service_capacity_policy WHERE singleton AND enabled FOR UPDATE;
        EXIT WHEN FOUND;
        SELECT enabled INTO is_enabled FROM service_capacity_policy WHERE singleton AND NOT enabled FOR SHARE;
        EXIT WHEN FOUND;
        IF NOT EXISTS (SELECT 1 FROM service_capacity_policy WHERE singleton) THEN
            RAISE EXCEPTION 'service capacity policy is missing';
        END IF;
    END LOOP;
    IF TG_NARGS>0 AND TG_ARGV[0]='lock_only' THEN RETURN NULL; END IF;
    baseline_key := 'gregale.capacity_' || TG_TABLE_NAME || '_' || pg_trigger_depth()::text;
    PERFORM set_config(baseline_key,CASE WHEN is_enabled THEN service_capacity_snapshot()::text ELSE '' END,true);
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION service_capacity_after_write() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE before_text text; prior jsonb; next jsonb; k text; value jsonb; grew boolean := false; excess boolean; physical_growth boolean:=false; service_growth boolean:=false;
BEGIN
    before_text := current_setting('gregale.capacity_' || TG_TABLE_NAME || '_' || pg_trigger_depth()::text,true);
    IF coalesce(before_text,'')='' THEN RETURN NULL; END IF;
    prior:=before_text::jsonb; next:=service_capacity_snapshot();
    FOR k,value IN SELECT * FROM jsonb_each(next->'demands') LOOP
        IF (value->>'count')::bigint>0 AND (
            NOT (prior->'demands' ? k) OR
            (value->>'count')::bigint>coalesce((prior->'demands'->k->>'count')::bigint,0) OR
            (value->>'ram')::bigint>(prior->'demands'->k->>'ram')::bigint OR
            (value->>'cpu')::bigint>(prior->'demands'->k->>'cpu')::bigint OR
            (value->>'vcpu')::bigint>(prior->'demands'->k->>'vcpu')::bigint) THEN grew:=true; END IF;
    END LOOP;
    FOR k,value IN SELECT * FROM jsonb_each(next->'resident') LOOP
        IF (value->>'ram')::bigint>coalesce((prior->'resident'->k->>'ram')::bigint,0) OR
           (value->>'cpu')::bigint>coalesce((prior->'resident'->k->>'cpu')::bigint,0) OR
           (value->>'vcpu')::bigint>coalesce((prior->'resident'->k->>'vcpu')::bigint,0) THEN
            physical_growth:=true;
            IF NOT (next->'other' ? k) THEN
                RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='service_capacity_protection',MESSAGE='node is not ready for protected admission';
            END IF;
            IF (next->'other'->k->>'ram')::bigint>coalesce((prior->'other'->k->>'ram')::bigint,0) OR
               (next->'other'->k->>'cpu')::bigint>coalesce((prior->'other'->k->>'cpu')::bigint,0) OR
               (next->'other'->k->>'vcpu')::bigint>coalesce((prior->'other'->k->>'vcpu')::bigint,0) THEN grew:=true; END IF;
        END IF;
    END LOOP;
    -- Instance role changes cannot remove declarations. Ordinary growth from
    -- these writes must preserve headroom; removing app/deployment intent may
    -- reclassify resident guests without blocking stops.
    IF TG_TABLE_NAME='instances' THEN
        FOR k,value IN SELECT * FROM jsonb_each(next->'other') LOOP
            IF (value->>'ram')::bigint>coalesce((prior->'other'->k->>'ram')::bigint,0) OR
               (value->>'cpu')::bigint>coalesce((prior->'other'->k->>'cpu')::bigint,0) OR
               (value->>'vcpu')::bigint>coalesce((prior->'other'->k->>'vcpu')::bigint,0) THEN grew:=true; END IF;
        END LOOP;
    END IF;
    -- Reclassification can occupy service slots without allocating resources.
    -- Include ineligible hosts here, even though placement excludes them.
    FOR k,value IN SELECT * FROM jsonb_each(next->'service_usage') LOOP
        IF value::bigint>coalesce((prior->'service_usage'->>k)::bigint,0) THEN
            service_growth:=true;
            IF NOT (next->'placement' ? k) THEN
                RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='service_capacity_protection',MESSAGE='node is not ready for protected admission';
            END IF;
        END IF;
    END LOOP;
    IF (next->>'replica_ram_mb')::bigint>(prior->>'replica_ram_mb')::bigint OR
       (next->>'replica_cpu_millicores')::bigint>(prior->>'replica_cpu_millicores')::bigint OR
       (next->>'replica_vcpu')::bigint>(prior->>'replica_vcpu')::bigint THEN grew:=true; END IF;
    SELECT EXISTS (SELECT 1 FROM jsonb_each_text(next->'actual') a
                    WHERE a.value::bigint>coalesce((next->'demands'->a.key->>'count')::bigint,0)
                      AND a.value::bigint>coalesce((prior->'actual'->>a.key)::bigint,0)) INTO excess;
    -- Service recovery spends an existing declaration even after a host has
    -- failed. New intent, bursts, mirrors, jobs and excess service replicas
    -- must leave the fleet protected. Releases and unrelated updates pass.
    IF ((grew OR excess) AND next->>'state'<>'protected') OR
       (NOT (next->>'placements_fit')::boolean AND (physical_growth OR service_growth)) THEN
        RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='service_capacity_protection',
            MESSAGE='service admission would consume bare-metal recovery capacity';
    END IF;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
DECLARE table_name text; resource_columns text;
BEGIN
    FOREACH table_name IN ARRAY ARRAY['apps','deployments','instances'] LOOP
        resource_columns:=CASE table_name WHEN 'apps' THEN 'ram_mb,cpu_millicores,manifest,status,account_id' WHEN 'deployments' THEN 'status,scope,sidecars,app_id' ELSE 'state,ram_mb,node_id,app_id,deployment_id,mode,capacity_ram_mb,capacity_cpu_millicores,capacity_vcpu' END;
        EXECUTE format('DROP TRIGGER IF EXISTS service_capacity_before ON %I',table_name);
        EXECUTE format('CREATE TRIGGER service_capacity_before BEFORE INSERT OR UPDATE OF %s OR DELETE ON %I FOR EACH STATEMENT EXECUTE FUNCTION service_capacity_before_write()',resource_columns,table_name);
        EXECUTE format('DROP TRIGGER IF EXISTS service_capacity_after ON %I',table_name);
        EXECUTE format('CREATE TRIGGER service_capacity_after AFTER INSERT OR UPDATE OF %s OR DELETE ON %I FOR EACH STATEMENT EXECUTE FUNCTION service_capacity_after_write()',resource_columns,table_name);
    END LOOP;
END;
$$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS service_capacity_nodes_lock ON compute_nodes;
CREATE TRIGGER service_capacity_nodes_lock BEFORE INSERT OR UPDATE OF lifecycle,admission_ceiling_mb,vpcpus,vcpu_budget,last_heartbeat_at OR DELETE ON compute_nodes FOR EACH STATEMENT EXECUTE FUNCTION service_capacity_before_write('lock_only');
DROP TRIGGER IF EXISTS service_capacity_plan_before ON accounts;
CREATE TRIGGER service_capacity_plan_before BEFORE UPDATE OF plan ON accounts FOR EACH STATEMENT EXECUTE FUNCTION service_capacity_before_write();
DROP TRIGGER IF EXISTS service_capacity_plan_after ON accounts;
CREATE TRIGGER service_capacity_plan_after AFTER UPDATE OF plan ON accounts FOR EACH STATEMENT EXECUTE FUNCTION service_capacity_after_write();

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION set_service_capacity_protection(wanted boolean) RETURNS jsonb LANGUAGE plpgsql AS $$
DECLARE result jsonb;
BEGIN
    PERFORM 1 FROM service_capacity_policy WHERE singleton FOR UPDATE;
    UPDATE service_capacity_policy SET enabled=wanted WHERE singleton;
    result:=service_capacity_snapshot();
    IF wanted AND result->>'state'<>'protected' THEN
        RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='service_capacity_protection',
            MESSAGE='fleet cannot yet preserve service capacity after one host failure';
    END IF;
    RETURN result;
END;
$$;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER IF EXISTS capture_instance_capacity ON instances;
DROP FUNCTION IF EXISTS capture_instance_capacity();
DROP TRIGGER IF EXISTS service_capacity_nodes_lock ON compute_nodes;
DROP TRIGGER IF EXISTS service_capacity_plan_before ON accounts;
DROP TRIGGER IF EXISTS service_capacity_plan_after ON accounts;
DROP TRIGGER IF EXISTS service_capacity_before ON instances;
DROP TRIGGER IF EXISTS service_capacity_after ON instances;
DROP TRIGGER IF EXISTS service_capacity_before ON deployments;
DROP TRIGGER IF EXISTS service_capacity_after ON deployments;
DROP TRIGGER IF EXISTS service_capacity_before ON apps;
DROP TRIGGER IF EXISTS service_capacity_after ON apps;
DROP FUNCTION IF EXISTS set_service_capacity_protection(boolean);
DROP FUNCTION IF EXISTS service_capacity_before_write();
DROP FUNCTION IF EXISTS service_capacity_after_write();
DROP FUNCTION IF EXISTS service_capacity_snapshot();
ALTER TABLE instances DROP COLUMN IF EXISTS capacity_ram_mb;
ALTER TABLE instances DROP COLUMN IF EXISTS capacity_cpu_millicores;
ALTER TABLE instances DROP COLUMN IF EXISTS capacity_vcpu;
DROP TABLE IF EXISTS service_capacity_policy;
