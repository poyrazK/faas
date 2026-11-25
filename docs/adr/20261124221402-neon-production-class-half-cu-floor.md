# ADR-20261124221402: Neon production class starts at 0.5 CU

Status: Accepted

Amends ADR-155 (service class mapping), ADR-20261031165104 (overage
billing) and ADR-20261031091607 (GA, PG-02), which assume a 1 CU production
floor. Related: the customer suspend timeout (PR #7930) and the idle-tail
telemetry (PR #8039).

## Context

ADR-155 maps the service classes inside the Neon adapter to 0.25–1 CU
(`development`), 0.25–2 CU (`burstable`) and 1–4 CU (`production`). Always-on
compute is in no plan, so every production database is scale-to-zero, and
its floor is what each wake costs:

- Each wake bills at least the suspend timeout at the minimum CU. At the
  120 s default, 1 CU is 0.033 CU-hours a wake (about $0.0035 at Neon's
  $0.106 per CU-hour; $0.0078 at the customer's $0.2331 overage price).
- A production database busy all month uses at least 730 CU-hours, against
  200 included in Scale.

On 2026-11-24 prod had never had a production-class database (85
development and 13 burstable ever created), so no customer has relied on
the 1 CU floor. Customer docs do not state class CU ranges; only the operator
guide and ADR-155 do.

What the floor buys is warm-start headroom: Neon sizes RAM (4 GB per CU), the
local file cache and direct `max_connections` (RAM ÷ ~9.5 MB) from the
minimum, so a larger floor starts each wake with more cache and connections
before autoscaling grows it. Pooled connections (PgBouncer) are not limited by
it.

## Decision

The Neon `production` class is 0.5–4 CU. `development` (0.25–1 CU) and
`burstable` (0.25–2 CU) are unchanged.

- 0.5 CU halves the per-wake and idle-tail cost of a production database
  and its floor for a busy month (365 CU-hours).
- It keeps 2 GB of RAM at every wake (twice `burstable`'s floor), which
  matches the native Firecracker production class (2 GiB), and about 225
  direct connections, while autoscaling still reaches 4 CU under load.
- Production stays distinct from burstable by its higher floor and its
  4 CU ceiling.

### Rollout: two releases

The adapter reads a database's class from its endpoint's exact range, and
release N runs beside N-1, so a neond that does not know a range reports a
database at it as `spec_mismatch` and refuses its resizes and snapshot copies.
The change is therefore expand/contract:

1. **Accept** (this ADR's first change). Every neond reads both 1–4 and
   0.5–4 CU as `production` (`acceptedProfiles`); production is still
   provisioned at 1–4 CU and the agent budget still prices it at 1 CU.
2. **Provision**, in a later release, once every neond runs step 1:
   production is provisioned at 0.5–4 CU, 1–4 CU moves to the accepted
   ranges, and `api.ManagedPostgresMinimumQuarterComputeUnits["production"]`
   becomes 2.

Nothing converges endpoint sizes. An endpoint at 1–4 CU stays there, still
reading as `production`, until the database changes class.

## Consequences

- The operator guide's class table states the provisioned range and the
  accepted one.
- After step 2 an always-on production endpoint left at 1–4 CU would be
  priced by an agent's budget at 0.5 CU, half its cost. Always-on compute is
  in no plan, so no customer database can be one; this is accepted.
- The compute-cap qualification probe measures against the class profile;
  it runs on a database the same release just provisioned, so it is never
  at an accepted range.
- `faas_managed_postgres_idle_tail_share:ratio24h{class="production"}`
  (the idle-tail telemetry from PR #8039) is the check once production
  databases exist; if warm-up latency hurts them, the floor can go back to
  1 CU with another amendment, or become a per-database setting.
- The financial model's managed PostgreSQL line prices CU-hours, not floors;
  this lowers COGS per production wake and does not change any plan's
  included compute or price. ADR-20261031165104's month at a 1 CU floor (800
  CU-hours, $104 of overage) becomes 360 CU-hours and $32 at 0.5 CU.
