# ADR-1256: Bounded management MCP request admission

## Status

Accepted. Extends ADR-1253, ADR-1254 and ADR-1255.

## Decision

Each management instance admits at most eight short requests and two rollout
watches concurrently. A local stdio connection owns its instance; all SDK sessions
and HTTP requests for one OAuth grant share its instance. Separate grants retain
independent capacity. These are local bridge bounds, not plan or billing quotas.

Short capacity covers ordinary `tools/call`, `resources/read`, `prompts/get` and
`completion/complete`. `gregale_watch_rollout` reserves only watch capacity, for
its entire handler including consent checks and progress sends. Subscription
baseline reads, polls and recipient revalidation reserve short capacity for each
snapshot. Existing listen-stream/resource bounds remain independent; an idle
listen does not consume a short slot. Initialize, discovery, ping and notifications
bypass admission so cancellation and unsubscribe cannot be blocked by saturation.

Acquire without queuing before invoking handlers or customer API reads. Saturation
returns fixed `mcp_busy` diagnostics with status 429, `retry_after_seconds: 1`, and
retry guidance. Tool overload is a standard `isError` tool result; other methods
use the existing sanitized RPC error contract. This is MCP-level backpressure,
not an HTTP status change. Use jitter when retrying, and reuse the same plan ID
for a mutation. Rejected requests do not execute their handler or consume a plan.

Defer release until the handler exits on success, error, deadline or cancellation.
Do not release on client cancellation before the native operation exits. Propagate
modern HTTP request cancellation for short calls as well as watches/listens;
legacy peers retain bounded handler deadlines. There are no admission workers,
queues or timers to clean up. Each subscription skips a busy poll/revalidation
without advancing its digest or terminating the listen; the next poll can deliver
an invalidation after capacity returns. Other subscription errors remain fail-closed.

## Consequences

Watches cannot occupy ordinary read capacity, and short-request saturation cannot
block watch admission or control notifications. Subscription observations may be
delayed further under overload. Limits do not guarantee fairness, request-rate
control, or an aggregate bound across many OAuth grants or bridge processes;
operators still apply their existing gateway controls.
