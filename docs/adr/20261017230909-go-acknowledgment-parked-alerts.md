# ADR: Opt-in Go alerts for parked acknowledgment intents

- Status: Accepted
- Date: 2026-10-17

## Context

Finite callback budgets park exhausted acknowledgment intents durably. Bounded
health counts them separately, outside due/stalled eligibility. Existing recovery
watches can therefore report a clear automatic backlog while protected callbacks
still require operator action. Applications need sustained parked alerts with
restart-safe notification replay and the existing conservative evidence rules.

## Decision

Add optional `ParkedThreshold` to both Go intent health watches. Zero disables it;
positive counts through the health scan limit enable metric `parked_intents`.
Permit a parked-only watch. Use the existing read-only health query and its
`Parked` count, distinct from due/stalled values. Both opening and recovery need
at least two fresh, complete, advancing observations across the sustain interval.
Incomplete lower bounds cannot confirm even a threshold breach. Lost evidence
remains unknown and never resolves a retained incident.

Checkpointed watches use version 2 exactly when parked alerts are enabled. Bind
the parked threshold into the effective policy and append its enabled condition
after due/stalled. Retain the same Go kind and notification/incident/acknowledgment
shape. Reuse the bounded outbox, reserved resolution slots, ordered replay, exact
acknowledgments, detached snapshots and fresh proof after resume. No new incident
state machine or notification transport is introduced.

With parked alerts disabled, retain version 1 and omit `parked_threshold` from
policy JSON, preserving existing serialized field order and behavior. Validate
the version against the selection: version 1 cannot enable parked alerts and
version 2 cannot disable them. Older Go validators reject version 2 instead of
silently dropping the condition. Resume requires the same effective policy;
selection changes require a new session after application reconciliation and
retention of the old incidents/checkpoint/pending notifications under its writer
fence. Do not translate a saved version or discard notification history.

## Consequences

Upgrade all hosts that can resume or acknowledge a parked-enabled checkpoint.
The host continues to own fenced atomic storage, idempotent delivery and durable
nondestructive acknowledgment receipts. A resolved parked condition describes
only the retained count: granting more claims or newer staging may reduce it
without callback confirmation or business success. The watch neither claims nor
resumes work and makes no intent/schema/SQL mutation. Go retry-policy versions
remain independent of monitoring format versions. Node/Python parked watch
parity follows separately; their health counts remain available meanwhile.

## Validation

Standalone SDK/example build and vet passed. Static source comparisons confirmed
21 existing evidence/lifecycle/copy/acknowledgment bodies are unchanged, along
with the legacy policy field types, JSON tags and order. The new parked field
uses `omitempty`; health/query/classification and fresh/upgrade intent DDL remain
byte-identical. Gofmt, ADR uniqueness, documentation routes, all four added local
links/anchors and whitespace checks passed. No tests or assertions were added/
changed and no local tests or SDK helper smoke checks ran. Hosted qualification
remains pending for parked-only
and mixed incidents, threshold reversals, incomplete/stale/out-of-order evidence,
restart replay, outbox capacity, exact acknowledgment ordering, version skew,
operator resume/supersession, fenced storage and cooperative shutdown.
