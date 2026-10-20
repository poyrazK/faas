-- migration-compat: expand
-- Applied online (ADR-202610171900): every goose statement is its own short
-- transaction locking at most three existing tables. Every step is idempotent.
-- +goose NO TRANSACTION
-- +goose Up
SET lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS project_worker_job_evidence (
 invocation_id uuid NOT NULL REFERENCES invocations(id) ON DELETE CASCADE,
 replay_generation bigint NOT NULL CHECK(replay_generation>=0),
 attempt integer NOT NULL CHECK(attempt>0),
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 binding_id uuid NOT NULL REFERENCES queue_bindings(id) ON DELETE CASCADE,
 deployment_id uuid NOT NULL,
 started_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(started_at)),
 finished_at timestamptz CHECK(isfinite(finished_at) AND finished_at>=started_at),
 outcome text NOT NULL DEFAULT 'running' CHECK(outcome IN ('running','succeeded','retry','dead_letter')),
 queue_delay_ms bigint NOT NULL CHECK(queue_delay_ms>=0),
 PRIMARY KEY(invocation_id,replay_generation,attempt),
 CHECK((outcome='running')=(finished_at IS NULL))
);
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='project_worker_job_evidence'::regclass AND conname='project_worker_job_evidence_deployment_id_fkey') THEN
  ALTER TABLE project_worker_job_evidence ADD CONSTRAINT project_worker_job_evidence_deployment_id_fkey FOREIGN KEY (deployment_id) REFERENCES deployments(id) ON DELETE CASCADE;
 END IF;
END $$;
-- +goose StatementEnd
CREATE INDEX IF NOT EXISTS project_worker_job_evidence_window ON project_worker_job_evidence(app_id,binding_id,deployment_id,started_at);
-- +goose Down
DROP TABLE project_worker_job_evidence;
