# ADR-20261104110000 · Retain rollback release and contract evidence

- **Status:** accepted
- **Date:** 2026-11-04
- **Amends:** [ADR-601](601-binding-checked-historical-rollback.md),
  [ADR-1448](1448-releases-that-change-the-database.md), and
  [ADR-838](838-approved-route-removal-contract-exceptions.md).
- **Decision:** Persist the exact release runs acknowledged by a checked
  rollback, validate fresh release history in its routing transaction, compare
  the rollback contract against serving traffic, and retain captured deployment
  contracts when historical artifacts are primed again.
- **Why:** Readiness can outlast the admission checks. A third deployment can
  run a migration while the expected current deployment keeps serving. A newer
  zero-traffic snapshot can also hide a contract break, and rebuilding the
  historical target's contract from the current app document destroys evidence.

## Release history

The checked rollback receipt stores private release-review metadata: deployment
IDs and the exact start times acknowledged at admission. Status updates preserve
it, and API responses continue to expose only the public operation. An older
receipt without this metadata acknowledges no release runs. A retry with a new
start time requires a new acknowledgement even when the deployment ID is equal.

MemStore validates current history under its mutation mutex. PostgreSQL validates
it under the same app lock held through traffic commit; starting a release task
acquires that lock too. Completed database switches remove the runs they undid,
but code whose release a database switch undid remains ineligible.

A new unacknowledged run fails the operation with `rollback_crosses_release`
while the current deployment keeps traffic. The caller can inspect release
history and submit another rollback with acknowledgement. Acknowledged release
IDs are included in the committed routing audit, including service handoffs.

## API contracts

The contract feature flag protects the default, `prod`, and `production` scopes;
non-production scopes remain advisory. Manual and alert rollbacks use captured
historical contracts and the selected serving deployment as their baseline, so a newer dark deployment cannot
replace the baseline. Checked rollback repeats this gate after readiness before
routing. Historical rollback retains strict contract checks without route-removal
exceptions.

Activation retains an existing deployment contract, including its hash and
capture timestamp, instead of projecting the app's current document over it.
Deployments without a captured contract retain the existing first-capture path.
No new public request fields or database columns are required.

## Validation

API regressions cover late migrations, acknowledged resubmission, production
scope aliases, staging behavior, dark baselines and delayed contract checks.
Memory/PostgreSQL regressions cover snapshot preservation, acknowledgement
persistence through status saves and restart, refusal of later release runs,
audited acknowledgements and serialization of release starts with cutover.
