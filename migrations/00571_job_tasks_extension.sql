-- +goose Up
-- +goose StatementBegin
-- ADR-099 (proposed) + Mega-1 supplement: extend job_tasks with the
-- fields the dispatch + retry + cancel paths need that the 00255
-- schema left out.
--
-- - exit_code          : captured from the guest's job_exit vsock DGRAM
--                        (port 1026 msg_type 4) so retries can carry
--                        the prior exit forward and dead_letter
--                        classification works.
-- - next_attempt_at    : backoff-delayed retry scheduling.
--                        JobBackoffBaseSeconds * 2^(attempt-1) capped
--                        at JobBackoffMaxSeconds.
--
-- Plus two CHECK constraint relaxations:
--
-- - job_tasks_instance_pair_chk: free `cancelled` to have instance_id
--   IS NULL. A task cancelled before dispatch has no instance.
--   The pair check now reads:
--     queued      → instance_id IS NULL
--     claimed     → instance_id IS NOT NULL
--     terminal    → either (instance_id NULL if cancelled-from-queued)
--
-- - job_tasks_error_class_check: widen vocabulary to include
--   'success', 'cancelled', 'job_paused', 'oom_or_killed' so the
--   engine + reaper + job_exit handler can use the same field.

-- Production repair for the ADR-099 jobs migration cluster. The original
-- jobs DDL was renumbered from 00245 to 00255 after a reservation fence had
-- already been applied at 00255 on the live database. Goose therefore sees
-- 00255-00257 as complete even though the three jobs tables are absent.
--
-- This migration is the first jobs-cluster migration that production has not
-- applied, so restore the base schema before applying its extensions. Every
-- object is creation-safe for fresh databases and for databases where the
-- original 00255 DDL landed normally.

CREATE TABLE IF NOT EXISTS jobs (
    id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id      uuid        NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    kind            text        NOT NULL,
    name            text        NOT NULL,
    image_ref       text        NOT NULL,
    ram_mb          int         NOT NULL,
    task_timeout_s  int         NOT NULL,
    max_parallelism int         NOT NULL,
    retry_max       int         NOT NULL,
    env_overrides   jsonb       NOT NULL DEFAULT '{}'::jsonb,
    status          text        NOT NULL DEFAULT 'active',
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT jobs_kind_check
        CHECK (kind IN ('app','function')),
    CONSTRAINT jobs_name_check
        CHECK (name ~ '^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$'),
    CONSTRAINT jobs_ram_mb_check
        CHECK (ram_mb > 0),
    CONSTRAINT jobs_task_timeout_s_check
        CHECK (task_timeout_s BETWEEN 1 AND 86400),
    CONSTRAINT jobs_max_parallelism_check
        CHECK (max_parallelism BETWEEN 1 AND 1000),
    CONSTRAINT jobs_retry_max_check
        CHECK (retry_max BETWEEN 0 AND 10),
    CONSTRAINT jobs_status_check
        CHECK (status IN ('active','paused','deleted'))
);

CREATE INDEX IF NOT EXISTS jobs_account_idx
    ON jobs (account_id, created_at DESC);

CREATE UNIQUE INDEX IF NOT EXISTS jobs_account_name_uniq
    ON jobs (account_id, name) WHERE status <> 'deleted';

CREATE TABLE IF NOT EXISTS job_runs (
    id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    job_id          uuid        NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    account_id      uuid        NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    trigger_kind    text        NOT NULL,
    env_overrides   jsonb       NOT NULL DEFAULT '{}'::jsonb,
    tasks           int         NOT NULL,
    parallelism     int         NOT NULL,
    retry_max       int         NULL,
    task_timeout_s  int         NULL,
    aggregate_status text        NOT NULL DEFAULT 'queued',
    tasks_succeeded int         NOT NULL DEFAULT 0,
    tasks_failed    int         NOT NULL DEFAULT 0,
    tasks_cancelled int         NOT NULL DEFAULT 0,
    tasks_running   int         NOT NULL DEFAULT 0,
    started_at      timestamptz NULL,
    finished_at     timestamptz NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT job_runs_trigger_kind_check
        CHECK (trigger_kind IN ('manual','scheduled','triggered')),
    CONSTRAINT job_runs_tasks_check
        CHECK (tasks BETWEEN 1 AND 100000),
    CONSTRAINT job_runs_parallelism_check
        CHECK (parallelism BETWEEN 1 AND 1000),
    CONSTRAINT job_runs_retry_max_check
        CHECK (retry_max IS NULL OR retry_max BETWEEN 0 AND 10),
    CONSTRAINT job_runs_task_timeout_s_check
        CHECK (task_timeout_s IS NULL OR task_timeout_s BETWEEN 1 AND 86400),
    CONSTRAINT job_runs_aggregate_status_check
        CHECK (aggregate_status IN (
            'queued','running','succeeded','failed','cancelled','dead_letter'
        )),
    CONSTRAINT job_runs_counters_check
        CHECK (
            tasks_succeeded >= 0 AND tasks_failed >= 0
            AND tasks_cancelled >= 0 AND tasks_running >= 0
            AND tasks_succeeded + tasks_failed + tasks_cancelled
                + tasks_running <= tasks
        ),
    CONSTRAINT job_runs_terminal_pair_chk
        CHECK (
            (finished_at IS NULL AND aggregate_status IN ('queued','running'))
            OR (finished_at IS NOT NULL AND aggregate_status IN
                ('succeeded','failed','cancelled','dead_letter'))
        )
);

CREATE INDEX IF NOT EXISTS job_runs_account_idx
    ON job_runs (account_id, created_at DESC);

CREATE INDEX IF NOT EXISTS job_runs_job_idx
    ON job_runs (job_id, created_at DESC);

CREATE INDEX IF NOT EXISTS job_runs_active_idx
    ON job_runs (account_id, id)
    WHERE aggregate_status IN ('queued','running');

CREATE TABLE IF NOT EXISTS job_tasks (
    run_id          uuid        NOT NULL REFERENCES job_runs(id) ON DELETE CASCADE,
    task_index      int         NOT NULL,
    status          text        NOT NULL DEFAULT 'queued',
    attempt         int         NOT NULL DEFAULT 1,
    instance_id     uuid        NULL REFERENCES instances(id),
    error_class     text        NULL,
    error_message   text        NULL,
    started_at      timestamptz NULL,
    finished_at     timestamptz NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (run_id, task_index),
    CONSTRAINT job_tasks_task_index_check
        CHECK (task_index >= 0),
    CONSTRAINT job_tasks_status_check
        CHECK (status IN (
            'queued','claimed','succeeded','failed','timeout','cancelled','oom'
        )),
    CONSTRAINT job_tasks_attempt_check
        CHECK (attempt BETWEEN 1 AND 11),
    CONSTRAINT job_tasks_error_class_check
        CHECK (error_class IS NULL OR error_class IN (
            'timeout','refused','tls_handshake','dns','unreachable',
            'oom','user_error','infra'
        )),
    CONSTRAINT job_tasks_terminal_pair_chk
        CHECK (
            (finished_at IS NULL AND status IN ('queued','claimed'))
            OR (finished_at IS NOT NULL AND status IN
                ('succeeded','failed','timeout','cancelled','oom'))
        ),
    CONSTRAINT job_tasks_instance_pair_chk
        CHECK ((instance_id IS NULL AND status = 'queued') OR (instance_id IS NOT NULL))
);

CREATE INDEX IF NOT EXISTS job_tasks_ready_idx
    ON job_tasks (created_at ASC, run_id, task_index)
    WHERE status = 'queued';

CREATE INDEX IF NOT EXISTS job_tasks_run_idx
    ON job_tasks (run_id, task_index);

DROP TRIGGER IF EXISTS job_tasks_notify_trg ON job_tasks;

CREATE OR REPLACE FUNCTION job_tasks_notify() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF (TG_OP = 'INSERT' AND NEW.status = 'queued')
       OR (TG_OP = 'UPDATE' AND OLD.status = 'queued'
           AND NEW.status <> 'queued') THEN
        PERFORM pg_notify(
            'job_tasks_queued',
            format('%s|%s|%s', NEW.run_id, NEW.task_index, TG_OP)
        );
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER job_tasks_notify_trg
    AFTER INSERT OR UPDATE OR DELETE ON job_tasks
    FOR EACH ROW
    EXECUTE FUNCTION job_tasks_notify();

ALTER TABLE job_tasks
    ADD COLUMN IF NOT EXISTS exit_code int NULL;

ALTER TABLE job_tasks
    ADD COLUMN IF NOT EXISTS next_attempt_at timestamptz NULL;

CREATE INDEX IF NOT EXISTS job_tasks_next_attempt_idx
    ON job_tasks (next_attempt_at)
    WHERE next_attempt_at IS NOT NULL
      AND status IN ('queued', 'claimed');

ALTER TABLE job_tasks
    DROP CONSTRAINT IF EXISTS job_tasks_instance_pair_chk;

ALTER TABLE job_tasks
    ADD CONSTRAINT job_tasks_instance_pair_chk
    CHECK (
        (status = 'queued' AND instance_id IS NULL)
        OR (status = 'claimed' AND instance_id IS NOT NULL)
        OR (status IN ('succeeded', 'failed', 'timeout', 'cancelled', 'oom'))
    );

ALTER TABLE job_tasks
    DROP CONSTRAINT IF EXISTS job_tasks_error_class_check;

ALTER TABLE job_tasks
    ADD CONSTRAINT job_tasks_error_class_check
    CHECK (
        error_class IS NULL
        OR error_class IN (
            'timeout',
            'refused',
            'tls_handshake',
            'dns',
            'unreachable',
            'oom',
            'user_error',
            'infra',
            'success',
            'cancelled',
            'job_paused',
            'oom_or_killed'
        )
    );
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Forward-only: do not drop columns on rollback (memory:
-- migration-gates-collision-and-replay — replay safety).
SELECT 1;
-- +goose StatementEnd
