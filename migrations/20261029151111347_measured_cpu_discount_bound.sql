-- filename: 20261029151111347_measured_cpu_discount_bound.sql
-- ADR 20261029151111: measured CPU admission discounts measured instances'
-- unused CPU and lets a node's vCPU and CPU bookings reach
-- api.MeasuredCPUMaxOvercommitPercent (700) of their budgets, so the durable
-- node reservation bounds the scheduler's discount at that cap instead of
-- ADR-1151's 300%. Only the bound changes; a previous-release scheduler's
-- discounts never pass 300% and are checked exactly as before.
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION admission_reserve_node(
    p_node_id uuid, p_expected_generation bigint, p_ram_mb integer, p_counts boolean, p_overhead_mb integer,
    p_reservation_percent integer,
    p_vcpu integer, p_quiet_vcpu integer, p_cpu_millicores integer, p_quiet_cpu_millicores integer,
    p_default_cpu_millicores integer, p_cpu_overcommit integer
) RETURNS void LANGUAGE plpgsql AS $$
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

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION admission_reserve_node(
    p_node_id uuid, p_expected_generation bigint, p_ram_mb integer, p_counts boolean, p_overhead_mb integer,
    p_reservation_percent integer,
    p_vcpu integer, p_quiet_vcpu integer, p_cpu_millicores integer, p_quiet_cpu_millicores integer,
    p_default_cpu_millicores integer, p_cpu_overcommit integer
) RETURNS void LANGUAGE plpgsql AS $$
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
    -- A quiet-pause discount is the scheduler's measurement; it never lets
    -- bookings pass api.QuietPauseMaxVCPUOvercommitPercent (300) of a budget.
    quiet_vcpu := least(greatest(coalesce(p_quiet_vcpu, 0), 0), node_vcpu_budget * 2);
    IF p_vcpu > 0 AND node_vcpu_budget > 0 AND used_vcpu - quiet_vcpu + p_vcpu > node_vcpu_budget THEN
        RAISE EXCEPTION 'node vCPU headroom exhausted'
            USING ERRCODE = '23514', CONSTRAINT = 'instance_node_capacity',
                  DETAIL = format('resource=vcpu used_vcpu=%s quiet_vcpu=%s admit_vcpu=%s budget_vcpu=%s',
                                  used_vcpu, quiet_vcpu, p_vcpu, node_vcpu_budget);
    END IF;
    quiet_cpu := least(greatest(coalesce(p_quiet_cpu_millicores, 0), 0), node_cpu_budget * 2);
    IF p_cpu_millicores > 0 AND node_cpu_budget > 0 AND used_cpu - quiet_cpu + p_cpu_millicores > node_cpu_budget THEN
        RAISE EXCEPTION 'node CPU headroom exhausted'
            USING ERRCODE = '23514', CONSTRAINT = 'instance_node_capacity',
                  DETAIL = format('resource=cpu used_cpu_millicores=%s quiet_cpu_millicores=%s admit_cpu_millicores=%s budget_cpu_millicores=%s',
                                  used_cpu, quiet_cpu, p_cpu_millicores, node_cpu_budget);
    END IF;
END $$;
-- +goose StatementEnd
