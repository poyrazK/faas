# ADR-20261019120010: Progressive project canary policy

Status: Policy implemented; ADR-20261019120013 connects persistence and routing behind a default-off qualification gate. Native qualification remains pending.

## Context

Atomic project releases currently move the whole production graph at once. Post-cutover
HTTP probes and worker delivery evidence detect regressions, but cannot limit the
initial exposure. Progressive canaries should select one immutable graph and configuration
for an entire request or queued job, rather than independently weight each application.

The scheduler currently models one worker target per environment. Routing candidates
before supporting both worker targets and durable invocation pins would strand deliveries
or move retries between releases. No progressive CLI flag or admission path is enabled
by this decision.

## Decision

Implement a pure internal policy checkpoint and decision engine first. Select new work
at 5%, then 25%, then 100%, using a deterministic hash of the rollout, immutable graph
IDs and a trusted request identity or durable job replay root. Existing release pins
remain authoritative; selection must never be rerun to choose a retry's deployment.

Each stage requires two consecutive healthy observations separated by at least one
minute, within a fresh ten-minute deadline. The first observation must cover a full
interval after the stage starts. HTTP and worker verdicts are independent: low worker
traffic, incomplete evidence, and an unhealthy baseline block promotion. Unknown
evidence resets confirmation. A generation and prior observation timestamp reject
stale controller rounds, including after a stage transition or process restart.
Observation windows must start at the current stage's checkpoint and cover a full
interval; overlapping, future, or stale windows cannot count. Healthy-baseline
evidence must be less than one verification interval old when making a decision.

Policy decisions describe required effects without performing them. The graph owner
must commit the new checkpoint and its effects together under graph/config compare-and-swap
guards. At 5% and 25%, the baseline graph and global configuration head stay active.
At 100%, activate the existing candidate graph and its configuration together, then
verify full traffic separately. Two regressed rounds stop the rollout. Before full
activation, stop candidate admission; afterward, restore the baseline graph/config.
At deadline, only fresh healthy-baseline evidence permits restoration. Otherwise
stop admission and require an operator rather than restore a known or potentially
failing baseline. Expired or terminal checkpoints cannot admit new work.

## Remaining integration and qualification

The original policy-only change did not persist or route production traffic.
ADR-20261019120011 adds durable job pins, ADR-20261019120012 adds retained worker recovery, and
[ADR-20261019120013](20261019120013-staged-project-canary-publication.md) now connects staged graph/checkpoint
persistence, cohort routing, configuration promotion and the opt-in deploy contract.
Production admission remains disabled until native qualification.

Pinned work on a stopped candidate must retain its graph and either drain within a
defined bound or be quarantined explicitly; it must not silently execute the baseline.
Production admission requires native x86_64 KVM qualification of mixed graph routing,
worker restart, retries/replays, controller recovery, and graph/config rollback.

## Consequences

Policy tests can verify promotion, restart, cohort stability and fail-closed decisions
without VM lifecycle changes. These tests do not establish durable store transactions,
production routing, worker capacity, or native acceptance. ADR-20261019120013 adds an explicit opt-in contract; ordinary deploy behavior remains unchanged.
