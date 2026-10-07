# ADR-621: Selected systemd activation audit

Status: accepted · 2026-10-07

## Context

ADR-615 leaves unreachable historical public startup sessions pending. ADR-620
connects selected origins to live main processes, but an activated socket may
remain open in PID 1 after that main process exits. An inactive service, vanished
PID, failed identity probe or removed DNS/Caddy entry cannot establish a fence.
Before designing external fencing receipts, collect explicit evidence about
selected service activation, retained sockets and existing TCP endpoints.

## Decision

Add private read-only `edgetopology.NativeActivationProbe.Observe`. There is no
daemon, CLI, API, database or withdrawal-store integration. Freeze the entire
review before opening any native resource: ADR-620 host identity and directly
assigned addresses, one historical `NativeServiceReview`, and one to sixteen
distinct canonical `.socket` unit names. Keep the historical invocation, PID,
start ticks, executable digest and cgroup inode as caller-reviewed attribution;
the collector does not reconstruct a disappeared startup session or certify
that the selected socket list is complete. Check every TCP address in that
historical service review, including its private control port.

Production collection requires local Linux procfs, unified cgroups v2, and the
same protected root-owned systemctl used by ADR-620. It never targets a remote
host, runs a shell, starts/stops/masks a unit, closes a socket or kills a process.
Read-only systemd `show --all` includes empty properties; absence of a requested
property fails rather than becoming an inferred zero. Require exact unit ID,
masked load state, persistent `UnitFileState=masked`, inactive/dead state, no
pending job, zero control PID, empty current control group, and no pending daemon
reload. A service must also have zero main PID and zero stored descriptors.
Alias IDs, runtime-only masks, disabled or missing units and unknown properties
fail. Reuse the bounded command writer without embedding an `io.ReaderFrom`
implementation; suppress arbitrary command and operating-system error text.

For each service/socket, independently inspect its exact persistent mask at
`/etc/systemd/system/<unit>`: root-owned symlink to exactly `/dev/null`, protected
root-owned directory ancestors, and the actual Linux null character device.
Compare mask device/inode, ownership, mode and modification/change times around
the systemd read. Retain only mask device/inode in the observation. Gregale's
locally installed unit files may need operator-reviewed relocation before such
a mask can exist; this collector performs no relocation or deployment change.

Inspect the reviewed historical cgroup through the contained native cgroups
root. Only ENOENT at both directory lookups counts as absence. If present,
retain a directory handle, require the exact historical inode, and read bounded
`cgroup.events`: `populated=0` and `frozen=0`. Population covers the descendants,
so checking only `cgroup.procs` or the main PID is insufficient. Reject read or
permission errors, populated descendants, frozen groups, malformed events,
replacement and disappearance while reading. Compare the retained directory to
the current contained path before returning.

Each snapshot checks host machine/boot IDs, host/self PID and network namespaces
and selected assigned addresses at both ends. Bracket complete bounded TCP and
TCP6 table reads with unit/mask/cgroup observations. Reject ANY row overlapping
a reviewed local endpoint, in any TCP state: listeners held by PID 1 or other
processes, established HTTP/upgraded traffic, half-closed connections and even
TIME_WAIT conservatively keep the audit unverified. Unmap IPv4-mapped TCP6
connection addresses before comparing IPv4 endpoints. Wildcard overlap remains
conservative across address families because procfs does not expose V6ONLY.
Unrelated ports or disjoint exact addresses stay outside the selected scope.

Collect two equal snapshots within one bounded session. Internal unit/cgroup
bookends must match too. A later failure or drift returns the zero observation,
never partial, cached or stale success. Always release native roots. Non-Linux
returns unverified before attempting systemd or native filesystem collection.
Reuse ADR-620 byte/row/time budgets; new review socket limit is 16 and total audit
timeout is 60 seconds, centralized in `pkg/api/limits.go`.

## Consequences and limits

The result identifies selected persistent masks and empty historical cgroup
state, and reports two snapshots without selected TCP endpoints. It addresses
the PID 1 socket-retention blind spot without needing an exhaustive process/FD
holder census. These observations are **not external fencing receipts**: root
can unmask/reactivate units or create another listener after collection, and
equal snapshots do not exclude ABA changes between reads. There is no lease,
transactional activation ownership or immutable host shutdown guarantee.

No escaped-process census, all-unit activation inventory, other network
namespace coverage, UDP/QUIC, NAT, redirect/BPF, external network isolation or
future reachability guarantee follows. Caller-reviewed historical identities
are not authenticated startup attribution. An unreachable host cannot return
these observations and remains pending. The DTO cannot clear an ADR-615
withdrawal, authorize retirement, prove VM quiescence or delete artifacts.

The next fencing slice needs an independently verified host/network authority
that pins the exact withdrawal/startup and host epoch, covers existing traffic
and all reviewed activation/ingress paths, and prevents reactivation for its
whole lifetime. Receipt publication must use both intent heads and exact
withdrawal fencing; a timeout or local mask snapshot must never substitute.
Private flags stay default off and public `execution_available=false`.

## Validation and adversarial review

Portable synthetic readers exercise the production common capture logic and
strict systemd/cgroup parsers: both masked units, missing/ambiguous properties,
retained PID 1/unrelated listeners, all TCP states, mapped dual-stack connections,
populated/frozen/replaced/absent cgroups, complete-table failures, mutation within
and between captures, frozen review inputs, cancellation, sanitization and
resource closure. Existing ADR-620 origin/service tests cover the shared native
host-reader refactor. Normal and race tests, Linux amd64 compilation, focused
Linux lint and repository policy gates are required locally.

Local validation passed 95 top-level tests in normal mode and the same 95 with
the race detector across edge topology and ingress, with no failures or skips.
Linux amd64 test-binary compilation succeeded, and focused Linux lint including
tests reported zero issues. Runbook SQL, text encoding, shell quoting and ADR
number uniqueness gates passed. These are local code checks, not native systemd
or production acceptance.

Adversarial pass: treat MainPID=0 alone, runtime masks, retained descriptor stores,
IPv4-mapped accepted sockets, populated descendants, permission errors mistaken
for absence and successful first reads followed by drift as rejection cases.
Avoid stopped-service drain claims: masks do not enforce a permanent external
fence. No production unit or customer traffic changes occur during validation.
Native systemd/procfs/cgroup acceptance on Linux is still required; compilation
and synthetic readers do not qualify the production collector. The larger
runtime upgrade still requires native Caddy/DNS/systemd acceptance, Linux amd64
KVM `test-metal` and final `leakcheck` before enablement.

Primary references:

- [systemd mask semantics](https://github.com/systemd/systemd/blob/main/man/systemctl.xml)
- [systemd socket activation](https://github.com/systemd/systemd/blob/main/man/systemd.socket.xml)
- [systemd properties](https://github.com/systemd/systemd/blob/main/man/org.freedesktop.systemd1.xml)
- [systemctl Job formatting](https://github.com/systemd/systemd/blob/main/src/systemctl/systemctl-show.c)
- [recursive cgroup population](https://www.kernel.org/doc/html/latest/admin-guide/cgroup-v2.html#un-populated-notification)
- [native TCP tables](https://www.kernel.org/doc/html/latest/networking/proc_net_tcp.html)
