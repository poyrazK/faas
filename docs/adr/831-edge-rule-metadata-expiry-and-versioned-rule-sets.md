# ADR-831: Edge-rule metadata, expiry, and versioned rule sets

- **Status:** accepted
- **Date:** 2026-10-08
- **Related:** ADR-091 (edge rules; D20.8 deferred maintenance TTL), ADR-160
  (edge-rule cache), ADR-201

## Context

Edge rules carry no operator-facing identity: a listing is a column of UUIDs
and kinds. Temporary policy (a maintenance window, a block during an
incident, a short-lived redirect) has to be removed by hand, and ADR-091
deferred the maintenance TTL (D20.8) for want of an `expires_at`.

Rule changes are also unversioned. The change log records only the operation
and hosts, the `edge_rule.updated` audit row carries no rule content, and
PATCH has no precondition, so an operator cannot see what an app's policy was
before a bad edit, cannot restore it, and two concurrent editors silently
overwrite each other.

## Decision

### 1. Metadata and expiry (columns on `edge_rules`)

- `name` (≤ 100 characters, trimmed, NULL when unset), `description`
  (≤ 1000), `expires_at timestamptz` (NULL = never). CHECK constraints mirror
  the API bounds.
- An expiry must be in the future when set. PATCH sets it with `expires_at`
  and removes it with `clear_expires_at`.
- **Expired rows are retained** and listed with `expired: true`; nothing
  deletes them. The gateway read (`MatchEdgeRulesForHost`) excludes them.
- The gateway does not wait for a notification when a rule lapses. The loader
  records the earliest future `expires_at` of a host's rules as the cache
  entry's `NotAfter`, which caps the entry lifetime, so the next request after
  expiry reloads without the rule. The outage fallback (last-known rules
  while Postgres is unreachable) refuses an entry past `NotAfter`, so a lapsed
  maintenance window or block is never replayed. Expired rows are also dropped
  at compile time to absorb gateway/database clock skew.
- Trace evaluates expired rules as skipped with an "expired" reason.

### 2. Versioned rule sets

- `edge_rule_set_versions(app_id, version, rules jsonb, rules_sha256,
  rule_count, created_at)`, unique on `(app_id, version)`. `version` is
  per-app and monotonic. `rules` holds every column needed to recreate each
  rule, including its ID.
- A **deferred constraint trigger** on `edge_rules` snapshots the app's whole
  rule set at commit. Every write path (API, manifest async routes, route
  policy, OpenAPI apply, rollback) is covered without per-handler code, and a
  multi-statement transaction produces one version (later row triggers in the
  same commit see an identical digest and skip). A snapshot whose digest
  equals the app's latest version is not stored.
- Retention: the newest 100 versions per app; older rows are pruned by the
  trigger.
- **Rollback** (`POST /v1/apps/{slug}/edge-rules/rollback`, `{"version": N}`)
  replaces the app's rules with version N in one transaction, preserving rule
  IDs, behind the usual two-phase fleet convergence over the union of current
  and restored hosts. It records a new version (history is append-only). It
  is refused when the restored set exceeds the app's current plan quota or
  references a CORS preset that no longer exists.
- **Optimistic concurrency:** listing and mutations return
  `ETag: "<latest version>"`. A mutation sent with `If-Match` is refused with
  412 when the app's latest version differs. Without `If-Match` behaviour is
  unchanged.

## Consequences

- Operators can label rules, time-box them, list prior policy, and roll back.
- A rule's expiry takes effect within one request of the deadline on every
  gateway, with no background sweeper and no notification.
- Each rule mutation costs one extra snapshot insert at commit (bounded by
  the per-app rule cap) and storage of up to 100 versions per app.
- The version is set-level, so `If-Match` on one rule also fails when an
  unrelated rule in the same app changed. This is deliberate: rule evaluation
  depends on the whole ordered set.
- Rollback can reintroduce a host-scoped rule whose host has since moved to
  another account; request-time owner scoping (OwnedEdgeRules) still ignores
  it, as for any foreign `match_host`.
