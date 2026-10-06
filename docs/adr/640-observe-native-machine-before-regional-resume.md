# ADR-640: Observe the native machine before regional resume

Date: 2026-10-06
Status: Implementation in progress; independent inventory and native acceptance pending.

## Context

The regional launch journal retains the original requested guest RAM, vCPU, and
CPU ceiling. These are provenance, not observations of the machine loaded by
Firecracker. A snapshot can contain a different machine from the current request.
Loading it with `resume_vm=true` lets that guest execute before compatibility is
checked. A cached HTTP connection or socket path alone cannot establish which
kernel task supplied a machine configuration.

## Decision

Native restore loads the snapshot paused. While retaining the original
per-instance journal lock, Gregale pins the recorded task with a pidfd and checks
its liveness, kernel boot identity, PID/start time, managed instance, jail UID,
and cgroup ownership. Each fresh Unix API connection must have SO_PEERCRED PID,
UID and GID matching that exact task and physical lease. The original daemon
descriptor and locally registered generation must remain live throughout the
operation. A recovered record cannot grant local producer authority.

Bound GET `/` reads surround GET `/machine-config`. The VM must remain in the
expected loaded state; missing, duplicate, invalid, trailing, oversized or
unexpected HTTP responses refuse the operation. Actual RAM must match the
original lease. Regional RAM and vCPU must also match the immutable original
version 3 footprint. Version 2 regional records remain unknown and cannot resume
using today's plan as invented provenance. Nonregional version 1 owners retain
their existing lack of original vCPU provenance; their observed vCPU is not a
regional compatibility receipt. Cold boot additionally compares the observed
machine with its concrete boot configuration before returning success.

Only a matching paused machine can receive PATCH `/vm` to resume. A later warm
resume repeats observation in a fresh session, rather than consuming a cached
shape. An already running VM is an idempotent success only after observing the
same task and compatible shape. An arbitrary HTTP 409 is not native success.
Connections may wait for the original task to exec Firecracker and create its
socket; load/resume requests are never replayed after an ambiguous transport
failure. KeepPaused still skips resume and the guest hook. Serving restore keeps
the existing mandatory entropy/clock hook and readiness sequence. Failure uses
the existing teardown and cold-boot fallback paths.

Firecracker 1.7.0 exposes these reads in its [API schema](https://raw.githubusercontent.com/firecracker-microvm/firecracker/v1.7.0/src/firecracker/swagger/firecracker.yaml).
Its [restore implementation](https://raw.githubusercontent.com/firecracker-microvm/firecracker/v1.7.0/src/vmm/src/persist.rs)
updates machine configuration from the snapshot's vCPU states and guest memory
size before building the paused VM. Gregale checks this loaded configuration,
rather than the default pre-load configuration or requested machine JSON.

This guard does not prove snapshot source/output ownership, retained snapshot
metadata compatibility, cgroup CPU/memory limits, complete host inventory, or
capacity reconciliation. It does not prevent independent external API mutation
or natural exits after an observation. All regional heads remain frozen. Native
capture/pause/migration, first-fence reconciliation, global directory/billing,
independent regional provisioning and all selected healthy eligible region
routing remain required for end-to-end multi-region support.

## Validation

Portable fixtures exercise paused load ordering, mismatched loaded RAM/vCPU,
warm resume re-observation, changed/revoked ownership, dead/recycled task,
response validation, cancellation, lock contention and terminal transport
failure without effect replay. Linux tests check actual Unix peer credentials
and live/exited pidfd observations without KVM. These checks do not establish
native VM acceptance. Dedicated native Linux x86_64 KVM test-metal, leakcheck,
and recovery acceptance remain required.
