# ADR-20261109130000: Reset an app guest's TCP connections before destroying it

- Status: Accepted
- Date: 2026-11-09
- Related: ADR-159 (idle park reuses the retained capture), ADR-1151 (quiet
  pause and the frozen tier), ADR-20261107160000 (snapshot contract for plain
  apps), STATUS "PostgreSQL starter checkpoint drain — 2026-10-17"

## Context

vmmd destroys a guest by killing Firecracker and deleting its network
namespace. The guest kernel never sends a FIN or RST, and the namespace's
conntrack entries go with it, so nothing tells the app's peers that the
connection is gone. A database, cache or upstream keeps each session until its
own TCP keepalive gives up: two hours plus probes on Linux defaults.

Idle park reuses a retained capture without recapturing (ADR-159) and destroys
the serving guest, so this happens on almost every park. An app with a pool of
ten database connections that parks a few times an hour leaves hundreds of
orphaned server sessions and can exhaust `max_connections` on the customer's
own Postgres, MySQL or Redis. Advisory locks, `LISTEN` registrations and temp
tables held by those sessions stay held. Gregale's own Postgres starters hit the
capture-time version of this on 2026-10-17 and were fixed with a
`before_checkpoint` drain, which plain apps do not have.

The guest kernel (Firecracker's 6.18 guest config) builds in
`CONFIG_INET_DIAG_DESTROY`, so guest-init can abort sockets with sock_diag
`SOCK_DESTROY`. The kernel's `tcp_abort` sends an RST for a connection that
still has a peer, and the app sees `ECONNABORTED`.

## Decision

Before teardown destroys an app guest that may have served traffic, vmmd asks
guest-init to reset the guest's off-guest TCP connections, then destroys it.

- **Wire.** Message type 8 on the resume listener (`pkg/connreleaseproto`),
  empty body. guest-init dumps IPv4 and IPv6 TCP sockets in ESTABLISHED,
  SYN_SENT, SYN_RECV, FIN_WAIT1/2, CLOSE_WAIT, LAST_ACK and CLOSING, destroys
  each whose peer is not loopback or unspecified, and replies OK plus the
  count. Loopback peers are the app and its sidecars, which die with the VM.
  LISTEN, TIME_WAIT and CLOSE sockets have no peer state to free.
- **Budget.** guest-init stops after 400 ms and replies with what it reset;
  vmmd waits at most 600 ms. Any failure (an older guest-init answers a
  single non-zero byte, a kernel without the option, a timeout) is logged at
  debug and the destroy goes ahead. A successful reset that freed anything is
  logged at info with the count.
- **Which guests.** App guests torn down through `Manager.Destroy`, which
  includes idle park with a retained capture, frozen-tier park, scale-in and
  app deletion. Not builders, exports, one-shot executions, data repair or
  qualification VMs, and not warm-pool spares: a spare holds only its
  capture's sockets, whose sessions belong to the VM that took the capture.
- **Paused guests.** A quiet-paused guest is resumed first, as `SignalAndKill`
  already does. A frozen guest is resumed with a plain VM resume (no probes,
  no resume hook) so its kernel can send; the destroy follows at once.
- **Switch.** `disable_release_connections_on_destroy` in `vmmd.toml`;
  `FAAS_RELEASE_CONNECTIONS_ON_DESTROY` overrides TOML in either direction.
  On by default.

## Consequences

- Peers free the app's sessions as soon as a guest is destroyed, instead of
  after their keepalive.
- Teardown of an app guest takes up to 600 ms longer, usually a few
  milliseconds. Park is not on the request path.
- A frozen guest's memory that the host swapped out faults back in only as far
  as the network stack touches it.
- Not covered: the guest that takes a capture (park with capture, deploy
  prime) is paused for the snapshot and torn down by `Park`; its sessions are
  still orphaned once per capture. Resetting them before capture would put
  aborted sockets into the snapshot that every restore inherits, which needs
  its own decision. A guest whose vmmd or node dies also sends nothing.

## Validation

- Unit (any platform): sock_diag request layout, state mask, peer parsing for
  IPv4, IPv6 and v4-mapped addresses (`guest/init/release_connections_test.go`);
  host wire, older-guest reply and eligibility (`pkg/fcvm/release_connections_test.go`);
  config switch (`cmd/vmmd/config_test.go`).
- Linux as root, in a private network namespace: a connection to a non-loopback
  address is reset and a loopback one is not
  (`guest/init/release_connections_linux_test.go`).
- Metal, before release: park an app holding a Postgres pool and check that
  `pg_stat_activity` drops its sessions at the park, not two hours later.
