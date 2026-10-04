-- +goose Up
-- Runtime scaling history belongs to one environment lifetime and is never
-- materialized from the source during cloning.
CREATE TABLE runtime_environment_scaling_states (
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    environment_key text NOT NULL,
    environment_id uuid REFERENCES project_environments(id) ON DELETE CASCADE,
    scope text NOT NULL CHECK (scope ~ '^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$' AND scope <> 'default'),
    last_scale_in_at timestamptz,
    last_scale_out_at timestamptz,
    PRIMARY KEY(app_id, environment_key),
    CHECK (environment_key = CASE WHEN environment_id IS NULL THEN 'scope:' || scope ELSE 'environment:' || environment_id::text END)
);

-- +goose Down
DROP TABLE runtime_environment_scaling_states;
