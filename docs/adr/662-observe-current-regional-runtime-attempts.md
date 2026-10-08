# ADR-662: Observe current regional runtime attempts

Date: 2026-10-08
Status: Implemented locally; complete regional reconciliation and native acceptance pending.

## Context

A retained native runtime can match its original issued permit after its
instance row was replaced or collected, or its task claim was cancelled,
expired or completed. Physical ownership alone does not establish a current
scheduler attempt. Retirement must still retain the original physical hold.

## Decision

The paged regional inventory observer reports physical matches separately from
current attempts. After a fresh authenticated native match, read the app and
its original instance or task row. Require the original account/app/organization relationship.
For an instance, require the exact native lookup spelling and original
run/app/deployment/node, plus a resident lifecycle state. For an app task,
require its captured task/claim relationship, account/app/deployment, an active
restoring or running claim, a nonempty owner, an expiry strictly after the
observation time, and no cancellation request. Do not derive historical source
or resources from these current rows.

Missing rows and changed relationships produce current-attempt issues without
removing their original physical matches. Read failures also return an error
while preserving the page cursor, so an unavailable item cannot starve later
pages. Each page performs a fresh native observation. Recovery logs count
physical matches, current attempts and current-attempt issues separately.
Startup's retirement-only row binding retains its existing physical evidence
path; it does not depend on current task intent or this additional report.

These are advisory observations at a point in time. They grant no task lease,
VM command, serving or admission authority; all ordinary dispatch and intent
gates must still be checked atomically by their owner. Concurrent intent changes
can invalidate the report immediately. No accounting is merged or released,
no stale claim is adopted, and task completion cannot acknowledge physical
absence. No SQL, public protocol, lifecycle edge or plan limit changes.

## Consequences and validation

Portable contracts exercise replacement runs, exact lookup spelling, resident
states, missing rows, account mismatches, read failures, every inactive/expired/
cancelled task state, and the real durable task restore/run/cancel/complete
lifecycle. They verify retained original receipts and RAM/vCPU/CPU charges,
original physical matches, bounded cursor progress and frozen admission.

Complete physical inventory including historical escaped helpers, safe live
hold/row reconciliation, native first-fence recovery, snapshot capture/pause/
migration and latency qualification, shared global authorization/billing,
independent regional provisioning/stores/replication and cached ingress serving
every selected healthy eligible region remain in the complete multi-region
scope. Dedicated native Linux x86_64 KVM metal, leakcheck and fleet recovery
acceptance remains required; nested GCE diagnostics cannot satisfy it.
