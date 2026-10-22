# ADR-1183: Capacity-command receipt-budget early warnings

Status: Accepted
Date: 2026-10-13
Related: ADR-1181, ADR-1182

## Context

A sustained full-budget alert detects exhaustion after new command UUIDs can already be rejected. Operators need an earlier configurable warning based on remaining receipt slots.

## Decision

Add optional ReceiptBudgetRemaining (zero disables, otherwise 1–128) and ReceiptBudgetLowSustain to the existing CLI/Go watcher. CLI flags are --receipt-budget-remaining-threshold N and --receipt-budget-low-sustain DURATION, both requiring --watch. Sustain defaults to 30 seconds when the remaining-slot condition is enabled and accepts existing 100ms–24h duration bounds. Explicit sustain requires an enabled remaining-slot threshold. The existing full-budget duration alert remains independent.

Warn strictly below N remaining slots, including zero. To reuse the existing upper-bound lifecycle and preserve meaningful alert values, expose metric capacity_command_receipt_budget_used_slots with raw retained usage and threshold limit-N+1. For limit 128 and N=16, usage >=113 means remaining <=15. Clear when remaining reaches N or more. Use the existing sustained breach helper, with at least two fresh observations and configured elapsed duration; serialize minimum_samples=2 and sustain_seconds in conditions and alerts.

Reuse the full-budget evidence validator, including complete recognized pressure, complete owned receipt budget, scope and shared observation binding, limit/count arithmetic and earliest expiry. Freshness, monotonic observations, future-date handling, sample gaps, confirmation loss, durable outbox reservations and acknowledgments stay in the existing watcher. Missing or invalid evidence is unknown rather than a zero-slot warning or false recovery. Resume requires fresh sustained confirmation and preserves incident identity; downtime is not observed pressure.

The runtime checkpoint validator recognizes a bounded integer used-slot threshold and validates exact sample count, bounded sustain duration and raw usage in 0–128. Existing resume matching includes selected metric, converted threshold, samples and sustain. Disabled warnings preserve prior checkpoint selections. Older readers need upgrading for checkpoints with this metric. The new default lives in pkg/api/limits.go and is mirrored in the Go SDK's leaf constants.

## Consequences

Warnings are advisory, do not reserve slots, do not predict time to exhaustion, and trigger no capacity mutation. Expiry and concurrent commands can change the budget. Retained commands repeated with their original inputs remain available when full. No endpoint, schema, migration, new quota or compliance registry entry is added. Node/Python health budget models remain usable without adding local lifecycle watchers.

CLI and Go SDK builds, generated CLI reference and static evidence/lifecycle/checkpoint/mirror checks qualify implementation. No local tests are added or run under the user's constraint; runtime qualification remains for CI. Prior table and SDK-route registry approvals remain pending.
