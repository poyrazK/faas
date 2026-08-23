-- filename: 00383_openapi_import.sql
-- +goose Up
-- +goose StatementBegin

-- OpenAPI import + auto-generation (issue #975 item #2 / ADR-126).
-- ADR-122 (item #1) stands up the per-deployment jsonb blob that
-- holds the captured OpenAPI document during cold boot. This
-- migration stands up the per-app jsonb blob that holds the
-- customer-imported OpenAPI document — the "declared surface"
-- side of the closed loop.
--
-- The two tables serve different read paths and do not share a
-- code path beyond the apid layer's load:
--
--   deployment_openapi_docs (item #1)
--     -> GET /v1/apps/{slug}/deployments/{deployment}/openapi
--        (audit / source-of-truth — what the customer actually
--         served on this build)
--   app_openapi_docs (this migration)
--     -> GET /v1/apps/{slug}/openapi
--        (dashboard surface — what the customer declares the app
--         exposes)
--
-- Per-app PK = one row per app. The Upsert path is idempotent
-- overwrite (INSERT ... ON CONFLICT (app_id) DO UPDATE) — last
-- write wins, with audit emit app.openapi_import.{created,
-- replaced,deleted} so the lineage is visible to the
-- customer-facing audit reader.
--
-- Source enum: IN ('manual_import'). Cold-boot captures go to
-- deployment_openapi_docs (item #1), not here — the app-level
-- invariant is what the customer declares, not what Gregale
-- observed.
--
-- openapi_version enum: pinned to the seven versions whose
-- meta-schemas we accept (3.0.0-3.0.4 and 3.1.0-3.1.1). The
-- validator at pkg/openapiimport/validator.go compiles the
-- imported doc against the OpenAPI 3.1 meta-schema
-- (santhosh-tekuri/jsonschema/v6 under Draft2020) regardless of
-- the declared version — 3.0.x docs that don't use 3.0-only
-- features (specifically nullable: true) pass; customers
-- needing strict 3.0 can ship 3.1.
--
-- byte_size is hard-bounded 1..262144 (= 256 KiB) at the SQL
-- CHECK layer. Per-plan upper bounds at the apid layer
-- (OpenAPIImportMaxDocBytes: 262144 across all plans; this is
-- the abuse-surface limit, not the plan-tier limit).
--
-- endpoint_count is hard-bounded 0..50. The number is the count
-- of HTTP operations in the imported doc's paths.* — a generous
-- ceiling for a single-app surface (a Stripe-scale 700-operation
-- doc would be split per-app, not per-spec).
--
-- doc_sha256 is the SHA-256 of the raw JSON bytes (not the
-- json.Marshal of the JSONB re-serialisation), computed in-store
-- via crypto/sha256. The cache key at
-- pkg/openapidiff/spec_cache.go includes this hash so an
-- unchanged import + a route-rule write produces a cache hit
-- for the doc half of the key.

CREATE TABLE IF NOT EXISTS app_openapi_docs (
  app_id          uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
  account_id      uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  doc             jsonb NOT NULL,
  doc_sha256      bytea NOT NULL,
  byte_size       integer NOT NULL,
  endpoint_count  integer NOT NULL,
  source          text NOT NULL,
  openapi_version text NOT NULL,
  captured_at     timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT app_openapi_docs_byte_size_chk CHECK (
    byte_size > 0 AND byte_size <= 262144
  ),
  CONSTRAINT app_openapi_docs_endpoint_count_chk CHECK (
    endpoint_count >= 0 AND endpoint_count <= 50
  ),
  CONSTRAINT app_openapi_docs_source_vocab_chk CHECK (
    source IN ('manual_import')
  ),
  CONSTRAINT app_openapi_docs_openapi_version_vocab_chk CHECK (
    openapi_version IN (
      '3.0.0','3.0.1','3.0.2','3.0.3','3.0.4','3.1.0','3.1.1'
    )
  ),
  CONSTRAINT app_openapi_docs_sha256_len_chk CHECK (
    octet_length(doc_sha256) = 32
  ),
  CONSTRAINT app_openapi_docs_captured_before_updated_chk CHECK (
    updated_at >= captured_at
  )
);

-- Per-account quota lookup. The OpenAPIImportsPerAccount limit
-- (pkg/api/limits.go) is enforced both at the apid write path
-- (CountOpenAPIImportsByAccount) and at the store layer
-- (atomic check inside the Upsert transaction). Indexed for
-- the per-account cardinality read.
CREATE INDEX IF NOT EXISTS app_openapi_docs_account_id_idx
  ON app_openapi_docs (account_id);

-- updated_at trigger. Same shape as 00329_consumer_keys.sql and
-- 00375_endpoint_discovery.sql: table-scoped function (no
-- forward dependency on a future shared helper), DROP TRIGGER
-- IF EXISTS makes CREATE TRIGGER replay-safe (a second
-- MigrateUp finds the trigger, drops it, recreates).
CREATE OR REPLACE FUNCTION app_openapi_docs_set_updated_at()
  RETURNS trigger
  LANGUAGE plpgsql
AS $$
BEGIN
  NEW.updated_at = now();
  RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS app_openapi_docs_set_updated_at_trg
  ON app_openapi_docs;
CREATE TRIGGER app_openapi_docs_set_updated_at_trg
  BEFORE UPDATE ON app_openapi_docs
  FOR EACH ROW
  EXECUTE FUNCTION app_openapi_docs_set_updated_at();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Forward-only migration. Drop order: trigger first, then the
-- function, then the table. The secondary index is dropped
-- with the table.

DROP TRIGGER IF EXISTS app_openapi_docs_set_updated_at_trg
  ON app_openapi_docs;
DROP FUNCTION IF EXISTS app_openapi_docs_set_updated_at();
DROP TABLE IF EXISTS app_openapi_docs;

-- +goose StatementEnd
