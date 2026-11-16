# ADR: Data API product workflows and shared request budgets

Status: Accepted

## Decision

Expose existing Data API diagnostics in the owner workspace, provide a local
browser launch flow and a coordinated-sync configuration generator, and publish
the Apache-2.0 data client through a versioned SDK release workflow. Publishing
requires the repository's npm credential; changing source does not publish it.

Live access settings are fingerprinted at the proxy. A changed or unavailable
route closes affected anonymous/server-key subscriptions before another result
is delivered. Successful access reports resynchronize endpoints. Short leases
bound stale access while an endpoint update is delayed. Client hooks accept an
explicit identity scope and discard rows when that scope changes.

An optional shared request budget uses a workload-authenticated control-plane
endpoint and PostgreSQL atomic fixed-window counters. The app is taken from the
workload assertion. The runtime derives the budget key from a verified subject
or an owner-selected JWT tenant claim, hashes it, and never sends JWTs or raw
subjects. The runtime supplies owner deployment policy, as with existing owner
caps; this is not a billing quota or protection against owner-modified code.
There is no automatic mutation replay. Shared admission fails closed before SQL
when its coordinator is unavailable, and local admission/rate limits remain.

SQL stays in sqlc queries and customer SQL stays in the app VM. Dashboard probes
use only the platform-derived public HTTPS hostname, carry no credentials, do
not follow redirects, and have bounded bodies and deadlines. Historical samples
and current health remain separate; missing observations are shown as unknown.

## Consequences

New control-plane schema and routes deploy before runtimes using shared limits.
The feature is opt-in so older control planes and runtimes continue to serve.
SDK publication and hosted acceptance require their configured external services.
