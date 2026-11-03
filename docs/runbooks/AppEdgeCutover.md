# App edge cutover: pilot hostname first (ADR-1343 step 6)

This runbook moves app traffic from the control-plane edge (Caddy and
gatewayd-public on the control plane) to the compute-node app edge (a
Cloudflare Tunnel connector and gatewayd-public on every compute node). It
does one test app first. The `*.<apps domain>` wildcard moves only after that
pilot passes.

DNS chooses each host's origin (ADR-20261030115139): app hosts go to the
tunnel and platform hosts to the control plane. The public-timeout Worker
(`deploy/cloudflare/public-timeout-worker`) is being removed, because Workers
bill per request. While one of its routes still covers a host, the Worker
picks that host's origin with `cf.resolveOverride` and DNS does not matter.
So a host moves with two changes: a route with no Worker for it, which beats
the wildcard route, and its own DNS record.
`deploy/scripts/cloudflare-app-dns.sh` makes both, prints each change, and
writes nothing without `APPLY=1`.

Before any app host leaves the Worker, the control plane and every compute
node must run a release with #6015. That release sends Gregale's own 502/504
problems as 503, which Cloudflare passes through. Without it, a timeout or a
failed app connection on a host the Worker no longer covers reaches the
client as Cloudflare's HTML page.

Alerts for the running edge: [FaasComputeEdge](FaasComputeEdge.md).

## Before you start

- The release on the compute nodes contains ADR-1343 steps 1–5 (#4781, #4795,
  #4854, #4855; any release since rc.274).
- The production manifest sets `public_edge.compute_nodes: true` and that
  release has rolled out to the control plane and every compute node. It
  installs `faas-gatewayd-public-edge` and `faas-cloudflared` on each compute
  node, the control plane's private listener on port 8090, and the nftables
  rules. It also holds back 1,000 millicores and 256 MB per compute node
  from tenants (`gregalectl compute-nodes sizing --edge`). The connector stays
  stopped until its token exists, so no app traffic moves yet. Steps 1–3 may
  run before that release: the staged token waits, and the release starts
  the connector.
- No test app with a synthetic check or cron is pinned to a node: a rollout's
  drain waits for them (rc.290, 2026-10-22).
- You have a Cloudflare API token for the account with
  `Cloudflare Tunnel:Edit`, and `DNS:Edit` and `Workers Routes:Edit` on the
  zone. It stays on your workstation; nodes only ever get the connector
  token.

## Steps 1–3 in one command

`deploy/scripts/cloudflare-app-edge-tunnel.sh` does steps 1–3 from your
workstation and is safe to re-run. It pipes the connector token from the
Cloudflare API straight into each node's root-only file over SSH, so the
token is never printed or stored locally. It asks for the API token without
echo when `CF_API_TOKEN` is unset:

```bash
CF_ACCOUNT_ID=<account id> CF_ZONE_ID=<gregale.dev zone id> deploy/scripts/cloudflare-app-edge-tunnel.sh
```

The sections below are what it does, for doing it by hand.

## 1. Create the tunnel without touching DNS

Use the API. The dashboard's *Public hostname* form also writes a DNS record
for the hostname it routes, which for `*.<apps domain>` would point the
wildcard at the tunnel before the pilot.

```bash
export CF_API_TOKEN=...   CF_ACCOUNT_ID=...   APPS_DOMAIN=gregale.dev
api=https://api.cloudflare.com/client/v4/accounts/$CF_ACCOUNT_ID/cfd_tunnel

tunnel_id=$(curl -fsS -X POST "$api" -H "Authorization: Bearer $CF_API_TOKEN" \
  -H 'Content-Type: application/json' \
  --data '{"name":"gregale-apps-production-us","config_src":"cloudflare"}' | jq -r .result.id)

curl -fsS -X PUT "$api/$tunnel_id/configurations" -H "Authorization: Bearer $CF_API_TOKEN" \
  -H 'Content-Type: application/json' --data @- <<JSON
{"config":{"ingress":[
  {"hostname":"*.$APPS_DOMAIN","service":"http://127.0.0.1:8090"},
  {"service":"http_status:404"}]}}
JSON
```

Keep `tunnel_id`. Fetch the connector token with
`curl -fsS "$api/$tunnel_id/token" -H "Authorization: Bearer $CF_API_TOKEN" | jq -r .result`.

## 2. Stage the connector token on every compute node

On each compute node (fsn-2, fsn-3):

```bash
sudo install -d -m 0700 /etc/faas/secrets/cloudflared
sudo install -m 0600 /dev/stdin /etc/faas/secrets/cloudflared/tunnel-token   # paste the token, then Ctrl-D
sudo systemctl start faas-cloudflared.service
```

Check from your workstation:

```bash
make check-compute-edges CP_SSH=<user@control-plane>
```

Every node must show its edge ready and its connector connected. In
Cloudflare the tunnel shows one connector per node (four connections each).
Nothing reaches the tunnel yet: no DNS record and no Worker route point at it.

## 3. Add the tunnel origin record

Create `app-edge.<apps domain>` as a **proxied** CNAME to
`<tunnel_id>.cfargotunnel.com`. `cloudflare-app-dns.sh` reads the tunnel from
this record. Until the Worker is gone, a Worker that lists pilot hosts in
`APP_EDGE_HOSTS` resolves them to it too; it must be in the same zone, or
Cloudflare ignores `resolveOverride` without an error.

## 4. Route the pilot app to the tunnel

Pick a platform-owned test app with no customer traffic, for example
`h10-fn-go.<apps domain>`. Check the zone first, then give the platform hosts
their own records and move the pilot host:

```bash
export CF_ZONE_ID=<gregale.dev zone id>
deploy/scripts/cloudflare-app-dns.sh plan
APPLY=1 deploy/scripts/cloudflare-app-dns.sh platform
APPLY=1 deploy/scripts/cloudflare-app-dns.sh pilot h10-fn-go.gregale.dev
```

- **`plan`** lists the platform hosts' records, the wildcard, every address
  record in the zone and the Worker routes. Run it again after each step.
- **`platform`** gives each platform host (the apex, `api.`, `operations.`,
  `s3.`) that has no record of its own a proxied CNAME to
  `origin.<apps domain>`. It never repoints an existing record. In production
  (2026-10-31) every one already has its own: the apex and `operations.` are
  on Vercel, and `api.` and `s3.` are A records to the control plane. So it
  changes nothing there.
- **`pilot`** adds the route `h10-fn-go.gregale.dev/*` with no Worker and a
  proxied CNAME to the tunnel.

Rollback at any point: `APPLY=1 deploy/scripts/cloudflare-app-dns.sh unpilot
h10-fn-go.gregale.dev` deletes the route and the record, and the Worker serves
the host again within seconds. To stop all tunnel traffic at once, also stop
`faas-cloudflared` on every node.

## 5. Pilot evidence

Edge readiness and checks 1 and 2 (and 3, with `ECHO_PATH`) run in one
command from your workstation. It changes nothing on any host:

```bash
PILOT_HOST=h10-fn-go.gregale.dev CONTROL_HOST=<an unlisted warm test app>.gregale.dev deploy/scripts/faas-app-edge-pilot-check.sh
```

Set `ECHO_PATH=<path>` when the pilot app has a path that echoes its request
headers. The other checks below are by hand. Record each result in the pilot's PR or issue.

1. **The edge serves it.** The control plane's gatewayd-public opens a new
   TCP connection to a compute node for every request it routes
   (keep-alives off), so its `ActiveOpens` counter in `/proc/net/snmp` grows
   by one per request; the edge uses its node's Unix socket instead. Send 100
   requests to the pilot host and 100 to an unlisted, warm test app (the
   control): the counter must grow with the control's requests and not with
   the pilot's. On the edges, `gatewayd_public_edge_local_fallback_total`
   and `gatewayd_public_edge_misdirected_total` stay flat.
2. **No control-plane Postgres per request.** With the app warm and
   `ratelimit_lease` on, reset `pg_stat_statements` on the control plane (the
   extension lives in the `postgres` database), send the 200 requests, and
   list statements whose calls grew by about 200. There must be none. Journal
   batches (ADR-1521) and lease refills grow with time, not requests.
3. **Client IP.** An app that echoes its request headers receives your
   public address in `X-Forwarded-For`, not `127.0.0.1`, also when you send
   your own `X-Forwarded-For` (ADR-1343 §2 replaces it from
   `CF-Connecting-IP`).
4. **Timeouts keep their body.** A request that exceeds the app's budget
   returns 503 `request_budget_exceeded` with the problem body, a request id
   and `X-Faas-Error-Code` (ADR-20261030115139). A Cloudflare HTML page
   means the release with #6015 has not reached that host's edge.
5. **Credential-bound routes forward.** If the pilot app has a public bucket,
   a public object read on the pilot host succeeds; it is forwarded to the
   control plane's private listener.
6. **Streams.** A WebSocket or SSE request to the pilot host stays open for
   at least 5 minutes with traffic every 30 s.
7. **Drain loses nothing.** Run 5 requests/s against the pilot host, drain
   one compute node through the normal drain path, and undrain it. Zero
   failed requests; that node's `gatewayd_public_edge_node_drained` is 1
   while drained.
8. **Load.** At the same request rate as the October load test, the control
   plane's CPU stays flat while the pilot host takes the traffic.

## 6. Move every app host

After the pilot passes, at low traffic:

1. Run `plan` and read every address record and Worker route.
   - **Names without a record of their own.** A name that is neither an app
     nor a platform host follows the wildcard to the tunnel, where the edge
     answers 421 `misdirected_request`. Give each one that must keep working
     its own record: `APPLY=1 deploy/scripts/cloudflare-app-dns.sh keep <name>`.
   - **Names whose own record goes elsewhere.** A Worker route sends such a name
     to the control plane today, and it follows its record once the routes go.
     In production that is `www.` (its record points to Vercel, and the control
     plane answers it 404 today).
2. `APPLY=1 deploy/scripts/cloudflare-app-dns.sh wildcard` points
   `*.<apps domain>` at the tunnel. It refuses until every platform host has
   its own record. No traffic moves yet: the Worker's wildcard route still
   overrides DNS.
3. `APPLY=1 deploy/scripts/cloudflare-app-dns.sh drop-worker-routes` deletes
   every route that runs the Worker. From here DNS alone decides: app hosts
   reach the tunnel and platform hosts the control plane.
4. `unpilot` the pilot host. Its route is no longer needed, and the wildcard
   covers it.
5. After a day without rollback, delete the Worker
   (`wrangler delete --name gregale-public-timeout`) and its directory in the
   repository. One release later, the gateway's 409 transport goes
   (ADR-20261030115139, order step 6).

Notes:

- WebSocket and SSE connections drop when their origin changes; clients
  reconnect to the tunnel.
- To keep one app on the control plane (an app that misbehaves on the edge),
  run `keep <host>`. Delete that record to move it back.
- Rollback: `APPLY=1 deploy/scripts/cloudflare-app-dns.sh unwildcard` points
  the wildcard back at the control-plane origin. No Worker is needed.
- Watch [FaasComputeEdge](FaasComputeEdge.md) alerts, the control plane's CPU,
  and the ADR-1343 exit criterion (§5 checks 1, 2 and 7 at full traffic).

Custom domains (ADR-520) stay on the control-plane edge.
