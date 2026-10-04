-- +goose Up
-- +goose StatementBegin
-- ADR-492: retained capacity bookkeeping does not activate runtime authority.
CREATE TABLE regional_runtime_capacity_receipts (
    app_id uuid NOT NULL,
    allocation_id uuid NOT NULL,
    permit_id uuid NOT NULL CHECK (permit_id<>'00000000-0000-0000-0000-000000000000'),
    generation bigint NOT NULL,
    instance_id uuid NOT NULL CHECK (instance_id<>'00000000-0000-0000-0000-000000000000'),
    run_id uuid NOT NULL CHECK (run_id<>'00000000-0000-0000-0000-000000000000'),
    deployment_id uuid NOT NULL CHECK (deployment_id<>'00000000-0000-0000-0000-000000000000'),
    node_id uuid NOT NULL CHECK (node_id<>'00000000-0000-0000-0000-000000000000'),
    host_epoch_id uuid NOT NULL CHECK (host_epoch_id<>'00000000-0000-0000-0000-000000000000'),
    kind text NOT NULL CHECK (kind IN ('serving','warm','prime','app_task')),
    permit_claims jsonb NOT NULL CHECK (jsonb_typeof(permit_claims)='object' AND octet_length(permit_claims::text)<=4096),
    created_at timestamptz NOT NULL DEFAULT now(),
    issued_at timestamptz,
    fence_id uuid CHECK (fence_id<>'00000000-0000-0000-0000-000000000000'),
    released_at timestamptz,
    stop_claims jsonb,
    stop_envelope jsonb,
    PRIMARY KEY (app_id,permit_id),
    UNIQUE (app_id,node_id,host_epoch_id,instance_id,run_id),
    FOREIGN KEY (app_id,allocation_id) REFERENCES regional_runtime_quota_receipts(app_id,allocation_id),
    CONSTRAINT regional_capacity_permit_shape CHECK (
        ROW(permit_claims->>'ID',permit_claims->>'InstanceID',permit_claims->>'RunID',permit_claims->>'DeploymentID',permit_claims->>'NodeID',permit_claims->>'HostEpochID',permit_claims->>'Kind')
        IS NOT DISTINCT FROM ROW(permit_id::text,instance_id::text,run_id::text,deployment_id::text,node_id::text,host_epoch_id::text,kind)),
    CONSTRAINT regional_capacity_stop_shape CHECK (
        (stop_claims IS NULL)=(stop_envelope IS NULL)
        AND (released_at IS NULL OR fence_id IS NOT NULL)
        AND (issued_at IS NULL OR released_at IS NULL OR stop_claims IS NOT NULL)
        AND (stop_claims IS NULL OR (
            issued_at IS NOT NULL AND released_at IS NOT NULL AND fence_id IS NOT NULL
            AND jsonb_typeof(stop_claims)='object' AND octet_length(stop_claims::text)<=8192
            AND jsonb_typeof(stop_envelope)='object' AND octet_length(stop_envelope::text)<=8192
            AND stop_claims->'Permit' IS NOT DISTINCT FROM permit_claims
            AND stop_claims->>'FenceID' IS NOT DISTINCT FROM fence_id::text
            AND stop_claims->>'AdmissionFrozen' IS NOT DISTINCT FROM 'true'
            AND stop_claims->>'CountedRuntimes' IS NOT DISTINCT FROM '0')))
);
-- No app, deployment, instance or node FK: original attempts outlive collection.
CREATE INDEX regional_runtime_capacity_held ON regional_runtime_capacity_receipts(app_id,allocation_id,permit_id) WHERE released_at IS NULL;

CREATE FUNCTION guard_regional_runtime_capacity() RETURNS trigger LANGUAGE plpgsql AS $$
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
        PERFORM 1 FROM apps a WHERE a.id=NEW.app_id AND a.status IN ('active','evicted_cold')
            AND a.account_id::text=r.grant_claims->'Binding'->>'RegionalAccountID'
            AND coalesce(a.org_id::text,'')=r.grant_claims->'Binding'->>'RegionalOrgID'
            AND a.slug=r.grant_claims->'Binding'->>'RegionalAppSlug' FOR SHARE;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'regional capacity app binding is inactive'
                USING ERRCODE='23514',CONSTRAINT='regional_capacity_frozen';
        END IF;
        IF coalesce(r.grant_claims->'Binding'->>'RegionalOrgID','')<>'' THEN
            PERFORM 1 FROM orgs WHERE id=(r.grant_claims->'Binding'->>'RegionalOrgID')::uuid AND status='active' AND NOT deleted_pending FOR SHARE;
            IF NOT FOUND THEN
                RAISE EXCEPTION 'regional capacity tenant is inactive'
                    USING ERRCODE='23514',CONSTRAINT='regional_capacity_frozen';
            END IF;
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER regional_capacity_guard BEFORE INSERT OR UPDATE OR DELETE ON regional_runtime_capacity_receipts FOR EACH ROW EXECUTE FUNCTION guard_regional_runtime_capacity();
CREATE FUNCTION refuse_regional_capacity_truncate() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
    RAISE EXCEPTION 'regional capacity receipts and fences are retained' USING ERRCODE='23514',CONSTRAINT='regional_capacity_identity';
END; $$;
CREATE TRIGGER regional_capacity_truncate_guard BEFORE TRUNCATE ON regional_runtime_capacity_receipts FOR EACH STATEMENT EXECUTE FUNCTION refuse_regional_capacity_truncate();

CREATE TABLE regional_runtime_serving_seats (
    app_id uuid NOT NULL,
    allocation_id uuid NOT NULL,
    seat_id uuid NOT NULL CHECK (seat_id<>'00000000-0000-0000-0000-000000000000'),
    permit_id uuid NOT NULL,
    generation bigint NOT NULL,
    deployment_id uuid NOT NULL,
    rollout boolean NOT NULL,
    seat_claims jsonb NOT NULL CHECK (jsonb_typeof(seat_claims)='object' AND octet_length(seat_claims::text)<=4096),
    created_at timestamptz NOT NULL DEFAULT now(),
    issued_at timestamptz,
    fence_id uuid CHECK (fence_id<>'00000000-0000-0000-0000-000000000000'),
    released_at timestamptz,
    stop_claims jsonb,
    stop_envelope jsonb,
    closed_by_physical_stop boolean NOT NULL DEFAULT false,
    PRIMARY KEY (app_id,seat_id),
    FOREIGN KEY (app_id,permit_id) REFERENCES regional_runtime_capacity_receipts(app_id,permit_id),
    CONSTRAINT regional_seat_shape CHECK (
        ROW(seat_claims->>'ID',seat_claims->>'Rollout') IS NOT DISTINCT FROM ROW(seat_id::text,rollout::text)),
    CONSTRAINT regional_seat_stop_shape CHECK (
        (stop_claims IS NULL)=(stop_envelope IS NULL)
        AND (released_at IS NULL OR fence_id IS NOT NULL)
        AND (issued_at IS NULL OR released_at IS NULL OR stop_claims IS NOT NULL OR closed_by_physical_stop)
        AND (NOT closed_by_physical_stop OR (issued_at IS NOT NULL AND released_at IS NOT NULL AND stop_claims IS NULL))
        AND (stop_claims IS NULL OR (
            issued_at IS NOT NULL AND released_at IS NOT NULL AND fence_id IS NOT NULL
            AND jsonb_typeof(stop_claims)='object' AND octet_length(stop_claims::text)<=8192
            AND jsonb_typeof(stop_envelope)='object' AND octet_length(stop_envelope::text)<=8192
            AND stop_claims->'Seat' IS NOT DISTINCT FROM seat_claims
            AND stop_claims->>'FenceID' IS NOT DISTINCT FROM fence_id::text
            AND stop_claims->>'AdmissionFrozen' IS NOT DISTINCT FROM 'true'
            AND stop_claims->>'CountedServing' IS NOT DISTINCT FROM '0')))
);
CREATE UNIQUE INDEX regional_seat_active_permit ON regional_runtime_serving_seats(app_id,permit_id) WHERE released_at IS NULL;
CREATE INDEX regional_seat_held ON regional_runtime_serving_seats(app_id,allocation_id,seat_id) WHERE released_at IS NULL;

CREATE FUNCTION guard_regional_runtime_serving_seat() RETURNS trigger LANGUAGE plpgsql AS $$
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
        PERFORM 1 FROM apps a WHERE a.id=NEW.app_id AND a.status IN ('active','evicted_cold')
            AND a.account_id::text=r.grant_claims->'Binding'->>'RegionalAccountID'
            AND coalesce(a.org_id::text,'')=r.grant_claims->'Binding'->>'RegionalOrgID'
            AND a.slug=r.grant_claims->'Binding'->>'RegionalAppSlug' FOR SHARE;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'serving seat app binding is inactive' USING ERRCODE='23514',CONSTRAINT='regional_capacity_frozen';
        END IF;
        IF coalesce(r.grant_claims->'Binding'->>'RegionalOrgID','')<>'' THEN
            PERFORM 1 FROM orgs WHERE id=(r.grant_claims->'Binding'->>'RegionalOrgID')::uuid AND status='active' AND NOT deleted_pending FOR SHARE;
            IF NOT FOUND THEN
                RAISE EXCEPTION 'serving seat tenant is inactive' USING ERRCODE='23514',CONSTRAINT='regional_capacity_frozen';
            END IF;
        END IF;
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
CREATE TRIGGER regional_serving_seat_guard BEFORE INSERT OR UPDATE OR DELETE ON regional_runtime_serving_seats FOR EACH ROW EXECUTE FUNCTION guard_regional_runtime_serving_seat();
CREATE TRIGGER regional_serving_seat_truncate_guard BEFORE TRUNCATE ON regional_runtime_serving_seats FOR EACH STATEMENT EXECUTE FUNCTION refuse_regional_capacity_truncate();

CREATE FUNCTION close_serving_seats_for_physical() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
    IF NEW.fence_id IS NOT NULL THEN
        UPDATE regional_runtime_serving_seats SET fence_id=coalesce(fence_id,NEW.fence_id),
            released_at=CASE WHEN issued_at IS NULL THEN now() WHEN NEW.stop_claims IS NOT NULL THEN NEW.released_at ELSE released_at END,
            closed_by_physical_stop=(issued_at IS NOT NULL AND NEW.stop_claims IS NOT NULL)
            WHERE app_id=NEW.app_id AND permit_id=NEW.permit_id AND released_at IS NULL;
    END IF;
    RETURN NEW;
END; $$;
CREATE TRIGGER regional_capacity_close_seats AFTER UPDATE OF fence_id,released_at ON regional_runtime_capacity_receipts FOR EACH ROW EXECUTE FUNCTION close_serving_seats_for_physical();
CREATE FUNCTION fence_regional_capacity_on_retirement() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
    IF NEW.retirement IS NOT NULL THEN
        UPDATE regional_runtime_capacity_receipts SET fence_id=coalesce(fence_id,(NEW.retirement->>'RetirementID')::uuid),
            released_at=CASE WHEN issued_at IS NULL THEN coalesce(released_at,now()) ELSE released_at END
            WHERE app_id=NEW.app_id AND allocation_id=NEW.allocation_id AND fence_id IS NULL;
    END IF;
    RETURN NEW;
END; $$;
CREATE TRIGGER regional_capacity_retirement AFTER INSERT OR UPDATE OF retirement ON regional_runtime_quota_receipts FOR EACH ROW EXECUTE FUNCTION fence_regional_capacity_on_retirement();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    LOCK TABLE regional_runtime_quota_receipts,regional_runtime_capacity_receipts,regional_runtime_serving_seats IN ACCESS EXCLUSIVE MODE;
    IF EXISTS (SELECT 1 FROM regional_runtime_capacity_receipts) OR EXISTS (SELECT 1 FROM regional_runtime_serving_seats) THEN
        RAISE EXCEPTION 'cannot downgrade while regional runtime capacity receipts exist';
    END IF;
END; $$;
DROP TRIGGER regional_capacity_retirement ON regional_runtime_quota_receipts;
DROP TRIGGER regional_capacity_close_seats ON regional_runtime_capacity_receipts;
DROP TABLE regional_runtime_serving_seats;
DROP TABLE regional_runtime_capacity_receipts;
DROP FUNCTION fence_regional_capacity_on_retirement();
DROP FUNCTION guard_regional_runtime_capacity();
DROP FUNCTION refuse_regional_capacity_truncate();
DROP FUNCTION guard_regional_runtime_serving_seat();
DROP FUNCTION close_serving_seats_for_physical();
-- +goose StatementEnd
