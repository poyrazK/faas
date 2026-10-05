# ADR-629: Native regional runtime provenance

Date: 2026-10-05
Status: Original physical binding and recovery join implemented; native capture, pause/stop proof and activation pending.

## Context

The persistent host quota journal retains a regional permit across mutable cell
row changes. The native launch journal independently retains an allocator lease,
kernel boot, generation and PID incarnation. Without a durable join, native
recovery cannot attribute a paused, late or deleted-row guest to its original
regional allowance. A known unsupported native capture also must not fence a
serving seat merely to discover that its publication producer is unavailable.

## Decision

Write a version 2 native launch record with the complete original regional
placement in the same prepared-record transaction as the full allocator lease,
kernel boot and launch generation. Write it before native producer registration,
network, image or launch effects. Child PID/start-time authorization subsequently
updates that same record. Require exact actual instance identity and lease plan;
builder and disposable execution ownership cannot borrow a regional permit.
Retain the initial placement after later serving seats change. Live regional
publication, capture and resume join that original physical permit and complete
lease to the current locally owned generation. Bookkeeping supplies neither
current cell authority nor native pause/stop proof.

Keep version 1 records unbound and readable for independent lifecycle recovery.
Reject malformed, missing, duplicate, null or noncanonical version 2 placement
fields. An in-place restore fallback retains the complete original lease and
placement; it cannot retrofit regional ownership onto an unbound frame. Archive
that binding with the retired PID incarnation. A full replacement requires
complete native resource retirement and an explicit next placement; it cannot
downgrade regional ownership to an unbound launch. Exit/resource confirmations
and archive retries also match the complete regional binding.

During native startup recovery, join version 2 frames to the exact registered
permit and initial seat in the retained host inventory, including fences. Require the
current node, host epoch and matching kernel boot. Missing journals, conflicting
bindings and unfinished version 1 frames matching a current regional permit
keep admission closed. Matching frames stay quarantined; recovery does not
populate a serving map or release physical/serving capacity. Host permits with
no native frame remain retained rather than being declared absent. Their
source-bound reconciliation and historical native inventory are still required.

Require explicit regional capture preflight before migration lease reservation.
Repeat it inside the capture flight before source persistence or seat fencing.
The current native snapshot publication producer is unavailable, so that path
refuses without mutation. Native recovery-disabled JailerVMM also refuses new
regional launches. Portable VMM fixtures retain their separate mock surface.

Native pause and resource verification, authenticated old-seat release,
scheduler rollback seat issuance, migration row recovery, source retirement and
orphan inventory integration remain subsequent requirements. All accepted
runtime quota heads remain frozen. Preserve the entire multi-region objective,
including request admission, task attempts, signed compute authority, billing,
provisioning/release orchestration, cached ingress to all selected healthy
eligible regions and dedicated native acceptance.

## Validation

Exercise atomic regional binding before native effects, failed writes and
retained allocator ownership, strict versioned decoding, process authorization,
fallback and archive identity, exact exit/resource confirmation, and legacy
compatibility. Exercise recovery with missing/conflicting quota ownership,
fenced sources and no serving publication. Verify unsupported capture cannot
reserve a lease, fence the old seat, change monitors or call native/storage
producers. Run affected portable race suites and diff lint on macOS and Linux.
Dedicated native x86_64 Linux KVM test-metal, leakcheck and recovery remain
required before lifecycle acceptance; shared nested GCE checks do not qualify.
