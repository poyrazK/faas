# ADR-215 · Durable async routes

- **Status:** accepted
- **Date:** 2026-09-22
- **Decision:** Add `kind=async` to edge rules. After the normal public
  authentication and rate-limit gates, a matching request is admitted and
  stored as an `async_invoke`; the gateway returns `202` with its invocation
  ID and `/v1/invocations/{id}` status URL without waking the app. Schedd's
  existing durable drain later wakes the app, delivers the original method,
  URL, JSON body, and safe headers, and stores the terminal result/error.
- **Why:** Gregale already has the durable invocation state machine, retry and
  retention policy, wake integration, and status/result API. Requiring an app
  to add a second queue just to move a slow HTTP handler off the request path
  duplicates that infrastructure and splits one execution model into two.
- **Consequences:** The edge-rule vocabulary grows from 17 to 18. Async routes
  are available wherever `AsyncInvokeAllowed` is true (Hobby+), use the plan's
  `MaxSourceBytesPerInvocation`, and accept JSON bodies because the existing
  invocation payload is JSONB. Public `Authorization`, `Cookie`, hop-by-hop,
  and `x-faas-*` headers are never persisted. `Idempotency-Key` deterministically
  derives the invocation UUID so a retried acceptance returns the same job.
  Synthetic delivery is excluded from matching to prevent recursion. Worker
  and job workloads are rejected because they have no request listener.
- **Rejected alternatives:** A new jobs table and worker pool would duplicate
  `invocations` and its schedd drain. Calling apid from the gateway would add a
  network dependency to acceptance and bypass the established Postgres plus
  scheduler-drain boundary. Making arbitrary byte bodies durable would require
  a blob contract distinct from the current JSON invocation API; that is a
  separate feature.

## Ordering and failure posture

`kind=async` is matched after app authentication. Cache lookup is skipped for
the matched route, then the existing account/app rate limits and bounded upload
admission run. Persistence happens before burst/wake admission. A successful
insert is therefore the durability boundary: only then is `202` written. Store
failure returns `503`; payload or JSON validation failure returns `413`/`400`.

The result is read through the existing authenticated
`GET /v1/invocations/{id}` contract. The request's `status_url` is deliberately
that control-plane path rather than an unauthenticated application-host URL.
