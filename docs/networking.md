# Networking

Inspect the effective network contract for an app:

```bash
gregale app APP_ID network show
gregale app APP_ID network doctor
gregale app APP_ID network attach NETWORK_ID --region REGION --cidrs CIDR[,CIDR...]
gregale app APP_ID network detach
```

`network attach` and `network detach` belong to private networks, a preview;
see [private networks](private-networks.md).

## Outbound connections

An app can open TCP connections to public hosts on ports 80 and 443. Pro and
Scale apps can add up to 8 and 32 more ports with
`gregale app egress-ports add <port>`.

Connect by hostname. Gregale's resolver allows the addresses each lookup
returns, for between 10 minutes and an hour, so an HTTP client that looks up
`api.example.com` connects normally. A connection to an IP address the app
never looked up is refused.

An egress allowlist (Hobby and up; 8, 16 or 64 entries by plan) changes both
rules: the listed ranges are reachable without a lookup, on any TCP port but
25, and every other destination is refused, hostnames included. Use it to pin
an app to the services it calls, or to reach fixed addresses, and list every
destination the app needs:

```bash
gregale app egress-allowlist add 203.0.113.10/32
```

Always refused: private ranges (`10.0.0.0/8`, `100.64.0.0/10`,
`172.16.0.0/12`, `192.168.0.0/16`), link-local and cloud metadata addresses
(`169.254.0.0/16`), port 25, and anything but TCP apart from DNS. Ports 465
and 587 reach allowlisted destinations only. DNS always goes to Gregale's
resolver, so DNS over HTTPS or TLS does not work. New connections are
rate-limited per plan.

## Public WebSockets

To serve WebSockets from your own app, enable WebSocket upgrades on it:

```bash
gregale app my-api --websocket-enabled
```

An open WebSocket counts as an in-flight request, so its instance stays
running past the idle timeout for as long as the connection is open. For long
connections with sparse messages, [managed realtime](realtime.md#long-lived-connections-and-scale-to-zero)
holds the socket instead, so the app can park between messages.

The public edge closes a WebSocket that carries no frames for about 100
seconds. Send a ping or an application heartbeat at least every 60 seconds,
and reconnect when the connection closes. A connection that carries traffic
stays open.

## Public gRPC

The Gregale gateways preserve gRPC trailers. The hosting front also needs to
support them: Cloudflare's public Tunnel hostnames do not support gRPC.
On Gregale's current Tunnel app front, successful calls can deliver messages
without the final `grpc-status`, causing standard clients to fail. Enabling
the zone gRPC toggle alone does not establish support. Ask the operator for a
verified direct-origin route before relying on public gRPC through this front.
gRPC-Web requests are not translated.

When Gregale itself refuses a gRPC call, before it reaches your app, the
client receives a gRPC status, not an HTTP error page. Examples are a missing
bearer token, a rate limit, a timed-out wake, or maintenance. `grpc-message`
carries the reason and the stable problem code, for example
`Authentication required: … (unauthorized)`.

| Refusal (HTTP) | gRPC status |
|---|---|
| 400, 422 | `INVALID_ARGUMENT` |
| 401 | `UNAUTHENTICATED` |
| 402, 412, 428 | `FAILED_PRECONDITION` |
| 403 | `PERMISSION_DENIED` |
| 404, 410 | `NOT_FOUND` |
| 405, 501 | `UNIMPLEMENTED` |
| 408, 504 | `DEADLINE_EXCEEDED` |
| 409 | `ABORTED` |
| 413, 431, 429 | `RESOURCE_EXHAUSTED` |
| 500 | `INTERNAL` |
| 502, 503 | `UNAVAILABLE` |
| anything else | `UNKNOWN` |

Your app's own gRPC responses, including their `grpc-status` trailers, pass
through the Gregale gateways unchanged; the front limitation above still
applies.

## Long-lived streams

A streamed HTTP response lasts at most about 15 minutes (900 seconds) from
the request, however active it is. This covers server-sent events, chunked
and NDJSON responses, and gRPC server, client and bidirectional streams. It
applies on every plan, to calls from the internet and between services alike.
At the limit the platform ends the stream: the client sees the response or
RPC end before your app finished it. A quiet stream can end sooner: the
public edge closes a connection that carries nothing for about 100 seconds,
so send a heartbeat (an SSE comment line, a no-op message on a gRPC stream) at least
every 60 seconds.

Write long-lived consumers to reconnect:

- **Server-sent events.** Send an `id:` with each event. A browser
  `EventSource` reconnects on its own and sends `Last-Event-ID`, so the app
  can resume from there.
- **gRPC streams.** Treat a stream that ends early as retryable: open a new
  RPC and resume from the last message the client processed, such as a watch
  cursor or offset. Keep unary RPCs for work that must finish in one call.
- **Longer sessions.** A [WebSocket](#public-websockets) stays open for up to
  24 hours while it carries traffic, and
  [managed realtime](realtime.md#long-lived-connections-and-scale-to-zero)
  holds sparse connections for you.

An open stream counts as an in-flight request, so its instance stays running
and is billed for as long as the stream is open.

## Internal-only ingress

Apps on every plan can be hidden from the public edge while remaining reachable
from authenticated same-account service calls. Set the visibility at create
time (`visibility: "internal"`) or update an existing app:

```bash
gregale app APP_ID --visibility internal
gregale app APP_ID --visibility public
```

A Compose project sets this for you: a new app for a service without `ports:`
is created internal, as Docker publishes nothing for it.
The plan shows `visibility=internal`. The choice is made once, when the app is
created; later syncs keep whatever visibility the app has, so publish such a
service with `--visibility public`. PR previews inherit their app's visibility.

Internal apps do not receive a public platform-subdomain or verified custom
domain route. Service discovery continues to resolve them through
`APP_SLUG.svc.gregale:10081`, where the service proxy enforces caller identity
and same-account authorization. Visibility changes are audited and invalidate
the gateway route cache.

Internal services scale to zero like any other app. A service call to a parked
target is held at the node-local proxy while the snapshot is restored, then
forwarded — the same wake-blocking contract the public edge offers.
The restore is coalesced with any concurrent public request for that app, so a
burst of internal callers costs one restore rather than one per caller. Set
client timeouts above the platform wake budget plus your own handler time, and
note that a fully cold chain (`public-api` → `auth` → `billing`) pays each
restore in sequence. `min_instances` remains available to trade resident RAM
for first-call latency, but it is no longer required for an internal
dependency to be reachable.

When a wake cannot produce a replica, the proxy answers `503`. A saturated
wake queue carries `Retry-After`; a target at its plan concurrency ceiling
reports `service has no healthy replicas`. Internal wakes appear in the wake
timeline with trigger `service.mesh`, distinct from public `gateway` traffic.

Internal calls honour the target's wire protocol. An app configured
`app_protocol: grpc` or `http2` is reached over the H2C guest bridge, and the
node-local listener accepts H2C prior knowledge, so a workload can use an
ordinary gRPC client against `http://APP_SLUG.svc.gregale:10081`. Response
trailers — including `grpc-status` — are preserved across the hop.
`Connection: Upgrade` requests (WebSocket and friends) take the verbatim-bytes
bridge and are neither buffered nor retried; they require the target app to
have WebSockets enabled and return `501` otherwise. Non-HTTP protocols use
private TCP addresses, described below.

## Private TCP between services

Private TCP is not enabled for every account yet. Until it is, `*.svc.gregale` answers with the
HTTP-only address and only HTTP calls on port 10081 reach a service; raw TCP
connections to other ports are refused. Contact support before you rely on
it, and keep a datastore your services share outside Gregale or behind an HTTP
API until it is on.

Where it is on, same-account services can also talk over plain TCP, which
covers protocols such as PostgreSQL, Redis, MQTT, AMQP and custom binary
protocols. Dial the service name on its own port:

```text
redis://cache.svc.gregale:6379
postgres://db.svc.gregale:5432
```

`cache.svc.gregale` resolves to a stable private address for the `cache` app,
for example `198.19.0.7`. The address belongs to the app, not to a machine,
so it stays the same across restarts, scale-to-zero and moves between
nodes. It is meaningful only inside your account; another account's
workloads cannot use it to reach your services. HTTP calls to
`http://cache.svc.gregale:10081` keep working on the same name.

A service exposes these TCP ports:

- every TCP port it declares in `ports`, including internal ones (see below);
- the port it serves on (its main listener).

Image `EXPOSE` entries other than the serving port are not exposed
automatically: declare them. Ports 10080, 10081 and 443 on a service address
always belong to the HTTP service mesh.

Declare listeners that should never be public with compose `expose:`:

```yaml
services:
  api:
    build: ./api
    depends_on: [cache]
  cache:
    build: ./cache
    expose: ["6379"]
```

`expose:` entries become `internal` listeners. Same-account services reach
them at the service address, but they never get a public
`--port-<name>` hostname or a public raw TCP listener. Only TCP entries are
supported; UDP and port ranges are skipped with a warning. Through the app
API, set `"internal": true` on an entry in `ports`.

An app deployed without Compose declares its ports with the CLI. Each entry is
`[NAME=]tcp|udp:PORT`, with `:internal` for a listener that stays private; an
entry without a name is named `tcp-<port>` or `udp-<port>`. The list replaces
the app's declared ports, and `--ports none` clears them:

```bash
gregale app my-api --ports tcp:7000,udp:5353,cache=tcp:6379:internal
```

A raw TCP listener (`gregale app listener create`) and a UDP listener
(`gregale app listener create my-api --protocol udp`) need a declared port with the same name and
guest port.

Each declared dependency also receives `GREGALE_SERVICE_<NAME>_HOST`, the bare
host for non-HTTP clients:

```bash
redis-cli -h "$GREGALE_SERVICE_CACHE_HOST" -p 6379 PING
```

The same caller policy as HTTP service calls applies when the connection
opens: same account, the caller's `depends_on` (under the `declared` policy,
and for targets outside the caller's project under `project`), the target's
allowed callers, and preview scoping. A target that grants a
caller only method/path scopes (`x-gregale-allow-call-scopes`) refuses that
caller's raw TCP connections, because those scopes cannot be enforced on raw
bytes.

A parked service is woken when a connection arrives. The connection is held
for up to 30 seconds, then forwarded. While bytes keep flowing, the target
stays running. A connection that is idle for longer than the target's idle
timeout is closed, so an idle connection pool does not keep a service awake
longer than idle HTTP traffic would. Configure your client pool to reconnect.
Concurrent private TCP connections are capped per account on each node, by
plan.

A connection that is refused (unknown service, undeclared port, denied
caller, or a target that cannot be woken) is reset with no data, since raw
TCP carries no error message. Raw TCP connections carry no caller-assertion
header; authenticate inside your protocol if the target must know the
caller.

Private TCP addresses are a node-level rollout. Until your node has it
enabled, `*.svc.gregale` keeps answering with the HTTP-only address, and HTTP
service calls work as before.

gRPC between services does not need private TCP: it runs over the HTTP
service mesh. Mark a gRPC service with `x-gregale-app-protocol: grpc` in
Compose (or `gregale app update <app> --app-protocol grpc`) so the platform
reaches it over HTTP/2.

## More on this topic

- [Internal services](internal-services.md) — Call other apps by name, set deadlines and call scopes, and verify the caller.
- [Service bindings](service-bindings.md) — The binding inventory, readiness checks in CI, canaries and release enforcement.
- [Binding verification runs](binding-verification-runs.md) — Durable, resumable verification runs for one app or a whole project.
- [Service release graphs](service-release-graphs.md) — Keep callers and services on consistent revisions; qualify, activate and recover graphs.

## Where sections moved

These sections now have their own pages.

### Internal services

Moved to [Internal services](internal-services.md).
