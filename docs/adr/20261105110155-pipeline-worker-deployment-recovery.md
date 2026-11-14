# ADR-20261105110155: Pipeline worker deployment and recovery

- **Status:** implemented for local/staging qualification; production disabled
- **Date:** 2026-11-05
- **Amends:** ADR-1344, ADR-1591, and ADR-1650

## Context

The private replication and health workers share the `apid` binary but were
missing from its per-unit release plan. An active worker could therefore keep
running an old binary after deployment. Ansible unit and database-role changes
could likewise leave the active worker using old configuration. The workers
also exhausted systemd's restart limit during a transient catalog outage, and a
PostgreSQL service restart could stop them through a hard unit dependency.

## Decision

Register both workers as optional control-plane units and include them in the
`apid` release verification. Deployment converges workers already running, but
does not start workers an operator has not enabled. Ansible reloads the manager
and uses `try-restart` on the affected worker after its unit or database role
drop-in changes, respecting deferred service handlers.

The services remain `Type=simple` so a newly installed unit is compatible
with older binaries during deployment and rollback. The worker explicitly opts
into database boot waiting, including the direct pool when configured; systemd
may report it active before that wait finishes. PostgreSQL is wanted and ordered
before the workers, without a hard dependency that stops them on a database
restart. During operation, worker queues retry catalog unavailability with
bounded exponential backoff; other fatal errors still exit. The independent
health monitor continues its existing retry behavior.

Release verification includes activating and reloading workers and bounds the
restart command as well as the readiness wait. The health worker uses a bounded
pool of delivery loops so a slow receiver does not hold up all other pipelines;
the catalog claim and per-pipeline ordering remain authoritative.

## Validation and rollout

Release-planning, optional-worker convergence, Ansible handler, generated-unit,
and worker retry regressions cover the changes. No schema migration or new
privilege is required. Existing staging pilot and production admission gates
remain in force. A live systemd deployment remains an operational check.
