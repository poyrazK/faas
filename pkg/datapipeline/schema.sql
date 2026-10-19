-- The projection is also the source for the migration's owned tables.
CREATE TABLE data_pipeline_connections (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL,
 name text NOT NULL CHECK (name ~ '^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$'),
 revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
 ciphertext bytea NOT NULL CHECK (octet_length(ciphertext) BETWEEN 1 AND 16384),
 identity text NOT NULL CHECK (identity ~ '^[0-9a-f]{64}$'),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(account_id,name), UNIQUE(account_id,id)
);
CREATE TABLE data_pipelines (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL,
 name text NOT NULL CHECK (name ~ '^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$'),
 source_connection_id uuid NOT NULL,
 database_id uuid NOT NULL,
 tables jsonb NOT NULL CHECK (jsonb_typeof(tables)='array' AND jsonb_array_length(tables) BETWEEN 1 AND 16),
 read_subjects jsonb NOT NULL CHECK (jsonb_typeof(read_subjects)='array' AND jsonb_array_length(read_subjects) BETWEEN 1 AND 32),
 desired_state text NOT NULL DEFAULT 'running' CHECK (desired_state IN ('running','paused','deleted')),
 state text NOT NULL DEFAULT 'validating' CHECK (state IN ('validating','copying','catching_up','live','paused','blocked','deleting','deleted','resync_required')),
 revision bigint NOT NULL DEFAULT 1 CHECK (revision>0),
 observed_revision bigint NOT NULL DEFAULT 0 CHECK (observed_revision>=0 AND observed_revision<=revision),
 generation bigint NOT NULL DEFAULT 1 CHECK (generation>0),
 reason text NOT NULL DEFAULT '' CHECK (reason ~ '^[a-z0-9_]{0,80}$'),
 tables_ready integer NOT NULL DEFAULT 0 CHECK (tables_ready BETWEEN 0 AND 16),
 retained_wal_bytes bigint CHECK (retained_wal_bytes>=0),
 applied_through_at timestamptz,
 verification_progress jsonb NOT NULL DEFAULT '{}' CHECK(jsonb_typeof(verification_progress)='object' AND octet_length(verification_progress::text)<=4194304),
 reader_change jsonb NOT NULL DEFAULT '{}' CHECK(jsonb_typeof(reader_change)='object' AND octet_length(reader_change::text)<=131072 AND (reader_change='{}' OR reader_change->>'State' IN ('preview','approved','applied'))),
 schema_approval text NOT NULL DEFAULT '' CHECK(schema_approval='' OR schema_approval ~ '^[a-f0-9]{64}$'),
 schema_change jsonb NOT NULL DEFAULT '{}' CHECK(jsonb_typeof(schema_change)='object' AND octet_length(schema_change::text)<=4194304),
 observed_at timestamptz,
 verification text NOT NULL DEFAULT 'unknown' CHECK (verification IN ('unknown','pending','passed','mismatch','inconclusive')),
 verified_at timestamptz,
 initialized boolean NOT NULL DEFAULT false,
 verify_requested boolean NOT NULL DEFAULT false,
 lease_token text NOT NULL DEFAULT '',
 lease_until timestamptz,
 retry_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(account_id,name),
 FOREIGN KEY(account_id,source_connection_id) REFERENCES data_pipeline_connections(account_id,id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX data_pipelines_owned_target ON data_pipelines(database_id) WHERE state<>'deleted';
CREATE INDEX data_pipelines_due ON data_pipelines(retry_at,id) WHERE state<>'deleted';

CREATE TABLE data_connection_checks (
 connection_id uuid PRIMARY KEY,
 account_id uuid NOT NULL,
 id uuid NOT NULL UNIQUE,
 connection_revision bigint NOT NULL CHECK (connection_revision>0),
 database_id text NOT NULL DEFAULT '' CHECK (database_id='' OR database_id ~ '^[0-9a-f-]{36}$'),
 state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending','running','passed','blocked')),
 result jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(result)='object' AND octet_length(result::text)<=4194304),
 lease_token text NOT NULL DEFAULT '',
 lease_until timestamptz,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 observed_at timestamptz,
 FOREIGN KEY(account_id,connection_id) REFERENCES data_pipeline_connections(account_id,id) ON DELETE CASCADE
);
CREATE INDEX data_connection_checks_due ON data_connection_checks(created_at,connection_id) WHERE state IN ('pending','running');

CREATE TABLE data_pipeline_setups (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL,
 source_connection_id uuid NOT NULL,
 name text NOT NULL,
 request jsonb NOT NULL CHECK (jsonb_typeof(request)='object' AND octet_length(request::text)<=32768),
 mirror_plan jsonb NOT NULL CHECK (jsonb_typeof(mirror_plan)='object'),
 data_api_plan jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(data_api_plan)='object' AND octet_length(data_api_plan::text)<=16384),
 database_id uuid NOT NULL,
 mirror_name text NOT NULL,
 pipeline_id text NOT NULL DEFAULT '' CHECK (pipeline_id='' OR pipeline_id ~ '^[0-9a-f-]{36}$'),
 state text NOT NULL DEFAULT 'working' CHECK (state IN ('working','blocked','ready','cancelled')),
 phase text NOT NULL DEFAULT 'checking_source' CHECK (phase IN ('checking_source','provisioning','checking_mirror','starting','syncing','configuring_api','deploying_api','waiting_api','api_failed')),
 reason text NOT NULL DEFAULT '' CHECK (reason ~ '^[a-z0-9_]{0,80}$'),
 revision bigint NOT NULL DEFAULT 1 CHECK (revision>0),
 lease_token text NOT NULL DEFAULT '',
 lease_until timestamptz,
 retry_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(account_id,name),
 FOREIGN KEY(account_id,source_connection_id) REFERENCES data_pipeline_connections(account_id,id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX data_pipeline_setups_source_active ON data_pipeline_setups(source_connection_id) WHERE state IN ('working','blocked');
CREATE INDEX data_pipeline_setups_due ON data_pipeline_setups(retry_at,id) WHERE state='working';

CREATE TABLE data_pipeline_schema_refreshes (
 pipeline_id uuid NOT NULL REFERENCES data_pipelines(id) ON DELETE CASCADE,
 change_id text NOT NULL CHECK(change_id ~ '^[a-f0-9]{64}$'),
 app_id uuid NOT NULL,
 attempt integer NOT NULL CHECK(attempt BETWEEN 0 AND 10),
 wake_id uuid NOT NULL UNIQUE,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(pipeline_id,change_id,attempt,app_id)
);

CREATE TABLE data_pipeline_alerts (
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
CREATE INDEX data_pipeline_alerts_due ON data_pipeline_alerts(check_at,pipeline_id) WHERE enabled;
CREATE TABLE data_pipeline_alert_deliveries (
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
CREATE INDEX data_pipeline_alert_deliveries_due ON data_pipeline_alert_deliveries(next_attempt_at,sequence) WHERE status='pending';
