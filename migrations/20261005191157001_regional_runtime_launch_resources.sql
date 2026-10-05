-- filename: 20261005191157001_regional_runtime_launch_resources.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-635: retained resource dimensions precede issuance, never inferred on retry.
ALTER TABLE regional_runtime_launch_sources ADD COLUMN resource_claims jsonb;
ALTER TABLE regional_runtime_launch_sources ADD CONSTRAINT regional_launch_resources_shape CHECK (
    resource_claims IS NULL OR (jsonb_typeof(resource_claims)='object'
        AND octet_length(resource_claims::text)<=512
        AND resource_claims ?& ARRAY['RAMMB','VCPU','CPUMillicores']
        AND resource_claims-ARRAY['RAMMB','VCPU','CPUMillicores']='{}'::jsonb
        AND CASE WHEN jsonb_typeof(resource_claims->'RAMMB')='number' THEN
            (resource_claims->>'RAMMB')::numeric BETWEEN 1 AND 2147483647
            AND (resource_claims->>'RAMMB') ~ '^[1-9][0-9]*$'
        ELSE false END
        AND CASE WHEN jsonb_typeof(resource_claims->'VCPU')='number' THEN
            (resource_claims->>'VCPU')::numeric BETWEEN 1 AND 2147483647
            AND (resource_claims->>'VCPU') ~ '^[1-9][0-9]*$'
        ELSE false END
        AND CASE WHEN jsonb_typeof(resource_claims->'CPUMillicores')='number' THEN
            (resource_claims->>'CPUMillicores')::numeric BETWEEN 1 AND 2147483647
            AND (resource_claims->>'CPUMillicores') ~ '^[1-9][0-9]*$'
        ELSE false END));
CREATE INDEX regional_runtime_capacity_inventory_scan
    ON regional_runtime_capacity_receipts ((permit_claims->'Grant'->'Binding'->>'CellID'),app_id,permit_id)
    WHERE issued_at IS NOT NULL AND released_at IS NULL;
CREATE INDEX regional_runtime_inventory_seat_scan
    ON regional_runtime_serving_seats(app_id,permit_id)
    WHERE issued_at IS NOT NULL AND released_at IS NULL;
-- Existing immutable source guards also retain resource_claims: no update, delete or truncate.
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Resource provenance and the capacity inventory are retained on rollback.
SELECT 1;
-- +goose StatementEnd
