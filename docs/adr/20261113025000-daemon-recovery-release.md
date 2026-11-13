# Preserve client channels during a daemon recovery release

Status: Accepted

## Context

The sales release activated on the control plane, but the serving rc.339 VM
manager cannot remove an adopted jail whose creator used another mount namespace.
The compactor instance remains draining, so the normal compute rollout correctly
refuses to replace the host runtime. The adoption and evicted-bind fixes already
landed on main and need a maintenance build over the existing runtime.

A full repair release from that older source must not move public npm or curl
clients backwards from the newer sales release.

## Decision

On this maintenance branch, a committed daemon recovery profile pins rc.341 as
its base. A validator permits only the reviewed VM adoption implementation,
regressions, this record, and release-profile machinery to differ from that base.
Runtime image inputs, API packages, dependencies, guest code, and schema stay
identical. The nearest signed ancestor supplies the existing guest-init and
kernel through the normal reuse and signature checks.

The canonical release still builds every CLI target and the daemon bundle, signs
the bundle, and publishes its SBOM and verification assets. This explicit profile
preserves public client channels by skipping npm, data SDK and curl-channel
promotion. It does not change signature, source, node-adoption, drain, availability
or release-convergence gates.

Qualify the exact maintenance source with the normal light tier, the full native
fcvm package, namespace tests and leakcheck before tagging it. Compare the emitted
runtime bases with rc.341 before deployment. Use the normal compute repair
workflow to install it where required; keep the sales control plane on rc.342.
After adoption permits cleanup, resume the complete rc.342 fleet rollout.

## Consequences

Customers retain the newer public CLI and SDK channels while compute receives a
qualified adoption repair. The GitHub recovery release is for the maintenance
operation; it is not a general client upgrade. Ordinary releases without this
profile continue publishing their client channels.
