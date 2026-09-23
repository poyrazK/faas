# ADR-225: Configurable startup CPU boost

Status: accepted

## Context

ADR-168 gives application VMs a bounded CPU allowance while cold-booting or
restoring a snapshot, then restores the configured quota before the VM is
published as ready. Some deployments prefer to keep their configured CPU quota
through startup, for predictable resource behavior.

## Decision

Add the create-time `disable_startup_cpu_boost` deployment option and the
`gregale deploy --disable-startup-cpu-boost` flag. The stored deployment value
is immutable and is carried through retries, source deploys, snapshot restores,
and cold boots. Omitted or false preserves the existing boost, including for
rows created before this option existed. Builders and jobs keep their existing
CPU policies.

Disabling the option applies the configured CPU fence from the initial host
cgroup setup. Readiness and routing order do not change.

## Consequences

This adds an opt-out without changing the default CPU behavior or the
pre-routing quota restoration invariant. A post-readiness boost tail, such as
Cloud Run's, remains out of scope until Gregale defines its CPU accounting and
admission treatment; it must not be introduced as unaccounted headroom.
