-- +goose Up
-- ADR-642: retain both independently won first fences in the signed cell stop.
ALTER TABLE regional_runtime_capacity_receipts
ADD CONSTRAINT regional_capacity_host_fence_shape CHECK (
    stop_claims IS NULL OR NOT (stop_claims ? 'HostFenceID') OR (
        jsonb_typeof(stop_claims->'HostFenceID')='string'
        AND stop_claims->>'HostFenceID' ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
        AND stop_claims->>'HostFenceID'<>'00000000-0000-0000-0000-000000000000'
        AND stop_claims->>'HostFenceID'<>stop_claims->>'FenceID'
    )
);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    LOCK TABLE regional_runtime_capacity_receipts IN ACCESS EXCLUSIVE MODE;
    IF EXISTS (SELECT 1 FROM regional_runtime_capacity_receipts WHERE stop_claims ? 'HostFenceID') THEN
        RAISE EXCEPTION 'cannot downgrade while regional runtime fence relations exist';
    END IF;
END; $$;
ALTER TABLE regional_runtime_capacity_receipts DROP CONSTRAINT regional_capacity_host_fence_shape;
-- +goose StatementEnd
