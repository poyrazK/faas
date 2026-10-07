# ADR-681 · Fence image deployment promotion against newer accepted releases

- **Status:** accepted
- **Date:** 2026-10-07
- **Decision:** Extend the existing same-app, same-scope revision check to
  automatic image promotion. `MarkDeploymentLiveIfLatest` compares the candidate
  with all accepted deployment intents while holding the same app lock used by
  admission. An older candidate becomes superseded with zero traffic. The
  previous Git-driven method remains a compatibility name for this operation.
- **Why:** Compose and CI-published workloads use `kind=image`. The former
  GitHub/preview-only promotion check therefore missed them: a slower image
  deployment could replace an already accepted or live newer release.
- **Consequences:** The latest accepted revision is eligible for automatic
  cutover after the existing readiness, smoke, and policy checks. A newer failed
  or cancelled intent still blocks an older in-flight image; the current serving
  release stays available. Different environment scopes remain independent.
  Already-live canaries and traffic splits retain their existing redelivery and
  rollout semantics. Explicit and checked rollback keep their existing promotion
  path and can intentionally select a historical release.
- **GitHub provenance:** Image deployments carrying a recorded GitHub source
  branch now use the same pre-promotion branch check as source builds. Moved or
  deleted branches fail with `source_ref_stale`; unavailable verification fails
  with `source_ref_unavailable`. Pinned commits and tag events without branch
  metadata keep their existing semantics. PR previews retain their existing
  revision check and do not gain a branch-head requirement.
- **Boundaries:** Revision order means acceptance order, not image publication
  time or Git ancestry. The image-published endpoint still accepts an immutable
  digest without Git branch provenance; CI owns the ordering of its publication
  calls. The remote branch lookup cannot be atomic with the database cutover.
  No schema migration, public API/SDK change, registry poller, or VM lifecycle is
  introduced.
- **Rejected alternatives:** A worker-side latest-row read without the app lock
  races concurrent admission. Ordering by timestamps or image tags cannot prove
  intent. Applying automatic ordering to the explicit rollback path would block
  intentional recovery.

This extends [ADR-311](311-github-push-freshness-and-promotion-fence.md),
[ADR-316](316-source-ref-branch-freshness-before-promotion.md), and the
[ADR-679 image handoff](679-image-published-deployment-trigger.md).
