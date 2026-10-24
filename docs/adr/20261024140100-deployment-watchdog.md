# ADR-20261024140100: Every deployment reaches a terminal state

Date: 2026-10-24
Status: Accepted
Related: [ADR-117](117-deploy-stage-progress.md) (deployment stages), builderd's
stuck-build reaper (issue #195 B1.4)

## Context

A deployment moves through `pending`, `building`, `imaging` and
`snapshotting` before it goes live or fails. Only one step had a bound:
builderd's reaper fails a build that has been `running` too long, and the
deployment with it. Nothing bounded the rest:

- a build waiting in the builder queue has no time-to-live, so a builder
  outage leaves deployments queued indefinitely;
- `imaging` and `snapshotting` rely on outbox redelivery between builderd,
  imaged and schedd, so a lost notification leaves the deployment in flight;
- a build that finished or failed without the deployment being updated
  leaves the deployment behind.

The CLI's `--timeout` is client-side: the deployment stays in flight on the
server, shown as in progress in the dashboard and holding its place for
later promotions.

## Decision

apid owns deployments (CLAUDE.md component ownership), so apid runs the
watchdog (`pkg/apid/deploywatchdog`). Every
`DeploymentWatchdogIntervalSeconds` (5 minutes) it fails in-flight
deployments that can no longer finish on their own:

- **Queued too long** (`deployment_queue_timeout`): a build queued for more
  than `DeploymentQueuedBuildTimeoutSeconds` (6 hours). Its queued builds
  are cancelled in the same transaction. The bound is generous on purpose:
  a builder pause or a single-slot backlog should delay work, not fail it.
- **Stalled** (`deployment_stalled`): no build is queued or running and the
  deployment has made no progress for `DeploymentStallTimeoutSeconds`
  (3 hours). Progress is the latest of its creation, its current stage's
  start (`stage_state.current_started_at`) and its last build's finish.

A running build stays with builderd's reaper. Deployments that are in
flight by design are never candidates: a prepared rollback target (its
receipt sweeper owns it), a deployment under an MCP task promotion hold, a
parked deployment, an environment workload runtime (owned by the promotion
workers), and a deployment waiting on a dependency, build-dependency or
hosting-verification gate.

Each failure locks the deployment row and re-evaluates the same predicate
before it writes, so a deployment that progressed after the listing is left
alone and several apid replicas can sweep concurrently. The failure goes
through the ordinary failure path (`setDeploymentFailedTx`), whose trigger
emits `deployment_changed`, so the CLI, dashboard and failure email see it
like any other failure. Bounds live in `pkg/api/limits.go` and are
documented in `docs/deploys.md`.

## Consequences

- Every deployment ends `live`, `failed`, `superseded` or `cancelled`
  within its bounds, with a stable code and a message that says what to do
  (deploy again).
- A deployment queued through a builder outage longer than six hours must be
  redeployed once builders return.
- The excluded states keep their own owners; a stall inside one of them is
  not caught here.
