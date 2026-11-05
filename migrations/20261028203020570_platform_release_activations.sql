-- filename: 20261028203020570_platform_release_activations.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- Issue #574: one row per release activation on a control-plane host, so
-- "what was running at 14:32?" has an answer after the shell history is gone.
-- deployctl writes it as the schema owner, best effort, under the host's
-- deploy lock: a deploy or rollback inserts in_progress and finishes it as
-- succeeded, failed or rolled_back (a deploy that unwound to the previous
-- release). A deployctl that died mid-activation leaves its row in_progress
-- until the host's next activation marks it interrupted. services is the
-- per-unit --only list; NULL means every service.
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS platform_release_activations (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    host                text NOT NULL CHECK (octet_length(host) BETWEEN 1 AND 253),
    kind                text NOT NULL CHECK (kind IN ('deploy', 'rollback')),
    release_id          text NOT NULL CHECK (octet_length(release_id) BETWEEN 1 AND 128),
    commit_sha          text CHECK (octet_length(commit_sha) BETWEEN 1 AND 64),
    previous_release_id text CHECK (octet_length(previous_release_id) BETWEEN 1 AND 128),
    services            text[] CHECK (cardinality(services) <= 64),
    actor               text NOT NULL CHECK (octet_length(actor) BETWEEN 1 AND 200),
    status              text NOT NULL CHECK (status IN ('in_progress', 'succeeded', 'failed', 'rolled_back', 'interrupted')),
    detail              text NOT NULL DEFAULT '' CHECK (octet_length(detail) <= 2000),
    started_at          timestamptz NOT NULL CHECK (isfinite(started_at)),
    finished_at         timestamptz CHECK (isfinite(finished_at)),
    CHECK ((status = 'in_progress') = (finished_at IS NULL)),
    CHECK (finished_at IS NULL OR finished_at >= started_at)
);
-- +goose StatementEnd
CREATE INDEX IF NOT EXISTS platform_release_activations_started_idx
    ON platform_release_activations (started_at DESC, id);
CREATE INDEX IF NOT EXISTS platform_release_activations_host_started_idx
    ON platform_release_activations (host, started_at DESC, id);
-- The deploy lock allows one activation per host at a time.
CREATE UNIQUE INDEX IF NOT EXISTS platform_release_activations_one_in_progress_idx
    ON platform_release_activations (host) WHERE status = 'in_progress';

COMMENT ON TABLE platform_release_activations IS
    'Release activations (deploy, rollback) per control-plane host, written by deployctl as the schema owner. Read with gregalectl release history (issue #574).';

-- Like the audit log, the history is not the daemons'. Default privileges
-- grant faas_runtime DML on every new table; take the writes back so a
-- compromised daemon cannot rewrite what was deployed. The postgres role's
-- runtime-roles.sql repeats this on every converge.
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'faas_runtime') THEN
        REVOKE INSERT, UPDATE, DELETE ON platform_release_activations FROM faas_runtime;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS platform_release_activations;
-- +goose StatementEnd
