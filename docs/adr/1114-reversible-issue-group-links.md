# ADR-1114 · Reversible issue group links

- **Status:** accepted
- **Date:** 2026-10-11
- **Decision:** Let owners merge existing fingerprint groups within one app and
  environment, then split a linked original group back out. Preserve original
  issue IDs, fingerprints, occurrence payloads, release totals, resolutions, and
  activity. Store direct links to one canonical issue instead of moving evidence.
- **Behavior:** The inbox shows canonical issues with summed lifetime counters
  and combined release/activity history. Retained customer impact deduplicates
  customers across members. Occurrences retain their original issue ID and verify
  explanations against their original fingerprint. Reads and retries of a linked
  ID return the current canonical ID. New captures retain original membership;
  lifecycle and ownership actions apply to the entire current group. Merging
  adopts the destination's lifecycle and owner. Splitting restores one original
  identity and its lifetime counters, opens it for review, and keeps its current
  owner. The remaining group keeps its current lifecycle.
- **Bounds and concurrency:** At most 32 original identities belong to a group,
  defined in pkg/api/limits.go. Original identities still count toward storage
  quotas. All mutations serialize on the existing app lock. Links have scoped
  foreign keys and a database guard against nested links/cycles. Merging a group
  into another flattens its members. Repeating a merge or completed split is a
  no-op; conflicting membership is rejected. Authenticated mutation controls use
  existing write scopes, MFA, API idempotency, and dashboard CSRF protections.
- **Audit:** Merge/split append actor-attributed activity on both identities.
  Existing lifecycle and impact notifications use canonical issue metadata;
  regrouping itself emits no new webhook event type. Links and audit data survive
  automated migration rollback. Historical counters survive event retention.
- **Scope:** Splitting arbitrary occurrences inside one original fingerprint
  requires a durable matching rule for future captures and is separate work.
  These controls never recompute fingerprints or silently migrate strategies.
- **Rejected alternatives:** Moving occurrences loses expired-event release
  totals and original grouping explanations. Deleting source issues breaks saved
  links and retries. Recursive merge chains complicate bounded reads and permit
  cycles. Replaying retained events cannot reconstruct lifetime history.
