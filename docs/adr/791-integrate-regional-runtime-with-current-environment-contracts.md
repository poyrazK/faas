# ADR-791: Integrate regional runtime with current environment contracts

Date: 2026-10-08
Status: Implementation in progress; combined validation and native acceptance pending.

## Context

The regional branch and upstream evolved independently after their shared
2026-10-05 baseline. Upstream added deployment-owned environment accounting,
configuration and secret snapshots, retained layer custody, guest timer identity,
image command readiness and protected workflow/operation ledgers. Regional
physical ownership, original-source retirement and issued resume seats must
remain enforced when integrating these changes.

## Decision

Preserve upstream's environment and shared application concurrency ceilings,
while retaining regional physical holds and private observed-residency recovery.
A public recovery flag cannot grant new capacity. Normalize application identity
consistently in the new environment indexes. Resolve task secrets and runtime
values from their retained deployment without losing the original active claim.
Regional deployment receipts, promotion captures and clone inputs remain typed,
separate creation origins; combining those origins is rejected.

Keep upstream's warm-resume protobuf tags for image readiness. The unshipped
regional payload uses request field 3 and response field 4. A checked regional
resume must acknowledge both the exact original issued seat and fresh image
readiness/monitoring when its retained deployment requires that contract. The
native resume uses its stored boot contract and performs the fresh challenge
before serving. Missing acknowledgement retains the original hold; no generic
Destroy can replace original-source regional retirement.

Every new unowned clone/cgroup maintenance sweep participates in vmmd's native
producer barrier, including sweeps in native recovery mode. Retained cleanup
continues through its own original ownership checks. Snapshot GC retains the
actual persisted regional layer key and upstream's deletion custody. Snapshot
backing identity retains both timer configuration and capture-specific inputs.
Ordinary failed wakes use a bounded detached context for their terminal write;
regional failures keep their authenticated original-stop protocol.

Regional ADRs 585–662 are renumbered to 713–790 before merging. Only citations
introduced by the branch are changed; append-only migration history is retained.
The reference mapping is in REGIONAL_RENUMBERING_20261008.md. Generated SQL,
protobuf and SDK output is refreshed from the combined canonical sources.

Register the regional schema at the environment-clone boundary. Global routing
intent keeps explicit isolation blockers; account grants and consents stay with
their original identity, physical/runtime/release receipts reset as operational
history, and cell/issuer policy remains platform configuration. Register retained
log archive metadata and regional columns without copying original authority.

Preserve layer deletion custody during regional cleanup: a referenced regional
rootfs remains cold bootable until its deployment is explicitly cleared. Pin
unissued runtime refusal and persisted job identity in the shared MemStore and
PostgreSQL conformance suite. Exercise region/global-app JSON output and map
quota/consent/delivery leaves to their global-app dispatcher. Refuse incomplete
legacy job-store bootstrap dependencies before attempting any database access.

## Validation and remaining scope

Validate the combined scheduler, native VM boundary, state and API/SDK contracts,
including environment/shared limits, refused recovery flags, original regional
failure holds, simultaneous seat/image-readiness acknowledgements and maintenance
exclusion during inventory. Verify migrations and regenerated schema against a
disposable database. Portable and nested GCE tests are diagnostic evidence.

The full multi-region objective remains active: complete physical inventory,
original source/run/task authority, safe live hold/row reconciliation, first-fence
native recovery, capture/pause/migration and latency, global authorization and
billing, independent regional stores/provisioning/replication, cached ingress
serving all selected healthy eligible regions, and dedicated native x86_64 KVM
metal/leakcheck/fleet recovery acceptance. This integration does not activate
regional admission or establish positive serving readiness.
