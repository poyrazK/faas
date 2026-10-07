# ADR-649: Transport native inventory for regional reconciliation

Date: 2026-10-07
Status: Implemented for advisory reconciliation; admission activation and native acceptance pending.

## Context

The scheduler retains immutable issued capacity holds after app/instance
collection, while vmmd can freshly observe original tasks under the native
inventory barrier. These two inventories were disconnected. A successful RPC
must not turn a partial scan, changing sample, journal restart, or lost final
reply into a claim that an original resident was observed or drained.

## Decision

Add `ObserveNativeRuntimeInventory`, a server stream accessible only to an
authenticated `schedd.faas` TLS peer. Requests name the exact compute node and
a fresh canonical UUID challenge. The actual server certificate must resolve
to that node before the scheduler receives payload. A transport deadline of
at most `api.NativeRuntimeInventoryTimeout` (30 seconds) is required, so a
non-reading caller cannot hold the native barrier indefinitely.

The response contains canonical versioned JSON binding the challenge, node,
journal host epoch and fresh inventory. Kernel boot identity is now available
to this private authenticated observation receiver; it remains absent from
public stats/customer APIs. Chunks have contiguous offsets and are bounded to
64 KiB; the whole sample is bounded to 64 MiB. All bounds live in
`pkg/api/limits.go`. Oversize samples refuse rather than truncate or paginate
one native observation across separately acquired barriers. Unknown fields,
duplicate/noncanonical JSON, unknown frame fields, missing/duplicate completion,
offset gaps, extra frames, invalid SHA-256 and failed final status refuse.

The native consumer sends data while original locks, task pins and the producer
barrier remain held. Only after `WithNativeRuntimeInventory` returns successfully
following its final rechecks, and journal identity is unchanged, does vmmd send
the terminal total size and SHA-256. The client requires that frame and successful
EOF before decoding or returning any sample. Native cancellation, mutation or
post-consumer failure therefore discards already transmitted chunks. Observation
can age immediately after the final checks; it grants no current authority.

The configured scheduler retirement recovery loop also traverses all retained
issued holds, including previous signers, in bounded pages with its own cursor.
It requests one fresh observation per original node within a page. An unavailable
node advances the cursor and is retried on the next traversal. The report matches
only an unfenced, unreleased original permit, exact native lookup spelling,
original resources, host epoch, kernel boot, pinned task incarnation, compatible
measured machine and independently matching cgroup/task observations. Duplicate
ownership, missing provenance, historical epochs, PID reuse, unknown or absent
owners remain unresolved. Missing owners still require native retirement evidence.
Logs expose counts rather than native resource details. Individual matches may
coexist with unresolved native resources elsewhere on that node.

This connection changes no quota head, capacity receipt, accounting charge,
mutable instance row or serving seat. It does not merge original holds with
current rows, reconstruct resources from current plans, fence a guest, acknowledge
absence, or release capacity. Reports are advisory and are not stored as later
admission authority. The existing exact signed retirement path remains the only
way to release issued original holds.

Complete physical inventory (including escaped historical root helpers), current
run/task authority and safe row binding remain required before admission can
activate. Regional heads stay frozen. Snapshot provenance/pre-load compatibility,
capture/migration authority, shared auth and billing, independently provisioned
regional stores/replication and cached global ingress also remain in scope. Global
routing continues to target all selected healthy eligible regions by default.

## Validation

Portable tests exercise actual mTLS client/server node binding, wrong roles and
plaintext refusal, bounded requests, multiple chunks, final recheck failure,
journal changes, cancellation, strict framing/canonical decoding, original permit
and resource mismatches, independent task/cgroup mismatches and the configured
recovery-loop connection. Both successful and failed observations retain original
charges and frozen heads. These are diagnostic transport and fixture checks;
dedicated native Linux x86_64 KVM test-metal, leakcheck and regional recovery
acceptance remain required for lifecycle activation.
