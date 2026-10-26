# Long-lived connections survive data-path restarts

Date: 2026-10-17
Status: Proposed (design only; no implementation)
Related: ADR-009 (identical inner network), ADR-028 (remote routing through
vmmd; revised here), ADR-047 (streaming), ADR-070 (edge split), ADR-080
(raw-bytes bridge), ADR-126/127 (H2C bridge), ADR-183 (public raw TCP),
ADR-190 (daemon durability), ADR-052/056 (mTLS, node verifier), ADR-472–475
(vmmd resource journal), `20261017-vmmd-restart-adoption.md` (the adoption
ADR), ADR-909 (measured admission), ADR-1151 (pause
tiers), ADR-1343 (edge on compute nodes), spec §4.1, §4.4, §6.2, §11, §12, §13
Revises: ADR-028's decision that vmmd forwards customer bytes, and the
bridge process model of ADR-028 (amendment), ADR-080 and ADR-183
Amends when accepted: ADR-909 (the node's `used` figure includes bridge
memory); CLAUDE.md "Component ownership"; spec §4.4, §12, §13

## Context

The owner wants zero downtime, releases in 30 minutes or less, and deploys
that only change what they touch. Every release drains compute nodes today,
because restarting vmmd orphans its VMs. A separate effort makes vmmd adopt
running VMs. Adoption alone does not save customer connections. WebSocket,
SSE, gRPC streams and raw TCP all pass through vmmd and both gateways, so a
restart of any of them still cuts every stream through it. This ADR deals
with those connections.

### The data path today (main @ 7616ce44f9)

```
client ─ Cloudflare ─ Caddy (CP :443) ─▶ gatewayd-public 127.0.0.1:8080
   ─▶ gatewayd-internal   /run/faas/gatewayd-internal.sock (H2C), or TCP from
                          the CP's compute_gateway_pool to <node>:8080
   ─▶ vmmd gRPC Forward{HTTP,Raw,TCP,UDP}Stream on the instance's node
                          (unix /run/faas/vmmd.sock, or the overlay with mTLS)
   ─▶ bridge process inside the instance netns ─▶ guest 10.0.0.2:<port>
```

tcpd and udpd (ADR-183, inside gatewayd-public) call vmmd's
`ForwardTCPStream` and `ForwardUDPStream` directly. schedd's environment
qualification smoke test calls `ForwardHTTPStream`
(`pkg/sched/vmmclient_environment_qualification_smoke.go`).

| Hop | Restart behaviour today |
|---|---|
| gatewayd-public | `faas-gatewayd-public.socket` keeps the listener in PID 1. On SIGTERM the process stops accepting and waits up to `GatewayDrainGraceSeconds` (25 s) for in-flight requests and hijacked streams, then exits (`cmd/gatewayd-public/main.go` `runDrain`). New connections queue in the backlog until the replacement starts. Any stream older than 25 s is cut. |
| gatewayd-internal | No socket unit. `drain.DrainGrace` (25 s), then exit. Every stream through it is cut, including all gRPC streams it holds to vmmd. |
| vmmd | One process generation only. The resource journal is exclusive (ADR-473). Every Forward* RPC ends, and every bridge dies with it. |
| bridges | Children of vmmd (see below). |

### Bridges, verified in code

The brief that started this ADR described the stream bridge as
per-request. Since `62b9da95a7` (2026-09-01, persistent stream bridge
lifecycle; prewarm during restore came in `b1e0c8f3c6`), that is only the
rollback path. The current state:

- **HTTP** (`forwardHTTPStreamV2`, `pkg/vmmdgrpc/stream_bridge_manager.go`).
  There is one persistent `vmmd-stream-bridge` per live instance, started by
  `ip netns exec`, serving H2C on `/var/run/faas/stream/<instance>.sock`.
  vmmd prewarms it during restore and reaps it after 2 min idle
  (`streamBridgeIdleTimeout`). It is a vmmd child with
  `Pdeathsig: SIGTERM` (`forward_pdeathsig_linux.go`), so it dies with vmmd
  by design. `FAAS_STREAM_BRIDGE_PERSISTENT=0` brings back one process per
  RPC (`streamBridgeSockPathForRequest`).
- **Raw/Upgrade (WebSocket), TCP, UDP** (`ForwardRawStream`,
  `forward_tcp.go`, `forward_udp.go`). These are still per connection:
  `nsenter --target <firecracker-pid> --net -- vmmd-{raw,tcp,udp}-bridge`,
  two execs and a setns before the guest dial, wired over stdio pipes and
  killed with the RPC context. The long-lived connection types are the ones
  that pay a process spawn per connect.
- **Privilege.** No bridge drops privilege. `cmd/vmmd-*-bridge` contain no
  setuid, setgid, capability or seccomp code, and the spawn sites set no
  `Credential`. Every bridge runs as uid 0 with vmmd's capability bounding
  set (including `cap_sys_admin`, `cap_net_admin` and `cap_sys_ptrace`) and
  parses bytes the guest controls.
- **Memory.** Persistent bridges run in vmmd's cgroup inside `faas-cp.slice`.
  The spec §13 ledger gives vmmd 256 MB and has no line for bridges.

The forward handlers also carry duties that are not about moving bytes. All
four call `beginActivity` and `prepareGuest`: the ADR-1151 activity count
(an open stream blocks quiet pause), quiet-pause resume, frozen refusal
(`pkg/vmmdgrpc/freeze.go`) and `forwardSessions` teardown, which ends
upgraded sessions when the instance stops (H9-129). Activity reaches schedd
through `InstanceStats` (`inflight_requests`, `last_request_at`) for scaling
and idle decisions. Moving forwarding out of vmmd must keep all of these.

### Caps today

HTTP and SSE streams are capped at 910 s per hop, plus a 60 min idle timer
(`newStreamSession(…, 910*time.Second, streamIdleTimeout)` in
`pkg/gateway/forwardproxy.go`, `api.StreamingIdleTimeoutDefault`). Raw and
WebSocket sessions are capped at 24 h (`rawStreamSessionDeadline`,
`streamBridgeSessionDeadline`). When a cap fires, the client sees an abrupt
close.

### Prior art

Envoy hot restart (parent and child over a UDS, drain time, parent shutdown
time), HAProxy seamless reload (master-worker; `-x` fetches listener FDs;
old workers keep their connections until `hard-stop-after`) and Pingora
graceful upgrade (the new process asks the old one for its listeners over a
UDS; the old one finishes its connections). All three rely on the same
property: listening sockets are kernel objects that can be passed between
processes, and an old process can keep serving the connections it already
accepted while a new one accepts. vmmd cannot use this pattern, because its
exclusive ownership of VMs and the journal admits one generation at a time.
The gateways and a new forwarder can.

## Decision

### 1. `forwardd`: an unprivileged per-node forwarder

A new daemon, `cmd/forwardd`, runs on every compute node and takes over the
four Forward* RPCs from vmmd. vmmd keeps the VM lifecycle and never sees a
customer byte again.

- **Service.** `onebox.faas.forward.v1.Forwarder` with `ForwardHTTPStream`,
  `ForwardRawStream`, `ForwardTCPStream` and `ForwardUDPStream`. It imports
  the existing `vmmd.v1` request and response messages unchanged, so callers
  change their dial target and nothing else.
- **Listeners.** Unix `/run/faas/forwardd.sock` for gateways on the same
  node. A TCP listener on the overlay (`forward_listen_addr` in node config)
  for other nodes, with mTLS (§6). No Postgres, no pg_notify, no customer
  config. Its only input is the node-local bridge registry (§2). It changes
  rarely, so most releases will not need to roll it.
- **Discovery.** vmmd's registration writes `compute_nodes.forward_target`
  (one migration). gatewayd-internal's `NodeClientCache`, tcpd, udpd and the
  schedd smoke test dial `forward_target` when it is set, and fall back to
  vmmd's Forward* while it is unset. Both the rollout and the rollback switch
  per node.
- **Per request.** forwardd resolves `(instance, wake_id)` against the
  registry. If no entry matches, it returns `NotFound`, which the gateway
  already handles as a stale target. Otherwise it connects to the instance
  bridge and moves bytes. It enforces the 910 s and 24 h ceilings and
  `RawStreamMaxRequestBytes` as defence in depth. These were vmmd's
  authority before. The gateway's ceilings stay authoritative for the client.
- **Hop count.** It does not change. Today a stream goes gatewayd-internal
  → vmmd (unix or overlay) → bridge. Afterwards it goes gatewayd-internal →
  forwardd (unix or overlay) → bridge.
- **Retirement of the old path.** vmmd's Forward* handlers stay as the
  fallback until `vmmd_forward_rpcs_total` has been zero across one release
  on every node. Then vmmd's authorizer rejects them and the code and the
  `vmmd-{stream,raw,tcp,udp}-bridge` binaries are deleted.

### 2. One bridge per instance, owned by systemd and started by vmmd

`cmd/instance-bridge` replaces the four bridge binaries. Each instance that
accepts inbound traffic (it has an HTTP port or a declared TCP or UDP
listener) gets exactly one bridge. Builders, jobs and portless workers get
none.

**Lifecycle.**

- vmmd starts the bridge when the instance's netns is ready, on cold boot
  and on restore. On restore it starts in parallel with the Firecracker
  restore, as the prewarm does today, so it stays off the
  `gateway_wake_latency_seconds` critical path.
- The instance is not ready until its bridge is. A bridge start failure is a
  wake failure with reason `bridge_start_failed`, retried under the existing
  policy. It never marks a snapshot stale, and wake does not depend on any
  earlier bridge (ADR-005).
- The bridge lives as long as the instance: no idle reap. At park, destroy
  or failed wake, vmmd closes the bridge gracefully (§4), stops its unit and
  waits for the cgroup to go away before it reports the instance parked.
  This keeps invariant 4: a parked app has no resident process or cgroup.

**Placement.** The bridge is a transient systemd *service* named
`faas-ibridge-<instance>-<incarnation>.service`, which vmmd creates with
`StartTransientUnit` over D-Bus. PID 1 forks it, so it is never in vmmd's
cgroup, needs no Pdeathsig and survives every vmmd restart by construction.
The brief asked for a scope unit. A scope adopts a process that already
exists: vmmd would fork the bridge inside its own cgroup and move it out
afterwards, and a vmmd death between fork and move leaves a bridge that
vmmd's unit stop kills. The transient service has no such window, and it
provides the sandbox below without custom code.

**Unit properties.**

- `NetworkNamespacePath=` set to the netns binding whose device and inode
  vmmd journaled under ADR-475, checked again immediately before the call.
  vmmd never passes a name it has not verified.
- `User=`/`Group=` set to the instance's bridge uid, `jail_uid + 10000`.
  That range, 30000–39999, must stay free of host accounts: with
  unprivileged bridges on, vmmd refuses to start when any account or group
  uses it (`checkBridgeUIDRange`).
- `CapabilityBoundingSet=` empty, `NoNewPrivileges=yes`,
  `SystemCallFilter=@system-service`, `ProtectSystem=strict`,
  `ProtectHome=yes`, `PrivateTmp=yes`, `PrivateDevices=yes`,
  `ProtectProc=invisible`, `RestrictAddressFamilies=AF_UNIX AF_INET`.
- `IPAddressAllow=10.0.0.2/32` and `IPAddressDeny=any`. This is a cgroup BPF
  filter scoped to the bridge, so the bridge can only reach its guest.
- `ReadWritePaths=` limited to its own socket directory (below).
- `Slice=faas-tenant-ibridge.slice` (memory, below), with
  `MemoryMax=InstanceBridgeMemoryMaxMB`, `TasksMax=` and `LimitNOFILE=`.
- Logs go to journald. No stdio is tied to vmmd.

**Memory.** The memory these bridges use already exists today, but it is
not accounted for. It sits inside vmmd's cgroup in `faas-cp.slice`, whose
§13 ledger gives vmmd 256 MB and has about 250 MB of slack once the
guaranteed builder is counted.

- Placement: bridges move into `faas-tenant-ibridge.slice`, a child of
  `faas-tenant.slice`. The tenant hard fence then covers them, and a bridge
  leak cannot OOM the control plane.
- Admission: ADR-909's measured charge is computed per instance cgroup, so
  it would not see bridges in a separate slice. This ADR amends ADR-909:
  the node's `used` figure gains the measured memory of
  `faas-tenant-ibridge.slice`. vmmd reports that number in its node stats,
  and schedd adds it to the measured charge.
- Per-VM `memory.max = plan + 8 MB` does not change.
- Billing does not change: plan RAM plus 8 MB per running second (§4.7).
  The bridge is platform overhead, like vmmd.
- The density cost is real, and P0 measures it (§Phases). If an idle
  bridge costs more than 4 MB PSS, §2 falls back to the socket-broker
  variant under Rejected alternatives. That fallback needs an amendment to
  this ADR.

**Registry.** The registry lives under `/run/faas-ibridge/<instance>/`. The
root is vmmd's systemd `RuntimeDirectory` (0711, preserved across restarts),
directly under `/run`, which only root can write. It is not under `/run/faas`:
group `faas` can rename entries there, so another daemon could swap the
directory that vmmd creates, chowns and removes inside.

- The instance directory is mode 0750, `root:faas-forward`. Only vmmd
  writes in it.
- For each bridge generation, vmmd writes `<bridge-gen>.json` there:
  instance, incarnation, wake_id, app, deployment, ports, bridge uid,
  bridge version and protocol, unit name and InvocationID.
- `serving` is a symlink to the generation that takes new connections.
  vmmd replaces it with an atomic rename.
- Each generation's sockets live in `<bridge-gen>/`, owned by the bridge
  uid, group `faas-forward`, mode 0750. It holds `data.sock` (0660) and
  `control.sock` (0600; vmmd connects as root).

A bridge can write only its own socket directory, so even a compromised
bridge cannot rewrite its metadata or the `serving` pointer. forwardd watches
the tree with inotify and rescans every 5 s. The registry is tmpfs, so it
disappears with the VMs on reboot. It survives restarts of vmmd and of
forwardd.

**Data protocol.** One data socket carries every kind:

- HTTP requests go over a pooled H2C connection per forwardd generation.
  This is today's v2 bridge contract, including private `X-Faas-Bridge-*`
  metadata headers, H1 or H2C toward the guest, and the guest transport
  pool.
- Raw upgrade, TCP and UDP each get their own unix stream connection. It
  starts with a versioned preamble (kind, port, wake_id, request head for
  raw) and then carries bytes, spliced on both sides. One connection per
  session gives exact backpressure and confines a failure to one session.
- The preamble version is checked both ways, so forwardd and the bridge can
  differ by version (§5).
- This removes the per-connection spawn: each WebSocket, TCP and UDP connect
  saves a fork and exec of `nsenter`, a setns and a second exec. The
  `streamBridgeSocketReadyTimeout` comment records that the per-request
  stream bridge's 50 ms startup probe "routinely killed a valid bridge on a
  loaded compute node", so a bridge spawn can take more than 50 ms under
  load. P0 measures the raw and TCP spawn cost, and the saving lands in P1,
  before forwardd exists.

**Gate and counters: the ADR-1151 duties move to the bridge.** Every byte
for an instance passes through its bridge, and the bridge outlives both
forwardd and vmmd. That makes it the right place to count.

- The bridge counts open sessions and in-flight requests, and records the
  last request time. vmmd keeps one long-lived connection to each bridge's
  control socket and reads these counters for `InstanceStats`.
- vmmd's own activity sources stay in vmmd: `waitUntil` tail tasks and exec
  sessions.
- The gate states are `open`, `quiet`, `frozen`, `draining` and `closing`.
  vmmd sets them over the control connection. `draining` refuses new
  connections and keeps existing ones; it is used for in-place rolls (§5).
- **Quiet pause.** vmmd asks `quiesce`. The bridge answers `idle` and holds
  new connections, or answers `busy`. Only after `idle` does vmmd pause
  Firecracker. A new connection to a `quiet` bridge raises a `resume` event
  on the control connection. The bridge coalesces events, so there is one
  resume per instance (ADR-1151). vmmd resumes the VM and reopens the gate.
- **vmmd down.** The bridge never reopens a `quiet` gate on its own. If vmmd
  is restarting, it holds new connections for up to
  `InstanceBridgeResumeWaitSeconds` and then answers a retryable 503,
  `instance_resuming`. A quiet-paused instance has no open streams by
  definition (ADR-1151 counts them as activity), so a vmmd restart delays
  only new connections to paused instances.
- **Frozen.** A `frozen` gate refuses new connections with
  `instance_frozen`. forwardd maps that to the existing 503 problem, which
  triggers the gateway's thaw wake as today.
- **Teardown.** A `closing` gate closes sessions gracefully (§4) within
  `InstanceBridgeCloseGraceSeconds`. This replaces `forwardSessions`
  (H9-129).
- Bridges never connect to vmmd. No new vmmd listener is reachable from a
  bridge uid.

**Journal and adoption.** Bridge generations are recorded in the Bridge group
of the instance's version-7 adoption record (adoption ADR §1), so there is one
journal version for both efforts. Before the `StartTransientUnit` call, vmmd
commits an intent with the unit name, uid, registry path and expected binary
identity. After the call it commits the InvocationID and the main PID with its
start ticks.

On start, vmmd runs the ADR-472 inventory, which now also covers bridge
uids and `faas-ibridge-*` units. A bridge is adopted only if all of these
hold:

- its unit, InvocationID, uid, cgroup and netns inode match the journal;
- `/proc/<pid>/exe` matches the journaled digest;
- its control socket answers a hello naming the same instance and
  incarnation.

An adopted bridge keeps serving and vmmd rebuilds its view from the
counters. A bridge vmmd cannot prove it owns is quarantined: its uid stays
reserved and it is never killed blind (ADR-472). Its instance is then refused
adoption (`bridge_unproven`), because the reserved uid blocks a replacement
bridge. A bridge whose process is verified absent is replaced by a new
generation; its streams are lost and the guest keeps running. `make leakcheck` gains
zero leaked `faas-ibridge-*` units, bridge uids and registry directories.

**What does not change.**

- ADR-009: the bridge dials 10.0.0.2 from 10.0.0.1 inside the netns, as
  today's bridges do. The inner network is identical and snapshot reuse is
  untouched. The bridge is never part of a snapshot. A restored instance
  gets a new bridge, a new incarnation and a new uid, which extends
  invariant 5.
- Per-netns nftables policing: guest egress rules match `iifname tap0`. Bridge
  connections originate locally in the netns, as today, so no rule changes.
  The new `IPAddressAllow` is a strict narrowing on top.
- vmmd is still the only root component, and the only one that touches
  Firecracker and the jailer.

### 3. Overlapping process generations

This applies to forwardd, gatewayd-internal and gatewayd-public. A
*generation* is one process of one binary and config. At most one
generation of a daemon is *serving*: it holds the listeners and accepts.
Any number up to a bound may be *draining*: they accept nothing and serve
only the streams they already have.

**Mechanism.** It is one shared library, `pkg/dataplane/generation`. Unit
names do not change, and `faas-gatewayd-public.socket` stays.

1. `systemctl reload <unit>` runs `ExecReload=<binary> generation spawn`. A
   process started by systemd for the unit gets the unit's *current*
   environment, credentials and sandbox after `daemon-reload`. Changes to
   the unit file and environment therefore reach the new generation, unlike
   an in-process re-exec.
2. The new generation warms up (route caches, DB pool, TLS) while the old
   one keeps serving. It then connects to the serving generation's
   `/run/faas/<daemon>/handoff.sock` (mode 0600). The serving generation
   checks the peer with `SO_PEERCRED` (same uid) and checks that
   `/proc/<peer>/cgroup` is this unit's cgroup. Every daemon runs as `faas`,
   and a uid check alone would let apid take gatewayd-public's listener.
3. The serving generation sends every listener FD with `SCM_RIGHTS`:
   public, private, control, unix socket and the tcpd and udpd ports. The
   handover then runs in this order:
   1. The new generation checks itself and asks to commit.
   2. The old generation stops accepting, acknowledges, and becomes
      draining.
   3. Only then does the new generation start accepting. Connections that
      arrive in between wait in the shared accept queue, which is never
      dropped or reset, and nothing is accepted twice.
   4. The new generation sends `MAINPID=<self>` and `READY=1`
      (`NotifyAccess=all`) and takes over `handoff.sock` by rename.
4. A draining generation:
   - sends HTTP/2 GOAWAY on the H2 connections it serves, so clients send
     new requests over new connections, which reach the new generation,
     while existing streams continue;
   - opens no new upstream streams;
   - answers HTTP/1.1 keep-alive requests with `Connection: close`;
   - has gRPC `GracefulStop` semantics on its server side;
   - drops route caches and closes its Postgres pool and LISTEN connection,
     so the ADR-190 connection budget does not grow with the number of
     generations;
   - writes end-of-stream records (egress meter, consumer usage, log
     drains, audit) to node-local spool files suffixed with its generation
     ID, which the serving generation flushes;
   - reports its per-instance and per-app open-stream counts to the serving
     generation every second over the handoff connection. The serving
     generation adds them in its picker, rate limits and concurrency
     accounting, so new sessions do not pile onto an instance that is still
     carrying 500 old ones.
5. A draining generation exits when its last stream ends, or when it is
   retired. Retirement happens when a new generation would exceed
   `DataPathMaxGenerations` (the oldest is retired), at
   `DataPathGenerationMaxDrainHours`, or on a new operator command (for
   security fixes), sent over the handoff socket. Retirement closes the
   remaining sessions gracefully (§4).
6. **Failure is never worse than today.** If the new generation does not
   commit within `DataPathHandoffTimeoutSeconds`, it exits without having
   accepted anything, the old generation keeps serving, and `systemctl
   reload` fails. The release
   step fails with the old version still serving. A crash of the *serving*
   generation restarts the unit, and systemd's stop kills every generation
   of that daemon, which is today's crash behaviour. A crash of a draining
   generation affects only its own streams.
7. `MemoryMax=` of each unit covers one serving generation plus the draining
   bound. P0 measures a draining generation's resident size after it drops
   its caches. `WatchdogSec=` follows `MAINPID`.

**Releases.** The rollout uses `systemctl daemon-reload && systemctl reload`
for these three units instead of `restart`. A real restart is kept for a
change to the handoff protocol's major version, which should be rare, and
for host maintenance. Once these daemons and vmmd adoption are in place, a
compute release no longer needs a node drain.

**Generation bound and session lifetime.** HTTP and SSE streams end within
910 s, so a draining generation that holds only HTTP is gone within about
16 minutes. Only raw, WebSocket and TCP sessions (24 h) keep generations
alive. With `DataPathMaxGenerations = 4` (one serving, three draining), a
WebSocket survives at least three releases of each data-path daemon. At up
to four releases a day, every session reaches its own 24 h cap first. If
the generation gauges show retirements in practice, the later phase "raw
session handoff" (§Phases) passes quiescent spliced sessions to the new
generation and removes the bound.

### 4. Graceful close when a session must end

Caps, retirement, instance teardown, node drain and host reboot still end
sessions. The end becomes a protocol-level close the client can act on,
instead of a reset.

| Kind | Close |
|---|---|
| WebSocket | Close frame **1012** (Service Restart) to the client and to the guest, then FIN. gatewayd-public tracks server-to-client frame boundaries. The bridge tracks client-to-server boundaries and sends a masked close frame to the guest. Only frame headers are parsed (2–14 bytes), never payloads. Control frames may be sent between the fragments of a message (RFC 6455 §5.4), so a frame boundary is enough. |
| SSE (`text/event-stream`) | At the next event boundary (`\n\n`): `retry: <InstanceBridgeSSERetryMs>\n\n`, then the end of the stream. EventSource reconnects with `Last-Event-ID`. Resuming from it is the customer's job and is documented. |
| gRPC (`application/grpc*`) | At a message boundary (5-byte length prefix): trailers `grpc-status: 14` (UNAVAILABLE), `grpc-message: server restarting`. That status is retryable by default in gRPC clients. |
| H2 between our hops | GOAWAY, with existing streams allowed to finish (§3). |
| Raw TCP (ADR-183) | Half-close (FIN) both ways, never RST. TCP clients must reconnect. This is documented. |
| HTTP long-poll / chunked | Unchanged: the 910 s cap applies. Ending a response cleanly at a chunk boundary would turn it into a truncated success. |

The hop that ends a session writes a typed reason (`restart`, `cap`, `idle`,
`teardown`, `drain`, `reboot`) into the RPC trailer or the bridge preamble's
close frame. Each hop forwards the reason, so the two ends that understand
the protocol (gatewayd-public and the bridge) emit the close. A node drain
for reboot runs in this order:

1. Stop admission.
2. Withdraw edge traffic (ADR-1343 connector withdrawal, where it applies).
3. Wait up to `DataPathRebootDrainSeconds` for sessions to end on their own.
4. Retire the rest with the closes above.
5. Reboot.

UDP flows have no connection to close. After a gatewayd-public handoff the
next datagram creates a new flow, and the guest sees a new source port.

### 5. Bridge versions follow the instance lifecycle

- vmmd starts bridges from the immutable release directory, never through
  `/opt/faas/current`, and records the version and digest in `<bridge-gen>.json` and
  the journal. New instances (boot, restore, wake) get the new bridge. A
  running instance keeps its bridge until park. Instances park after their
  plan's idle timeout (30–600 s; functions default to 30 s), so most of the
  fleet turns over within minutes of a release.
- Long-lived instances (always-on services, workers) do not park. vmmd
  rolls their bridge in place: it starts bridge generation N+1 for the
  instance, renames `serving` to it, and moves N to `draining`, so N keeps
  its sessions until they end or reach their caps. The guest does not care
  how many bridges connect to it. vmmd adds the
  counters of all of an instance's bridges, and quiet pause requires all of
  them to be idle.
- Rolls are rate-limited (`InstanceBridgeRollPerMinute`). They are required
  when a bridge is older than `InstanceBridgeMaxAgeDays` or
  `InstanceBridgeMaxVersionSkew` releases. A security fix in the bridge
  rolls at once and retires old generations with graceful close.
- **Compatibility.** The forwardd↔bridge and vmmd↔bridge protocols are
  additive within a major version. forwardd speaks every bridge protocol
  version still running. A major bump ships only when
  `instance_bridge_running{protocol_major=<old>}` is zero on every node.
- Pruning a release directory is safe for running bridges, because the
  kernel keeps the inode. vmmd's adoption check compares `/proc/<pid>/exe`
  to the journaled digest, not the path.

### 6. Security

**Privilege.** Customer bytes leave the only root process. Today vmmd and
all four kinds of bridge run as root with `cap_sys_admin` and parse
guest-controlled data.
Afterwards:

- forwardd runs as a new user `faas-forward`, with no capabilities and the
  gateways' sandbox. It can read the bridge registry and connect to
  `data.sock`, and it has no other privilege.
- Each bridge has its own uid, so a guest that exploits its bridge's parser
  gets one unprivileged process. That process can reach only its own guest
  (`IPAddressAllow`) and has no write access outside its socket
  directory. It cannot signal or ptrace other tenants' bridges or any
  Firecracker process, because all uids differ (jail uid ≠ bridge uid).
- vmmd keeps root for the VM lifecycle only.

**Cross-tenant isolation.**

- A bridge accepts a data connection only if `SO_PEERCRED` shows uid
  `faas-forward`, and a control connection only from uid 0.
- forwardd accepts a bridge only if the bridge peer's uid equals the uid in
  its `<bridge-gen>.json` and its hello names the expected instance and incarnation.
  Instance IDs are reused after park and wake, so the registry is keyed by
  incarnation and every request carries `wake_id`.
- A request for one tenant can never be served by another tenant's socket:
  the path, the uid and the hello must all agree.

**mTLS between gateway and forwardd.**

- The overlay listener uses the ADR-052 mTLS stack with cert reload and the
  ADR-056 node verifier. forwardd's certificate CN or SAN is its node ID,
  and callers verify that it equals the target node. A compromised node
  cannot impersonate another node's forwardd.
- forwardd's handler authorizes only the gatewayd-internal and
  gatewayd-public (tcpd, udpd) roles, plus schedd for the qualification
  smoke test.
- The local unix socket checks `SO_PEERCRED` against the same roles' uids.
- The port is open on the overlay interface only, through the same host
  firewall input rule as vmmd's.

**Handoff socket.** Peers must have the same uid *and* the same unit
cgroup (§3).

**Denial of service.**

- Per-bridge `MemoryMax`, `TasksMax`, `LimitNOFILE` and
  `InstanceBridgeMaxConnections`.
- Today's gateway per-app concurrency and wake queue limits are unchanged.
- The generation bound limits a slow loris of old generations.

**Recommended separately.** Give each data-path daemon its own uid instead
of the shared `faas`. That is outside this ADR.

### 7. Metrics and alerts (spec §12 additions)

- `dataplane_generations{daemon,state}` (gauge, `state` is serving or
  draining)
- `dataplane_generation_open_streams{daemon,generation,kind}`
- `dataplane_handoff_duration_seconds{daemon}` (histogram) and
  `dataplane_handoff_failures_total{daemon,reason}`
- `dataplane_session_closes_total{daemon,kind,reason,mode}`, where `mode`
  is `graceful` or `abrupt`
- `forwardd_streams_active{kind}`, `forwardd_bridge_connect_seconds`,
  `forwardd_bridge_connect_failures_total{reason}`
- `instance_bridge_running{version,protocol_major}`,
  `instance_bridge_start_seconds` (in parallel with restore; its p95 must
  stay below the restore p95), `instance_bridge_adoptions_total{result}`,
  `instance_bridge_memory_bytes` (node sum) and
  `instance_bridge_resume_wait_seconds`
- `vmmd_forward_rpcs_total{rpc}` (legacy path; must reach zero before
  removal)

Alerts:

- `FaasDataPlaneHandoffFailed`
- `FaasDataPlaneGenerationPileUp`: draining generations ≥
  `DataPathMaxGenerations − 1` for 30 min
- `FaasInstanceBridgeVersionSkew`
- `FaasInstanceBridgeQuarantined`

Existing names (`gateway_wake_latency_seconds`, `gateway_stream_active`,
`gateway_ws_active_sessions`, `gateway_ws_session_duration_seconds`,
`gateway_ws_upgrade_total`, …) do not change.

### 8. Limits (`pkg/api/limits.go`)

The values are proposals. P0 confirms them before any of them ships.

| Constant | Proposed |
|---|---|
| `InstanceBridgeMemoryMaxMB` | 32 |
| `InstanceBridgeTasksMax` | 64 |
| `InstanceBridgeMaxConnections` | 4096 (= `DefaultConntrackCap`) |
| `InstanceBridgeCloseGraceSeconds` | 10 |
| `InstanceBridgeResumeWaitSeconds` | 10 |
| `InstanceBridgeSSERetryMs` | 1000 |
| `InstanceBridgeMaxAgeDays` / `InstanceBridgeMaxVersionSkew` | 7 / 3 |
| `InstanceBridgeRollPerMinute` | 30 per node |
| `DataPathMaxGenerations` | 4 |
| `DataPathGenerationMaxDrainHours` | 24 |
| `DataPathHandoffTimeoutSeconds` | 30 |
| `DataPathRebootDrainSeconds` | 120 |

The bridge uid range (`BridgeUIDBase = JailUIDBase + 10000`) is an allocator
constant in `pkg/fcvm/alloc.go`, next to `JailUIDBase`, and is not a plan
limit.

### What a restart costs afterwards

| Event | Today | After |
|---|---|---|
| gatewayd-public, gatewayd-internal or forwardd release | streams cut (25 s drain) | none cut (reload) |
| vmmd restart | every stream cut; VMs orphaned, so nodes are drained | no stream cut. New connections to quiet-paused instances wait for vmmd. VM management needs the adoption effort. |
| bridge upgrade | — (dies with vmmd) | new instances only; long-lived ones roll in place without cutting |
| crash of a serving generation | its streams cut | same, for every generation of that daemon |
| 910 s / 24 h / idle cap, retirement | abrupt | graceful (1012, `retry:`, UNAVAILABLE, FIN) |
| host reboot, kernel or Firecracker upgrade | abrupt | drain, then graceful close |
| Caddy, cloudflared, Cloudflare | cut | unchanged (out of scope) |

## Phases

Each slice is a small PR behind a per-node flag, and each phase is useful on
its own.

- **P0 Measure (no behaviour change).** On fsn-4, measure the PSS and start
  time of today's persistent stream bridge at 50, 300 and 700 live
  instances, and the connect latency of the per-connection raw and TCP
  spawns.

  The production-us baseline was read from Prometheus on 2026-10-17
  (read-only). Prod carries only test accounts, so these numbers are a
  floor, not real load. Over 7 days:

  - about 44,800 gateway requests;
  - 12 WebSocket sessions, with at most 1 open at a time;
  - peak `gateway_stream_active` of 160;
  - WebSocket duration (`gateway_ws_session_duration_seconds`) p50 30 s,
    p95 271 s, p99 294 s.

  Production cannot size the generation bound or bridge memory yet. Both
  come from the fsn-4 load in §Acceptance, and the §8 values and the §2
  memory gate follow from it. Measure SSE and gRPC stream durations too.
  Today only WebSocket has a duration histogram, so add one for each kind
  of stream.
- **P1 Instance bridge (vmmd still forwards).**
  - 1a: every existing bridge runs as its per-instance bridge uid with no
    capabilities (`nsenter … -S <uid> -G <gid>`, `no_new_privs`). The HTTP
    bridge moves from `ip netns exec` to the same `nsenter` form, and its
    socket moves to a per-instance directory owned by the bridge uid.
    **Built** on `feat/vmmd-no-drain` behind `FAAS_BRIDGE_UNPRIVILEGED=1`
    (default off): `pkg/vmmdgrpc/bridge_identity.go`, `pkg/bridgeproc`.
    - The socket directory is `/run/faas-ibridge/<instance>/` (0700, bridge
      uid) under the 0711 root-only `RuntimeDirectory`. vmmd accepts the socket's peer only if
      `SO_PEERCRED` shows the expected bridge uid, because the directory
      belongs to the bridge.
    - Each bridge re-checks its own credentials at startup (all uids and
      gids equal the expected uid, no supplementary group, no capability)
      and exits otherwise. It re-arms the parent-death signal that the uid
      change cleared.
    - Every bridge now gets a minimal environment. Before this, the raw, TCP
      and UDP bridges inherited vmmd's whole environment, including the
      database and storage credentials.
    - The restart inventory reserves the slot of any surviving process that
      runs as a bridge uid, and `make leakcheck` reports such processes and
      leftover socket directories.
    - Public-target bridges (ADR-1749, merged from main 2026-10-22) keep the
      root identity: they run in the host namespace and dial through the
      node's interface, which needs privilege. `forwardBridgeIdentity`
      gives them the legacy socket path, environment and peer check, so
      only namespace bridges run as the bridge uid.
    - Still owed: the fsn-4 metal run, then the switch turned on per node.
  - 1b: one `instance-bridge` with one data socket. vmmd's four handlers
    become its clients, and the per-connection spawn goes away.
    **Built** behind `FAAS_INSTANCE_BRIDGE_SESSIONS=1` (vmmd, default off):
    `pkg/instancebridge`, `pkg/vmmdgrpc/bridge_session.go`.
    - The instance bridge is today's persistent stream bridge
      (`cmd/vmmd-stream-bridge`); it keeps that binary name until P1c
      installs it as `instance-bridge` with its unit, so release N and N−1
      run the same bridge path. Its socket now also serves sessions: a
      connection whose first bytes are the hello is a session, anything
      else (H2C, HTTP/1) is served as before.
    - A session is one unix stream connection. The client sends
      `FAAS-IBRIDGE/1 <raw|tcp|udp> <port>\n`, then the request bytes
      (raw, tcp) or udpwire frames (udp), and half-closes at the end of
      the request. The bridge dials the guest and answers `OK` or
      `ERR <message>`, then sends framed output (`D` + length + bytes)
      and one end frame (`E` + code + message). The end frame carries
      what the per-connection process's exit code and stderr carried, so
      a guest that drops a request body is still a failure, not a clean,
      truncated response.
    - vmmd's raw, TCP and UDP handlers open a session on the instance's
      live bridge whatever port started it; with none, the bridge starts
      with the parameters the instance's HTTP prewarm recorded, so a
      later HTTP request does not replace it. A session holds a lease, so
      the idle reaper keeps the bridge while it is open. The session
      context ends a session as it killed the process (H9-129).
    - A session lives as long as its bridge, as an HTTP stream through it
      does: the bridge's crash, or vmmd replacing it (another port or
      protocol for the instance, its teardown), ends the session, where a
      per-connection process lived on. The idle reaper never stops a
      bridge a session holds. 1c's unit gives the bridge the instance's
      lifetime.
    - The process path stays the default and the fallback: with the flag
      off, and whenever a bridge answers the hello with anything but the
      session protocol.
    - Activity counting, quiet pause, the frozen gate and session teardown
      stay in vmmd until 1d. The bridge still accepts only vmmd (its
      socket is in vmmd's directory); the `faas-forward` peer check comes
      with forwardd (P2).
  - 1c: transient unit, journal kind, adoption, and lifetime equal to the
    instance's.
    **Unit, lifetime and adoption built** behind `FAAS_INSTANCE_BRIDGE_UNIT=1`
    (vmmd, default off): `pkg/sdunit`, `pkg/vmmdgrpc/bridge_unit.go`,
    `pkg/vmmdgrpc/bridge_unit_adopt.go`.
    - The persistent bridge of a namespace instance with a bridge uid (1a)
      is `faas-ibridge-<instance>-<gen>.service`, created with
      `StartTransientUnit` (`Type=exec`) and the unit properties above.
      `<gen>` is random per run, not the incarnation, so a run whose stop
      is still settling never blocks the next one. vmmd then proves the run:
      it holds a pidfd on the main pid and checks that pid's cgroup is the
      unit's and its netns is the binding vmmd named. A run it cannot prove
      is stopped.
    - The unit does not set `User=`/`Group=`: systemd resolves them through
      the user database and refuses a uid with no account (`217/USER`), and
      bridge uids must have none (`checkBridgeUIDRange`). The unit starts
      the bridge as root with `CapabilityBoundingSet=CAP_SETUID CAP_SETGID`
      and `NoNewPrivileges`; the bridge drops to its uid (supplementary
      groups, then every gid, then every uid) before it reads anything, and
      its existing check then refuses to run unless every uid and gid is the
      bridge uid and it holds no capability (`pkg/bridgeproc`,
      `FAAS_BRIDGE_DROP_TO_UID`).
    - Lifetime: the wake's prewarm starts the unit, the idle reaper leaves
      it, and park, destroy, stop, a failed wake and a freeze stop it and
      wait until systemd has unloaded it before the RPC answers (invariant
      4). A thaw starts a new run.
    - **Journal and adoption.** vmmd records each run in the bridge group
      of the instance's version-7 adoption block (`pkg/fcvm/instance_bridge.go`):
      unit, invocation, main process (boot, pid, start ticks), executable
      inode, uid, socket, port and app protocol. A ready instance's block is
      rewritten at once; a waking one gets it with its first block. With
      `adopt_running_vms` and `adoption_record` on, a stopping vmmd leaves
      its units running. The successor, after adopting guests and before
      serving, keeps a unit only if its instance was adopted with a record
      naming it, systemd runs it with the recorded invocation and main pid,
      that pid is the recorded process running the recorded executable as
      the recorded bridge uid in the unit's cgroup and the instance's netns,
      and its socket answers as that uid. It stops every other
      `faas-ibridge-*` unit, including those a crash left. `make leakcheck`
      reports any left behind.
    - Deviations from §2 "Journal and adoption": there is no intent before
      `StartTransientUnit`, because a run the record does not name is
      stopped anyway; a unit that fails the proof is stopped, not
      quarantined, because only vmmd (root) creates `faas-ibridge-*` units
      and refusing the instance's adoption would turn a bridge fault into a
      lost guest (the instance gets a new bridge on its next request); and
      the socket check is a connect with a peer-credential check, until the
      control socket's hello lands in 1d.
    - The bridge group is a new field of the strict version-7 block: a
      reader that predates it rejects the block. Bridge units may be
      switched on only once every node that could read the journal, a
      rollback target included, runs a reader.
    - `faas-tenant-ibridge.slice` (ansible `systemd_slices`) sits inside the
      tenant fence. vmmd reports the slice's `memory.current` as
      `instance_bridge_resident_bytes` in `Stats` and on the capacity stream
      (telemetry, outside the capacity signature, like the instance rows).
      schedd adds it to the node's measured charge (the ADR-909 amendment);
      it reserves nothing and is never billed. An older vmmd omits it, and an
      older schedd ignores it.
    - Not yet: the bridge keeps the `vmmd-stream-bridge` binary and path
      until P5 launches from the release directory, and a failed bridge start
      does not fail the wake (`bridge_start_failed`): the first request
      still starts the bridge, as before.
    - Still owed in 1c: the `instance_bridge_*` metrics of §7. Streams
      through a restarting vmmd still end with it: the RPCs terminate there
      until P2.
    - Per-node switches in ansible: `vmmd_bridge_unprivileged`,
      `vmmd_instance_bridge_sessions` and `vmmd_instance_bridge_unit`
      (default false; roles/vmmd_service/defaults/main.yml) render
      `FAAS_BRIDGE_UNPRIVILEGED`, `FAAS_INSTANCE_BRIDGE_SESSIONS` and
      `FAAS_INSTANCE_BRIDGE_UNIT` as `1` only when on, and node_join writes
      them on every rollout (`zz-faas-bridge.conf`). Units need the
      unprivileged bridge; ansible refuses the unit switch without it.
      Turning units off is safe: a vmmd started with them off stops every
      `faas-ibridge-*` unit it finds before serving (only when systemd holds
      one, so a node that never ran them makes no D-Bus call), where it
      used to leave a predecessor's handed-off units running.
  - 1d: gate and counters move to the bridge, with ADR-1151 property tests.

  After P1, bridges survive vmmd, but streams are still cut, because the
  RPCs still terminate in vmmd.
- **P2 forwardd.**
  - The daemon, the registry reader, and the unix and mTLS listeners.
  - The `compute_nodes.forward_target` migration.
  - Gateways, tcpd, udpd and the schedd smoke test prefer forwardd per
    node, then by default.
  - vmmd rejects Forward*.

  After P2, a vmmd restart cuts nothing, given vmmd adoption.
- **P3 Generations.**
  - `pkg/dataplane/generation`.
  - forwardd first, since it has no legacy.
  - Then gatewayd-internal (spools per generation, DB release, counter
    sharing).
  - Then gatewayd-public (the socket unit, and the tcpd and udpd listeners).
  - The rollout switches to `reload`.

  This phase must converge with the "zero-downtime restarts for non-vmmd
  daemons" work, so the daemons share one generation mechanism.
- **P4 Graceful close.** WebSocket frame trackers, SSE retry, gRPC
  UNAVAILABLE, TCP FIN, then the drain and reboot sequence and the caps.
  This can start alongside P1, because caps close streams abruptly today.
- **P5 Bridge version policy.** Versioned launch paths, skew gauges and
  alerts, in-place roll.
- **P6 Acceptance and default-on.** The fsn-4 test below, then the two-node
  native pair (`make native-m9-acceptance` hosts), then one production
  canary node, then the fleet. Then delete the legacy handlers,
  `FAAS_STREAM_BRIDGE_*` and the old bridge binaries.
- **Later, only if the data asks for it.** Raw session handoff: pass
  quiescent spliced sessions (both FDs, frame-tracker state, meter
  counters) to the new generation, so draining generations never hold
  WebSocket or TCP sessions.

**First slice: 1a.** Run every bridge unprivileged. It is small:

- three spawn functions (`streamBridgeSpawnReal`, `rawBridgeCommand`,
  `namespaceBridgeSpawn`);
- a uid derivation;
- a per-instance socket directory;
- the ansible range reservation;
- an ADR-472 inventory extension for bridge uids.

It removes a live risk, because guest bytes are parsed as root today. Every
later phase needs it. The P0 measurements run alongside it.

## Acceptance (fsn-4; any run needs the owner's OK)

**Fixture.** One service app with `pause: never`, so quiet pause stays out
of the main run, with a WebSocket echo, an SSE ticker, a gRPC
server-streaming method and a declared raw TCP echo listener. A load client
off the node opens 1,000 connections: 400 WebSocket, 300 SSE, 200 gRPC and
100 TCP. Each sends or receives a sequence-numbered message every second.
A second client opens 10 new connections per second for the whole run.

**Run.**

1. 5 min steady state.
2. Reload gatewayd-public.
3. Reload gatewayd-internal.
4. Reload forwardd.
5. Restart vmmd.
6. Roll the instance's bridge in place.

Leave 2 min between steps. Until vmmd adoption lands, step 5 checks only
that streams survive, which requires vmmd's startup to keep running guests
and bridges (ADR-472 already preserves scheduler-live guests). Then:

7. A full release: reload the three daemons and restart vmmd at the same
   time.
8. Five releases back to back, to exercise the generation bound.

**Pass.**

- 0 of the 1,000 long-lived connections are closed or reset. Cross-check
  the client's count against `gateway_ws_active_sessions` and
  `gateway_stream_active`. Neither may dip during a step. After the run,
  `gateway_ws_session_duration_seconds` must show no sessions shorter than
  the run.
- 0 sequence gaps or duplicates.
- 0 failed new connections.
- Echo p99 during each step stays within 50 ms of steady state.
- `dataplane_generations{state="draining"}` returns to 0 once the clients
  disconnect.
- `make leakcheck` reports zero leaked netns, TAPs, jail or bridge uids,
  cgroups, `faas-ibridge-*` units and registry directories.
- `gateway_wake_latency_seconds` and the restore p95 do not regress.

**Negative cases.**

- Shortened caps: WebSocket clients receive 1012, SSE clients reconnect with
  `Last-Event-ID`, gRPC clients get UNAVAILABLE at a message boundary, and
  TCP clients see a FIN.
- Generation bound exceeded: the oldest generation retires gracefully.
- Handoff timeout: the old generation keeps serving and the reload fails.
- Quiet-paused instance during a vmmd restart: new connections wait, then
  succeed.
- Bridge adoption with a forged unit or uid: the bridge is quarantined, not
  killed.

Then the cross-node variant: a gateway on node A and the instance on node B.

## Consequences

- One new daemon (`forwardd`), one new binary replacing four
  (`instance-bridge`), one migration (`compute_nodes.forward_target`), one
  journal version, one uid range and one slice.
- New sections in the spec (§4.1, §4.4, §12, §13 ledger row) and in
  CLAUDE.md "Component ownership": forwardd is the only component that
  carries customer bytes between gateways and instances. vmmd starts bridges
  but does not forward.
- Releases of the data-path daemons stop cutting streams, and a node drain
  is needed only for vmmd (until adoption lands), the kernel and Firecracker.
- Up to 24 h of mixed generations of each data-path daemon. Hop-to-hop wire
  contracts must stay additive for that window.
- Reloading costs memory for the draining generations, bounded by
  `DataPathMaxGenerations`.

## Risks

1. **Bridge memory at density.** Hundreds of bridges per node now count
   against measured admission (the ADR-909 amendment in §2). Mitigation:
   the P0 gate, a minimal bridge
   (small buffers, `GOMEMLIMIT`), and the socket-broker fallback. Density
   must be checked against the financial model's apps-per-node before P1c
   ships.
2. **Generation pile-up.** Frequent releases combined with 24 h sessions.
   It is bounded by `DataPathMaxGenerations` plus graceful retirement and
   alerted; raw session handoff is the escape hatch.
3. **Old code runs up to 24 h after a release.** A security fix in a gateway
   needs explicit retirement (§3, step 5).
4. **systemd mechanics** (`MAINPID` handoff, `NotifyAccess=all`, children
   of `ExecReload`, about 700 transient units per node). Prove them in
   P1c/P3 on fsn-4's systemd version before any fleet rollout. A failed
   handoff degrades to today's behaviour.
5. **Moving ADR-1151 duties** (pause gate, resume, frozen refusal, activity)
   is subtle concurrent code. Port the existing quiet-pause tests, and add
   property tests for "never pause with an open session" and "one resume
   per burst".
6. **Spools and counters shared across generations** in gatewayd-internal:
   a double count or a lost record bills customers wrongly. Per-generation
   files, plus a test that sums across a handoff.
7. **Scope overlap** with the sibling "zero-downtime restarts for non-vmmd
   daemons" design. There must be one mechanism, or §3 is amended.

## Rejected alternatives

- **Keep forwarding in vmmd and rely on adoption.** The RPC streams and
  bridges still end on every vmmd restart, and vmmd cannot run two
  generations (exclusive journal).
- **(B) Socket broker, no per-instance process.** vmmd creates unconnected
  TCP sockets inside the netns and passes them to forwardd with
  `SCM_RIGHTS`. forwardd connects them to the guest itself. Kernel sockets
  survive vmmd, and there is no per-instance memory. It was rejected as the
  primary design for three reasons:
  - one process would parse every tenant's guest-controlled bytes;
  - activity, gate and teardown would live in forwardd and be lost on its
    crash;
  - vmmd would have to keep each instance's socket pool filled.

  It is the named fallback if P0's memory gate fails.
- **Dial the published `HostIP:8080` from forwardd.** The host can already
  reach the guest this way through the per-netns DNAT; health probes use
  it. Three problems:
  - only :8080 is published, while raw upgrades, ADR-183 TCP listeners and
    UDP target other ports;
  - new flows would count against the forward-chain conntrack cap
    (`DefaultConntrackCap` 4096), which the tenant's egress shares;
  - the activity choke point that survives vmmd would be lost.
- **Gateways dial bridges directly.** This works only on the same node.
  Cross-node, it either puts bridges on the overlay or routes through the
  remote gatewayd-internal, a large process with DB access that changes
  often, kept on every stream's path.
- **One systemd template unit per generation** (`faas-x@<gen>.service`).
  Each generation would get its own cgroup and limits, but it breaks
  `.socket` units and the daemon registry, and listener continuity would
  still need FD passing. An in-unit generation started by `ExecReload` gets
  the same current environment with less churn.
- **`SO_REUSEPORT` handover.** A closing listener resets its accept queue
  unless `tcp_migrate_req` or eBPF steering is set up, and the option does
  not apply to unix sockets. FD passing covers every listener type.
- **Longer drain grace instead of generations.** A drain of 24 h makes a
  release take 24 h.

## Out of scope

- VM adoption by vmmd (`20261017-vmmd-restart-adoption.md`).
- Caddy, cloudflared and Cloudflare restarts. Run two connector replicas and
  withdraw before drain (ADR-1343).
- Restarts of platform stream servers (realtimed, livequeryd). They own a
  resume protocol; only their gateway hops are covered here.
- The apid and control-API listeners.
