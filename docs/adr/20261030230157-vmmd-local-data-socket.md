# ADR-20261030230157 · vmmd local data socket for the node's own gateway

- **Status:** Proposed
- **Date:** 2026-10-30
- **Amends:** [ADR-015](015-unix-socket-auth-v1.md), which rejected an
  `SO_PEERCRED` check while every daemon shared one uid, and
  [ADR-025](025-decoupled-control-plane-and-compute.md) / ADR-052, under which
  a multi-box vmmd serves every caller over TCP with mTLS. Callers on other
  nodes, and every RPC outside the list below, keep mTLS.
- **Switches:** vmmd `local_data_socket` / `FAAS_VMMD_LOCAL_DATA_SOCKET` and
  gatewayd-internal `FAAS_GATEWAY_LOCAL_VMMD_SOCKET`. Both are off by default.

## Context

Every request a compute node serves crosses gatewayd-internal → vmmd on the
same host. vmmd binds a single gRPC listener: `/run/faas/vmmd.sock` when
`listen_addr` is empty (single box), otherwise TCP with mTLS. Multi-box nodes
use TCP, so a gateway reaches the vmmd beside it through a TLS session on the
overlay address, and both daemons encrypt and decrypt every request and
response byte.

Measured in production on 2026-10-25 (100 req/s on a 256 MB Node app,
`h10-hello-node`; CPU profiles attributed by package):

- TLS was 9% of gatewayd-internal's samples and 13% of vmmd's.
- Per request, gatewayd-internal used ~2.2 ms of CPU (after `GOGC=200`) and
  vmmd ~1.7 ms. The guest itself used ~2.1 ms. More than two thirds of a
  hello-world request's node CPU is platform hops.

The single-box socket cannot simply be turned on beside TCP:

- `pkg/vmmdgrpc/caller_policy.go` and `authorizeSchedulerPeer` trust any unix
  peer that has no `AuthInfo`. That is the ADR-015 model: membership of group
  `faas` is the boundary.
- On a multi-box node that would hand the scheduler-only RPCs (exec, destroy,
  runtime admission) to every process in group `faas`.

What has changed since ADR-015 rejected peer credentials: daemons no longer
share one uid. schedd runs as `faas-schedd`, builderd as `faas-builderd`,
gatewayd-internal as `faas` (`pkg/daemonunitspec`). A uid now identifies a
daemon on a compute node.

## Decision

1. **A second listener, serving only the gateway's RPCs.** With
   `local_data_socket` on, vmmd also serves `/run/faas/vmmd-data.sock`
   (`api.VmmdLocalDataSocket`, created like `vmmd.sock`: group `faas`, mode
   0660). It is a separate `grpc.Server` with the same service implementation
   and these options (`vmmdgrpc.LocalDataServerOptions`):
   - **Peer-uid authentication.** The transport handshake reads the peer's
     uid with `SO_PEERCRED` before any byte is read and admits only the uid of
     `api.GatewaydInternalUser`. The connection's `AuthInfo` is a
     `LocalDataPeer`, never nil, so the unix-socket shortcuts above never
     apply to it, and `wire.PeerCN` fails on it.
   - **An allowlist of methods.** Only the RPCs gatewayd-internal calls are
     served: `ForwardHTTPStream`, `ForwardRawStream`, `ForwardTCPStream`,
     `ForwardUDPStream`, `Ping`, `AllowResolvedEgress`, `AttachAppTask`,
     `InflightRequestTrace`, `Logs` and `ResolveServiceCaller`. Every other RPC
     answers `PermissionDenied`. A test scans gatewayd-internal and
     `pkg/gateway` for vmmd RPC calls and fails when one is missing from the
     list.
2. **Off removes the socket.** With the switch off, vmmd deletes a socket an
   earlier run left at startup. A socket that cannot be set up is logged and
   removed; vmmd still starts and the gateway keeps mTLS.
3. **The gateway uses it for its own node only, while it exists.** With
   `FAAS_GATEWAY_LOCAL_VMMD_SOCKET=1`, gatewayd-internal resolves its own node
   (`FAAS_NODE_NAME`) to `unix:///run/faas/vmmd-data.sock` and dials it
   without TLS, but only while that path is a socket. Other nodes keep the
   registry target and mTLS. Every `compute_node_changed` heartbeat
   re-resolves cached targets (`EvictIfChanged`), so the gateway moves to the
   socket when vmmd starts serving it and back to mTLS when it goes away.
4. **Measured rollout.** Both switches start off. Turn vmmd's on per node
   (it takes effect at the next vmmd start), then the gateway's on the same
   node, and compare gateway and vmmd CPU per request with a node that keeps
   mTLS.

## Security

- **Who can call what, compared with today.** Over mTLS, any process that
  can read a fleet daemon key may call every RPC outside the scheduler-only
  set. gatewayd-internal's vmmd client key is `root:faas 0440`
  (`gatewayd_internal_service`), so that is every process in group `faas`.
  The local data socket admits one uid, `faas`, and ten RPCs. It is narrower
  than the access those processes already have.
- **Same host only.** A unix socket cannot be reached from another node, and
  the internet-facing listeners stay where they are. Cross-node calls keep
  mTLS and the caller policy of bug hunt H17.
- **No change for the scheduler.** schedd and builderd never connect to the
  socket (wrong uid), and the scheduler-only RPCs are not served on it.
- **What is lost.** Requests on this hop are no longer encrypted on the host.
  They never left it: a local attacker who can read loopback traffic or
  another daemon's memory is root or already holds the `faas` group key.

## Consequences

- One more listener in vmmd, the only root daemon. It is off by default, and
  turning it off removes it.
- Expected saving: the TLS share of gatewayd-internal's and vmmd's CPU for
  same-node traffic, ~0.4 ms of CPU per request from the 2026-10-25 profile.
  Not yet measured with the socket on.
- Traffic that crosses nodes (an instance on the other node) is unchanged.
- A new RPC the gateway starts calling must be added to the allowlist; the
  scanning test fails until it is.

## Verification

- `pkg/vmmdgrpc`: the handshake admits only the configured uid and records a
  `LocalDataPeer`; the allowlist serves only the gateway's RPCs, and the
  scheduler-only RPCs are refused even on the socket; a real-socket round
  trip on Linux; the scanning test.
- `cmd/vmmd`: the switch parses, off removes a stale socket, a setup failure
  removes the socket and keeps vmmd up.
- `cmd/gatewayd-internal`: the own node resolves to the socket only while it
  exists, other nodes keep their target and TLS.
- In production: `/run/faas/vmmd-data.sock` exists on the canary node,
  `ss -xp` shows gatewayd-internal connected to it, and gateway and vmmd CPU
  per request drop on that node against a node on mTLS.
