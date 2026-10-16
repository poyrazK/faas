-- filename: 20261016153050995_environment_cache_purges.sql

-- +goose Up
CREATE TABLE IF NOT EXISTS environment_cache_purges (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY CHECK (id>0 AND id<=9007199254740991),
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
 environment_id uuid NOT NULL REFERENCES project_environments(id) ON DELETE CASCADE,
 request_id uuid NOT NULL,
 path text NOT NULL CHECK (octet_length(path)<=1024),
 requested_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(account_id,request_id)
);
CREATE INDEX IF NOT EXISTS environment_cache_purges_target_idx ON environment_cache_purges(app_id,environment_id,id);
CREATE TABLE IF NOT EXISTS environment_cache_purge_watermarks (
 node_id uuid PRIMARY KEY REFERENCES compute_nodes(id) ON DELETE CASCADE,
 session_id uuid NOT NULL,
 last_id bigint NOT NULL CHECK (last_id>=0 AND last_id<=9007199254740991),
 observed_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
-- +goose Down
DROP TABLE environment_cache_purge_watermarks;
DROP TABLE environment_cache_purges;
