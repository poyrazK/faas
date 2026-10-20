# ADR-20261019120029: Fair, bounded promotion reconciliation

## Status

Implemented for environment promotion health and migration recovery. Existing
promotion admission flags remain default-off pending native qualification.

## Context

The PostgreSQL inventory sorted by promotion UUID and returned only 20 records.
Repeatedly visiting those records could starve later promotions until their health
deadlines expired. A sequential APID loop also let a slow probe delay unrelated
work. The Memory inventory used updated timestamps instead, hiding the PostgreSQL
ordering problem in portable tests. Migration recovery ran before the health lease.

## Decision

APID runs four reconciliation loops. Each loop claims one due job immediately
before executing it, without a local batch queue. Claims use the existing durable
health lease and support both running promotions and failed promotions with a
pending migration recovery. The normal minute-spaced evidence rounds, ten-minute
health/control windows and retained-artifact deadlines do not change.

`next_check_at` and `last_claimed_at` persist scheduling independently of customer
status timestamps. Ordinary slots select the oldest due work, then the oldest
claim and promotion ID. One slot prefers due rollback, restoration and migration
recovery work; it falls back to ordinary work when none is due. Other slots keep
using due-time order so recovery preference cannot indefinitely displace ordinary
work. Running actions are not preempted.

Short PostgreSQL claim transactions serialize slot accounting through a dedicated
transaction advisory lock. The selection statement takes a fresh READ COMMITTED
snapshot after that lock and uses `FOR UPDATE SKIP LOCKED` on the selected health
row. No locks are held during probes, preparation or publication. Updated APID
instances share a maximum of four live health leases and two per account. Older
direct reconciliation callers use the same accounting; old binaries retain their
previous concurrency behavior until replaced, and their live leases are counted
by updated dispatchers. Memory mirrors these limits under its existing mutex.

A successful claim parks its next due time at lease expiry. On completion, only
the matching, unexpired token can release and reschedule it. Fresh preflight,
verification and restoration samples wait for their next full minute; other work
and failures retry after the existing five-second poll interval. An earlier future
deadline wakes work sooner. An already expired deadline does not cause a busy loop.
A crash leaves a reclaimable job after the existing one-minute lease expires.
Health generations and migration recovery status changes wake sleeping work,
including decisions recorded by older writers through database triggers.

Scheduled reconciliation consumes its existing claim and checks ownership before
external work. Health observations include the lease token as well as generation,
so a retired owner cannot count a late sample. Existing transaction guards still
own cutover, rollback and migration recovery decisions. Context cancellation stops
new claims, cancels active calls, waits for the bounded worker set and attempts
lease cleanup with a short independent context. Release errors are reported;
lease expiry provides recovery if cleanup cannot reach PostgreSQL.

## Validation

Memory/PostgreSQL contracts keep more than three batches of promotions active and
require every job to receive a turn. They cover priority versus ordinary fairness,
concurrent fleet/account limits, locked-row skipping, restart and lease expiry,
stale observations/releases, durable sample spacing and recovery wakeups. Runner
tests keep one job blocked while later jobs run, isolate errors and verify shutdown.
Existing health, abort/recovery and migration suites provide compatibility checks.

Native acceptance must additionally observe concurrent projects/accounts with
real gateway probes and retained workers, including APID loss while leases are
held. Scheduling provides fair turns within bounded capacity; it does not promise
that arbitrarily overloaded admissions finish within unchanged health deadlines.
