# ADR-679: Image-published deployment trigger

- **Status:** accepted
- **Date:** 2026-10-07
- **Decision:** Add `POST /v1/apps/{slug}/image-published` and
  `gregale registry published`. CI supplies the exact digest after pushing an
  image. Existing app-bound deploy tokens, CI bearers, and `deploy:write` keys
  authenticate the request. The workload must already declare a project image,
  and the published registry/repository must match. An immutable declaration
  cannot advance to another digest through this trigger.
- **Why:** A repository push can arrive before CI publishes its image. Moving
  a tag also does not change a project source hash. An explicit post-publish
  handoff makes externally built images deployable without editing Compose or
  racing a registry tag lookup.
- **Deduplication:** A namespaced deterministic deployment UUID binds app,
  normalized scope, and pinned image reference. The existing deployment primary
  key makes concurrent deliveries atomic in memory and PostgreSQL. First
  acceptance returns 202; duplicates return 200 and the current durable row,
  including failed, cancelled, and superseded states. Replays neither enqueue
  new work nor resurrect a release after rollback. First accepted configuration
  wins; explicit retries or configuration redeploys use existing endpoints.
- **Admission:** Share the normal JSON-image deployment path, retaining
  signature/security policy, plan and traffic validation, actor/activity
  recording, account rate limits, and downstream image verification. The
  Compose container port supplies the default port. Omitted workflows and the
  source release command inherit from the latest image deployment in the same
  scope. App command, service-binding, and environment behavior remain governed
  by existing contracts. The tag declaration is not replaced with a digest.
- **Ownership:** Apid records the image deployment and durable lifecycle
  outbox; imaged resolves the pinned index/child, verifies, and materializes it.
  Existing readiness and public-route verification guard traffic cutover.
  No source build, new registry poller, or VM lifecycle is introduced.
- **Boundaries:** This first slice accepts a CI request, not provider-specific
  registry webhook bodies. CI owns publication ordering and sends the pushed
  digest. New scopes have no prior release/workflow defaults. Direct ephemeral
  deployment overrides are not inferred from previous releases. Source-defined
  operations on image workloads retain ADR-678's boundary. Normal deployment
  authorization and environment semantics apply.

See [the CI handoff](../image-published-deployments.md) for usage.
