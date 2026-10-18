# ADR-20261018072701: Zero-downtime daemon restarts

Date: 2026-10-17 (two-generation hand-off added 2026-10-18)
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
7. **Two-generation hand-off on reload** for gatewayd-public (and its compute
   edge), gatewayd-internal, apid and realtimed. A restart can only stop the
   old process before starting the new one, so a request or stream longer
   than the 25 s grace is cut. A reload starts the new process first, on the
   same sockets, and lets the old one finish. See "Two generations" below.

## Two generations

### Sequence

`systemctl reload faas-<daemon>` runs `ExecReload=/bin/kill -USR2 $MAINPID`.
The daemon (`wire.HandoffRequests`, `wire.Handoff`):

1. Waits while its cgroup's `memory.current` exceeds
   `api.HandoffMaxMemoryPercent` (45 %) of `memory.max`: both generations
   share the unit's `MemoryMax`. It returns freed heap to the OS and checks
   again every 2 s for up to `api.HandoffMemoryWaitSeconds` (60 s), then
   refuses, reports `STATUS=hand-off failed at …` and keeps serving.
2. Duplicates every listening socket it serves (inherited from a socket unit
   or bound itself and registered with `wire.Share`) with `F_DUPFD_CLOEXEC`,
   keeping the shared open file description non-blocking, and marks unix
   listeners not to unlink their path on close.
3. Re-executes its `ExecStart` path, which `/opt/faas/current` now resolves to
   the new release, with those sockets from fd 3 (`FAAS_HANDOFF_FDS`), a
   readiness pipe (`FAAS_HANDOFF_READY_FD`) and its own PID
   (`FAAS_HANDOFF_PARENT`). The successor inherits the environment, minus
   systemd's activation and watchdog PID variables, and the unit's
   credentials directory. It is started through a short-lived intermediate
   copy of the binary (`FAAS_HANDOFF_SPAWN`), which starts the successor,
   reports its PID on the pipe and exits. The orphaned successor is
   re-parented to PID 1, so it is systemd's child before it becomes the main
   process. systemd treats a main process it did not start as alien: on
   stop it sends SIGTERM and SIGKILL at once instead of waiting for the
   drain, and it cannot learn the exit status that `Restart=` needs. A
   native run on systemd 255 showed the first: a restart after a hand-off
   cut 35 s requests 5 s in and reported the stop done in 162 ms.
4. The successor claims the inherited sockets by address, as it would a
   socket unit's. Its listeners accept nothing until it is ready
   (`daemonunit.HandoffReady`): meanwhile the old generation takes every
   connection from the shared kernel queue, so none waits on a process that
   cannot answer. Once ready it writes to the pipe; sockets the new release no
   longer serves are closed then.
5. The old generation sends `MAINPID=<successor>` and confirms through
   `systemctl show` that systemd adopted it (`NotifyAccess=main` lets only the
   main process do this). From then on systemd tracks the successor: its
   watchdog pings, its exit, its `Restart=`. The old generation stops sending
   sd_notify messages.
6. The old generation stops accepting and finishes what it holds.

Any failure before step 5 completes stops the successor and leaves the old
generation serving; the unit never has zero processes. A second reload while
an old generation still drains sends that generation SIGTERM (it is found
by sharing the unit's cgroup): at most two generations hold requests, plus
briefly a third finishing its last grace.

### What the old generation keeps and stops

| | Keeps until done | Stops at the hand-off |
| --- | --- | --- |
| all four | in-flight HTTP requests and long polls; per-request sinks (usage outbox, request telemetry, audit) | listeners, `/metrics` and health (the successor answers), sd_notify |
| gatewayd-public | SSE and WebSocket proxying | raw TCP/UDP ingress, DNS hand-off subscriber, standby warm-up |
| gatewayd-internal | SSE, WebSocket and vmmd stream proxying; private service TCP sessions | meterd's egress stream (meterd reconnects to the successor), log-drain delivery (the successor takes the spool lease), wake-ahead persistence, runtime gateway heartbeat, surface cert renewal, guest service DNS |
| apid | SSE streams, long polls, uploads | every background worker (its generation context ends), internal gRPC servers (clients reconnect) |
| realtimed | nothing long-lived | WebSocket sessions |

realtimed's fan-out is per process: a session left on the old generation
would miss what apps publish to the successor. So the old generation closes
its sessions once the successor serves, and clients reconnect to a ready
process at once instead of waiting out a restart.

Node-local durable queues get a slot per generation (`pkg/genslot`): slot 0
is the queue's existing root, slot n is `root/.generation-n`, each held by an
flock for the process's lifetime. gatewayd-internal's egress replay ledger
and consumer usage outbox, and realtimed's callback outbox, live in slots.
A running generation adopts an exited one's records every 10 s
(`egressgrpc.Server.Absorb`, `usageoutbox.Outbox.Absorb`,
`realtime.CallbackOutbox.Absorb`), keeping event IDs so a replay after a
crash is deduplicated downstream. A generation outside slot 0 moves its
records back to slot 0 on a clean exit, waiting up to 5 s for slot 0, so a
release without slots finds them where it looks. The log-drain spool keeps
its single lease, which the successor waits for.

### Bound on the old generation's lifetime

`api.HandoffDrainMaxSeconds` (1,800 s) from the hand-off. That covers the
customer request envelope (`api.CustomerRequestEnvelopeTimeout`, about
25 minutes). SSE and WebSocket sessions end one restart grace (25 s) before
the bound so their clients reconnect cleanly; anything still running at the
bound is cut. A SIGTERM — the unit stopping, a restart, or a newer hand-off —
ends streams at once and allows one more grace. A successor that is not
ready within `api.HandoffReadyTimeoutSeconds` (120 s) is stopped and the
reload fails.

### Memory and CPU on one control-plane host

The two generations share the unit's cgroup, so a hand-off never raises the
host's commitment above the unit's `MemoryMax`: 512 M for both gateways,
realtimed and apid, all inside `faas-cp.slice`. The 45 % check
leaves the successor at least as much as the old generation used; the old
generation's memory falls as its requests finish. If the kernel still
OOM-kills one of them, `OOMPolicy=continue` keeps the other running (systemd's
default would stop the unit); a killed main process restarts as before.
deployctl reloads one daemon at a time, so at most one daemon starts a second
generation at once, though earlier ones may still be draining. There is no
CPU quota on these units: the successor's start-up (cache loads, and apid's
migrations) overlaps a draining process that is mostly idle. apid's cap
and its `GOMEMLIMIT` of 256 MiB per process are sized from production use in
ADR 20261018132058; its hand-off threshold is 230 MB.

### Deploys

deployctl reloads a hand-off daemon instead of restarting it when:

- its unit hands off on reload and is active with no pending
  daemon-reload;
- nothing systemd reads at start changed since the unit started: unit
  fragment, drop-ins, environment files and credential sources (the
  successor inherits the old values, so only a new binary takes effect
  through a reload). deployctl keeps an unchanged unit file's mtime;
- both the running binary (`/proc/<MainPID>/exe`) and the new one answer
  `-handoff-capable` with 0 (`wire.HandoffDaemon`; a release before this one
  rejects the flag).

It then waits until `MainPID` changes, and restarts instead when the daemon
reports `STATUS=hand-off failed`, or after the readiness bound plus 30 s.
Otherwise it restarts as before. apid's successor runs its migrations while
the old generation still serves, so a migration must stay compatible with
the previous release for up to the drain bound — already required of compute
nodes during a rolling deploy.

### Rollback

- A deploy back to a release without hand-off restarts: the older binary
  fails the `-handoff-capable` probe, and its units have no `ExecReload`.
  Records in slots other than slot 0 are moved home on the clean exit of the
  generation holding them; only a generation killed while another held slot
  0 can leave records an older release does not read, and the next start of
  a slot-aware release adopts them.
- To turn hand-off off without a release, restart instead of reloading
  (`systemctl restart`): the unit behaves as it did before this section.
  Removing `ExecReload=` from the units makes deployctl restart.

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
- On a restart, a request still running when the 25 s grace ends is cut,
  because systemd cannot start the replacement before the old process exits.
  A reload of a hand-off daemon keeps it, up to `api.HandoffDrainMaxSeconds`.
  Deploys reload those daemons; ansible handlers that change configuration
  still restart.
- During a hand-off, Prometheus scrapes the successor; counters the old
  generation increments while draining are not scraped.
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

- **Two template instances sharing the socket** (`faas-gatewayd-internal@{a,b}`,
  both with `Sockets=`): start the new instance, then stop the old one. It
  gives the same overlap, but every unit, drop-in, credential and
  dependency on the daemon's unit name would split in two, and deploys would
  alternate instances. The in-unit hand-off keeps one unit name and one
  cgroup, at the cost of the `MAINPID=` step.
- **systemd file descriptor store** (`FileDescriptorStorePreserve=`, systemd
  254+): no socket units and no traffic activation, but it depends on host
  systemd versions we do not pin.
- **Self re-exec with an fd hand-off** (tableflip): chosen, as the
  two-generation hand-off above, with reload-based deploys.
- **`Type=notify-reload`** (systemd 253+): `systemctl reload` would block
  until the hand-off finished, but hosts run older systemd; deployctl polls
  `MainPID` instead.
