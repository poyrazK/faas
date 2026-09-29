# ADR-376 · Platform tenant identity for durable HTTP work

- **Status:** accepted
- **Date:** 2026-09-29
- **Decision:** Async ingress stores its verified platform tenant in a nullable,
  immutable invocation column, separate from request payloads and headers.
  Retries retain it; owner and tenant replay copy it to the new invocation.
- **Why:** A customer platform needs the same customer boundary for deferred
  requests, worker database access, status and results as for synchronous ingress.
- **Consequences:** Idempotency keys are scoped to app and tenant. The database
  checks account ownership at enqueue and serializes claims with tenant
  suspension. Suspended work stays pending without attempts or async quota.
  Existing maximum-age deadlines still apply while work is held. Resume makes
  eligible work claimable on the next drain tick. A dispatch already admitted
  may finish; cancellation cannot reverse an executed side effect.

The synthetic transport carries tenant identity as a separate field. Gateway
delivery checks it against the dispatching ledger row, rechecks app ownership,
and uses the persisted request. Omitted or forged identity, cancelled work and
unclaimed work cannot reach the tenant worker. The guest identity renderer
replaces client-supplied platform headers with the admitted tenant.

Tenant-bound access tokens gain `platform_tenant:invocations:read` and
`platform_tenant:invocations:manage`. Tenant-self status, cancel and replay
resolve identity from the token and return the same 404 for foreign, unbound
and missing rows. Status omits the original payload, headers and owner metadata.
Replay idempotency checks ownership before returning a previous acceptance. Suspended tenants may inspect or cancel work; new enqueue and replay are refused.
Account API keys cannot mint these scopes or use tenant-self endpoints.

This decision covers async HTTP ingress and its HTTP replay. Named queue batch
delivery, OCI Jobs and AppTasks retain their existing contracts; tenant-bound
invocations with those sources are rejected until a separate delivery contract
defines tenant isolation across a batch or process. Legacy unbound invocations
and synthetic cron envelopes retain their behavior.

Acceptance uses real PostgreSQL and the runnable Node customer-platform
starter through a warm guest substitute. Native x86_64 KVM acceptance remains
required before claiming evidence for deployed VM delivery.
