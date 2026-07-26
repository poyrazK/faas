-- +goose Up
-- +goose StatementBegin
--
-- 00048_build_provenance.sql — Tier 3 / issue #197 B3.1 + B3.10-read half.
--
-- Adds the `build_provenance` table: one row per `builds.id` that
-- records what actually ran and where. The populator lands in this
-- PR (pkg/builderd/builderd.go::recordProvenance) and is called from
-- the two existing markSucceeded sites (cache-hit + fresh-build).
-- The reader is GET /v1/builds/{id}/provenance (apid) + `faas build
-- provenance <id>` (CLI).
--
-- Columns:
--
--   id — PK. UUID v4 default so the populator doesn't have to mint
--        one. Indexes reference it directly.
--
--   build_id — the FK to builds(id). UNIQUE so each build row has
--        at most one provenance row. The populator uses
--        `ON CONFLICT (build_id) DO UPDATE` to make redelivery
--        idempotent (LISTEN race between the apid write path and
--        the imaged reaper, PR-A).
--
--   buildkit_version / railpack_version — semver strings inside
--        the builder microVM. Empty on cache hit (buildkit didn't
--        run). No length cap needed (these are short constants).
--
--   base_digest — sha256 of the base ext4 attached to the builder
--        VM (from FAAS_BUILDER_BASE_REF). Empty on cache hit.
--
--   source_sha256 — sha256 of the customer's source tarball
--        (cached lookup key). Always populated on success.
--
--   source_url / commit_sha — copied from deployments (Phase 1
--        columns added in 00047). Empty for image: deploys with
--        no upstream URL. commit_sha is hex-anchored by the
--        deployments_commit_sha_shape_chk CHECK at the upstream
--        column; we don't re-validate here.
--
--   plan — free / hobby / pro / scale. NOT validated by FK
--        against accounts (the value is copied at claim time and
--        may diverge from the account's current plan during a
--        downgrade window). Use api.LimitsFor at the read site.
--
--   runner_digest — sha256 of the function runner shim injected
--        at build time (spec §4.9). Empty for non-function deploys.
--
--   builder_node_id — the builder microVM's compute_node name
--        (default "default-local" on the one-box).
--
--   started_at / finished_at — copied from builds.started_at /
--        builds.finished_at. NOT NULL because the populator runs
--        only on success (markSucceeded path).
--
--   sbom_storage_key — populated by Phase 3's syft path
--        (pkg/builderd/sbom.go). Empty string in this PR; the
--        column exists so Phase 3 is a zero-cost schema change.
--
-- Indexes:
--
--   build_provenance_build_id_idx — UNIQUE btree (build_id). The
--        single read path is by build_id; the FK lookup at INSERT
--        time hits this index too. UNIQUE instead of a separate
--        UNIQUE constraint for compactness.
--
-- Down drops the table. No data loss in steady state because the
-- rows are recomputable from builds + deployments + the builder
-- config (the populator is idempotent and rebuilds them on the
-- next successful build).

CREATE TABLE build_provenance (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    build_id        uuid NOT NULL UNIQUE REFERENCES builds(id),
    buildkit_version text,
    railpack_version text,
    base_digest     text,
    source_sha256   text NOT NULL,
    source_url      text,
    commit_sha      text,
    plan            text,
    runner_digest   text,
    builder_node_id text,
    started_at      timestamptz NOT NULL,
    finished_at     timestamptz NOT NULL,
    sbom_storage_key text
);

CREATE INDEX build_provenance_build_id_idx
    ON build_provenance (build_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS build_provenance_build_id_idx;
DROP TABLE IF EXISTS build_provenance;
-- +goose StatementEnd