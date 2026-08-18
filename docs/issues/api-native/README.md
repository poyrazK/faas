# API-native protection & observability — mega issue

Tracking issue for the 12-feature capability audit performed on 2026-08-18 against
the Gregale codebase. Eight of the twelve capabilities are not implemented or
only partially implemented; together they constitute the "Cloudflare lesson,
adapted to the application runtime" framing the platform is missing.

## What this is

A coordinated set of sub-issues that, taken together, lift Gregale from
"deploy a container" to "deploy an API with first-class protection,
observability, and per-consumer analytics". Each sub-issue is independently
shippable; together they unlock the workflow:

> Gregale discovered 23 endpoints
> → 4 are undocumented
> → 2 frequently receive invalid payloads
> → 1 has an abnormal error rate
> → suggested protection rules are ready

## Scope

In scope (8 items):

| # | Feature | Verdict | Sub-issue |
|---|---|---|---|
| 1 | Automatic endpoint discovery | ❌ | [01-endpoint-discovery.md](01-endpoint-discovery.md) |
| 2 | OpenAPI import + auto-generation from deployed app | �️ export only | [02-openapi-import-gen.md](02-openapi-import-gen.md) |
| 3 | Schema validation w/ observe / warn / block modes | ⚠️ block only | [03-validate-modes.md](03-validate-modes.md) |
| 4 | CORS presets (named reusable profiles) | ❌ | [04-cors-presets.md](04-cors-presets.md) |
| 5 | Application API keys w/ scopes + expiration | ❌ | [05-app-api-keys.md](05-app-api-keys.md) |
| 6 | Consumer-level usage analytics | � | [06-consumer-analytics.md](06-consumer-analytics.md) |
| 7 | Route-level cold-start + egress attribution | ⚠️ traffic/latency only | [07-route-metrics-coldstart.md](07-route-metrics-coldstart.md) |
| 8 | Queryable request logs | ❌ | [08-queryable-request-logs.md](08-queryable-request-logs.md) |
| 9 | Safe request replay with redaction | ⚠️ verbatim replay | [09-safe-replay.md](09-safe-replay.md) |

Out of scope (already implemented at the level needed):

- Per-route rate limits (ADR-091, ADR-104)
- JWT validation at the gateway (ADR-091 D9–D10)
- Request-ID propagation across gateway + runtime (`x-faas-request-id`)

## Architectural framing — the consumer identity gap

The dominant reason the bulk shape of this audit is "� / ⚠️" is the absence
of a **consumer** as a first-class entity. Gregale today models:

- **account** → owns apps, deployments, secrets, billing, throttle bucket.
- **api_keys** → Gregale-platform credentials bound to the account, with
  account-scoped grants (`/v1/apps`, `/v1/usage`, etc.). Defined in
  `schema.sql:506-526`, documented scopes in `pkg/api/apikey.go:104-138`.

This is the *operator* identity. The application itself, once deployed, has
no concept of its end-user — the consumer that hits the public URL with a
JWT, a bearer token, or anonymously. ADR-079 explicitly states that per-app
public auth reuses the owner account's `apps:read` platform key
(`docs/adr/079-per-app-public-auth.md:71-87`), which is a deliberate
shortcut for v1, not the target shape.

Sub-issues **05 (app API keys)** and **06 (consumer analytics)** together
introduce the consumer model. The remaining sub-issues plug into it:

- **04 CORS presets** references consumers when it wants to expose per-key
  origin allowlists.
- **06 consumer analytics** uses the consumer row as the metering key.
- **07 route metrics** uses consumer as a *reserved label* (admitted free,
  per the ADR-093 cardinality framework at
  `docs/adr/093-per-route-app-metrics.md:108-118`).
- **08 request logs** stores `consumer_id` as a queryable column.
- **09 safe replay** redacts any secret header bound to a consumer identity.

**Rollout order** (each tier can ship independently once green):

1. **Foundation** (no dependencies):
   - #03 validate modes (small, fills a D-shaped hole in ADR-091)
   - #04 CORS presets
2. **Identity** (the load-bearing change):
   - #05 app API keys
3. **Analytics on the identity** (depend on #05):
   - #06 consumer analytics
   - #07 route metrics cold-start split (uses consumer as a label)
   - #08 queryable request logs (uses consumer as a column)
4. **Discovery + replay** (depend on #08 for stored captures):
   - #01 endpoint discovery
   - #02 OpenAPI import + gen
   - #09 safe replay

## Cross-cutting constraints (from CLAUDE.md)

These apply to every sub-issue:

- New quota / limit → must be added to `pkg/api/limits.go`, never inlined.
- New SQL → `sqlc`-generated queries only, every state column has a CHECK,
  migrations are goose-numbered, append-only.
- Money: integer cents / millicents. Floats near money fail review.
- No global state except wiring. Handlers ≤ 50 lines, extract.
- Logs: `slog` JSON. Metrics: Prometheus names as specced in §12.
- Never log secret values. Consumer keys are sealed at rest (mirroring G2
  lean for env secrets — §17).
- Component ownership (§CLAUDE.md): `apid` is the only writer to
  customer-intent tables (apps, deployments, domains, **and now edge rule
  presets, consumer keys**); `vmmd` is the only root component and does not
  see HTTP envelopes; `gatewayd-internal` enforces rules at the edge.

## Acceptance for the mega issue

The mega issue is closed when **all** of the following hold:

1. Every sub-issue is merged (or explicitly closed as wontfix with rationale).
2. The "23 endpoints discovered → 4 undocumented → 2 invalid payloads → 1
   abnormal error rate → suggested rules" workflow runs end-to-end against
   a representative app (any e2e app with ≥ 5 routes) and is documented
   under `cmd/e2e/api_native_e2e_test.go`.
3. The new surfaces are exposed in the OpenAPI document and the SDK regen
   produces Go / Node / Python bindings (per the spec-sync flow in
   `docs/adr/085-spec-sync.md`).
4. Limits table (`pkg/api/limits.go`) carries entries for: consumer keys
   per app, consumer keys per account, request log retention days, replay
   redaction rule count.

## Audit provenance

Each sub-issue links back to the file:line proof from the audit. The full
audit table lives in the chat log of 2026-08-18; the load-bearing citations
are repeated in each sub-issue so reviewers don't have to chase the
parent thread.

## Status

Draft. Sub-issues 1–9 are linked below. PR will follow once each sub-issue
has been sanity-checked against the current branch state.
