# ADR: Node recovery-worker liveness

- Status: Accepted
- Date: 2026-10-18

## Context

Database health polling cannot prove that a recovery worker schedules or joins
callback passes. Go now observes actual pass activity. Node needs equivalent
timing evidence while preserving its native cleanup and commit-uncertainty model.

## Decision

Add an explicitly invoked wrapper around the existing bounded Node recovery pass
and a fixed-memory owner-scoped cache. Validate options, native cache ownership
and nonoverlap before I/O. Record actual pass start and joined return. Preserve
the original result or typed exception, including cancellation and commit
uncertainty. Unknown non-pass rejections leave activity in flight. Health reads,
construction and scraping never schedule recovery or refresh actual activity.

Use conservative wall/local monotonic ages and sticky clock-invalid evidence.
Copy bounded scalar progress without retaining errors, owner identities, callback
inputs or pool resources. Node cleanup failures and uncertainty can overlap known
settlements; validate its native algebra rather than Go's exclusive outcome sum.

Expose Go's 23 fixed worker gauges plus an optional native cleanup gauge. Compose
the existing health-only host with a cache-only combined HTTP handler and an
alternative scrape job for the same endpoint. Extend the existing pass-failure
alert with cleanup failures and the worker dashboard with one cleanup series.
Keep the original health definitions and panels unchanged. Missing evidence stays
unknown. Applications own scheduling, resource bounds and joined shutdown.

## Consequences

Worker activity can fail independently from database-read evidence. Alerts retain
no durable incident identity and authorize no retries, budget changes, business
replay or fence takeover. Latest-pass gauges cannot establish global drain or
callback success. Go does not emit Node cleanup metadata. Python worker parity
remains separate work. See the [Node contract](../event-acknowledgment-node-worker-liveness.md)
and [operator runbook](../runbooks/GregaleEventRecovery.md#node-worker-hosts-and-cleanup).

## Validation

Publication verification is recorded in the PR. No local tests, assertion changes
or SDK helper smoke executions are permitted. Hosted behavioral qualification
remains pending for overlapping calls, clocks, native cleanup uncertainty,
cancellation, live scraping and alert transitions.
