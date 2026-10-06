# ADR-490: Declarative workflow step guards

Status: Accepted

## Context

Customer automations can start on schedules/events and call app handlers or
managed outbound integrations. `wait_for_condition` polls an app checker; it
cannot select an action from existing JSON data without additional customer code.

## Decision

Add optional `when` predicates to all normal step targets. A predicate has one
of four forms: `{ref, op, value}`, `{all: [...]}`, `{any: [...]}`, or `{not: ...}`.
References use the existing input-template path grammar without delimiters:
`input.amount` or `steps.lookup.output.body.active`. Only direct dependencies
may supply outputs. Failure context is excluded, and exception-handler targets
cannot be guarded, so a false guard cannot suppress a configured recovery action.

Operators are `eq`, `ne`, `gt`, `gte`, `lt`, `lte`, and `exists`. Equality accepts
JSON scalar literals and preserves their types. Numbers compare exactly rather
than through floating-point conversion. Missing paths fail comparisons, including
`ne`; present null values exist. `exists` takes a boolean. `not` negates normally,
including a false result for a missing path. Arrays support canonical zero-based
index path components. Literal strings are not interpolated or executed.

Each predicate is bounded to 16 KiB, 32 nodes and eight levels. Compared numeric
literals and source numbers are bounded to 4096 bytes with scientific exponent
magnitude at most 4096, preventing unbounded arbitrary-precision allocation.
All limits live in `pkg/api/limits.go`. Invalid definitions fail validation;
unusable runtime JSON fails the step with a generic error, without exposing
referenced values in errors. A missing field alone is a predicate result.

The state store evaluates against run input and completed dependency outputs
while holding the run lock used for cancellation, output completion, and
recovery. It persists `when_matched` and `when_evaluated_at` once. A false
decision also commits the `skipped` transition, finish time and `when_false`
reason in that transaction. No executor attempt or wait activation is allocated.
A true decision remains attached to the step across durable retries and both
explicit and lease-expiry recovery. Handler dispatch cannot start a guarded
step before its true decision is recorded.

A step with any skipped dependency is skipped with `dependency_skipped`.
Propagation reaches descendants regardless of declaration/storage order and lets
the run finish when all remaining steps are terminal. Existing failure/timeout
handlers retain their routing rules. Their inactive targets and failed-dependency
paths gain `route_not_taken` and `dependency_failed` reasons. Skip operations
cannot overwrite a dispatched/terminal step or resurrect a cancelled run.

The first version has success dependencies and skip propagation only. It does
not add joins that accept inactive branches: a step depending on both sides of
an if/else is skipped. Customers place continuation steps within each branch.
General branch joins, loops, scripts and dynamic step creation are deferred
from this increment. [ADR-491](491-native-workflow-branch-joins.md) subsequently
adds native joins for conditional branches.

The existing step inspection API exposes the decision, evaluation time and skip
reason. Draft validation/publication, manifest decoding, OpenAPI and generated
Node/Python SDKs accept guards. No frontend or VM lifecycle changes are needed.

## Rollout and rollback

Apply `20261003140000001_workflow_step_guards.sql` before updated binaries.
Deploy apid and schedd together before publishing guarded definitions. Guards
use the existing workflow runtime gate; no additional production flags change.

Before downgrading, pause starts and drain/cancel guarded runs, then replace
guarded API and manifest definitions with definitions the older binaries can
decode. Keep the inspection columns until all updated processes are stopped;
dropping them removes guard history. The migration down path is operator-only.

## Verification

Exercise strict JSON/YAML decoding, reference access, scalar types, exact large
integer/decimal comparisons, missing versus null, bounded parsing and malformed
runtime data. Memory and PostgreSQL tests cover durable decisions, cancellation
races and recovery. Scheduler tests cover branch selection, transitive skip
propagation and false guards on every target, with no calls or parked waits.
SDK tests preserve nested guards, boolean/null/numeric literals and skip history.
