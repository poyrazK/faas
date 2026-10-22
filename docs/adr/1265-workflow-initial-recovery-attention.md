# ADR-1265: Initial automatic recovery attention notifications

## Status

Accepted, 2026-10-13.

## Context

Automatic first recovery retains unknown outcomes and exposes worker waiting
reasons, but dedicated queue notifications previously covered only saved-failure
retries. Operators need actionable changes without repeated queue checks.

## Decision

Extend the existing `automation.recovery_queue_blocked` webhook with optional
`initial_failure_id`, `state`, `previous_reason`, `operator_action` and `job_url`.
For automatic initial reviews, `state` is `blocked` or `recovered`. Legacy retry
payloads and their once-per-action behavior remain compatible.

Capture in the same worker transaction as the committed initial review
observation. Alert on missing evidence, unsaved terminal generation evidence,
account execution quota exhaustion or a paused recovery job. Repeated observation
of the same actionable reason stays quiet. Normal budgets, cooldowns and running
jobs do not open incidents. Policy pauses retain their existing dedicated event.

A previously observed block clears only when fresh worker evidence demonstrates
admission, execution progress or a proven result. Policy replacement, cancellation,
expiry and uninspected waits do not claim recovery. A recovered alert reports
blocker clearance, not successful execution. Native job completion remains the
source of completion notifications.

Persist the current block reason, monotonically increasing transition sequence
and latest notification receipt in the existing review JSON. Event identity is
review ID plus transition sequence, so repeated block/clear cycles have distinct
identities. Capture receivers at each transition, including an explicit
`no_receivers` receipt. New subscriptions do not replay old transitions. The
existing outbox freezes historical payloads and recipients and deduplicates by
event ID. Events can arrive after later transitions; receivers deduplicate by
`event_id` and compare `observed_at` for historical ordering.

Review and job links identify initial recovery directly; no parent job is
invented. Account and app authority are inherited from the account-first locked
worker. Submitted initial reviews remain observable across policy versions and
mode changes while unresolved. Read endpoints never capture notifications.

## Consequences

Operators get actionable reason changes and clearance notifications through an
existing subscription. Existing integrations accepting only `evidence_unknown`
should update their payload handling before opting into automatic initial
recovery. Notification captures grant no execution authority, release no slots,
refund no budget and never replace saved terminal generation proof. The latest
receipt is available on the review and the combined queue; full delivery history
remains in the webhook ledger.
