-- filename: 20261110180538212_migration_capacity_memory_only.sql
-- ADR-20261109172421 step 6.2 (memory is the only capacity limit): live
-- and function migration admission refuse a destination on memory alone.
-- function_migration_check_capacity, guard_instance_migration_admission
-- and guard_migration_admission no longer compare CPU millicores or guest
-- vCPUs with the node's budgets; a node must still be sized and active, and
-- an admission must still cover its source's admitted shape.
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.function_migration_check_capacity(node uuid, extra_ram bigint, extra_cpu bigint, extra_vcpu bigint, exclude_id uuid) RETURNS void
    LANGUAGE plpgsql
    AS $$
DECLARE n compute_nodes%ROWTYPE; used_ram bigint; used_cpu bigint; used_vcpu bigint; p service_capacity_policy%ROWTYPE;
BEGIN
 PERFORM pg_advisory_xact_lock(1852793957,hashtext(node::text));
 SELECT * INTO n FROM compute_nodes WHERE id=node FOR SHARE NOWAIT;
 SELECT * INTO p FROM service_capacity_policy WHERE singleton;
 IF n.id IS NULL OR n.lifecycle<>'active' OR n.admission_ceiling_mb<=0 OR n.vpcpus<=0 OR n.vcpu_budget<=0 THEN
  RAISE EXCEPTION 'migration target is not sized and active' USING ERRCODE='23514',CONSTRAINT='function_migration_destination_capacity';
 END IF;
 SELECT ram,cpu,vcpu INTO used_ram,used_cpu,used_vcpu FROM migration_admission_node_usage(node,exclude_id);
 -- ADR-20261109172421: memory is the only capacity limit; CPU and vCPUs
 -- are measured (used_cpu, used_vcpu), never refused.
 IF used_ram+extra_ram>n.admission_ceiling_mb THEN
  RAISE EXCEPTION 'migration destination capacity is reserved' USING ERRCODE='23514',CONSTRAINT='function_migration_destination_capacity';
 END IF;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.guard_instance_migration_admission() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
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
 -- ADR-20261109172421: memory is the only capacity limit.
 IF (n.admission_ceiling_mb>0 AND used.ram+greatest(NEW.capacity_ram_mb,NEW.ram_mb+(SELECT overhead_mb FROM service_capacity_policy WHERE singleton))>n.admission_ceiling_mb) THEN
  RAISE EXCEPTION 'capacity reserved for incoming migration' USING ERRCODE='23514',CONSTRAINT='migration_admission_capacity';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.guard_migration_admission() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
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
  -- ADR-20261109172421: memory is the only capacity limit.
  IF (n.admission_ceiling_mb>0 AND used.ram+NEW.ram_mb>n.admission_ceiling_mb) THEN
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

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.function_migration_check_capacity(node uuid, extra_ram bigint, extra_cpu bigint, extra_vcpu bigint, exclude_id uuid) RETURNS void
    LANGUAGE plpgsql
    AS $$
DECLARE n compute_nodes%ROWTYPE; used_ram bigint; used_cpu bigint; used_vcpu bigint; p service_capacity_policy%ROWTYPE;
BEGIN
 PERFORM pg_advisory_xact_lock(1852793957,hashtext(node::text));
 SELECT * INTO n FROM compute_nodes WHERE id=node FOR SHARE NOWAIT;
 SELECT * INTO p FROM service_capacity_policy WHERE singleton;
 IF n.id IS NULL OR n.lifecycle<>'active' OR n.admission_ceiling_mb<=0 OR n.vpcpus<=0 OR n.vcpu_budget<=0 THEN
  RAISE EXCEPTION 'migration target is not sized and active' USING ERRCODE='23514',CONSTRAINT='function_migration_destination_capacity';
 END IF;
 SELECT ram,cpu,vcpu INTO used_ram,used_cpu,used_vcpu FROM migration_admission_node_usage(node,exclude_id);
 IF used_ram+extra_ram>n.admission_ceiling_mb OR used_cpu+extra_cpu>n.vpcpus::bigint*1000*p.cpu_overcommit OR used_vcpu+extra_vcpu>n.vcpu_budget THEN
  RAISE EXCEPTION 'migration destination capacity is reserved' USING ERRCODE='23514',CONSTRAINT='function_migration_destination_capacity';
 END IF;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.guard_instance_migration_admission() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
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
CREATE OR REPLACE FUNCTION public.guard_migration_admission() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
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
