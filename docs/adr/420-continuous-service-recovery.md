# ADR-420: Continuous service recovery

- **Status:** accepted
- **Date:** 2026-09-30
- **Milestone:** M9 managed service recovery
- **Related:** ADR-053, ADR-191, ADR-208, ADR-419

## Problem

The service reconciler already allocates replicas and preserves rollout
handoffs. Notifications invoke it immediately, but a failed replacement has
no durable retry schedule. The periodic rollout sweep covers in-flight
deployments rather than every active service. A dropped notification or a
scheduler restart can therefore leave a healthy live deployment below its
desired capacity until another event or customer request arrives.

## Decision

schedd discovers due active services on startup and every five seconds.
Discovery includes unassigned apps and apps owned by this scheduler; evicted,
deleted, request/job/worker-mode apps and inactive or abuse-held accounts are
excluded. Past-due accounts retain service during their existing billing grace.
Reconciliation rereads ownership and intent before admission and uses the
existing app-level allocation, capacity, readiness and handoff controllers.
Starting and draining rows continue to hold managed capacity; their presence
does not justify a duplicate replacement. Mirror generations keep their
separate lifecycle.

Desired replicas zero keeps the service stopped. Stopping an individual
replica while retaining a positive target permits the periodic controller
to replace it, just as an infrastructure failure does. Persist a target of
zero or change execution mode to stop continuous service admission.

Store retry state in `service_recovery`, one row per app, independent of the
customer manifest. A UUID token and a ten-minute lease fence each attempt.
Claims are atomic; completion must match the current token. A scheduler that
dies leaves a reclaimable lease. A resumed predecessor cannot overwrite the
successor's state. The same durable cooldown applies to periodic and event
reconciliation. Failure counters and deadlines survive scheduler/client
replacement.

Successful convergence resets failures and schedules a check in thirty
seconds. Progress (`starting`, `draining`, `rolling_out`) checks again in five
seconds. Capacity, startup and dependency failures persist bounded status
values and exponential retry delays of 5, 10, 20, 40, 80, 160, then 300 seconds.
The delay stays capped; failures do not exhaust service recovery. Failure
counts saturate at 32. New desired configuration or generation metadata can
reset cooldown when its notification arrives. Failed instance IDs are not
part of the revision, so a failed boot cannot reset its own cooldown. If a
configuration notification is lost, the next due sweep reads the new intent.

One discovery worker scans at most 32 candidates; eight app workers coalesce
by app. Saturated work is dropped before claiming and remains due for the
next sweep. Earliest deadlines sort first, so completed or leased candidates
leave the front of the queue. State reads/claims use a five-second context;
the entire app attempt has a ten-minute deadline. Completion gets a separate
five-second context so an expired attempt can still persist its retry. The
existing bounded notification and rollout handlers also use these claims.
Limits are centralized in `pkg/api/limits.go`.

## Qualification and operation

Shared store conformance tests verify exclusive claims, persistent cooldown,
revision resets, expired-lease takeover, stale completion fencing, ownership,
billing grace and inactive-app filtering in MemStore and Postgres. Portable
scheduler tests exercise the production dispatch path with a fake VMM: start two service
replicas, lose one, fail its replacement, replace both scheduler and store
client, restore capacity, and recover exactly two without requests. Tests also
cover increasing/capped backoff, changed intent, overlapping schedulers and
worker saturation.

Apply the additive migration before deploying the scheduler. Old schedulers
ignore the retry table; rollback preserves customer intent. The retry ledger,
replica gauges and existing lifecycle events expose current recovery progress;
see [the operations guide](../ops/continuous-service-recovery.md).

Native x86_64 Linux KVM lifecycle acceptance and leakcheck remain required
before fleet rollout. This controller fills deficits after existing failure
controllers retire an instance. It does not independently assert VM death,
move ownership after host loss, supply missing host capacity, or repair invalid
application code.
