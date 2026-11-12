# ADR-20261104115303: Pipeline maintenance and pause observations

- **Status:** implemented for local/staging qualification; production disabled
- **Date:** 2026-11-04
- **Amends:** ADR-1591, ADR-20261103225803, ADR-20261104105500

## Context

PostgreSQL clears `atthasmissing` when VACUUM FULL or another heap rewrite
materializes fast-default values. Treating that physical metadata change as
logical schema drift gated reads and required a full resync, including during
an approved backfill. A retained Data API archive also conflicted with a new
worker's embedded template after an unaccepted enqueue failed. Retrying reused
the same conflicting build intent indefinitely. Finally, a pause request alone
reported successful suspension and suppressed monitoring even if the worker
could not reach the mirror to disable its subscription.

## Decision

Preserve a recorded missing-value bit when it clears on an otherwise identical
source column in the same relation. Apply that comparison to ordinary schema
reconciliation, table workflows, backfill and verification. Keep the existing
saved schema and approval identities. Changes to types, keys, defaults,
nullability, ordinals, identity or table ownership remain unsupported as before.
Physical heap fingerprints still restart verification after rewrites; retaining
the logical identity never reuses ranges proved against different storage.

Classify a conflicting source archive as retryable with a new build intent only
after confirming that no deployment was accepted. Persist `api_failed` with
`data_api_source_changed`; an explicit setup retry saves a new intent before
submitting the current template. Retain mirror, pipeline, app and binding IDs,
the preceding failed deployment lineage and the preceding archive. Recover
accepted deployment/build IDs before source inspection and retain the existing
source digest checks when recovering an incomplete accepted deployment.

Report pause completion only from a fresh observation of `paused` for the
current intent. Pending or stale acknowledgement remains unconfirmed; a fresh
failed observation reports a failed pause. Neither state suggests that native
replication has stopped. Until pause is acknowledged, the independent monitor
continues stale-observation detection and reports current blocked observations.
An acknowledged intentional pause retains its existing notification behavior:
WAL pressure can alert, and pause never emits recovery.

## Validation and rollout

PostgreSQL regressions cover physical materialization, stable approvals,
unmasked logical changes and alert incidents for failed suspension. Native TLS
replication tests cover manual verification and approved backfill across VACUUM
FULL, changed physical proof IDs with stable logical intent, retained native
identities and actual CDC continuing during a failed pause before successful
suspension. API tests cover archive conflicts before acceptance, explicit source
retry, persisted intent before submission, previous deployment lineage and
accepted-work recovery. Full pipeline acceptance, race detection, generated SQL,
affected-package lint and repository policy checks remain required.

This change needs no additional catalog migration. Apply the preceding ownership
migrations before deploying the combined control-plane changes. Production and
provider qualification gates are unchanged.
