# Deployments shows degraded only while failures are recent

Date: 2026-11-27
Status: Accepted
Amends: ADR-20261025131406 (two failing accounts); ADR-130 (status page wiring); spec §12 (public SLOs)

## Context

The public status page computes deployment success over a rolling 24-hour
window against the spec §12 target of 99%. Since ADR-20261025131406, a value
under target marks the Deployments component degraded when the window's
platform-attributed failures come from at least two accounts.

The same 24-hour window decided both the published percentage and whether
Deployments is degraded now. On 2026-11-27 the rc.382 rollout produced three
platform-attributed failures between 08:54 and 09:15Z, from a customer and the
release-acceptance account, while the two compute nodes ran different
releases. Every deploy after 09:19Z went live (31 of 31 by 10:03Z), yet the
page showed Deployments degraded at 88.9% and would have kept doing so until
about 09:15Z the next day. A status page that reports a recovered problem as
current is wrong in the direction customers notice.

## Decision

Deployment success degrades the Deployments component only when the 24-hour
breach also has at least one platform-attributed failure in the last hour.

- The published percentage is unchanged: it still counts every
  platform-attributed failure in the 24-hour window, and the daily history
  still records the day's dip.
- The two-account rule of ADR-20261025131406 is unchanged and still applies to
  the 24-hour window.
- The last-hour count uses the same durable aggregate
  (`CountDeploymentOutcomesSince`) with the same attribution and account
  exclusions. It is skipped when the 24-hour window has no failures.
- If the last-hour count fails, the breach stands, as before this ADR. The
  Prometheus fallback, which cannot attribute failures, keeps the plain target
  comparison.
- The other indicators (API availability, wake p95) and alert-driven states
  are unchanged.

## Consequences

- Deployments returns to operational one hour after the last
  platform-attributed failure, while the percentage keeps showing the day's
  failures for 24 hours.
- A platform fault that keeps breaking deploys keeps the component degraded,
  since each new failure renews the hour. A fault during an hour in which
  nobody deploys is not shown by this indicator; alert rules with a
  `public_status` label remain the way to publish a known fault regardless of
  deploy traffic, and operators can publish an incident.
- The status cache runs one more aggregate query per refresh while the
  24-hour window holds a failure.
