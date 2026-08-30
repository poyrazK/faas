-- filename: 00533_upload_sessions.sql
-- +goose Up
-- +goose StatementBegin

-- Issue #1182 §P1 packaging follow-up: resumable upload session
-- protocol for `gregale deploy --tarball` and the zero-config cwd-
-- auto-pack path. PR-1 of 3 (server-only foundation).
--
-- Why resumable: the current single-shot `POST /v1/apps/{slug}/
-- deployments/source-tarball` endpoint at
-- cmd/apid/handlers_source_tarball.go:58 has no client retry, no
-- progress, and a 60-second ReadTimeout on the customer listener
-- (cmd/apid/main.go:398-411). A 250 MB upload over a flaky link
-- currently fails on the first byte drop; PR-2 will wire the CLI to
-- use this surface.
--
-- Wire shape (PR-2 client side):
--   POST   /v1/uploads                   → 201 {upload_id, chunk_size, total_size, expires_at}
--   PATCH  /v1/uploads/{id}              → 200, Upload-Offset: N header, body = raw bytes
--   POST   /v1/uploads/{id}/commit       → 201 {DeploymentResponse}
--   DELETE /v1/uploads/{id}              → 204
--
-- The atomic CAS in AppendUploadBytes (UPDATE ... WHERE id=$1 AND
-- status='open' AND received_bytes=$2 RETURNING ...) is the load-
-- bearing safety: a slow client that resumes mid-flight corrupts the
-- spool file if the CAS is missing. See
-- docs/adr/NNN-resumable-upload-protocol.md (added in PR-3) for the
-- design rationale.
--
-- Plan-aware caps live in the handler, not in SQL — the per-plan
-- SourceTarballMaxMB table at pkg/api/limits.go:1355,1675,2020,2340
-- is the single source of truth and is read at POST /v1/uploads time
-- via api.MustLimitsFor(acct.Plan). The hard ceiling below (1 GiB)
-- is the worst-case spool size; per-plan caps in the handler keep
-- Free/Hobby at 100 MB and Pro/Scale at 250 MB.
--
-- ADR-115 trust boundary: the CLI is the trust root on the source-
-- tarball path (docs/adr/115-local-tarball-deploy-trust-root.md:9-17).
-- sha256_hex is recorded for the build_provenance audit row ONLY —
-- the server never re-verifies the digest at commit time.

CREATE TABLE upload_sessions (
    id              text PRIMARY KEY,
    account_id      uuid NOT NULL,
    app_slug        text NOT NULL,
    total_size      bigint NOT NULL CHECK (total_size > 0 AND total_size <= 1073741824),
    received_bytes  bigint NOT NULL DEFAULT 0 CHECK (received_bytes >= 0 AND received_bytes <= total_size),
    chunk_size      integer NOT NULL DEFAULT 8388608 CHECK (chunk_size > 0 AND chunk_size <= 67108864),
    sha256_hex      text,
    part_path       text NOT NULL,
    status          text NOT NULL DEFAULT 'open' CHECK (status IN ('open','committed','cancelled','expired')),
    created_at      timestamptz NOT NULL DEFAULT now(),
    last_patched_at timestamptz NOT NULL DEFAULT now(),
    expires_at      timestamptz NOT NULL DEFAULT (now() + INTERVAL '24 hours'),
    deployment_id   text
);

-- Partial index for the per-(account, app) open-session cap query
-- (handler: "no more than 5 concurrent open sessions per account per app").
-- A partial index keeps the working set tight — closed/cancelled rows
-- are not scanned on every POST.
CREATE INDEX upload_sessions_account_open_idx
    ON upload_sessions(account_id, app_slug)
    WHERE status = 'open';

-- Partial index for the reaper (cmd/apid/upload_session_reaper.go):
-- "WHERE status='open' AND expires_at < now()".
-- The composite predicate is identical to the reaper's WHERE clause,
-- so the reaper hits only the index without touching the heap.
CREATE INDEX upload_sessions_expires_idx
    ON upload_sessions(expires_at)
    WHERE status = 'open';

-- Companion table for commit-dedupe. The POST /v1/uploads/{id}/commit
-- handler INSERTs ON CONFLICT DO NOTHING here after a successful
-- Enqueue; a retry of COMMIT (network blip after server wrote the
-- build row but before the 201 reached the client) hits the conflict
-- path and returns the stored outcome. CASCADE on delete so a reaped
-- session cleans up its dedupe row.
CREATE TABLE upload_commit_outcomes (
    upload_id       text PRIMARY KEY REFERENCES upload_sessions(id) ON DELETE CASCADE,
    deployment_id   text NOT NULL,
    build_id        text NOT NULL,
    finalized_at    timestamptz NOT NULL DEFAULT now()
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS upload_commit_outcomes;
DROP TABLE IF EXISTS upload_sessions;

-- +goose StatementEnd