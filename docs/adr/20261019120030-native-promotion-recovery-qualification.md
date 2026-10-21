# ADR-20261019120030: Native promotion control and scheduling qualification

## Status

Acceptance implementation added. Execution on the designated native KVM host is
pending. Promotion admission remains default-off.

## Context

The native promotion lane previously qualified successful HTTP/worker promotion
and automatic rollback. ADR-20261019120028 operator controls and ADR-20261019120029 concurrent health
reconciliation had portable contracts and a manual runbook, but no automated
native scenarios or required CI evidence. Extending a single invocation would
exceed its existing source-build and host budgets.

## Decision

The explicit `project-environment-promotion-only` lane runs three serialized,
independently cleaned native jobs: `promotion-core`, `promotion-controls`, and
`promotion-load`. Each retains the 210-minute test budget, 215-minute systemd
envelope, dedicated-host lock, exact source identity, service restoration and
final leakcheck. Names are derived from assigned test files. Each job qualifies
only its own partition; the aggregate requires all three commit-bound artifacts
and recomputes a complete report from their logs. No partition report enables
admission or claims the full lane passed.

Controls reuse one set of real source builds for independent admissions. They
abort an in-flight pre-cutover probe, restart APID, and require the candidate VM
and namespace to retire without late publication. Post-cutover abort regresses
candidate HTTP health and qualifies fresh baseline-only rounds, retained worker VM recovery, graph/configuration
restoration, immutable terminal receipts, and existing candidate job pins.
The blocked case interrupts real gateway probe transport for the full ten-minute
control deadline, checks the candidate remains selected and retention does not
extend, repairs transport, and issues a new recovery request. Five real baseline
deliveries fail once and retry with their original version pins during recovery,
providing fresh delivery health rather than inserted ledger evidence.

Load acceptance uses 21 accepted promotions across three isolated Scale accounts
and projects, with seven destination environments each. Nine real source builds
supply the projects; public direct OCI deployments supply destination baselines,
and a normal promotion supplies each immutable staging snapshot. App instance
limits and platform RAM admission remain unchanged. A delayed real probe must
coexist with unrelated progress. APID is killed with a live claim; the replacement
must wait for lease expiry and reclaim it, with no late checkpoint advance.
Read-only lease sampling checks global/account bounds. Every promotion must
receive a turn while the inventory is still running. An operator restoration
must complete while ordinary checks progress. Terminal graph identities and
candidate counts must survive a second APID restart without duplicate publication.

Network faults never synthesize a successful or attributed health response.
Tests do not insert health observations, runtime startup proofs, delivery outcomes
or promotion publications. Production sample intervals, timeouts and retention
are not shortened. The validator rejects absent/duplicate scenarios, incomplete
partitions, altered concurrency limits, reused sample times, short control
deadlines, early lease reclaim, failures/skips, and contradictory cleanup or
runner completion records. Reports retain the source SHA and combined log hash.

## Validation and limits

Portable checks exercise the evidence validator, derived partition selection,
runner contracts, aggregate rejection of missing groups, workload retry fault,
fixture metadata and native-tagged compilation. These checks are not a native
runtime pass. Native execution and final leakcheck remain required before any
admission decision. Migration/service-binding graphs, concurrent operator intent,
publication-boundary crash injection, baseline job failure/low-traffic variants,
and all retention exhaustion cases retain their separate qualification gates.
