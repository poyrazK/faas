-- filename: 20261109193043884_instance_booked_vcpu.sql
-- ADR-20261109172421 step 5: an instance's capacity_vcpu holds the guest vCPU
-- count it boots with. For an app whose CPU schedd shapes
-- (guest_vcpu_from_cpu_apps, cpu_size_apps: the apps it stamps a CPU booking
-- for), schedd sets the count in the admitting transaction
-- (faas.instance_booked_vcpu); capture_instance_capacity stores it on the new
-- row and books it instead of the plan's service_capacity_policy.plan_vcpus,
-- and migration admission usage reads it the same way. NULL, every row today
-- and every row an older schedd writes, keeps the plan's count.
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE instances ADD COLUMN IF NOT EXISTS booked_vcpu integer;
-- New rows are checked now; existing rows are all NULL. Guarded so the
-- migration is re-runnable.
-- +goose StatementBegin
DO $booked$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'instances'::regclass AND conname = 'instances_booked_vcpu_check') THEN
    ALTER TABLE instances ADD CONSTRAINT instances_booked_vcpu_check
      CHECK (booked_vcpu IS NULL OR booked_vcpu BETWEEN 1 AND 32) NOT VALID;
  END IF;
END
$booked$;
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
    -- ADR-20261109172421 step 5: the guest vCPU count the scheduler boots
    -- this instance with, set in the same transaction; NULL keeps the plan's.
    NEW.booked_vcpu:=coalesce(NEW.booked_vcpu,
        nullif(current_setting('faas.instance_booked_vcpu',true),'')::integer);
    SELECT * INTO STRICT p FROM service_capacity_policy WHERE singleton;
    SELECT greatest(a.cpu_millicores,p.startup_cpu),coalesce((p.plan_vcpus->>ac.plan)::integer,4)
      INTO main_cpu,guest_vcpu FROM apps a JOIN app_resource_accounts ac ON ac.app_id=a.id WHERE a.id=NEW.app_id;
    SELECT coalesce(sum(greatest(0,(sc->>'ram_mb')::integer)),0),
           coalesce(sum(greatest(0,coalesce(nullif(greatest(0,(sc->>'cpu_millicores')::integer),0),p.startup_cpu))),0)
      INTO side_ram,side_cpu FROM deployments d,
        LATERAL jsonb_array_elements(coalesce(d.sidecars,'[]'::jsonb)) sc WHERE d.id=NEW.deployment_id;
    NEW.capacity_ram_mb:=greatest(NEW.capacity_ram_mb,NEW.ram_mb+p.overhead_mb+side_ram);
    NEW.capacity_cpu_millicores:=greatest(NEW.capacity_cpu_millicores,coalesce(NEW.booked_cpu_millicores,main_cpu,p.startup_cpu)+side_cpu);
    NEW.capacity_vcpu:=greatest(NEW.capacity_vcpu,coalesce(NEW.booked_vcpu,guest_vcpu,1));
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
   greatest(i.capacity_vcpu,CASE WHEN i.app_id IS NULL THEN 1 ELSE coalesce(i.booked_vcpu,(p.plan_vcpus->>ac.plan)::integer,4) END)::bigint AS vcpu
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

-- +goose Down
SET LOCAL lock_timeout = '5s';
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

ALTER TABLE instances DROP CONSTRAINT IF EXISTS instances_booked_vcpu_check;
ALTER TABLE instances DROP COLUMN IF EXISTS booked_vcpu;
