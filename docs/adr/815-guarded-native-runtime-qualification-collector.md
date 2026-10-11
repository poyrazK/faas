# ADR-815: Guarded native runtime qualification collector

Status: accepted · 2026-10-06

## Context

ADR-817 verifies signed exact-runtime native evidence but leaves production
collection, exclusive host ownership, source staging and signing to a trusted
operator. A hand-built report can lose command exits, use the wrong checkout
or sign before resources have retired. We need one private runner which creates
the evidence and invokes the existing importer only after successful cleanup.

## Decision

Add `cmd/runtime-qualification-collect` as dedicated native acceptance tooling,
not a production daemon or customer API. Its production wiring has no bypass
for hardware, source or cleanup checks. The portable orchestration interface
exists for synthetic unit tests; a fake owner is never native acceptance.
The existing metal test and static vmmd helpers own VM operations. Production
daemon ownership, two-drive rootfs, cold boot and snapshot rules are unchanged.

Require root on the designated Linux amd64 acceptance host with accessible
KVM, no virtualization, cgroups v2 with cpu/memory/pids, disabled unprivileged
user namespaces, the protected acceptance marker, tenant bridge and forwarding.
Measure host and kernel boot UUIDs, including again under the shared exclusive
`/var/lock/faas-builder-acceptance.lock`. This is the same lock as the builder,
metal and e2e acceptance gates. Hold it through staging, test, final leakcheck,
host/asset rechecks and restoration. Actual native acceptance remains required
before deployment; compilation on macOS cannot qualify a release.

Select release, host, source commit and fresh run UUID independently from the
fixture. Check the published managed function deployment and physical account
binding before taking native ownership. Use a root-owned protected Git clone
with its own `.git`, protected ancestors and no writable entries, links,
alternates or uncommitted archive attributes. Disable hooks, fsmonitor, global
attributes and replacement objects. Archive only the pinned committed Git
object into a fresh private run directory. Extract ordinary files/directories
only, reject unsafe paths, duplicates and special modes, and then write the
source marker. Dirty/untracked worktree contents never enter the build.

Stream published immutable base and layer into distinct private files and
verify their exact hashes. Copy and verify the protected pinned host kernel.
Pin the root-protected Go executable hash and require the source `go.mod`
version. Pin and recheck the installed Firecracker hash and version. Native tool
paths and their resolved ancestors must be root-owned and not writable by other users.
The Go distribution and host tools remain trusted operator installations; this
is not a hermetic supply-chain build. Build static vmmd and jail helpers, then
a native race-enabled metal test binary, from the selected source.

Child commands receive an explicit environment with a fixed system PATH,
private caches/home/tmp, local Go toolchain, disabled Go workspace/config and
empty GOFLAGS. They do not inherit operator database/artifact credentials,
signing paths, proxy hooks or arbitrary build flags. On cancellation, kill the
owned process group and join command output before final leakcheck. Firecracker
can own another session, so process-group termination alone is insufficient;
the final native resource check is authoritative.

The caller must supply a root-owned 0600 regular file with one link containing
exactly one binary 32-byte Ed25519 seed, outside resolved source/output paths.
Never follow its final symlink. Match the derived public key to the independent
public trust pin, keep the key in parent memory and clear it when the command
returns. Key enrollment/discovery is outside this workflow. The private source
commit, host tools and acceptance-owner binary are trusted privileged code;
this runner is not a sandbox for arbitrary customer source.

Before stopping services, run the pinned source's leakcheck and require a
drained host. Record originally active units durably before each stop, stop
the scheduler first and vmmd last, then repeat leakcheck to catch a drain race.
Inactive services stay inactive. Run only
`TestMetalRuntimeReleaseColdBootReady` through real `go tool test2json`, capturing
stdout, stderr and actual process exit. Always run final leakcheck after the
test has stopped, including failed/canceled attempts. Validate the ADR-817
exact observation, log ordering and coverage. Recheck host/boot identity and
pinned host assets, restore recorded services in reverse order, and remove
owned staging before signing. Failed test, skip, cleanup, restore or recheck
cannot produce signed success.

Leakcheck failure leaves services stopped and staging retained, with no
automatic reaping. Restoration failure also retains staging and the original
active-service list. Any retained qualification staging blocks another
collector attempt until explicit operator recovery. SIGKILL, host reboot and
power loss cannot execute cleanup; the same retained run provides the recovery
record. Other acceptance gates share the lock but do not consume this recovery
record, so the operator must keep them stopped until recovery is complete.

Evidence output must be a new private directory under a protected parent,
outside source. Use exclusive files, retain raw logs/exits/build diagnostics,
sync the signed report and directory, and only then call the ADR-817 importer.
Import independently verifies published bytes/binding and retained readback
before the ledger write. A failed import leaves the valid bundle available for
retry with the private importer. Receipts remain immutable and revocable under
ADR-739. No migrations, customer state or traffic writes are added.

Central policy in `pkg/api/limits.go`: 2 GiB per staged native asset, 512 MiB per
source archive including headers/padding, 15-minute lock wait with 100 ms polling,
10-minute source/tool/drain and build preparation budgets, 3-minute test budget,
2-minute host-probe/cleanup budgets and 5-second command pipe wait delay. ADR-817 evidence bounds
remain 64 KiB per JSON report/envelope/fixture, 64 MiB per captured stream,
256 KiB per event/line and 16 JSON nesting levels.

## Consequences

This extends ADR-817's collection boundary while reusing its trusted import
contract. It provides no customer apply or maintenance scheduler. Public
previews keep `execution_available=false`; a qualification receipt gates
preparation only. Fresh candidate acceptance, baseline checks and authoritative
cutover serialized against revocation remain separate work.

The dedicated host must be drained and operator provisioned. No production
host, local macOS environment, nested KVM or synthetic image is a substitute.
The runbook describes quarantine and recovery without pretending that failed
native cleanup can be repaired by metadata or by rerunning the collector.

## Validation

Synthetic owner tests cover successful collection/import ordering, failures in
preparation/test/leakcheck/recheck/restoration, skips, cancellation, replay and
pin/binding mismatch. File tests reject altered/truncated/oversized assets and
unsafe archives. Real child-process tests capture exit status, isolate ambient
environment and cancel stalled commands. CLI tests guard native execution
before opening key/infrastructure and reject aliased secret placement. The
existing PostgreSQL importer test exercises immutable receipts and revocation
through the real store. Linux compilation and lint cover native-only code;
only designated-host execution plus final leakcheck prove native acceptance.
