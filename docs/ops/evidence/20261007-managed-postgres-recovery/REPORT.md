# Neon restore recovery acceptance — 2026-10-07

## Scope

Disposable Neon PostgreSQL 18 resources in Gregale's organization, driven from
the local adapter with staging configuration. Production provisioning stayed
disabled. No production rollout or billing-plan change was performed.

## Reproduction and fix

An actual restore requested `2026-10-07T15:43:08Z`. Once ready, its target
reported `parent_timestamp=2026-10-07T15:43:07Z` and `parent_lsn=0/19C7AE0`.
The previous equality check rejected normal creation, receipt recovery and
lost-response discovery. All failed attempts were compensated and deleted.

The fixed adapter independently routes a read-only historical connection to
the pinned source branch, requires recovery mode and a nonzero replay LSN,
compares that position with the target, then rechecks target metadata. The
passing run requested `2026-10-07T15:46:25Z`; target metadata reported the
earlier commit timestamp `15:46:24Z` and parent LSN `0/19C7C18`.

## Live results

All 43 checks passed in the final run, including:

| Scenario | Proof and readiness | Fresh-adapter replay | Physical targets created | Data and login isolation | Cleanup |
| --- | --- | --- | --- | --- | --- |
| Normal restore | Passed | Passed | 1 | Passed | Confirmed absent |
| Cancellation immediately after durable creation acknowledgement | Passed after rebuilding the provider | Passed | 1 | Passed | Confirmed absent |
| Accepted POST response deliberately discarded | Passed through discovery | Passed | 1 | Passed | Confirmed absent |

The data probe checks earlier committed contents, excludes later data, and
preserves its transaction boundary. Source runtime credentials cannot access
the restored target. Target credentials and probe fixtures were retired before
cleanup; each target DELETE was followed by an exact-ID 404. The disposable
source project was then deleted.

Final organization inventory contained only the original `Gregale` project,
`raspy-brook-65967063`. The temporary `gregale-recovery-proof-20261007` key was
revoked; a subsequent authenticated request returned 401. Its local credential
file was removed. Logs contain no API key, password or connection URI.

## Limits

This is focused recovery acceptance, not complete provider qualification.
Snapshot capture/retention, native snapshot copy, completed settled usage, and
the full version-7 qualification plus lifecycle smoke remain required before
enabling provisioning. The historical source must remain available while a
target is being provisioned. Health checks do not perform historical SQL reads.

Local regression tests cover route and identity drift, missing or invalid
mapping, changed target metadata, reconciliation gates, and rejection of
qualification with missing lineage despite a successful data probe. A real
PostgreSQL 16 primary was rejected as a historical source in both writable and
read-only sessions.
