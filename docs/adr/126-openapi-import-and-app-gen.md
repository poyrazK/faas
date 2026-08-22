# ADR-126 · OpenAPI Import + Auto-Generation (issue #975 item #2)

- **Status:** Proposed
- **Date:** 2026-08-22
- **Supersedes:** none
- **Related:** ADR-122 (endpoint discovery — per-deployment
  capture, item #1), ADR-085 (OpenAPI spec-sync),
  ADR-091 (per-app public auth), ADR-093 (per-route app
  metrics — observed-routes source for `?source=auto`),
  ADR-089 (per-app metrics vocabulary).

## Context

Item #1 (ADR-122) shipped a read-only capture path: Gregale
fetches the customer's `/openapi.json` during cold boot, stores
it per-deployment, and exposes it via apid. The customer can
*see* what Gregale observed — but they cannot yet *shape* the
platform's view of their surface through Gregale. Item #2 closes
that loop.

The customer's declared API surface is an app-level invariant
(one spec per app), distinct from a deployment-level artifact
(a given build may serve a different `/openapi.json` than the
last). Today's `deployment_openapi_docs` table from #1 captures
the latter; this ADR introduces a sibling `app_openapi_docs`
table for the former, plus the surface to consume it:

1. **Manual import** — `POST /v1/apps/{slug}/openapi` lets the
   customer upload their OpenAPI 3.0/3.1 doc at the app level,
   validated against a vendored OpenAPI 3.1 meta-schema (no
   network fetch in the validator path).
2. **Dry-run** — `POST /v1/apps/{slug}/openapi/dry-run` accepts
   the same body, returns `[]EdgeRuleSuggestion` of suggested
   edge rules that the customer can paste into the existing
   create-edge-rule endpoint. Mirrors the throttle-suggestions
   read-only pattern at `pkg/api/dto.go:5339-5407` and
   `cmd/gregale/commands_metrics.go:172-176`.
3. **Auto-generated spec** — `GET /v1/apps/{slug}/openapi?source=auto`
   returns a merged OpenAPI doc combining: (a) the imported doc,
   (b) ADR-093 observed routes, (c) existing edge rules
   projected as `x-faas-edge-rules` extension properties on each
   operation. Degrade-don't-502: when the gateway-side observed
   routes read is unavailable, return a `degraded: routes_unavailable`
   source string rather than failing.
4. **Delete** — `DELETE /v1/apps/{slug}/openapi` is idempotent
   204, audit-emits `app.openapi_import.deleted`.

The downstream unblock is the dashboard's "complete API picture"
view, which today cannot tell the customer which paths are
declared vs. observed vs. enforced.

## Decision

Six sub-decisions land together because each constrains the
others.

### D1 · Per-app table, not per-deployment

The customer's declared surface is one spec per app — different
builds of the same app serve the same `/openapi.json` from
upstream. The new `app_openapi_docs` table keys on `app_id`
(one row per app, INSERT ... ON CONFLICT DO UPDATE for
overwrite-not-insert). It co-exists with the per-deployment
`deployment_openapi_docs` from #1:

- `deployment_openapi_docs` → `GET /v1/apps/{slug}/deployments/{deployment}/openapi`
  (audit / source-of-truth — what the customer actually served
  on this build).
- `app_openapi_docs` → `GET /v1/apps/{slug}/openapi` (dashboard
  surface — what the customer declares the app exposes).

The two tables serve different read paths and do not share a
code path beyond the apid layer's load.

### D2 · Strict validate via vendored OpenAPI 3.1 meta-schema

The OpenAPI 3.1 meta-schema is a JSON Schema 2020-12 document
(santhosh-tekuri/jsonschema/v6 compiles it under `Draft2020`).
It is vendored at `pkg/openapiimport/schemas/openapi-3.1.json`
via `//go:embed` (no network fetch in the validator path —
fail-closed). 3.0.x docs that do not use 3.0-only features
(specifically `nullable: true`) pass; customers needing strict
3.0 can ship 3.1. The early-reject `looksLikeOpenAPIDoc` sniff
at `cmd/apid/handler_openapi_doc.go:341` stays as a cheap
pre-filter; the strict validate is the gate.

The `schemaRefURLPattern` guard at
`pkg/edgevalidate/jsonschema.go:41` (rejects external `$ref`
URLs) does NOT apply here — it gates customer-supplied schemas
compiled into the runtime validator, not the imported doc
being meta-schema-validated.

### D3 · Dry-run returns suggested rule objects

Mirrors `pkg/api/dto.go:5339-5407` (`ThrottleSuggestionRow`)
and `cmd/gregale/commands_metrics.go:172-176` (`gregale
throttle-suggestions --dry-run`). For each path in the imported
doc not currently covered by an edge rule of matching kind,
emit a candidate `EdgeRuleSuggestion{Path, Methods, Kind,
Action}` that the customer can paste into the existing
create-edge-rule endpoint. Dry-run does NOT write; the response
is read-only. The endpoint is `POST /v1/apps/{slug}/openapi/dry-run`
(POST because the body IS the import body — same shape as the
write endpoint, minus persistence).

### D4 · Auto-gen cache key `(app_id, sha(doc), sha(routes), sha(rules))`, TTL 5 min

Lives in `pkg/openapidiff/spec_cache.go` as an in-process
`*lru.Cache[string, *Spec]` (256 entries, one per app in the
busiest tier-1 control plane). Per-app key:
`fmt.Sprintf("%s/%x/%x/%x", appID, docSHA, routesSHA, rulesSHA)`.
Apid-process-local — no Redis. The cache is invalidated
wholesale per app on `NotifyAppOpenAPIDocChanged` OR
`NotifyEdgeRuleChanged` (wholesale because the payload does not
carry affected IDs, and the cache is bounded). TTL=5 min
guarantees freshness even if a notify is missed.

### D5 · Invalidation via `NotifyAppOpenAPIDocChanged` (new) + existing `NotifyEdgeRuleChanged`

Two-channel pattern: the imported-doc write triggers
`app_openapi_doc_changed`; edge-rule writes trigger
`edge_rule_changed` (already wired). Apid subscribes to both
in a new `cmd/apid/openapi_doc_subscriber.go` (mirror
`cmd/apid/audit_subscriber.go:60-68`); the subscriber flushes
the per-app cache entry on either notification.

Payload shape: `{"app_id":"<uuid>","op":"created|replaced|deleted"}`
— mirrors `NotifyEdgeRuleChanged`'s 3-arm pattern but without
`rule_id`.

The deployment-level `NotifyOpenAPIDocChanged` from ADR-122
§D5 is a separate gap that stays unaddressed in this PR (no
reader needs it yet).

### D6 · Limits are abuse-surface, not plan-tier

Every plan including Free can import:

- `OpenAPIImportMaxDocBytes = 262144` (256 KiB)
- `OpenAPIImportMaxEndpoints = 50`
- `OpenAPIImportsPerApp = 1` (one import per app — overwrite,
  not multi-version)
- `OpenAPIImportsPerAccount = 100/1000/10000/10000` ladder
  (Free/Hobby/Pro/Scale) — prevents a single account from
  spamming `app_openapi_docs` with throwaway rows.

The per-app cap of 1 is the load-bearing number; the
per-account cap is defensive. ADR-122's free-plan-OFF posture
is wrong for this surface — endpoint discovery is read-only
capture; this is write-side shaping the customer wants to do
regardless of tier.

## Consequences

### Positive

- Customers can shape Gregale's view of their surface
  end-to-end: capture (item #1) → declare (this PR) → enforce
  (existing edge rules) → consume (auto-gen).
- Dashboard gets a single source of truth: declared ∪ observed
  ∪ enforced, with `x-faas-edge-rules` annotations carrying
  the kind+action so the customer sees exactly which rules
  apply where.
- Dry-run is read-only and `POST`-only — no schema, no audit
  emit, no quota increment.

### Negative

- Adds two pg_notify channels to the apid subscriber set
  (`NotifyAppOpenAPIDocChanged` new, `NotifyEdgeRuleChanged`
  existing). The subscriber is small and mirror-pattern; the
  cost is a goroutine + LISTEN per apid process.
- The vendored OpenAPI 3.1 meta-schema is a dependency on the
  upstream OAI repo at a pinned SHA. Future OpenAPI 3.1.x
  revisions require updating the vendored copy; future 3.2
  (if it lands) requires a new vendored copy and a parallel
  validator branch.
- Per-account quota `OpenAPIImportsPerAccount` is defensive
  against per-app single-row bulk-write abuse — the per-app
  cap of 1 already forecloses the obvious abuse vector; the
  per-account cap is "in case the per-app cap ever moves".

### Neutral

- The OpenAPI 3.0 strict-validation gap (D2) is documented
  but not closed in this PR. Customers needing strict 3.0 can
  ship 3.1; the meta-schema gap is a documentation issue, not
  a security issue.

## Alternatives considered

1. **Per-app overwrite is fine; multi-version is over-engineering.**
   The customer's declared surface is one spec per app. Storing
   N versions per app would need an additional `version`
   discriminator + active/inactive flag + cascading logic. The
   dashboard can show "imported doc v3 → auto-gen vN" via the
   existing deployment history; storing multiple imports per
   app adds zero product value at significant complexity cost.

2. **Dry-run via `?dry_run=true` query param on POST.**
   Rejected: query params on POST are ambiguous in the
   dashboard's "paste URL into another tool" workflow, and the
   route conventions in `cmd/apid/server.go:869` (per-app
   surface, query-param-driven GET, body-driven POST) are
   cleaner with `/dry-run` as a sibling path.

3. **Auto-gen runs at request time, no cache.**
   Rejected: the merge cost (compile 3 edge-rule actions × N
   operations + walk imports + walk observed routes) is
   ~5-15 ms per call. The dashboard polls this on the routes
   view; without a cache we'd see 5-15 ms p99 per poll. 5 min
   TTL keeps the merge cost off the hot path while staying
   fresh enough for the dashboard use case.

4. **Per-rule `(kind, host, path, method)` cache invalidation.**
   Rejected: the cache is small (256 entries max). Wholesale
   per-app flush is one map delete; per-rule fan-out is O(rules).
   The simpler algorithm wins.

5. **Limit per-account even tighter (10/100/1000/10000).**
   The 100/1000/10000/10000 ladder is conservative — at 256 KiB
   × 100 = 25 MiB per account worst-case, well under the
   deployment table row size. Tighter limits would surprise
   customers with large microservice farms (one import per
   service × 100 services = 100 imports, just at the Free
   cap).
