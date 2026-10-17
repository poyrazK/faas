-- Compile-time projection of consumer-group messages for bulk recovery queries.
-- Runtime schema is owned by migrations 20261012102700470, 20261012102700490,
-- 20261012102700494, and 20261012102700501. This file never executes against a database.
CREATE TABLE managed_realtime_consumer_group_messages (
 group_id uuid NOT NULL,
 sequence bigint NOT NULL,
 status text NOT NULL,
 dead_since timestamptz,
 attempts integer NOT NULL,
 failure_code text NOT NULL,
 ordering_key text NOT NULL,
 lease_until timestamptz,
 worker text NOT NULL,
 token_hash bytea,
 credential_id uuid,
 available_at timestamptz NOT NULL
);
ALTER TABLE managed_realtime_consumer_group_messages ADD COLUMN delivery_deadline timestamptz, ADD COLUMN deadline_invalid boolean NOT NULL DEFAULT false;
-- Claim-rate projection; runtime columns are owned by migration 20261012102700503.
CREATE TABLE managed_realtime_consumer_groups (
 id uuid NOT NULL,
 claim_rate_per_second integer NOT NULL,
 claim_rate_burst integer NOT NULL,
 claim_rate_tat timestamptz NOT NULL,
 throttled_claim_requests bigint NOT NULL
);
-- Filter projection; runtime columns are owned by migration 20261012102700506.
ALTER TABLE managed_realtime_consumer_groups ADD COLUMN message_filter jsonb NOT NULL, ADD COLUMN assigned_sequence bigint NOT NULL, ADD COLUMN filtered_messages bigint NOT NULL;
-- Worker-health projection; runtime schema is owned by migration 20261012102700509.
ALTER TABLE managed_realtime_consumer_groups ADD COLUMN endpoint_id uuid NOT NULL,ADD COLUMN channel text NOT NULL,ADD COLUMN name text NOT NULL,ADD COLUMN next_worker_health_check_at timestamptz NOT NULL;
CREATE TABLE managed_realtime_worker_heartbeats (id uuid NOT NULL,group_id uuid NOT NULL,worker text NOT NULL,principal text NOT NULL,credential_id uuid,ready boolean NOT NULL,status_code text NOT NULL,last_seen_at timestamptz NOT NULL,lost boolean NOT NULL,UNIQUE(group_id,worker,principal));
CREATE TABLE managed_realtime_worker_credentials (id uuid NOT NULL,group_id uuid NOT NULL,expires_at timestamptz NOT NULL,revoked_at timestamptz,permissions text[] NOT NULL);
CREATE TABLE managed_realtime_endpoints (id uuid NOT NULL,account_id uuid NOT NULL,app_id uuid NOT NULL,enabled boolean NOT NULL);
CREATE TABLE accounts (id uuid NOT NULL,status text NOT NULL,plan text NOT NULL);
CREATE TABLE apps (id uuid NOT NULL,status text NOT NULL);

-- Metrics projection; runtime migration 20261012102700512.
ALTER TABLE managed_realtime_consumer_group_messages ADD COLUMN processing_started_at timestamptz,ADD COLUMN first_claim_observed boolean NOT NULL,ADD COLUMN message jsonb NOT NULL;
CREATE TABLE managed_realtime_group_metrics(group_id uuid NOT NULL,bucket_at timestamptz NOT NULL,first_claims bigint NOT NULL,completed bigint NOT NULL,retries bigint NOT NULL,dead_letters bigint NOT NULL,queue_milliseconds numeric NOT NULL,processing_milliseconds numeric NOT NULL);

-- Existing schedule/head projection for inspector discovery (no runtime schema changes).
CREATE TABLE managed_realtime_schedules(endpoint_id uuid NOT NULL,channel text NOT NULL,status text NOT NULL,updated_at timestamptz NOT NULL);
CREATE TABLE managed_realtime_channel_heads(endpoint_id uuid NOT NULL,channel text NOT NULL,oldest_sequence bigint NOT NULL,next_sequence bigint NOT NULL);

-- Priority projection; runtime migration 20261012102700515.
ALTER TABLE managed_realtime_consumer_groups ADD COLUMN priority_metadata_key text NOT NULL,ADD COLUMN priority_turn integer NOT NULL;
ALTER TABLE managed_realtime_consumer_group_messages ADD COLUMN priority integer NOT NULL;

-- Existing worker credential fields for fresh batch authorization.
ALTER TABLE managed_realtime_worker_credentials ADD COLUMN token_hash bytea NOT NULL;

-- Circuit projection; runtime migration 20261012102700517.
ALTER TABLE managed_realtime_consumer_groups ADD COLUMN circuit_enabled boolean NOT NULL,ADD COLUMN circuit_threshold integer NOT NULL,ADD COLUMN circuit_cooldown_seconds integer NOT NULL,ADD COLUMN circuit_failures integer NOT NULL,ADD COLUMN circuit_state text NOT NULL,ADD COLUMN circuit_open_until timestamptz,ADD COLUMN circuit_probe_sequence bigint,ADD COLUMN circuit_probe_fencing_token bigint,ADD COLUMN circuit_reason text NOT NULL;
ALTER TABLE managed_realtime_consumer_group_messages ADD COLUMN fencing_token bigint;

-- Delivery history projection; runtime migration 20261012102700520.
ALTER TABLE managed_realtime_consumer_groups ADD COLUMN history_pruned_events bigint NOT NULL;
CREATE TABLE managed_realtime_group_delivery_history(id bigint NOT NULL,group_id uuid NOT NULL,sequence bigint NOT NULL,event text NOT NULL,attempt integer NOT NULL,fencing_token bigint,failure_code text NOT NULL,recorded_at timestamptz NOT NULL);

ALTER TABLE managed_realtime_consumer_group_messages ADD COLUMN release_count bigint NOT NULL DEFAULT 0;
CREATE TABLE managed_realtime_group_settlement_receipts (
 group_id uuid NOT NULL,principal text NOT NULL,idempotency_key uuid NOT NULL,
 request_hash bytea NOT NULL,result jsonb NOT NULL,created_at timestamptz NOT NULL,expires_at timestamptz NOT NULL,
 PRIMARY KEY(group_id,principal,idempotency_key)
);
ALTER TABLE managed_realtime_consumer_groups ADD COLUMN deduplication_metadata_key text NOT NULL DEFAULT '',
 ADD COLUMN deduplication_window_seconds integer NOT NULL DEFAULT 0,ADD COLUMN duplicate_messages bigint NOT NULL DEFAULT 0;
CREATE TABLE managed_realtime_group_event_ids (group_id uuid NOT NULL,event_hash bytea NOT NULL,expires_at timestamptz NOT NULL,PRIMARY KEY(group_id,event_hash));
-- Existing fields required by read-only dead-letter export; no runtime migration.
ALTER TABLE managed_realtime_consumer_groups ADD COLUMN dead_letter_retention_seconds integer NOT NULL;

CREATE TABLE managed_realtime_replay_jobs (
 id uuid PRIMARY KEY,
 group_id uuid NOT NULL REFERENCES managed_realtime_consumer_groups(id) ON DELETE CASCADE,
 state text NOT NULL CHECK (state IN ('running','paused','completed','cancelled')),
 rate integer NOT NULL CHECK (rate BETWEEN 1 AND 32),
 selection jsonb NOT NULL CHECK(jsonb_typeof(selection)='array' AND jsonb_array_length(selection) BETWEEN 1 AND 128),
 outcomes jsonb NOT NULL DEFAULT '[]' CHECK(jsonb_typeof(outcomes)='array' AND jsonb_array_length(outcomes)<=128),
 error_count bigint NOT NULL DEFAULT 0 CHECK(error_count>=0),
 last_error text NOT NULL DEFAULT '',
 position integer NOT NULL DEFAULT 0 CHECK(position BETWEEN 0 AND 128),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 next_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX realtime_replay_jobs_due ON managed_realtime_replay_jobs(next_at) WHERE state='running';

ALTER TABLE managed_realtime_replay_jobs
 ADD COLUMN dry_run boolean NOT NULL DEFAULT false,
 ADD COLUMN reviewed_job_id uuid,
 ADD COLUMN started_job_id uuid;
ALTER TABLE managed_realtime_replay_jobs DROP CONSTRAINT managed_realtime_replay_jobs_state_check;
ALTER TABLE managed_realtime_replay_jobs ADD CONSTRAINT managed_realtime_replay_jobs_state_check
 CHECK(state IN ('running','paused','checking','check_paused','completed','cancelled'));
ALTER TABLE managed_realtime_replay_jobs ADD CONSTRAINT realtime_replay_job_mode_state_check
 CHECK((dry_run AND state IN ('checking','check_paused','completed','cancelled')) OR
 (NOT dry_run AND state IN ('running','paused','completed','cancelled')));

-- IDs link bounded history without retaining reports or cascading deletion of live jobs.

-- Usage projection (ADR-1323); runtime migration 20261014101101862.
CREATE TABLE realtime_usage_hours (
 endpoint_id uuid NOT NULL,
 hour timestamptz NOT NULL,
 node_name text NOT NULL,
 incarnation uuid NOT NULL,
 account_id uuid NOT NULL,
 app_id uuid NOT NULL,
 minute_peak_connections integer[] NOT NULL,
 connection_seconds bigint NOT NULL,
 delivered_messages bigint NOT NULL,
 delivered_bytes bigint NOT NULL,
 received_messages bigint NOT NULL,
 received_bytes bigint NOT NULL,
 reported_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY (endpoint_id, hour, node_name, incarnation)
);

-- Fair-use connection projection (ADR-1323); runtime migration 20261014194829908.
CREATE TABLE realtime_node_connection_syncs (node_name text PRIMARY KEY, synced_at timestamptz NOT NULL);
CREATE TABLE realtime_node_connections (node_name text NOT NULL, account_id uuid NOT NULL, connections integer NOT NULL, PRIMARY KEY (node_name, account_id));
