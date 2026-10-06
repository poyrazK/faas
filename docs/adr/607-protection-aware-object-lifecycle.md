# ADR-607: Protection-aware object lifecycle deletion

Status: Accepted
Date: 2026-10-05

## Context

ADR-606 preserves Object Lock policy on new versions. Lifecycle expiration still
needs to defer protected data without blocking later eligible versions, and a
native deletion acknowledgment must not erase custody after an uncertain effect.

## Decision

Capture whether permanent lifecycle deletion requires protection checks under the
existing account-before-bucket admission locks. Permanent Object Lock requirement
and observed enablement are sticky. Freeze the exact native version, nanosecond
modification time and data/delete-marker classification in the lifecycle binding.
Current expiration remains an ordinary versioned DELETE that creates a marker
and preserves protected data. Markers have no per-version retention or legal hold.

Before permanently deleting data, freshly read native Object Lock configuration,
Enabled versioning, the exact version's legal hold and retention. Require a known
supported profile without MFA deletion. Defer active COMPLIANCE/GOVERNANCE dates,
legal holds and event holds. Unknown or malformed policy cannot authorize DELETE.
Never bypass governance, shorten retention or remove a hold. An event hold released
without a fixed retain-until date remains unknown. The backend capability contract
also requires protected writes and both exact and null-selector deletion primitives.

Persist initial policy verification with dispatch. After a successful DELETE,
require complete bounded exact-key history proving that the selected native
version is absent. Validate history identity, timestamps, latest-version shape
and pagination. Settle using the frozen marker classification, since repeated
native DELETE may omit that response header. An ACK with the target still present
or an incomplete/failed read leaves the dispatched fence and capacity intact.

Recovery probes exact history before repeating deletion. Proven absence settles
without another DELETE. A present target must match its captured identity and
pass fresh protection reads before retry. Existing `null` versions may follow
this path only with the captured permanent Object Lock requirement and fresh
native Enabled/Object Lock configuration; ordinary mutable deletion keeps its
previous conservative recovery rules. Accepted recovery ignores new enrollment
and ingress flags and does not require an active lifecycle scan or unchanged rule.

Prepared protected targets finish with stable `object_protected` status. The
scanner skips their other overlapping rules, continues to eligible targets and
revisits protection on a new hourly scan. Deferred checks count toward the existing
per-step action budget; durable receipts let a restarted step advance past earlier
deferrals. Unknown outcomes after dispatch remain owned by deletion recovery.

Memory and PostgreSQL reject unaware dispatch and completion without exact
absence proof. Separate SQL guards survive historical S3 migration replay. The
append-only migration preserves active legacy attempts, leases and dispatch
evidence; it never invents missing historical target classification or proofs.
Such unknown legacy attempts retain custody. Rollback refuses any new identities,
protection history or receipts. Clone schema classification treats the new fields
as operational state, preserving the existing source drain fence.

## Limits and consequences

Use existing deletion operation, lease, retry, response, history page/version,
worker action and scan budgets from `pkg/api/limits.go`. Meter every native read
and deletion. Completion does not refund inventory baseline; verified all-version
capacity reconciliation owns that accounting update.

Local implementation qualification covers customer policy, daemon dispatch,
reconstructed stores, disabled-ingress recovery and native HTTP responses. Object
Lock enrollment remains explicit per backend and no deployment setting is changed.
Real-provider/network qualification remains deployment work. Event-hold mutation,
governance bypass, replication, cross-placement transfer and SSE-C remain separate.

S3 semantics: [Managing Object Lock](https://docs.aws.amazon.com/AmazonS3/latest/userguide/object-lock-managing.html).

## Validation

Memory/PostgreSQL and native HTTP tests cover fixed retention, legal/event holds,
expired/released policy, unknown policy, configuration drift, markers, qualified
null versions, current expiration, bounded scan progress, lost acknowledgments,
ACK-without-absence, hold changes during recovery, immutable binding aliases,
legacy SQL writers, live migration replay and guarded rollback. The customer API
and reconstructed daemon test verifies hourly reconsideration, public receipt
ownership, disabled-ingress recovery, request metering and unchanged quota baseline.
