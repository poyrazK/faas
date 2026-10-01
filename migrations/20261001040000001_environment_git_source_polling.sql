-- +goose Up
-- +goose StatementBegin
ALTER TABLE environment_git_sources
    ADD COLUMN IF NOT EXISTS source_commit_sha text NOT NULL DEFAULT '' CHECK (source_commit_sha = '' OR source_commit_sha ~ '^([a-f0-9]{40}|[a-f0-9]{64})$'),
    ADD COLUMN IF NOT EXISTS source_definition_digest text NOT NULL DEFAULT '' CHECK (source_definition_digest = '' OR source_definition_digest ~ '^[a-f0-9]{64}$'),
    ADD COLUMN IF NOT EXISTS source_verified_at timestamptz;
DO $replay$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='environment_git_sources'::regclass AND conname='environment_git_source_candidate_complete') THEN
    ALTER TABLE environment_git_sources ADD CONSTRAINT environment_git_source_candidate_complete CHECK (
      (source_commit_sha = '' AND source_definition_digest = '' AND source_verified_at IS NULL) OR
      (source_commit_sha <> '' AND source_definition_digest <> '' AND source_verified_at IS NOT NULL));
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='environment_git_sources'::regclass AND conname='environment_git_source_poll_error_code') THEN
    ALTER TABLE environment_git_sources ADD CONSTRAINT environment_git_source_poll_error_code CHECK (source_error_code IN (
      '', 'environment_git_source_unavailable', 'environment_git_definition_invalid',
      'environment_git_scope_mismatch', 'environment_git_repository_unavailable'));
  END IF;
END $replay$;

CREATE TABLE IF NOT EXISTS environment_git_source_polls (
    source_id uuid PRIMARY KEY REFERENCES environment_git_sources(id) ON DELETE CASCADE,
    next_poll_at timestamptz NOT NULL DEFAULT now(),
    lease_token uuid,
    lease_until timestamptz,
    CHECK ((lease_token IS NULL) = (lease_until IS NULL))
);
INSERT INTO environment_git_source_polls(source_id) SELECT id FROM environment_git_sources ON CONFLICT (source_id) DO NOTHING;

DO $replay$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
    WHERE p.proname='enqueue_environment_git_source_poll' AND n.nspname=current_schema()) THEN
    EXECUTE $function$CREATE FUNCTION enqueue_environment_git_source_poll() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO environment_git_source_polls(source_id) VALUES (NEW.id);
    RETURN NEW;
END;
$$;$function$;
  END IF;
END $replay$;
DROP TRIGGER IF EXISTS environment_git_source_poll_created ON environment_git_sources;
CREATE TRIGGER environment_git_source_poll_created AFTER INSERT ON environment_git_sources
FOR EACH ROW EXECUTE FUNCTION enqueue_environment_git_source_poll();
CREATE INDEX IF NOT EXISTS environment_git_source_polls_due_idx ON environment_git_source_polls(next_poll_at, source_id);
-- +goose StatementEnd

-- +goose Down
-- Preserve durable ownership, accepted work and runtime evidence on rollback.
SELECT 1;
