# ADR-459 · Transactional issue evidence handoffs

- **Status:** accepted for preview implementation
- **Date:** 2026-10-03
- **Decision:** Add explicitly selected `issue.handoff` webhook packets for
  created, regressed, reopened, and customer-impact threshold issue transitions.
- **Why:** Summary notifications identify an issue but leave incident receivers
  and coding agents to gather the exception, release, and request context.
- **Consequences:** apid snapshots versioned, redacted, bounded evidence and the
  recipient set in the existing issue transaction. The outbox and signed webhook
  retry ledger deliver immutable packets after restart. Empty wildcard filters
  retain summary semantics; detailed evidence requires explicit opt-in.
- **Rejected alternatives:** Gathering evidence at delivery would change retries
  and race retention. A new delivery system would duplicate the outbox and retry
  contract. Automatically sending stacks to wildcard receivers would widen their
  established data exposure.

The payload records the exact triggering occurrence when available, immutable
issue-release metadata, verified aggregate customer impact in an explicit rolling
window, and uniquely matched singleton request evidence. Reopening selects
the latest retained occurrence. Account, app, deployment, time bounds, and current
plan retention constrain correlation; absent, ambiguous, or conflicting evidence
is recorded as a gap. Request API references require separate authenticated read
access. No reporting credential gains access to customer evidence.

Only span identity, name, kind, status, and duration are projected. Arbitrary
attributes, SQL statements, bodies, headers, and customer identity values are
excluded. The existing redactor applies again to exported evidence. Packet,
stack, frame, and span bounds live in `pkg/api/limits.go`; JSON escaping overflow
omits larger excerpts and marks the loss. Persisted packets follow the existing
webhook ledger retention, while linked live evidence follows issue/debug retention.

No agent runs automatically, no fixes deploy, and no VM lifecycle changes. The
first receiver integration can consume the same documented payload and SDK types.

Evidence: focused production-router PostgreSQL tests cover immutable snapshots,
opt-in recipient snapshots, exact retry/recovery, regression/manual reopen, scoped
correlation, expired and aggregate telemetry, and reporting-token read rejection.
Pure projection tests cover redaction, bounds, and exclusion of raw span fields.
Migration replay and OpenAPI/SDK checks cover the additive public contract.
