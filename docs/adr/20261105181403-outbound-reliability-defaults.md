# ADR: Bound outbound work and pace retries without an explicit policy

- Status: Accepted
- Date: 2026-11-05

## Context

Several constructors depended on a caller supplying a deadline, and a few
background callers supplied only their process lifetime. Other code checked an
outer polling deadline but could block indefinitely inside a network call.
Immediate conflict retries and environment queue redelivery could amplify an
outage. A Redis `PoolSize` alone did not cap active connections.

## Decision

Apply finite connection, pool-acquisition and fallback query deadlines in the
shared Postgres configuration, retaining explicit caller deadlines and daemon
connection budgets. Alternative pools use explicit concurrency budgets and the
same timeout configuration. Finite HTTP calls have defaults; long-lived streams
keep their existing cancellation lifecycle and phase/stall bounds.

The shared pgx deadline implementation lives in the lightweight
`pkg/pgpoolbounds` package. Guest code imports this leaf directly so pool bounds
do not pull server migrations, goose or template reflection into guest-init.

Generated Go and Node MCP task starters size their Postgres pools for configured
workers plus four slots for notifications, polling, maintenance and foreground
requests. Web and observer roles reserve four slots without worker capacity.
The Go starter also bounds pool waits and queries without caller deadlines.

Retries of definite aborted transactions or rejected CAS writes retain their
attempt budgets and gain capped exponential backoff with jitter. Uncertain
unkeyed writes receive no additional transport replay. Redis retry-budget
increments disable automatic replay after a lost reply.

A project environment queue delivery with an unset retry policy no longer
requests immediate redelivery. It uses the existing platform event-delivery
base and cap (5 seconds and 5 minutes) with equal jitter. A pinned nonzero
policy still controls its delivery; plan and row attempt ceilings still decide
dead lettering. This amends the previous zero-policy immediate-retry behavior.

Ordinary Node and low-level Python SDK calls default to 30-second timeouts.
Configured Go, Node and Python transport retries make at most eight retries
(nine total attempts), have positive default delay, and cap exponential growth
at eight seconds. Longer Retry-After hints remain meaningful. Streams can keep
an explicit caller-controlled lifetime.

## Consequences

Pool saturation and stalled dependencies now fail within a known bound instead
of consuming resources indefinitely. Unconfigured environment queue consumers
observe a delay before redelivery. Clients that previously passed zero backoff
receive a positive default; callers needing longer requests must configure an
appropriate deadline. Production capacity is still validated against fleet
pool metrics, replica counts and rollout overlap.

The source inventory and detailed bounds are in
[the outbound resilience audit](../outbound-resilience-audit.md).
