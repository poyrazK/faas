-- +goose Up
-- A review reads child rows through a locked parent. These fences also cover
-- raw SQL, insert phantoms, disabled drains and legacy mutation paths. Approval
-- takes its parent locks with NOWAIT and retries, avoiding a wait cycle with
-- a writer that already holds a child row or a different parent.
-- +goose StatementBegin
CREATE FUNCTION application_standard_control_input_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE app_ids uuid[] := '{}';
BEGIN
    IF TG_OP <> 'INSERT' THEN app_ids := array_append(app_ids, OLD.app_id); END IF;
    IF TG_OP <> 'DELETE' THEN app_ids := array_append(app_ids, NEW.app_id); END IF;
    PERFORM 1 FROM apps WHERE id = ANY(app_ids) ORDER BY id FOR SHARE;
    IF TG_NARGS > 0 AND TG_ARGV[0] = 'account_quota' THEN
        -- Ownership cannot change after the app lock. A separate quota fence
        -- avoids adding account-row waits to legacy app-first writers.
        PERFORM pg_advisory_xact_lock_shared(hashtextextended('gregale.application-standard.account-quota.' || account_id::text, 0))
        FROM (SELECT DISTINCT account_id FROM apps WHERE id = ANY(app_ids) ORDER BY account_id) owners;
    END IF;
    IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER application_standard_drain_input_guard
BEFORE INSERT OR UPDATE OR DELETE ON app_log_drains FOR EACH ROW
EXECUTE FUNCTION application_standard_control_input_guard('account_quota');
CREATE TRIGGER application_standard_signer_input_guard
BEFORE INSERT OR UPDATE OR DELETE ON app_trusted_signers FOR EACH ROW
EXECUTE FUNCTION application_standard_control_input_guard();
CREATE TRIGGER application_standard_artifact_input_guard
BEFORE INSERT OR DELETE OR UPDATE OF app_id, scope, kind, status, image_digest, rootfs_key,
    rootfs_bytes, source_sha256, parked_reason, scan_status, scan_result ON deployments
FOR EACH ROW EXECUTE FUNCTION application_standard_control_input_guard();

-- Account-wide drain quota also depends on restored services and owner changes
-- outside the reviewed org. The account fence is based on persisted ownership.
-- +goose StatementBegin
CREATE FUNCTION application_standard_account_input_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE account_ids uuid[] := '{}';
BEGIN
    IF TG_OP <> 'INSERT' THEN account_ids := array_append(account_ids, OLD.account_id); END IF;
    IF TG_OP <> 'DELETE' THEN account_ids := array_append(account_ids, NEW.account_id); END IF;
    PERFORM pg_advisory_xact_lock_shared(hashtextextended('gregale.application-standard.account-quota.' || id::text, 0))
    FROM (SELECT DISTINCT id FROM unnest(account_ids) id ORDER BY id) owners;
    IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER application_standard_account_insert_guard
BEFORE INSERT OR DELETE ON apps FOR EACH ROW EXECUTE FUNCTION application_standard_account_input_guard();
CREATE TRIGGER application_standard_account_update_guard
BEFORE UPDATE OF account_id, status ON apps FOR EACH ROW
WHEN (OLD.account_id IS DISTINCT FROM NEW.account_id OR (OLD.status = 'deleted') <> (NEW.status = 'deleted'))
EXECUTE FUNCTION application_standard_account_input_guard();

-- A sidecar or newly live instance can make a retained terminal deployment
-- relevant to artifact verification. Fence those changes on the deployment,
-- including insertion into an otherwise empty child set. Heartbeats are free.
-- +goose StatementBegin
CREATE FUNCTION application_standard_artifact_child_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE deployment_ids uuid[] := '{}'; app_ids uuid[] := '{}';
BEGIN
    IF TG_OP <> 'INSERT' THEN deployment_ids := array_append(deployment_ids, OLD.deployment_id); END IF;
    IF TG_OP <> 'DELETE' THEN deployment_ids := array_append(deployment_ids, NEW.deployment_id); END IF;
    IF TG_TABLE_NAME = 'instances' THEN
        IF TG_OP <> 'INSERT' THEN app_ids := array_append(app_ids, OLD.app_id); END IF;
        IF TG_OP <> 'DELETE' THEN app_ids := array_append(app_ids, NEW.app_id); END IF;
    END IF;
    -- Preserve app-before-deployment ordering used by lifecycle/admission
    -- writers. Deployment locks still fence a reparented artifact and children.
    PERFORM 1 FROM apps WHERE id = ANY(app_ids) OR id IN (SELECT app_id FROM deployments WHERE id = ANY(deployment_ids))
    ORDER BY id FOR SHARE;
    PERFORM 1 FROM deployments WHERE id = ANY(deployment_ids) ORDER BY id FOR SHARE;
    IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER application_standard_sidecar_input_guard
BEFORE INSERT OR UPDATE OR DELETE ON deployment_sidecar_layers
FOR EACH ROW EXECUTE FUNCTION application_standard_artifact_child_guard();
CREATE TRIGGER application_standard_instance_input_guard
BEFORE INSERT OR DELETE OR UPDATE OF deployment_id, state, terminal_at ON instances
FOR EACH ROW EXECUTE FUNCTION application_standard_artifact_child_guard();

-- Reenrollment must revoke a worker's authority even if its old lease has time
-- left. Desired-intent changes revoke as well; consumer ACK/heartbeat changes
-- do not. An attempt to lower a generation never becomes valid implicitly.
-- +goose StatementBegin
CREATE FUNCTION application_standard_enrollment_generation_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.lease_generation < OLD.lease_generation THEN
        RAISE EXCEPTION 'application standard enrollment generation regressed'
            USING ERRCODE = '23514', CONSTRAINT = 'application_standard_enrollment_generation';
    END IF;
    IF NEW.app_id IS DISTINCT FROM OLD.app_id THEN
        RAISE EXCEPTION 'application standard enrollment identity changed'
            USING ERRCODE = '23514', CONSTRAINT = 'application_standard_enrollment_identity';
    END IF;
    IF NEW.org_id IS DISTINCT FROM OLD.org_id OR NEW.project_id IS DISTINCT FROM OLD.project_id
       OR NEW.base_settings IS DISTINCT FROM OLD.base_settings OR NEW.local_settings IS DISTINCT FROM OLD.local_settings
       OR NEW.additional_log_destinations IS DISTINCT FROM OLD.additional_log_destinations
       OR NEW.adoptions IS DISTINCT FROM OLD.adoptions OR NEW.desired_revision IS DISTINCT FROM OLD.desired_revision
       OR NEW.effective IS DISTINCT FROM OLD.effective OR NEW.effective_hash IS DISTINCT FROM OLD.effective_hash THEN
        NEW.lease_owner := '';
        NEW.lease_until := NULL;
        NEW.lease_generation := greatest(NEW.lease_generation, OLD.lease_generation + 1);
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER application_standard_enrollment_generation_guard
BEFORE UPDATE ON app_application_standards FOR EACH ROW
EXECUTE FUNCTION application_standard_enrollment_generation_guard();

-- +goose Down
DROP TRIGGER application_standard_enrollment_generation_guard ON app_application_standards;
DROP FUNCTION application_standard_enrollment_generation_guard();
DROP TRIGGER application_standard_instance_input_guard ON instances;
DROP TRIGGER application_standard_sidecar_input_guard ON deployment_sidecar_layers;
DROP FUNCTION application_standard_artifact_child_guard();
DROP TRIGGER application_standard_account_update_guard ON apps;
DROP TRIGGER application_standard_account_insert_guard ON apps;
DROP FUNCTION application_standard_account_input_guard();
DROP TRIGGER application_standard_artifact_input_guard ON deployments;
DROP TRIGGER application_standard_signer_input_guard ON app_trusted_signers;
DROP TRIGGER application_standard_drain_input_guard ON app_log_drains;
DROP FUNCTION application_standard_control_input_guard();
