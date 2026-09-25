# ADR-239 · Expiring revision pins and project release sets

- **Status:** implemented for HTTP ingress, durable invocations, and managed service calls
- **Date:** 2026-09-25
- **Decision:** An app may opt into `revision_pin_ttl_seconds` (maximum seven
  days). When a stable, canary, or service rollout replaces a live deployment,
  the predecessor remains `live` at 0% traffic with a cutover deadline,
  extended only when a published release set still requires that revision.
  `X-Gregale-Revision` contains an exact deployment UUID; the edge
  accepts it only for that app, scope, live status, and unexpired deadline.
  The selected UUID is returned on successful responses. A minute-level
  meterd sweep supersedes expired rows, while lookup checks the deadline
  directly so a delayed sweep cannot extend eligibility.
- **Project graph:** `POST /v1/projects/{slug}/environments/{environment}/release-sets`
  publishes one immutable deployment ID per project workload. All members
  must currently be live and either traffic-bearing, an explicitly dark
  manual deployment, or retained by a valid pin/release; every app's pin TTL must
  cover the release TTL. The insert and active-pointer swap share one
  transaction. The active set has no deadline; its configured TTL starts at
  replacement. At replacement, retained 0%-traffic members are kept through
  that deadline. `X-Gregale-Release` identifies an active or unexpired set. Ordinary
  production project ingress follows the active set; a client may continue
  an older set by sending its ID. The gateway returns the selected release
  ID and forwards it to the guest.
- **Internal routing:** Managed service calls retain same-account and declared
  binding authorization. The guest bridge resolves the caller app and exact
  deployment from its source IP; a release is usable only if that deployment
  is a member. The target service deployment comes from the same immutable
  set, independent of traffic weights. Without an explicit header, a unique
  release membership can be inferred. Ambiguous membership fails with 409,
  so middleware/SDKs must propagate `X-Gregale-Release` for callers reused
  across release sets. Exact one-hop deployment overrides conflict with a
  release, rather than escaping the graph.
  A caller's app-scoped revision header is stripped on the downstream hop.
  The guest listener rechecks the source IP against the live instance table
  on each call, because a VM network slot can be reused immediately; the
  indexed lookup fails closed on ambiguous identities.
  Ordinary unpinned service calls exclude retained zero-traffic replicas;
  direct one-hop overrides of retained revisions require a valid direct pin.
  A release-set target is validated by graph membership instead, so disabling
  direct pins does not invalidate an already-published release set.
- **Failure posture:** Unknown or expired explicit pins fail closed; store
  errors do not fall back to weighted routing. Rollback may select a retained
  zero-traffic revision. The weighted picker continues to ignore 0% rows.
  Existing apps are unchanged until they enable a TTL or publish a set.
- **Durable work:** Async invoke, delayed tasks, queues, inbox messages, and
  asynchronous edge routes capture the selected release or direct revision
  when enqueued. The scheduler and gateway revalidate it before delivery.
  Expired pins fail the row rather than rerouting it to a newer release.
  Cron and event producers without a client pin select the active release at
  delivery time; they do not inherit a prior caller's release context.
- **Limits:** The graph contract covers public HTTP/WebSocket handshakes,
  managed HTTP service calls, and the durable work above. Direct external
  calls and clients that do not replay a release header are not automatically
  pinned. WebSocket connections already established on a selected deployment
  stay there until disconnect; reconnects must carry the pin. Deployment
  artifacts must remain available through the configured TTL. Disabling an
  app's revision TTL rejects direct revision pins but does not revoke a
  previously published release set; operators must retire its member
  deployment to fail those requests closed.
- **Rejected alternatives:** A hash of the current weighted rollout is not
  stable across deploys. A caller-supplied downstream deployment ID is not a
  trustworthy graph context. Automatically pinning a caller app to whichever
  release is currently active can mix an old request with a newly published
  downstream graph when the caller deployment is reused.
