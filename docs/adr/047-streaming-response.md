# ADR-047 · Streaming responses through gatewayd

- **Status:** accepted (PR-D 2026-08-01; PR-A #481, PR-B + PR-C #490
  shipped earlier. PR-D closes the four deferred items: metal e2e,
  ADR status flip, dashboards, unary ForwardHTTP removal)
- **Superseded (in part, PR-E):** prose referred to the monolithic
  `cmd/gatewayd/` daemon split by ADR-070 into `gatewayd-public` (TLS-only
  edge) and `gatewayd-internal` (routing + wake + proxy). Body is preserved
  verbatim; readers should substitute "gatewayd-internal" for the
  routing/wake/proxy path and "gatewayd-public" for the certmagic/TLS path.
  `cmd/gatewayd/<file>.go` citations in this body are stale; see PR-E for
  the new file locations.
- **Date:** 2026-07-31 (initial); 2026-08-01 (PR-D finalization)
- **Issue:** #471 (HTTP streaming responses through the FaaS gateway)
- **Predecessors:** ADR-009 (identical inner network world — the
  invariant that makes snapshot reuse work), ADR-028 (ForwardHTTP gRPC
  bridge between gatewayd and vmmd), ADR-046 (per-instance egress
  metering; the telemetry primitive PR-B rides on top of)
- **Decision:** turn on real streaming responses through gatewayd on
  Hobby+ plans while keeping Free-tier apps strictly buffered. The
  mechanism is a single chokepoint (the `statusRecorder` in
  `pkg/gateway/handler.go`) augmented with a per-flush hook, plus a
  new bidi-streaming `ForwardHTTPStream` RPC in
  `pkg/vmmdgrpc/forward.go` that replaces the legacy unary
  `ForwardHTTP` for the streaming path. The wire boundary stays
  HTTP/1.1+chunked to the client; the gateway→vmmd leg becomes a
  bidi gRPC stream so the bridge stops `cat`-slurping the response.

## Context

Until PR-A, every gatewayd response was buffered end-to-end: the
ReverseProxy read the full upstream body into memory, the
`statusRecorder` counted the bytes, the response went out as one
buffered Write. That works for ≤ 25 MiB responses but breaks two
real customer shapes:

- **LLM streaming responses** (OpenAI-compatible `text/event-stream`
  with chunks of 10-200 bytes every 50-200 ms over 5-60 s). A 30 s
  response at 100 bytes/s is 3 KiB, but a customer running ten
  concurrent LLM chats buffers 30 KiB × N requests in flight. The
  financial model assumes the gateway streams; today's buffered
  path costs RAM and adds 100-500 ms tail latency on every chunk.
- **Long-running server-side analytics** (a single 100 MiB JSON
  array streamed as one record per row). The Free plan's 25 MiB
  cap blocks this entirely; even Hobby's 100 MiB cap is meaningless
  if the response is buffered.

PR-A (PR #481, merged) shipped the seam — the per-app flag, plan
default, env opt-in, plan-gate, and a once-per-process deprecation
log for the buffered fallback. This ADR is the design record for
PR-B (the Flusher path on local proxy) and PR-C (the bidi gRPC
bridge for multi-node) which together close the seam.

## Decision (one paragraph)

`pkg/gateway.handler.go`'s `statusRecorder` becomes the single
chokepoint for both buffering and streaming. On the streaming
path, the Handler wraps `w` with `(*Handler) setupStreamingWriter`,
which (a) installs a per-flush `onFlush(cumulativeBytes)` callback
that fires per `doFlush()` call and (b) wraps the recorder in a
`capWriter` that emits a 413 `streaming_not_available` problem+json
on cap-exceeded. The callback computes
`delta = cumulativeBytes - lastReported`, attributes to
`h.egressSink.RecordResponseBytes(target.InstanceID, delta)` and
`h.metrics.ObserveResponseBytes(app.ID, plan, delta)`, and tracks
`lastReported` in a closure-local `int64`. The gateway-side
`x-faas-stream: true` header stamp on the inbound request tells
`forwardproxy.fwdOnce` to open the new bidi `ForwardHTTPStream`
RPC instead of the unary `ForwardHTTP`. On the vmmd side,
`Server.ForwardHTTPStream` spawns the streaming bridge script
(chunked-encoded request body from stdin, streaming-read response
body in 8 KiB chunks with per-read timeout) and pipes each chunk
back as a `body_chunk` gRPC frame. The streaming request cap
lifts to 100 MiB (`ForwardStreamMaxBodyBytes`) and the response
timeout to 900 s (`ForwardStreamResponseTimeout`); the unary path
stays at 25 MiB / 60 s. Per-request opt-out is via the
`Accept: application/json` header — checked in `isAcceptJSON`
before the streaming wrap.

## Decision (numbered)

1. **Streaming gate.** The Handler takes the streaming path iff
   `h.streamingEnabled && app.StreamingEnabled &&
   !isAcceptJSON(r.Header.Get("Accept"))`. `h.streamingEnabled` is
   the operator opt-in (env `FAAS_GATEWAY_STREAMING` or TOML
   `streaming_enabled`); `app.StreamingEnabled` is the per-app flag
   set on build / PATCH; `isAcceptJSON` is the per-request opt-out.
   apid already rejects non-paid plans with
   `CodePlanStreamingNotAllowed` (`pkg/api/errors.go`), so a Free
   app with `streaming_enabled=true` is a misconfiguration that the
   buffered-fallback log catches (`streamingFallbackLog`).
2. **Per-flush triggers.** `statusRecorder.maybeFlush()` fires a
   flush on any of: (a) first chunk after `WriteHeader` (stamps
   the per-flush `SetWriteDeadline` via
   `http.ResponseController`); (b)
   `bytes-since-last-flush ≥ StreamingFlushBytesDefault (256 KiB)`;
   (c) `time-since-last-flush ≥ StreamingFlushIntervalDefault
   (200 ms)` AND `bytes-since-last-flush > 0`. The 256 KiB / 200 ms
   window is the throughput sweet spot for LLM-shaped streams
   (200-300 flushes per 30 s response) without burning CPU on
   per-byte flush attempts.
3. **Per-flush metering.** Each `doFlush()` calls
   `onFlush(cumulativeBytes)` BEFORE pushing bytes to the wire.
   The Handler's closure subtracts `lastReported` against
   `cumulativeBytes` to compute `delta`, attributes to the egress
   sink (per-instance, per-minute bucket) and the
   `gateway_response_bytes_total` counter. The total of every
   per-flush delta equals `rec.Bytes` after `proxy.ServeHTTP`
   returns (modulo the residual capture in `finalFlush`).
4. **Two-phase accounting.** `recordEgress`'s once-per-response
   call short-circuits on the streaming path (the per-flush
   deltas already cover everything). The Handler calls
   `rec.finalFlush()` after `proxy.ServeHTTP` returns; this fires
   one last `onFlush(cumulativeBytes)` covering the trailing
   bytes between the last periodic flush and the upstream
   finishing. The sum of per-flush deltas + residual equals
   total egress bytes — verified in unit + e2e.
5. **Cap lift via per-request writer.** `pkg/gateway.capWriter` is
   a thin `http.ResponseWriter` wrapper that enforces the plan's
   `MaxResponseBodyBytes` (Hobby 100 MB / Pro 100 MB / Scale 100 MB;
   Free 25 MB on the buffered path). On cap-exceeded, the writer
   fires an `onCap` callback that emits a 413
   `streaming_not_available` problem+json to the ORIGINAL
   `http.ResponseWriter` (before the wrap), then sets an
   `atomic.Bool` disabled flag so subsequent Writes short-circuit
   to `http.ErrHandlerTimeout`. The stdlib `http.MaxBytesWriter`
   is NOT used because it writes a generic 502; the platform needs
   the structured problem+json shape (RFC 7807) for dashboards and
   retry logic.
6. **Per-flush write deadline.** `doFlush()` installs
   `SetWriteDeadline(time.Now().Add(writeDeadline))` on the first
   flush via `http.ResponseController` (Go 1.20+). The deadline is
   re-installed on every subsequent flush, sliding forward by
   `flushInterval`. A streaming client that stalls the read for
   more than `flushInterval × N` flushes hits the deadline and the
   gateway closes the connection — the bridge sees EOF on the
   guest side and unwinds cleanly.
7. **Multi-node: bidi `ForwardHTTPStream` RPC.** Replaces the
   unary `ForwardHTTP` for streaming requests. Wire shape:
   ```
   client → server:  1× ForwardHTTPRequestInit, then N× body_chunk
   server → client:  1× ForwardHTTPResponseInit, then N× body_chunk
   ```
   Bidirectional because streaming requests often pair with
   streaming request bodies (an SSE handler consuming a client
   feed). The unary `ForwardHTTP` stays for one cycle as a
   deprecated path so a rolling deploy across the vmmd fleet
   doesn't break older gatewayd builds. PR-D removes it.
8. **Bridge script: chunked-encoded body, streaming-read
   response.** The new `buildStreamingBridgeScript` replaces the
   `cat <&3` slurp with a chunked-encoding loop on the request
   body (read from stdin, written as `<hex-len>\r\n<body>\r\n`
   per chunk) and a streaming read loop on the response body
   (`read -t N -n 8192`). The legacy script stays for the unary
   path. Output shape on stdout is unchanged
   (`<status>\n<headers>\n\n<body bytes>`) so
   `parseBridgeOutput` keeps working — the bidi gRPC framing is
   a pure transport translation.
9. **Cap-lift symmetry.** `ForwardStreamMaxBodyBytes = 100 MiB`
   and `ForwardStreamResponseTimeout = 900 s` apply iff the init
   frame's `stream=true` field is set (the gateway stamps this
   when the streaming path is taken). The unary path stays at
   `ForwardMaxBodyBytes = 25 MiB` and `forwardResponseTimeout
   = 60 s`. A misrouted streaming request through the legacy
   path still hits the smaller cap — fail-closed by design.
10. **Per-request opt-out header.** The customer-visible opt-out
    is `Accept: application/json`. `isAcceptJSON` parses the
    Accept header (comma-separated, case-insensitive, parameter-
    tolerant) and returns true if any token matches. The opt-out
    is purely gateway-side: apid never sees it; the per-app flag
    stays unchanged. A customer can flip the per-app flag back
    on at any time without the request-level opt-out mutating
    state.
11. **Single-registry metrics.** `gateway_stream_flushes_total`
    and `gateway_stream_active` are registered on the
    gatewayd-local Prometheus registry only (one registry per
    `Metrics` struct, per the `wire-opsmetrics-single-registry`
    invariant). PR-D closes the bug where `streamFlushes` was
    constructed but omitted from `reg.MustRegister` — both
    series are now emitted from the moment the daemon binds.
    `stream_flushes` incs once per `doFlush()` call (covers
    periodic + first-flush + residual capture). `stream_active`
    incs in `setupStreamingWriter` and decs in the handler's
    defer after `finalFlush` — buffered-path requests never
    touch the gauge. Both are labelled by (app, plan) under
    the `__other__` pre-instantiation pattern, same shape as
    `gateway_response_bytes_total` so the §12 dashboard can
    ratio them.

## Why a single chokepoint (statusRecorder)

The statusRecorder is the only piece of code that observes every
response body byte the gateway emits. Splitting streaming
accounting into a separate writer (a `streamingWriter` struct)
would mean the recorder loses its `Bytes` counter and the metering
path has to read it back from the wrapper — a race-prone
two-writer design. The "extend statusRecorder with a flusher +
onFlush" choice keeps the byte accounting single-source: every
Write advances Bytes, every flush fires onFlush, and the residual
capture reuses the same plumbing. The downside (the recorder
gains 8 fields and 4 methods) is bounded — `installFlushHook` is
nil-safe so the buffered path stays a strict pass-through.

## Why bidi gRPC (not server-streaming only)

A streaming response is often paired with a streaming request
body — an SSE chat handler consumes a client feed and emits a
token stream. Server-streaming only would force the request
body to land BEFORE the response can start. Bidirectional lets
the bridge pipe both directions through one goroutine pair,
mirroring the legacy pattern where the bridge reads request
body and writes response body over one `ip netns exec` process.
The wire boundary to the client stays HTTP/1.1+chunked (the
gateway's `Flush()` calls translate gRPC frames to chunked HTTP
writes); only the gateway→vmmd leg is bidi gRPC.

## Risks (numbered)

- **R1. Per-flush deadline drift.** The sliding deadline
  installed via `SetWriteDeadline` re-sets on every flush, so
  the total window is `flushInterval × N` flushes — bounded by
  the bridge's `ForwardStreamResponseTimeout` (900 s) but
  unbounded on the gateway side. A client that stalls the read
  but never closes the connection holds the gateway goroutine
  until the bridge's per-read `read -t N` fires inside the
  script. Mitigation: the bridge's `exec.CommandContext`
  cancellation tears down the bridge on
  `r.Context().Done()`.
- **R2. Cap-exceeded stdlib interaction.** `http.MaxBytesWriter`
  writes a generic 502; the platform needs the 413 problem+json
  shape. The custom `capWriter` handles this in-hand but loses
  the stdlib's interaction with the `http.Server` `WriteTimeout`
  safety net. The per-flush `SetWriteDeadline` is the substitute
  — covered by the gateway-side sliding window above.
- **R3. Client disconnect mid-stream.** A streaming client that
  drops the connection mid-response must terminate the bidi
  gRPC cleanly. The forwarder's receiver loop detects the
  `Write` error on `w`, returns, and the body-copy goroutine
  exits on the next `stream.Send` failure. The bridge sees EOF
  on stdin and unwinds. No goroutine leak.
- **R4. Two-phase accounting drift.** A bug in either
  `recordEgress`'s streaming short-circuit OR `finalFlush`'s
  residual capture would under-count egress bytes → bills
  diverge from `usage_minutes.tx_bytes`. The unit test
  `TestStatusRecorder_FlushTriggers/residual-capture-finalFlush-fires`
  pins the residual capture; the e2e AC #2 (free-tier attempt
  vs Hobby attempt + per-flush tx_bytes delta) is the
  production tripwire in PR-D.
- **R5. Bridge script portability.** The streaming script's
  chunked-encoding loop and `read -t N` are bash-portable
  (busybox includes both). The Lima arm64 guest uses bash
  already (PR-A used the same script shape for the unary path);
  the reference x86_64 guest also uses bash. No new runtime
  dependency.
- **R6. PR-C vmmd config drift.** The per-node vmmd client cache
  (`pkg/gateway/forwardproxy.go::NodeClientCache`) is reused
  unchanged for the bidi RPC. The bidi client holds the same
  `*grpc.ClientConn` as the unary client; the per-call
  `ForwardHTTPStream(ctx)` dial reuses the existing
  `pkg/wire.DialContext` mTLS configuration. Zero new cert
  surface.
- **R7. AC #5 regression.** The plan-matrix quota test (3
  concurrent streamed requests → `X-RateLimit-Remaining` drops
  by exactly 3) is the tripwire that prevents a future PR from
  accidentally adding per-flush rate-limit consumption. The
  test is scheduled for PR-D — without it, a "charge per flush"
  optimization would silently break the per-request rate-limit
  contract.
- **R8. Free-tier transparent buffering.** A Free app with
  `streaming_enabled=true` (operator misconfiguration OR a
  future plan default flip) gets a buffered response with no
  streaming; the PR-A `streamingFallbackLog` surfaces this in
  the gateway slog. PR-D tightened the call site to fire only
  when `!streaming && app.Plan == api.PlanFree` — the
  pre-PR-D code fired on any Hobby+ SSE response on the
  buffered path, which was a noisy false positive (the
  operator's `FAAS_GATEWAY_STREAMING` toggle is the lever
  there, not a customer misconfig). apid's
  `CodePlanStreamingNotAllowed` 403 is the load-bearing gate;
  the log is the tripwire for the misconfig path.
- **R9. `x-faas-stream` header timing.** The header is stamped
  on the OUTBOUND request to vmmd (before the bridge is
  dialed) — that's a normal pre-body header. The CLIENT
  response does not carry `x-faas-stream` (the upstream guest
  would have to cooperate, which is not a contract). The
  gateway-side stamp is the load-bearing signal for the vmmd
  cap-lift.

## PR-D resolutions

The four PR-D-territory questions resolved in the PR-D commit:

- **O1. `gateway_stream_active` gauge.** **Shipped.** PR-D adds
  the gauge alongside `gateway_stream_flushes_total`; the
  streaming path Inc's in `setupStreamingWriter` and the
  handler's defer Dec's after `finalFlush`. The buffered path
  never touches the gauge. Pre-instantiated under
  `__other__`. Dashboard panel added to all three dashboards.
  Load-bearing for the §12 capacity-planning story and
  completes the streaming metric surface.
- **O2. `capWriter` vs `http.MaxBytesWriter`.** **Closed
  without rolling our own.** PR-D kept the existing
  `capWriter` design. The PR-B `SetWriteDeadline` sliding
  window is the substitute for stdlib's interaction with
  `http.Server.WriteTimeout`, and the structured 413
  problem+json shape is required for the customer-facing
  contract — stdlib's 502 is the wrong code. The unit
  test `TestCapWriter_EmitsStructuredProblem` already
  exercises the cap path; no edge case was surfaced during
  PR-D metal testing.
- **O3. Pro plan 100 MB cap.** **Closed.** The spec
  consistently reflects Pro = 100 MB (the same as Hobby+)
  because that's the model. The SDK reflects it via the
  `pkg/api.MaxResponseBodyBytes()` accessor. No new
  customer-facing copy needed.
- **O4. Plan matrix AC #3.** **Closed.** PR-D adds
  `cmd/e2e/streaming_metal_test.go` with the four-plan
  matrix: Free 1 MB → 413 streaming_not_available; Hobby /
  Pro / Scale 1 MB → 200. The full 100 MB stress is left to
  the reference-node metal acceptance (the per-test 1 MB payload
  exercises the apid-side gate cleanly; the metal test
  verifies the platform's wiring under real Firecracker).

## Cross-references

- ADR-009 (identical inner network world — the invariant that
  makes snapshot reuse work)
- ADR-028 (ForwardHTTP gRPC bridge — the unary path PR-C
  supersedes on the streaming side)
- ADR-046 (per-instance egress metering — the telemetry
  primitive PR-B rides on top of; the per-flush deltas
  attribute to the same per-instance, per-minute buckets)
- spec §4.1 (response body cap; the per-plan MaxResponseBodyBytes
  the capWriter enforces)
- spec §12 (Prometheus metrics; the gateway_response_bytes_total
  + new gateway_stream_flushes_total counters)
- issue #471 (this work)
- PR-A #481 (the seam — per-app flag, plan default, env
  opt-in, buffered-fallback log)
- PR-B + PR-C #490 (Flusher path + bidi RPC + per-flush
  accounting + residual capture)
- PR-D (full metal e2e, ADR-047 finalization, unary
  ForwardHTTP removal, dashboard panel updates, the
  `gateway_stream_active` gauge, the unified `reg.MustRegister`
  fix, the narrower `streamingFallbackLog` gate)

## PR-E amendment — discoverability (ADR-102)

PR-D closed the wire-correctness gaps (Flusher path, bidi RPC,
per-flush accounting, residual capture). The infrastructure
streams; the response is wire-correct; the customer-facing
discoverability is missing. Five customer-facing gaps remained:

- **G1.** No discoverability — no spec section titled "Gregale
  streams responses"; no SDK signal of streaming state.
- **G2.** Silent buffered fallback — the legacy `streamingFor`
  returned `bool`, so a customer who pays for `streaming_enabled=true`
  but sets `Accept: application/json`, runs on Free, or hits a
  request with streaming operator-disabled gets buffered behavior
  with no error.
- **G3.** Undocumented `Accept: application/json` opt-out — the
  legacy down-convert was implicit; the customer-facing docs
  never mentioned it.
- **G5.** Per-endpoint streaming cap — the REQUEST side was wired
  via ADR-091 D24 §6; the RESPONSE side at handler.go:3227 was
  not yet extended to honor endpoint rules.
- **G7.** Free + `streaming_enabled=true` falls back silently via
  `streamingFallbackLog`.

ADR-102 closes all five in one branch (mega-PR per user
direction; accepted on the basis of BLOCKER count justifying
the CI-chase risk).

### What this amendment adds

- **`Streaming-Status` response header** — unconditional stamp at
  handler.go:~4026 on every response. Closed enum (6 variants).
  Load-bearing property: the customer can self-diagnose ANY
  streaming outcome by reading the response header. Closes G1.
- **`decideStreaming` helper** — replaces the legacy `streamingFor`
  bool. Returns `(streamingDecision, isStreaming, _)` where the
  decision carries Status + Cap + CapKind. Five-conjunct
  precedence-ordered decision tree. Preserves the legacy
  `streamingFor` wrapper for `applyEdgeRuleLimit`'s bool
  signature.
- **`accept-json-downgrade` enum variant + advisory header**
  — D3 hard-flip removes the legacy down-convert; the variant
  survives one release cycle + a `Streaming-Status-Accept-Hint:
  would-buffer-pre-D3` advisory header for pinned-SDK
  customers. Both retire in ADR-102-followup ~30 days post-merge.
  Closes G3.
- **Per-endpoint RESPONSE cap** — D4 mirrors the REQUEST-side
  cap (ADR-091 D24 §6) on the response side. `CapKind="plan"`
  or `"endpoint-rule"`. Closes G5.
- **apid CreateApp 403** — D5 mirrors the existing UpdateApp gate
  at handlers_ext.go:245-252. Defense-in-depth mirror in
  `decideStreaming` (plan-disallows branch) catches any pre-D5
  row that survives. Closes G7.
- **SDK + CLI probe** — D6 `GET /v1/apps/{slug}/streaming-cap`
  + `gregale apps streaming-cap <slug>` + the Go/Node/Python
  SDK methods. IDOR-safe via loadApp.
- **CORS expose-headers** — D7 adds `Streaming-Status,
  Streaming-Status-Accept-Hint` to `corsDefaultOps` so
  uncredentialed CORS clients see the custom headers.

### What this amendment does NOT change

- The Flusher path (PR-D's load-bearing primitive) is unchanged.
- The bidi gRPC bridge is unchanged.
- The per-plan MaxResponseBodyBytes cap is unchanged.
- No new edge-rule kind; no new migration in this PR (the
  Postgres CHECK constraint ships in the follow-up per
  deferred-work).
- The `gateway_stream_active` gauge + `gateway_stream_flushes_total`
  counter are unchanged.

### Deferred work (out of scope for this PR)

- **Postgres CHECK constraint** (`apps_streaming_enabled_plan_check`)
  — shipped in `20260922131114370_apps_streaming_plan_check.sql` after
  telemetry confirmed zero Free + flag rows in production. The migration
  uses the NOT VALID + VALIDATE idiom and guards paid → Free downgrades.
- **Retirement of `accept-json-downgrade` enum variant + advisory
  header** — ~30 days post-merge. The advisory header becomes
  redundant once the variant is gone.
- **Per-endpoint response cap at apid-side probe** — the SDK
  probe returns `EffectiveCap = PlanCap` this PR; the live
  endpoint-rule override is gatewayd-side state and is not part
  of the apid cache. Future ADR may wire the apid→gatewayd-internal
  control-listener hop for live cap resolution.
