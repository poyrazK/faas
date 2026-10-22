-- +goose Up
CREATE TABLE IF NOT EXISTS managed_realtime_principal_access (
 endpoint_id uuid NOT NULL REFERENCES managed_realtime_endpoints(id) ON DELETE CASCADE,
 principal text NOT NULL CHECK (octet_length(principal) BETWEEN 1 AND 256),
 denied_channels jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(denied_channels)='array' AND jsonb_array_length(denied_channels)<=64),
 sessions_revoked_before timestamptz,
 revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(endpoint_id,principal)
);
-- +goose Down
DROP TABLE managed_realtime_principal_access;
