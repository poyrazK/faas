# ADR 20261026160000 · Traffic revocation admits from each gateway's refreshed view

- **Status:** accepted (owner decision, 2026-10-26)
- **Date:** 2026-10-26
- **Amends:** ADR-570 (traffic enforcement guarantees), its emergency
  security revocation section
- **Related:** ADR-1343 (app edge on compute nodes), ADR-1344 (availability:
  static stability), ADR-1520 (rate-limit token leasing), the scaling
  roadmap rule "zero control-plane round trips per request"

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

ADR-570 made every participating HTTP admission verify its security scopes
in Postgres, and made the public hop verify the compute baseline against
Postgres again before it commits the response. A store outage refuses new
admissions and cancels active traffic, with no allow fallback.

On a warm app request that is about three sequential reads of the control
plane's Postgres: account and app before the wake, the selected deployment
before forwarding, and the public hop's check. Each has a 250 ms limit. Two
consequences:

- The control plane is back on every app request. That undoes the scaling
  roadmap's work: ADR-1520 removed the rate limiter's per-request writes,
  and ADR-1343 moves the app edge to compute nodes so that a request reaches
  no control-plane process.
- A slow or unreachable control-plane Postgres, including a restart or a
  failover, fails every app request with 503
  `traffic_revocation_unavailable`, on every node. ADR-1344 asks the
  opposite: running apps keep serving while the control plane is down.

ADR-570 also has each gateway re-read all active scopes at least once a
second, and earlier on a notification. Admitted traffic already relies on
that refresh to be cancelled when a generation changes.

## Decision

1. **Admit from the refreshed view.** A gateway admits a request from the
   generations its registry last verified when every scope of the request is
   tracked and was verified within `api.TrafficSecurityOutageGrace`. Only a
   scope it does not track, or one past that bound, is read before
   admission, as ADR-570 specified. A revoke then refuses new requests as
   soon as the refresher reads it: on its notification, or within
   `api.TrafficSecurityRefreshInterval` (1 s). That is the bound admitted
   traffic already has.
2. **Warm scopes.** A scope stays tracked after its last request ends, for
   `api.TrafficSecurityWarmScopeTTL` (10 minutes), and the refresher keeps
   it verified. Warm scopes share `api.TrafficSecurityMaxScopes` with active
   ones and are dropped least recently used first, so a refresh never reads
   more than that bound.
3. **Outage grace: 5 minutes.** When the store cannot be read, a gateway
   keeps admitting tracked scopes from their last verified generations, and
   keeps admitted traffic running, until its last successful refresh is
   older than `api.TrafficSecurityOutageGrace` (5 minutes). After that it
   refuses new admissions and cancels admitted traffic, as ADR-570 does at
   once. An untracked scope is refused during an outage, because it has
   nothing verified to admit from. A revocation written during the outage
   cannot land in an unreachable store anyway; the grace only decides how
   long traffic verified before the outage keeps flowing.
4. **The public hop.** Its check of the compute baseline uses its own
   refreshed view. A baseline newer than that view, which means compute
   read a generation the public hop has not seen yet, triggers a store
   read. All of ADR-570's comparison rules stay: a changed generation
   refuses, and a missed revoke and release pair cannot revive an old
   exchange.
5. **Inconsistent results stay fatal.** A store answer that cannot be a
   generation cancels admitted traffic at once, with no grace. A refresh
   that regresses a generation leaves that scope unverified, so its next
   admission reads the store and is refused, as before.

## Consequences

- A warm app request reads the control plane's Postgres zero times for
  revocation. Each gateway still runs one refresh query per second, covering
  its tracked scopes.
- Revocation still takes effect within about a second while the store is
  healthy, for new and admitted traffic alike. During a control-plane
  Postgres outage, apps that served traffic in the last ten minutes keep
  serving for up to five minutes. Apps that did not are refused.
- A gateway restart starts with an empty view: its first request per scope
  reads the store, as before.
- `pkg/trafficrevocation` gains `Registry.WithClock` for tests.

## Rejected alternatives

- **Keep the per-request read and add PgBouncer.** That shortens connection
  setup, but every request still waits on, and fails with, the control
  plane.
- **A 2-second grace.** Security would be essentially ADR-570's, but any
  control-plane Postgres restart or failover longer than two seconds would
  still fail every app request.
- **An unbounded allow fallback.** ADR-570 rejected it for good reason: a
  gateway cut off from the store indefinitely would never see a revoke.
