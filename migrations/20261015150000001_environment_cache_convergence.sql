-- +goose Up
CREATE TABLE environment_cache_gateway_sessions (
 node_id uuid PRIMARY KEY REFERENCES compute_nodes(id) ON DELETE CASCADE,
 session_id uuid NOT NULL,
 started_at timestamptz NOT NULL,
 gateway_target_url text NOT NULL,
 heartbeat_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE environment_cache_gateway_observations (
 node_id uuid NOT NULL REFERENCES compute_nodes(id) ON DELETE CASCADE,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 environment_id uuid NOT NULL REFERENCES project_environments(id) ON DELETE CASCADE,
 session_id uuid NOT NULL,
 revision bigint NOT NULL CHECK (revision>0),
 observed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(node_id,app_id,environment_id)
);
CREATE INDEX environment_cache_gateway_observations_policy_idx ON environment_cache_gateway_observations(app_id,environment_id);

-- +goose Down
DROP TABLE environment_cache_gateway_observations;
DROP TABLE environment_cache_gateway_sessions;
