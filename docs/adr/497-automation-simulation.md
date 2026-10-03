# ADR-497: Stateless automation simulation with bounded sample data

Status: Accepted — 2026-10-03.

## Context

Definition validation checks a customer's DAG but cannot show how sample input,
guards, joins and loop mappings interact. Starting a manual sample run invokes
real app handlers and integrations. Customers need a trace they can inspect
before publishing or executing an automation.

## Decision

Add POST `/v1/apps/{slug}/automations:simulate` with a submitted definition,
sample workflow input, successful root action mocks and sequential loop item
mocks. Ownership, MFA and read scope apply. Definition validation uses the
existing plan/DAG validator and read-only managed integration binding checks.
Simulation works without a live deployment or enabled runtime. It has no store,
executor, clock or credential dependency; the handler only reads app ownership
and bindings. It never writes drafts, run/attempt state, events, audit payloads
or outbox rows, unseals credentials or calls scheduler/guest/outbound services.

The pure state-package engine shares runtime input/guard evaluators, branch join
selection and ordered loop output aggregation. A bounded input evaluator checks
the encoded size while resolving each value, before allocating the entire
expansion. Existing unbounded runtime entry points retain their behavior.
Mocks represent successful JSON outputs, including explicit null; missing mocks
do not imply null or successful execution. Missing results block dependencies.
Control outputs resolve automatically. Guards and skip ancestry follow runtime
semantics. Loop inputs materialize together, then the supplied output prefix
resolves sequentially. Empty loops resolve to an empty output array.

Waits stay unresolved. This slice does not inject failed/timed-out outcomes,
advance timers, complete callbacks or model retries. An exception target stays
blocked while its source outcome is unknown and skips after success/inactivity.
An action mock using the exact reserved `{"timeout":true}` output with an
`on_timeout` route returns 400 rather than misclassifying a timeout as success.
Sample evaluation errors are reported with stable value-free reasons; no failure
context is invented. Bounded multipass evaluation accounts for exception edges
that are not part of the validator's ordinary dependency topological order.

The response distinguishes definition validity from sample evaluation issues.
Invalid definitions return 200 with an empty trace. Completeness means every
root resolved or skipped under the mocks; it does not establish live availability,
successful real effects, trigger matching, quotas or provider verification.
The response includes SHA-256 of the serialized submitted definition and a
deterministic flat trace, ordered by dependency order with items after parents.
Unknown/control mocks and excess materialized item mocks return 400. Unused
valid mocks produce warnings. Resolved customer values are returned only to
the authorized caller and are not included in error reasons or audit events.

Central limits in `pkg/api/limits.go` bound requests to 3 MiB, definitions and
individual samples to 1 MiB, roots to 128, trace entries to 1024 and responses
to 4 MiB. Existing 128-item/1-MiB input/1-MiB output loop limits remain in force.
Size expansions are rejected before constructing unbounded traces. Limits return
413 with machine-readable metadata. Go, Node and Python SDKs expose the contract.

## Rollout and rollback

Deploy updated apid for the API. No migration, scheduler change, production flag
or frontend change is needed. Downgrading apid removes this stateless endpoint
without persisted simulation state to drain or recover. The existing workflow
runtime prerequisites still apply to real runs.

## Validation

Pure engine tests cover runtime join parity, typed mapping, exact numbers,
template literal preservation, guards and skip ancestry, missing mocks, waits,
exception ordering, sequential/empty loops, invalid samples, determinism,
caller-data isolation, cancellation and expansion limits. API tests verify
ownership, scopes, strict decoding, bindings, request bounds, runtime-off use
and no runs, invocations, drafts or customer event changes. SDK tests and
OpenAPI parity checks exercise the typed wire contract.
