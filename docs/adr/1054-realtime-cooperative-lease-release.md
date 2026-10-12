# ADR 1054: Cooperative consumer-group lease release

Status: accepted for preview implementation; migration/runtime validation pending.

## Decision

Add `release` to account and scoped worker single/batch settlement. Scoped workers
need an explicit release grant; existing credentials receive no additional rights.
Use the same fresh credential, claim-token and unexpired-lease checks as existing
settlement. Batch authorization checks every action before processing items, and
stale leases return the existing per-item lease_lost outcome.

A successful release returns ready with available_at, clears ownership and failure
code, and refunds the current claim attempt. It preserves deadline, entity ordering,
priority and fencing token. The next lease allocates a fresh fencing token and spends
normal claim-rate credit. Releases are deliberate handoffs, so repeated release can
postpone retry exhaustion; grant this permission only to cooperating workers.

Persist a monotonically increasing release_count on the snapshot to distinguish
release from retry independently of caller-controlled failure codes. Release neither
increments retry metrics nor counts as a circuit failure/success. Releasing the exact
half-open probe clears its slot and leaves the circuit half-open for another probe.
Record released in the bounded delivery timeline with the released attempt and fence.
Release does not bypass pause, claim-rate, deadline or entity-order admission gates.

## SDK shutdown

Node processing helpers expose releaseOnShutdown (default false). On caller signal
abort, stop renewal and await the handler, then attempt single release with a separate
five-second request budget. Automatic releases bypass the cancelled settlement queue.
Manual batch settlement also supports release. Known lease/deadline loss and internal
worker failure do not trigger cooperative release. Release errors reject shutdown;
uncertain or failed requests leave recovery to lease expiry and are not retried.

Handlers must await all their side effects and cooperate with their signal. Abort
alone cannot stop detached work or roll back writes. A handler that never finishes
cannot safely release its claim. External writes still require idempotency or atomic
fencing. Completed/in-flight terminal settlement is never followed by release.

## Rollout and verification

Apply migration 20261012102700522 after delivery-history migration and before the
new binary. Downgrade refuses retained released history or release credential grants/
last-operation records until those are removed or expired. Previous trigger definitions
are restored before removing release_count. Existing ready snapshots retain their
refunded attempt count on downgrade.

Build, SDK generation, SQLC/spec parity and static checks only. Runtime tests and
migration execution are deferred by request.
