-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION assert_regional_runtime_quota_open(app_arg uuid) RETURNS void
LANGUAGE plpgsql AS $$
BEGIN
    IF app_arg IS NULL THEN RETURN; END IF;
    -- Admission must refresh its view after the journal lock. A fixed MVCC
    -- snapshot could otherwise miss a quota head committed while it waited.
    IF current_setting('transaction_isolation') <> 'read committed' THEN
        RAISE EXCEPTION 'regional runtime admission requires read committed isolation'
            USING ERRCODE='23514', CONSTRAINT='regional_runtime_quota_frozen';
    END IF;
    PERFORM pg_advisory_xact_lock(hashtextextended('gregale-regional-runtime-quota:' || app_arg::text, 0));
    IF EXISTS (SELECT 1 FROM regional_runtime_quota_heads WHERE app_id=app_arg) THEN
        RAISE EXCEPTION 'regional runtime quota admission is frozen'
            USING ERRCODE='23514', CONSTRAINT='regional_runtime_quota_frozen';
    END IF;
END;
$$;
CREATE FUNCTION fence_regional_runtime_instance_write() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE needs_admission boolean := false;
BEGIN
    IF TG_OP='INSERT' THEN
        needs_admission := NEW.state IN ('waking','cold_booting','running','draining','snapshotting','migrating','warm');
    ELSE
        needs_admission := NEW.app_id IS DISTINCT FROM OLD.app_id OR NEW.id IS DISTINCT FROM OLD.id;
        IF NEW.state IN ('waking','cold_booting','running','draining','snapshotting','migrating','warm') THEN
            needs_admission := needs_admission
                OR OLD.state NOT IN ('waking','cold_booting','running','draining','snapshotting','migrating','warm')
                OR ROW(NEW.node_id,NEW.deployment_id,NEW.wake_id,NEW.mode,NEW.ram_mb,NEW.netns,NEW.host_ip,NEW.guest_uid)
                   IS DISTINCT FROM ROW(OLD.node_id,OLD.deployment_id,OLD.wake_id,OLD.mode,OLD.ram_mb,OLD.netns,OLD.host_ip,OLD.guest_uid)
                OR (NEW.state<>OLD.state AND (NEW.state IN ('waking','cold_booting','running') OR (NEW.state='warm' AND OLD.state<>'running')));
        END IF;
    END IF;
    IF needs_admission THEN
        -- Lock identity roots in canonical order if an instance is reparented.
        IF TG_OP='UPDATE' AND OLD.app_id IS DISTINCT FROM NEW.app_id THEN
            PERFORM assert_regional_runtime_quota_open(LEAST(OLD.app_id,NEW.app_id));
            PERFORM assert_regional_runtime_quota_open(GREATEST(OLD.app_id,NEW.app_id));
        ELSE
            PERFORM assert_regional_runtime_quota_open(NEW.app_id);
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
-- Run before resource/identity triggers so normal creation takes the quota root
-- before app-row locks. SQL callers with incompatible explicit lock order abort
-- atomically on deadlock rather than admitting after a quota fence.
CREATE TRIGGER aa_regional_runtime_quota_instance_fence BEFORE INSERT OR UPDATE
    ON instances FOR EACH ROW EXECUTE FUNCTION fence_regional_runtime_instance_write();
CREATE FUNCTION fence_regional_runtime_app_task_write() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE needs_admission boolean := false;
BEGIN
    IF TG_OP='INSERT' THEN
        needs_admission := NEW.status IN ('restoring','running');
    ELSE
        needs_admission := NEW.app_id IS DISTINCT FROM OLD.app_id OR NEW.id IS DISTINCT FROM OLD.id;
        IF NEW.status IN ('restoring','running') THEN
            needs_admission := needs_admission OR NEW.status<>OLD.status
                OR ROW(NEW.account_id,NEW.deployment_id,NEW.lease_token,NEW.lease_owner,NEW.artifact_key,NEW.image_digest,NEW.command,NEW.command_shell)
                   IS DISTINCT FROM ROW(OLD.account_id,OLD.deployment_id,OLD.lease_token,OLD.lease_owner,OLD.artifact_key,OLD.image_digest,OLD.command,OLD.command_shell);
        END IF;
    END IF;
    IF needs_admission THEN
        IF TG_OP='UPDATE' AND OLD.app_id IS DISTINCT FROM NEW.app_id THEN
            PERFORM assert_regional_runtime_quota_open(LEAST(OLD.app_id,NEW.app_id));
            PERFORM assert_regional_runtime_quota_open(GREATEST(OLD.app_id,NEW.app_id));
        ELSE
            PERFORM assert_regional_runtime_quota_open(NEW.app_id);
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER aa_regional_runtime_quota_app_task_fence BEFORE INSERT OR UPDATE
    ON app_tasks FOR EACH ROW EXECUTE FUNCTION fence_regional_runtime_app_task_write();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    LOCK TABLE regional_runtime_quota_receipts, regional_runtime_quota_heads IN ACCESS EXCLUSIVE MODE;
    IF EXISTS (SELECT 1 FROM regional_runtime_quota_heads) THEN
        RAISE EXCEPTION 'cannot downgrade populated regional runtime quota write fences';
    END IF;
END $$;
DROP TRIGGER aa_regional_runtime_quota_app_task_fence ON app_tasks;
DROP TRIGGER aa_regional_runtime_quota_instance_fence ON instances;
DROP FUNCTION fence_regional_runtime_app_task_write();
DROP FUNCTION fence_regional_runtime_instance_write();
DROP FUNCTION assert_regional_runtime_quota_open(uuid);
-- +goose StatementEnd
