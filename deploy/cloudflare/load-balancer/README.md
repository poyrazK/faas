# Cell edge load balancer (ADR-1237 §2)

A cell runs one edge per control node: Caddy terminating Cloudflare Origin CA
TLS in front of gatewayd-public, one per zone. This directory holds the
Cloudflare Load Balancing objects that choose between them. Nothing here is
applied automatically; create the objects once per cell with the API calls
below, after the control nodes exist.

## How traffic reaches an edge

The `gregale-public-timeout` Worker (`../public-timeout-worker`) sends every
customer request to `ORIGIN_HOSTNAME` (`origin.gregale.dev`) through
`cf.resolveOverride`, keeping the customer Host header. Today that name is a
DNS-only A record for the single control node.

ADR-1237 makes `origin.gregale.dev` a **DNS-only** Cloudflare Load Balancer
(`"proxied": false`, 30 s TTL). The monitor below checks every edge every
15 s; an edge that fails twice drops out of the DNS answers, so the Worker
stops resolving to it within about a minute (monitor detection plus TTL),
inside the 60 s node-loss RTO. DNS-only keeps the Worker unchanged and avoids
a loop through the Worker's `*.gregale.dev/*` route.

**Verify first, in staging:** that `cf.resolveOverride` follows a DNS-only
load-balanced hostname (it resolves through Cloudflare's own DNS, so it
should). If it does not, keep `origin.gregale.dev` as plain A records and
add per-origin failover in the Worker instead (try the next origin on a
connection error); that was the alternative recorded in ADR-1237.

## The health signal

The monitor asks `GET https://<edge>/__gregale/edge-health` with
`Host: origin.gregale.dev`. Caddy (role `public_edge`) answers that one path
on that one host from gatewayd-public's loopback `/edgez`, which returns 200
`ready` only while the edge is not draining and can reach a compute gateway.
It deliberately ignores Postgres: during a database outage every edge keeps
serving warm traffic from its last-known routes (ADR-1237 §5), so the load
balancer must not drop them all. `/readyz` includes a Postgres ping and is
the wrong signal here.

`allow_insecure` is `true` because edges present a Cloudflare Origin CA
certificate, which the monitor does not treat as publicly trusted. It affects
only the health check, not customer traffic.

## Create the objects

Load Balancing is a paid add-on (about $5/month base plus per-origin and
health-check usage). Use an API token with Load Balancing: Monitors and
Pools (account) and Load Balancers (zone) edit permission.

```sh
ACCOUNT=<account id>; ZONE=<gregale.dev zone id>; TOKEN=<api token>
api() { curl -fsS -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" "$@"; }

# 1. Monitor
MONITOR=$(api -X POST "https://api.cloudflare.com/client/v4/accounts/$ACCOUNT/load_balancers/monitors" \
  --data @monitor.json | jq -r .result.id)

# 2. Pool: copy pool.example.json, fill in the three control nodes' public IPs,
#    the on-call email and $MONITOR.
POOL=$(api -X POST "https://api.cloudflare.com/client/v4/accounts/$ACCOUNT/load_balancers/pools" \
  --data @pool.json | jq -r .result.id)

# 3. Check every origin is healthy before switching DNS.
api "https://api.cloudflare.com/client/v4/accounts/$ACCOUNT/load_balancers/pools/$POOL/health" | jq .

# 4. Replace the origin.gregale.dev A record with the load balancer
#    (copy load-balancer.example.json and fill in $POOL). Cloudflare refuses
#    a load balancer on a name that still has a DNS record: delete the A
#    record first, then create the load balancer immediately; the Worker
#    fails requests for the seconds in between.
api -X POST "https://api.cloudflare.com/client/v4/zones/$ZONE/load_balancers" --data @load-balancer.json
```

## Verify

1. `dig +short origin.gregale.dev @1.1.1.1` returns one healthy edge IP per
   answer, varying across queries.
2. Stop gatewayd-public on one control node (or `systemctl stop caddy`): its
   pool origin turns unhealthy within about 30 s and stops appearing in
   answers; customer traffic keeps flowing through the other edges.
3. Stop Postgres on the primary during a load test: all origins stay
   healthy (the drill in ADR-1237 P1/P2).

## Roll back

Delete the load balancer and recreate the single `origin.gregale.dev` A
record for the node that should take all traffic. The Worker needs no change.
