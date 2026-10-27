# ADR 20261027085556 · Public routing admits from each gateway's refreshed view

- **Status:** proposed
- **Date:** 2026-10-27
- **Amends:** ADR-570 (traffic enforcement guarantees), its effective request
  policy section: the public routing snapshot
- **Related:** ADR-20261026160000 (traffic revocation admits from a refreshed
  view, the model for this record), ADR-1343 (app edge on compute nodes),
  ADR-1344 (availability: static stability), the owner rule of 2026-10-27:
  no control-plane Postgres on the request hot path

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

ADR-570 pins public routing before the edge response cache and before
dispatch. gatewayd-internal reads release resolution, revision-pin
eligibility, host-pinned deployment availability and the positive-weight
roster in one bounded, read-only, repeatable-read transaction, and verifies
the owner in the same view. That is one transaction against the control
plane's Postgres for every public request, cache hits included. Its bound
is 250 ms and it fails closed with 503 `traffic_policy_unavailable`.

After ADR-20261026160000 removed the per-request revocation reads, this was
the remaining per-request control-plane read on every app request. It has
the consequences that record describes. The control plane is on every
request again, and a slow or restarting control-plane Postgres refuses
every app request on every node. On rc.296 one of its refusals could not
even be attributed (#5577).

Most requests carry no pin. Their routing depends only on the app's owner,
its live positive-weight roster and its project's release graph, which
change only on a deploy, a traffic change, a release activation or a canary
stage. Notifications already announce all of these except the canary stage.

## Decision

1. **Ordinary requests admit from the view.** A public request with no
   host pin, hosting smoke, `X-Gregale-Release` (or release subprotocol or
   cookie) or `X-Gregale-Revision` routes from its gateway's view of the
   app (`pkg/publicrouting`). The view holds what the exact read would
   return for such a request: the verified owner (app, account, project),
   the roster and the scope it came from (`RosterScope`, the
   production-lane rule of #5585), and the project's release graph. The
   graph is the scope's active graph and, in production, the running
   canary's policy with its baseline and candidate graphs. The canary
   splits each request with its own key and clock, as before. The view
   builds the same `PublicRoutingSnapshot`, so validation, selection, the
   frozen roster across cold wake and retry, the release `gone` and
   `conflict` verdicts, and the refusal stages and log of #5577 are
   unchanged.
2. **Pinned requests keep the exact read.** Host pins, hosting smokes and
   release or revision headers verify one deployment or graph the view does
   not hold, so they keep ADR-570's per-request transaction. So does every
   request under the opt-in authoritative host policy
   (`FAAS_TRAFFIC_AUTHORITATIVE_HOST_POLICY`), which re-verifies the host
   in the same transaction as the roster. A route probe's deployment comes
   from its challenge, which the gateway holds in memory, as before; its
   routing snapshot is the ordinary one.
3. **Refresh.** Each gateway re-reads every app it tracks every
   `api.TrafficPublicRoutingRefreshInterval` (1 s), in one read-only
   repeatable-read transaction of four queries whatever the number of apps
   (`PgStore.ReadPublicRoutingViews`). Each query returns, for many apps,
   the rows its per-request counterpart reads for one. A `deployment_changed`,
   `app_changed`, `app_delete` or `account_deleted` notification refreshes
   the named app at once. Release activation and deletion notify
   `app_changed` for every member app. A `deployment_route_changed`
   notification refreshes the app synchronously before the gateway
   acknowledges it, because schedd drains the predecessor once every
   gateway has acknowledged. A canary stage has no notification and takes
   effect within the refresh interval.
4. **Cold apps.** An app this gateway does not track, or one whose last
   read is older than the outage grace, is read before admission with the
   same batched read, bounded by `api.TrafficPublicRoutingReadTimeout`.
   Concurrent requests for one app share that read. An app stays tracked
   for `api.TrafficPublicRoutingWarmTTL` (10 minutes) after its last
   request. At most `api.TrafficPublicRoutingMaxViews` (4,096) are tracked,
   and the least recently used is dropped first.
5. **Outage grace: 5 minutes.** While the store cannot be read, an app's
   view keeps admitting until its last successful read is
   `api.TrafficPublicRoutingOutageGrace` old, the same bound as
   `api.TrafficSecurityOutageGrace`. After that the app's requests read the
   store and are refused as before, with stage `routing_read_error` or
   `routing_read_timeout`. A request for an untracked app is refused during
   an outage, since there is nothing verified to admit from.
6. **Refusals are never cached.** A store answer that refuses an app (owner
   not verified, a roster over `api.TrafficPolicyMaxDeployments`, a canary
   policy that does not decode) replaces the app's view at once, with no
   grace. Every later request for it reads the store again. An answer
   replaces only an answer from an older read, so a slow refresh cannot
   bring back a view that a newer read refused.

## Consequences

- An ordinary warm public request, edge cache hits included, reads the
  control plane's Postgres zero times for routing. Each gateway runs one
  four-query refresh per second that covers every app it tracks.
- A deploy, traffic change or release activation reaches new requests when
  its notification is handled, as the route and weight caches already
  require. A missed notification is repaired within one refresh interval.
  Under the exact read, a request saw the change at its own commit. A
  route change acknowledgement still waits for the view.
- The ADR-570 "one bounded repeatable-read view" property holds for each
  app's view: one refresh transaction reads it whole. Different apps may
  come from different refreshes.
- During a control-plane Postgres outage, apps that served in the last ten
  minutes keep routing for up to five minutes on the roster and graphs they
  had. A deploy or rollback written during the outage cannot land in an
  unreachable store anyway. Releases that expire or revision pins that
  lapse during the outage are not observed until the store returns.
- A gateway restart starts with an empty view: the first request per app
  reads the store, as before.
- Other per-request control-plane reads are outside this record. The
  central rate counters (ADR-570, ADR-1520) fail closed on an outage unless
  leasing absorbs it. The financial budget admission re-reads each app every
  `api.FinancialBudgetAdmissionCacheTTL` (1 s) and fails closed on a miss.
  Both remain separate decisions.

## Rejected alternatives

- **Cache the snapshot per request shape for a short TTL.** A TTL cache
  would serve one request's canary choice to the next, and a short TTL
  still puts a read on the request path every TTL.
- **Refresh each app with the per-request queries.** That is three to five
  queries per tracked app per second per gateway. The batched read is four
  queries in total.
- **Serve pinned requests from the view too.** A revision or release pin
  names a deployment or graph that the view would have to hold for every
  retained graph and pin. Pinned traffic is a small share, and its exact
  read keeps ADR-570's guarantees.
- **No outage grace.** Routing would remain a fleet-wide single point of
  failure on the control-plane Postgres, which this record removes.
