# ADR-653: Bind app-task runtimes to the original lease

Date: 2026-10-07
Status: Implemented locally; native lifecycle acceptance pending.

## Context

The task coordinator retains a claim token for its running and terminal CAS,
but preparation previously used the durable task ID as the VM ID and omitted
the token. The host checked for a live task lease without comparing it with
the lease that created the VM. A recovered or replaced claim could therefore
reuse a native lookup key or authorize a surviving earlier VM. The app-task
resolver also bypassed regional launch issuance and used generic destruction.

## Decision

Pass the captured original claim through command-free resolution, the restore
RPC and the immutable host instance. Derive the native instance UUID from the
canonical task UUID and claim UUID using the versioned
`gregale/app-task-instance/v1` namespace. A retry of one claim has one lookup
key; each replacement claim has a distinct key. The guest command keeps the
durable task ID, and the existing MarkAppTaskRunning CAS still precedes it.

Both scheduler preparation and the production host authorizer require the
exact original unexpired, uncancelled claim and the appropriate task status.
The host compares the retained original claim again at command admission.
Regional preparation uses the same receipt-before-RPC launch boundary as other
physical purposes. Its permit run ID is the claim token, and its immutable
launch source additionally retains the original task ID. Postgres stores that
nullable UUID without a foreign key to collectible task rows. Source retries,
inventory and retirement preserve it. Historical sources remain readable;
missing task provenance cannot be backfilled or treated as a qualified match.

Reserve the actual startup CPU peak until task teardown; tasks do not use the
ordinary serving readiness callback to expire that reservation. Regional
cleanup invokes the captured original-source failure/retirement owner. Only
accepted native stop evidence releases the exact reservation. Lost restore or
stop replies keep the original hold for recovery and never fall back to generic
Destroy. Independent task cleanup remains pinned to its distinct attempt key.

## Consequences and validation

Old app-task RPC callers without the captured claim fail the production host
authorization check. Existing native instances without retained task metadata
cannot borrow today's row or lease. This change supplies original task/run
provenance; it does not establish complete physical inventory, merge startup
row shadows or activate regional quota heads.

Contracts cover distinct replacement identities, immutable source round trips
through both stores, coordinator propagation, restore/execution binding,
replacement-lease refusal and cleanup uncertainty. Dedicated native Linux
x86_64 KVM metal, leakcheck and fleet recovery acceptance remains required for
the complete multi-region lifecycle. Nested GCE diagnostics do not qualify it.
