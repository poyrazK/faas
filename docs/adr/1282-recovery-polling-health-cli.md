# ADR-1282: Polling observations in recovery health CLI

Date: 2026-10-13

## Context

The app recovery health API exposes persistent automatic polling errors and the
last captured polling alert, but the CLI omits these observations.

## Decision

Display the current polling error, attempt count, failure time and next retry in
`gregale automations health --app`. Show the retained alert's capture state,
receiver count and inspection URL separately. A historical failed alert with no
current error means success has not been confirmed; clearing error metadata by
changing policy intent does not establish recovery. Current errors take
precedence over historical recovered alerts.

Keep the existing queue classifier, API and JSON representation unchanged.
Explicitly identify delivery as uninspected: queue admission is not successful
webhook delivery. Absence of an error is an observation, not a liveness guarantee.

## Consequences

Operators can discover polling backoff from the existing read-only command.
Manual-review aggregation and actual alert delivery inspection remain separate
follow-up work requiring bounded, account-scoped store queries.
