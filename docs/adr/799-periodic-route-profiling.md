# ADR-799: Periodic route profiling for running deployments

Status: Accepted

## Context

Deployment and canary checks end after rollout. Long-running deployments can
regress later, and one noisy capture should not repeatedly open or recover an
advisory incident. Extend ADR-798 without changing rollout decisions.

## Decision

The automatic profiling policy optionally includes `periodic` with a whole-minute
interval of 60–86400 seconds and 1–5 confirmations. The interval must be at least
the capture window, so observations do not overlap. The dashboard proposes 900
seconds and two confirmations. Absence or null disables periodic monitoring.
Explicit advisory routes and enabled checks are required.

Each live, completed deployment and policy revision receives one durable monitor
per configured route. Parked applications remain eligible but sparse or absent
CPU/request evidence is inconclusive. Deleted, superseded, unfinished deployments,
ineligible accounts, and replaced or disabled policies cannot commit results.
Discovery is bounded and idempotent; claims use leases and SKIP LOCKED. Five
attempts retain the original windows. Repeated crashed leases eventually produce
an inconclusive receipt and move to a fresh window. Missed intervals are skipped
rather than replaying an unbounded backlog.

The first evidence-qualified window is pinned independently for each route by
comparing that window with itself. This establishes a reference with sufficient
traffic and capture quality; it does not prove that the initial code is fast.
Later comparisons keep that reference until its samples expire under the current
plan. Expiry resets calibration and creates a new baseline context; it does not
recover an incident whose evidence disappeared.

Periodic incidents use a separate namespace containing the baseline window,
deployment, policy revision, environment and route. Both opening and recovery
require consecutive supported results from distinct windows. Unknown evidence
resets the pending count and leaves an open incident unchanged. Deployment and
canary notifications retain their existing single-observation behavior.

Commit monitor history, confirmation state, optional saved investigation, and
recipient-snapshotted webhook events under the owned application lock and one
transaction. Keep confirmation and incident history even when webhook
notifications are disabled. Existing signed delivery remains at least once.
Changing policy invalidates outstanding work; already committed events remain.

Expose an app-read/MFA-protected history API and the same history in the Profiles
dashboard. Retain at most ten terminal observations per monitor for thirty days,
and return at most fifty monitors. Prune inactive old monitors in bounded batches.
No raw profiles are stored in the queue or event payloads. Save an investigation
on an incident transition when quota permits; otherwise keep pinned windows and
route evidence in monitor history.

## Consequences

Operators can catch and explain changes after deployment without automatic
rollback. Baselines and incidents do not silently migrate across revisions or
expired samples. Idle applications may never establish a baseline or recovery.
History is deliberately bounded; longer archives can use the existing webhook
integration. The migration must precede enabling the new policy option.
