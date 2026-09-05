-- +goose Up
-- +goose StatementBegin
-- Durable, coalescing outbox for GitHub Check Run projections. A single row
-- tracks the latest deployment state; generation prevents a worker completing
-- an older write from erasing a newer transition that arrived concurrently.
CREATE TABLE IF NOT EXISTS github_check_updates (
  deployment_id uuid PRIMARY KEY REFERENCES deployments(id) ON DELETE CASCADE,
  generation bigint NOT NULL DEFAULT 1 CHECK (generation > 0),
  status text NOT NULL DEFAULT 'pending'
    CHECK (status IN ('pending', 'processing', 'succeeded', 'dead')),
  attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  next_attempt_at timestamptz NOT NULL DEFAULT now(),
  last_error text NOT NULL DEFAULT '',
  processed_at timestamptz,
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS github_check_updates_due_idx
  ON github_check_updates (next_attempt_at, updated_at)
  WHERE status IN ('pending', 'processing');
CREATE INDEX IF NOT EXISTS github_check_updates_dead_idx
  ON github_check_updates (updated_at DESC) WHERE status = 'dead';
CREATE INDEX IF NOT EXISTS github_webhook_deliveries_dead_idx
  ON github_webhook_deliveries (updated_at DESC) WHERE status = 'dead';

INSERT INTO github_check_updates (deployment_id)
SELECT id FROM deployments
WHERE kind IN ('github', 'preview')
  AND status IN ('pending', 'building', 'imaging', 'snapshotting')
ON CONFLICT (deployment_id) DO NOTHING;

CREATE OR REPLACE FUNCTION notify_github_deployment_status_changed()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF (TG_OP = 'INSERT' OR NEW.status IS DISTINCT FROM OLD.status)
     AND NEW.kind IN ('github', 'preview') THEN
    INSERT INTO github_check_updates (deployment_id)
    VALUES (NEW.id)
    ON CONFLICT (deployment_id) DO UPDATE SET
      generation = github_check_updates.generation + 1,
      status = 'pending',
      attempts = 0,
      next_attempt_at = now(),
      last_error = '',
      processed_at = NULL,
      updated_at = now();

    -- LISTEN is only a low-latency hint. The durable worker polls the table and
    -- therefore recovers updates written while githubd is stopped.
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
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS github_check_updates;
DROP INDEX IF EXISTS github_webhook_deliveries_dead_idx;

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
-- +goose StatementEnd
