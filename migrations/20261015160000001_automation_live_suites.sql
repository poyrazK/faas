-- +goose Up
CREATE TABLE IF NOT EXISTS workflow_automation_live_suites (
 id uuid PRIMARY KEY CHECK (id <> '00000000-0000-0000-0000-000000000000'::uuid),
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 name text NOT NULL CHECK (octet_length(name) BETWEEN 1 AND 128),
 automation_version bigint NOT NULL CHECK (automation_version > 0),
 published_version bigint NOT NULL CHECK (published_version > 0 AND published_version <= automation_version),
 deployment_id uuid NOT NULL CHECK (deployment_id <> '00000000-0000-0000-0000-000000000000'::uuid),
 cases jsonb NOT NULL CHECK (jsonb_typeof(cases)='array' AND jsonb_array_length(cases) BETWEEN 1 AND 32 AND octet_length(cases::text) <= 16384),
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 FOREIGN KEY(app_id,name) REFERENCES workflow_automation_definitions(app_id,name) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS workflow_automation_live_suite_history ON workflow_automation_live_suites(app_id,name,created_at DESC,id DESC);

-- +goose Down
DROP TABLE IF EXISTS workflow_automation_live_suites;
