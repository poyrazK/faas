-- +goose Up
SET LOCAL lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS data_pipeline_alerts (
 pipeline_id uuid PRIMARY KEY REFERENCES data_pipelines(id) ON DELETE CASCADE,
 account_id uuid NOT NULL,
 revision bigint NOT NULL DEFAULT 1 CHECK (revision>0),
 enabled boolean NOT NULL DEFAULT false,
 ciphertext bytea NOT NULL DEFAULT '' CHECK (octet_length(ciphertext)<=16384),
 candidate_code text NOT NULL DEFAULT '' CHECK(candidate_code IN ('','monitoring_stale','sync_blocked','retention_pressure','apply_unconfirmed','recovered')),
 candidate_since timestamptz,
 incident_id text NOT NULL DEFAULT '' CHECK(incident_id='' OR incident_id ~ '^[0-9a-f-]{36}$'),
 incident_code text NOT NULL DEFAULT '' CHECK(incident_code IN ('','monitoring_stale','sync_blocked','retention_pressure','apply_unconfirmed')),
 check_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK (NOT enabled OR octet_length(ciphertext)>0)
);
CREATE INDEX IF NOT EXISTS data_pipeline_alerts_due ON data_pipeline_alerts(check_at,pipeline_id) WHERE enabled;
CREATE TABLE IF NOT EXISTS data_pipeline_alert_deliveries (
 id uuid PRIMARY KEY,
 sequence bigint GENERATED ALWAYS AS IDENTITY UNIQUE,
 pipeline_id uuid NOT NULL REFERENCES data_pipeline_alerts(pipeline_id) ON DELETE CASCADE,
 account_id uuid NOT NULL,
 config_revision bigint NOT NULL CHECK(config_revision>0),
 incident_id uuid NOT NULL,
 kind text NOT NULL CHECK(kind IN ('incident','recovery')),
 code text NOT NULL CHECK(code IN ('monitoring_stale','sync_blocked','retention_pressure','apply_unconfirmed')),
 occurred_at timestamptz NOT NULL,
 status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','delivered','dead','cancelled')),
 attempts integer NOT NULL DEFAULT 0 CHECK(attempts BETWEEN 0 AND 7),
 response_code integer NOT NULL DEFAULT 0 CHECK(response_code BETWEEN 0 AND 599),
 result text NOT NULL DEFAULT '' CHECK(result IN ('','accepted','receiver_rejected','redirect_rejected','network_failure','credential_unavailable','attempts_exhausted','destination_changed','pipeline_deleted')),
 next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 lease_token text NOT NULL DEFAULT '',
 lease_until timestamptz,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(pipeline_id,config_revision,incident_id,kind)
);
CREATE INDEX IF NOT EXISTS data_pipeline_alert_deliveries_due ON data_pipeline_alert_deliveries(next_attempt_at,sequence) WHERE status='pending';

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS data_pipeline_alert_deliveries;
DROP TABLE IF EXISTS data_pipeline_alerts;
