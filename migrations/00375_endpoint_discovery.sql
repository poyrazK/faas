-- filename: 00375_endpoint_discovery.sql
-- +goose Up
-- +goose StatementBegin

-- Endpoint discovery (issue #975 item #1 / ADR-122). Today the
-- cold-boot characterization probe at guest/init/characterize_linux.go
-- fetches /openapi.json from the customer's app but DISCARDS the body
-- (only the status line is inspected). This migration stands up the
-- per-deployment jsonb blob that holds the captured OpenAPI document.
--
-- The capture path is in guest-init (PR #1-D): probeHTTP reads the body
-- up to VsockCharacterizationMaxBody (128 KiB after the
-- 32 KiB→128 KiB wire-format bump in PR #1-C), validates the shape
-- sniff (top-level `openapi` or `swagger` key), and ships OpenAPIDoc
-- + OpenAPIDocTruncated fields on the CharacterizationReport wire.
--
-- The store path is in pkg/state (PR #1-E): UpsertDeploymentOpenAPIDoc
-- is idempotent overwrite per deployment_id. The (account_id, app_id)
-- denormalised columns drive the per-account quota gate
-- (OpenAPIDocsPerAccount in pkg/api/limits.go).
--
-- Source enum IN ('cold_boot', 'manual_upload'):
--   cold_boot       — guest-init captured it during the first cold boot
--                     of the deployment (the load-bearing default)
--   manual_upload   — operator PATCHed it via
--                     /v1/apps/{slug}/deployments/{deployment}/openapi
--                     (retry, GraphQL-style app, or override)
-- Last-write-wins on (deployment_id); the audit log emits
-- app.openapi_doc.{captured,updated,deleted} so the lineage is
-- visible to the customer-facing audit reader.
--
-- byte_size is hard-bounded 1..131072 (= 128 KiB) at the SQL CHECK
-- layer. Per-plan upper bounds at the apid layer
-- (OpenAPIDocMaxBytes: 0 for Free, 131072 for paid plans).
-- The cap accommodates complex apps (Stripe-scale: ~700 operations,
-- deep components.schemas) that exceed the previous 32 KiB wire cap.
--
-- doc_sha256 is the SHA-256 of the raw JSON bytes (not the
-- json.Marshal of the JSONB re-serialisation), computed in-store
-- via crypto/sha256. The audit reader surfaces the hash so a
-- customer can verify its captured doc was the one they expect.
--
-- jsonb_typeof(doc) = 'object' is enforced at the apid write
-- boundary (Draft 2020-12 compilation); the SQL CHECK can't enforce
-- it natively. The cold-boot path does the cheap shape sniff
-- (openapi/swagger key) at the guest and skips doc-level validation
-- — a malformed cold-boot doc is just a "no doc captured" outcome.

CREATE TABLE IF NOT EXISTS deployment_openapi_docs (
  deployment_id uuid PRIMARY KEY REFERENCES deployments(id) ON DELETE CASCADE,
  account_id    uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  app_id        uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
  doc           jsonb NOT NULL,
  doc_sha256    bytea NOT NULL,
  byte_size     integer NOT NULL,
  source        text NOT NULL,
  truncated     boolean NOT NULL DEFAULT false,
  captured_at   timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT deployment_openapi_docs_byte_size_chk CHECK (
    byte_size > 0 AND byte_size <= 131072
  ),
  CONSTRAINT deployment_openapi_docs_source_vocab_chk CHECK (
    source IN ('cold_boot', 'manual_upload')
  ),
  CONSTRAINT deployment_openapi_docs_sha256_len_chk CHECK (
    octet_length(doc_sha256) = 32
  ),
  CONSTRAINT deployment_openapi_docs_captured_before_updated_chk CHECK (
    updated_at >= captured_at
  )
);

-- Per-account quota lookup. The OpenAPIDocsPerAccount limit
-- (pkg/api/limits.go) is enforced both at the apid write path
-- (CountOpenAPIDocsByAccount) and at the store layer (atomic
-- check inside the Upsert transaction). Indexed for the
-- per-account cardinality read.
CREATE INDEX IF NOT EXISTS deployment_openapi_docs_account_id_idx
  ON deployment_openapi_docs (account_id);

-- Per-app doc listing. The PATCH endpoint can update by
-- (app_id, deployment_id); the list endpoint
-- GET /v1/apps/{slug}/deployments/{deployment}/openapi
-- narrows by deployment_id (PK), so this index covers the
-- per-app "show all docs" path if the customer ever wants it.
CREATE INDEX IF NOT EXISTS deployment_openapi_docs_app_id_idx
  ON deployment_openapi_docs (app_id);

-- updated_at trigger. Same shape as 00329_consumer_keys.sql:
-- table-scoped function (no forward dependency on a future
-- shared helper), DROP TRIGGER IF EXISTS makes CREATE TRIGGER
-- replay-safe (a second MigrateUp finds the trigger, drops it,
-- recreates).
CREATE OR REPLACE FUNCTION deployment_openapi_docs_set_updated_at()
  RETURNS trigger
  LANGUAGE plpgsql
AS $$
BEGIN
  NEW.updated_at = now();
  RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS deployment_openapi_docs_set_updated_at_trg
  ON deployment_openapi_docs;
CREATE TRIGGER deployment_openapi_docs_set_updated_at_trg
  BEFORE UPDATE ON deployment_openapi_docs
  FOR EACH ROW
  EXECUTE FUNCTION deployment_openapi_docs_set_updated_at();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Forward-only migration. Drop order: trigger first, then the
-- function, then the table. The secondary indexes are dropped
-- with the table.

DROP TRIGGER IF EXISTS deployment_openapi_docs_set_updated_at_trg
  ON deployment_openapi_docs;
DROP FUNCTION IF EXISTS deployment_openapi_docs_set_updated_at();
DROP TABLE IF EXISTS deployment_openapi_docs;

-- +goose StatementEnd
