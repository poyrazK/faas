# ADR-1349: Inspect qualification hosts and storage before creating node data

- Status: Accepted for development qualification; independent-host execution pending test hosts.
- Date: 2026-10-17
- Related: [ADR-1344](1344-owned-object-storage-ssh-qualification.md), [ADR-1346](1346-owned-object-storage-soak-qualification.md), [ADR-1348](1348-owned-object-storage-write-admission.md)

## Context

Local overload and recovery checks now preserve admitted receipts and throttle
new work within retention headroom. The next evidence needs primary and
follower data on separate hosts and known backing storage. The SSH runner already
rejects equal kernel boot identities by default, but a different kernel does not
identify its storage layout. Its reported filesystem type alone cannot exclude
root storage, a bind-mounted subtree or an unexpected filesystem after a mount
was omitted. Host selection should fail before a qualification creates node data.

The current managed development environment supplies one container filesystem
and a loopback SSH fixture. It has no independent test hosts, remote credentials,
VPN configuration or outbound TCP destinations. Transport validation here must
not be reported as independent-host qualification.

## Decision

### Inspect without initializing a node

Add `object-storage-qualify preflight` and a private `--inspect` helper. Run the
controller on the intended primary host and inspect its planned directory's
existing parent. Use ordinary noninteractive OpenSSH to execute the same
qualification binary on the intended follower. Send the requested role, planned
directory and daemon/control paths as one bounded JSON document on stdin.

Inspection reads kernel identity, binaries and filesystem metadata. It never
initializes the agent protocol, generates node credentials, creates fixture
directories, opens node data or starts node listeners/workers. The CLI writes
only the requested, exclusive 0600 evidence report. It does not provision hosts,
format/mount disks, install binaries or modify SSH configuration.

Keep strict host-key checking and batch authentication. Inspection disables
configured port forwards and local commands and does not reuse a control master.
Configured SSH routing remains operator-owned. Quote the helper executable as
one remote shell argument; put all inspection paths on stdin. Remove inherited
node credentials from the SSH process environment. Discard private SSH stderr;
report exit/context errors without copying arbitrary diagnostics into evidence.
Use the existing startup timeout and Linux parent-death/thread/reaping discipline.
Cancel a helper blocked on stdin when its context ends.

Reuse the existing 256 KiB agent message bound for inspection requests/responses,
including unknown-field and trailing-document refusal. Bound retained stdout
before parsing it, drain excess without allocating it, and reject overflow.
Bound kernel mount metadata by the existing 2 MiB qualification report budget
before allocation; individual lines retain the existing agent-line bound.

### Observe exact mounts and conservative readiness

Hash each kernel boot identity and all three executables: daemon, control CLI
and the running qualification helper. Require complete observations, equal
platforms/executable digests and distinct boot digests. The standalone preflight
does not permit `--allow-shared-host`. That flag remains available only to the
existing transport/development qualification profile.

The planned work paths must not exist. For a follower, check both mode suffixes
as well as the prefix. Parents must already exist and have no symlink components.
Inspect the parent's Linux `statx` mount ID and device, then select that exact
entry from `/proc/self/mountinfo`. Do not infer the active mount solely from a
path prefix, especially with stacked mounts. Require consistent device/path
observations, kernel mount-path decoding and a writable/traversable parent.
Missing mount identity or inconsistent metadata fails closed.

Readiness requires a local block-backed ext4 or XFS mount outside the namespace's
root filesystem. Cross-check the filesystem name against statfs magic, require
the whole filesystem root rather than a bound subtree, and reject a device equal
to the namespace root device, including a whole-root bind at another path.
Observe local block-device presence through sysfs. Require nonzero available
inodes and at least 3.5 GiB available bytes: the existing 2 GiB fixture budget,
1 GiB node floor and 512 MiB admission recovery margin. These are observations,
not block reservations; both modes, backup copies and admitted work can require
additional space.

Device major/minor numbers and mount IDs belong to one kernel namespace and
must not be compared as global disk identities. Separate kernels and apparently
local block devices do not prove separate physical drives, hypervisors, RAID
controllers or SAN failure domains. `physical_failure_domains_verified` remains
false. Operators must record the actual device/storage layout independently.
Read-only inspection also cannot certify write/fsync semantics or pin a mount
against later administrator changes.

### Guard actual runs with fresh observations

Add optional `--require-dedicated-storage` to the qualification runner. It
requires an SSH follower and rejects the shared-host override. Rerun preflight
inside the invocation before creating the controller work directory or generating
fixture state; embed its observations in the normal success/failure report.
There is no imported readiness token or cached-report bypass. The later node,
receipt, capacity, replication and authority checks remain unchanged.

Use an independent version-1 inspection/readiness document. Keep agent protocol
2, qualification report version 3 and storage format 4. The normal report gains
optional preflight evidence and a configuration boolean. Older helpers lacking
`--inspect` fail the optional guard instead of silently falling back. Existing
local and explicit shared-host profiles remain available with the guard disabled.

## Validation

Cover root/whole-root/subtree binds, overlay/network filesystems, mismatched
filesystem types, read-only/inaccessible parents, low capacity/inodes, symlinks,
existing follower mode paths and exact stacked-mount identity selection. Verify
inspection does not create data, rejects invalid message encodings/bounds,
does not open nonregular executables, and cancels blocked input.

Require tests for shared-kernel and binary/helper mismatch refusal, incomplete
identities, private stdin/credential filtering, safe SSH arguments, bounded output
through generic `io.Copy`, cancellation/reaping and immutable failure reports.
Exercise the built helper through actual loopback OpenSSH to verify negative
readiness and guarded-run refusal without node data. Run the existing SSH
qualification separately with the explicit shared-host override to preserve
transport/lifecycle compatibility. Neither run substitutes for the pending
independent-host execution on operator-provided dedicated test storage.

The 2026-10-17 development execution passed the negative readiness/guard checks
through actual loopback OpenSSH, including missing configuration, unsupported
helper and control-digest mismatch. Planned work paths remained absent. A separate
explicit shared-host compatibility run passed all 12 standard checks per
durability mode, with shared kernels recorded and physical domains unverified.
Independent-host execution remains pending; these results do not substitute for it.
