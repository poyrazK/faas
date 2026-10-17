# ADR: Python acknowledgment intent recovery health summary

- Status: Accepted
- Date: 2026-10-17

## Context

Python can durably stage every callback outcome and recover saved intents, but
listing a small set of individual statuses does not summarize a growing or stalled
recovery backlog. Go and Node already classify the retained unfinished store with
bounded sampling, explicit coverage and a single database clock.

## Decision

Add `aget_event_acknowledgment_intent_health`, immutable bounded options and a
frozen metadata-only summary. Reuse the Node query's classification, snapshot and
clock with psycopg positional parameter bindings. Sample unfinished rows in the
same deterministic order, using an extra row as truncation evidence. Exclude
terminal history from the sample. Compute eligibility from creation, retry time
and claim expiry, keeping live claims/future retries separate from stalled work.

Return owner scope, database observation time, counts/ages and explicit coverage,
completeness/truncation. Validate count identities, finite nonnegative ages,
representable timestamps and aggregate consistency. Query-detected timestamp
anomalies make coverage incomplete; malformed aggregate evidence raises a fixed
storage error. No capabilities, payloads or invocation identities are returned.

Use one SELECT on an exclusively leased idle autocommit psycopg async connection,
without explicit BEGIN/COMMIT or locks. No claim, callback, renewal or business
work occurs. Share cooperative owned-task cancellation; shield/join active SQL and
cursor cleanup. Sanitize driver/cursor failures and discard the failed connection.
Keep timeouts, acquisition, polling and alert policy application-owned.

Preserve the handwritten module and public exports through SDK generation and
wheel/sdist packaging. Add a separate monitoring observation in the protected
queue example without changing processing or recovery entry points.

## Consequences

Python operators can observe due/stalled callback backlog while business ingestion
continues. Counts from a truncated sample are lower bounds; counts/ages always
travel with completeness. Coverage concerns retained local unfinished intents,
not missing/pruned work, platform completion, host liveness or external effects.
No schema, platform endpoint, registry, automatic polling, alert transport or
external exactly-once change is introduced. Go/Node alert watches remain separate
from this Python read-only increment.

## Validation

Use compilation/lint, Python 3.11 syntax, static query-contract comparison, isolated
SDK generation without test files, wheel/sdist inclusion, canonical schema and
documentation/static checks. No tests/assertions are added/changed and no local
tests are run. Hosted PostgreSQL classification, concurrency and cancellation
qualification remains pending before release. See the
[Python health contract](../event-acknowledgment-intent-health.md#python-summary).
