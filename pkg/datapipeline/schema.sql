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
