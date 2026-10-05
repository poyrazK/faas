-- +goose Up
-- +goose StatementBegin
-- ADR-626: immutable retry identity; no runtime quota activation.
CREATE TABLE regional_runtime_serving_resumes (
    app_id uuid NOT NULL,
    resume_id uuid NOT NULL CHECK (resume_id<>'00000000-0000-0000-0000-000000000000'),
    permit_id uuid NOT NULL,
    seat_id uuid NOT NULL,
    intent_claims jsonb NOT NULL CHECK (jsonb_typeof(intent_claims)='object' AND octet_length(intent_claims::text)<=16384),
    created_at timestamptz NOT NULL DEFAULT now() CHECK (isfinite(created_at)),
    PRIMARY KEY (app_id,resume_id),
    UNIQUE (app_id,seat_id),
    FOREIGN KEY (app_id,permit_id) REFERENCES regional_runtime_capacity_receipts(app_id,permit_id),
    FOREIGN KEY (app_id,seat_id) REFERENCES regional_runtime_serving_seats(app_id,seat_id),
    CONSTRAINT regional_resume_shape CHECK (
        ROW(intent_claims->>'ID',intent_claims->'Seat'->>'ID',intent_claims->'Seat'->'Permit'->>'ID',intent_claims->'Seat'->'Permit'->'Grant'->'Binding'->>'RegionalAppID')
        IS NOT DISTINCT FROM ROW(resume_id::text,seat_id::text,permit_id::text,app_id::text))
);
-- Attempts survive app, instance and node collection; closures live on seats.
CREATE INDEX regional_resume_permit ON regional_runtime_serving_resumes(app_id,permit_id);
CREATE FUNCTION guard_regional_runtime_serving_resume() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE p regional_runtime_capacity_receipts; s regional_runtime_serving_seats;
    r regional_runtime_quota_receipts; h regional_runtime_quota_heads;
BEGIN
    IF TG_OP<>'INSERT' THEN
        RAISE EXCEPTION 'regional serving resume intents are retained and immutable'
            USING ERRCODE='23514',CONSTRAINT='regional_capacity_identity';
    END IF;
    IF current_setting('transaction_isolation')<>'read committed' THEN
        RAISE EXCEPTION 'regional serving resume requires read committed isolation'
            USING ERRCODE='23514',CONSTRAINT='regional_capacity_frozen';
    END IF;
    PERFORM pg_advisory_xact_lock(hashtextextended('gregale-regional-runtime-quota:'||NEW.app_id::text,0));
    SELECT * INTO p FROM regional_runtime_capacity_receipts WHERE app_id=NEW.app_id AND permit_id=NEW.permit_id;
    IF NOT FOUND OR p.issued_at IS NULL OR p.fence_id IS NOT NULL OR p.released_at IS NOT NULL
        OR p.kind NOT IN ('serving','warm') OR p.permit_claims IS DISTINCT FROM NEW.intent_claims->'Seat'->'Permit' THEN
        RAISE EXCEPTION 'regional resume requires its exact issued unfenced physical permit'
            USING ERRCODE='23514',CONSTRAINT='regional_capacity_identity';
    END IF;
    SELECT * INTO s FROM regional_runtime_serving_seats WHERE app_id=NEW.app_id AND seat_id=NEW.seat_id;
    IF NOT FOUND OR s.permit_id<>NEW.permit_id OR s.issued_at IS NULL OR s.fence_id IS NOT NULL
        OR s.released_at IS NOT NULL OR s.closed_by_physical_stop OR s.seat_claims IS DISTINCT FROM NEW.intent_claims->'Seat' THEN
        RAISE EXCEPTION 'regional resume requires its exact issued unfenced seat'
            USING ERRCODE='23514',CONSTRAINT='regional_capacity_identity';
    END IF;
    SELECT * INTO r FROM regional_runtime_quota_receipts WHERE app_id=NEW.app_id AND allocation_id=p.allocation_id;
    SELECT * INTO h FROM regional_runtime_quota_heads WHERE app_id=NEW.app_id;
    IF NOT FOUND OR h.allocation_id<>p.allocation_id OR h.generation<>p.generation OR r.grant_envelope IS NULL OR r.retirement IS NOT NULL THEN
        RAISE EXCEPTION 'regional resume grant is retired or superseded'
            USING ERRCODE='23514',CONSTRAINT='regional_capacity_frozen';
    END IF;
    PERFORM 1 FROM apps a WHERE a.id=NEW.app_id AND a.status IN ('active','evicted_cold')
        AND a.account_id::text=r.grant_claims->'Binding'->>'RegionalAccountID'
        AND coalesce(a.org_id::text,'')=r.grant_claims->'Binding'->>'RegionalOrgID'
        AND a.slug=r.grant_claims->'Binding'->>'RegionalAppSlug' FOR SHARE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'regional resume app binding is inactive'
            USING ERRCODE='23514',CONSTRAINT='regional_capacity_frozen';
    END IF;
    IF coalesce(r.grant_claims->'Binding'->>'RegionalOrgID','')<>'' THEN
        PERFORM 1 FROM orgs WHERE id=(r.grant_claims->'Binding'->>'RegionalOrgID')::uuid AND status='active' AND NOT deleted_pending FOR SHARE;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'regional resume tenant is inactive'
                USING ERRCODE='23514',CONSTRAINT='regional_capacity_frozen';
        END IF;
    END IF;
    RETURN NEW;
END; $$;
CREATE TRIGGER regional_serving_resume_guard BEFORE INSERT OR UPDATE OR DELETE ON regional_runtime_serving_resumes FOR EACH ROW EXECUTE FUNCTION guard_regional_runtime_serving_resume();
CREATE TRIGGER regional_serving_resume_truncate_guard BEFORE TRUNCATE ON regional_runtime_serving_resumes FOR EACH STATEMENT EXECUTE FUNCTION guard_regional_runtime_serving_resume();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    LOCK TABLE regional_runtime_quota_receipts,regional_runtime_capacity_receipts,regional_runtime_serving_seats,regional_runtime_serving_resumes IN ACCESS EXCLUSIVE MODE;
    IF EXISTS (SELECT 1 FROM regional_runtime_serving_resumes) THEN
        RAISE EXCEPTION 'cannot downgrade while regional serving resume intents exist';
    END IF;
END; $$;
DROP TABLE regional_runtime_serving_resumes;
DROP FUNCTION guard_regional_runtime_serving_resume();
-- +goose StatementEnd
