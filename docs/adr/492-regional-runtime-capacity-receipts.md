# ADR-492: Durable regional physical permits and serving seats

Date: 2026-10-04
Status: Implementation in progress; host enforcement, activation and native qualification pending.

## Context

A scheduler lease, terminal instance row, paused guest or failed launch RPC cannot
prove that an issued VM attempt is gone. Regional allowance reuse needs durable
bookkeeping that survives scheduler restart and ordinary app, deployment, node
and instance collection. Pausing a guest returns serving concurrency but leaves
resident memory and a possible future resume; destroying it closes both concerns.

## Decision

Retain two immutable journals, serialized with the signed quota journal by the
same app lock in MemStore and PostgreSQL. A physical permit pins the entire
original grant, permit, instance, run, deployment, node and host incarnation IDs.
Its kind records the original serving, warm, prime or app-task purpose. A serving
seat pins an eligible physical permit and a separate seat ID. No physical attempt
can be rebound, and only one unreleased seat can attach to a physical permit.
Paused warm permits can later reserve a new seat without reopening the old one.

Reservations consume finite regional serving shares before dispatch. Steady and
rollout shares are separate, and their sum cannot exceed the grant's peak share.
A rollout reservation needs an unfenced steady seat on another deployment;
rollout issuance additionally needs that steady seat already issued. Physical
permits account for all resident attempts independently of serving concurrency;
existing host RAM admission remains necessary for warm, prime and task residency.
This journal alone does not enforce the host RAM budget.

Persist physical issuance before sending a host request, and seat issuance before
sending an execution or resume request. An issued marker is bookkeeping and is
not execution authority. Retries return the original receipt including any
fence or closure; callers must inspect the full phase. No time or lease expiry
returns an issued hold. Cancellation or quota retirement closes unissued plans
immediately and fences issued plans while retaining their holds. Fresh reserve
and issue require the current exact grant, app and tenant bindings. Stops retain
historical grant identity after policy changes, supersession or collection.

Only a verified, pinned cell key can submit a domain-separated capacity-stop or
serving-seat-stop through the store receiver. Each asserts an irreversible fence
and zero remaining physical attempts or serving uses for the exact original
identity. A seat-stop returns only serving capacity. A physical stop closes all
remaining seats with its retained parent proof, preserving any earlier seat fence.
Retries across cell-key rotation preserve the first accepted envelope. SQL guards
retain identities, phase markers, fences and evidence and refuse deletion,
truncation and populated downgrades. Direct SQL runs under trusted internal
credentials; database shape checks do not verify Ed25519 signatures.

Reads expose bounded, cursor-based pages of held receipts and aggregate usage;
exact receipt lookup retains released tombstones. Zero usage is not proof that
legacy guests, unreachable hosts or late boots are stopped. These primitives
neither generate an allocation drain receipt nor activate any quota head. All
existing heads and scheduler/vmmd execution guards remain frozen.

Host incarnation storage, irreversible local fences, actual pause/teardown and
recovery enumeration must be integrated and qualified before stop assertions can
be emitted operationally. Scheduler ownership and compute transport, explicit
runtime quota consent, stable operator keys and rotation, global metering/billing
and routing from all selected regions remain required for the full feature.

## Validation

Run the shared memory/PostgreSQL contract for conservation, issuance ordering,
pause and physical stop, exact identities, retained retries and retirement races.
Exercise SQL bypass attempts, restart and collection, journal lock waits with
commit/rollback, fixed-snapshot rejection and guarded migration round trips.
Verify cell role, issuer, domain separation, strict canonical encoding and key
rotation. Native Linux x86_64 KVM failure/recovery and leak acceptance remain
required before host integration or activation is complete.
