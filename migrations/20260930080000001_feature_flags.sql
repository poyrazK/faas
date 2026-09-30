-- +goose Up
CREATE TABLE feature_flag_versions (
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
 environment_id uuid NOT NULL REFERENCES project_environments(id) ON DELETE CASCADE,
 version bigint NOT NULL CHECK (version > 0),
 config jsonb NOT NULL CHECK (jsonb_typeof(config) = 'object'),
 actor text NOT NULL CHECK (length(actor) BETWEEN 1 AND 256),
 restored_from bigint CHECK (restored_from > 0),
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY (environment_id, version)
);
CREATE INDEX feature_flag_versions_scope ON feature_flag_versions(account_id, project_id, environment_id, version DESC);

ALTER TABLE request_telemetry ADD COLUMN flag_evidence jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(flag_evidence) = 'array');

-- +goose Down
ALTER TABLE request_telemetry DROP COLUMN flag_evidence;
DROP TABLE feature_flag_versions;
