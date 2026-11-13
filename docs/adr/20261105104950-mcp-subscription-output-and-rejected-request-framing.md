# ADR-20261105104950: MCP subscription output and rejected request framing

## Status

Accepted; extends ADR-426, ADR-20261104220400 and ADR-20261104222700.

## Context

Stream-count limits alone did not bound a Node subscriber's buffered output.
The Go starter cleared its ordinary write deadline for subscriptions, allowing
a blocked write to retain a slot beyond token expiry. Python rejected some
POSTs before consuming their bodies but kept the connection reusable, so the
next request started inside the previous body. Node also classified JSON parser
failures and oversized requests as HTTP 500 errors.

## Decision

1. Bound Node's pending setup messages and socket output to 1 MiB each. A write
   that encounters backpressure starts a 15-second deadline; subsequent writes
   cannot extend it. A drain clears the deadline. Overflow or a missed deadline
   destroys the stream and releases registrations and capacity, including
   adapters that finish setup after cancellation. Graceful completion and token
   expiry also destroy a backpressured response rather than leaving queued data
   on an unbounded pending end. Keepalive comments use the same output path.
2. Go gives each subscription message and keepalive comment, including its flush,
   at most 15 seconds or the remaining verified token lifetime, whichever is
   shorter. Clear that deadline after a successful write so idle streams can stay open.
   Propagate flush failures through the protocol observation wrapper, preserving
   write-failure telemetry and allowing subscription cleanup to run.
3. Python advertises `Connection: close` when a POST is rejected before its body
   has been decoded, including auth, origin, drain, catalog-pin, route and
   content-type rejections. Once framing succeeds, preserve the HTTP parser's
   original connection decision, including successful HTTP/1.1 keepalive and a
   caller's explicit close request.
4. Node returns a redacted JSON-RPC parse error (`-32700`) with HTTP 400 for
   malformed JSON, and `request_too_large` with HTTP 413 for the existing 1 MiB
   request limit. These client errors do not emit a server-error log event.

No platform schema, plan limit or MCP wire format changes are introduced.
Clients recover a closed subscription by reconnecting, reading fresh task
snapshots and fetching current resource contents.

## Validation

Node tests cover multibyte byte accounting, a fixed deadline under repeated
writes, drain and cleanup, HTTP stream-capacity recovery after output overflow,
bounded setup bursts with late adapter disposal, and redacted parser errors on
both supported protocols. Go race tests use a blocked `net.Pipe` through the
protocol observation wrapper to verify token expiry releases the stream slot;
they also cover flush failures and write deadlines that leave idle streams open.
Python HTTP tests cover reconnecting after early rejections and normal successful
connection reuse. CLI scaffolding tests require the new regression modules.
