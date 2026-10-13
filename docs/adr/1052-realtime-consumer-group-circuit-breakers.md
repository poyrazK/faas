# ADR 1052: Automatic consumer-group circuit breakers

Status: accepted for preview implementation; migration/runtime validation pending.

## Decision

Add an opt-in policy per consumer group: enabled, consecutive failure threshold
1..100 and cooldown 1..3600 seconds. Defaults disabled, threshold 5, cooldown 30.
Account GET group `circuit` needs ownership, read scope and MFA; PUT needs admin
scope and MFA. Require all three fields. Identical writes preserve decisions;
changed policy resets closed and clears failures/probe state without touching
pause, drain or leases. No scoped policy route or new worker grant.

Count failure settlements (retry or dead) and expired leases in database observation
order, including batch item order. Complete resets the streak while closed.
Normal delivery-deadline expiry and invalid deadlines are excluded; failed probes
reopen even for deadline expiration. This protects the whole group's processing
path, not a named external dependency. Remaining already-leased failures may
still exhaust attempts while open; opening cannot undo running work.

The threshold opens admission until database cooldown time. Claims lazily advance
an eligible open circuit to half_open. Allow only one probe lease, identified by
sequence and fencing token under the group lock. Empty/blocked work leaves the
circuit waiting for an eligible probe; rate, priority, ordering, capacity, deadline
and lease limits continue to apply. Probe renewal extends its ordinary lease and
keeps other claims blocked. Only completion of that exact probe closes/reset the
breaker. Probe failure or lease expiry reopens for another cooldown. Existing
non-probe settlements do not alter open/half_open decisions. After recovery, all
settlements again participate in closed-state observation order.

Existing leases always retain renewal/settlement paths. Manual pause/drain takes
precedence over cooldown advancement and new probes. Lease expiry maintenance can
still observe failures while paused. Missing probe snapshots (for example after
explicit resync) reopen lazily on claims rather than blocking forever. Reads do
not advance state and may show an elapsed open deadline. No background worker.

Single claims return 429 `realtime_group_circuit_open` with Retry-After and the
existing problem retry hint. Batches return required `circuit_open`, potentially
with one probe claim, and the shared `retry_after_milliseconds` field. Long polls
return promptly on blocked circuits. Node worker helpers handle the code and batch
hint as bounded waits, keeping active processing, renewal and heartbeats running.
Generated SDKs expose the account policy/status operations. Inspector shows policy,
state, reason, streak, probe and next eligibility. Hints are observations, not
reservations or proof of dependency recovery. Active probes use a one-second polling
hint rather than delaying recovery observation until lease expiry.

Observe failed state transitions with an AFTER UPDATE message trigger and
successful deletion with the sqlc completion query's observer CTE. This includes
single/batch complete and retry, dead attempts and lazy lease expiry; administrative
replay/deletion adds no successful processing observation. Group serialization
coordinates worker replicas, and transaction rollback includes counters/decisions.
A group AFTER UPDATE trigger snapshots signed webhook recipients and enqueues
`realtime.consumer_group.circuit_opened`, `circuit_half_open` or `circuit_closed`
only on state changes. Payload contains IDs, policy, state, reason, failures,
next_probe_at and occurred_at; no payload, lease/credential token or hash.
Configuration-induced closure emits `configured`; identical updates do not emit.
Existing signed dispatcher retries apply; no subscriber backfill.

Migration 20261012102700517 adds bounded policy/state and transition functions.
Deploy it before the new binary; upgrade all apid replicas before enabling any
breaker. Older binaries ignore admission gating and omit completion observation.
Down drops policy/state but retains queued webhook vocabulary; downgrade binaries
with schema. Runtime tests, migration application and UI rendering remain deferred.
Builds and static checks do not establish Postgres execution evidence.
