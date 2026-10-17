# ADR-1346: Runtime binding audiences and explicit queue release requirements

- **Status:** Accepted
- **Date:** 2026-10-13
- **Context:** The shared inventory erased queue environments, so a staging
  consumer could block production. Runtime verification also required manual
  probes and application acknowledgements for release-only PostgreSQL migration
  credentials. Stored enforcement rejected healthy queues because connectivity
  probes remain unsupported.
- **Scope:** Preserve queue environments and filter requested inventories.
  Assess only shared legacy queues and queues in the selected deployment scope,
  including when calculating the traffic-write expiry deadline. Publish
  `queue_environment` explicitly: empty means shared, including when a real
  environment is named `app`. Older inventory scope labels remain readable.
- **Audience:** Keep migration bindings visible with `release_only` skipped
  results. Batch verification creates no migration probes, manual tasks cannot
  record their trusted evidence, and runtime ACK/rotation requirements do not
  apply. The existing deployment release-command gate remains authoritative:
  failed release tasks prevent activation. Skipping a runtime probe is not a
  claim that schema migration or database connectivity succeeded.
- **Queue policy:** Add bounded `queue_requirements` maps from binding names to
  `consumer_readiness`. This is an explicit, per-binding choice requiring one
  enabled push binding and an active healthy scheduler poll no older than thirty
  seconds. Missing, ambiguous, disabled, pull, unobserved, stale or unhealthy
  queues block. Readiness also requires the explicit queue environment; an older
  inventory cannot qualify an environment whose queue scope was erased.
  Unlisted queues retain unsupported connectivity coverage and
  block stored enforcement. Outbound requirements and unknown families retain
  existing behavior. A satisfied readiness requirement has a passed policy
  result with partial connectivity coverage and an explicit warning; no message
  is sent or consumed by checks.
- **Compatibility:** `PUT .../bindings/release-policy-with-queues` requires an
  explicit map, including `{}` to clear it, and can carry storage requirements.
  Older servers return 404 before mutation. Existing routes reject implicit
  removal of queue or storage requirements. CLI writes require the current
  revision and validate receipts. Policy-aware checks confirm map contents and
  revision after each read. The thirty-second queue deadline remains independent
  of probe age and is rechecked under the existing traffic fence.
- **Persistence and rollback:** Store the map with policy/history and compare it
  in both memory and PostgreSQL traffic grants. Policy mutations retain existing
  revision triggers. Migration rollback locks policy writers and refuses to
  remove a nonempty queue requirement; clear it explicitly first. The changes
  add no VM lifecycle actions, provider calls or guest protocol.
- **Validation:** Cross-environment inventories and deadlines, shared legacy
  queues and an environment named `app`, release-only probe/ACK exclusion, queue
  readiness failures and unrelated unsupported bindings, explicit clear/retain
  semantics, old-server rejection, memory/PostgreSQL policy and traffic parity,
  expiry and policy-change races, migration rollback, and API/SDK generation.
