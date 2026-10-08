# ADR-764: Retained regional runtime accounting at scheduler startup

Date: 2026-10-05
Status: Implementation in progress; native inventory reconciliation and acceptance pending.

## Context

ADR-763 retains original resource dimensions and issued physical inventory.
Startup still accounts only for live instance rows and can skip residents above
lowered limits. A configured cell signer must not hide historical cell holds in
its regional database. Instance/run tuples do not include the native host epoch.

## Decision

Before reading mutable customer rows, scheduler startup traverses every issued,
unreleased physical permit in its Store. Empty-cell inventory means the whole
Store; explicit-cell readers retain their existing scope. Pagination must advance
strictly by app and permit. Read errors, invalid pages, unknown native sources,
unknown original resources and unresolved original nodes block startup and all
new ledger admissions. Partial known charges survive these errors. No current
app, plan, instance or signer fills missing original provenance.

Physical holds have separate app/permit identities and preserve the full permit,
including host epoch. They charge original RAM plus VM overhead, guest vCPU,
conservative CPU startup peak and all issued unreleased serving seats, including
fenced seats. Reconstruction records these charges above today's limits. An
independent live row remains charged as well: matching its instance/run tuple
cannot prove a full physical identity. This conservative duplication may reduce
available capacity until native inventory reconciliation establishes the binding.
An issued permit retry does not duplicate its own charge.

At dispatch, the local reservation captures the complete original source before
issuance/RPC. Generic row release and snapshot bookkeeping cannot clear that
physical charge. Accepted native stop handling releases only an equal complete
source; an old host epoch or permit cannot release a newer reservation. Startup
row shadows without that dispatch binding remain conservative until subsequent
reconciliation or reconstruction. The barrier is not cleared by a single stop:
only a complete future reconciliation can establish inventory completeness.

Existing live rows are recovered above lowered RAM, vCPU and concurrency limits;
this private reconstruction path never authorizes new work. Customer inventory
read failures become startup errors rather than silently missing residents.

This change does not establish independent host inventory, task/run freshness,
first-fence reconciliation, capture/pause/migration ownership, directory authority,
global billing or all-selected-region routing. All regional heads remain frozen.
Native Linux x86_64 KVM acceptance, test-metal, leakcheck and recovery remain
required for the full feature.

## Validation

Test full-store inventory and composite pagination in both stores. Test orphan
and replacement-run charges, historical cell identities, over-limit recovery,
unknown provenance, read/traversal barriers, exact full-permit release and generic
release refusal. Run race suites, sqlc consistency and the pinned lint/policy gates.
