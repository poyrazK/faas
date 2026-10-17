-- +goose Up
CREATE TABLE IF NOT EXISTS workflow_automation_live_verifications (
 run_id uuid PRIMARY KEY REFERENCES workflow_runs(id) ON DELETE CASCADE,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 name text NOT NULL CHECK (length(name) BETWEEN 1 AND 128),
 case_name text NOT NULL CHECK (octet_length(case_name) BETWEEN 1 AND 128),
 automation_version bigint NOT NULL CHECK (automation_version > 0),
 published_version bigint NOT NULL CHECK (published_version > 0 AND published_version <= automation_version),
 deployment_id uuid NOT NULL CHECK (deployment_id <> '00000000-0000-0000-0000-000000000000'::uuid),
 inputs_hash text NOT NULL CHECK (inputs_hash ~ '^[0-9a-f]{64}$'),
 live_test jsonb NOT NULL CHECK (jsonb_typeof(live_test)='object'),
 idempotency_key text NOT NULL CHECK (octet_length(idempotency_key) BETWEEN 1 AND 255),
 request_fingerprint bytea NOT NULL CHECK (octet_length(request_fingerprint)=32),
 report jsonb CHECK (report IS NULL OR jsonb_typeof(report)='object'),
 created_at timestamptz NOT NULL DEFAULT now(),
 FOREIGN KEY(app_id,name) REFERENCES workflow_automation_definitions(app_id,name) ON DELETE CASCADE,
 UNIQUE(app_id,name,idempotency_key)
);
CREATE INDEX IF NOT EXISTS workflow_automation_live_history ON workflow_automation_live_verifications(app_id,name,created_at DESC,run_id DESC);

-- +goose Down
DROP TABLE IF EXISTS workflow_automation_live_verifications;
