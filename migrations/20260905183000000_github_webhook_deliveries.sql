-- +goose Up
-- +goose StatementBegin
-- Durable GitHub delivery inbox. GitHub retries deliveries and expects a fast
-- acknowledgement; keeping the payload in Postgres lets githubd acknowledge
-- after the durable write and do fetch/scan/build work asynchronously.
CREATE TABLE IF NOT EXISTS github_webhook_deliveries (
  delivery_id text PRIMARY KEY,
  event_type text NOT NULL CHECK (event_type IN ('push', 'pull_request')),
  payload bytea NOT NULL,
  status text NOT NULL DEFAULT 'pending'
    CHECK (status IN ('pending', 'processing', 'succeeded', 'dead')),
  attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  next_attempt_at timestamptz NOT NULL DEFAULT now(),
  last_error text NOT NULL DEFAULT '',
  received_at timestamptz NOT NULL DEFAULT now(),
  processed_at timestamptz,
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS github_webhook_deliveries_due_idx
  ON github_webhook_deliveries (next_attempt_at, received_at)
  WHERE status IN ('pending', 'processing');

-- A Gregale account can install the App on a personal account and multiple
-- organizations. Keep every installation instead of replacing the prior row;
-- installation_id stays globally exclusive so one GitHub installation cannot
-- be claimed by two Gregale accounts.
ALTER TABLE github_installations
  DROP CONSTRAINT IF EXISTS github_installations_pkey;
ALTER TABLE github_installations
  ADD CONSTRAINT github_installations_pkey PRIMARY KEY (account_id, installation_id);
-- Older code could associate a guessed installation ID with more than one
-- account. Keep the most recently verified owner before enforcing global
-- exclusivity; stale app bindings then fail the exact account/install lookup.
DELETE FROM github_installations older
USING github_installations newer
WHERE older.installation_id = newer.installation_id
  AND (older.sealed_at, older.account_id::text) < (newer.sealed_at, newer.account_id::text);
CREATE UNIQUE INDEX IF NOT EXISTS github_installations_installation_id_uidx
  ON github_installations (installation_id);
CREATE INDEX IF NOT EXISTS github_installations_account_recent_idx
  ON github_installations (account_id, sealed_at DESC);

-- Stable Check Run identity. Later lifecycle transitions PATCH the original
-- row instead of creating one GitHub Check Run per phase.
CREATE TABLE IF NOT EXISTS github_check_runs (
  repo_full_name text NOT NULL,
  commit_sha text NOT NULL,
  check_name text NOT NULL,
  check_run_id bigint NOT NULL,
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (repo_full_name, commit_sha, check_name)
);

-- Make every deployment state change observable. Several workers update the
-- deployment row directly; a database trigger is the one place that covers
-- pending/building/imaging/snapshotting/live/failed/cancelled consistently.
CREATE OR REPLACE FUNCTION notify_github_deployment_status_changed()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'INSERT' OR NEW.status IS DISTINCT FROM OLD.status THEN
    PERFORM pg_notify('github_deployment_changed', json_build_object(
      'kind', NEW.kind,
      'app_id', NEW.app_id,
      'deployment_id', NEW.id,
      'status', NEW.status
    )::text);
  END IF;
  RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS github_deployment_status_changed_trg ON deployments;
CREATE TRIGGER github_deployment_status_changed_trg
AFTER INSERT OR UPDATE OF status ON deployments
FOR EACH ROW EXECUTE FUNCTION notify_github_deployment_status_changed();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS github_deployment_status_changed_trg ON deployments;
DROP FUNCTION IF EXISTS notify_github_deployment_status_changed();
DROP TABLE IF EXISTS github_check_runs;
DROP TABLE IF EXISTS github_webhook_deliveries;
DROP INDEX IF EXISTS github_installations_account_recent_idx;
DROP INDEX IF EXISTS github_installations_installation_id_uidx;
-- Rolling back to the legacy one-install schema necessarily keeps the most
-- recently authorized installation for accounts that added more than one.
DELETE FROM github_installations older
USING github_installations newer
WHERE older.account_id = newer.account_id
  AND (older.sealed_at, older.installation_id) < (newer.sealed_at, newer.installation_id);
ALTER TABLE github_installations
  DROP CONSTRAINT IF EXISTS github_installations_pkey;
ALTER TABLE github_installations
  ADD CONSTRAINT github_installations_pkey PRIMARY KEY (account_id);
-- +goose StatementEnd
