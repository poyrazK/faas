# Sub-issue #02 — OpenAPI import + auto-generation

Parent: [README.md](README.md)

## Problem

Sub-issue #01 persists the document the probe captures. This sub-issue
does two things with it:

1. **Import** — let a customer upload an OpenAPI 3.x document *before*
   deploying, so Gregale can pre-create edge rule drafts and pre-generate
   SDKs.
2. **Generate** — derive an OpenAPI 3.1 spec from a deployed app using
   (a) the captured document from #01 if available, falling back to
   (b) the observed routes from sub-issue #07, falling back to
   (c) the customer's hand-declared `match_path` rules.

Gregale already exports *its own* OpenAPI document
(`pkg/apid/openapi_handler.go:22-34` + `:69-93`, registered at
`cmd/apid/server.go:1737-1741`). That is the third leg of the feature and
is already shipped — this sub-issue only covers customer-side import +
app-side generation.

## Proposal

### Import

New endpoints on `apid`:

- `POST /v1/apps/{slug}/openapi` — multipart upload of an OpenAPI 3.0 or
  3.1 document. Validated against a JSON Schema (vendored under
  `pkg/api/openapi_import_schema.json`); on success, written to
  `app_openapi_docs` from #01 with `source='manual_import'`,
  `captured_by='customer_import'`.
- `POST /v1/apps/{slug}/openapi/dry-run` — validates only, doesn't persist.
  Returns per-endpoint suggestions:
  - which path needs an `edge_rule` to be declared,
  - which path needs a JWT rule based on the security schemes,
  - which path has a body schema and would benefit from a `validate` rule
    (plugs into #03 observe mode).

### Generation

New endpoint:

- `GET /v1/apps/{slug}/openapi?source=auto` — generates the spec server-side
  by combining:
  - `app_openapi_docs` document (if present),
  - observed `route` labels from ADR-093 with their status/latency summary,
  - declared `edge_rules.match_path` strings,
  - hostnames from `tenant_hostnames`.

  Output is OpenAPI 3.1. Cache key = `(app_id, sha256(doc), sha256(routes
  snapshot), sha256(rules snapshot))`. Cache TTL = 5 min; invalidated on
  rule changes via the existing `pg_notify` channel
  (`edge_rules_changed` — already in the table family from ADR-091).

### SDK generation

This sub-issue does NOT add a code generator. It produces a 3.1 doc that
existing tooling (openapi-generator, Speakeasy, Stainless) can consume. If
the dashboard wants a "download SDK" button, that is a follow-up and
lives in a separate issue.

## Limits (pkg/api/limits.go)

- `openapi_import_max_doc_bytes` = 256 KB (same cap as #01, reused).
- `openapi_import_max_endpoints` = 50 (mirrors ADR-093 route cap; reject
  with 422 + RFC 7807 `code=openapi_too_many_endpoints`).

## Acceptance

1. Customer can upload an OpenAPI 3.1 doc for a Free-plan app and see it
   persisted at `GET /v1/apps/{slug}/openapi`.
2. `POST .../dry-run` returns non-empty suggestions for a 3-route app
   without declared edge rules.
3. `GET .../openapi?source=auto` returns a 3.1 doc that round-trips
   through `swagger-cli validate` (or equivalent) for any e2e app.
4. Per-endpoint suggestion is rate-limited at the apid layer (no DDoS via
   repeated dry-runs).

## Dependencies

- Reads `app_openapi_docs` from #01.
- Reads route metrics from #07.
- Feeds the rule-suggestion UX in the dashboard (out of scope here).

## Audit provenance

- `pkg/apid/openapi_handler.go:22-34` — own-spec export (already ships).
- `guest/init/characterize_linux.go:487-505` — probe exists but document not retained.
- `pkg/api/characterization.go:24-57` — no doc field.
