# ADR-20261022161757: Connection-local management MCP action history and recovery

## Status

Accepted — 2026-10-22.

## Context

Management MCP persists guarded plans, attempts and native receipts, but agents
must retain the plan ID to recover a lost response. Expiry removes replay
payloads. Deployment history alone does not connect an uncertain native attempt
to its original reviewed, consented plan.

## Decision

Add read-only `gregale_list_actions` and `gregale_get_action` for image/source
deploys, request promotion, checked rollback and regression triage. One history
record binds its plan to immutable account/app/environment identities, with
separate apply and promotion phases. Record only fixed phase names and
allowlisted native references. Exclude reason, route, source/configuration,
approval tokens, replay keys, arbitrary native DTO fields and errors. Triage
history requires current diagnostic consent. Validate current identities before
collection and again before releasing results. Each OAuth grant owns a separate
namespace; refresh retains it and revocation deletes it.

`not_attempted` differs from `outcome_unknown`. Persist the latter before the
native write. A validated deployment/rollback receipt records `accepted`; a
guarded triage receipt records `committed`. Neither establishes deployment
completion, healthy traffic or service recovery. Reads never retry, write to the
native API or observe completion. Recovery points to the existing guarded tool
and same plan ID only while its original plan is available. It does not authorize
a retry or reconstruct/extend an expired plan.

Commit history and replay payloads in one encrypted, fsynced StateStore snapshot.
Failed persistence prevents a native attempt; store failure withholds uncommitted
history. Retain at most 256 records through 24 hours after original plan expiry,
prioritizing live supported plans over archived records. Read pages are 1–50 rows
(default 20), ordered by first recorded time and plan ID descending. Context-bound
keyset cursors tolerate new records; pruning can remove older rows. Bounds live
in `pkg/api/limits.go`. Recording times describe bridge observation, not an
invented native timestamp or legacy creation time.

The additive `actions/` version-1 namespace preserves existing plan/OAuth formats.
Import unexpired legacy plans on first load without reconstructing discarded
payloads. N-1 bridges ignore history and cannot record new actions; later upgrades
reconcile retained plans but cannot recover already-expired N-1 actions. Pruning
is lazy on startup/plan persistence; reads exclude expired records. Memory-only
bridges lose history on restart. Coverage is bounded connection history rather
than an account audit log. There is no new native API or schema migration.

## Consequences

Agents can locate the original retry handle and distinguish receipts from
uncertain outcomes. Expired or missing records require native inspection before
replanning. Workload rollouts, environment promotions and route-budget writes
remain outside the first profile; coverage and supported actions are explicit.
Local storage remains bounded by the shared encrypted journal capacity. Native
mutation authority and VM lifecycle behavior do not change.

Tests cover both MCP protocol profiles, redaction, promotion/rollback receipts,
lost responses, encrypted restart and same-key replay, expired authority, grant,
account/app/environment isolation, in-flight consent changes, legacy import,
bounded paging/retention and revocation cleanup. These portable checks do not
qualify native/provider deployments.
