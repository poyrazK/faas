# ADR 20261022155812 · Pull-queue workers qualify by leasing their reviewed smoke

- **Status:** accepted
- **Date:** 2026-10-22
- **Related:** ADR-1140 (pull leases), ADR-1646 (candidate binding
  qualification), ADR-568 (environment GitOps; qualification guest egress),
  ADR-20261019200059 (HTTP service evidence), ADR 20261021153953 (push-bound
  worker listener)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

ADR-1646 keeps pull queues out of enforced candidate graphs. The existing
isolated pull smoke (ADR-568, 2026-10-09) POSTs the reviewed payload to `/` on
the candidate. A real pull worker (ADR-1140) never listens: it fetches a
`gregale:queues` workload token and leases from apid. In qualification:

- vmmd refuses candidates any workload identity
  (`qualification_identity_unavailable`), because candidates run unreviewed
  code.
- Candidate egress is drop-by-default. It admits the node-local service proxy
  on 10081, the opt-in private-CA proxy on 443 and pinned DNS, never the
  public API.
- The SDK consumers require an HTTPS API URL, and no deployment enables the
  private-CA proxy.

So a pull worker could not be qualified, and the `POST /` smoke only passed
for workers that also happen to serve HTTP.

## Decision

- **Arming.** For each enabled pull binding, schedd arms one row in
  `environment_qualification_pull_smokes` for the exact candidate execution:
  source before capture, restored target after. The row carries the
  attempt-derived message id and a deadline of
  `EnvironmentGitOpsQualificationPullSmokeWindow` (60 s), capped by the
  qualification lease. Queue name and payload come from the frozen review, not
  from the caller.
- **Lease route.** gatewayd-internal's qualification proxy serves
  `/v1/internal/environment-qualification-queues/v1/apps/{slug}/queues/{queue}/leases[:ack|:nack|:extend]`,
  the production lease shape under a private prefix. It identifies the caller
  only from its observed node address, as the service route does, and checks
  that the app and queue name the candidate's own enabled pull binding. It
  then hands out only that attempt's armed message. A lease long-polls like
  the production API, receipts are 256-bit tokens stored as SHA-256, and
  extend never passes the deadline. No credential is minted and no
  invocation, trigger record or customer queue row is created.
- **Lease URL.** schedd stamps the platform-owned
  `GREGALE_QUEUE_LEASE_URL=http://<bridge>:10081/v1/internal/environment-qualification-queues`
  into pull-worker candidates, next to the frozen service-binding URLs and
  inside the guest-config digest. A customer key with that name is a
  preparation conflict.
- **SDKs.** When `GREGALE_QUEUE_LEASE_URL` is set, the Node and Python
  consumers lease there and send no credential. It takes precedence over
  `apiURL`/`api_url` and API keys. Plain HTTP is accepted only for a private
  IPv4 host. Other pull clients must honor the same variable.
- **Outcome.** schedd waits for the row's outcome. An ack completes the smoke
  and becomes the queue's `handler_completion` evidence through the existing
  smoke receipt. A nack fails it, and so does the deadline. The VMM queue probe
  is now push-only.
- **Enforcement.** `candidateBindingPolicySupported` and
  `environment_binding_qualification_deadline` admit enabled pull bindings
  that require `handler_completion`.

## Consequences

- Enforced graphs can include pull workers. Graph serving already accepted
  them.
- Candidates still receive no identity. The only new reachable surface is a
  route that returns the candidate's own reviewed payload.
- **Release skew.**
  - An older gatewayd-internal forbids the prefix, so the smoke times out and
    qualification fails closed.
  - An older SDK ignores the variable and cannot reach the public API, so the
    smoke also times out.
  - Older schedd never arms a pull smoke.
  - Deploy gatewayd-internal before schedd uses this.
- Workers that consume pull queues through their own HTTP client must read
  `GREGALE_QUEUE_LEASE_URL` to qualify.
- Native capture remains closed by ADR-568, so native acceptance of this path
  waits for that gate. Portable memory, PostgreSQL, gateway, scheduler and SDK
  tests cover it.

## Rejected alternatives

- **Mint candidates a narrow `gregale:queues` token for apid.** Candidates
  cannot reach apid, and this would end the deliberate no-identity rule for
  unreviewed code.
- **Let pull workers listen and keep `POST /`.** This only qualifies workers
  that also run an HTTP server; ordinary pull consumers would still fail.
- **Admit pull bindings on `consumer_readiness` alone.** This would give no
  proof before activation that the handler works.
