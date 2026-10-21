# ADR-20261019120032: Native qualification of promotion admission

## Status

Acceptance implementation added. Designated native KVM execution is pending.
Production admission remains default-off.

## Context

ADR-20261019120031 bounds whole promotion operations and preserves durable waiting intent.
The ADR-20261019120030 native cohort of 21 operations never reaches the new admission
limits. Portable storage contracts cannot establish VM isolation, owner restart,
or restoration behavior under native saturation.

## Decision

Add a fourth serialized partition, `promotion-admission`, to the explicit
`project-environment-promotion-only` lane. Its tests are derived from its assigned
source file, included in the runner verdict, and mandatory in the aggregate
commit-bound qualification report. An older three-part report is incomplete.

Five isolated Scale accounts distinguish all four admission boundaries. A starts
eight operations and queues a ninth destination, B and C start six each, and two
synchronized HTTP requests from D/E contend for the final fleet slot. Exactly
one starts. A further D request must wait for fleet capacity despite available
account capacity. Four accounts then occupy 126 or 127 waiting slots and E occupies two
or one respectively (with D adjusted), reaching exactly 128. A subsequent A request proves
account rejection; E proves fleet rejection independently. Neither rejected key
may persist an operation. All 128 accepted waiters retain their deadlines across
APID restart and have no candidate deployment or associated VM resources.

Cancel most waiters, retain two ordered A requests, change D's destination
configuration, and release exactly one A/fleet slot. Interrupt APID after the
head's validated dispatch commits and before preparation creates a candidate.
Restart and replay the same key. The second A request must still wait; the head
gets one full fresh health window, publishes once, and succeeds using actual
minute-spaced gateway evidence. Changed D intent conflicts without overwriting
its graph or configuration. Waiting cancellations finish without preparation.

For recovery, first request post-cutover restoration at the 24-operation bound.
Interrupt actual probe transport for the complete ten-minute control deadline;
unknown evidence must preserve the candidate selection. Once that decision is
blocked, replenish 24 ordinary operations and request a new recovery. Recovery
must bypass admission, giving 25 total operations/nine in its account while
ordinary work continues receiving turns and new work remains queued. Only real
baseline probe responses may qualify restoration. Preserve the original baseline
retention and recover after APID restart. Ordinary admission still obeys 24/eight;
this one urgent recovery is an explicit exception, not a VM concurrency grant.

Finally stop APID with an accepted waiter for the full fifteen-minute wait
interval. On restart it must fail before dispatch, retain its original timestamps,
and leave the destination unchanged. The observer checks active and pending bounds
throughout. The fixture also checks source build counts, candidate uniqueness,
artifact identity, publication identity and normal final host cleanup.

All mutations use normal HTTP intent, real owners and process lifecycle faults.
SQL only reads evidence; no inserted health receipts, runtime acknowledgements,
publication rows or shortened production timeouts are allowed. A proxy permits
selected revision pins through to the real gateway and returns unattributed 503s
for the others. It never synthesizes successful or authenticated probe results.

The fixture uses fifteen ordinary source builds and reuses their artifacts across
phases. Its conservative resident bound is 63 instances at 512 MB plus overhead
(32,760 MB), below 47,600 MB; no app needs more than Scale's 20 instances. Source
builds remain in builder microVMs with unchanged per-build budgets. This partition
has a 330-minute test budget, 335-minute systemd envelope and 360-minute CI job.
Other promotion partitions retain 210/215/240 minutes. The designated host lock,
source identity, service restoration and final leakcheck remain mandatory.

The validator rejects missing/duplicate waiters, changed caps, incomplete groups,
shortened wait or control deadlines, stale healthy windows, unverified recovery,
wrong source identity, and contradictory runner/cleanup outcomes. The output
retains the tested source SHA and combined log hash and never enables admission.

## Validation and limits

Portable checks compile every native-tagged function, run fixture capacity and
ownership metadata, and exercise scenario/partition/wrapper/aggregate validation.
They are not native execution evidence. Run all four partitions on the designated
Linux amd64 root/KVM host and retain their exact-source reports before considering
production enablement. This new partition covers HTTP promotion queues. Queued
worker/migration graphs, migration recovery and publication transaction crash
injection retain their separate qualification requirements.
