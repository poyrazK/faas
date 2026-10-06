-- filename: 20261006152814585_regional_runtime_personal_binding_guards.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-644: retain the original blank personal wire scope while locking its actual tenant.
CREATE OR REPLACE FUNCTION lock_regional_runtime_quota_app_binding(binding jsonb) RETURNS void LANGUAGE plpgsql AS $$
DECLARE app_org uuid; expected_org uuid := NULLIF(binding->>'RegionalOrgID','')::uuid;
    expected_account uuid := (binding->>'RegionalAccountID')::uuid;
BEGIN
    SELECT a.org_id INTO app_org FROM apps a
    WHERE a.id=(binding->>'RegionalAppID')::uuid AND a.account_id=expected_account
      AND a.slug=binding->>'RegionalAppSlug' AND a.status IN ('active','evicted_cold')
    FOR UPDATE OF a;
    IF NOT FOUND OR (expected_org IS NOT NULL AND app_org IS DISTINCT FROM expected_org) THEN
        RAISE EXCEPTION 'regional quota app binding is inactive'
            USING ERRCODE='23514',CONSTRAINT='regional_capacity_frozen';
    END IF;
    IF app_org IS NOT NULL THEN
        PERFORM 1 FROM orgs o WHERE o.id=app_org AND o.status='active' AND NOT o.deleted_pending
          AND (expected_org IS NOT NULL OR (o.personal_org AND o.personal_owner_account_id=expected_account))
        FOR SHARE OF o;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'regional quota tenant binding is inactive'
                USING ERRCODE='23514',CONSTRAINT='regional_capacity_frozen';
        END IF;
    END IF;
END; $$;
CREATE OR REPLACE FUNCTION guard_regional_runtime_capacity() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE r regional_runtime_quota_receipts; h regional_runtime_quota_heads;
    needs_current boolean;
BEGIN
    IF TG_OP='DELETE' THEN
        RAISE EXCEPTION 'regional capacity receipts and fences are retained'
            USING ERRCODE='23514',CONSTRAINT='regional_capacity_identity';
    END IF;
    IF current_setting('transaction_isolation')<>'read committed' THEN
        RAISE EXCEPTION 'regional capacity requires read committed isolation'
            USING ERRCODE='23514',CONSTRAINT='regional_capacity_frozen';
    END IF;
    PERFORM pg_advisory_xact_lock(hashtextextended('gregale-regional-runtime-quota:'||NEW.app_id::text,0));
    SELECT * INTO r FROM regional_runtime_quota_receipts WHERE app_id=NEW.app_id AND allocation_id=NEW.allocation_id;
    IF NOT FOUND OR NEW.permit_claims->'Grant' IS DISTINCT FROM r.grant_claims OR NEW.generation<>r.generation THEN
        RAISE EXCEPTION 'regional capacity must retain its exact original grant'
            USING ERRCODE='23514',CONSTRAINT='regional_capacity_identity';
    END IF;
    IF TG_OP='INSERT' THEN
        IF NEW.issued_at IS NOT NULL OR NEW.fence_id IS NOT NULL OR NEW.released_at IS NOT NULL OR NEW.stop_claims IS NOT NULL THEN
            RAISE EXCEPTION 'new regional capacity must be reserved before issuance'
                USING ERRCODE='23514',CONSTRAINT='regional_capacity_identity';
        END IF;
        needs_current:=true;
    ELSE
        IF ROW(NEW.app_id,NEW.allocation_id,NEW.permit_id,NEW.generation,NEW.instance_id,NEW.run_id,NEW.deployment_id,NEW.node_id,NEW.host_epoch_id,NEW.kind,NEW.permit_claims,NEW.created_at)
            IS DISTINCT FROM ROW(OLD.app_id,OLD.allocation_id,OLD.permit_id,OLD.generation,OLD.instance_id,OLD.run_id,OLD.deployment_id,OLD.node_id,OLD.host_epoch_id,OLD.kind,OLD.permit_claims,OLD.created_at)
            OR (OLD.issued_at IS NOT NULL AND NEW.issued_at IS DISTINCT FROM OLD.issued_at)
            OR (OLD.fence_id IS NOT NULL AND NEW.fence_id IS DISTINCT FROM OLD.fence_id)
            OR (OLD.released_at IS NOT NULL AND NEW.released_at IS DISTINCT FROM OLD.released_at)
            OR (OLD.stop_claims IS NOT NULL AND ROW(NEW.stop_claims,NEW.stop_envelope) IS DISTINCT FROM ROW(OLD.stop_claims,OLD.stop_envelope)) THEN
            RAISE EXCEPTION 'regional capacity identity, issuance and closure are immutable'
                USING ERRCODE='23514',CONSTRAINT='regional_capacity_identity';
        END IF;
        needs_current:=OLD.issued_at IS NULL AND NEW.issued_at IS NOT NULL;
        IF needs_current AND (OLD.fence_id IS NOT NULL OR OLD.released_at IS NOT NULL OR NEW.fence_id IS NOT NULL OR NEW.released_at IS NOT NULL) THEN
            RAISE EXCEPTION 'regional capacity fence cannot reopen'
                USING ERRCODE='23514',CONSTRAINT='regional_capacity_identity';
        END IF;
        IF OLD.stop_claims IS NULL AND NEW.stop_claims IS NOT NULL AND (OLD.fence_id IS NULL OR OLD.issued_at IS NULL) THEN
            RAISE EXCEPTION 'physical stop requires a previously issued and fenced permit' USING ERRCODE='23514',CONSTRAINT='regional_capacity_identity';
        END IF;
    END IF;
    IF needs_current THEN
        SELECT * INTO h FROM regional_runtime_quota_heads WHERE app_id=NEW.app_id;
        IF NOT FOUND OR h.allocation_id<>NEW.allocation_id OR h.generation<>NEW.generation OR r.grant_envelope IS NULL OR r.retirement IS NOT NULL THEN
            RAISE EXCEPTION 'regional capacity grant is retired or superseded'
                USING ERRCODE='23514',CONSTRAINT='regional_capacity_frozen';
        END IF;
        PERFORM lock_regional_runtime_quota_app_binding(r.grant_claims->'Binding');
    END IF;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION guard_regional_runtime_serving_seat() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE p regional_runtime_capacity_receipts; r regional_runtime_quota_receipts; h regional_runtime_quota_heads;
    steady bigint; extra bigint; needs_current boolean;
BEGIN
    IF TG_OP='DELETE' THEN
        RAISE EXCEPTION 'regional serving seats and fences are retained' USING ERRCODE='23514',CONSTRAINT='regional_capacity_identity';
    END IF;
    IF current_setting('transaction_isolation')<>'read committed' THEN
        RAISE EXCEPTION 'regional serving seats require read committed isolation' USING ERRCODE='23514',CONSTRAINT='regional_capacity_frozen';
    END IF;
    PERFORM pg_advisory_xact_lock(hashtextextended('gregale-regional-runtime-quota:'||NEW.app_id::text,0));
    SELECT * INTO p FROM regional_runtime_capacity_receipts WHERE app_id=NEW.app_id AND permit_id=NEW.permit_id;
    IF NOT FOUND OR NEW.seat_claims->'Permit' IS DISTINCT FROM p.permit_claims
        OR ROW(NEW.allocation_id,NEW.generation,NEW.deployment_id) IS DISTINCT FROM ROW(p.allocation_id,p.generation,p.deployment_id)
        OR p.kind NOT IN ('serving','warm') THEN
        RAISE EXCEPTION 'regional serving seat must pin its exact eligible host permit' USING ERRCODE='23514',CONSTRAINT='regional_capacity_identity';
    END IF;
    IF TG_OP='INSERT' THEN
        IF NEW.issued_at IS NOT NULL OR NEW.fence_id IS NOT NULL OR NEW.released_at IS NOT NULL OR NEW.stop_claims IS NOT NULL OR NEW.closed_by_physical_stop THEN
            RAISE EXCEPTION 'new serving seat must be reserved before issuance' USING ERRCODE='23514',CONSTRAINT='regional_capacity_identity';
        END IF;
        needs_current:=true;
    ELSE
        IF ROW(NEW.app_id,NEW.allocation_id,NEW.seat_id,NEW.permit_id,NEW.generation,NEW.deployment_id,NEW.rollout,NEW.seat_claims,NEW.created_at)
            IS DISTINCT FROM ROW(OLD.app_id,OLD.allocation_id,OLD.seat_id,OLD.permit_id,OLD.generation,OLD.deployment_id,OLD.rollout,OLD.seat_claims,OLD.created_at)
            OR (OLD.issued_at IS NOT NULL AND NEW.issued_at IS DISTINCT FROM OLD.issued_at)
            OR (OLD.fence_id IS NOT NULL AND NEW.fence_id IS DISTINCT FROM OLD.fence_id)
            OR (OLD.released_at IS NOT NULL AND NEW.released_at IS DISTINCT FROM OLD.released_at)
            OR (OLD.stop_claims IS NOT NULL AND ROW(NEW.stop_claims,NEW.stop_envelope) IS DISTINCT FROM ROW(OLD.stop_claims,OLD.stop_envelope))
            OR (OLD.closed_by_physical_stop AND NOT NEW.closed_by_physical_stop) THEN
            RAISE EXCEPTION 'regional serving seat identity and closure are immutable' USING ERRCODE='23514',CONSTRAINT='regional_capacity_identity';
        END IF;
        needs_current:=OLD.issued_at IS NULL AND NEW.issued_at IS NOT NULL;
        IF needs_current AND (OLD.fence_id IS NOT NULL OR OLD.released_at IS NOT NULL OR NEW.fence_id IS NOT NULL OR NEW.released_at IS NOT NULL OR p.issued_at IS NULL) THEN
            RAISE EXCEPTION 'serving seat is closed or physical permit is unissued' USING ERRCODE='23514',CONSTRAINT='regional_capacity_identity';
        END IF;
        IF OLD.stop_claims IS NULL AND NEW.stop_claims IS NOT NULL AND (OLD.fence_id IS NULL OR OLD.issued_at IS NULL) THEN
            RAISE EXCEPTION 'seat stop requires a previously issued and fenced seat' USING ERRCODE='23514',CONSTRAINT='regional_capacity_identity';
        END IF;
        IF needs_current AND NEW.rollout AND NOT EXISTS (
            SELECT 1 FROM regional_runtime_serving_seats WHERE app_id=NEW.app_id AND allocation_id=NEW.allocation_id
            AND NOT rollout AND issued_at IS NOT NULL AND released_at IS NULL AND fence_id IS NULL AND deployment_id<>NEW.deployment_id) THEN
            RAISE EXCEPTION 'rollout seat requires an issued steady seat on another deployment' USING ERRCODE='23514',CONSTRAINT='regional_capacity_exhausted';
        END IF;
        IF NEW.closed_by_physical_stop AND (p.stop_claims IS NULL OR p.released_at IS NULL) THEN
            RAISE EXCEPTION 'physical closure requires the original host stop receipt' USING ERRCODE='23514',CONSTRAINT='regional_capacity_identity';
        END IF;
    END IF;
    IF needs_current THEN
        SELECT * INTO r FROM regional_runtime_quota_receipts WHERE app_id=NEW.app_id AND allocation_id=NEW.allocation_id;
        SELECT * INTO h FROM regional_runtime_quota_heads WHERE app_id=NEW.app_id;
        IF NOT FOUND OR h.allocation_id<>NEW.allocation_id OR h.generation<>NEW.generation
            OR r.grant_envelope IS NULL OR r.retirement IS NOT NULL OR p.fence_id IS NOT NULL OR p.released_at IS NOT NULL THEN
            RAISE EXCEPTION 'serving seat grant or physical permit is fenced' USING ERRCODE='23514',CONSTRAINT='regional_capacity_frozen';
        END IF;
        PERFORM lock_regional_runtime_quota_app_binding(r.grant_claims->'Binding');
    END IF;
    IF TG_OP='INSERT' THEN
        IF EXISTS (SELECT 1 FROM regional_runtime_serving_seats WHERE app_id=NEW.app_id AND permit_id=NEW.permit_id AND released_at IS NULL) THEN
            RAISE EXCEPTION 'physical permit already holds a serving seat' USING ERRCODE='23514',CONSTRAINT='regional_capacity_identity';
        END IF;
        SELECT count(*) FILTER (WHERE NOT rollout),count(*) FILTER (WHERE rollout) INTO steady,extra
            FROM regional_runtime_serving_seats WHERE app_id=NEW.app_id AND allocation_id=NEW.allocation_id AND released_at IS NULL;
        IF (NOT NEW.rollout AND steady>=(r.grant_claims->>'Concurrency')::bigint)
            OR (NEW.rollout AND (extra>=(r.grant_claims->>'RolloutConcurrency')::bigint OR NOT EXISTS (
                SELECT 1 FROM regional_runtime_serving_seats WHERE app_id=NEW.app_id AND allocation_id=NEW.allocation_id AND NOT rollout AND released_at IS NULL AND deployment_id<>NEW.deployment_id)))
            OR steady+extra>=(r.grant_claims->>'Concurrency')::bigint+(r.grant_claims->>'RolloutConcurrency')::bigint THEN
            RAISE EXCEPTION 'regional serving capacity is reserved' USING ERRCODE='23514',CONSTRAINT='regional_capacity_exhausted';
        END IF;
    END IF;
    RETURN NEW;
END; $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Original personal scope and its lock/recheck guard remain valid on rollback.
SELECT 1;
-- +goose StatementEnd
