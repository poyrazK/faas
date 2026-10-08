-- filename: 20261008114924486_runtime_upgrade_forwarding_drains.sql

-- +goose Up
-- adr: 697
ALTER TABLE deployments ADD COLUMN runtime_upgrade_routing_token uuid NOT NULL DEFAULT gen_random_uuid()
 CHECK(runtime_upgrade_routing_token<>'00000000-0000-0000-0000-000000000000'::uuid);
-- +goose StatementBegin
CREATE FUNCTION renew_runtime_upgrade_routing_token() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 NEW.runtime_upgrade_routing_token:=gen_random_uuid();
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER deployments_runtime_upgrade_routing_token BEFORE INSERT OR UPDATE ON deployments
 FOR EACH ROW EXECUTE FUNCTION renew_runtime_upgrade_routing_token();

CREATE TABLE runtime_upgrade_gateway_drains (
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 gateway_session_id uuid NOT NULL CHECK(gateway_session_id<>'00000000-0000-0000-0000-000000000000'::uuid),
 slot_id uuid NOT NULL CHECK(slot_id<>'00000000-0000-0000-0000-000000000000'::uuid),
 operation_id uuid NOT NULL REFERENCES runtime_upgrade_operations(id) ON DELETE CASCADE,
 deployment_id uuid NOT NULL REFERENCES deployment_runtime_upgrade_cutovers(deployment_id) ON DELETE CASCADE,
 serving_deployment_id uuid NOT NULL REFERENCES deployments(id) ON DELETE CASCADE CHECK(serving_deployment_id<>deployment_id),
 gateway_roster_revision uuid NOT NULL REFERENCES runtime_upgrade_gateway_rosters(revision),
 routing_revision text NOT NULL CHECK(routing_revision~'^[0-9a-f]{64}$'),
 fence_id uuid NOT NULL CHECK(fence_id<>'00000000-0000-0000-0000-000000000000'::uuid),
 activity_version text NOT NULL CHECK(activity_version~'^[1-9][0-9]{0,19}$' AND activity_version::numeric<=18446744073709551615),
 active_forwards integer NOT NULL DEFAULT 0 CHECK(active_forwards=0),
 cutover_at timestamptz NOT NULL CHECK(isfinite(cutover_at)),
 observed_at timestamptz NOT NULL CHECK(isfinite(observed_at)),
 expires_at timestamptz NOT NULL CHECK(isfinite(expires_at) AND expires_at=observed_at+interval '1 minute'),
 PRIMARY KEY(app_id,gateway_session_id)
);
CREATE INDEX runtime_upgrade_gateway_drains_expiry ON runtime_upgrade_gateway_drains(expires_at);
-- +goose Down
-- Forward-only: old observations must not become reusable after schema rollback.
SELECT 1;
