# ADR-385 · OCI zero ownership and workload log descriptors

- **Status:** accepted
- **Date:** 2026-10-01
- **Issue:** #3946; release audit #3937
- **Amends:** ADR-136 and ADR-142 (full OCI rootfs ownership), ADR-051 (log wiring)
- **Decision:** preserve numeric zero ownership in full OCI rootfs artifacts,
  create conventional descriptor links after guest runtime mounts, and give
  each app/sidecar private output pipes owned by its effective credentials.
- **Why:** the pinned unprivileged nginx image cannot start on rc.220. Its
  manifest says USER 101, but valid 101:0 paths become image-builder-owned.
  The guest lacks /dev/stderr, and non-root processes cannot reopen inherited
  root console descriptors. TCP readiness correctly waits, then times out.

## Ownership boundary

Full OCI rootfs extraction treats numeric UID and GID zero as valid root
metadata. Existing image-passwd named-UID resolution and the 0..65534 range
checks remain in effect. Unknown names retain the numeric archive value;
no host passwd/group lookup occurs. All writes remain contained in staging,
symlinks use lchown, and capability failures stop publication. This does not
change the legacy daemon-owned zero-header behavior of source app layers or
shared runtime base extraction. imaged keeps its existing CAP_CHOWN,
CAP_DAC_OVERRIDE, and CAP_FOWNER boundary; no new capability is granted.

## Descriptor boundary

After pivot and /dev plus /proc mounts, guest-init creates /dev/fd, stdin,
stdout, and stderr links to the corresponding /proc/self/fd paths. An existing
correct link is accepted; an unexpected entry fails without following or
replacing it. Sidecars inherit the dev/proc views and resolve these links in
their own chroot.

App and sidecar stdout/stderr use fresh anonymous pipes owned by that
workload's effective UID/GID before exec. They retain private 0600 access.
The parent closes its write descriptors immediately after Start, drains output
into the existing console and bounded ring, and closes readers on cleanup.
A detached descendant cannot keep a log-copy goroutine alive indefinitely:
cleanup has a one-second drain budget. No console device is made writable
by all workloads, and neither UID nor public logging policy is changed.

## Validation and release

Parser regressions cover 101:0, 0:101, 0:0, named UIDs, and range rejection.
Privileged Linux CI builds a real ext4 and checks file, directory, and symlink
owners with debugfs. Credential-drop subprocess tests reopen both outputs,
retain stdout/stderr bytes, and cover failed startup and detached writers.
Descriptor-link tests cover idempotency and unexpected entries.

Guest runtime changes require native x86 KVM lifecycle tests and leak checks
before release qualification. Hosted Linux subprocess/metadata tests, macOS
unit tests, and production nested-GCP probes do not replace that gate. Live
acceptance must deploy a fresh pinned non-root HTTP image, verify request,
park/restore, cold boot, and cleanup while security gates stay enabled.

## Rejected alternatives

Running nginx as root or making /dev/console world-writable hides the runtime
contract failures. Changing source-layer ownership globally breaks its legacy
contract. Preserving permissions only in preflight does not prove ext4 metadata
or a credential-dropped process can use the produced image.
