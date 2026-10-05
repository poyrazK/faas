# ADR-627: Regional warm resume promotion and recovery

Date: 2026-10-05
Status: Implemented behind frozen regional admission; portable validation passed.

## Context

ADR-624 provides explicit seat-bound resume transport, and ADR-626 retains one
durable intent across uncertain replies and competing schedulers. The independent
warm-pool path destroys failed candidates and counts concurrency only after a
successful reply. Those actions cannot conserve an issued regional serving hold.
A scheduler restart must also account for a WARM row whose resume already escaped.

## Decision

Connect regional warm promotion to the original issued warm physical permit.
Require the selected app, account, deployment, registered active node and region,
canonical instance/run identity, actual host epoch and current signed grant to
match. Read and validate the physical receipt; never create a replacement guest
or infer physical identity from the mutable WARM state. Reserve local serving
concurrency before intent issuance or transport, and dispatch the returned intent.
Retries on that physical permit remain possible at the local concurrency cap.

Protocol claims remain canonical UUIDs. Parse compact row/runtime UUIDs when
comparing them with claims, while retaining the actual row and resident lookup
keys. Never rewrite a retained permit to match an encoding. The host journal's
one-launch rule remains keyed by the immutable physical permit; another encoding
cannot mint another attempt. Resume acknowledgements still echo the exact request
runtime key and serialized placement.

Require explicit regional resume capability and its exact acknowledgement.
Repeat admission and issued-placement checks at execution/publication boundaries.
An uncertain issuance, RPC, acknowledgement or runtime publication retains local
concurrency, physical capacity and serving capacity. Return a retryable capacity
error without trying another candidate or cold fallback. Legacy candidate cleanup
also checks authority before destroying or releasing an uncertain resident.

Recover a peer publication only if its RUNNING app/deployment/node/run/network/
guest identity matches the original WARM resident and the same placement remains
issued. Do not emit another committed transition for a peer's successful CAS.

At scheduler startup, find issued, unreleased resume seats by the full regional
app/instance/node/run tuple. Fenced seats and retired grants remain held and count
without authorizing execution. Two held host incarnations, unreadable resume/head lookup or
malformed identity conservatively charge the resident and return a startup error
for inventory reconciliation. Reconstruct resident RAM first and then concurrency,
including existing holds above a lowered admission cap. Snapshot bookkeeping alone
cannot drop a pending regional resume's concurrency reservation.

Propagate incomplete regional recovery as a typed startup error. The production
schedd startup must return that error before serving admission RPCs; logging it
and continuing would discard the recovery barrier. Preserve ordinary independent
startup error handling.

All accepted regional heads remain frozen. Bookkeeping helpers and simulated
transport/publication tests do not activate admission, prove native pause/stop,
or account for deleted/legacy guests without native inventory. Migration rollback
still needs lease-scoped durable completion, original source physical identity and
native pause/old-seat release verification. Other activation prerequisites from
ADR-626 remain outstanding. Dedicated native x86_64 Linux KVM test-metal,
leakcheck and recovery evidence are required by CLAUDE.md.
This recovery covers surviving WARM rows and their resume journal. Complete native
inventory and other startup reconstruction paths remain activation prerequisites.

## Validation

Exercise shared MemStore/PostgreSQL held-lookup contracts, fences and retirement,
authenticated release, ambiguity and cancellation. Test scheduler competing
proposals, lost issuance and RPC replies, retries at the cap, physical bindings,
missing capability, peer publication recovery, frozen production gates and restart
accounting. Run portable race suites, PostgreSQL contracts, SQLC generation and
repository policy checks; retain source-bound evidence and distinguish Linux unit
validation on the shared nested internal node from native lifecycle acceptance.
Persistent bridge unit fixtures use private socket paths so non-root tests cannot
remove or depend on live host stream sockets. Production retains its existing
socket path. Keep the first UUID-binding failure and Linux shared-path permission
failures in the evidence; corrected-source checks must pass before committing.

Focused regional race checks and full scheduler, VM manager, VM protocol and
schedd startup suites passed on macOS. The PostgreSQL resume contract passed all
19 cases with no skips. SQLC regeneration was byte-identical, diff lint reported
zero issues, and repository policy gates passed; migration-version hygiene was
skipped outside PR context. On the internal Linux node, runtimequota, host journal,
state, scheduler and VM manager suites passed; corrected private bridge fixtures
and startup recovery then passed full scheduler, VM protocol and schedd suites.
Source manifests bind each run, including both failed earlier attempts. These
checks provide no dedicated native KVM lifecycle, leakcheck or recovery acceptance.
