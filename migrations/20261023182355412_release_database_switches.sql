-- migration-compat: contract
-- filename: 20261023182355412_release_database_switches.sql
--
-- ADR-20261023181931: rollbacks that restore the database. A rollback switch
-- activates its managed PostgreSQL cutover, so cutovers gain the one-way
-- `activated` state and their staged credentials a `published` state with no
-- envelope. `activated`, like `cancelled`, releases the admission fence and
-- the database and binding pins, and no longer counts as an open cutover for
-- the per-app uniqueness index or capacity qualification.
--
-- The two state CHECKs are replaced online: each widened copy is added NOT
-- VALID and validated, the old one is dropped, and the copy takes the old
-- name. The open-cutover index is rebuilt concurrently under a new name
-- before the old one is dropped. Every step commits on its own and is
-- guarded, so a rerun after a partial failure converges. The previous release
-- never writes the new states and survives every step, but RENAME CONSTRAINT
-- is a contract statement to migration-lint, so a full release applies this
-- migration.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_cutovers'::regclass AND conname = 'managed_postgres_cutovers_state_next_chk')
    AND position('activated' in pg_get_constraintdef((SELECT oid FROM pg_constraint WHERE conrelid = 'managed_postgres_cutovers'::regclass AND conname = 'managed_postgres_cutovers_state_check'))) = 0 THEN
  ALTER TABLE managed_postgres_cutovers ADD CONSTRAINT managed_postgres_cutovers_state_next_chk
    CHECK (state IN ('preparing','prepared','verifying','verified','cancelling','cancelled','activated')) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_cutovers'::regclass AND conname = 'managed_postgres_cutovers_state_next_chk') THEN
  ALTER TABLE managed_postgres_cutovers VALIDATE CONSTRAINT managed_postgres_cutovers_state_next_chk;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_cutovers'::regclass AND conname = 'managed_postgres_cutovers_state_next_chk') THEN
  ALTER TABLE managed_postgres_cutovers DROP CONSTRAINT IF EXISTS managed_postgres_cutovers_state_check;
  ALTER TABLE managed_postgres_cutovers RENAME CONSTRAINT managed_postgres_cutovers_state_next_chk TO managed_postgres_cutovers_state_check;
 END IF;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_cutover_credentials'::regclass AND conname = 'managed_postgres_cutover_credentials_state_next_chk')
    AND position('published' in pg_get_constraintdef((SELECT oid FROM pg_constraint WHERE conrelid = 'managed_postgres_cutover_credentials'::regclass AND conname = 'managed_postgres_cutover_credentials_state_check'))) = 0 THEN
  ALTER TABLE managed_postgres_cutover_credentials ADD CONSTRAINT managed_postgres_cutover_credentials_state_next_chk
    CHECK (state IN ('pending','sealed','revoked','published')) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_cutover_credentials'::regclass AND conname = 'managed_postgres_cutover_credentials_state_next_chk') THEN
  ALTER TABLE managed_postgres_cutover_credentials VALIDATE CONSTRAINT managed_postgres_cutover_credentials_state_next_chk;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_cutover_credentials'::regclass AND conname = 'managed_postgres_cutover_credentials_state_next_chk') THEN
  ALTER TABLE managed_postgres_cutover_credentials DROP CONSTRAINT IF EXISTS managed_postgres_cutover_credentials_state_check;
  ALTER TABLE managed_postgres_cutover_credentials RENAME CONSTRAINT managed_postgres_cutover_credentials_state_next_chk TO managed_postgres_cutover_credentials_state_check;
 END IF;
END $$;
-- +goose StatementEnd

-- A published credential carries no envelope: app_secrets holds it now.
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_cutover_credentials'::regclass AND conname = 'managed_postgres_cutover_credentials_envelope_next_chk')
    AND position('published' in pg_get_constraintdef((SELECT oid FROM pg_constraint WHERE conrelid = 'managed_postgres_cutover_credentials'::regclass AND conname = 'managed_postgres_cutover_credentials_check'))) = 0 THEN
  ALTER TABLE managed_postgres_cutover_credentials ADD CONSTRAINT managed_postgres_cutover_credentials_envelope_next_chk CHECK (
    (state = 'sealed' AND num_nonnulls(provider_identity_id, credential_ref, ciphertext, kid, value_hash) = 5
      AND length(provider_identity_id) > 0 AND length(credential_ref) > 0 AND length(ciphertext) > 0
      AND length(kid) > 0 AND length(value_hash) > 0)
    OR (state IN ('pending','revoked','published') AND provider_identity_id IS NULL AND credential_ref IS NULL
      AND ciphertext IS NULL AND kid IS NULL AND value_hash IS NULL)
  ) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_cutover_credentials'::regclass AND conname = 'managed_postgres_cutover_credentials_envelope_next_chk') THEN
  ALTER TABLE managed_postgres_cutover_credentials VALIDATE CONSTRAINT managed_postgres_cutover_credentials_envelope_next_chk;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_cutover_credentials'::regclass AND conname = 'managed_postgres_cutover_credentials_envelope_next_chk') THEN
  ALTER TABLE managed_postgres_cutover_credentials DROP CONSTRAINT IF EXISTS managed_postgres_cutover_credentials_check;
  ALTER TABLE managed_postgres_cutover_credentials RENAME CONSTRAINT managed_postgres_cutover_credentials_envelope_next_chk TO managed_postgres_cutover_credentials_check;
 END IF;
END $$;
-- +goose StatementEnd

-- One open cutover per app and scope; an activated cutover is history.
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS managed_postgres_cutovers_open_app_scope_idx
  ON managed_postgres_cutovers (app_id, scope) WHERE state NOT IN ('cancelled','activated');
DROP INDEX CONCURRENTLY IF EXISTS managed_postgres_cutovers_active_app_scope_idx;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_managed_postgres_admission_fence() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE intent managed_postgres_cutovers%ROWTYPE; checked_at timestamptz;
BEGIN
 IF NEW.managed_postgres_admission_cutover_id IS NOT DISTINCT FROM OLD.managed_postgres_admission_cutover_id THEN
  IF NEW.managed_postgres_admission_fenced_at IS DISTINCT FROM OLD.managed_postgres_admission_fenced_at
   OR (OLD.managed_postgres_admission_cutover_id IS NOT NULL AND ROW(NEW.id,NEW.account_id) IS DISTINCT FROM ROW(OLD.id,OLD.account_id)) THEN
   RAISE EXCEPTION 'admission fence is immutable' USING ERRCODE='23514', CONSTRAINT='managed_postgres_cutover_conflict';
  END IF;
  RETURN NEW;
 END IF;
 IF OLD.managed_postgres_admission_cutover_id IS NOT NULL THEN
  SELECT * INTO intent FROM managed_postgres_cutovers WHERE id=OLD.managed_postgres_admission_cutover_id FOR SHARE;
  IF NEW.managed_postgres_admission_cutover_id IS NOT NULL OR intent.state IS NULL OR intent.state NOT IN ('cancelled','activated') THEN
   RAISE EXCEPTION 'admission fence cannot be released' USING ERRCODE='23514', CONSTRAINT='managed_postgres_cutover_conflict';
  END IF;
 ELSE
  SELECT * INTO intent FROM managed_postgres_cutovers WHERE id=NEW.managed_postgres_admission_cutover_id FOR SHARE;
  checked_at := clock_timestamp();
  IF intent.app_id IS DISTINCT FROM NEW.id OR intent.account_id IS DISTINCT FROM NEW.account_id
   OR intent.state IS DISTINCT FROM 'verified' OR intent.lease_token IS NOT NULL
   OR intent.verified_at IS NULL OR intent.verified_at>checked_at OR intent.verified_at<checked_at-interval '5 minutes'
   OR NEW.status NOT IN ('active','evicted_cold')
   OR NOT EXISTS (SELECT 1 FROM managed_postgres_cutover_credentials WHERE cutover_id=intent.id)
   OR EXISTS (SELECT 1 FROM managed_postgres_cutover_credentials WHERE cutover_id=intent.id
    AND (state<>'sealed' OR verified_at IS NULL OR verified_at>checked_at OR verified_at<checked_at-interval '5 minutes')) THEN
   RAISE EXCEPTION 'cutover verification is not fresh' USING ERRCODE='23514', CONSTRAINT='managed_postgres_cutover_conflict';
  END IF;
  NEW.managed_postgres_admission_fenced_at := checked_at;
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_managed_postgres_cutover_binding() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE pinned uuid;
BEGIN
 IF TG_OP IN ('UPDATE','DELETE') AND OLD.cutover_id IS NOT NULL THEN
  IF TG_OP='DELETE' OR (to_jsonb(NEW)-'cutover_id') IS DISTINCT FROM (to_jsonb(OLD)-'cutover_id')
   OR (NEW.cutover_id IS DISTINCT FROM OLD.cutover_id AND (NEW.cutover_id IS NOT NULL OR NOT EXISTS (SELECT 1 FROM managed_postgres_cutovers WHERE id=OLD.cutover_id AND state IN ('cancelled','activated')))) THEN
   RAISE EXCEPTION 'binding is pinned by a cutover' USING ERRCODE='23514', CONSTRAINT='managed_postgres_cutover_conflict';
  END IF;
 END IF;
 IF TG_OP='INSERT' OR (TG_OP='UPDATE' AND ROW(NEW.database_id,NEW.app_id,NEW.scope,NEW.environment_key) IS DISTINCT FROM ROW(OLD.database_id,OLD.app_id,OLD.scope,OLD.environment_key)) THEN
  SELECT cutover_id INTO pinned FROM managed_postgres_databases WHERE id=NEW.database_id FOR KEY SHARE;
  IF pinned IS NOT NULL THEN
   RAISE EXCEPTION 'database is pinned by a cutover' USING ERRCODE='23514', CONSTRAINT='managed_postgres_cutover_conflict';
  END IF;
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_managed_postgres_cutover_database() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
 IF OLD.cutover_id IS NOT NULL THEN
  IF TG_OP='DELETE' OR ROW(NEW.account_id,NEW.state,NEW.backend_id,NEW.backend_fingerprint,NEW.provider_resource_id,NEW.desired_generation,NEW.region,NEW.postgres_major,NEW.service_class,NEW.availability,NEW.scale_to_zero,NEW.storage_limit_bytes,NEW.restore_window_seconds)
     IS DISTINCT FROM ROW(OLD.account_id,OLD.state,OLD.backend_id,OLD.backend_fingerprint,OLD.provider_resource_id,OLD.desired_generation,OLD.region,OLD.postgres_major,OLD.service_class,OLD.availability,OLD.scale_to_zero,OLD.storage_limit_bytes,OLD.restore_window_seconds)
     OR (NEW.cutover_id IS DISTINCT FROM OLD.cutover_id AND (NEW.cutover_id IS NOT NULL OR NOT EXISTS (SELECT 1 FROM managed_postgres_cutovers WHERE id=OLD.cutover_id AND state IN ('cancelled','activated')))) THEN
   RAISE EXCEPTION 'database is pinned by a cutover' USING ERRCODE='23514', CONSTRAINT='managed_postgres_cutover_conflict';
  END IF;
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_managed_postgres_source_fence_cancellation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
 IF NEW.state='cancelling' AND OLD.state<>'cancelling' THEN
  UPDATE managed_postgres_cutover_source_fences SET state='abandoning',lease_token=NULL,lease_until=NULL,
   retry_at=clock_timestamp(),updated_at=clock_timestamp() WHERE cutover_id=NEW.id AND state<>'released';
 END IF;
 IF NEW.state IN ('cancelled','activated') AND EXISTS(SELECT 1 FROM managed_postgres_cutover_source_fences WHERE cutover_id=NEW.id AND state<>'released') THEN
  RAISE EXCEPTION 'source SQL fence cleanup is incomplete' USING ERRCODE='55000';
 END IF;
 IF OLD.state='activated' AND NEW.state<>'activated' THEN
  RAISE EXCEPTION 'an activated cutover is final' USING ERRCODE='23514', CONSTRAINT='managed_postgres_cutover_conflict';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION postgres_capacity_reserve(kind text, owner uuid, ep text, rk text, place text, gen bigint, conns bigint, sl bigint, se bigint, wo bigint) RETURNS void
    LANGUAGE plpgsql
    AS $$
DECLARE r postgres_capacity_resources; used record; held postgres_capacity_reservations;
BEGIN
 PERFORM pg_advisory_xact_lock(51733);
 SELECT * INTO held FROM postgres_capacity_reservations WHERE owner_kind=kind AND owner_id=owner AND endpoint=ep;
 IF FOUND THEN
  IF (held.resource_key,held.placement,held.generation,held.connections,held.slots,held.senders,held.workers) IS DISTINCT FROM (rk,place,gen,conns,sl,se,wo) THEN
   RAISE EXCEPTION 'capacity placement changed' USING ERRCODE='PCC02';
  END IF;
  RETURN;
 END IF;
 SELECT * INTO r FROM postgres_capacity_resources WHERE resource_key=rk FOR UPDATE;
 IF NOT FOUND OR r.placement<>place OR r.generation<>gen OR r.observed_at>clock_timestamp() OR r.expires_at<=clock_timestamp() THEN
  RAISE EXCEPTION 'capacity requires qualification' USING ERRCODE='PCC02';
 END IF;
 -- A legacy native generation or unbounded credential must be accounted for
 -- before ANY new consumer can claim this allocation.
 IF EXISTS(SELECT 1 FROM data_pipelines p WHERE p.state<>'deleted' AND
  (NOT EXISTS(SELECT 1 FROM postgres_capacity_reservations h WHERE h.owner_kind='pipeline' AND h.owner_id=p.id AND h.endpoint='source') OR
   NOT EXISTS(SELECT 1 FROM postgres_capacity_reservations h WHERE h.owner_kind='pipeline' AND h.owner_id=p.id AND h.endpoint='target')))
 OR EXISTS(SELECT 1 FROM managed_postgres_cutovers c JOIN postgres_capacity_targets t ON t.database_id IN(c.source_database_id,c.target_database_id) WHERE t.resource_key=rk AND c.state NOT IN ('cancelled','activated'))
 OR EXISTS(SELECT 1 FROM managed_postgres_bindings b JOIN postgres_capacity_targets t ON t.database_id=b.database_id
  WHERE t.resource_key=rk AND b.state<>'deleted' AND b.connection_limit=0) THEN
  RAISE EXCEPTION 'existing consumers require qualification' USING ERRCODE='PCC02';
 END IF;
 SELECT coalesce(sum(connections),0) AS c,coalesce(sum(slots),0) AS sl,coalesce(sum(senders),0) AS se,coalesce(sum(workers),0) AS wo INTO used
 FROM postgres_capacity_reservations WHERE resource_key=rk;
 IF used.c+conns>r.connections OR used.sl+sl>r.slots OR used.se+se>r.senders OR used.wo+wo>r.workers THEN
  RAISE EXCEPTION 'insufficient database capacity' USING ERRCODE='PCC01';
 END IF;
 INSERT INTO postgres_capacity_reservations(owner_kind,owner_id,endpoint,resource_key,placement,generation,connections,slots,senders,workers)
 VALUES(kind,owner,ep,rk,place,gen,conns,sl,se,wo);
END; $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS release_database_switches (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 scope text NOT NULL CHECK (scope ~ '^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$'),
 from_deployment_id uuid REFERENCES deployments(id) ON DELETE SET NULL,
 target_deployment_id uuid NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
 undone_deployment_ids uuid[] NOT NULL CHECK (cardinality(undone_deployment_ids) BETWEEN 1 AND 64),
 source_database_id uuid NOT NULL,
 target_database_id uuid,
 target_name text NOT NULL CHECK (length(target_name) BETWEEN 1 AND 63),
 restore_point timestamptz NOT NULL,
 cutover_id uuid,
 phase text NOT NULL CHECK (phase IN ('restoring','staging','verifying','fencing','settling','draining',
   'switching','resuming','aborting','completed','failed','cancelled')),
 failure_code text NOT NULL DEFAULT '' CHECK (failure_code ~ '^[a-z_]{0,63}$'),
 failure_detail text NOT NULL DEFAULT '' CHECK (length(failure_detail) <= 500),
 requested_by text NOT NULL DEFAULT '' CHECK (length(requested_by) <= 200),
 cancel_requested_at timestamptz,
 fenced_at timestamptz,
 drain_requested_at timestamptz,
 release_switched_at timestamptz,
 activated_at timestamptz,
 finished_at timestamptz,
 phase_started_at timestamptz NOT NULL,
 drain_retired integer NOT NULL DEFAULT 0 CHECK (drain_retired >= 0),
 drain_parked integer NOT NULL DEFAULT 0 CHECK (drain_parked >= 0),
 drain_pending integer NOT NULL DEFAULT 0 CHECK (drain_pending >= 0),
 drain_blocked integer NOT NULL DEFAULT 0 CHECK (drain_blocked >= 0),
 attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count BETWEEN 0 AND 1000000),
 last_error_code text NOT NULL DEFAULT '' CHECK (last_error_code ~ '^[a-z_]{0,63}$'),
 lease_token text,
 lease_until timestamptz,
 retry_at timestamptz NOT NULL,
 created_at timestamptz NOT NULL,
 updated_at timestamptz NOT NULL,
 CHECK ((lease_token IS NULL) = (lease_until IS NULL)),
 CHECK ((phase IN ('completed','failed','cancelled')) = (finished_at IS NOT NULL)),
 CHECK (phase NOT IN ('failed','aborting') OR failure_code <> ''),
 CHECK (activated_at IS NULL OR phase IN ('switching','resuming','completed')),
 CHECK (phase <> 'completed' OR (activated_at IS NOT NULL AND release_switched_at IS NOT NULL
   AND target_database_id IS NOT NULL AND cutover_id IS NOT NULL))
);
-- +goose StatementEnd
CREATE UNIQUE INDEX IF NOT EXISTS release_database_switches_active_app
  ON release_database_switches (app_id) WHERE phase NOT IN ('completed','failed','cancelled');
CREATE INDEX IF NOT EXISTS release_database_switches_due
  ON release_database_switches (retry_at, id) WHERE phase NOT IN ('completed','failed','cancelled');
CREATE INDEX IF NOT EXISTS release_database_switches_app
  ON release_database_switches (app_id, created_at DESC, id);
CREATE INDEX IF NOT EXISTS release_database_switches_account
  ON release_database_switches (account_id);
CREATE INDEX IF NOT EXISTS release_database_switches_target_deployment
  ON release_database_switches (target_deployment_id);
CREATE INDEX IF NOT EXISTS release_database_switches_from_deployment
  ON release_database_switches (from_deployment_id) WHERE from_deployment_id IS NOT NULL;

-- +goose Down
SET lock_timeout = '5s';
-- A rollback cannot narrow the CHECKs again once a cutover was activated;
-- refuse instead of rewriting that history.
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM managed_postgres_cutovers WHERE state = 'activated') THEN
  RAISE EXCEPTION 'activated cutovers exist; their history cannot be narrowed';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE IF EXISTS release_database_switches;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_managed_postgres_admission_fence() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE intent managed_postgres_cutovers%ROWTYPE; checked_at timestamptz;
BEGIN
 IF NEW.managed_postgres_admission_cutover_id IS NOT DISTINCT FROM OLD.managed_postgres_admission_cutover_id THEN
  IF NEW.managed_postgres_admission_fenced_at IS DISTINCT FROM OLD.managed_postgres_admission_fenced_at
   OR (OLD.managed_postgres_admission_cutover_id IS NOT NULL AND ROW(NEW.id,NEW.account_id) IS DISTINCT FROM ROW(OLD.id,OLD.account_id)) THEN
   RAISE EXCEPTION 'admission fence is immutable' USING ERRCODE='23514', CONSTRAINT='managed_postgres_cutover_conflict';
  END IF;
  RETURN NEW;
 END IF;
 IF OLD.managed_postgres_admission_cutover_id IS NOT NULL THEN
  SELECT * INTO intent FROM managed_postgres_cutovers WHERE id=OLD.managed_postgres_admission_cutover_id FOR SHARE;
  IF NEW.managed_postgres_admission_cutover_id IS NOT NULL OR intent.state IS DISTINCT FROM 'cancelled' THEN
   RAISE EXCEPTION 'admission fence cannot be released' USING ERRCODE='23514', CONSTRAINT='managed_postgres_cutover_conflict';
  END IF;
 ELSE
  SELECT * INTO intent FROM managed_postgres_cutovers WHERE id=NEW.managed_postgres_admission_cutover_id FOR SHARE;
  checked_at := clock_timestamp();
  IF intent.app_id IS DISTINCT FROM NEW.id OR intent.account_id IS DISTINCT FROM NEW.account_id
   OR intent.state IS DISTINCT FROM 'verified' OR intent.lease_token IS NOT NULL
   OR intent.verified_at IS NULL OR intent.verified_at>checked_at OR intent.verified_at<checked_at-interval '5 minutes'
   OR NEW.status NOT IN ('active','evicted_cold')
   OR NOT EXISTS (SELECT 1 FROM managed_postgres_cutover_credentials WHERE cutover_id=intent.id)
   OR EXISTS (SELECT 1 FROM managed_postgres_cutover_credentials WHERE cutover_id=intent.id
    AND (state<>'sealed' OR verified_at IS NULL OR verified_at>checked_at OR verified_at<checked_at-interval '5 minutes')) THEN
   RAISE EXCEPTION 'cutover verification is not fresh' USING ERRCODE='23514', CONSTRAINT='managed_postgres_cutover_conflict';
  END IF;
  NEW.managed_postgres_admission_fenced_at := checked_at;
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_managed_postgres_cutover_binding() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE pinned uuid;
BEGIN
 IF TG_OP IN ('UPDATE','DELETE') AND OLD.cutover_id IS NOT NULL THEN
  IF TG_OP='DELETE' OR (to_jsonb(NEW)-'cutover_id') IS DISTINCT FROM (to_jsonb(OLD)-'cutover_id')
   OR (NEW.cutover_id IS DISTINCT FROM OLD.cutover_id AND (NEW.cutover_id IS NOT NULL OR NOT EXISTS (SELECT 1 FROM managed_postgres_cutovers WHERE id=OLD.cutover_id AND state='cancelled'))) THEN
   RAISE EXCEPTION 'binding is pinned by a cutover' USING ERRCODE='23514', CONSTRAINT='managed_postgres_cutover_conflict';
  END IF;
 END IF;
 IF TG_OP='INSERT' OR (TG_OP='UPDATE' AND ROW(NEW.database_id,NEW.app_id,NEW.scope,NEW.environment_key) IS DISTINCT FROM ROW(OLD.database_id,OLD.app_id,OLD.scope,OLD.environment_key)) THEN
  SELECT cutover_id INTO pinned FROM managed_postgres_databases WHERE id=NEW.database_id FOR KEY SHARE;
  IF pinned IS NOT NULL THEN
   RAISE EXCEPTION 'database is pinned by a cutover' USING ERRCODE='23514', CONSTRAINT='managed_postgres_cutover_conflict';
  END IF;
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_managed_postgres_cutover_database() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
 IF OLD.cutover_id IS NOT NULL THEN
  IF TG_OP='DELETE' OR ROW(NEW.account_id,NEW.state,NEW.backend_id,NEW.backend_fingerprint,NEW.provider_resource_id,NEW.desired_generation,NEW.region,NEW.postgres_major,NEW.service_class,NEW.availability,NEW.scale_to_zero,NEW.storage_limit_bytes,NEW.restore_window_seconds)
     IS DISTINCT FROM ROW(OLD.account_id,OLD.state,OLD.backend_id,OLD.backend_fingerprint,OLD.provider_resource_id,OLD.desired_generation,OLD.region,OLD.postgres_major,OLD.service_class,OLD.availability,OLD.scale_to_zero,OLD.storage_limit_bytes,OLD.restore_window_seconds)
     OR (NEW.cutover_id IS DISTINCT FROM OLD.cutover_id AND (NEW.cutover_id IS NOT NULL OR NOT EXISTS (SELECT 1 FROM managed_postgres_cutovers WHERE id=OLD.cutover_id AND state='cancelled'))) THEN
   RAISE EXCEPTION 'database is pinned by a cutover' USING ERRCODE='23514', CONSTRAINT='managed_postgres_cutover_conflict';
  END IF;
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_managed_postgres_source_fence_cancellation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
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
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION postgres_capacity_reserve(kind text, owner uuid, ep text, rk text, place text, gen bigint, conns bigint, sl bigint, se bigint, wo bigint) RETURNS void
    LANGUAGE plpgsql
    AS $$
DECLARE r postgres_capacity_resources; used record; held postgres_capacity_reservations;
BEGIN
 PERFORM pg_advisory_xact_lock(51733);
 SELECT * INTO held FROM postgres_capacity_reservations WHERE owner_kind=kind AND owner_id=owner AND endpoint=ep;
 IF FOUND THEN
  IF (held.resource_key,held.placement,held.generation,held.connections,held.slots,held.senders,held.workers) IS DISTINCT FROM (rk,place,gen,conns,sl,se,wo) THEN
   RAISE EXCEPTION 'capacity placement changed' USING ERRCODE='PCC02';
  END IF;
  RETURN;
 END IF;
 SELECT * INTO r FROM postgres_capacity_resources WHERE resource_key=rk FOR UPDATE;
 IF NOT FOUND OR r.placement<>place OR r.generation<>gen OR r.observed_at>clock_timestamp() OR r.expires_at<=clock_timestamp() THEN
  RAISE EXCEPTION 'capacity requires qualification' USING ERRCODE='PCC02';
 END IF;
 -- A legacy native generation or unbounded credential must be accounted for
 -- before ANY new consumer can claim this allocation.
 IF EXISTS(SELECT 1 FROM data_pipelines p WHERE p.state<>'deleted' AND
  (NOT EXISTS(SELECT 1 FROM postgres_capacity_reservations h WHERE h.owner_kind='pipeline' AND h.owner_id=p.id AND h.endpoint='source') OR
   NOT EXISTS(SELECT 1 FROM postgres_capacity_reservations h WHERE h.owner_kind='pipeline' AND h.owner_id=p.id AND h.endpoint='target')))
 OR EXISTS(SELECT 1 FROM managed_postgres_cutovers c JOIN postgres_capacity_targets t ON t.database_id IN(c.source_database_id,c.target_database_id) WHERE t.resource_key=rk AND c.state<>'cancelled')
 OR EXISTS(SELECT 1 FROM managed_postgres_bindings b JOIN postgres_capacity_targets t ON t.database_id=b.database_id
  WHERE t.resource_key=rk AND b.state<>'deleted' AND b.connection_limit=0) THEN
  RAISE EXCEPTION 'existing consumers require qualification' USING ERRCODE='PCC02';
 END IF;
 SELECT coalesce(sum(connections),0) AS c,coalesce(sum(slots),0) AS sl,coalesce(sum(senders),0) AS se,coalesce(sum(workers),0) AS wo INTO used
 FROM postgres_capacity_reservations WHERE resource_key=rk;
 IF used.c+conns>r.connections OR used.sl+sl>r.slots OR used.se+se>r.senders OR used.wo+wo>r.workers THEN
  RAISE EXCEPTION 'insufficient database capacity' USING ERRCODE='PCC01';
 END IF;
 INSERT INTO postgres_capacity_reservations(owner_kind,owner_id,endpoint,resource_key,placement,generation,connections,slots,senders,workers)
 VALUES(kind,owner,ep,rk,place,gen,conns,sl,se,wo);
END; $$;
-- +goose StatementEnd
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS managed_postgres_cutovers_active_app_scope_idx
  ON managed_postgres_cutovers (app_id, scope) WHERE state <> 'cancelled';
DROP INDEX CONCURRENTLY IF EXISTS managed_postgres_cutovers_open_app_scope_idx;
-- +goose StatementBegin
DO $$ BEGIN
 ALTER TABLE managed_postgres_cutover_credentials DROP CONSTRAINT IF EXISTS managed_postgres_cutover_credentials_check;
 ALTER TABLE managed_postgres_cutover_credentials ADD CONSTRAINT managed_postgres_cutover_credentials_check CHECK (
   (state = 'sealed' AND num_nonnulls(provider_identity_id, credential_ref, ciphertext, kid, value_hash) = 5
     AND length(provider_identity_id) > 0 AND length(credential_ref) > 0 AND length(ciphertext) > 0
     AND length(kid) > 0 AND length(value_hash) > 0)
   OR (state IN ('pending','revoked') AND provider_identity_id IS NULL AND credential_ref IS NULL
     AND ciphertext IS NULL AND kid IS NULL AND value_hash IS NULL));
 ALTER TABLE managed_postgres_cutover_credentials DROP CONSTRAINT IF EXISTS managed_postgres_cutover_credentials_state_check;
 ALTER TABLE managed_postgres_cutover_credentials ADD CONSTRAINT managed_postgres_cutover_credentials_state_check
   CHECK (state IN ('pending','sealed','revoked'));
 ALTER TABLE managed_postgres_cutovers DROP CONSTRAINT IF EXISTS managed_postgres_cutovers_state_check;
 ALTER TABLE managed_postgres_cutovers ADD CONSTRAINT managed_postgres_cutovers_state_check
   CHECK (state IN ('preparing','prepared','verifying','verified','cancelling','cancelled'));
END $$;
-- +goose StatementEnd
RESET lock_timeout;
