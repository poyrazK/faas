-- +goose Up
ALTER TABLE managed_postgres_checkpoint_maintenance
 ALTER COLUMN reserved_by_operation_id DROP NOT NULL,
 ADD COLUMN IF NOT EXISTS reserved_by_cutover_id uuid REFERENCES managed_postgres_cutovers(id) ON DELETE RESTRICT;

-- +goose StatementBegin
DO $migration$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint
  WHERE conrelid='managed_postgres_checkpoint_maintenance'::regclass
   AND conname='managed_postgres_maintenance_original_owner') THEN
  ALTER TABLE managed_postgres_checkpoint_maintenance
   ADD CONSTRAINT managed_postgres_maintenance_original_owner CHECK (
    (reserved_by_operation_id IS NOT NULL)::integer+(reserved_by_cutover_id IS NOT NULL)::integer=1);
 END IF;
END;
$migration$;
-- +goose StatementEnd

CREATE TABLE IF NOT EXISTS managed_postgres_cutover_source_fences (
 cutover_id uuid PRIMARY KEY REFERENCES managed_postgres_cutovers(id) ON DELETE RESTRICT,
 owner_token uuid NOT NULL UNIQUE CHECK(owner_token<>'00000000-0000-0000-0000-000000000000'),
 maintenance_id uuid NOT NULL REFERENCES managed_postgres_checkpoint_maintenance(id) ON DELETE RESTRICT,
 source_database_id uuid NOT NULL REFERENCES managed_postgres_databases(id) ON DELETE RESTRICT,
 backend_id text NOT NULL CHECK(backend_id<>''),
 backend_fingerprint text NOT NULL CHECK(backend_fingerprint ~ '^[a-f0-9]{64}$'),
 source_provider_resource_id text NOT NULL CHECK(source_provider_resource_id<>''),
 source_data_resource_id text NOT NULL CHECK(source_data_resource_id<>''),
 source_generation bigint NOT NULL CHECK(source_generation>0),
 source_spec jsonb NOT NULL CHECK(jsonb_typeof(source_spec)='object'),
 owner_node_id uuid NOT NULL,
 owner_revision bigint NOT NULL CHECK(owner_revision>0),
 admission_fenced_at timestamptz NOT NULL,
 runtime_fingerprint text NOT NULL CHECK(runtime_fingerprint ~ '^[a-f0-9]{64}$'),
 state text NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','closing','closed','abandoning','released')),
 database_names text[] NOT NULL DEFAULT '{}',
 close_requested_at timestamptz,
 closure jsonb CHECK(closure IS NULL OR jsonb_typeof(closure)='object'),
 observed_at timestamptz,
 last_error_code text NOT NULL DEFAULT '' CHECK(last_error_code ~ '^[a-z_]*$'),
 remote_terminal_state text CHECK(remote_terminal_state IN ('released','abandoned','not_dispatched')),
 remote_released_at timestamptz,
 lease_token text,
 lease_until timestamptz,
 attempt_count integer NOT NULL DEFAULT 0 CHECK(attempt_count>=0),
 retry_at timestamptz NOT NULL,
 created_at timestamptz NOT NULL,
 updated_at timestamptz NOT NULL,
 CHECK((lease_token IS NULL)=(lease_until IS NULL)),
 CHECK((closure IS NULL)=(observed_at IS NULL)),
 CHECK(state<>'closed' OR (close_requested_at IS NOT NULL AND closure IS NOT NULL)),
 CHECK(close_requested_at IS NULL OR cardinality(database_names)>0),
 CHECK((state='released' AND remote_terminal_state IS NOT NULL AND
   ((remote_terminal_state='not_dispatched' AND close_requested_at IS NULL AND remote_released_at IS NULL)
    OR (remote_terminal_state IN ('released','abandoned') AND close_requested_at IS NOT NULL AND remote_released_at IS NOT NULL)))
  OR (state<>'released' AND remote_terminal_state IS NULL AND remote_released_at IS NULL))
);
CREATE UNIQUE INDEX IF NOT EXISTS managed_postgres_cutover_source_fence_dataset
 ON managed_postgres_cutover_source_fences(backend_id,backend_fingerprint,source_data_resource_id) WHERE state<>'released';
CREATE INDEX IF NOT EXISTS managed_postgres_cutover_source_fence_due
 ON managed_postgres_cutover_source_fences(retry_at,cutover_id) WHERE state<>'released';

-- Both source-hold families must serialize even when catalog aliases differ.
-- +goose StatementBegin
DO $migration$
BEGIN
 IF to_regprocedure('guard_managed_postgres_source_fence_owner()') IS NULL THEN
CREATE FUNCTION guard_managed_postgres_source_fence_owner() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.state='released' THEN RETURN NEW; END IF;
 IF current_setting('transaction_isolation')<>'read committed' THEN
  RAISE EXCEPTION 'source fence insertion requires read committed' USING ERRCODE='55000';
 END IF;
 PERFORM pg_advisory_xact_lock(hashtextextended(NEW.backend_id||':'||NEW.backend_fingerprint||':'||NEW.source_data_resource_id,1334));
 IF TG_TABLE_NAME='managed_postgres_cutover_source_fences' THEN
  IF EXISTS(SELECT 1 FROM project_environment_clone_postgres_write_fences
    WHERE backend_id=NEW.backend_id AND backend_fingerprint=NEW.backend_fingerprint
      AND source_data_resource_id=NEW.source_data_resource_id AND state<>'released') THEN
   RAISE EXCEPTION 'source dataset already held' USING ERRCODE='55000';
  END IF;
 ELSE
  IF EXISTS(SELECT 1 FROM managed_postgres_cutover_source_fences
    WHERE backend_id=NEW.backend_id AND backend_fingerprint=NEW.backend_fingerprint
      AND source_data_resource_id=NEW.source_data_resource_id AND state<>'released') THEN
   RAISE EXCEPTION 'source dataset already held' USING ERRCODE='55000';
  END IF;
 END IF;
 RETURN NEW;
END $$;
 END IF;
END;
$migration$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $migration$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_trigger
  WHERE tgrelid='managed_postgres_cutover_source_fences'::regclass AND tgname='managed_postgres_cutover_sql_owner') THEN
CREATE TRIGGER managed_postgres_cutover_sql_owner BEFORE INSERT OR UPDATE OF state,backend_id,backend_fingerprint,source_data_resource_id ON managed_postgres_cutover_source_fences
 FOR EACH ROW EXECUTE FUNCTION guard_managed_postgres_source_fence_owner();
 END IF;
END;
$migration$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $migration$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_trigger
  WHERE tgrelid='project_environment_clone_postgres_write_fences'::regclass AND tgname='managed_postgres_clone_sql_owner') THEN
CREATE TRIGGER managed_postgres_clone_sql_owner BEFORE INSERT OR UPDATE OF state,backend_id,backend_fingerprint,source_data_resource_id ON project_environment_clone_postgres_write_fences
 FOR EACH ROW EXECUTE FUNCTION guard_managed_postgres_source_fence_owner();
 END IF;
END;
$migration$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $migration$
BEGIN
 IF to_regprocedure('guard_managed_postgres_source_fence_evidence()') IS NULL THEN
CREATE FUNCTION guard_managed_postgres_source_fence_evidence() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP IN ('DELETE','TRUNCATE') THEN
  RAISE EXCEPTION 'source fence recovery evidence is retained' USING ERRCODE='55000';
 END IF;
 IF OLD.state='released' OR ROW(NEW.cutover_id,NEW.owner_token,NEW.maintenance_id,NEW.source_database_id,
   NEW.backend_id,NEW.backend_fingerprint,NEW.source_provider_resource_id,NEW.source_data_resource_id,NEW.source_generation,
   NEW.source_spec,NEW.owner_node_id,NEW.owner_revision,NEW.admission_fenced_at,NEW.runtime_fingerprint,NEW.created_at)
  IS DISTINCT FROM ROW(OLD.cutover_id,OLD.owner_token,OLD.maintenance_id,OLD.source_database_id,
   OLD.backend_id,OLD.backend_fingerprint,OLD.source_provider_resource_id,OLD.source_data_resource_id,OLD.source_generation,
   OLD.source_spec,OLD.owner_node_id,OLD.owner_revision,OLD.admission_fenced_at,OLD.runtime_fingerprint,OLD.created_at)
  OR (cardinality(OLD.database_names)>0 AND NEW.database_names IS DISTINCT FROM OLD.database_names)
  OR (OLD.close_requested_at IS NOT NULL AND NEW.close_requested_at IS DISTINCT FROM OLD.close_requested_at)
  OR (OLD.state='abandoning' AND NEW.state NOT IN ('abandoning','released'))
  OR (OLD.state IN ('closing','closed') AND NEW.state='pending') THEN
  RAISE EXCEPTION 'source fence identity or terminal state changed' USING ERRCODE='55000';
 END IF;
 RETURN NEW;
END $$;
 END IF;
END;
$migration$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $migration$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_trigger
  WHERE tgrelid='managed_postgres_cutover_source_fences'::regclass AND tgname='managed_postgres_source_fence_evidence') THEN
CREATE TRIGGER managed_postgres_source_fence_evidence BEFORE UPDATE OR DELETE ON managed_postgres_cutover_source_fences
 FOR EACH ROW EXECUTE FUNCTION guard_managed_postgres_source_fence_evidence();
 END IF;
END;
$migration$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $migration$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_trigger
  WHERE tgrelid='managed_postgres_cutover_source_fences'::regclass AND tgname='managed_postgres_source_fence_truncate') THEN
CREATE TRIGGER managed_postgres_source_fence_truncate BEFORE TRUNCATE ON managed_postgres_cutover_source_fences
 FOR EACH STATEMENT EXECUTE FUNCTION guard_managed_postgres_source_fence_evidence();
 END IF;
END;
$migration$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $migration$
BEGIN
 IF to_regprocedure('guard_managed_postgres_source_fence_cancellation()') IS NULL THEN
CREATE FUNCTION guard_managed_postgres_source_fence_cancellation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.state='cancelling' AND OLD.state<>'cancelling' THEN
  UPDATE managed_postgres_cutover_source_fences SET state='abandoning',lease_token=NULL,lease_until=NULL,
   retry_at=clock_timestamp(),updated_at=clock_timestamp() WHERE cutover_id=NEW.id AND state<>'released';
 END IF;
 IF NEW.state='cancelled' AND EXISTS(SELECT 1 FROM managed_postgres_cutover_source_fences WHERE cutover_id=NEW.id AND state<>'released') THEN
  RAISE EXCEPTION 'source SQL fence cleanup is incomplete' USING ERRCODE='55000';
 END IF;
 RETURN NEW;
END $$;
 END IF;
END;
$migration$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $migration$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_trigger
  WHERE tgrelid='managed_postgres_cutovers'::regclass AND tgname='managed_postgres_source_fence_cancellation') THEN
CREATE TRIGGER managed_postgres_source_fence_cancellation BEFORE UPDATE ON managed_postgres_cutovers
 FOR EACH ROW EXECUTE FUNCTION guard_managed_postgres_source_fence_cancellation();
 END IF;
END;
$migration$;
-- +goose StatementEnd

ALTER TABLE managed_postgres_cutover_preflights
 ADD COLUMN IF NOT EXISTS source_sessions jsonb,
 ADD COLUMN IF NOT EXISTS source_sessions_fingerprint text NOT NULL DEFAULT '';

-- +goose StatementBegin
DO $migration$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint
  WHERE conrelid='managed_postgres_cutover_preflights'::regclass
   AND conname='managed_postgres_preflight_source_sessions') THEN
  ALTER TABLE managed_postgres_cutover_preflights
   ADD CONSTRAINT managed_postgres_preflight_source_sessions CHECK(
  (source_sessions IS NULL AND source_sessions_fingerprint='') OR
  (source_sessions IS NOT NULL AND jsonb_typeof(source_sessions)='object' AND source_sessions_fingerprint ~ '^[a-f0-9]{64}$'));
 END IF;
 IF NOT EXISTS (SELECT 1 FROM pg_constraint
  WHERE conrelid='managed_postgres_cutover_preflights'::regclass
   AND conname='managed_postgres_preflight_pending_source_sessions') THEN
  ALTER TABLE managed_postgres_cutover_preflights
   ADD CONSTRAINT managed_postgres_preflight_pending_source_sessions CHECK(state<>'pending' OR source_sessions IS NULL);
 END IF;
END;
$migration$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM managed_postgres_cutover_source_fences WHERE state<>'released') OR
    EXISTS(SELECT 1 FROM managed_postgres_checkpoint_maintenance WHERE reserved_by_cutover_id IS NOT NULL AND state<>'reserved') THEN
  RAISE EXCEPTION 'cutover SQL recovery authority must be retained';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE managed_postgres_cutover_preflights DROP CONSTRAINT managed_postgres_preflight_pending_source_sessions,
 DROP CONSTRAINT managed_postgres_preflight_source_sessions,DROP COLUMN source_sessions_fingerprint,DROP COLUMN source_sessions;
DROP TRIGGER managed_postgres_source_fence_cancellation ON managed_postgres_cutovers;
DROP FUNCTION guard_managed_postgres_source_fence_cancellation();
DROP TRIGGER managed_postgres_clone_sql_owner ON project_environment_clone_postgres_write_fences;
DROP TABLE managed_postgres_cutover_source_fences;
DROP FUNCTION guard_managed_postgres_source_fence_evidence();
DROP FUNCTION guard_managed_postgres_source_fence_owner();
DELETE FROM managed_postgres_checkpoint_maintenance WHERE reserved_by_cutover_id IS NOT NULL;
ALTER TABLE managed_postgres_checkpoint_maintenance DROP CONSTRAINT managed_postgres_maintenance_original_owner,
 DROP COLUMN reserved_by_cutover_id,ALTER COLUMN reserved_by_operation_id SET NOT NULL;
