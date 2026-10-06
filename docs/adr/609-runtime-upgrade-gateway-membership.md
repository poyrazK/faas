# ADR-609: Reviewed gateway membership for private runtime verification

Status: accepted · 2026-10-06

## Context

ADR-608 freezes a caller-supplied process list. That list can omit an ingress
participant, and a receipt does not establish that its process is still alive.
An expired or absent participant must not disappear from the expected set.

## Decision

Private platform-administrative apid controls own a bounded desired gateway
roster: 1–64 stable slot UUIDs paired with exact process session UUIDs. Slots
are explicitly provisioned ingress identities, independent of compute-node
inventory. A session is newly generated on each internal gateway process start.
The trusted reviewer must enumerate every ingress participant in the private
acceptance topology. This declaration is authoritative for this private verifier;
heartbeat discovery, caller-selected subsets and compute-node liveness are not.

`GatewayRosterControls.Status` reads the current revision.
`GatewayRosterControls.Review` requires its revision as a compare-and-swap
precondition; an empty precondition is allowed only for the first review.
Canonicalization sorts slots and rejects duplicates, duplicate processes, nil
UUIDs and empty/oversized rosters. An exact review with the current precondition
returns the unchanged revision. A changed review appends immutable history,
atomically publishes a new head and resets bounded operational heartbeats.
Concurrent reviews cannot both replace the same revision. These controls have
no customer route, CLI command, automatic enrollment or worker admission.

Gatewayd owns only heartbeat facts. The existing default-off
`FAAS_RUNTIME_UPGRADE_ROUTING_CONFIRMATION=1` additionally requires a canonical
stable `FAAS_RUNTIME_UPGRADE_GATEWAY_SLOT_ID`. Startup logs its slot and fresh
session for private review. Every 15 seconds, with a 10-second database timeout,
it renews a one-minute database-clock heartbeat for that exact reviewed pair.
A process cannot enroll itself, replace another session, or resurrect an old
session after a reviewed replacement. Unreviewed processes continue retrying
without publishing liveness. Heartbeats run independently of routing repair;
liveness and installed-weight receipts are separate required facts.

New verification enrollment requires the entire current roster's process set
and freezes its revision alongside ADR-608's participants and deadline. A
retry returns the original journal; it never rebinds to the current revision.
Fresh verification requires this exact current roster, a nonfuture live heartbeat
for every slot, the existing installed-weight receipts and scoped health.
Evidence validity is capped by the earliest heartbeat expiry. Missing, expired
or subsecond heartbeat validity stays pending without shrinking the desired set.
A changed revision blocks a pending frozen verification, including a roster
shrink, expansion or restarted process. New explicit work is required; no worker
silently adopts another revision. Historical verified journals remain immutable
while fresh observations can report changed membership.

Advance locks operation, journal, then the roster head for shared access before
existing environment/app/workload/qualification fences. Review takes the head
exclusively; heartbeat takes it shared. Neither review nor heartbeat takes app
locks. The head remains fenced through the repeatable-read observation and
lease/evidence-expiry checkpoint, preventing a review between observation and
publication. A serialization conflict is retryable and publishes no success.
Heartbeat expiry is evaluated after all evidence reads and bounded again by the
existing database-clock evidence checkpoint; lock waits do not extend it.

Append a forward-only migration. Immutable roster revisions and a singleton
current head are platform configuration; heartbeat rows are operational.
Verification's nullable roster reference preserves existing history. Pre-609
pending journals cannot acquire membership authority implicitly: they block as
`gateway_membership_unreviewed`. New inserts require the full current roster;
the database guard freezes its revision. Existing terminal history remains intact.
Memory storage mirrors these contracts and returns copies of mutable slices.

## Consequences

This closes caller-selected membership for the private verification path. The
reviewer's declaration still requires evidence from actual ingress configuration:
this slice does not provision ingress, reject production traffic from gateways
outside the roster, or prove that the declaration covers the public fleet.
Whole-fleet ingress coverage and connection-drain receipts remain prerequisites
for retirement. Neither liveness, routing installation nor historical verification
authorizes predecessor cleanup, artifact deletion, rollback or VM actions.

Public `execution_available` remains false. No backend PR, public Apply,
production daemon launch or deployment is part of this slice. Dedicated Linux
amd64 KVM acceptance, `test-metal` and final `leakcheck` remain required before
customer enablement. Local fixtures exercise metadata and SQL, not VM behavior.

## Validation

Memory/PostgreSQL contracts cover roster review, canonical identities, bounds,
concurrent compare-and-swap, immutable/copy-safe intent, unreviewed processes,
restart rejection, complete membership enrollment and changed-roster blocking.
PostgreSQL additionally exercises review fencing while a worker waits on an app
lock, heartbeat expiry during that wait, fresh-worker recovery, historical success
invalidation and fail-closed legacy enrollment. Pure evidence contracts cover
missing, expired, future, wrong-revision and restarted heartbeat observations and
conservative validity. Gateway heartbeat contracts cover immediate startup,
identity preservation, bounded database calls and in-flight shutdown cancellation.
