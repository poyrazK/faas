# ADR-218 · Curated global organization activity timeline

- **Status:** accepted
- **Date:** 2026-09-22
- **Decision:** Add an append-only `org_activity` projection and expose it as
  `GET /v1/orgs/{slug}/activity`. Entries are organization-scoped structured
  facts with captured actor/resource labels, optional app/project/deployment
  identifiers, safe kind-specific metadata, and an idempotent producer source
  key. The read path uses stable `(occurred_at DESC, id DESC)` keyset
  pagination and optional kind, actor, and app filters.
- **Why:** Gregale currently records related history across `events`,
  `audit_log`, `deployment_audit`, deployment rows, domain state, and daemon
  logs. Those stores have different retention, tenancy, and payload contracts.
  A customer should not have to reconstruct one workspace's infrastructure
  history from security and operator products, and exposing a raw union would
  leak internal vocabulary and make redaction depend on every reader.
- **Security:** The projection accepts JSON objects containing non-secret
  display metadata only. Environment values, secret values, credentials,
  tokens, raw provider payloads, and client IPs are forbidden. Actor and
  resource labels are copied at emission time so a later account, API-key, or
  resource deletion cannot rewrite history. Every read is pinned to the
  `LoadOrg` membership's organization id before optional filters are applied.
- **Initial producers:** Image, source-ref, local-tarball, resumable-upload,
  and GitHub deployments; environment set/delete; domain attachment; TLS
  issuance; and requested rollbacks. New producer kinds may be added without a
  schema change, but each needs an explicit safe-data mapping and tests.
- **Organization attribution:** Existing apps are account-owned, so their
  activity projects into the app owner's personal organization regardless of
  the caller's active org or org-bound API key. Those credentials can authorize
  a mutation but cannot establish the app's owning organization. The first
  shared-workspace app slice adds `POST /v1/orgs/{slug}/apps`, which resolves
  ownership from verified membership and persists that organization on the new
  app before activity is emitted. The creator account remains the current
  quota/billing and app-API authorization identity; existing app-specific
  routes are not yet shared-member aware. Follow-up work must migrate those
  routes by permission class before shared-workspace app management is
  complete. Authoritative resource `org_id` attribution is a prerequisite for
  calling the shared-workspace history complete.
- **Delivery:** `(org_id, source_type, source_id)` is unique, so retries and
  webhook redelivery return the original row. The initial apid projection is a
  post-commit side effect: failure is logged and never changes a successfully
  committed infrastructure mutation into an ambiguous HTTP failure. A durable
  projection outbox and reconciliation/backfill pass are required before the
  timeline can be described as complete historical evidence.
- **Historical seed:** Backfills import legacy deployment intents, environment
  changes, and custom-domain attachment/removal only when the surviving app
  row establishes its organization. They use a strict safe projection,
  suppress legacy rows at or after the live-producer cutover, and use stable
  source keys for replay safety. Environment values, raw audit payloads,
  deleted/unattributed apps, and other audit products remain outside the seed.
- **Consequences:** Customers gain one display-ready workspace history while
  the security audit and deployment forensics products keep their specialized
  schemas. The new table is deliberately FK-free and append-only, so deleting
  source resources does not erase the explanation of what happened. Storage
  grows monotonically until a separately-decided retention/export policy is
  added.
- **Rejected alternatives:** Query-time `UNION` over existing audit tables was
  rejected because organization attribution, actor identity, ordering, and
  redaction differ per source. Reusing `events` was rejected because it is an
  account-oriented security/daemon vocabulary and contains payloads that are
  not a stable customer UI contract. Building the timeline only in the browser
  was rejected because it reproduces the same joins, pagination gaps, and
  access-control risks in every client.
