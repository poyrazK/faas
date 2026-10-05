-- filename: 20261005150729071_regional_runtime_launch_sources.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-633: actual native lookup keys precede issuance and outlive current rows.
CREATE TABLE regional_runtime_launch_sources (
    app_id uuid NOT NULL,
    permit_id uuid NOT NULL,
    instance_key text NOT NULL CHECK (
        instance_key ~ '^([0-9a-f]{32}|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})$'
        AND instance_key::uuid <> '00000000-0000-0000-0000-000000000000'),
    permit_claims jsonb NOT NULL CHECK (jsonb_typeof(permit_claims)='object' AND octet_length(permit_claims::text)<=4096),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (app_id,permit_id),
    FOREIGN KEY (app_id,permit_id) REFERENCES regional_runtime_capacity_receipts(app_id,permit_id)
);
-- No mutable app, instance, deployment or node foreign key, and no backfill.
CREATE FUNCTION guard_regional_runtime_launch_source() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE p regional_runtime_capacity_receipts;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'original regional launch sources are immutable and retained'
            USING ERRCODE='23514',CONSTRAINT='regional_capacity_identity';
    END IF;
    IF current_setting('transaction_isolation')<>'read committed' THEN
        RAISE EXCEPTION 'regional launch sources require read committed isolation'
            USING ERRCODE='23514',CONSTRAINT='regional_capacity_frozen';
    END IF;
    PERFORM pg_advisory_xact_lock(hashtextextended('gregale-regional-runtime-quota:'||NEW.app_id::text,0));
    SELECT * INTO p FROM regional_runtime_capacity_receipts WHERE app_id=NEW.app_id AND permit_id=NEW.permit_id;
    IF NOT FOUND OR NEW.permit_claims IS DISTINCT FROM p.permit_claims
        OR NEW.instance_key::uuid IS DISTINCT FROM p.instance_id
        OR p.issued_at IS NOT NULL OR p.fence_id IS NOT NULL OR p.released_at IS NOT NULL THEN
        RAISE EXCEPTION 'regional launch source must bind the original unissued unfenced permit'
            USING ERRCODE='23514',CONSTRAINT='regional_capacity_identity';
    END IF;
    RETURN NEW;
END; $$;
CREATE TRIGGER regional_launch_source_guard BEFORE INSERT OR UPDATE OR DELETE ON regional_runtime_launch_sources
    FOR EACH ROW EXECUTE FUNCTION guard_regional_runtime_launch_source();
CREATE TRIGGER regional_launch_source_truncate_guard BEFORE TRUNCATE ON regional_runtime_launch_sources
    FOR EACH STATEMENT EXECUTE FUNCTION refuse_regional_capacity_truncate();
CREATE INDEX regional_runtime_capacity_retirement_scan ON regional_runtime_capacity_receipts(app_id,permit_id)
    WHERE issued_at IS NOT NULL AND fence_id IS NOT NULL AND released_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Retain original launch identity and issued capacity evidence on rollback.
SELECT 1;
-- +goose StatementEnd
-- (No destructive down migration.)
