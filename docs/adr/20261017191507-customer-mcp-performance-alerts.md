# ADR 20261017191507: Customer MCP performance alerts

Status: accepted

## Context

MCP protocol charts and fixed operator warnings exist. App owners need their own
thresholds and notifications through the existing customer alert system.

## Decision

Add two app-scoped, webhook-only metrics: `mcp_protocol_error_rate_pct` and
`mcp_protocol_execution_p95_ms`. They use the host-attributed starter observations
from ADR 20261017191506. They are not authorization evidence or deployment-action inputs.
The error population includes RPC errors, tool errors, authorization errors,
internal errors, and timeouts. Cancellations remain separate. Execution p95 uses
response-construction measurements, independently of SSE delivery duration.

Persist `mcp_min_calls` (20..1,000,000, default 20) and `mcp_for_minutes` (1..60,
default 10) with each rule. Non-MCP rules must leave both settings unset/zero.
Windows are 5m, 15m, or 1h, comparisons are gt/gte, error thresholds are 0..100
percent, and latency thresholds are 0..300,000 milliseconds. The upper latency
bound follows the histogram's last finite bucket. PostgreSQL CHECK constraints,
MemStore validation, the API, and evaluator each enforce the notification-only
boundary. The migration is transactional, replay guarded, and refuses rollback
while MCP protocol rules exist. Its saved metric constraint preserves the prior
allowed set and restores it on rollback.

The evaluator checks app ownership before querying by app UUID. Latency traffic
gating counts measured executions; error-rate gating counts RPC outcomes. Current
observations must pass their population gate in the same PromQL expression.
Every historical 30-second evaluation must also meet its population threshold.
A min_over_time verdict plus an exact expected sample count rejects brief bursts
and holes in eligible history. Step-aligned queries include both endpoints;
unaligned queries have one fewer sample, and the count guard accounts for that.
A promtool fixture runs the actual evaluator expressions and a Go test prevents
query/fixture drift.

Sustained duration comes from retained Prometheus history rather than evaluator
memory, so meterd restarts do not reset it. Newly created rules wait at least the
configured duration before firing. Edits and re-enabling use the current settings
against retained history; they do not discard that history. A breach must still
be current. Missing/insufficient observations and incomplete breach history leave
the rule unknown; backend failures leave it degraded. Neither sends a webhook or
claims healthy recovery. Sufficient current observations below threshold resolve
the rule normally. Cooldowns, sealed signing secrets, delivery deduplication and
retries use the existing alert system. Webhook payloads include the selected MCP
population and duration settings and mark the source as starter reported.

The authenticated MCP operations view offers create/edit/enable/disable forms for
the selected app. Writes require a named account CSRF token, bounded form bodies,
nonduplicate whitelisted fields and app/rule ownership checks. The forms reuse
API handlers for plan limits, quotas, URL egress checks, sealing and validation.
Signing secrets are entered only on creation and never rendered back. Existing
rule details and delivery history remain available. OpenAPI and generated Node
and Python SDKs expose the settings and metrics.

## Freshness and limits

An idle app has no protocol events; lack of events is not a service failure.
Observation recency remains separate on the performance charts. No customer
freshness alarm is inferred from event silence, and no publisher heartbeat is
invented from RPC completions. Missing instrumentation, lost guest output, partial
host coverage and unsupported starters limit the evidence. Long-lived streams
report when delivery ends. Custom tools share `other`; alerts aggregate the app.
Sustained detection has 30-second history resolution plus the evaluator interval.
