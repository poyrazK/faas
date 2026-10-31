# ADR 20261028095914 · Pinned public requests admit from the routing view

- **Status:** proposed
- **Date:** 2026-10-28
- **Amends:** ADR-20261027085556 (public routing admits from each gateway's
  refreshed view), decision 2, "Pinned requests keep the exact read"
- **Related:** ADR-570 (traffic enforcement guarantees),
  ADR-20261026160000 (revocation from a refreshed view),
  ADR-20261028083324 (budget admission outage grace), the owner rule of
  2026-10-27: no control-plane Postgres on the request hot path

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

ADR-20261027085556 moved ordinary public requests to a refreshed per-gateway
view, but kept ADR-570's per-request transaction for every pinned request.
Pinned requests are:

- **Named-environment and deployment-preview hostnames.** The host pins one
  deployment (`App.PinnedDeploymentID`).
- **`X-Gregale-Revision`.**
- **`X-Gregale-Release`**, including the release subprotocol and the release
  cookie of a project WebSocket handshake.
- **Hosting smokes.**

That kept the control-plane Postgres on the request path for every request to
a staging or preview hostname, and for every client that pins a revision or
release. A store outage refused those requests within one read, while
ordinary requests rode out the 5-minute grace. It was also the only
per-request control-plane read left on a warm request apart from budget
admission (ADR-20261028083324).

The decision rejected moving pins to the view because the view "would have to
hold every retained graph and pin". That is a bounded set per app:

- its live deployments (at most one or two per scope);
- the unexpired release graphs of its project's environments;
- the revision-pin expiry of each live deployment.

## Decision

1. **Pins admit from the view.** The view reads, in the same repeatable-read
   transaction as the roster:
   - every live deployment of the app in every scope, with its traffic and
     its revision-pin expiry;
   - whether the app allows revision pins (`revision_pin_ttl_seconds > 0`);
   - every release graph that is active or unexpired, with its expiry.

   The view pinner answers from those, as ADR-570's reads would against the
   same state:
   - a host pin with `PublicHostPinAllowed`;
   - a revision pin with `PublicRevisionAllowed`;
   - a release pin with `ResolvePublicProjectRelease`.

   Expiry, of a graph or a revision pin, is evaluated at request time against
   the gateway's clock. The rest follows the view's refresh, as for ordinary
   requests, and the snapshot fields are the exact read's.
2. **Bounded lists.** One app keeps at most
   `api.TrafficPublicRoutingMaxLiveDeployments` (128) live deployments and
   `api.TrafficPublicRoutingMaxReleaseGraphs` (64) graphs. Graphs are ordered
   active first, then a running canary's, then newest. A pin naming a
   deployment or graph that a cut list does not hold keeps the exact read. So
   does a revision pin on an app whose stored pin setting is not an integer,
   which the exact read refuses.
3. **Hosting smokes keep the exact read.** A smoke checks a candidate that is
   not live yet, is platform-internal, and is rare
   (`ReadPublicRoutingSmokeCandidate`). Route probes are unchanged: their
   deployment comes from an in-memory challenge.
4. **The same grace.** A pinned request admits from the view's last
   successful read within `api.TrafficPublicRoutingOutageGrace`, like an
   ordinary request. An owner refusal still drops the app's view at once.

## Consequences

- With ADR-20261028083324, a warm public request's routing and budget make
  no synchronous control-plane Postgres read, pinned or not. The exceptions
  are hosting smokes and pins outside the bounded lists. Revision- and
  release-pinned requests serve through a control-plane Postgres outage
  within the grace.
- Named-environment and deployment-preview hostnames still read one thing per
  request outside routing: their scoped declared-route policy.
  `declaredRoutesMatcher.ResolveScopedRoutePolicy` reads the environment
  workload spec or route policy whenever the host pins a deployment scope.
  Until that read moves to a refreshed cache (a separate change), those
  hostnames keep one per-request read and fail closed with the store.
- One refresh reads five queries for all tracked apps, up from four. The new
  query returns at most 129 rows per app.
- A new graph or deployment is seen by pinned requests within one refresh or
  on its notification, as for ordinary requests. A release pin to a graph
  published in the last second can see `gone` once and succeed on retry.
  Clients that pin release IDs they just received get them from the response
  of a request that already admitted from the refreshed graph.

## Rejected alternatives

- **Keep the exact read for pins.** Staging and preview hostnames would stay
  on the control-plane Postgres for every request and fail with it.
- **Unbounded lists.** One app with very many retained graphs would grow every
  refresh. The bound with an exact-read fallback keeps refreshes bounded
  without refusing any valid pin.
