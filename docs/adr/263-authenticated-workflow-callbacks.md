# ADR-263 · Authenticated one-time workflow callbacks

- **Status:** proposed
- **Date:** 2026-09-25
- **Decision:** Add a mutually exclusive `wait_for_callback: true` workflow step. Each run and callback step has a deterministic opaque UUID handle. An account-authorized client lists the handles and completes a callback with JSON through the workflow API. Completion writes a single event with that UUID as its id. The same payload may be retried; a different payload conflicts. A completion before the wait activates is retained and consumed when dependencies finish.
- **Why:** Named `wait_for_event` works for generic events, but a callback should have a per-execution identity, one-time completion semantics, and a clear authenticated completion endpoint. Applications should not need their own polling row or retry column to resume a parked workflow.
- **Consequences:** The callback ID is **not** a bearer secret. Both discovery and completion require the owning account's workflow API authorization. External webhook providers still need an application endpoint to verify signatures and relay the completion; giving a provider an API key is unsafe. A callback's timeout starts on step activation and is bounded by the plan's wait limit. Completion, event insertion, wait parking, and deadline resolution lock the same run row, so arrivals and timeout decisions are serialized. Events for active runs cannot be swept before a late-dependent wait consumes them. Existing workflow event and step tables are reused without a migration.
- **Rejected alternatives:** A public callback URL backed only by an unguessable token would require separate token lifecycle, abuse control, rotation, and provider-signature policy; event-name-only callbacks permit accidental cross-run matches; checking for events and parking in separate transactions can lose a simultaneous arrival; deleting old events from active runs can erase an early callback.

This extends ADR-081 and ADR-262, but is still a declarative DAG, not checkpoint/replay of one long-lived function. Callbacks are accepted at most once in the logical ledger; downstream handler execution remains at least once and must be idempotent.

ADR-264 adds a Stripe-specific path from an existing provider-verified inbound
webhook endpoint to this callback ledger. The authenticated completion API
remains the provider-neutral path.
