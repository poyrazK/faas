# Snapshot publication and deployment activation diagnostics

Applies to FaasPostCaptureTelemetryUnavailable, FaasSnapshotPublicationPending,
FaasDeploymentActivationPending, FaasDeploymentActivationGateUnfinalized,
FaasPostCaptureEvidenceUnknown and FaasSnapshotPublicationErrors. All are internal
warnings. They neither change public status nor retry or bypass a deployment gate.

## Validate the observation

Require `up{job="imaged"} == 1` and `imaged_post_capture_observation_healthy == 1`
for the same job/instance. The independent observer reads at startup and every
30 seconds, with a three-second deadline and 120-second freshness ceiling from
read start. Scrapes never query PostgreSQL. Failed, stale, future or inconsistent
observations are unknown, not zero backlog. Check `imaged: post-capture observation
failed`, database access, clocks, binary and scrape configuration.

`imaged_post_capture_observation_enabled == 0` withdraws expectation on loop
cancellation/return; it does not mean work drained. Compare replicas separately:
this is a shared fleet observation, matching imaged's snapshot event consumer.
Never sum it, even when image-preparation recovery uses node ownership.

Each class exposes `imaged_post_capture_pending`, `_age_unknown` and
`_oldest_boundary_timestamp_seconds`. Unknown age is included in pending. Oldest
covers known boundaries only; zero means no known ages. The recording
`imaged_post_capture_oldest_known_boundary_age_seconds` is zero for proven empty,
absent for unknown-only classes or untrusted observations.

| Class | Evidence and boundary |
| --- | --- |
| publication_pending | Fresh post-stage parked capture; no current init publication. Age starts at parked time, if known. |
| activation_pending | Current non-stale init row, but deployment is not live and no stronger retained blocker applies. Age starts at publication. |
| dependency_held | Last durable dependency gate is waiting within its deadline; boundary is no earlier than publication and gate start. |
| compatibility_held | Current-attempt compatibility hold; boundary is no earlier than publication and first hold. |
| hosting_verifying | Current hosting attempt without a completed receipt or retry code. |
| hosting_retry | Retained recoverable verification code within its recovery deadline. |
| gate_overdue | A failed dependency gate or expired waiting/hosting-retry deadline still has a snapshotting candidate. |
| unknown | Qualifying stage, capture or receipt evidence cannot establish classification or time. |

These are retained observations, not a new eligibility verdict. Readiness-stage,
live/terminal, deleted-app, workload-held and scoped ephemeral-secret deployments
are excluded. Worker/job instance modes cannot prove parked snapshot capture.
The latest-64-instances-per-app lookback matches prime recovery; the deployment
population has no batch limit. Warm snapshots cannot prove init activation.

## Investigate the boundary

Publication/activation warnings require a known boundary older than five minutes,
held five minutes. Ages describe currently classified attempts, not continuous
idle time or residence in that class. A recent publication resets activation age
even if the earlier capture stage took a long time. Do not time activation using
the readiness stage: imaged advances it after promotion.

For publication_pending, inspect snapshot_written outbox delivery and imaged
publication logs, `imaged_snapshot_publication_total{tier,outcome}`, runtime/wake
receipts and storage/database errors. Parking or a successful capture call alone
does not establish artifact acceptance. Config or secret changes may invalidate
a parked capture. Preserve source tokens, snapshot keys and runtime fences.

For activation_pending/hosting_verifying/hosting_retry, inspect the retained
hosting verification's start, deadline, attempt, retry-not-before and closed error
code, plus supported deployment evidence. Verification completion still may
precede receipt persistence or promotion. A gateway/transport/unproven-response
retry does not establish an app failure or a platform-caused outage.

Dependency and compatibility holds do not trigger generic activation delay.
Inspect their durable pins and normal recovery state. Dependency status is the
last committed evaluation; this observer does not recheck dependencies. A failed
or expired gate remaining nonterminal warns after five minutes: investigate replay
and terminal-evidence writes. Do not manually move traffic, clear a hold, replay a
customer release command, or overwrite timestamps to silence a warning.

Old snapshot, hold and receipt evidence before rollback's current stage cannot
supply progress for that new attempt. A hold re-observed after rollback is still
a hold; if its first-held timestamp belongs to the old attempt, its new age
remains unknown rather than being reconstructed. Ephemeral-secret and worker/job deployments
use their supported no-snapshot activation paths; do not manufacture captures.

## Read attempts and errors correctly

`imaged_deployment_activation_attempts_total{input,phase,outcome}` counts actual
handler returns, including deferred, held and caller-canceled work. Phases are
evidence, dependency, verification, promotion and finalize. Success at evidence
can be warm publication or redelivery, not a newly live deployment. Success means
a nil handler return; it can also acknowledge a discarded capture or terminal
verdict. Errors can
include app/policy verdicts. Counts reset on process restart; they are not the
fleet backlog and must not substitute for durable observations.

FaasSnapshotPublicationErrors reuses write_error and policy_error outcomes, above
10% with at least three errors in each rolling five-minute window, held five
minutes. Its denominator includes published/duplicate/error decisions; stale,
RAM and ephemeral-policy rejections are excluded. Inspect publication writes and
policy reads separately from customer verification verdicts.

Unknown evidence held 15 minutes warns separately. Missing timestamps are not
reconstructed from deployment creation. Mixed cohorts retain their oldest known
boundary, which may not be oldest overall.

See [scheduler priming](FaasSnapshotPrime.md),
[init capture failures](FaasInitSnapshotCaptureFailures.md),
[image preparation](FaasImagePreparation.md) and stale-deployment diagnostics.
