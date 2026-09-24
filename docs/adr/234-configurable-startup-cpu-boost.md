# ADR-234: Configurable startup CPU boost

Status: accepted

## Context

ADR-168 gives eligible application VMs a bounded CPU allowance while
cold-booting or restoring a snapshot. Some deployments prefer to keep their
configured CPU quota throughout startup and the post-readiness tail, for
predictable resource behavior.

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

This adds an opt-out without changing the default CPU behavior or readiness
ordering. The opt-out covers both the startup phase and the post-readiness
tail. Tail quota exposure is separately recorded from actual CPU consumption;
the scheduler reserves the temporary peak against node CPU capacity until the
persisted expiry, including across scheduler restarts. The feature does not
change CPU billing semantics.
