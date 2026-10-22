# ADR 991: Audited durable entity quota increases

Legacy quota admission takes the stricter of persisted and configured limits.
Changing node settings cannot safely relax a durable policy, and entity caps live
in separate manifests whose writes cannot share the quota ledger's transaction.

Provide an opt-in policy preview and compare-and-swap apply workflow. Apply
requires an existing admission hold, expected policy and migration revisions,
monotonic limits, projected reservation capacity and local configuration matching
the proposal. One ledger CAS commits the policy, immutable operation receipt and
a fresh migration identity. Preserve up to 32 receipts and fail closed at that
bound; identical retries return the original receipt after uncertain writes.

Managed ledger schema 2 rejects older readers. Policy-aware mutable quota paths
require exact configured limits rather than silently changing approved limits.
Operators must upgrade and quiesce all writers; quota-disabled and direct bucket
writers remain outside this fence. Policy inspection reports approved limits even
when the inspecting node disagrees.

Managed migration raises entity caps through ownership-fenced monotonic manifest
CAS writes, rereads them, then advances the reservation checkpoint. Partial writes
leave admission held and retries never lower caps raised by a newer policy. A full
verification pass must observe matching caps, bounded usage and reservation
coverage before admission resumes. No policy downgrade, reservation release,
fleet acknowledgement or automatic production enablement is included.

See [operator workflow](../durable-entity-quota-policy.md). Native GCS/KVM,
listing completeness, clock bounds and fleet rollout acceptance remain external.
