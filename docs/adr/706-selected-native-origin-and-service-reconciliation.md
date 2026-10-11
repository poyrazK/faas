# ADR-706: Selected native origin and service reconciliation

Status: accepted · 2026-10-07

## Context

ADR-705 observes selected DNS-only RRsets and delegation. ADR-703 inventories the
declared Caddy graph, and ADR-702 verifies participating public startup tuples.
None connects an origin address or configured socket to its actual Linux service
invocation and process. Socket activation can leave PID 1 holding the same socket
across main-process replacement. A socket address alone is insufficient evidence.

## Decision

Add private read-only `edgetopology.NativeOriginProbe.Observe`, without daemon,
CLI, API or database wiring. Freeze the complete explicit review before I/O:
ADR-705 DNS review, full Caddy digest, selected direct A/AAAA origin links and
selected proxy/backend startup tuples, plus one local host and its reviewed
systemd main processes. Each process pins unit name, nonzero invocation ID, PID,
start ticks, all four UID credentials, executable SHA-256, exact unified cgroup
path/inode and the complete TCP LISTEN address set held by that main process.
The host pins machine/boot IDs, PID/network namespaces and directly assigned
origin IP/interface/index/prefix tuples. No process, service, DNS endpoint or
origin disappears because collection fails.

Production sessions require Linux procfs and cgroups v2, a root-owned systemctl
binary and protected ancestors, and retained pidfds plus procfs directory handles
for every selected process. Non-Linux fails before network access. Keep these
handles until the whole observation ends; poll them without signaling processes.
Use only fixed local paths and local systemd `show`, with explicit properties,
clean environment, no shell, password prompt, remote host or service mutation.
Bound command time/output and suppress arbitrary command/OS error content.

Capture native facts before and after network observations. Within each capture,
check host identity/namespaces/assigned addresses at both ends, and check service
invocation/process/cgroup/executable around descriptor enumeration. Read both
native-endian `/proc/self/net/tcp` and `tcp6`, including non-listening rows for
bounded complete parsing. Match LISTEN socket inodes to every enumerated main
process descriptor. Require the exact reviewed TCP listener set; reject missing,
extra, duplicate and separate overlapping listener inodes. Allow multiple FDs
holding one inode and other holders of that SAME activated socket, including
PID 1. Hash the bounded open kernel executable target and compare its identity,
size, mode, ownership and modification/change times around the read. Access time
changes are immaterial. Pin the native cgroup directory inode through a contained
read-only root. No command line, environment, private key or raw executable bytes
escape in results.

Bracket fresh ADR-705 served-DNS collection and nonce-bound backend identity
probes with full Caddy GET config/strong-ETag reads and these native captures.
Require the reviewed exact whole-config digest at the first read and equal
digest/ETag at the second. After backend probing, collect another complete fresh
ADR-704 provider inventory and require its digest to match the served-DNS
inventory; retain that final provider bookend in the result. Require every
selected A/AAAA RRset value to have at least one reviewed origin link, with no
extra values, and every linked IP to be
directly assigned to the reviewed host. Link each explicit declared Caddy
server/listener to a held native Caddy TCP socket, including the actual literal
loopback admin endpoint. For wildcard bindings, verify only the native socket's
address family; never infer IPv4 coverage from an IPv6 wildcard. Since procfs
does not expose IPV6_V6ONLY, conservatively reject a separate IPv6 wildcard inode
competing with an IPv4 selected bind. NAT/unassigned/floating origins require a
separate adapter and cannot borrow this proof.

Require each selected proxy to belong to a reviewed origin server and to have
the exact supported ADR-702 upstream set. Each backend must name a distinct
reviewed service holding that exact loopback TCP socket and pass a fresh signed
startup identity probe. Account for every service in the review, including Caddy;
reject unused or conflicting scopes. Preserve the entire declared Caddy graph
alongside these explicitly SELECTED links and proxies. Do not reuse the all-proxy
ADR-703 binding result type: unrelated S3 or other services remain visible and
unverified. This is an inventory relationship, not evaluation of effective Host,
path, method, header, TLS/SNI or ordered route reachability.

Return the frozen review, fresh DNS/Caddy evidence, both equal native captures
and completion time. Any failure returns zero observation. All bounds live in
`pkg/api/limits.go`: 16 services, 256 held TCP listeners/origin links/host address
entries, 16,384 FDs per capture, 32,768 total TCP rows, 8 MiB per TCP table,
64 KiB metadata, 16 KiB command output, 256 MiB per executable, 15-byte interface
names, 30 seconds per native capture, 90 seconds overall, two seconds per local
systemd call and one-second command wait delay. Existing Caddy/DNS/backend limits
also apply. Context checks bound user-space work; kernel/filesystem stalls can
still delay synchronous native reads.

## Consequences

Selected directly assigned DNS-only origins can now be reconciled with native
Caddy listeners and participating public main-process service identities.
Equal bookends do not exclude ABA or later changes. Service invocation/cgroup
checks do not inventory all unit children, prove exclusive socket ownership or
pin future systemd activation. A shared activated socket and its signed response
do not uniquely attribute that response to the reviewed PID. Native TCP holdings
do not establish ingress reachability, acceptance of traffic or completion of
existing connections. UDP/QUIC, generated Caddy routes, kernel redirect/NAT/BPF
paths, other hosts/namespaces, raw IP bypasses, all zone/platform records, proxied
origins and effective aliases/flattening remain outside these SELECTED links.
Plaintext DNS endpoint trust assumptions from ADR-705 remain unchanged.

There is no publication of roster facts, future lease, fencing/drain receipt,
withdrawal clearance, VM quiescence or predecessor retirement authority. No
production host is probed by local tests. Private execution remains unavailable
and deployment flags stay off; no PR, push or deployment. Portable synthetic
reader, provider, TCP DNS and HTTP identity fixtures establish contracts only.
Linux amd64 compilation is required locally; native Linux/systemd/Caddy/DNS
acceptance and KVM `test-metal` plus final `leakcheck` remain pending before
enablement. Next work is external host/socket/network fencing for unreachable
historical startup sessions, including durable socket-activation ownership.

References: [Linux procfs](https://www.kernel.org/doc/html/latest/filesystems/proc.html),
[native TCP tables](https://www.kernel.org/doc/html/latest/networking/proc_net_tcp.html),
[systemd service properties](https://www.freedesktop.org/software/systemd/man/latest/org.freedesktop.systemd1.html),
[systemd invocation IDs](https://www.freedesktop.org/software/systemd/man/sd_id128_get_machine.html).
