-- filename: 20261104215117167_instance_booked_cpu.sql
-- ADR-20261101160231 step 5b: an app on schedd's guest_vcpu_from_cpu_apps list
-- books its plan's CPU baseline (1,000 m per GB of plan RAM: 250 m Free,
-- 500 m Hobby, 1,000 m Pro, 2,000 m Scale) instead of its configured
-- cpu_millicores. schedd sets the booking in the admitting transaction
-- (faas.instance_booked_cpu_millicores); capture_instance_capacity stores it on
-- the new row, and the node guard, capacity capture and migration usage read
-- it before the app's value. NULL, every row today and every row an older
-- schedd writes, books exactly as before. The startup boost still books one
-- core while it lasts.
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE instances ADD COLUMN IF NOT EXISTS booked_cpu_millicores integer;
-- New rows are checked now; existing rows are all NULL. A later migration
-- validates it.
ALTER TABLE instances ADD CONSTRAINT instances_booked_cpu_millicores_check
    CHECK (booked_cpu_millicores IS NULL OR booked_cpu_millicores BETWEEN 1 AND 2000) NOT VALID;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.admission_reserve_node(p_node_id uuid, p_expected_generation bigint, p_ram_mb integer, p_counts boolean, p_overhead_mb integer, p_reservation_percent integer, p_vcpu integer, p_quiet_vcpu integer, p_cpu_millicores integer, p_quiet_cpu_millicores integer, p_default_cpu_millicores integer, p_cpu_overcommit integer) RETURNS void
    LANGUAGE plpgsql
    AS $$
DECLARE
    node_lifecycle text;
    node_generation bigint;
    ceiling_mb bigint;
    cap_mb bigint;
    used_mb bigint;
    resident_mb bigint;
    node_vcpu_budget bigint;
    node_cpu_budget bigint;
    used_vcpu bigint;
    used_cpu bigint;
    quiet_vcpu bigint;
    quiet_cpu bigint;
BEGIN
    PERFORM node_admission_lock_capacity();
    SELECT lifecycle::text, lifecycle_generation INTO node_lifecycle, node_generation
      FROM compute_nodes WHERE id = p_node_id FOR SHARE;
    IF NOT FOUND OR node_lifecycle NOT IN ('active', 'recovering')
       OR (p_expected_generation IS NOT NULL AND node_generation <> p_expected_generation) THEN
        RAISE EXCEPTION 'node placement is stale or not admitting'
            USING ERRCODE = '23514', CONSTRAINT = 'instance_node_admission_current';
    END IF;
    IF NOT p_counts THEN
        RETURN;
    END IF;
    PERFORM pg_advisory_xact_lock(1852793957, hashtext(p_node_id::text));
    SELECT n.admission_ceiling_mb::bigint,
           coalesce((SELECT sum(i.ram_mb + p_overhead_mb)
                       FROM instances i
                      WHERE i.node_id = n.id
                        AND i.state IN ('waking','cold_booting','running','draining','warm','snapshotting')), 0)::bigint
      INTO ceiling_mb, used_mb
      FROM compute_nodes n WHERE n.id = p_node_id;
    IF NOT FOUND THEN
        RETURN;
    END IF;
    SELECT (coalesce(sum(greatest(0, i.capacity_ram_mb - i.ram_mb - p.overhead_mb)), 0)
            + (SELECT ram FROM migration_source_node_usage(p_node_id, true)))::bigint
      INTO resident_mb
      FROM instances i
      CROSS JOIN service_capacity_policy p
     WHERE p.singleton
       AND (EXISTS (SELECT 1 FROM migration_admissions r WHERE r.phase = 'committed' AND r.instance_id = i.id
                       AND r.destination_node_id = i.node_id AND r.destination_wake_id = i.wake_id)
            OR EXISTS (SELECT 1 FROM migration_sources s WHERE s.phase = 'resumed' AND s.instance_id = i.id
                       AND s.source_node_id = i.node_id AND s.source_wake_id = i.wake_id))
       AND i.node_id = p_node_id
       AND i.state IN ('waking','cold_booting','running','draining','warm');
    used_mb := used_mb + resident_mb;
    cap_mb := ceiling_mb * greatest(100, least(coalesce(p_reservation_percent, 100), 200)) / 100;
    IF ceiling_mb > 0 AND used_mb + p_ram_mb + p_overhead_mb > cap_mb THEN
        RAISE EXCEPTION 'node RAM headroom exhausted'
            USING ERRCODE = '23514', CONSTRAINT = 'instance_node_capacity',
                  DETAIL = format('used_mb=%s admit_mb=%s ceiling_mb=%s cap_mb=%s',
                                  used_mb, p_ram_mb + p_overhead_mb, ceiling_mb, cap_mb);
    END IF;
    IF coalesce(p_vcpu, 0) <= 0 AND coalesce(p_cpu_millicores, 0) <= 0 THEN
        RETURN;
    END IF;
    -- The same live-instance sums placement reads (ComputeNodeUsedVCPUByNode,
    -- ComputeNodeUsedCPUMillicoresByNode without migration reservations), so
    -- this check never refuses a node placement found room on.
    SELECT n.vcpu_budget::bigint, n.vpcpus::bigint * 1000 * greatest(coalesce(p_cpu_overcommit, 1), 1),
           coalesce((SELECT sum(i.capacity_vcpu)
                       FROM instances i
                      WHERE i.node_id = n.id
                        AND i.state IN ('waking','cold_booting','running','draining','warm')), 0)::bigint,
           coalesce((SELECT sum(CASE WHEN i.startup_cpu_boost_until > now()
                                     THEN greatest(coalesce(i.booked_cpu_millicores, CASE WHEN a.cpu_millicores > 0 THEN a.cpu_millicores ELSE p_default_cpu_millicores END),
                                                   p_default_cpu_millicores)
                                     ELSE coalesce(i.booked_cpu_millicores, CASE WHEN a.cpu_millicores > 0 THEN a.cpu_millicores ELSE p_default_cpu_millicores END) END)
                       FROM instances i JOIN apps a ON a.id = i.app_id
                      WHERE i.node_id = n.id
                        AND i.state IN ('waking','cold_booting','running','draining','warm')), 0)::bigint
      INTO node_vcpu_budget, node_cpu_budget, used_vcpu, used_cpu
      FROM compute_nodes n WHERE n.id = p_node_id;
    -- A discount is the scheduler's measurement: quiet-paused instances, and
    -- since ADR 20261029151111 measured instances' unused CPU. It never lets
    -- bookings pass api.MeasuredCPUMaxOvercommitPercent (700) of a budget.
    quiet_vcpu := least(greatest(coalesce(p_quiet_vcpu, 0), 0), node_vcpu_budget * 6);
    IF p_vcpu > 0 AND node_vcpu_budget > 0 AND used_vcpu - quiet_vcpu + p_vcpu > node_vcpu_budget THEN
        RAISE EXCEPTION 'node vCPU headroom exhausted'
            USING ERRCODE = '23514', CONSTRAINT = 'instance_node_capacity',
                  DETAIL = format('resource=vcpu used_vcpu=%s quiet_vcpu=%s admit_vcpu=%s budget_vcpu=%s',
                                  used_vcpu, quiet_vcpu, p_vcpu, node_vcpu_budget);
    END IF;
    quiet_cpu := least(greatest(coalesce(p_quiet_cpu_millicores, 0), 0), node_cpu_budget * 6);
    IF p_cpu_millicores > 0 AND node_cpu_budget > 0 AND used_cpu - quiet_cpu + p_cpu_millicores > node_cpu_budget THEN
        RAISE EXCEPTION 'node CPU headroom exhausted'
            USING ERRCODE = '23514', CONSTRAINT = 'instance_node_capacity',
                  DETAIL = format('resource=cpu used_cpu_millicores=%s quiet_cpu_millicores=%s admit_cpu_millicores=%s budget_cpu_millicores=%s',
                                  used_cpu, quiet_cpu, p_cpu_millicores, node_cpu_budget);
    END IF;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.capture_instance_capacity() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE p service_capacity_policy%ROWTYPE; main_cpu integer; guest_vcpu integer; side_ram bigint; side_cpu bigint;
BEGIN
    IF TG_OP='UPDATE' THEN
        NEW.capacity_ram_mb:=greatest(OLD.capacity_ram_mb,NEW.capacity_ram_mb);
        NEW.capacity_cpu_millicores:=greatest(OLD.capacity_cpu_millicores,NEW.capacity_cpu_millicores);
        NEW.capacity_vcpu:=greatest(OLD.capacity_vcpu,NEW.capacity_vcpu);
        RETURN NEW;
    END IF;
    -- ADR-20261101160231 step 5b: the scheduler's booking for this instance,
    -- set in the inserting transaction; NULL keeps the app's cpu_millicores.
    NEW.booked_cpu_millicores:=coalesce(NEW.booked_cpu_millicores,
        nullif(current_setting('faas.instance_booked_cpu_millicores',true),'')::integer);
    SELECT * INTO STRICT p FROM service_capacity_policy WHERE singleton;
    SELECT greatest(a.cpu_millicores,p.startup_cpu),coalesce((p.plan_vcpus->>ac.plan)::integer,4)
      INTO main_cpu,guest_vcpu FROM apps a JOIN app_resource_accounts ac ON ac.app_id=a.id WHERE a.id=NEW.app_id;
    SELECT coalesce(sum(greatest(0,(sc->>'ram_mb')::integer)),0),
           coalesce(sum(greatest(0,coalesce(nullif(greatest(0,(sc->>'cpu_millicores')::integer),0),p.startup_cpu))),0)
      INTO side_ram,side_cpu FROM deployments d,
        LATERAL jsonb_array_elements(coalesce(d.sidecars,'[]'::jsonb)) sc WHERE d.id=NEW.deployment_id;
    NEW.capacity_ram_mb:=greatest(NEW.capacity_ram_mb,NEW.ram_mb+p.overhead_mb+side_ram);
    NEW.capacity_cpu_millicores:=greatest(NEW.capacity_cpu_millicores,coalesce(NEW.booked_cpu_millicores,main_cpu,p.startup_cpu)+side_cpu);
    NEW.capacity_vcpu:=greatest(NEW.capacity_vcpu,coalesce(guest_vcpu,1));
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.migration_admission_node_usage(target uuid, omit_instance uuid DEFAULT NULL::uuid) RETURNS TABLE(ram bigint, cpu bigint, vcpu bigint)
    LANGUAGE sql
    AS $$
 SELECT coalesce(sum(u.ram),0)::bigint,coalesce(sum(u.cpu),0)::bigint,coalesce(sum(u.vcpu),0)::bigint FROM (
  SELECT greatest(i.capacity_ram_mb,i.ram_mb+p.overhead_mb)::bigint AS ram,
   greatest(i.capacity_cpu_millicores,coalesce(i.booked_cpu_millicores,nullif(a.cpu_millicores,0),p.startup_cpu))::bigint AS cpu,
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
CREATE OR REPLACE FUNCTION public.migration_source_node_usage(target uuid, legacy boolean DEFAULT false) RETURNS TABLE(ram bigint, cpu bigint, vcpu bigint)
    LANGUAGE sql
    AS $$
 SELECT original.ram+extra.ram,original.cpu+extra.cpu,original.vcpu+extra.vcpu
 FROM (
SELECT coalesce(sum(CASE WHEN i.id IS NULL THEN r.ram_mb ELSE greatest(0,r.ram_mb-greatest(i.ram_mb+p.overhead_mb,CASE WHEN NOT legacy OR owned.present THEN i.capacity_ram_mb ELSE 0 END)) END),0)::bigint,
  coalesce(sum(CASE WHEN i.id IS NULL THEN r.cpu_millicores ELSE greatest(0,r.cpu_millicores-greatest(coalesce(i.booked_cpu_millicores,nullif(a.cpu_millicores,0),p.startup_cpu),CASE WHEN NOT legacy OR owned.present THEN i.capacity_cpu_millicores ELSE CASE WHEN i.startup_cpu_boost_until>now() THEN p.startup_cpu ELSE 0 END END)) END),0)::bigint,
  coalesce(sum(CASE WHEN i.id IS NULL THEN r.vcpu ELSE greatest(0,r.vcpu-i.capacity_vcpu) END),0)::bigint
 FROM migration_sources r CROSS JOIN service_capacity_policy p
 LEFT JOIN instances i ON r.phase='bound' AND i.id=r.instance_id AND i.node_id=r.source_node_id AND i.wake_id=r.source_wake_id
  AND (i.state IN ('waking','cold_booting','running','draining','warm') OR NOT legacy AND i.state IN ('snapshotting','migrating','frozen'))
 LEFT JOIN apps a ON a.id=i.app_id
 LEFT JOIN LATERAL (SELECT EXISTS(SELECT 1 FROM migration_admissions d WHERE d.phase='committed' AND d.instance_id=i.id AND d.destination_node_id=i.node_id AND d.destination_wake_id=i.wake_id) OR EXISTS(SELECT 1 FROM migration_sources s WHERE s.phase='resumed' AND s.instance_id=i.id AND s.source_node_id=i.node_id AND s.source_wake_id=i.wake_id) AS present) owned ON true
 WHERE p.singleton AND r.source_node_id=target AND r.phase IN ('bound','retiring')
 ) original(ram,cpu,vcpu) CROSS JOIN (
 SELECT coalesce(sum(ram),0)::bigint AS ram,coalesce(sum(cpu),0)::bigint AS cpu,coalesce(sum(vcpu),0)::bigint AS vcpu
 FROM function_migration_pending_node_usage(target)
 ) extra;
$$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.admission_reserve_node(p_node_id uuid, p_expected_generation bigint, p_ram_mb integer, p_counts boolean, p_overhead_mb integer, p_reservation_percent integer, p_vcpu integer, p_quiet_vcpu integer, p_cpu_millicores integer, p_quiet_cpu_millicores integer, p_default_cpu_millicores integer, p_cpu_overcommit integer) RETURNS void
    LANGUAGE plpgsql
    AS $$
DECLARE
    node_lifecycle text;
    node_generation bigint;
    ceiling_mb bigint;
    cap_mb bigint;
    used_mb bigint;
    resident_mb bigint;
    node_vcpu_budget bigint;
    node_cpu_budget bigint;
    used_vcpu bigint;
    used_cpu bigint;
    quiet_vcpu bigint;
    quiet_cpu bigint;
BEGIN
    PERFORM node_admission_lock_capacity();
    SELECT lifecycle::text, lifecycle_generation INTO node_lifecycle, node_generation
      FROM compute_nodes WHERE id = p_node_id FOR SHARE;
    IF NOT FOUND OR node_lifecycle NOT IN ('active', 'recovering')
       OR (p_expected_generation IS NOT NULL AND node_generation <> p_expected_generation) THEN
        RAISE EXCEPTION 'node placement is stale or not admitting'
            USING ERRCODE = '23514', CONSTRAINT = 'instance_node_admission_current';
    END IF;
    IF NOT p_counts THEN
        RETURN;
    END IF;
    PERFORM pg_advisory_xact_lock(1852793957, hashtext(p_node_id::text));
    SELECT n.admission_ceiling_mb::bigint,
           coalesce((SELECT sum(i.ram_mb + p_overhead_mb)
                       FROM instances i
                      WHERE i.node_id = n.id
                        AND i.state IN ('waking','cold_booting','running','draining','warm','snapshotting')), 0)::bigint
      INTO ceiling_mb, used_mb
      FROM compute_nodes n WHERE n.id = p_node_id;
    IF NOT FOUND THEN
        RETURN;
    END IF;
    SELECT (coalesce(sum(greatest(0, i.capacity_ram_mb - i.ram_mb - p.overhead_mb)), 0)
            + (SELECT ram FROM migration_source_node_usage(p_node_id, true)))::bigint
      INTO resident_mb
      FROM instances i
      CROSS JOIN service_capacity_policy p
     WHERE p.singleton
       AND (EXISTS (SELECT 1 FROM migration_admissions r WHERE r.phase = 'committed' AND r.instance_id = i.id
                       AND r.destination_node_id = i.node_id AND r.destination_wake_id = i.wake_id)
            OR EXISTS (SELECT 1 FROM migration_sources s WHERE s.phase = 'resumed' AND s.instance_id = i.id
                       AND s.source_node_id = i.node_id AND s.source_wake_id = i.wake_id))
       AND i.node_id = p_node_id
       AND i.state IN ('waking','cold_booting','running','draining','warm');
    used_mb := used_mb + resident_mb;
    cap_mb := ceiling_mb * greatest(100, least(coalesce(p_reservation_percent, 100), 200)) / 100;
    IF ceiling_mb > 0 AND used_mb + p_ram_mb + p_overhead_mb > cap_mb THEN
        RAISE EXCEPTION 'node RAM headroom exhausted'
            USING ERRCODE = '23514', CONSTRAINT = 'instance_node_capacity',
                  DETAIL = format('used_mb=%s admit_mb=%s ceiling_mb=%s cap_mb=%s',
                                  used_mb, p_ram_mb + p_overhead_mb, ceiling_mb, cap_mb);
    END IF;
    IF coalesce(p_vcpu, 0) <= 0 AND coalesce(p_cpu_millicores, 0) <= 0 THEN
        RETURN;
    END IF;
    -- The same live-instance sums placement reads (ComputeNodeUsedVCPUByNode,
    -- ComputeNodeUsedCPUMillicoresByNode without migration reservations), so
    -- this check never refuses a node placement found room on.
    SELECT n.vcpu_budget::bigint, n.vpcpus::bigint * 1000 * greatest(coalesce(p_cpu_overcommit, 1), 1),
           coalesce((SELECT sum(i.capacity_vcpu)
                       FROM instances i
                      WHERE i.node_id = n.id
                        AND i.state IN ('waking','cold_booting','running','draining','warm')), 0)::bigint,
           coalesce((SELECT sum(CASE WHEN i.startup_cpu_boost_until > now()
                                     THEN greatest(CASE WHEN a.cpu_millicores > 0 THEN a.cpu_millicores ELSE p_default_cpu_millicores END,
                                                   p_default_cpu_millicores)
                                     ELSE CASE WHEN a.cpu_millicores > 0 THEN a.cpu_millicores ELSE p_default_cpu_millicores END END)
                       FROM instances i JOIN apps a ON a.id = i.app_id
                      WHERE i.node_id = n.id
                        AND i.state IN ('waking','cold_booting','running','draining','warm')), 0)::bigint
      INTO node_vcpu_budget, node_cpu_budget, used_vcpu, used_cpu
      FROM compute_nodes n WHERE n.id = p_node_id;
    -- A discount is the scheduler's measurement: quiet-paused instances, and
    -- since ADR 20261029151111 measured instances' unused CPU. It never lets
    -- bookings pass api.MeasuredCPUMaxOvercommitPercent (700) of a budget.
    quiet_vcpu := least(greatest(coalesce(p_quiet_vcpu, 0), 0), node_vcpu_budget * 6);
    IF p_vcpu > 0 AND node_vcpu_budget > 0 AND used_vcpu - quiet_vcpu + p_vcpu > node_vcpu_budget THEN
        RAISE EXCEPTION 'node vCPU headroom exhausted'
            USING ERRCODE = '23514', CONSTRAINT = 'instance_node_capacity',
                  DETAIL = format('resource=vcpu used_vcpu=%s quiet_vcpu=%s admit_vcpu=%s budget_vcpu=%s',
                                  used_vcpu, quiet_vcpu, p_vcpu, node_vcpu_budget);
    END IF;
    quiet_cpu := least(greatest(coalesce(p_quiet_cpu_millicores, 0), 0), node_cpu_budget * 6);
    IF p_cpu_millicores > 0 AND node_cpu_budget > 0 AND used_cpu - quiet_cpu + p_cpu_millicores > node_cpu_budget THEN
        RAISE EXCEPTION 'node CPU headroom exhausted'
            USING ERRCODE = '23514', CONSTRAINT = 'instance_node_capacity',
                  DETAIL = format('resource=cpu used_cpu_millicores=%s quiet_cpu_millicores=%s admit_cpu_millicores=%s budget_cpu_millicores=%s',
                                  used_cpu, quiet_cpu, p_cpu_millicores, node_cpu_budget);
    END IF;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.capture_instance_capacity() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
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
      INTO main_cpu,guest_vcpu FROM apps a JOIN app_resource_accounts ac ON ac.app_id=a.id WHERE a.id=NEW.app_id;
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

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.migration_admission_node_usage(target uuid, omit_instance uuid DEFAULT NULL::uuid) RETURNS TABLE(ram bigint, cpu bigint, vcpu bigint)
    LANGUAGE sql
    AS $$
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
CREATE OR REPLACE FUNCTION public.migration_source_node_usage(target uuid, legacy boolean DEFAULT false) RETURNS TABLE(ram bigint, cpu bigint, vcpu bigint)
    LANGUAGE sql
    AS $$
 SELECT original.ram+extra.ram,original.cpu+extra.cpu,original.vcpu+extra.vcpu
 FROM (
SELECT coalesce(sum(CASE WHEN i.id IS NULL THEN r.ram_mb ELSE greatest(0,r.ram_mb-greatest(i.ram_mb+p.overhead_mb,CASE WHEN NOT legacy OR owned.present THEN i.capacity_ram_mb ELSE 0 END)) END),0)::bigint,
  coalesce(sum(CASE WHEN i.id IS NULL THEN r.cpu_millicores ELSE greatest(0,r.cpu_millicores-greatest(coalesce(nullif(a.cpu_millicores,0),p.startup_cpu),CASE WHEN NOT legacy OR owned.present THEN i.capacity_cpu_millicores ELSE CASE WHEN i.startup_cpu_boost_until>now() THEN p.startup_cpu ELSE 0 END END)) END),0)::bigint,
  coalesce(sum(CASE WHEN i.id IS NULL THEN r.vcpu ELSE greatest(0,r.vcpu-i.capacity_vcpu) END),0)::bigint
 FROM migration_sources r CROSS JOIN service_capacity_policy p
 LEFT JOIN instances i ON r.phase='bound' AND i.id=r.instance_id AND i.node_id=r.source_node_id AND i.wake_id=r.source_wake_id
  AND (i.state IN ('waking','cold_booting','running','draining','warm') OR NOT legacy AND i.state IN ('snapshotting','migrating','frozen'))
 LEFT JOIN apps a ON a.id=i.app_id
 LEFT JOIN LATERAL (SELECT EXISTS(SELECT 1 FROM migration_admissions d WHERE d.phase='committed' AND d.instance_id=i.id AND d.destination_node_id=i.node_id AND d.destination_wake_id=i.wake_id) OR EXISTS(SELECT 1 FROM migration_sources s WHERE s.phase='resumed' AND s.instance_id=i.id AND s.source_node_id=i.node_id AND s.source_wake_id=i.wake_id) AS present) owned ON true
 WHERE p.singleton AND r.source_node_id=target AND r.phase IN ('bound','retiring')
 ) original(ram,cpu,vcpu) CROSS JOIN (
 SELECT coalesce(sum(ram),0)::bigint AS ram,coalesce(sum(cpu),0)::bigint AS cpu,coalesce(sum(vcpu),0)::bigint AS vcpu
 FROM function_migration_pending_node_usage(target)
 ) extra;
$$;
-- +goose StatementEnd

ALTER TABLE instances DROP CONSTRAINT IF EXISTS instances_booked_cpu_millicores_check;
ALTER TABLE instances DROP COLUMN IF EXISTS booked_cpu_millicores;
