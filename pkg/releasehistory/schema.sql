-- Projection of migration 20261028203020570.
CREATE TABLE platform_release_activations (
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
CREATE INDEX platform_release_activations_started_idx
    ON platform_release_activations (started_at DESC, id);
CREATE INDEX platform_release_activations_host_started_idx
    ON platform_release_activations (host, started_at DESC, id);
CREATE UNIQUE INDEX platform_release_activations_one_in_progress_idx
    ON platform_release_activations (host) WHERE status = 'in_progress';
