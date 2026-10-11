# ADR-603: Alert-driven service rollback

Status: Accepted

Date: 2026-10-05

## Context

ADR-602 made canary alert rollback durable but deliberately rejected service
deployments. Services already have exact binding-checked abort requests and
gateway acknowledgement/drain handoffs (ADR-600). An alert must use those
barriers and distinguish an accepted request from completed recovery.

## Decision

- Extend production alert capture to the sole active, zero-step service rollout.
  Use the handoff's retained predecessor, including at zero traffic. Before a
  handoff is pinned, accept only the sole older positive-traffic stable recipient
  in the same scope. Missing, newer, ambiguous or changed pairs fail closed.
  Completed historical rollback and account-wide automation remain separate.
- APID commits the exact abort request, alert fire linkage, attributed intent
  audit, ledger receipt and PostgreSQL notification in one transaction. The
  request UUID equals the fire UUID. Duplicate callbacks reuse that request;
  another active abort cannot be adopted or overwritten. A newer abort request
  invalidates the earlier alert receipt instead of silently taking it over.
- Reuse the existing service binding worker for current policy, evidence expiry,
  inventory revision and recipient capacity checks. Reuse scheduler gateway
  acknowledgement and request draining. This slice adds no instance writes,
  VM calls or scheduler lifecycle changes. Alert processing executes no probes.
- Project pending/blocked status plus service request, phase and routing audit
  through the existing read-only alert endpoints. After routing, expired binding
  evidence or a disabled rule cannot strand already accepted handoff cleanup.
  Before accepting intent, the enabled rule and exact pair are rechecked under
  app/rule locks. Late pre-request errors cannot overwrite accepted intent.
- Mark complete only for the matching abort request, committed routing audit,
  acknowledgement, completed handoff and exact restored traffic. Commit an
  attributed completion audit and terminal receipt together. Concurrent refresh
  adds one completion audit. Receipts carry no reusable binding grants.
- Add `gregale alerts actions --app APP --fire UUID --wait` with bounded timeout,
  GET-only polling and pinned app/fire/pair/request validation. Routing and
  draining are progress; timeout retains the last blockers. Interrupt exits 130.
  Keep non-waiting reads compatible and synchronize Go, Node and Python models.

This supersedes ADR-602's service exclusion for active zero-step service rollouts.

## Validation

Memory/PostgreSQL tests cover zero-weight retention, concurrent intent and
completion, wrong-recipient evidence, absent ready capacity, changed releases,
disabled rules, concurrent manual abort and stale writes. API tests cover
replacement-APID recovery, blocker projection and routing/draining barriers.
CLI tests cover GET-only waiting, timeout, mismatched receipts and incomplete
completion. Existing service binding and scheduler handoff regressions remain
part of the portable validation. Native Linux KVM acceptance from ADR-601 remains
pending; this feature does not add VM lifecycle behavior.
