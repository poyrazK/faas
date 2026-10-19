# Operator running-build and VM cleanup recovery observation

Status: accepted

## Context

Queue observations stop when a build is claimed. A worker can disappear with a
row still running, or a terminal build can retain a durable VM teardown
obligation. The existing reaper recovers these states, but its sweep and cleanup
errors are primarily log evidence. Spec section 12 calls for operator recovery
visibility; section 17 and Appendix D do not require changes to build admission,
execution, ownership or rollout for this read-only addition.

## Decision

Add an optional narrow `BuildRecoveryObservationStore` implemented by PgStore
and MemStore. One SQLC statement aggregates running count/minimum `started_at`
and all outstanding `builder_vm_cleanup` rows, their minimum `created_at`, and
the count with a recorded failed attempt. One MemStore lock supplies the same
snapshot; its cleanup rows retain an observation-only creation timestamp.
Claiming, retrying and fenced late completions cannot reset that creation time.
The existing running-only index supports the build aggregate. No migration or
write behavior changes are needed.

Apid polls immediately and every 30 seconds with a three-second deadline and a
two-minute freshness bound. Scrapes read memory only. Validate nonnegative
counts, retry count within pending count, empty/zero timestamp pairs, and
positive nonfuture timestamps for nonempty sets. Running rows without a start
time fail the read. Late, failed, incomplete or expired observations omit all
five fleet gauges instead of reporting zero. Each apid independently observes
the whole fleet; never sum replicas or repair one instance with another's data.
Expose collection success and conservative read-start last-success separately.

Builderd exports effective normalized reaper threshold and interval, loop
initialization, last attempt, last completion, last whole-pass success and
bounded counters. A whole-pass success requires row sweep, cleanup claim, VM
cancel and durable result recording to succeed. Count recovered rows even when
cleanup later fails. VM cancel and record response counters use only fixed
operation/outcome labels, never build IDs or error strings. A successful record
response can be a claim-fenced no-op, not proof of a deleted obligation.

Keep existing recovery scheduling, batches, leases, context handling, return
values and retry behavior. In particular, observing cleanup errors must not
change the legacy sweep return contract or suppress subsequent cleanup work.
No systemd watchdog or automated remediation is added.

Internal warnings distinguish:

- Running age above the largest effective threshold plus two sweep intervals.
- Cleanup obligation age above the largest two sweep intervals.
- An unsuccessful latest completed whole pass.
- No successful whole pass within two intervals plus the existing maximum
  serial cleanup call budget: 64 rows times two 30-second calls (3840 seconds).
- Missing, inconsistent or stale apid evidence, or unusable builder telemetry.

All warnings hold for five minutes. The no-success warning uses loop startup
when there has never been a success, including on idle fleets. Age warnings
describe overdue customer work and can fire during a large, progressing batch;
they do not prove a VM remains alive or that the reaper is wedged. The separate
stalled warning allows the full serial call budget before calling progress
overdue. Database sweep/claim calls retain their existing contexts; slow calls
can exhaust this allowance and produce a warning without changing behavior.

Fleet age allowances require valid configuration from every discovered builder
target. A missing or failed target cannot silently reduce the maximum. This
assumes, like existing fleet queue observations, one deployment/database per
Prometheus rule scope. Discovery absence cannot prove a missing installation;
daemon/discovery health and explicit role expectations remain separate signals.

## Validation

Run shared PostgreSQL/MemStore contracts for claim/requeue, cancellation,
cleanup claim/retry, stale-token completion, successful cleanup and timeout
recovery. Race-test cached observations, cancellation, deadlines, invalid
snapshots, omitted unknown state, and whole-pass outcomes independently of
legacy return values. Exercise the actual production loop with a fake VM.
Promtool fixtures cover exact allowances/holds, idle startup, stopped loops,
failed passes, recovery, retries, drain, missing/future/stale evidence and
replica isolation. Regenerate both catalogs and retain the zero-exception metric
emission inventory. Review adversarially for false zeroes, cross-instance
evidence mixing, cleanup-success overclaims and premature stall warnings.
