# ADR-631: Authenticated regional native retirement

Date: 2026-10-05
Status: Transport and cell release implementation; lifecycle integration and native acceptance pending.

## Context

The host can retire its original native runtime and return an unsigned assertion
(ADR-630). A generic Destroy response, node claimed in JSON or terminal cell row
cannot authorize quota reuse. Host and cell first fences survive independent
retries, and the cell must sign only exact authenticated native evidence.

## Decision

Add an explicit vmmd retirement RPC addressed by the actual native lookup key,
complete historical physical permit and proposed fence. Accept only a verified
schedd.faas client certificate. Require the local compute-node identity and the
native retirement capability, with no generic Destroy fallback. Encode bounded
canonical JSON and return the original host first fence without rewriting it.

The routed scheduler client checks the RPC's actual verified TLS server leaf
against the compute-node identity resolver and exact original permit node. A
valid certificate for another registered node is insufficient. Refuse local
plaintext transport, missing resolvers, alternate lifecycle clients, malformed
assertions, changed original pins or changed actual lookup keys. Production
schedd wires its existing node verifier into this response binding.

The cell retirement owner first validates its configured cell signing key and
trust role without signing evidence. Under the existing app quota journal lock,
coalesce the first cell fence and validate the full original permit. Unissued
reservations cancel locally. Issued attempts remain held while vmmd is contacted
outside the database transaction. RPC errors, unavailable nodes, native
uncertainty and mismatched host/cell first fences retain that hold. Never
silently substitute fence IDs; independent existing fence conflicts still need
an explicit durable evidence relation and reconciliation.

After authenticated native evidence matches the cell's exact retained permit
and fence, recheck that receipt and sign/close under the same app journal lock
used by issuance and quota retirement. Require the signer pinned as the original
cell authority, independent of the current app/deployment row or quota head.
Retain the first accepted signed envelope across retries, restart and key
rotation. Physical release closes remaining serving seats through the existing
ledger rules, preserving earlier seat fences.

This adds an explicit owner operation. Automatic lifecycle/recovery dispatch,
stable operator key provisioning and rotation, historical/orphan inventory,
native pause/capture, migration rollback integration and dedicated native Linux
x86_64 KVM test-metal/leakcheck/recovery acceptance remain required. All quota
heads remain frozen. The full multi-region objective, including routing to all
selected healthy eligible regions, remains open.

## Validation

Exercise canonical bounds, real mTLS peer roles and cross-node response binding,
missing native capabilities, compact actual keys, conflicting acknowledgements,
lost replies and first-fence retry. Run shared memory/PostgreSQL contracts for
concurrent fence coalescence, signature trust, exact historical pins, cancelled
reservations, preserved seat fences and original evidence across key rotation.
Portable transport tests do not establish native lifecycle acceptance.
