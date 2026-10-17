# ADR-1416 · Deadline count alerts in the delivery-health CLI

Date: 2026-10-16
Status: Accepted

## Context

Complete deadline cohort counts, reason-specific conditions and configurable recovery
boundaries are available in Go watches. Operators cannot select those conditions
through the delivery-health CLI.

## Decision

Expose deadline-window horizon, total and repeatable reason warning counts, optional
inclusive recovery counts, and independent warning/recovery durations on the existing
subscription-delivery-health watch. Require a valid horizon and at least one enabled
warning. Reject invalid cross-field settings before authentication or checkpoint
preparation. Preserve explicit zero recovery and reject duplicate reason selections.

Map options to the existing Go watch. Reuse its full-count evidence, sustained proof,
checkpoint policy matching, durable outbox, acknowledgment handling and persistence
before alert emission. Show effective count boundaries and existing confirmation
progress in text output; keep lifecycle alerts on stderr and final JSON unchanged.
Add no server endpoint, scheduler, database migration or notification transport.

## Consequences

Operators can use deadline alerts without writing a Go client. Selected conditions
add the existing deadline-window read to the watch, with unknown evidence preserved
on failures. CLI scope and timing limits match the Go behavior. Resume requires the
same effective policy and fresh observations. Recovery still describes rolling
aggregate counts rather than proof that deliveries completed.

See [usage](../event-subscription-deadline-window.md#cli-watch).
