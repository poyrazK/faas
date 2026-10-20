# ADR-20261019120055: Activate project HTTP crons after workload readiness

Date: 2026-10-12
Status: Accepted

## Context

Project apply replaced HTTP cron rows during app reconciliation, before builds
were accepted or production workloads became ready. One failed workload could
therefore lose its active schedules even when another workload deployed safely.
Cron-only changes also need a deployment when source and app metadata match.

## Decision

Project apply passes no cron mutations into app reconciliation. Each selected
production workload receives an exact HTTP cron replacement intent, prepared
immediately before its deployment is enqueued. The submission receipt binds the
intent to that workload's accepted deployment. ADR-20261019120052 activation guards and
ADR-20261019120054 retained diagnostics apply unchanged: production readiness, full traffic,
completed rollout, newest candidate, operator revisions, identity and current
quotas. Existing schedule/path identities retain timezone and skip-if-running;
new schedules use UTC. Command crons remain outside HTTP replacement.

Cron changes add otherwise unchanged workloads to the enqueue set. An identical
pending intent with the same current revision and before set may reuse its
accepted deployment when app/source/secrets do not need a fresh deployment.
A declaration reverted to the active set still supersedes a different pending
intent. Failures stay per workload; definite enqueue rejection cancels the new
intent without changing active schedules. Excluded/unselected workloads and
`--no-triggers` receive no intent. Preview scopes never replace production crons.
The project `production` environment alias is enqueued in the canonical
`default` deployment scope, consistent with production selection and activation.

`AppliedBuild.configuration` exposes the per-deployment cron outcome. CLI wait
results retain the latest deployment configuration and text receipts display
pending, conflicted or discarded results independently of build status. A live
build receipt may still show pending activation until the server's next poll.

## Consequences

Healthy workloads can activate independently of failed workloads. App removal
continues to cascade its schedules during reconciliation. Configuration is not
a project-wide transaction. Each intent must fit current account cron quotas;
projects reallocating quota across workloads may need a retry after another
workload frees capacity. This uses ADR-20261019120052/998 persistence and requires no new
migration. Roll out their migrations, then updated APID, then clients.
