# ADR-716: Opt-in keyed event delivery ordering

- **Status:** accepted
- **Date:** 2026-10-08
- **Decision:** Let an application event subscription opt into acceptance-ordered routing and serialized invocation execution for each app, work policy, and canonical scalar work key.
- **Why:** Independent recipient recovery protects consumers from unrelated failures, but some consumers need older state changes admitted before newer changes for the same entity. Global FIFO would reduce concurrency and couple unrelated keys.
- **Consequences:** `ordered: true` is captured with each recipient snapshot. Same-lane publishes serialize in the publishing transaction and receive an immutable routing order. Routing retries hold only that lane; different keys continue independently. Ordered policies cannot coalesce, expire, or execute in parallel.
- **Rejected alternatives:** A global event queue would serialize unrelated work. Sorting only by outbox ID does not define order for simultaneous transactions whose commits complete in a different order. Rewriting existing recipient snapshots on deployment would change acceptance-time configuration.

## Contract

An ordered event trigger requires `work_policy`, `work_key`, and the default or
explicit `work_action: invoke`. The effective policy must use
`max_running_per_key: 1`, `pending_updates: all`, zero debounce, and zero
expiry. State rejects incompatible bindings and later policy updates while an
ordered binding uses that policy. To change the policy shape, first disable
ordering on the binding.

The ordering lane is the app, policy name, and canonical scalar resolved from
the event's captured key selector. Strings, booleans, and numbers retain the
same typed-key rules used by application work policies; numerically equivalent
JSON values share a lane. For each lane, the database acquires a transaction
advisory lock before assigning the routing order. The lock is held through
commit, so simultaneous publishes receive the order in which their publish
transactions serialize. The order token is captured in the immutable recipient
snapshot.

Routing admits an ordered recipient only after earlier matching routing has
reached `enqueued`, `filtered`, or terminal `failed`. Retryable routing failures
therefore hold later recipients for the same lane. After admission, the
required work policy keeps invocations serialized and retains all pending work
in order. A terminal failure releases later work; retrying that old event by
hand cannot undo a younger invocation that already advanced, so explicit
replay may change the observed order. No order is promised between different
keys or subscriptions outside a shared lane.

## Validation

Qualification covers configuration validation and policy-update rejection,
immutable ordered snapshots, per-key independent routing, a same-key retry
holding younger delivery, release after retry admission, and the unchanged
independent-recipient recovery behavior.
