# ADR-623: Durable binding-checked environment promotion

- Status: Accepted
- Date: 2026-10-06
- Amends: ADR-590 and ADR-618

An opt-in binding-checked promotion captures approved source/target identities, prepares exact dark deployments, and returns a durable operation. A bounded APID worker leases that operation, persists blockers without changing routing, and re-observes every exact member after verification or restart. Status reads and CLI waits do not create probes or drive execution.

Preparation reserves each target deployment identity before artifact publication. Lease tokens fence staging checkpoints and the final transaction; a stale worker cannot replace a completed receipt or activate a graph. All members qualify against immutable deployment settings and exact service targets. Unknown or unsupported environment queue dispatch evidence blocks activation rather than borrowing global consumer health.

The activation transaction validates the original catalog/configuration fences before applying the captured configuration. Only those known, validated configuration changes may rebase the private revision tokens while retaining policy identities and evidence deadlines. Graph publication, desired settings, configuration, feature flags, audit, and the successful operation receipt commit together. Recheck every deadline and worker lease after lock waits. A competing target publication or stale preview fails without overwriting it.

CLI `--require-bindings` selects this contract. A blocked operation remains inspectable and retryable with exact verification commands; local wait timeouts do not cancel it. The additive API/SDK receipts include the immutable membership and qualification report. Binding-aware rollback and native Linux/KVM operator qualification remain separate work.
