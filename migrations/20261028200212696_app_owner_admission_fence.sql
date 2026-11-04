-- filename: 20261028200212696_app_owner_admission_fence.sql
-- ADR-20261028200212: an owner scheduler's admission re-checks, in its own
-- transaction, that it still owns the app, so a pressure rebalance cannot
-- move the app while the admission's instance is landing.
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- admit_wake_instance gains p_owner_node_id. The previous signature, kept for
-- the previous release, delegates with NULL: its admissions are unfenced, as
-- before.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION admission_check_app_owner(p_app_id uuid, p_owner_node_id uuid) RETURNS void LANGUAGE plpgsql AS $$
DECLARE
    current_owner uuid;
BEGIN
    IF p_app_id IS NULL OR p_owner_node_id IS NULL THEN
        RETURN;
    END IF;
    -- FOR KEY SHARE is the lock the instance insert's foreign key takes: it
    -- waits for a reassignment holding FOR UPDATE, then reads its result, and
    -- does not wait on ordinary app updates.
    SELECT node_id INTO current_owner FROM apps WHERE id = p_app_id FOR KEY SHARE;
    IF FOUND AND current_owner IS NOT NULL AND current_owner <> p_owner_node_id THEN
        RAISE EXCEPTION 'app owner moved'
            USING ERRCODE = '23514', CONSTRAINT = 'app_owner_current',
                  DETAIL = format('owner=%s admitting=%s', current_owner, p_owner_node_id);
    END IF;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION admit_wake_instance(
    p_app_id uuid, p_deployment_id text, p_state text, p_ram_mb integer, p_node_id uuid, p_wake_id text, p_mode text,
    p_expected_generation bigint, p_counts boolean, p_overhead_mb integer,
    p_bind_recovery boolean, p_snapshot_id uuid,
    p_guard_generation bigint, p_reservation_percent integer,
    p_vcpu integer, p_quiet_vcpu integer, p_cpu_millicores integer, p_quiet_cpu_millicores integer,
    p_default_cpu_millicores integer, p_cpu_overcommit integer,
    p_owner_node_id uuid
) RETURNS SETOF instances LANGUAGE plpgsql AS $$
DECLARE
    admitted instances%ROWTYPE;
    bound integer;
    current_generation bigint;
BEGIN
    PERFORM admission_reserve_node(p_node_id, p_expected_generation, p_ram_mb, p_counts, p_overhead_mb, p_reservation_percent,
        p_vcpu, p_quiet_vcpu, p_cpu_millicores, p_quiet_cpu_millicores, p_default_cpu_millicores, p_cpu_overcommit);
    -- ADR-20261028200212: the admitting scheduler must still own the app.
    PERFORM admission_check_app_owner(p_app_id, p_owner_node_id);
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
    p_guard_generation bigint, p_reservation_percent integer,
    p_vcpu integer, p_quiet_vcpu integer, p_cpu_millicores integer, p_quiet_cpu_millicores integer,
    p_default_cpu_millicores integer, p_cpu_overcommit integer
) RETURNS SETOF instances LANGUAGE plpgsql AS $$
BEGIN
    RETURN QUERY SELECT * FROM admit_wake_instance(p_app_id, p_deployment_id, p_state, p_ram_mb, p_node_id, p_wake_id, p_mode,
        p_expected_generation, p_counts, p_overhead_mb, p_bind_recovery, p_snapshot_id, p_guard_generation, p_reservation_percent,
        p_vcpu, p_quiet_vcpu, p_cpu_millicores, p_quiet_cpu_millicores, p_default_cpu_millicores, p_cpu_overcommit, NULL);
END $$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION admit_wake_instance(
    p_app_id uuid, p_deployment_id text, p_state text, p_ram_mb integer, p_node_id uuid, p_wake_id text, p_mode text,
    p_expected_generation bigint, p_counts boolean, p_overhead_mb integer,
    p_bind_recovery boolean, p_snapshot_id uuid,
    p_guard_generation bigint, p_reservation_percent integer,
    p_vcpu integer, p_quiet_vcpu integer, p_cpu_millicores integer, p_quiet_cpu_millicores integer,
    p_default_cpu_millicores integer, p_cpu_overcommit integer
) RETURNS SETOF instances LANGUAGE plpgsql AS $$
DECLARE
    admitted instances%ROWTYPE;
    bound integer;
    current_generation bigint;
BEGIN
    PERFORM admission_reserve_node(p_node_id, p_expected_generation, p_ram_mb, p_counts, p_overhead_mb, p_reservation_percent,
        p_vcpu, p_quiet_vcpu, p_cpu_millicores, p_quiet_cpu_millicores, p_default_cpu_millicores, p_cpu_overcommit);
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

DROP FUNCTION admit_wake_instance(uuid, text, text, integer, uuid, text, text, bigint, boolean, integer, boolean, uuid, bigint, integer,
    integer, integer, integer, integer, integer, integer, uuid);
DROP FUNCTION admission_check_app_owner(uuid, uuid);
