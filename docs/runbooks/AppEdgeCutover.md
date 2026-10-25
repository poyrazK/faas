# App edge cutover: pilot hostname first (ADR-1343 step 6)

This runbook moves app traffic from the control-plane edge (Caddy and
gatewayd-public on the control plane) to the compute-node app edge (a
Cloudflare Tunnel connector and gatewayd-public on every compute node). It
does one test app first. The `*.<apps domain>` wildcard moves only after that
pilot passes.

Production's public-timeout Worker (`deploy/cloudflare/public-timeout-worker`)
sits on `*.<apps domain>/*` and sends every request to its origin with
`cf.resolveOverride`. So the request host's DNS record does not choose the
origin; the Worker does. The pilot is therefore a Worker setting, not a DNS
change: hosts listed in `APP_EDGE_HOSTS` resolve to the tunnel, and every
other host keeps `ORIGIN_HOSTNAME`.

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
  stopped until its token exists, so no app traffic moves yet.
- No test app with a synthetic check or cron is pinned to a node: a rollout's
  drain waits for them (rc.290, 2026-10-22).
- You have a Cloudflare API token for the account with
  `Cloudflare Tunnel:Edit` and `DNS:Edit` on the zone, and Wrangler access
  for the Worker. These stay on your workstation; nodes only ever get the
  connector token.

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
`<tunnel_id>.cfargotunnel.com`. The Worker resolves pilot hosts to it; it must
be in the same zone, or Cloudflare ignores `resolveOverride` without an error.

## 4. Route the pilot app to the tunnel

Pick a platform-owned test app with no customer traffic, for example
`h10-fn-go.<apps domain>`. Deploy the Worker from main with:

```toml
[vars]
ORIGIN_HOSTNAME = "origin.gregale.dev"
FRONTEND_HOSTNAME = "gregale.dev"
APP_EDGE_ORIGIN_HOSTNAME = "app-edge.gregale.dev"
APP_EDGE_HOSTS = "h10-fn-go.gregale.dev"
```

```bash
npx wrangler deploy --config deploy/cloudflare/public-timeout-worker/wrangler.toml
```

Rollback at any point: remove the host from `APP_EDGE_HOSTS` and deploy again.
The pilot host returns to the control-plane origin within seconds. To stop
all tunnel traffic at once, also stop `faas-cloudflared` on every node.

## 5. Pilot evidence

Record each result in the pilot's PR or issue.

1. **The edge serves it.** The control plane's gatewayd-public opens a new
   TCP connection to a compute node's port 8080 for every request it routes
   (keep-alives off), and each closed one sits in TIME_WAIT for a minute; the
   edge uses its node's Unix socket instead. On the control plane, count
   `ss -Htan state time-wait '( dport = :8080 )' | wc -l`, send 200 requests
   to the pilot host, and count again: it must not grow by about 200. Do the
   same with an unlisted test app as the control: it must. On the compute
   nodes, gatewayd-internal's `gateway_requests_total` for the pilot app grows
   by 200 in total, and the edges' `gatewayd_public_edge_local_fallback_total`
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
   still returns 504 `request_budget_exceeded` with a request id (the Worker
   restores it from the 409 transport).
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

## 6. Move the wildcard

Not yet possible from this runbook. Moving every app host needs the Worker to
send all app hosts, but none of the platform hosts (`api.`, `operations.`,
`s3.`, the apex), to the tunnel. ADR-1343 §3 derives that split from
`apid.IsPlatformHost` with a CI check; that is the next change. After it
lands, schedule the move at low traffic: WebSocket and SSE connections drop
when the origin changes.

Custom domains (ADR-520) stay on the control-plane edge.
