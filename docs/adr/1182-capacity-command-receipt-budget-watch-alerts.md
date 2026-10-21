# ADR-1182: Capacity-command receipt-budget watch alerts

Status: Accepted
Date: 2026-10-13
Related: ADR-1168, ADR-1181

## Context

Capacity-command receipt usage is visible in status and capacity preview, but operators otherwise need to poll manually to notice that new command UUIDs can be rejected at the retention limit.

## Decision

Add opt-in ReceiptBudgetFull duration to the existing CLI and Go SDK delivery-health watcher. CLI `--receipt-budget-full-threshold DURATION` requires `--watch`; zero disables it, and enabled thresholds use the existing 100ms–24h duration bounds. The condition metric is capacity_command_receipt_budget_full_seconds.

Read the optional complete receipt budget inside complete recognized capacity pressure, with matching app/subscription scope and observation time. Validate the existing receipt limit, count and remaining-slot arithmetic, full flag, and earliest-expiry consistency. This evidence is independent of the routing outcome window and execution-root history completeness. The outer watcher still verifies routing/execution app binding and applies freshness, future-date and strictly advancing observation checks.

Use the existing sustained-condition machine: require at least two fresh full observations and the configured local elapsed duration to open one incident. A fresh valid observation with available slots resolves it. Missing, malformed, stale, future-dated, repeated or mismatched evidence produces unknown; active incidents lose confirmation rather than reporting recovery. Sample gaps reset duration confirmation. Resume preserves historical incident/outbox identity but requires fresh duration confirmation and never counts process downtime as observed fullness.

Register the metric in the runtime checkpoint validator. Reuse existing sequence numbers, incidents, durable outbox reservations, persistence-before-notification ordering, acknowledgment handling and resume selection checks. Checkpoints without the disabled new option remain compatible; changing the selected metric or threshold still requires a new watch selection. Preserve the Go SDK's leaf-only constants while synchronizing watcher code. Node and Python continue to expose the health budget response; this change adds no language-specific lifecycle watcher there.

## Consequences

Alerts are advisory observations of retained command quota, not delivery capacity or write authorization. Expiry may free slots and concurrent commands may consume them. Full quota affects new UUIDs; retained commands repeated with their original inputs remain available. The watcher performs only existing health GETs, applies no controls and extends no retention. No route, migration, new quota, external notification integration or compliance registry entry is added.

CLI and Go SDK builds, source synchronization, static evidence/lifecycle/checkpoint checks and generated CLI documentation qualify implementation. No local tests are added or run under the user's constraint. Runtime qualification remains for CI. Earlier clone-table and SDK route registry approvals remain pending.
