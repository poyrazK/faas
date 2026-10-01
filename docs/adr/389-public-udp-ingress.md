# ADR-389 · Public UDP ingress

- **Status:** proposed
- **Date:** 2026-09-30

## Decision

Extend the opt-in public edge with app-owned UDP endpoints. Keep API customer
intent writes in apid, instance admission/wake in schedd, and guest-network
socket creation in vmmd. UDP forwarding must preserve one datagram per message;
the TCP byte stream bridge cannot substitute for a datagram transport.

Use a bounded per-peer session keyed by endpoint identity and client address.
Each admitted session owns a connected guest UDP socket and returns replies
only to that peer. Teardown, endpoint reassignment, idle expiration, and instance
loss close the session. No transparent source-address spoofing or host-network
access is introduced. A parked app must pass normal admission before forwarding.

The helper pipe representation is a four-byte big-endian payload length followed
by exactly that payload. Empty records represent real empty datagrams. Reject
partial records and payloads larger than the IPv4 guest network permits before
allocating their bodies. Transport errors terminate the affected session;
records are never reinterpreted as byte streams or silently fragmented.

## Required implementation and acceptance

The framing primitive, connected UDP pipe helper, durable API/CLI and opt-in
public gateway wiring are implemented locally; production acceptance is pending. Socket tests cover empty/binary datagrams, cancellation
of blocked I/O, and rejection of truncated records without forwarding partial
payloads. Full-size socket round trips run on Linux; macOS uses a smaller
payload because its host UDP send limit differs. Framing tests retain the full
guest payload limit on every platform.
The VMMD protobuf schema now defines initialization separately from oneof
datagram messages. Protobuf round-trip tests require empty datagram presence to
survive encoding in both directions. VMMD now implements the handler for a live instance namespace, reuses the
namespace-helper spawn/readiness lifecycle, and tracks activity during the peer
session. It rejects repeated initialization and incomplete datagrams and applies
independent per-direction byte and message budgets. Empty datagrams count toward
the message budget; caller-provided caps can only lower the receiver defaults.
Request EOF terminates the UDP peer session rather than half-closing it. The
public edge uses the transport through the supervisor and gateway wiring below.
Remaining work includes
PostgreSQL execution, native acceptance and firewall rollout. Set all resource limits in the shared API limits table.

Native acceptance must cover cold wake, restore, multiple peers, empty/binary
and maximum-size datagrams, oversized/truncated messages, loss/reordering,
endpoint disable/reassignment, idle expiry, quota pressure, node loss, and leak
cleanup. Reflection/amplification controls and source-CIDR exposure must be
validated before enabling UDP publicly. This ADR remains proposed until those
integration choices, deployment and acceptance are complete. Public UDP remains
disabled by default.

The gateway now has a peer forwarding seam that resolves the chosen compute
node, sends initialization, forwards one protobuf datagram per peer message,
and independently checks directional caps. Activity in either direction resets
its idle timer. Cancellation and idle expiry cancel both directions. The peer
interface requires cancellation-aware Send/Receive and retains ownership of the
shared public socket. Concrete public UDP listeners, admission, durable intents and rate controls are
implemented below; deployment exposure remains pending. Gateway peer-forwarding tests pass locally after rebuilding in a task-owned
cache. Native verification remains required.

`udpd.Peer` supplies the concrete cancellation-aware queue contract. Each peer
has a small fixed inbound queue, drops new packets when full, copies retained
payloads, and associates replies with the originating client address and peer
context. Queued replies become invalid when their peer closes. A shared peer
pool enforces both global and account-scoped session limits before queue
allocation and makes releases idempotent. The UDP socket server now integrates these primitives for one immutable
app-listener identity. It filters source prefixes before admission, rejects
truncated packets, resolves one target per peer, isolates client reply addresses,
and stops all peers on cancellation. Its shared reply queue is bounded and
writes have deadlines. Socket tests cover two clients, empty datagrams, shutdown,
and fail-closed empty source allowlists. The supervisor and production wiring below connect this server to durable
routing.

The socket server now applies independent inbound/outbound account packet and
payload-byte token buckets. Empty datagrams consume packet credit. Entries
survive peer churn, and a bounded account cache refuses new accounts while full
rather than evicting active budgets. Idle entries may be reclaimed after their
credit would already have fully refilled. Source filtering and inbound rate
checks precede peer allocation/admission. Outbound rate checks precede socket
writes. A supervisor must share one RateLimits and PeerPool across its sockets
so extra listener ports do not multiply budgets. Limits live in pkg/api/limits.go.

Durable UDP endpoint storage now uses an append-only `app_udp_listeners`
migration and separate PostgreSQL/in-memory store interfaces. Public UDP port
uniqueness is independent of TCP. App/account ownership is checked during
creation, names and ports are validated, and disabled or deleted-app records
cannot resolve as public endpoints or enter the enabled bind set. Records
start disabled unless explicitly enabled. In-memory lifecycle, namespace,
collision, ownership and deleted-app tests pass. PostgreSQL 16 store and migration
tests now pass against an isolated local cluster without skips, including
concurrent cross-app port reservation, independent TCP/UDP namespaces, deleted
app routing exclusion, SQL disabled defaults and database check constraints. The HTTP API and Go client now support list, create, enable/disable and delete, using the existing read/deploy scopes, MFA and app ownership checks. Creation is disabled by default, and enabling revalidates the declared UDP port. HTTP lifecycle, duplicate, stale-manifest and cross-account tests pass. The CLI supports `gregale apps udp <slug> [list|add|enable|disable|rm]` and `gregale app <slug> udp ...`, including JSON output. Creation explains that the endpoint starts disabled. CLI HTTP-contract tests pass. The production wiring described below is implemented; deployment and native
acceptance remain outstanding.

The UDP supervisor now reconciles enabled durable identities, shares one peer
pool and rate limiter across all ports, and cancels and joins old peer sessions
before binding a replacement owner. Disabled/deleted endpoints close their
sockets and peers. Failed socket servers are retried during reconciliation.
Initial readiness requires every desired socket to bind. Local socket tests
cover reassignment, deletion and a shared peer cap across ports. Production
scheduler admission, source-policy configuration and shutdown wiring are described
below. Native acceptance remains pending.

The production gateway now wires the supervisor to durable PostgreSQL intents,
authoritative app/intent validation and schedd admission. Each new peer checks
current account ownership, enabled state and the UDP declaration before selecting
a live instance or requesting wake. Admission has a bounded deadline. A single
atomic selection cursor avoids an unbounded per-app cursor cache. The public
process requires `FAAS_UDPD_ENABLED=1` and a nonempty explicit IPv4 source-CIDR
allowlist; it uses independent schedd/vmmd TLS configuration. Cancellation closes
all UDP sockets and joins peers before transport dependencies close. UDP shutdown
has no TCP half-close drain. Deployment templates now load `/etc/faas/udpd.env` with UDP disabled by default.
The gateway and nftables roles use the same explicit IPv4 client CIDRs; no
source list yields no UDP firewall rules. The bridge helper is now in both
Packer builds and the signed release support catalog, and the e2e harness
provides its release-local path. Deployment-render tests and the default
Go/Jinja firewall cross-check pass. Live firewall rollout and native cold/restore acceptance remain pending.

UDP telemetry is now exported through the public gateway's existing operator
metrics gatherer. Fixed label sets cover active peers/sockets, started/completed
peers, duration, packet/payload totals, source/truncation/rate/peer/queue/write
drops and reconciliation failures. No client addresses or arbitrary identity
labels are emitted. Local socket tests assert empty-datagram accounting and zero
active gauges after cancellation. Completion accounting runs before peer cleanup
and before the server's join completes. Prometheus alerts cover sustained
reconciliation failures, admission/forwarding errors and resource-pressure drops;
source rejection and normal idle expiry are excluded from peer-failure alerts.

`make udp-postgres-check` now requires a test `DATABASE_URL` and rejects skip,
missing-test and failure verdicts. The required test names are derived from the
UDP store and migration source files so adding a test cannot silently leave it
out of this gate. This gate validates durable storage on PostgreSQL; it does not
substitute for native KVM networking, cold wake, restore or leak acceptance.

The native container lane now selects `TestUDPIngressMetal` from its source
file. It uses the actual public gateway, scheduler and VMMD namespace helper,
requires durable cold-boot then restore wake-method evidence, and checks distinct
peers, empty/binary/full-size datagrams, disable/re-enable and delete. Disable
and delete require reacquiring the same kernel UDP socket, rather than treating
a lost reply as proof of closure. The fixture
runs HTTP health readiness alongside its UDP socket. Native execution, packet
loss/reordering, oversized transport messages, quota pressure, reassignment,
node loss and leak acceptance remain pending; the test's presence is not a pass.

A portable composition test now drives real loopback UDP sockets through
in-memory listener intents, the actual target resolver and gRPC protobuf
transport. Scheduler RPC and guest namespace execution are substituted. It
verifies one admission-seam call, running-instance reuse, two isolated peers,
empty/binary datagrams, directional caps in initialization, malformed-response
isolation, and cancellation of guest streams plus kernel socket release after
disable. This strengthens local integration evidence but does not prove native
cold wake or restore. Cancellation during a supervisor's in-flight state read is
now treated as ordinary shutdown, with no reconciliation-error metric or log.

Gateway boundary tests now explicitly reject oversized datagrams in either
direction, independent directional byte caps, repeated readiness frames and
empty-datagram message-credit exhaustion. A guest that emits two empty replies
to one request is limited by its outbound message budget independently of the
inbound request count. Rejected payloads never reach the public peer. Unexpected
remote gRPC cancellation now retains its failure status; only clean local peer
EOF treats the resulting receive cancellation as successful teardown. The full
gateway and UDP race suites pass after that fix. Native acceptance remains
required for the actual guest namespace and VM lifecycle.

ResourceExhausted completion statuses now use the fixed `resource_exhausted`
outcome rather than `forward_error`, and the public gateway logs them as warnings.
This includes peer byte/message caps and transport resource limits. A dedicated
sustained resource-pressure alert keeps these events visible without reporting
normal enforcement as a transport failure. A socket-level regression asserts
one resource completion, zero forward errors and zero active peers after cleanup;
alert scenarios require resource pressure to fire its own alert while leaving the
peer-failure alert inactive. UDP race tests, public-gateway tests and rule checks
pass locally. Native acceptance remains unverified.

Peer admission now requires the listener's app ID and a live instance/node
identity before forwarding. Results returned after admission cancellation or its
deadline are rejected. Socket regressions verify that invalid admission never
reaches the forwarder and releases its reserved peer slot; the UDP race suite
passes. No native acceptance host is currently available, so namespace execution,
VM cold wake/restore and leak qualification remain pending.

The portable socket/gRPC composition case also reassigns an active public port
to another app. It requires both old streams to terminate, verifies that the same
client sockets subsequently reach instances owned by the replacement app, and
checks one admission per app before disabling and releasing the endpoint. Five
race-enabled repetitions pass locally. This verifies supervisor reconciliation
and target ownership across the substituted scheduler/guest boundaries; native
reassignment and node-failure acceptance remain pending.

New UDP peer sessions respect app maintenance mode before running-instance
selection or scheduler admission. Portable regressions verify no wake while
maintenance is enabled, rejection even with an existing running instance, and
routing recovery after maintenance is cleared. Established sessions retain their
normal bounded lifetime; disable the listener to cancel them immediately.
