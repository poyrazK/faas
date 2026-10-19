# ADR-20261019120035: Native promotion publication crash qualification

## Status

Implemented as the `promotion-publication` partition of the explicit environment
promotion acceptance lane. Native execution and final host leakcheck are pending.
No production setting, approval or signing key is provisioned.

## Context

Phase-level owner restarts do not establish what happens when APID dies inside
publication or loses the result of a committed transaction. Cutover commits the
active graph, paired configuration, workload activation, publication receipt and
health transition together. Restoration commits its graph/configuration/receipt
before the controller records rollback completion. Both boundaries need real
process-death qualification, including newer customer intent after restoration.

## Decision

Add barriers immediately before and after the actual PostgreSQL commit in
`PublishProjectEnvironmentPromotionReleaseSet` and
`RollbackProjectEnvironmentPromotionReleaseSet`. An ordinary production or
`metal` build compiles barrier activation to a no-op. Activation requires Linux
amd64 with both `metal,promotionacceptance`, the existing qualification-only
host/catalog guard and a private Unix socket explicitly supplied to APID. The
guard requires root, the designated root-owned marker, KVM and a local isolated
`faas_test_*` catalog; production names and templates are refused.

The socket protocol carries process, project, promotion, proposed release,
boundary and observation time. The private harness checks Linux peer credentials
against the PID of its owned APID child. Unselected boundaries are acknowledged
immediately. A selected boundary waits until the harness kills that child. All
waits honor the original 45-second health action deadline, reported by the daemon.
The harness must reap the killed owner before that deadline. Missing or failed
barriers fail the native action; they never fabricate successful results. The
protocol bounds events to 4 KiB and acknowledgements to 1 KiB. The
barrier writes no database state, runtime proof, job result or health evidence.
The harness preserves both build tags when compiling its daemons.

One native fixture reuses six real builds across two independent admissions:

1. Kill APID before cutover commit, after cutover commit, before restoration
   commit and after restoration commit. Uncommitted proposed graphs remain
   invisible; committed graphs and paired configuration/receipts remain visible.
   Phase, generation and deadline survive every process death. Replacement owners
   resume the existing operation using ordinary lease expiry and real probes.
   Verify candidate HTTP traffic, destination credentials, real candidate jobs,
   restored HTTP/job admission and exactly two persisted production publications.
2. Lose restoration acknowledgement again. While the committed owner is paused,
   publish a newer graph and configuration through the public API, then kill and
   restart APID. Recovery must preserve both new values, stop with
   `rollback_failed` and never publish over the operator. Exactly three graphs
   are added: cutover, restoration and the explicit operator publication.

Both cases retain candidate job pins/completed outcomes and unchanged artifacts,
reject duplicate candidate allocations or rebuilds, and restart the terminal
owner again without another publication. No real interval, lease, deadline or
retention timestamp is shortened or edited. Cleanup uses the existing APID/guest
retirement and guarded final leakcheck contracts.

Require this sixth independently bounded job in the CI matrix, test inventory,
runner selection, combined verdict and runtime approval inventory. It uses the
existing 210/215/240-minute test/systemd/job envelopes. The full report requires
eleven PASS records: ten runtime cases and fixture metadata. An older five-part
report cannot authorize a new rollout approval.

The evidence validator requires both independent promotions, the exact ordered
crash inventory, distinct owned process IDs, bounded kill/restart timestamps,
commit visibility, phase/generation/deadline relationships, graph counts and
terminal recovery. The existing `publication-crash-boundaries` supplemental gate
still needs a reviewed receipt. Operators can hash the retained publication
partition report as its evidence; the signer must review its provenance, scope
and any remaining operator-control variants. Automatic report generation neither
signs an approval nor enables admission.

## Validation and limits

Portable checks cover the barrier handshake/cancellation, production no-op,
native production-catalog rejection, daemon build tags, strict evidence parsing
and CI partition/aggregate rejection. Native-tagged compilation and fixture
metadata can run without KVM. They do not qualify the process-death scenarios.

Local verification passed: barrier/rollout race tests, production and ordinary
metal no-op checks, promotion PostgreSQL/memory regressions, acceptance-tagged
APID build and fixture metadata, focused production/native lint, environment
contract sync, fifteen evidence-parser tests and CI runner/partition/aggregate
contracts. Actual native execution and final leakcheck remain pending.

This scope covers paired HTTP/push-worker promotion and automatic restoration.
Multi-service binding graphs, migration dispatch/SQL recovery, concurrent
operator-control commands, low-traffic/baseline failures, retention exhaustion
and operator-control-specific publication interruptions retain their existing
qualification requirements. Production admission remains disabled pending actual
native evidence, remaining applicable gates and operator approval.
