# ADR-1258: Graceful management MCP drain and journal cleanup

## Status

Accepted. Extends ADR-1254 through ADR-1257.

## Decision

Introduce a shared `Lifecycle` coordinator for management servers and their HTTP
wrapper. Atomically reject new work once drain begins. Track MCP handlers,
subscription polling workers and the complete HTTP durable-response wrapper,
including its final journal commit. Fresh and restored OAuth grant servers inherit
one coordinator. Initialize, discovery and session controls bypass the MCP work
gate; the HTTP wrapper rejects new requests with 503 and `Retry-After: 1`.

Cancel read-only observations, watches, listen streams and their workers immediately.
Registered mutating tools retain their active contexts during grace. Keep accepted
HTTP handlers tracked through OAuth/journal publication. Rejected MCP work returns
sanitized `mcp_draining` diagnostics with status 503 and retry guidance; no handler
or customer API call runs. Existing admission bounds and consent guards remain.

On CLI SIGTERM/SIGINT, allow five seconds for active mutations and HTTP journal
commits. Then cancel remaining tracked work and wait at most another five seconds
for cleanup. HTTP listener shutdown runs concurrently and force-closes connections
at the grace deadline so stalled request bodies cannot hold cleanup indefinitely.
Stdio transport cancellation follows drain, allowing accepted tool handlers their
grace while still handling control messages. EOF or transport failure also drains
remaining tracked handlers; a disconnected client's own cancellation remains effective.

Close the encrypted single-writer state store only after the coordinator reports
all tracked work finished. If cleanup remains incomplete, report failure and retain
the lease until process exit rather than unlocking a journal that active handlers
might still write. Library embedders pass `NewLifecycle()` in server options, call
`Shutdown` with their grace context, and keep state open if cleanup returns an error.
A drained coordinator never reopens; restart uses a fresh coordinator.

## Consequences

Subscription, watch and short-request reservation gauges return to zero after
successful cleanup. Drain waits for worker exit and durable-response publication,
not merely cancellation delivery. Accepted native mutations are not undone by
cancellation. Ambiguous remote writes retain their durable plan and native retry
key; after restart with the same encrypted state, retry the same plan ID. Ephemeral
stdio plans do not survive process restart. Shutdown preserves grants rather than
revoking their native credentials, and metrics stay available during drain until
the CLI exits.
