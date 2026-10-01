# ADR-428 · Managed PostgreSQL Commit outbox

- **Status:** accepted; customer promotion remains gated
- **Date:** 2026-10-02
- **Decision:** Gregale Commit observes a customer-owned PostgreSQL outbox and
  starts one durable HTTP operation through the existing invocation ledger and
  request-app drain. The customer inserts the business write and event in their
  existing transaction. Gregale operates the polling relay and records the
  acceptance receipt and invocation atomically in its own database.
- **Why:** An application-defined webhook outbox does not establish atomicity
  with a customer database write. Observing the committed customer outbox removes
  the publish-after-commit gap and the customer's relay service.
- **Consequences:** Customer commit, Gregale acceptance, and operation completion
  are distinct facts. A stable source/event pair identifies retries, including
  lost acceptance responses and expired relay leases. Delivery is at least once.
  A consumer owns its business transaction and records that pair alongside its
  business effect before replying successfully. Gregale does not promise atomic
  execution of external side effects or exactly-once delivery.
- **Rejected alternatives:** Application-side publish after commit retains the
  crash gap. A customer-operated relay retains the recovery burden. Streaming
  capture is deferred until polling no longer meets the supported workload.

The first release uses one documented public outbox schema and one fixed source
binding per customer database. The owner installs and binds it; the relay can
read the binding but cannot rewrite its destination. Managed connections use
verified TLS, an operator-owned CA store, exact hostnames and approved address
prefixes on every connection. Source-bound age encryption protects credentials;
the public API and CLI never return them. Pause serializes with acceptance,
while existing receipts remain readable.

The relay uses bounded batches, short claims, expiring leases and token-fenced
checkpoints. Permanent errors become visible blocked events with bounded
observations and explicit replay requests. Cleanup deletes bounded batches of
accepted customer rows after retention; it preserves pending and blocked work.
Platform identity history survives invocation retention. Its current indefinite
retention and interaction with other invocation producers require the production
storage and quota audit documented in `docs/gregale-commit.md`.

Request-serving applications are supported. Worker/job and tenant-required
destinations are rejected; payload fields cannot confer tenant identity. A future
delivery contract must define those targets before enabling them.

Qualification requires real PostgreSQL transaction helpers, the actual scheduler
relay, producer termination after commit, rollback invisibility, stable receipts
after restart, and native x86_64 KVM snapshot/cold-boot completion. The HTTP
consumer gate additionally terminates a real consumer process before its business
commit and after commit before its response, then verifies recovery and concurrent
duplicate suppression in a separate business database. All required gates fail
when skipped. Production promotion additionally requires operator configuration,
operational alerts, outage recovery, and the documented quota/storage audit.
