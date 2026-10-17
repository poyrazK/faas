# ADR-1541: Continuous MCP readiness monitoring

Status: Accepted

Readiness admission (ADR-1540) records public protocol and expected-catalog checks before traffic increases. A serving deployment can subsequently regress, and an old successful observation must not imply continuous health.

Each readiness policy role may opt into monitoring with a 60–3600 second interval, a threshold of 1–10 consecutive failures, and an optional `secret:KEY` credential reference. If admission is enabled, the interval must be at most half the maximum evidence age. Monitoring can operate while admission is disabled. Existing policies remain unscheduled until explicitly configured.

The apid worker polls every 15 seconds and claims at most four checks per batch. PostgreSQL row leases fence competing workers and expire after 40 seconds. Probes receive a 25-second deadline; reporting has a separate bounded budget. Only live deployments receiving positive traffic are scheduled. Non-HTTP, deleted, inactive, and internal-only applications are excluded. Preview origins come from the platform's deployment URL builder, never from a customer URL or HTTP Host header. Redirects and private-address egress retain the existing guarded transport behavior.

The worker resolves the referenced secret in the deployment's environment scope on each attempt, rejects platform-managed credentials, and unseals it only after checking ownership and the current policy and runtime subject. This supports ordinary secret rotation without storing plaintext credentials in policies, observations, logs, or alert payloads. Missing or invalid credentials produce a fixed `credential_unavailable` result. An endpoint's authentication rejection is recorded as a protocol failure; the platform does not guess token expiry from arbitrary server error text.

Completion atomically records admission evidence and monitoring progress. A changed policy or deployment configuration, expired lease, retired deployment, or superseded observation rejects the result. Every failed check immediately invalidates readiness evidence for subsequent traffic increases; the configured threshold governs notifications only. Existing serving traffic remains unchanged.

Progress retains the last successful observation, next scheduled attempt, consecutive failures, and the last 20 observations for the current policy/runtime subject. A policy or runtime-subject change resets that status. Results are exposed in the deployment readiness response and MCP dashboard. History is retained with its deployment and removed by deployment deletion.

The `mcp_readiness_unhealthy` customer alert metric counts roles whose failure threshold is reached or whose next scheduled check is over 90 seconds overdue. This detects crashed or stalled workers as well as protocol failures and catalog drift. Pending observations cannot establish recovery; store failures skip evaluation. The existing webhook delivery, cooldown, and recovery state machinery is reused. Webhooks contain at most 50 status summaries without observation histories. Alert rules are notification-only and cannot initiate deployment actions.

Rollback refuses to discard retained monitoring history or active readiness alert rules. This increment does not add automatic rollback, provider OAuth renewal, or credential-expiry inference.
