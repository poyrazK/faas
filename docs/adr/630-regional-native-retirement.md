# ADR-630: Regional native retirement

Date: 2026-10-05
Status: Host retirement implemented; transport, cell signing and native acceptance pending.

## Context

A regional permit must remain counted until its original physical runtime is
retired. A closed operation barrier and a successful generic Destroy are not
native resource evidence. Legacy native mode also discovers its inability to
retain a regional launch after quota registration and a prepared-network claim.

## Decision

Preflight native regional launch capabilities and complete native inventory
recovery before registering a new host permit, reading snapshot prefetch data,
allocating a lease or claiming a prepared network. Require both the explicit
regional prepared-record producer and the preflight capability. Portable VMM
fixtures retain their separate simulation surface.

Add a host retirement operation addressed by the original instance lookup key
and complete immutable capacity permit. Validate current node and host epoch,
and require registered retained ownership. Persist the first irreversible host
permit fence after acquiring exclusive native daemon ownership and recovery,
before joining boot or resume. Use the existing teardown flight,
then resolve the native frame after late work has joined. Refuse missing,
unbound, conflicting or different-boot frames. Cleanup uses that frame's full
lease and joins any surviving Manager identity by native generation and lease.
No mutable cell instance row or current execution admission authorizes cleanup.

After cleanup, drain the fenced host operation barrier. Under the physical
journal lock, require the original regional binding, launch revocation, native
exit and resource acknowledgements, current kernel boot, a fresh complete
kernel process scan and fresh native resource checks. Recheck healthy durable
host fencing before returning an unsigned CapacityStop assertion containing
the retained first fence. Failures return no assertion and retain quota
ownership; retries repeat native verification rather than trusting a prior RPC.

This operation does not sign, release ledger capacity or release a paused
guest's serving seat. Recovered unfinished producers remain quarantined under
the existing native recovery rules. Missing frames, historical boots and
archived/orphan reconciliation need further source-bound inventory work.
Authenticated transport, cell stop signing, scheduler release integration,
native pause/capture and dedicated KVM acceptance remain required. All quota
heads remain frozen, and the entire multi-region objective remains open.

## Validation

Exercise refusal before host registration and prepared-network mutation;
original prepared, live, paused and deleted-row ownership; retained fences and
idempotent retry; late boots; process incarnation mismatch; missing native
frames; uncertain process exit and resource cleanup; and resource or process
reappearance after prior acknowledgements. Portable fixtures establish
ordering and identity behavior only. Dedicated native x86_64 Linux KVM
test-metal, leakcheck and recovery remain lifecycle acceptance requirements.
