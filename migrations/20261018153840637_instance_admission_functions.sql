-- filename: 20261018153840637_instance_admission_functions.sql
-- ADR-1741: instance admission and publication in one round trip each.
-- The previous release calls none of these functions.
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- ADR-1741: instance admission in one round trip.
--
-- admission_reserve_node holds the node checks every instance admission
-- makes, in the order the Go transaction made them as separate statements:
-- the service-capacity policy lock (node_admission_lock_capacity), the node
-- row share-locked and checked admitting at the placement's lifecycle
-- generation (ADR-1528), then, for a row that counts toward the node's
-- resident RAM, the per-node advisory lock (ADR-193) followed by the
-- headroom read. Under READ COMMITTED each statement of this function takes
-- its own snapshot, so the headroom read sees every admission committed
-- before the lock was granted, as the separate statement did.
--
-- admit_wake_instance is a wake's whole admission: the node checks, the
-- instance insert, the ADR-1523 snapshot-recovery binding and the ADR-1741
-- runtime-generation check, taken last. publish_instance_on_generation is
-- the matching publication after the boot. Each refusal raises with a
-- constraint name pkg/state maps to the error the Go transaction returned.
--
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

-- publish_instance_on_generation is a boot's publication when its admission
-- decided from schedd's owner cache: the runtime/state compare-and-swap, the
-- config proof with the wake's event and notification, the runtime-config
-- receipt, and the runtime-generation check taken last, in one statement.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION publish_instance_on_generation(
    p_instance_id uuid, p_app_id uuid, p_deployment_id uuid, p_node_id uuid, p_wake_id uuid,
    p_expected_state text, p_target_state text, p_netns text, p_host_ip inet, p_guest_uid integer,
    p_config_fingerprint text, p_environment_id uuid, p_scope text, p_secret_fingerprint text,
    p_event_kind text, p_event_data jsonb, p_notify_channel text, p_notify_payload text,
    p_record_receipt boolean, p_receipt_scope text, p_boundary_at timestamptz, p_variables jsonb,
    p_secret_versions jsonb, p_all_secrets boolean, p_secret_refs jsonb, p_sidecar_secret_versions jsonb,
    p_guard_generation bigint,
    OUT published instances, OUT snapshot_backoff_recorded boolean
) LANGUAGE plpgsql AS $$
DECLARE
    receipts integer;
    current_generation bigint;
BEGIN
    UPDATE instances SET netns = p_netns, host_ip = p_host_ip, guest_uid = p_guest_uid,
           started_at = clock_timestamp(), state = p_target_state
     WHERE id = p_instance_id AND app_id = p_app_id AND deployment_id = p_deployment_id
       AND node_id = p_node_id AND wake_id = p_wake_id AND state = p_expected_state
       AND p_expected_state NOT IN ('warm', 'frozen')
    RETURNING * INTO published;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'instance is not in its admitted state'
            USING ERRCODE = '23514', CONSTRAINT = 'instance_publication_current';
    END IF;
    INSERT INTO runtime_instance_config_proofs(instance_id, wake_id, node_id, deployment_id, environment_id, scope,
                                               secret_fingerprint, config_fingerprint)
    VALUES (published.id, published.wake_id, published.node_id, published.deployment_id, p_environment_id, p_scope,
            p_secret_fingerprint, p_config_fingerprint)
    ON CONFLICT (instance_id) DO UPDATE SET wake_id = excluded.wake_id, node_id = excluded.node_id,
        deployment_id = excluded.deployment_id, environment_id = excluded.environment_id, scope = excluded.scope,
        secret_fingerprint = excluded.secret_fingerprint, config_fingerprint = excluded.config_fingerprint;
    IF p_event_kind IS NOT NULL THEN
        INSERT INTO events(actor, kind, subject, data) VALUES ('schedd', p_event_kind, p_instance_id, p_event_data);
    END IF;
    IF p_notify_channel IS NOT NULL THEN
        PERFORM pg_notify(p_notify_channel, p_notify_payload);
    END IF;
    SELECT EXISTS (SELECT 1 FROM deployments d WHERE d.id = published.deployment_id
                     AND (d.snapshot_miss_count <> 0 OR d.snapshot_miss_last_at IS NOT NULL
                          OR d.snapshot_miss_backoff_until IS NOT NULL))
      INTO snapshot_backoff_recorded;
    IF p_record_receipt THEN
        INSERT INTO instance_runtime_config_receipts(instance_id, wake_id, scope, boundary_at, variables, secret_versions,
                                                     all_secrets, secret_refs, sidecar_secret_versions)
        SELECT i.id, i.wake_id, d.scope, p_boundary_at, p_variables, p_secret_versions, p_all_secrets, p_secret_refs,
               p_sidecar_secret_versions
          FROM instances i JOIN deployments d ON d.id = i.deployment_id
         WHERE i.id = p_instance_id AND i.wake_id = p_wake_id AND i.state = 'running' AND d.scope = p_receipt_scope
        FOR UPDATE OF i
        ON CONFLICT (instance_id) DO UPDATE SET wake_id = excluded.wake_id, scope = excluded.scope,
            boundary_at = excluded.boundary_at, variables = excluded.variables, secret_versions = excluded.secret_versions,
            all_secrets = excluded.all_secrets, secret_refs = excluded.secret_refs,
            sidecar_secret_versions = excluded.sidecar_secret_versions, acknowledged_at = now()
        WHERE instance_runtime_config_receipts.wake_id <> excluded.wake_id OR
            (instance_runtime_config_receipts.scope = excluded.scope AND instance_runtime_config_receipts.boundary_at = excluded.boundary_at
             AND instance_runtime_config_receipts.variables = excluded.variables AND instance_runtime_config_receipts.secret_versions = excluded.secret_versions
             AND instance_runtime_config_receipts.all_secrets = excluded.all_secrets AND instance_runtime_config_receipts.secret_refs = excluded.secret_refs
             AND instance_runtime_config_receipts.sidecar_secret_versions = excluded.sidecar_secret_versions);
        GET DIAGNOSTICS receipts = ROW_COUNT;
        IF receipts <> 1 THEN
            RAISE EXCEPTION 'runtime config receipt does not match the instance'
                USING ERRCODE = '23514', CONSTRAINT = 'instance_runtime_config_receipt_current';
        END IF;
    END IF;
    -- Taken last, after every row lock above (ADR-1741 lock order).
    SELECT generation INTO current_generation FROM app_runtime_generations WHERE app_id = p_app_id FOR SHARE;
    IF NOT FOUND OR current_generation <> p_guard_generation THEN
        RAISE EXCEPTION 'app runtime generation moved'
            USING ERRCODE = '23514', CONSTRAINT = 'app_runtime_generation_current';
    END IF;
END $$;
-- +goose StatementEnd

-- snapshot_restore_pressure_acquire takes the fleet-wide restore-pressure
-- session lock and, only once it holds it, expires stale leases and counts
-- the active ones per node, in one call. The count is its own statement, so
-- it sees every lease committed before the lock was granted.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION snapshot_restore_pressure_acquire(p_lock_name text)
RETURNS TABLE(locked boolean, node_id text, active bigint) LANGUAGE plpgsql AS $$
BEGIN
    IF NOT pg_try_advisory_lock(hashtextextended(p_lock_name, 0)) THEN
        RETURN QUERY SELECT false, NULL::text, NULL::bigint;
        RETURN;
    END IF;
    DELETE FROM snapshot_restore_pressure_leases WHERE expires_at <= now();
    RETURN QUERY SELECT true, l.node_id::text, count(*) FROM snapshot_restore_pressure_leases l
                  WHERE l.expires_at > now() GROUP BY l.node_id;
    IF NOT FOUND THEN
        RETURN QUERY SELECT true, NULL::text, NULL::bigint;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP FUNCTION IF EXISTS snapshot_restore_pressure_acquire(text);
DROP FUNCTION IF EXISTS publish_instance_on_generation(uuid, uuid, uuid, uuid, uuid, text, text, text, inet, integer, text, uuid, text, text, text, jsonb, text, text, boolean, text, timestamptz, jsonb, jsonb, boolean, jsonb, jsonb, bigint);
DROP FUNCTION IF EXISTS admit_wake_instance(uuid, text, text, integer, uuid, text, text, bigint, boolean, integer, boolean, uuid, bigint);
DROP FUNCTION IF EXISTS admission_reserve_node(uuid, bigint, integer, boolean, integer);
-- +goose StatementEnd
