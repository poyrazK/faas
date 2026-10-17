# ADR-1580: Zero-downtime daemon restarts

Date: 2026-10-17
Status: Accepted
Related: ADR-068 (issue #607, apid and gatewayd-public socket activation),
ADR-015, ADR-070, ADR-079, ADR-190, ADR-790

## Context

Every deploy must be invisible to customers, and per-unit releases restart
single daemons often. systemd restarts a service by stopping it and then
starting it, so a restart is a window in which no process serves. ADR-068
closed that window for apid and gatewayd-public with socket units. The other
daemons still refused connections, or failed requests, while they restarted:

- gatewayd-internal bound its unix socket itself, and Go unlinks a socket
  path when its listener closes. gatewayd-public's connections failed for the
  whole restart. Compute nodes' data listener (`FAAS_GATEWAY_LISTEN`) had the
  same gap.
- A stopping gateway waited up to its 25 s grace for long-lived SSE and
  WebSocket sessions, and connections queued for the replacement meanwhile.
  gatewayd-internal also stopped its listeners one at a time, and its egress
  gRPC server spent another 5 s on meterd's open stream.
- gatewayd-public reaches gatewayd-internal over pooled H2C. A request sent
  just as gatewayd-internal's graceful GOAWAY arrived was never processed, but
  the transport could not resend it: request bodies were not rewindable, and
  even GETs carried a wrapped `http.NoBody`. They became 502s.
- Wake RPCs to schedd failed fast with Unavailable while schedd restarted, and
  gRPC's default reconnect backoff grows to two minutes. Compute nodes' schedd
  listens on TCP, so a socket unit does not cover it. schedd's GracefulStop
  also always spent its full 5 s on gateways' warm-hint and log streams and
  vmmd's capacity stream.
- realtimed's socket had the same restart gap as gatewayd-internal's.
- outboundd's and bridged's `run` returned as soon as `Shutdown` began, and the
  process exited under every in-flight call.
- net/http drops a request it reads after `Shutdown` began: a connection
  accepted just before the listener closed, whose request was still arriving,
  is closed unanswered and the client sees EOF. A restart-under-load test lost
  requests this way in about 40% of runs. `Shutdown` also waits up to 5 s for
  a connection that never sent anything, such as a proxy's preconnect.

## Decision

1. **Shared socket inheritance.** `pkg/wire` reads the systemd activation
   environment once per process and lets a listener claim an inherited socket
   by address (unix path, or TCP address with apid's wildcard rule).
   `ListenOrRecreate`, `ListenAs`, `ListenInherited` and the gateway's synth
   server use it before binding. An inherited unix socket that grants access
   to other users is refused, because socket permissions are the
   authentication (ADR-015). A daemon calls `RequireInheritedListenersClaimed`
   after binding and refuses to start when its unit passes an address it does
   not serve, since such a socket would accept connections nobody reads.
2. **Opt-in socket units** for gatewayd-internal (unix socket, plus the
   compute data listener rendered from `faas_gateway_listen`), realtimed, and
   the control plane's schedd. The static service units do not reference them;
   a role drop-in adds `Wants=` and `After=` only where a socket is configured,
   so a rollback that removes a socket unit still starts its service.
   - **schedd**: `faas_schedd_listen_addr`, which the split-box manifest sets
     from `daemons.schedd.bind` (the same bind the renderer writes to
     `schedd.toml`), renders both schedd's `FAAS_SCHEDD_LISTEN_ADDR` and the
     socket's `ListenStream`. The two cannot drift; empty leaves the socket off.
     On the production control plane this is `tcp://0.0.0.0:9091`.
   - **compute**: `faas_compute_durable_listeners`, off by default, until
     node_join stops and starts these sockets with their services.
   The roles never start a socket themselves. A daemon that bound the address
   itself unlinks a unix socket path when it exits, so a handler stops the
   service and its socket and starts the service, which brings the socket up
   first. deployctl retires the schedd socket on rollback to an older bundle,
   as it does for apid's.
3. **Fast, bounded drains.** `drain.Tracker.BeginShutdown` runs first. SSE
   responses then end cleanly (EventSource reconnects with `Last-Event-ID`) and
   upgraded WebSocket sessions close, in both gateways. Other requests keep the
   25 s grace. gatewayd-internal stops all listeners at once.
   `wire.StreamDrain` ends a gRPC server's open streams with Unavailable, the
   status their clients treat as reconnect, so GracefulStop waits only for
   unary RPCs; schedd and gatewayd-internal's egress server use it.
   `wire.HTTPDrain` stops an HTTP server in two steps: close its listeners,
   wait until every accepted connection delivered its first request (closing
   those still silent after 1 s), then `Shutdown`. apid, both gateways'
   listeners, outboundd and bridged use it.
4. **Replayable internal hop.** gatewayd-public buffers request bodies up to
   `api.InternalHopReplayableBodyBytes` (64 KiB) and sends empty bodies as nil,
   so the H2C transport resends a request that crossed the GOAWAY. Streams
   above the GOAWAY's last stream ID were never processed, so this cannot
   duplicate work.
5. **Wakes wait for schedd.** Wake, admit, ensure-wake, mirror admission and
   activity RPCs wait for the connection to be ready within their deadline,
   and schedd connections retry at most a second apart. The gateway's 30 s
   wake hold (`WakeQueueTTLSeconds`) then covers a schedd restart.
6. **outboundd and bridged drain before exiting** (`wire.ServeUntilShutdown`),
   all servers together, within 25 s and 5 s.

## Consequences

- A restart costs callers latency, not errors: new connections wait for the
  old process's drain plus the replacement's start-up. Most drains are now
  milliseconds.
- With a durable data listener, a restarting compute node no longer refuses
  dials, so the control plane's compute pool cannot fail over on a dial error.
  The pool instead probes each node's gatewayd-internal every second and skips
  one that has not answered within `api.ComputeGatewayRestartWaitSeconds`
  (3 s, `FAAS_COMPUTE_GATEWAY_RESTART_WAIT`): new requests wait for a
  restarting node at most that long before failing over. Requests already
  queued on it wait for the restart. When no node answers, requests still go
  to one and wait.
- A request still running when the 25 s grace ends is cut, because systemd
  cannot start the replacement before the old process exits.
- HTTP/1.1 has no GOAWAY: a request a client sends on an idle keep-alive
  connection just as the server closes it can still fail. Go clients resend
  idempotent requests; others see an error. apid's SSE operation streams still
  hold its 10 s drain.
- A request body larger than 64 KiB, or chunked, that crosses the GOAWAY
  window (microseconds) can still fail. Bodies up to 64 KiB are read before
  forwarding.
- A connection to a stopped service's socket starts the service again. To keep
  a daemon stopped, stop its socket too. node_join must stop and restart the
  compute sockets with their services before `faas_compute_durable_listeners`
  is turned on.
- Changing a socket's `ListenStream=` restarts the socket and its service; that
  restart refuses connections briefly.
- A schedd restart longer than the wake hold, for example one whose loop
  drain takes its full 30 s worst case, still fails the waiting wakes.
- Not socket-activated: compute nodes' schedd TCP listener (covered by
  WaitForReady for wakes only), outboundd's relay and API listeners (the code
  accepts inherited sockets, but their addresses are operator configuration),
  gatewayd-public's private listener for compute edges, and gatewayd-internal's
  guest service proxy and DNS listeners. Guests see those restarts.
- builderd requeues a running build at shutdown; it restarts from the
  beginning on the new process. imaged replays deploy handoffs from its
  durable outbox, but its start-up can take up to 20 minutes.

## Alternatives

- **Two instances sharing the socket** (`faas-gatewayd-internal@{a,b}`, both
  with `Sockets=`): start the new instance, then stop the old one. This is the
  only design with no restart window at all. It needs gatewayd-internal's
  spools, sessions, control port and guest DNS listener to tolerate two
  processes; deferred to its own ADR.
- **systemd file descriptor store** (`FileDescriptorStorePreserve=`, systemd
  254+): no socket units and no traffic activation, but it depends on host
  systemd versions we do not pin.
- **Self re-exec with an fd hand-off** (tableflip): needs reload-based deploys
  instead of the restarts the release pipeline uses.
