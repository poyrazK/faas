# ADR-642: reconcile independently retained runtime first fences

Date: 2026-10-06
Status: Accepted

## Context

The regional cell and native host have independent irreversible first-fence
journals (ADR-631). A host can fence an attempt before the cell receives its
stop, while the cell independently retains a different fence. An authenticated
stop then proves the exact original attempt stopped but cannot match the
cell's fence. Refusing that conflict indefinitely retains physical capacity.

## Decision

Keep each original first fence. After the scheduler authenticates the native
stop's actual TLS peer against the permit's original node, it may ask the
original cell authority to reconcile the two fences. The cell signs a separate
`CapacityStop` containing the complete original permit, its unchanged `FenceID`,
and the host's unchanged first fence in optional `HostFenceID`. The host's
assertion is never edited or accepted as an already reconciled relation.

The cell's app quota journal lock serializes signing and closure against
issuance, retirement and retries. Exact original permit equality, prior
issuance and the retained cell fence are required. The first accepted relation
and envelope remain immutable, including across key rotation. A conflicting
relation refuses. Physical closure also closes the original serving seats
while retaining any earlier seat fence. Historical and collected app rows
remain closable through their original retained capacity receipt.

An omitted host fence preserves existing canonical JSON and signatures. Native
wire decoding and the native server reject a supplied host-fence relation;
only the cell signing boundary may derive one. PostgreSQL additionally checks
that the optional host fence is a canonical nonzero UUID distinct from the
cell fence. Downgrade refuses while any such retained relation exists.
Older strict readers refuse new relation claims; update receipt consumers
before enabling reconciliation in a serving regional rollout.

## Consequences and validation

This repairs one per-attempt retirement gap. It does not prove fresh complete
host inventory, grant current execution authority, reconcile an inventory
barrier, or unfreeze regional capacity heads. Missing, unauthenticated,
uncertain or mismatched native evidence still retains the full original hold.

Portable tests cover legacy encoding, signed relations, unsigned relation
refusal, actual mutually authenticated transport, wrong-node refusal, lost
responses, collected apps, parallel first acceptance and key rotation. Shared
memory/PostgreSQL contracts cover durable closure and refusal. Native resource
absence, metal lifecycle, leakcheck and recovery acceptance still require the
dedicated native Linux x86_64 KVM fleet; the authorized nested internal GCE
node provides diagnostics only.
