-- filename: 20261026090412734_measured_node_reservation_allowance.sql
-- ADR-909 amendment 1: the per-node reservation guard honours measured
-- admission's overcommit bound when the admitting scheduler measured the node.
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- admission_reserve_node and admit_wake_instance gain p_reservation_percent:
-- Σ(ram_mb + overhead) on the node may reach that percent of
-- admission_ceiling_mb, clamped to [100, 200] (200 is
-- api.MeasuredAdmissionMaxOvercommitPercent). The previous signatures stay
-- for the previous release and admit at 100, exactly as before.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION admission_reserve_node(
    p_node_id uuid, p_expected_generation bigint, p_ram_mb integer, p_counts boolean, p_overhead_mb integer,
    p_reservation_percent integer
) RETURNS void LANGUAGE plpgsql AS $$
DECLARE
    node_lifecycle text;
    node_generation bigint;
    ceiling_mb bigint;
    cap_mb bigint;
    used_mb bigint;
    resident_mb bigint;
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
                        AND i.state IN ('waking','cold_booting','running','draining','warm')), 0)::bigint
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
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION admission_reserve_node(
    p_node_id uuid, p_expected_generation bigint, p_ram_mb integer, p_counts boolean, p_overhead_mb integer
) RETURNS void LANGUAGE plpgsql AS $$
BEGIN
    PERFORM admission_reserve_node(p_node_id, p_expected_generation, p_ram_mb, p_counts, p_overhead_mb, 100);
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION admit_wake_instance(
    p_app_id uuid, p_deployment_id text, p_state text, p_ram_mb integer, p_node_id uuid, p_wake_id text, p_mode text,
    p_expected_generation bigint, p_counts boolean, p_overhead_mb integer,
    p_bind_recovery boolean, p_snapshot_id uuid,
    p_guard_generation bigint, p_reservation_percent integer
) RETURNS SETOF instances LANGUAGE plpgsql AS $$
DECLARE
    admitted instances%ROWTYPE;
    bound integer;
    current_generation bigint;
BEGIN
    PERFORM admission_reserve_node(p_node_id, p_expected_generation, p_ram_mb, p_counts, p_overhead_mb, p_reservation_percent);
    INSERT INTO instances (app_id, deployment_id, state, ram_mb, node_id, wake_id, started_at, mode)
    VALUES (p_app_id, nullif(p_deployment_id, '')::uuid, p_state, p_ram_mb, p_node_id,
            CASE WHEN p_wake_id = '' THEN gen_random_uuid() ELSE p_wake_id::uuid END, now(), p_mode)
    RETURNING * INTO admitted;
    IF p_bind_recovery THEN
        INSERT INTO instance_snapshot_recovery (instance_id, wake_id, generation, snapshot_id)
        SELECT admitted.id, admitted.wake_id, d.snapshot_recovery_generation, p_snapshot_id
          FROM deployments d JOIN apps a ON a.id = d.app_id
         WHERE d.id = admitted.deployment_id AND a.status <> 'deleted'
           AND (p_snapshot_id IS NULL OR EXISTS (
                SELECT 1 FROM snapshots s WHERE s.id = p_snapshot_id AND s.deployment_id = d.id
                   AND NOT s.stale AND NOT s.delete_pending AND s.recovery_generation = d.snapshot_recovery_generation))
        FOR SHARE OF d, a;
        GET DIAGNOSTICS bound = ROW_COUNT;
        IF bound <> 1 THEN
            RAISE EXCEPTION 'snapshot recovery lineage changed before admission'
                USING ERRCODE = '23514', CONSTRAINT = 'instance_snapshot_recovery_current';
        END IF;
    END IF;
    -- Taken last, after every row lock above (ADR-1741 lock order).
    IF p_guard_generation IS NOT NULL THEN
        SELECT generation INTO current_generation FROM app_runtime_generations WHERE app_id = p_app_id FOR SHARE;
        IF NOT FOUND OR current_generation <> p_guard_generation THEN
            RAISE EXCEPTION 'app runtime generation moved'
                USING ERRCODE = '23514', CONSTRAINT = 'app_runtime_generation_current';
        END IF;
    END IF;
    RETURN NEXT admitted;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION admit_wake_instance(
    p_app_id uuid, p_deployment_id text, p_state text, p_ram_mb integer, p_node_id uuid, p_wake_id text, p_mode text,
    p_expected_generation bigint, p_counts boolean, p_overhead_mb integer,
    p_bind_recovery boolean, p_snapshot_id uuid,
    p_guard_generation bigint
) RETURNS SETOF instances LANGUAGE plpgsql AS $$
BEGIN
    RETURN QUERY SELECT * FROM admit_wake_instance(p_app_id, p_deployment_id, p_state, p_ram_mb, p_node_id, p_wake_id, p_mode,
        p_expected_generation, p_counts, p_overhead_mb, p_bind_recovery, p_snapshot_id, p_guard_generation, 100);
END $$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION admission_reserve_node(
    p_node_id uuid, p_expected_generation bigint, p_ram_mb integer, p_counts boolean, p_overhead_mb integer
) RETURNS void LANGUAGE plpgsql AS $$
DECLARE
    node_lifecycle text;
    node_generation bigint;
    ceiling_mb bigint;
    used_mb bigint;
    resident_mb bigint;
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
                        AND i.state IN ('waking','cold_booting','running','draining','warm')), 0)::bigint
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
    IF ceiling_mb > 0 AND used_mb + p_ram_mb + p_overhead_mb > ceiling_mb THEN
        RAISE EXCEPTION 'node RAM headroom exhausted'
            USING ERRCODE = '23514', CONSTRAINT = 'instance_node_capacity',
                  DETAIL = format('used_mb=%s admit_mb=%s ceiling_mb=%s', used_mb, p_ram_mb + p_overhead_mb, ceiling_mb);
    END IF;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION admit_wake_instance(
    p_app_id uuid, p_deployment_id text, p_state text, p_ram_mb integer, p_node_id uuid, p_wake_id text, p_mode text,
    p_expected_generation bigint, p_counts boolean, p_overhead_mb integer,
    p_bind_recovery boolean, p_snapshot_id uuid,
    p_guard_generation bigint
) RETURNS SETOF instances LANGUAGE plpgsql AS $$
DECLARE
    admitted instances%ROWTYPE;
    bound integer;
    current_generation bigint;
BEGIN
    PERFORM admission_reserve_node(p_node_id, p_expected_generation, p_ram_mb, p_counts, p_overhead_mb);
    INSERT INTO instances (app_id, deployment_id, state, ram_mb, node_id, wake_id, started_at, mode)
    VALUES (p_app_id, nullif(p_deployment_id, '')::uuid, p_state, p_ram_mb, p_node_id,
            CASE WHEN p_wake_id = '' THEN gen_random_uuid() ELSE p_wake_id::uuid END, now(), p_mode)
    RETURNING * INTO admitted;
    IF p_bind_recovery THEN
        INSERT INTO instance_snapshot_recovery (instance_id, wake_id, generation, snapshot_id)
        SELECT admitted.id, admitted.wake_id, d.snapshot_recovery_generation, p_snapshot_id
          FROM deployments d JOIN apps a ON a.id = d.app_id
         WHERE d.id = admitted.deployment_id AND a.status <> 'deleted'
           AND (p_snapshot_id IS NULL OR EXISTS (
                SELECT 1 FROM snapshots s WHERE s.id = p_snapshot_id AND s.deployment_id = d.id
                   AND NOT s.stale AND NOT s.delete_pending AND s.recovery_generation = d.snapshot_recovery_generation))
        FOR SHARE OF d, a;
        GET DIAGNOSTICS bound = ROW_COUNT;
        IF bound <> 1 THEN
            RAISE EXCEPTION 'snapshot recovery lineage changed before admission'
                USING ERRCODE = '23514', CONSTRAINT = 'instance_snapshot_recovery_current';
        END IF;
    END IF;
    -- Taken last, after every row lock above (ADR-1741 lock order).
    IF p_guard_generation IS NOT NULL THEN
        SELECT generation INTO current_generation FROM app_runtime_generations WHERE app_id = p_app_id FOR SHARE;
        IF NOT FOUND OR current_generation <> p_guard_generation THEN
            RAISE EXCEPTION 'app runtime generation moved'
                USING ERRCODE = '23514', CONSTRAINT = 'app_runtime_generation_current';
        END IF;
    END IF;
    RETURN NEXT admitted;
END $$;
-- +goose StatementEnd

DROP FUNCTION IF EXISTS admit_wake_instance(uuid, text, text, integer, uuid, text, text, bigint, boolean, integer, boolean, uuid, bigint, integer);
DROP FUNCTION IF EXISTS admission_reserve_node(uuid, bigint, integer, boolean, integer, integer);
