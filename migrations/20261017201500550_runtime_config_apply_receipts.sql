-- migration-compat: expand
-- +goose Up
-- ADR-1700: each daemon instance that consumes a hot runtime-configuration
-- key records the version it applied (or the error that stopped it), so the
-- operator console can show convergence where a value is consumed instead of
-- only apid's acknowledgement. Rows are rewritten when a version changes or
-- an apply fails, never per request.
CREATE TABLE IF NOT EXISTS runtime_config_apply_receipts (
 config_key text NOT NULL CHECK (char_length(config_key) BETWEEN 1 AND 128),
 scope text NOT NULL DEFAULT 'global' CHECK (scope IN ('global', 'control_plane', 'daemon', 'node')),
 scope_id text NOT NULL DEFAULT '' CHECK (char_length(scope_id) <= 255),
 daemon text NOT NULL CHECK (daemon ~ '^[a-z][a-z0-9-]{0,63}$'),
 instance text NOT NULL CHECK (char_length(instance) BETWEEN 1 AND 255),
 version bigint NOT NULL CHECK (version >= 0),
 status text NOT NULL CHECK (status IN ('applied', 'failed')),
 error text NULL CHECK (error IS NULL OR char_length(error) <= 1024),
 reported_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY (config_key, scope, scope_id, daemon, instance)
);

-- +goose Down
DROP TABLE IF EXISTS runtime_config_apply_receipts;
