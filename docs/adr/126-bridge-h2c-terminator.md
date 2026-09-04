# ADR-126 · Bridge-side H2C terminator: end-to-end H2/gRPC inside the guest

- **Status:** proposed
- **Date:** 2026-08-23
- **Issue / PR:** G19 (per spec §17) — close the customer→guest framing gap left open by PR #1023 (ADR-124).
- **Related:** ADR-124 (per-app `app_protocol` knob), ADR-079 (gatewayd-internal
  H2C + raw-bytes bridge), ADR-080 (raw-bytes bridge for Upgrade traffic),
  ADR-005 (snapshots are cache, not truth), ADR-016 (additive wire changes),
  ADR-028 (vmmd-stream-bridge inner-leg amendment, no-deploy rollback story),
  PR #750 (issue #686 — bridge cutover to H2C v2).

## Context

PR #1023 (ADR-124) shipped the **customer knob** — every Gregale app now carries a
closed-set `apps.app_protocol ∈ {http1, http2, grpc}` field with default `http1`
(per spec §4.1 / docs/faas_implementation_spec.md:115). `gatewayd-internal` stamps
`x-faas-protocol: <http1|http2|grpc>` on every inbound request at the site
`x-faas-stream` and `x-faas-upgrade` are stamped today (`pkg/gateway/handler.go:5164-5188`
streaming, `:5307-5320` buffered), and `pkg/gateway/forwardproxy.go::fwdStreamOnceWithEvents`
emits a `slog.Debug` framing-selection line so operators with debug logging on can
correlate per-app protocol choice with bridge-side framing behaviour.

But the framing the **guest** actually receives is still HTTP/1.1 + chunked,
regardless of the customer's choice. A customer running `app_protocol=grpc` gets
their gRPC trailers back through the edge as plain `Trailer:` headers over H1+chunked;
the in-guest server only sees H1. The customer→edge parity PR-A ships is **not**
customer→guest parity.

### Why today is H1+chunked on the guest side

`cmd/vmmd-stream-bridge` (issue #686 / PR #750, 2026-08-08) speaks **H2C** on the
vmmd side via Go 1.24+ stdlib `srv.Protocols.SetUnencryptedHTTP2(true)`
(`cmd/vmmd-stream-bridge/main.go:159-160`), but **re-frames to H1+chunked** before
talking to the guest at `10.0.0.2:<port>`. The H1+chunked shape is the legacy
from the v1 shell bridge (`pkg/vmmdgrpc/forward.go:1115::buildStreamingBridgeScript`)
and was kept on purpose in PR #750 so guest snapshots stayed valid across the
cutover. The initial v2 path deliberately opened a fresh guest TCP dial per H2C
stream (`main.go:205-210` comment: "one H2C request = one guest dial") and wrote the
HTTP/1.1 request line + headers + `Transfer-Encoding: chunked` (`writeH1RequestHead`,
`writeChunkedBody`). The bridge's framing decision is the load-bearing framing
transition in the chain — not the gateway, not guest-init, not the runner.

### What "gRPC trailers inside the guest" actually requires

HTTP/2 native trailer semantics (RFC 9113 §8.1) carry trailers as a HEADERS frame
with `END_STREAM`, the same frame type as initial response headers. The trailer
fields (`grpc-status: 0`, `grpc-message: ""`, custom metadata) appear AFTER all
DATA frames on the response stream. The contrast with HTTP/1.1 trailers:

| | HTTP/1.1 + chunked | HTTP/2 |
|---|---|---|
| Initial response headers | Headers block | HEADERS frame |
| Body | Chunked DATA | DATA frames |
| Trailers | `Trailer:` headers in a separate block after chunked terminator | HEADERS frame with END_STREAM |
| Required by spec | Optional (Hop-by-hop "Trailer" hint) | Native |
| Where the customer sees it | `Trailer:` response headers (parse after body) | gRPC client reads them automatically |

If the bridge re-frames to H1+chunked, the customer's gRPC client **never sees
HTTP/2 trailers** — even when the customer's app runs a gRPC server. The bridge's
current contract (`main.go:307-313`) mirrors guest headers verbatim with no
trailer split: no `w.Header().Set("Trailer", ...)` and no `http.TrailerPrefix`
handling. Trailer-only headers from the guest are silently merged into the
regular header block — `grpc-status: 0` arrives as a top-of-response header, not
a trailer, breaking the gRPC client's trailer-aware handshake.

### Cloud Run / Kubernetes benchmark

Customers migrating gRPC services from Cloud Run expect a `service.protocol`
field that selects the wire protocol their end-customer sees. Cloud Run ships
this as `run-service-protocol ∈ {http1, http2}` (no `grpc` value because ALPN
h2 covers gRPC). Kubernetes' `Ingress` class exposes `h2` as an annotation.
Gregale has the customer knob (`apps.app_protocol`) but the wire shape stops
short — the platform can't actually deliver `app_protocol=grpc` semantics
because the bridge downgrades H2 → H1 inside the VM.

### Spec §17 G19

The spec explicitly files this as a multi-week follow-on ADR (docs/faas_implementation_spec.md:115,
"filed in §17 G19 as a multi-week follow-on ADR (bridge-side termination on the
guest)"). The §17 G-register table currently stops at G17 with no G18/G19 row
filed — G18 (the customer knob, ADR-124) and G19 (this ADR) both live in the §4.1
narrative paragraph as prose references only.

## Decision

### 1. Per-stream framing switch on `cmd/vmmd-stream-bridge`

The bridge gets **two framing paths** selected per inbound request by a new
`FAAS_BRIDGE_PROTOCOL ∈ {h1, h2c}` env var wired from the vmmd-side init frame:

| Inbound (vmmd side) | Outbound (guest side) | When |
|---|---|---|
| H2C (today's contract verbatim) | H2C over `10.0.0.2:<port>` via prior-knowledge (no Upgrade dance) | `FAAS_BRIDGE_PROTOCOL=h2c` |
| H2C (today's contract verbatim) | H1+chunked (today's `writeH1RequestHead` + `writeChunkedBody` verbatim) | `FAAS_BRIDGE_PROTOCOL=h1` |

The bridge's existing H2C inbound contract is unchanged — the vmmd-side
`newStreamBridgeH2CTransport` (`pkg/vmmdgrpc/forward.go:1807`) is the canonical
H2C client transport and stays as-is. The HTTP/1.1 outbound path is the unchanged
`writeH1RequestHead` + `writeChunkedBody` code. The **new** path is a per-stream
H2C terminator that originates HTTP/2 frames to the guest.

The framing decision is **per-stream** (per inbound H2C request), not per-bridge-
process. Each `ForwardHTTPStream` invocation reads its own `FAAS_BRIDGE_PROTOCOL`
env var (mirroring `currentStreamBridgeVersion()` at `pkg/vmmdgrpc/forward.go:565-570`),
so two streams for the same app can race different protocols only if the operator
mutates the env var mid-flight — which is the rollback story below.

### 2. HPACK state ownership delegated to `golang.org/x/net/http2`

The H2C terminator is a per-stream `io.Reader` / `io.Writer` pair against a
transport-backed conn. HPACK state is owned by `golang.org/x/net/http2`'s
standard transport, NOT hand-rolled. The bridge does not maintain its own HPACK
header table — it owns frame ordering and stream multiplexing, not state
compression. This mirrors the canonical `golang.org/x/net/http2.Transport{
AllowHTTP: true, DialTLSContext: …, IdleConnTimeout: 5 * time.Minute, ReadIdleTimeout:
30 * time.Second, PingTimeout: 15 * time.Second }` shape used by the vmmd-side
H2C transport (`pkg/vmmdgrpc/forward.go:1807`).

### 3. Prior-knowledge H2C on the guest side (no Upgrade dance)

The H2C terminator sends the HTTP/2 connection preface (`PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n`),
the client's initial SETTINGS frame, and waits for the guest's SETTINGS + SETTINGS ACK
— then opens a new H2 stream and sends HEADERS + DATA frames mirroring the inbound
request. We deliberately skip the HTTP/1.1 → H2 Upgrade dance:

1. **One less round-trip per request.** Prior-knowledge skips the
   `Connection: Upgrade` / `Upgrade: h2c` / `HTTP/1.1 101 Switching Protocols`
   triplet. For long-lived gRPC streaming RPCs this is negligible; for
   unary RPCs and short server-streaming it shaves 0.5–2ms off TTFB.
2. **Removes an interop foot-gun.** A guest whose `:8080` server doesn't
   speak Upgrade (the v1 behavior on every Python / Node / older Go runner)
   would silently fall through to H1+chunked on the v2 wire. Prior-knowledge
   fails fast if the guest's listener doesn't speak H2C — the operator gets
   a clear "guest doesn't advertise H2C" diagnostic rather than a confusing
   "works for http1, hangs for http2" surprise.
3. **The guest's `:8080` listener opts into H2C explicitly** via stdlib
   `srv.Protocols.SetUnencryptedHTTP2(true)` (Go 1.24+ — same pattern as
   `cmd/vmmd-stream-bridge/main.go:159-160` and `pkg/gateway/synth.go:167-169`).
   If the guest listener is Go 1.24+ stdlib `net/http` or any modern HTTP/2
   stack (Envoy, nginx 1.25.1+, hypercorn, caddy), the bridge's prior-knowledge
   framing just works.

### 4. Stream multiplexing is one request stream per pooled guest transport

The initial cutover used one guest TCP connection per H2C stream to keep the
wire-shape change small. The follow-up connection-pooling implementation
supersedes that lifecycle choice for the persistent bridge: each per-instance
bridge owns a bounded transport entry per guest port, and
`golang.org/x/net/http2.Transport` multiplexes independent request streams on
reusable guest connections. Request contexts and bodies remain per-stream, so
cancellation cannot cancel a sibling request and HPACK state remains owned by
the transport. HTTP/1.1 requests use the corresponding keep-alive transport;
Upgrade traffic stays on the raw-bytes bridge.

The pool closes idle connections on bridge shutdown and bounds distinct port
entries to prevent future request metadata from retaining an unbounded number
of transports. If production measurements show that a guest's HTTP/2 stream
cap causes queueing, a later change can add an explicit bounded
multi-connection policy; this follow-up keeps the existing
`StrictMaxConcurrentStreams` contract and does not silently create unbounded
guest sockets.

### 5. Trailer framing for `grpc` is HTTP/2 native

The H2C terminator is a **frame-forwarder** — no application-level translation
between H1 chunked trailers and H2 trailer HEADERS frames. The bridge reads
frames off the guest conn and re-emits them as H2C frames to the inbound H2C
stream. For `grpc`, the trailer framing (HTTP/2 HEADERS frame with END_STREAM
carrying `grpc-status: 0`, `grpc-message: ""`, custom metadata) is preserved
1:1 by construction. The bridge does NOT translate trailers to `Trailer:`
headers — that translation is the bug we are fixing, not a feature.

The `vmmdpb.ForwardHTTPResponseInit` proto gains an additive `Trailers
[]*Header` field (per ADR-016) so the gateway forwarder
(`pkg/gateway/forwardproxy.go:464-468`) and the bridge's H2C terminator can
keep trailers distinct from initial headers. The legacy `Headers` field
preserves the H1+chunked path verbatim — backward compatibility for the
`FAAS_BRIDGE_PROTOCOL=h1` rollback and for `apps.app_protocol=http1` apps.

### 6. Snapshot invalidation is contained to the opt-in slice

Every app that opts into `apps.app_protocol ∈ {http2, grpc}` must adopt the
new h2c-capable guest base image (which enables
`srv.Protocols.SetUnencryptedHTTP2(true)` on the customer's `:8080` listener
when the runtime supports it — Go 1.24+ runners yes; older Python / Node
runners fall back to the H1+chunked path regardless of `app_protocol`).
**`apps.app_protocol=http1` snapshots stay valid forever** — they ride the
unchanged H1+chunked bridge path and don't need the new base image. This
limits the cold-boot spike to the opt-in slice and protects every existing
customer who hasn't explicitly opted in.

Operators monitor the rebuild via:

- `snapshot_fleet_avg_mb` (target 130 MB, alert 160 MB per
  `pkg/api/limits.go::snapshot_fleet_avg_mb`) — the per-snapshot average
  size alert.
- `snapshots.fc_version` distribution — the load-bearing
  `MarkAllSnapshotsStaleByFCVersion` path at
  `pkg/state/pgstore.go:9720-9728` marks every snapshot with a stale
  `fc_version` as `stale=true` on imaged restart; the lazy re-snapshot path
  in vmmd (`pkg/fcvm/snapshot.go::LazyResnapshot`) rebuilds transparently.

The `FAAS_BASE_IMAGE_VERSION` constant in `pkg/fcvm` bumps from its current
value to `v2` as part of this PR. The base image bump invalidates every
snapshot whose `fc_version` matches the old base image per ADR-005 (snapshots
are cache, not truth).

### 7. Rollback story — two switches

Operators have **two independent rollback switches** for the bridge-side
framing path:

1. **`FAAS_BRIDGE_PROTOCOL=h1`** on the bridge process — surgical rollback
   to the H1+chunked path for that single vmmd process. Per-request
   env-var lookup (mirrors `currentStreamBridgeVersion()` at
   `pkg/vmmdgrpc/forward.go:565-570`) so the flip takes effect on the next
   inbound H2C stream without restarting the bridge. Used when the H2C
   terminator is broken on a single instance but the legacy path is sound.
2. **`FAAS_STREAM_BRIDGE_VERSION=v1`** on vmmd — wholesale shell-bridge
   fallback (the pre-existing ADR-028 amendment rollback). vmmd stops
   spawning the H2C binary entirely and falls back to
   `buildStreamingBridgeScript` (the HTTP/1.1 hard-coded shell bridge at
   `pkg/vmmdgrpc/forward.go:1115`). This is the load-bearing disaster
   fallback for "the bridge binary is fundamentally broken." Per-request
   lookup pattern, no restart of unrelated processes.

Both switches are env-var-only with no restart. The surgical switch is the
load-bearing rollback for H2C terminator bugs; the wholesale switch is the
load-bearing rollback for bridge-binary regressions.

### 8. Wire-additive proto bump

The proto change is additive per ADR-016:

- `vmmdpb.ForwardHTTPRequestInit.app_protocol = 7` (string) — closed-set
  `{http1, http2, grpc}` validated upstream by `apid` at
  `pkg/api/limits.go::AppProtocol` + the column-level CHECK constraint
  `apps_app_protocol_chk` (`migrations/00382_apps_app_protocol.sql`).
  Defaults to `http1` for legacy callers (zero behavior change).
- `vmmdpb.ForwardHTTPResponseInit.trailers = 7` (repeated Header) — new
  field for HTTP/2 trailer HEADERS frames carrying gRPC trailers. Legacy
  callers (forwarders that don't populate `Trailers`) get an empty slice;
  the `Headers` field preserves the H1+chunked path verbatim.

Both bumps are wire-additive. Pre-ADR-126 vmmd / schedd / gatewayd
binaries observe byte-identical behavior: the new fields default to
zero-value (empty string / empty slice) which maps to "use H1+chunked,
no trailers split."

### 9. Operator monitoring

The framing decision surfaces in two places:

- `slog.Debug` per-request line: `"bridge framing selection"` with
  `app_protocol`, `bridge_protocol`, `framing`, `latency_first_byte_us`.
  The existing framing-selection observability line at
  `pkg/gateway/forwardproxy.go:300-326` reads `x-faas-protocol` and
  `slog.Debug`s the choice; this ADR extends that line with
  `bridge_protocol=h1|h2c` so operators correlate customer-knob choice
  with bridge-side framing.
- `gateway_wake_latency_seconds` histogram (already in spec §12) — the
  TTFB for `app_protocol=http2|grpc` paths should drop vs `http1`
  (prior-knowledge saves a round-trip) once H2C terminator is active.
  Stays the same metric name; the histogram buckets are wide enough.

## Consequences

- **Customer knob parity.** `apps.app_protocol=grpc` apps now receive gRPC
  trailers as HTTP/2 native trailers end-to-end. The customer→guest parity
  gap closes.
- **Per-app opt-in isolates the blast radius.** Existing
  `apps.app_protocol=http1` apps (the platform default today and every
  pre-ADR-124 app) ride the unchanged H1+chunked path. Zero behavior change
  for them.
- **Snapshot invalidation is opt-in scoped.** Only apps that opt into
  `app_protocol ∈ {http2, grpc}` see a cold-boot spike while their snapshots
  rebuild. Hobby tier rebuilds first (lowest blast radius), then Pro, then
  Scale (per the rollout runbook).
- **Bridge code gets bigger.** `cmd/vmmd-stream-bridge/main.go` grows from
  ~520 lines to ~900 lines with two helper files (`framing.go` ~80 lines for
  the CR/LF sanitize shared between paths; `h2c_terminator.go` ~280 lines
  for the H2C frame parser + per-stream I/O). The H1+chunked path stays
  verbatim — only the H2C path is new.
- **Two rollback switches** are env-var-only with no restart — surgical
  (`FAAS_BRIDGE_PROTOCOL=h1`) and wholesale (`FAAS_STREAM_BRIDGE_VERSION=v1`).
- **Wire-additive proto bumps** — no breaking vmmd↔schedd upgrade order per
  ADR-016.
- **Spec §17 G19 closes.** The §17 register gains a new entry:
  `G19 RESOLVED (ADR-126)`. The §4.1 line 115 paragraph rewrites to
  describe the shipped state.

## Rejected alternatives

- **Universal H2C terminator (always-on, not per-app).** Rejected because
  it would invalidate every existing customer snapshot, not just the
  opt-in slice. The §6.2 invariant "two instances restored from one snapshot
  never share IP, netns, jail uid, or RNG stream" is preserved either way,
  but the operational cost of invalidating the entire fleet for an
  opt-in feature is the wrong trade.
- **H2C terminator in guest-init instead of the bridge.** Rejected because
  it adds a process inside the guest (guest-init becomes a stateful H2
  terminator) and shifts the framing ownership into a binary that's
  already load-bearing for the boot sequence. The bridge is the single hop
  where the framing transition happens today; owning the H2C terminator
  there keeps the vmmd-side and gateway-side contracts unchanged.
- **Bridge reuses a single guest TCP conn across N H2C streams
  (multiplexing).** Rejected because connection migration (HTTP/2 RFC
  8441), HPACK state ownership across callers, and snapshot invalidation
  all get cheaper when the bridge owns the conn per stream. A future ADR
  can multiplex if profiling shows dial latency is the dominant cost.
- **H2 Upgrade dance (not prior-knowledge).** Rejected because (a) it
  adds a round-trip, (b) it silently fails on every Python / Node runner
  whose stdlib HTTP server doesn't speak Upgrade, and (c) prior-knowledge
  is a cleaner "guest is H2C-capable or the wire fails fast" contract.
- **Hand-rolled HPACK state.** Rejected because HPACK is one of the most
  security-critical wire-level pieces of HTTP/2 (integer overflow, header
  table size, dynamic table eviction under Doce). `golang.org/x/net/http2`
  is the canonical implementation; we don't roll our own.
- **gRPC trailer translation at the bridge (H2 trailers → H1 `Trailer:`
  headers).** Rejected because it's the bug we are fixing, not a feature.
  The translation breaks trailer-aware gRPC clients and produces confusing
  cross-language interop failures.

## Out of scope (filed as future ADRs)

- **Per-app ALPN preference at the cert engine.** `gatewayd-public` ALPN h2
  stays universal (Caddy + Cloudflare upstream per ADR-070 revision). Per-app
  ALPN is a customer-runtime concern (their cert choice), not a platform
  knob.
- **One-bridge-stream-per-guest-conn → conn-multiplexed.** Today's shape
  is one dial per H2C stream. A future ADR might multiplex across N
  streams on a single guest conn if dial latency becomes the bottleneck.
- **Guest-side HPACK header table tuning.** Defaults via
  `golang.org/x/net/http2.Transport` are good enough; profile-driven
  tuning is a separate concern.
- **gRPC-web support.** gRPC-web is HTTP/1.1 + trailers; the bridge's
  H1 fallback already handles it. A future ADR might add per-app
  `app_protocol=grpc-web` if customer demand materializes.
- **HTTP/2 connection migration (RFC 8441).** Connection migration would
  enable cross-host failover of long-lived streaming RPCs. Out of scope
  until we measure cross-host failover latency as a customer pain point.
- **gRPC trailers on the vmmd-side wire.** This ADR adds the `Trailers`
  field to `ForwardHTTPResponseInit`; a follow-on ADR could split trailers
  on the wire so vmmd-side observability sees gRPC status codes
  distinctly from initial headers.
