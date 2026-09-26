# ADR-259: Continuous primary-app readiness

Status: accepted

## Context

Gregale's primary-app healthcheck gates startup, and its liveness probe can
restart a persistently unhealthy VM. Neither expresses the middle state: an
instance is still running but should temporarily stop receiving customer
traffic while a dependency or application subsystem recovers. Companion
readiness already has a reversible gateway traffic gate, but it is scoped to
the `primary_ingress` companion rather than the primary app.

## Decision

Add an optional immutable `overrides.readiness_probe` to deployments. It
selects exactly one HTTP path or standard gRPC health check and has bounded
period, timeout, and consecutive-failure settings. Omitted values resolve to a
5-second period, 2-second timeout, and 3 consecutive failures. A successful
probe restores traffic immediately (success threshold 1).

The probe starts only after startup readiness succeeds. Until the first
successful recurring check, the instance is not routable. After the configured
number of consecutive failures, Gregale reversibly withdraws the instance from
request routing. Recovery makes it routable again without destroying,
restarting, or parking the VM. Probe state is reported as durable readiness
transitions and hydrated into the gateway cache after restart.

The feature is independent of startup `healthcheck` and `liveness_probe`:
startup failure still rejects a boot, readiness failure only gates traffic,
and liveness failure remains responsible for restarting a wedged VM. When no
`readiness_probe` is configured, current routing behavior is unchanged.

## Consequences

Deployments gain an additive JSONB setting and API response field. The runtime
reuses the existing per-instance probe transport and gateway readiness gate,
while adding a distinct primary-app readiness event so companion lifecycle
events retain their meaning. This avoids conflating temporary app unavailability
with VM failure and preserves billing/runtime state during recovery.
