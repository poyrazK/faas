# ADR-1407 · Read-only subscription delivery pacing previews

Date: 2026-10-15
Status: Accepted

## Context

Operators can set shared consumer token-bucket limits, but need to review their
impact on retained pending deliveries and deadline risk before guarded mutation.
Combining independent client reads would mix revisions, clocks and backlog state.

## Decision

Expose a read-authorized POST preview with explicit enabled/interval/burst settings.
Use one read-only Repeatable Read transaction and database clock in Postgres, and
one locked observation in the memory store. Read current bucket credit, legacy
window, pause/circuit state, live-slot counts and a bounded oldest pending sample.
Do not acquire admission locks, reserve capacity, debit credit or record commands.

Reuse bucket refill and configuration projection rules. Return the reviewed bucket
revision for a later guarded command. Model rate-only admissions in deterministic
acceptance order with current scheduled times and legacy pacing. Return payload-free
identities for already expired and model-at-risk deadlines, honoring captured age
policy and overrides. Bound the pending sample at 1000 and risk details at 100;
expose coverage, incompleteness and independent truncation flags.

Keep timing advisory: this is not a scheduling/ordering guarantee, completion ETA,
rate reservation or deadline-safety certificate. Report current pause, circuit,
capacity and lease constraints separately. Omit duration on unusable clock evidence
or overflow. Maintain existing ordering, deduplication, retention and retry behavior.

## Consequences

Operators can review proposals without changing recovery state. The subsequent
revision guard detects bucket configuration changes only; tokens, other controls
and backlog can change immediately. Sampling can omit risks and the rate-only
model can overestimate or underestimate real drain time. The endpoint uses existing
operational schema, scope enforcement and SDK generation; no new DDL is required.

See [preview semantics and limits](../event-subscription-rate-preview.md).
