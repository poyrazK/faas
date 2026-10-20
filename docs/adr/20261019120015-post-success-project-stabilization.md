# ADR-20261019120015: Bounded health monitoring after progressive deploy success

## Status

Implemented behind opt-in `--stabilize` on progressive project deploys. Native
qualification remains pending; production progressive admission stays disabled.

## Context

A rollout can pass its initial verification and regress under later real traffic.
Ending the controller at `succeeded` loses the opportunity to restore the retained
baseline automatically. Monitoring must remain bounded, preserve job pins and
cede ownership when production intent changes.

## Decision

`gregale deploy --project --atomic --verify --progressive --stabilize --config-file
release.json` requires a ten-minute stabilization window after full-traffic
verification succeeds. Workers additionally require `--workers --worker-health`.
Scan and apply bind `Project-Stabilization: required` and the immutable enabled
flag; stage/status/evidence do not change request identity. Missing server support
is rejected before apply. Existing retention must cover final verification plus
the monitor window and recovery margin: `ProjectReleaseStabilizationMinRetention`
is 21 minutes. All bounds live in `pkg/api/limits.go`; admission never extends TTL.

Rollout success still returns normally. The public receipt retains `succeeded`
and exposes waiting/monitoring/completed/rolled_back/needs_operator/conflicted
stabilization status, health, start and deadline. `gregale deploy status` reports
those fields. Unknown or insufficient evidence is never reported as healthy;
window completion is independent of the last health verdict.

Persist a bounded monitoring checkpoint as a complete full-traffic canary. It
continues to reconcile through the existing receipt/controller path. Fresh,
disjoint generation-fenced observations use the existing critical HTTP probes
and actual worker delivery outcomes, retries, duration, queue delay and backlog
policy. Two confirmed regressions with a qualified healthy baseline restore graph
and paired configuration through the existing guarded transaction. Pinned jobs
remain on their own retained graph; completed effects are not reversed.

The existing baseline worker reference window is taken before monitor start;
candidate windows remain disjoint. Baseline runtime configuration and HTTP probes
are checked again for every observation. Missing baseline job evidence blocks
restoration rather than manufacturing health jobs. Eligible baseline/candidate
worker pools remain warm during the bounded watch under existing app/account
capacity and the same rollout grant; no extra quota or direct VM operation exists.

Active graph, configuration, membership, artifacts and worker binding guards still
apply. A newer active graph/configuration or a newer running canary stops the old
monitor without restoration. Completed monitor records do not acquire the unique
running-canary slot or influence new cohort routing. Operators may abort an active
watch through ADR-20261019120014's durable control, with the same baseline health guards.
Monitoring stops at its deadline, including when evidence is unknown. Retained
resources then follow ordinary pin eligibility and scheduler retirement.

## Validation

Memory/PostgreSQL tests cover delayed regressions after success, stale rounds,
unknown baseline health, bounded expiry, operator abort and newer graph ownership.
The native progressive lane adds a required delayed-regression scenario that
waits for rollout success before failing the real candidate critical probe.
Compilation and local contracts do not qualify native VM behavior or cleanup.
