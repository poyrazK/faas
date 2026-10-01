# ADR-390 · Raw ingress deployment traffic selection

- **Status:** proposed pending native qualification
- **Date:** 2026-10-01

## Decision

New customer TCP connections select from the application's
live deployments using persisted traffic percentages. Exclude zero-weight,
non-live and foreign-app rows. Reject malformed or incomplete serving weight
sets instead of routing around a deployment policy. Shares sum and percentage
bounds with the platform's 100-percent traffic contract.

Draw once per session using a uniform process-local random sample; preserve the
chosen deployment for that session. This differs from HTTP request-level stride
selection because a raw connection carries many application messages.
Weights apply to new sessions, not individual messages or byte counts. Existing
sessions stay on their instance until normal teardown, listener replacement,
idle expiry, instance loss or policy-driven cancellation.

Choose running non-mirror instances only within the selected deployment. If that
bucket is cold, pass its explicit deployment ID to schedd admission. Do not route
to a warmer zero-weight or superseded bucket. Fail if admission is unavailable,
at capacity, or returns another deployment. The scheduler remains the lifecycle
writer; public gateways read intent and serving policy.

## Acceptance

Portable contracts cover exact weighted bucket coverage, input-order stability,
zero/superseded/foreign exclusion, malformed weights, database read failure,
warm selection and pinned cold admission. Real socket and gRPC composition tests
must retain their listener replacement, disable and teardown coverage. Native
canary/rollback, cold-bucket wakes, node loss and leak qualification remain required.
