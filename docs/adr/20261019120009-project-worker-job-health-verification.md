# ADR-20261019120009: Real job health for atomic worker releases

## Status

Implemented behind explicit `--worker-health`, within ADR-20261019120008's disabled-by-default
worker admission gate. Native execution and production qualification remain pending.

## Context

A worker can pass startup and configuration checks while returning errors for every
job. HTTP probes cannot establish queue health. Job effects cannot be undone by
restoring a release, and low traffic cannot prove a candidate healthy.

## Decision

`gregale deploy --project --atomic --verify --workers --worker-health` binds a
required worker-health policy into the scan token, durable receipt and local recovery
journal. Older worker releases retain their startup/probe policy. An older server
that omits worker health is rejected before apply. No feature gate is enabled here.

Gateway-internal begins an operational delivery observation after validating the
claimed invocation and selected running worker, before forwarding to the guest.
The observation is uniquely keyed by invocation, replay generation and attempt.
A duplicate or stale claim cannot create another observation or execute again.
The final gateway classification records success, retry or immediate dead letter,
including HTTP errors, malformed responses, explicit batch item failures and
transport failures after selecting the worker. SQL predicates check the live
lease and identity on both writes. A failed outcome write leaves an unfinished
observation: it cannot become positive evidence. These measurements do not replace
schedd's durable acknowledgement, retry or dead-letter state machine.

Verification reads deployment- and binding-attributed evidence inside its existing
guarded transaction. The baseline window is the five minutes immediately before
cutover. Candidate windows are disjoint, from cutover/the previous checkpoint to
the current checkpoint. Every window requires at least five attempts, all finished;
the baseline must have no more than 20% failures. A missing baseline, low traffic
or unfinished attempt reports `insufficient_evidence`. No synthetic queue job is
created by verification. The baseline must already have real delivery evidence
from this gateway version before enabling the policy.

A window regresses when any of these conditions holds:

- At least three failures and failure rate at least 20 percentage points above baseline.
- At least three retry classifications and retry rate at least 20 points above baseline.
- Mean delivery duration or mean queue wait exceeds twice baseline and reaches one second.
- Due pending backlog grows by at least ten jobs from its cutover snapshot, and
  its oldest age grows by at least thirty seconds, with otherwise sufficient job evidence.

Failed delivery classifications include attempts that subsequently exhaust the
consumer budget; the final consumer disposition remains schedd-owned. Delivery
counts include retries and replays; a later success does not erase earlier failures.
Queue wait includes retry delay and keyed scheduling. Means are intentionally used
for this first policy, not an unsupported claim of p95 coverage. Backlog can grow
from increased producer load; these conservative initial thresholds are operational
policy, not proof of a code defect. All policy constants live in `pkg/api/limits.go`.

Healthy HTTP probes and healthy worker windows must agree for success. Two separated,
consecutive regressed windows trigger the existing guarded graph/configuration/worker
rollback. Unknown worker evidence resets health counts. The existing ten-minute
verification deadline restores production without a false success; a known failing
baseline blocks automatic restoration and requires operator action. Operator edits,
expired retention and concurrent releases retain the existing CAS/conflict behavior.

Measurements are operational state, excluded from environment clones, and cascade
with invocation/deployment retention. No payload, job response body or credentials
are stored. Customer receipts expose aggregate counts, durations, backlog and verdict.

## Validation

Memory and PostgreSQL cases cover insufficient evidence, job-driven rollback and
healthy completion alongside existing startup/configuration/operator guards. Claim
cases exercise stale attempts, replay generations, duplicate starts and immutable
outcomes. Gateway cases classify success, HTTP errors, partial failures, malformed
responses and transport failures. SDK/OpenAPI and CLI contracts include the policy.
Native acceptance is still required before enabling worker admission in production;
this workspace cannot establish KVM runtime or leak qualification.
