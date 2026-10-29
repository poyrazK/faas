# ADR 20261028083324 · Budget admission survives a control-plane Postgres outage

- **Status:** proposed. The owner sets the grace length. 5 minutes is
  proposed to match the routing view and revocation graces.
- **Date:** 2026-10-28
- **Amends:** ADR-1750 (fund app compute before execution). It amends that
  ADR's gateway sentence "reusing each decision for at most one second
  (store errors are not reused)".
- **Related:** ADR-20261026160000 (revocation admits from a refreshed view),
  ADR-20261027085556 (public routing view), ADR-566 (budgets), ADR-1430
  (prices), ADR-1344 (static stability), the owner rule of 2026-10-27 that
  the control-plane Postgres stays off the request path

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

Every public request and managed service call passes the financial budget
admission in gatewayd-internal (`cmd/gatewayd-internal/financial_budget.go`).
That check reads the app and its holds and stops from the control-plane
Postgres. Each decision is cached for one second per app, scope and pinned
deployment, which causes two problems.

- **One blocking read per second per app.** When the cached decision expires,
  the next request runs the read on its own goroutine: an app row plus the
  compute-hold and stop queries, several statements. That is the last
  synchronous control-plane read left on a warm request on rc.299 (measured
  2026-10-28). Routing (ADR-20261027085556) and revocation
  (ADR-20261026160000) now come from refreshed views.
- **Fails closed within a second of an outage.** A store error is not cached.
  So within about a second of the control-plane Postgres becoming
  unreachable, every app answers 503 "Budget admission unavailable". The
  routing view and revocation registry, meanwhile, keep admitting for their
  5-minute grace. Budget admission alone turns a short control-plane outage
  into a fleet-wide outage.

The gateway check is not where strict spending is bounded. ADR-1750 funds
strict app compute in vmmd: at most two minutes of execution plus thirty
seconds of teardown, renewed every twenty seconds. A renewal that cannot reach
the store keeps the last deadline, and vmmd retires the VM at that deadline.
A CP outage therefore stops strict-budget VMs within about 2.5 minutes,
whatever the gateway answers.

What the gateway check enforces for warm traffic is the stop policies and the
account spend cap (ADR-566, #5524). Those decisions are written to Postgres by
the control plane. During an outage no new hold or stop can be written, so
the store holds no newer decision for the gateway to miss.

## Decision

1. **Serve warm decisions without waiting.** A decision younger than
   `api.FinancialBudgetAdmissionCacheTTL` (1 s) is served as before. An older
   one, still within the outage grace, is served while one background read
   per key refreshes it, bounded by `api.FinancialBudgetAdmissionRefreshTimeout`
   (2 s). A warm request never waits for Postgres. A hold or stop takes effect
   within about one TTL plus one read, as before.
2. **Outage grace.** Only a successful read replaces a decision. While the
   store cannot be read, an app's last decision keeps answering until
   `api.FinancialBudgetAdmissionOutageGrace` (proposed 5 minutes) after that
   read. Past it, the next request reads synchronously and an unavailable
   store fails closed (503), as today.
3. **Refusals never relax.** A held decision is only replaced by a read that
   admits. An outage keeps it held, and past the grace it still refuses (503
   instead of 402).
4. **No grace without a decision.** A key never read on this gateway reads on
   the request. During an outage it is refused, as today.
5. **Definitive answers act at once.** When the read says the admission
   cannot be evaluated (the app is gone, or the read is invalid or
   conflicting: `state.ErrNotFound`, `ErrInvalidArgument`, `ErrConflict`),
   the key's decision is dropped. The next request reads and is refused.

## Money exposure

The grace only matters when the control plane cannot record a stop. The
added exposure is compute that admitted apps accrue during the grace, beyond
a cap they cross in it. Compute bills at plan RAM + 8 MB per running second
at $0.01 per GB-hour, 1,000 millicents (ADR-1430, spec §4.7). Per running
instance that is (RAM + 8) / 1024 × 1000 / 60 millicents per minute.

| Plan | Millicents/min per instance | Per app at max concurrency | Per app over 5 min | Per account at max apps, over 5 min |
| --- | ---: | ---: | ---: | ---: |
| Free (256 MB, 1 × 1 app) | 4.3 | 4.3 | 21.5 | 21 |
| Hobby (512 MB, 2 × 5 apps) | 8.5 | 16.9 | 84.6 | 423 |
| Pro (1 GB, 5 × 25 apps) | 16.8 | 84.0 | 420 | 10,498 (10.5¢) |
| Scale (2 GB, 20 × 100 apps) | 33.5 | 669 | 3,346 (3.3¢) | 334,635 ($3.35) |

The per-account column assumes every app is at maximum concurrency, which no
node can hold. Measured admission (ADR-909) keeps billed RAM per node at or
below 2 × 47,600 MB. That caps one node's whole compute at about 1,550
millicents per minute, or **about 7.7¢ per node over a 5-minute grace**,
across all accounts together. Strict-budget compute adds nothing beyond its
funded window. Requests and egress are not billed (ADR-1430). Free plans have
no overage, so their exposure is platform cost, not customer billing.

## Consequences

- With the routing view and revocation registry, a warm request on a gateway
  makes no synchronous control-plane Postgres read. A control-plane Postgres
  outage shorter than the grace no longer refuses warm traffic.
- Each gateway re-reads only keys that receive traffic, one read in flight
  per key, at most once per TTL. The number of statements is unchanged; they
  move off the request goroutine.
- Central rate counters are the code default. With lease mode, the
  production manifest's setting, a lease is debited in the background and the
  request is admitted from the local allowance. They are the remaining
  control-plane write load per request; budget admission no longer adds a
  synchronous read.

## Rejected alternatives

- **Keep failing closed after one second.** One control-plane blip refuses
  every app, while every other request-path check already rides out 5
  minutes.
- **No grace, only the background refresh.** That removes the latency but
  leaves the outage behavior unchanged.
- **Unbounded grace.** A gateway cut off from the store would never see a
  stop or a cap again.
