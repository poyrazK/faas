# ADR-769: Observe native runtime under the inventory barrier

Date: 2026-10-06
Status: Implementation in progress; first-fence reconciliation and native acceptance pending.

## Context

Startup recovery quarantines physical leases once, but its cached ready flag is
not a fresh machine or resource inventory. A requested footprint and a journal
pathname cannot establish the loaded guest, current kernel ceilings, occupants,
or an orphan scope. Recovery must inspect a live original task even after its
producer daemon has restarted or its launch authority has been fenced. Giving
that inspection producer authority would allow an old task to resume or mutate
resources under a new daemon generation.

## Decision

`Manager.WithNativeRuntimeInventory` performs a fresh scan under the exclusive
native inventory barrier on every invocation, including after cached startup
recovery. It returns separate original provenance and measured machine/cgroup
data to a synchronous consumer. It keeps the root barrier, original VM journal
locks and pinned task handles until the consumer and final rechecks finish.
Consumers receive a deep copy. Errors, cancellation or a changing sample refuse
the operation. This API produces advisory observations, not a signed stop ACK,
admission receipt, absence proof or capacity-release instruction.

A distinct read-only machine session may observe recovered, revoked or
historical live owners without adding them to the current daemon's producer
map. It requires the active original inventory context, exact current journal
owner, retained daemon descriptor, live pidfd and original process incarnation.
It uses ADR-768's fresh credential-bound Unix connections for GET `/` and
`/machine-config` only. Other reads, writes and resume refuse; the regular
producer session also refuses an inventory context. Unknown historical guest
dimensions remain unknown despite observing a current shape.

The independent Linux scanner traverses current/legacy tenant cgroup roots,
plan scopes, the builder root and every descendant, including scopes absent
from the launch journal. It opens protected root-owned cgroup-v2 directories
without following symlinks and requires stable kernel device, inode and mount
identity. Controls must belong to the pinned cgroup mount. Nested mounts,
threaded domains, missing interfaces, malformed or oversized responses and
changing occupants/ceilings refuse observation. The scanner reads `memory.max`,
`cpu.max`, `cpu.max.burst`, `cgroup.procs` and recursive populated state. It
compares each scope before/after walking and reopens discovered paths. The
[kernel cgroup-v2 documentation](https://docs.kernel.org/admin-guide/cgroup-v2.html)
defines these hierarchical limits and notes that PID lists can change or contain
duplicates; such ambiguous lists are not an ownership receipt. Mount identity
uses fd-based [statx](https://man7.org/linux/man-pages/man2/statx.2.html) with a
checked `STATX_MNT_ID` result mask.

Independent procfs discovery scans every managed jail UID regardless of its
executable name. A stable UID and PID/start-time sample is required. An unknown
executable occupying a jail UID refuses startup recovery and fresh observation;
it cannot hide behind the existing Firecracker/jailer name filter. Known legacy
native tasks still retain their existing quarantine behavior. Fresh task sets,
machine dimensions/state, cgroups and original journal owners are rechecked
before and after the consumer. Natural exits and external mutation can still
occur after observation, so later authority cannot rely on a cached sample.

A matched regional observation requires the immutable original RAM/vCPU,
exact original task in its expected scope, a finite memory ceiling equal to
`api.BillableRAMMB(actual guest RAM)`, a bounded CPU ceiling no greater than the
original peak and zero burst. CPU comparison rounds upward and refuses overflow.
A lower observed CPU limit never replaces or reduces the retained original
peak. Unbounded limits, foreign/duplicate occupants, unknown populated scopes,
missing original provenance and incompatible machines remain unresolved and
charged/quarantined. Retired or unobservable journal owners require their
original retirement evidence; this observer invents no absence ACK. Empty
unpopulated descendants of a known original scope grant no new task authority.

This step does not establish complete historical/escaped root-helper inventory,
original first-fence reconciliation, current run/task authority, safe global row
binding/deduplication, or snapshot input compatibility before load. It does not
wire an authenticated inventory receiver or authorize capacity release. All
regional heads remain frozen. Native capture/pause/migration, shared global
directory and billing, independent regional provisioning/replication/ingress,
and serving from every selected healthy eligible region remain required for
end-to-end multi-region support.

## Validation

Portable tests model recovered/fenced/historical sessions, refusal of producer
effects, expired/foreign contexts, retained locks/handles, fresh scans after
cached recovery, copy isolation, task/machine/cgroup/journal changes and unknown
occupants. Procfs fixtures exercise name-independent jail-UID discovery and
incarnation/root changes. Linux checks use real read-only cgroup mount identity
and refuse regular files, symlinks and traversal without modifying host cgroups.
These tests do not establish native VM acceptance. Dedicated native Linux
x86_64 KVM test-metal, leakcheck and recovery acceptance remain mandatory.
