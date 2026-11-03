-- filename: 20261027172037418_snapshotting_holds_node_ram.sql
-- ADR-20261027172037: a SNAPSHOTTING instance still holds its RAM on the
-- node, so the per-node reservation counts it, as placement now does.
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- Only the node sum changes. The previous signature delegates here, so the
-- previous release's admissions count snapshotting rows too: a node it saw
-- as having room may now refuse, which is the correct answer while those
-- VMs are still resident.
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
END $$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
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
