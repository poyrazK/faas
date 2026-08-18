# Sub-issue #01 — Automatic endpoint discovery

Parent: [README.md](README.md)

## Problem

ADR-051 (`docs/adr/051-characterization-boot-workload-classification.md:61-67`)
describes a characterization probe that hits `/openapi.json` and GraphQL
introspection. Today the probe runs but discards the captured document:

- `guest/init/characterize_linux.go:433-455` and `:487-505` issue the probes
  and only forward a workload class (`http` | `graphql` | `grpc`).
- The wire shape `pkg/api/characterization.go:24-57` has no field for the
  OpenAPI document, route list, or path patterns.
- Customer-visible edge rules require the operator to declare paths by hand
  (`schema.sql:1287-1301` — `match_path`, `match_methods` are opaque strings).

We need the captured document persisted and queryable so that:

1. Gregale can tell the customer "you have 23 endpoints; 4 are not
   declared in your edge rules".
2. The dashboard can suggest protection rules (see
   `docs/issues/api-native/README.md` — workflow).
3. The auto-generation flow in sub-issue #02 has raw material.

## Proposal

Promote the captured OpenAPI / introspection document from a discarded
side-effect of characterization into a first-class artifact.

### Data model

New table `app_openapi_docs`:

```sql
CREATE TABLE app_openapi_docs (
  app_id          UUID PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
  source          TEXT NOT NULL CHECK (source IN ('openapi_json','graphql_introspection','manual_import','auto_generated')),
  document        JSONB NOT NULL,
  endpoint_count  INT  NOT NULL CHECK (endpoint_count >= 0),
  sha256          BYTEA NOT NULL,
  captured_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  captured_by     TEXT  NOT NULL CHECK (captured_by IN ('guest_probe','customer_import','auto_gen'))
);
```

The probe upload runs from `guest/init` after characterization completes.
On conflict (`app_id`), update the row + bump `captured_at`. SHA256 is
computed at insert time so the dashboard can diff versions.

### API surface

- `GET /v1/apps/{slug}/openapi` → returns the stored document + metadata.
- `GET /v1/apps/{slug}/endpoints` → returns a normalized list:
  `{ method, path, declared_in_edge_rules: bool, last_seen_at, status_2xx_p50 }`.
  This is the artifact the dashboard consumes for the "23 endpoints / 4
  undocumented" workflow.

### Guest probe changes

- `characterize_linux.go:487-505` already buffers the body. Change it to
  POST the buffer back to a control-plane endpoint over the existing
  in-guest egress (subject to tenant egress deny-list — the host can
  serve a unix-socket sink via `gatewayd-internal` instead of going
  through the public edge).
- Add a `characterization_report_openapi` field to the wire shape at
  `pkg/api/characterization.go:24-57` so the upload is decoupled from the
  workload-class decision (capture always; classification is allowed to
  downgrade or discard).

### Cardinality

Endpoint count per app is bounded by the route cap from ADR-093 (50), so
even with the document stored we don't introduce unbounded cardinality
into metrics. Prometheus does not see the document; the dashboard queries
the table directly.

### Limits (pkg/api/limits.go)

- `endpoint_discovery_per_app` — max 1 stored doc per app (PK enforces).
- `endpoint_discovery_max_doc_bytes` — 256 KB cap on the stored document
  to bound storage; reject larger with 413 at upload time.

## Acceptance

1. Probe captures OpenAPI 3.x document for an app serving `/openapi.json`
   and persists it.
2. `GET /v1/apps/{slug}/openapi` returns it; `GET /v1/apps/{slug}/endpoints`
   returns the normalized list.
3. The dashboard can mark a path "declared in edge rules" by joining
   `edge_rules.match_path` (regex) against the stored paths.
4. Existing characterization workload-class decision is unchanged.
5. Migration is numbered in the next available slot; cross-PR fence check
   runs before commit (see memory `cross-pr-slot-precheck-pr-867-collision-2026-08-13`).

## Dependencies

- Foundation only. No dependency on #05 (consumer identity).
- Feeds #02 (auto-generation reads from this table).

## Audit provenance

- `guest/init/characterize_linux.go:433-455`, `:487-505` — probes run, document discarded.
- `pkg/api/characterization.go:24-57` — wire shape lacks document field.
- `docs/adr/051-characterization-boot-workload-classification.md:61-67` — aspirational "full route list" not implemented.
