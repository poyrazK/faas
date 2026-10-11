# ADR 992: Permanent durable entity retirement and verified capacity credit

Full-cap entity reservations survive indefinitely. Removing a quota entry alone
would permit a stale owner or old request to recreate state without retaining
the original admission charge. Cleanup also cannot erase committed receipts
without changing the request recovery contract.

Retire an exact identity through manifest CAS. Preserve its version and receipt
graph in a new snapshot with empty application state, advance epoch/generation,
and retain a permanent schema-7 tombstone. Require no live owner, scheduled alarm
or outgoing work. A separate schema-3 quota CAS tracks a permanent operation
receipt and rejects admission to the identity. Until tracking succeeds, its full
reservation remains charged. Older writers reject the new schemas.

Use resumable bounded receipt-graph proof, obsolete-object cleanup and a full
verification pass, with a lease/invocation wait before deleting candidates. Quota
credit requires verified entity completion. Remove the active reservation and
charge authenticated recovery bytes in the same CAS as the completion receipt.
An uncertain acknowledgement cannot duplicate credit. Migration skips retired
entities; policy/migration replacement cannot race pending tracked retirement.

Keep at most 256 permanent retirement receipts per scope, failing closed at the
bound. No identity reuse, tombstone TTL, cancellation after start, receipt erasure,
provider-history erasure or automatic production enablement is provided. External
fleet, native storage and clock acceptance remain required.

See [operator workflow](../durable-entity-retirement.md).
