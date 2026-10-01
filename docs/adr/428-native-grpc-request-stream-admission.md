# ADR-428 · Native gRPC request stream admission

- **Status:** accepted
- **Date:** 2026-10-02
- **Amends:** ADR-126 and ADR-200
- **Issue:** #4066
- **Context:** Complete body admission waits for EOF before dispatching a native
  gRPC request that expects replies while its upload remains open.
- **Decision:** Admit native gRPC incrementally through the existing bounded readers.
- **Why:** Request EOF cannot precede response messages in bidirectional exchange.
- **Consequences:** A stream can expose a prefix before a later size error; it is
  non-replayable, and ordinary upload admission remains unchanged.
- **Rejected alternatives:** Increasing timeouts does not break the EOF cycle;
  bypassing body caps weakens isolation; removing admission for every media type
  would discard the ordinary upload contract.

Native gRPC requests for an app explicitly configured with `app_protocol=grpc`
forward request messages without first waiting for request EOF. The media type
must be `application/grpc` or a native `application/grpc+...` subtype. Ordinary
HTTP requests, including requests that merely claim a gRPC media type on an
HTTP app, retain complete body admission and its upload/spool contract.

The existing plan-wide and matching edge-rule request readers remain installed
and enforce their byte limits as the stream is consumed. Known oversized lengths
still fail before wake or forwarding. Streaming consumption can expose an
accepted prefix to the guest before a later size error; it cannot promise atomic
prevalidation of an indefinitely open request. No limit is enlarged. Policies
that explicitly require complete body validation retain that validation.

Streaming request bodies are not made replayable and are not retried after a
partial send. Inbound cancellation, existing admission budgets, stream activity,
idle limits, and bridge ceilings remain effective. No VM lifecycle changes occur.
The response body cap wrapper preserves server duplex/deadline controls, and
native gRPC responses flush small messages even with ordinary response streaming
disabled. Response caps and metering remain enforced.

Acceptance uses actual HTTP servers and clients through the complete gateway
handler, an open request pipe, replies before request EOF, final trailers, both
native media types, and both ordinary response-streaming settings. Existing
ordinary upload/body-cap and flush/metering regressions must remain green. Signed
full-fleet rollout and verified TLS-origin/public wire tests remain required;
portable tests alone do not establish customer-path acceptance.
