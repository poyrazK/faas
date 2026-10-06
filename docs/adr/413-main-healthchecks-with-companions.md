# ADR-413 · Main-image healthchecks with companion workloads

- **Status:** accepted
- **Date:** 2026-09-30

## Context

The companion orchestration path returned before the legacy main-image OCI
HEALTHCHECK poller was started. Adding a companion could therefore remove
periodic main-image health reporting. The legacy poller also built its
process environment without deployment values or secrets, so image probes
could fail even while the app had the required configuration.

## Decision

Start one main-image healthcheck poller per app guest in both the single- and
multi-workload paths. Keep the existing vsock report protocol and host-side
health policy. A lifecycle channel delays polling until the main process has
started; dependency/init time is not charged against image start-period timing.
Guest shutdown cancels a poller waiting for the main process as well as active
probe execution.

Use an environment callback evaluated on every probe. The single-workload
callback takes the latest scoped secret snapshot, and both paths include the
main image and deployment environment. Companion orchestration adds its
reserved workload endpoint variables. Effective PORT wins over customer values.
Resolve a bare probe command using this environment's PATH and run it in the
main image's working directory with the shared OCI credential resolver.

When the main workload has an inner resource leaf, acquire its cgroup descriptor
only after the start signal and launch every probe atomically in that leaf.
A failed acquisition never retries the probe outside its resource scope. This
uses ADR-412's cgroup launch contract. Single workloads without an inner leaf
retain the existing host-enforced VM resource scope.

## Validation and recovery

Linux runtime tests exercise dependency/start gating, scoped deployment and
secret environment, effective port, image PATH and working directory, secret
refresh between probes, cancellation while waiting, and failure before probe
execution when cgroup acquisition fails. The native container lane derives
these tests from their source file and rejects skips or missing results.

Linux test execution and integrated native microVM health reporting with
companions remain required acceptance evidence. Compilation is insufficient.
Existing HTTP/TCP readiness, companion probes, ownership boundaries, scheduling,
and snapshot cold fallback remain unchanged. Rollback redeploys the previous
guest artifact through the established runtime upgrade procedure.
