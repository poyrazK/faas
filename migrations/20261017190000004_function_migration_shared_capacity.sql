-- adr: 1547. Both migration protocols share capacity; retained sources remain resident.
-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION function_migration_pending_node_usage(target uuid) RETURNS TABLE(ram bigint,cpu bigint,vcpu bigint)
LANGUAGE sql STABLE AS $$
 SELECT i.capacity_ram_mb::bigint,i.capacity_cpu_millicores::bigint,i.capacity_vcpu::bigint
 FROM function_migration_destinations d JOIN instances i ON i.id=d.instance_id
 WHERE d.node_id=target AND d.phase IN ('issued','ready')
 UNION ALL
 SELECT i.capacity_ram_mb::bigint,i.capacity_cpu_millicores::bigint,i.capacity_vcpu::bigint
 FROM function_migration_destinations d JOIN instances i ON i.id=d.instance_id
 JOIN function_migration_sources s ON s.instance_id=d.instance_id AND s.lease_token=d.lease_token
 WHERE s.source_node_id=target::text AND d.phase='committed' AND s.phase NOT IN ('recovered','retired')
$$;
CREATE OR REPLACE FUNCTION migration_source_node_usage(target uuid,legacy boolean DEFAULT false) RETURNS TABLE(ram bigint,cpu bigint,vcpu bigint) LANGUAGE sql AS $$
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
 ) original CROSS JOIN (
 SELECT coalesce(sum(ram),0)::bigint AS ram,coalesce(sum(cpu),0)::bigint AS cpu,coalesce(sum(vcpu),0)::bigint AS vcpu
 FROM function_migration_pending_node_usage(target)
 ) extra;
$$;
CREATE OR REPLACE FUNCTION function_migration_check_capacity(node uuid, extra_ram bigint, extra_cpu bigint, extra_vcpu bigint, exclude_id uuid) RETURNS void
 LANGUAGE plpgsql AS $$
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
CREATE OR REPLACE FUNCTION function_migration_instance_capacity_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.state NOT IN ('waking','cold_booting','running','draining','warm','snapshotting','migrating','frozen') THEN RETURN NEW; END IF;
 IF TG_OP='UPDATE' AND OLD.node_id IS NOT DISTINCT FROM NEW.node_id AND OLD.state IN ('waking','cold_booting','running','draining','warm','snapshotting','migrating','frozen')
  AND NEW.capacity_ram_mb<=OLD.capacity_ram_mb AND NEW.capacity_cpu_millicores<=OLD.capacity_cpu_millicores AND NEW.capacity_vcpu<=OLD.capacity_vcpu THEN RETURN NEW; END IF;
 -- Serialize before inspecting pending claims: the first destination may still
 -- be uncommitted, so testing EXISTS before taking this lock would miss it.
 PERFORM pg_advisory_xact_lock(1852793957,hashtext(NEW.node_id::text));
 IF EXISTS (SELECT 1 FROM function_migration_pending_node_usage(NEW.node_id)) THEN
  PERFORM function_migration_check_capacity(NEW.node_id,NEW.capacity_ram_mb,NEW.capacity_cpu_millicores,NEW.capacity_vcpu,NEW.id);
 END IF;
 RETURN NEW;
END;
$$;
CREATE OR REPLACE FUNCTION guard_function_migration_node() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (TG_OP='DELETE' OR NEW.lifecycle IN ('maintenance','retired')) AND EXISTS (
  SELECT 1 FROM function_migration_pending_node_usage(OLD.id)
 ) THEN
  RAISE EXCEPTION 'function migration cleanup retains node resources' USING ERRCODE='23514',CONSTRAINT='function_migration_node_pending';
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER function_migration_node_pending BEFORE DELETE OR UPDATE OF lifecycle ON compute_nodes
 FOR EACH ROW EXECUTE FUNCTION guard_function_migration_node();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER function_migration_node_pending ON compute_nodes;
DROP FUNCTION guard_function_migration_node();
CREATE OR REPLACE FUNCTION migration_source_node_usage(target uuid,legacy boolean DEFAULT false) RETURNS TABLE(ram bigint,cpu bigint,vcpu bigint) LANGUAGE sql AS $$
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
CREATE OR REPLACE FUNCTION function_migration_check_capacity(node uuid, extra_ram bigint, extra_cpu bigint, extra_vcpu bigint, exclude_id uuid) RETURNS void
 LANGUAGE plpgsql AS $$
DECLARE n compute_nodes%ROWTYPE; used_ram bigint; used_cpu bigint; used_vcpu bigint; p service_capacity_policy%ROWTYPE;
BEGIN
 PERFORM pg_advisory_xact_lock(1852793957,hashtext(node::text));
 SELECT * INTO n FROM compute_nodes WHERE id=node FOR SHARE NOWAIT;
 SELECT * INTO p FROM service_capacity_policy WHERE singleton;
 IF n.id IS NULL OR n.lifecycle<>'active' OR n.admission_ceiling_mb<=0 OR n.vpcpus<=0 OR n.vcpu_budget<=0 THEN
  RAISE EXCEPTION 'migration target is not sized and active' USING ERRCODE='23514',CONSTRAINT='function_migration_destination_capacity';
 END IF;
 SELECT coalesce(sum(ram),0),coalesce(sum(cpu),0),coalesce(sum(vcpu),0) INTO used_ram,used_cpu,used_vcpu FROM (
  SELECT i.capacity_ram_mb ram,i.capacity_cpu_millicores cpu,i.capacity_vcpu vcpu FROM instances i
   WHERE i.node_id=node AND i.id IS DISTINCT FROM exclude_id AND i.state IN ('waking','cold_booting','running','draining','warm','snapshotting','migrating','frozen')
  UNION ALL SELECT i.capacity_ram_mb,i.capacity_cpu_millicores,i.capacity_vcpu FROM function_migration_destinations d JOIN instances i ON i.id=d.instance_id
   WHERE d.node_id=node AND d.phase IN ('issued','ready')
 ) resident;
 IF used_ram+extra_ram>n.admission_ceiling_mb OR used_cpu+extra_cpu>n.vpcpus::bigint*1000*p.cpu_overcommit OR used_vcpu+extra_vcpu>n.vcpu_budget THEN
  RAISE EXCEPTION 'migration destination capacity is reserved' USING ERRCODE='23514',CONSTRAINT='function_migration_destination_capacity';
 END IF;
END;
$$;
CREATE OR REPLACE FUNCTION function_migration_instance_capacity_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.state NOT IN ('waking','cold_booting','running','draining','warm','snapshotting','migrating','frozen') THEN RETURN NEW; END IF;
 IF TG_OP='UPDATE' AND OLD.node_id IS NOT DISTINCT FROM NEW.node_id AND OLD.state IN ('waking','cold_booting','running','draining','warm','snapshotting','migrating','frozen')
  AND NEW.capacity_ram_mb<=OLD.capacity_ram_mb AND NEW.capacity_cpu_millicores<=OLD.capacity_cpu_millicores AND NEW.capacity_vcpu<=OLD.capacity_vcpu THEN RETURN NEW; END IF;
 -- Serialize before inspecting pending claims: the first destination may still
 -- be uncommitted, so testing EXISTS before taking this lock would miss it.
 PERFORM pg_advisory_xact_lock(1852793957,hashtext(NEW.node_id::text));
 IF EXISTS (SELECT 1 FROM function_migration_destinations WHERE node_id=NEW.node_id AND phase IN ('issued','ready')) THEN
  PERFORM function_migration_check_capacity(NEW.node_id,NEW.capacity_ram_mb,NEW.capacity_cpu_millicores,NEW.capacity_vcpu,NEW.id);
 END IF;
 RETURN NEW;
END;
$$;
DROP FUNCTION function_migration_pending_node_usage(uuid);
-- +goose StatementEnd
