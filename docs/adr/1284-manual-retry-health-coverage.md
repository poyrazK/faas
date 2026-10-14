# ADR-1284: Manual retry review health coverage

Date: 2026-10-14

## Context

The recovery health overview covers manual initial reviews but omits retained
manual retry response plans. These reviews have no initial-review expiry field.
Saved disposition and completion observations do not prove current target
freshness, application, execution success or worker liveness.

## Decision

Add separate manual retry counts to the same coherent read-only health snapshot.
Count undecided preview reviews with nonempty retained plans and retained parent
jobs only while the enabled current policy version matches, excluding explicitly
suppressed or completed reviews. Describe them as awaiting inspection, not ready
for execution. Count approved reviews without a completion observation across
policy changes; approval alone does not imply that a plan was applied. Dismissed,
automatic and completed reviews are excluded. Future observations are excluded.

Keep exact counts with bounded, deterministic issue samples and explicit
truncation. Combine them with existing overview state without changing legacy
queue classification. Link the existing review status endpoint and CLI inspector,
which rechecks action freshness and existing evidence. Reading health never
executes an inspector, worker, pruning or a recovery mutation. Existing permission,
version and action fences remain in force. Initial-review counters retain their
original meaning; new fields are optional in the public schema for compatibility.

## Consequences

Operators can find both initial and retry manual recovery work in one health view.
An approved review can remain unresolved until a worker records completion even
if its action has already finished; the status inspector resolves that distinction.
No migration or new execution authority is introduced.
