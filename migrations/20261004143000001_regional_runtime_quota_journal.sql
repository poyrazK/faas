-- +goose Up
-- +goose StatementBegin
-- ADR-488: signed receipt storage is not an operational scheduler fence.
CREATE TABLE regional_runtime_quota_receipts (
    app_id uuid NOT NULL CHECK (app_id <> '00000000-0000-0000-0000-000000000000'),
    allocation_id uuid NOT NULL CHECK (allocation_id <> '00000000-0000-0000-0000-000000000000'),
    generation bigint NOT NULL CHECK (generation BETWEEN 1 AND 9007199254740991),
    grant_claims jsonb NOT NULL,
    grant_envelope jsonb,
    retirement jsonb,
    retirement_envelope jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (app_id, allocation_id),
    UNIQUE (app_id, generation),
    CONSTRAINT regional_runtime_quota_grant_shape CHECK (
        jsonb_typeof(grant_claims) = 'object'
        AND octet_length(grant_claims::text) <= 4096
        AND (grant_claims->'Binding'->>'RegionalAppID') IS NOT DISTINCT FROM app_id::text
        AND (grant_claims->>'AllocationID') IS NOT DISTINCT FROM allocation_id::text
        AND (grant_claims->>'Generation') IS NOT DISTINCT FROM generation::text
    ),
    CONSTRAINT regional_runtime_quota_envelope_shape CHECK (
        (grant_envelope IS NOT NULL OR retirement IS NOT NULL)
        AND (grant_envelope IS NULL OR (jsonb_typeof(grant_envelope) = 'object' AND octet_length(grant_envelope::text) <= 8192))
        AND (retirement_envelope IS NULL OR (jsonb_typeof(retirement_envelope) = 'object' AND octet_length(retirement_envelope::text) <= 8192))
    ),
    CONSTRAINT regional_runtime_quota_retirement_shape CHECK (
        (retirement IS NULL AND retirement_envelope IS NULL) OR
        (retirement IS NOT NULL AND retirement_envelope IS NOT NULL
         AND jsonb_typeof(retirement) = 'object' AND octet_length(retirement::text) <= 8192
         AND (retirement->'Grant') IS NOT DISTINCT FROM grant_claims
         AND COALESCE((retirement->>'RetirementID')::uuid <> '00000000-0000-0000-0000-000000000000', false)
         AND COALESCE((retirement->>'RetiredAtMicros')::bigint BETWEEN 1 AND 9007199254740991, false))
    )
);
-- No FK to apps: tombstones survive app/tenant collection and delayed delivery.
CREATE TABLE regional_runtime_quota_heads (
    app_id uuid PRIMARY KEY,
    allocation_id uuid NOT NULL,
    generation bigint NOT NULL CHECK (generation BETWEEN 1 AND 9007199254740991),
    stream jsonb NOT NULL CHECK (jsonb_typeof(stream) = 'object'),
    admission_enabled boolean NOT NULL DEFAULT false CHECK (NOT admission_enabled),
    FOREIGN KEY (app_id, allocation_id) REFERENCES regional_runtime_quota_receipts(app_id, allocation_id)
);
CREATE FUNCTION regional_runtime_quota_stream(grant_arg jsonb) RETURNS jsonb
LANGUAGE sql IMMUTABLE STRICT AS $$
    SELECT (grant_arg - ARRAY['AllocationID','Generation','Policy','Concurrency','RolloutConcurrency','IssuedAtMicros','Binding'])
        || jsonb_build_object('Binding', (grant_arg->'Binding') - 'CellGeneration');
$$;
CREATE FUNCTION protect_regional_runtime_quota_receipt() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE h regional_runtime_quota_heads;
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'regional quota receipts and tombstones are retained'
            USING ERRCODE = '23514', CONSTRAINT = 'regional_runtime_quota_identity';
    END IF;
    PERFORM pg_advisory_xact_lock(hashtextextended('gregale-regional-runtime-quota:' || NEW.app_id::text, 0));
    IF TG_OP = 'UPDATE' THEN
        IF ROW(NEW.app_id, NEW.allocation_id, NEW.generation, NEW.grant_claims, NEW.grant_envelope, NEW.created_at)
             IS DISTINCT FROM ROW(OLD.app_id, OLD.allocation_id, OLD.generation, OLD.grant_claims, OLD.grant_envelope, OLD.created_at)
           OR (OLD.retirement IS NOT NULL AND ROW(NEW.retirement, NEW.retirement_envelope)
             IS DISTINCT FROM ROW(OLD.retirement, OLD.retirement_envelope)) THEN
            RAISE EXCEPTION 'regional quota authority and retirement are immutable'
                USING ERRCODE = '23514', CONSTRAINT = 'regional_runtime_quota_identity';
        END IF;
        RETURN NEW;
    END IF;
    SELECT * INTO h FROM regional_runtime_quota_heads WHERE app_id = NEW.app_id;
    IF FOUND AND (h.stream IS DISTINCT FROM regional_runtime_quota_stream(NEW.grant_claims)
        OR (NEW.retirement IS NULL AND NEW.generation <= h.generation)
        OR (NEW.generation = h.generation AND NEW.allocation_id <> h.allocation_id)) THEN
        RAISE EXCEPTION 'regional quota generation is stale or stream conflicts'
            USING ERRCODE = '23514', CONSTRAINT = 'regional_runtime_quota_order';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER regional_runtime_quota_receipt_guard BEFORE INSERT OR UPDATE OR DELETE
    ON regional_runtime_quota_receipts FOR EACH ROW EXECUTE FUNCTION protect_regional_runtime_quota_receipt();
CREATE FUNCTION protect_regional_runtime_quota_head() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'regional quota heads are retained'
            USING ERRCODE = '23514', CONSTRAINT = 'regional_runtime_quota_identity';
    END IF;
    PERFORM pg_advisory_xact_lock(hashtextextended('gregale-regional-runtime-quota:' || NEW.app_id::text, 0));
    IF TG_OP = 'UPDATE' AND (NEW.app_id <> OLD.app_id OR NEW.stream IS DISTINCT FROM OLD.stream OR NEW.generation <= OLD.generation) THEN
        RAISE EXCEPTION 'regional quota head identity is immutable and generation must increase'
            USING ERRCODE = '23514', CONSTRAINT = 'regional_runtime_quota_order';
    END IF;
    PERFORM 1 FROM regional_runtime_quota_receipts r
        WHERE r.app_id = NEW.app_id AND r.allocation_id = NEW.allocation_id
          AND r.generation = NEW.generation AND regional_runtime_quota_stream(r.grant_claims) = NEW.stream;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'regional quota head must identify its accepted receipt'
            USING ERRCODE = '23514', CONSTRAINT = 'regional_runtime_quota_order';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER regional_runtime_quota_head_guard BEFORE INSERT OR UPDATE OR DELETE
    ON regional_runtime_quota_heads FOR EACH ROW EXECUTE FUNCTION protect_regional_runtime_quota_head();
CREATE FUNCTION advance_regional_runtime_quota_head() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO regional_runtime_quota_heads(app_id, allocation_id, generation, stream)
        VALUES (NEW.app_id, NEW.allocation_id, NEW.generation, regional_runtime_quota_stream(NEW.grant_claims))
    ON CONFLICT (app_id) DO UPDATE SET allocation_id = EXCLUDED.allocation_id, generation = EXCLUDED.generation
        WHERE regional_runtime_quota_heads.generation < EXCLUDED.generation;
    RETURN NEW;
END;
$$;
CREATE TRIGGER regional_runtime_quota_advance_head AFTER INSERT
    ON regional_runtime_quota_receipts FOR EACH ROW EXECUTE FUNCTION advance_regional_runtime_quota_head();
CREATE FUNCTION refuse_regional_runtime_quota_truncate() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'regional quota authority and tombstones are retained'
        USING ERRCODE = '23514', CONSTRAINT = 'regional_runtime_quota_identity';
END;
$$;
CREATE TRIGGER regional_runtime_quota_receipt_truncate_guard BEFORE TRUNCATE
    ON regional_runtime_quota_receipts FOR EACH STATEMENT EXECUTE FUNCTION refuse_regional_runtime_quota_truncate();
CREATE TRIGGER regional_runtime_quota_head_truncate_guard BEFORE TRUNCATE
    ON regional_runtime_quota_heads FOR EACH STATEMENT EXECUTE FUNCTION refuse_regional_runtime_quota_truncate();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    LOCK TABLE regional_runtime_quota_receipts, regional_runtime_quota_heads IN ACCESS EXCLUSIVE MODE;
    IF EXISTS (SELECT 1 FROM regional_runtime_quota_receipts) OR EXISTS (SELECT 1 FROM regional_runtime_quota_heads) THEN
        RAISE EXCEPTION 'cannot downgrade while regional runtime quota authority or tombstones exist';
    END IF;
END;
$$;
DROP TABLE regional_runtime_quota_heads;
DROP TABLE regional_runtime_quota_receipts;
DROP FUNCTION advance_regional_runtime_quota_head();
DROP FUNCTION protect_regional_runtime_quota_head();
DROP FUNCTION protect_regional_runtime_quota_receipt();
DROP FUNCTION refuse_regional_runtime_quota_truncate();
DROP FUNCTION regional_runtime_quota_stream(jsonb);
-- +goose StatementEnd
