# ADR-20261019120005: Durable project verification and guarded rollback

Date: 2026-10-13
Status: Accepted

## Context

ADR-20261019120004 atomically activates a ready source graph but stops at cutover. An HTTP
release can pass readiness and then fail on a critical application route.
Existing ADR-847 gateway probes provide deployment-attributed responses without
bypassing customer authentication or recording synthetic requests as usage.

## Decision

`gregale deploy --project --atomic --verify` requests the `atomic-verify` mode.
Plain `--atomic` retains ADR-20261019120004 behavior. The source-only HTTP restrictions and
existing named production release graph remain prerequisites. Verification
requires a configured probe sender and at least fifteen minutes of baseline
retention. At most five configured GET/HEAD probe routes across the project are
snapshotted from route-health configuration. Other workloads still participate
in the atomic release and rollback; only explicitly configured routes are probed.

The scan token and submission fingerprint bind the concrete probe policy as well
as the previous graph ID. A policy change between scan and apply requires a new
scan. The CLI rejects a missing or changed verification policy in server replies.

Activation and a `verifying` checkpoint with a ten-minute deadline commit in the
same transaction. APID recovers verifying and rolling-back receipts after
restart. A round sends ten probes to each selected route on the candidate and
retained baseline, with bounded concurrency and normal customer auth gates.
Scoped private challenges select the named production deployments even when
cold. A critical probe requires an attributed 2xx response: redirects, other
4xx, 401/403, delivery failures and missing samples cannot establish health.

Two complete healthy candidate rounds at least one minute apart produce
`succeeded`, including when a candidate fixes baseline 5xx.
Two consecutive rounds containing candidate 5xx while every baseline route is
healthy produce `rolling_back`. Sparse or unavailable evidence resets consecutive
counts. If health cannot be confirmed before the deadline, the operation also
requests rollback, recording uncertainty rather than claiming a regression.
If the latest evidence contains baseline 5xx, the deadline produces
`rollback_failed` instead of restoring a baseline known to be failing.
Evidence, counters, deadline and the last round checkpoint are durable. Workers
compare the previous checkpoint so duplicate or concurrent rounds cannot count
twice. These are critical synthetic route checks, not organic error-rate or
latency enforcement.

Rollback rechecks the existing validator artifact preflight and publishes the entire previous
membership through the existing release-set primitive. Publication compares the
active candidate graph ID and commits the new restored graph ID with `rolled_back`
in one transaction. Lost replies cannot create duplicate restored releases. A
newer operator graph produces `conflicted` and is preserved. Expired or ineligible
baseline membership produces `rollback_failed`, requiring operator intervention.
Transient artifact failures keep the durable rollback request pending while the
baseline remains retained. The original candidate graph ID remains available for
inspection; `restored_release_id` identifies the newly active baseline graph.

The CLI waits for `succeeded`, and returns a failing exit status for rollback,
rollback failure or conflict. `--no-wait` and saved-session resume retain the same
server-owned operation; local cancellation cannot cancel verification or rollback.

## Consequences

Post-cutover regressions on opted-in critical endpoints can restore a complete
project graph. No VM lifecycle or scheduler state-machine behavior changes.
Deploy the recovery-index migration, scoped-challenge gateway support and APID
before enabling verified deployments. An old gateway may fail cold named-production probes; unavailable or
unattributed responses cannot establish health.

This first milestone does not verify arbitrary business assertions, database
migration reversibility, background workers, configuration changes or organic
traffic latency. Operators should select read-only public endpoints whose
responses reliably represent application health. Dedicated native host acceptance
is still needed before production rollout.
