# One tenant's deployment failures do not degrade public status

Date: 2026-10-25
Status: Proposed
Amends: ADR-130 (status page wiring); spec §12 (public SLOs)

## Context

The public status page publishes deployment success over a rolling 24-hour
window against the spec §12 target of 99%. Customer-caused failure codes
(`api.CustomerCausedDeploymentFailureCodes`) and user-code build failures are
excluded, and a value under the target marks the Deployments component
degraded.

Production deploys about 16 times a day, so a single platform-attributed
failure (93.75%) is already a breach. In Hunt 14 (H14-22), one tenant's apps
that never became ready (`app_startup_timeout`) kept the page degraded. On
2026-10-25 the window held 24 successes and 3 such failures: h14-slow and
h14-grpc (`app_startup_timeout`) and h14-py (`application_standards_pending`),
all in the hunt's test account. `app_startup_timeout` is ambiguous: it can be a
slow or broken app or the platform, and the timeout alone does not say which.
A platform problem that breaks deploys breaks them for more than one tenant.

## Decision

Deployment success degrades public status only when the window's
platform-attributed failures come from at least two distinct accounts.

- The durable aggregate (`CountDeploymentOutcomesSince`) also returns
  `FailedAccounts`, the distinct accounts behind its failures. A failed
  deployment whose app row no longer exists counts as its own tenant.
- The published percentage is unchanged and still counts every
  platform-attributed failure. Only the degraded flag and the component state
  wait for a second failing account.
- The Prometheus fallback, used when the database window is idle, cannot tell
  accounts apart and keeps the plain target comparison.
- The other indicators (API availability, wake p95) and alert-driven states
  are unchanged.

## Consequences

- A tenant whose apps keep failing no longer shows the platform as degraded.
  Operators still see the failures in the percentage and through the runbook
  query (`docs/runbooks/StatusPageDegraded.md`).
- A platform fault that, in a quiet day, reaches only one account's deploys
  (for example only the platform's own acceptance account deploys) is not
  shown as degraded by this indicator. The alert path (`public_status`
  labels) remains for failures that need a public state regardless of spread.
- Splitting `app_startup_timeout` by evidence (the guest answered HTTP but the
  app never became ready, versus the guest never answered) would let the
  aggregate exclude the customer-caused half. That is left for a follow-up,
  because it needs the readiness probe to record which case it saw.
