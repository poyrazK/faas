# ADR 20261021153953 · Push-bound workers may listen for their deliveries

- **Status:** accepted
- **Date:** 2026-10-21
- **Related:** ADR-137 (execution modes), ADR-051 (host-derived
  characterization), #2061 (declared execution mode enforced at boot),
  ADR-1646 (candidate binding qualification), ADR-1644 (managed worker queue
  canaries)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

ADR-137 defines a worker as a long-running daemon with no public port. Since
#2061, vmmd refuses a worker-mode app that binds any port at startup with
`execution_mode_conflict`.

Push queue bindings send each delivery as a private HTTP POST to the
consumer's `/_triggers/<kind>/<trigger-id>` route on its port. That applies to
ordinary trigger dispatch and to ADR-1646 qualification smoke, which accepts
push-queue smoke only for worker-mode workloads. A worker with a push binding
must therefore listen, and vmmd refused to boot it. On the native acceptance
host, `TestEnvironmentGitOpsNativeMetal` failed with "execution mode "worker"
conflicts with characterized class "http" (port=8080)" once the host
prerequisites from ADR-1749 were in place.

## Decision

- schedd sends a new `AppSpec.worker_push_delivery` flag on every wake, cold
  boot, migration and prime boot of a worker-mode app that has a non-retired
  push queue binding. A disabled binding still counts: pausing delivery does
  not change the app's code. Qualification boots set the flag from the frozen
  smoke policy (any push queue message), not from live bindings.
- With the flag set, vmmd accepts a worker whose characterization reports a
  bound HTTP server, and the boot is ready. A worker that binds nothing is
  still ready. A worker that exits, or serves gRPC or GraphQL, still conflicts.
  Other modes ignore the flag.
- ADR-051 normalization is unchanged: the report still says `http`. schedd
  does not persist an `http` observation for an explicit worker, so the app
  keeps its worker class and never gains public routing from its delivery
  listener.

## Consequences

- Push-bound workers boot in production and pass qualification; the native
  GitOps journey's push worker can qualify.
- Workers without push bindings keep the #2061 contract, and the
  `execution_mode_conflict` code and copy are unchanged for them.
- Release skew is safe in both directions. An older schedd omits the flag and
  an older vmmd ignores it, so either side keeps today's refusal until both
  units run this release.
- Each worker-mode wake reads the app's queue bindings once. Non-worker wakes
  skip the read.

## Rejected alternatives

- **Qualify push queues on request/service-mode workloads.** This would
  contradict ADR-1646 and ADR-1644, which define push consumers as worker
  mode, and leave ordinary push workers unbootable.
- **Let every worker bind its declared port.** This would drop the #2061
  check for the workers it was written for: daemons that should never serve.
- **Normalize the bound report to `worker` in vmmd.** This would break
  ADR-051's rule that normalization never turns a bound workload into a
  no-bind class, and would hide the observed listener from events.
