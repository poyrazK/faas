# ADR 20261030115139 · Cloudflare carries app traffic without a Worker

- **Status:** proposed
- **Date:** 2026-10-30
- **Amends:**
  - ADR-1343 §3 ("The public-timeout Worker gets two origins") and the
    2026-10-25 note under its cutover step 6 (the Worker chooses the origin
    per host).
  - ADR-093 (request budgets): the public status of `request_budget_exceeded`.
  - The 409 edge transport for 502/504 (production hunt #9, H9-96 and H9-134).
- **Related:** ADR-1344 (availability, §8 launch topology),
  ADR-20261026100000 (platform and app domains, alias apps domains),
  ADR-070 (one public ingress per box)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

Since 2026-09 the public-timeout Worker
(`deploy/cloudflare/public-timeout-worker`) runs on `gregale.dev/*`,
`api.gregale.dev/*` and `*.gregale.dev/*`, so it handles every public request.
It does two jobs:

- **It chooses the origin.** Every request is fetched with
  `cf.resolveOverride`, so a host's DNS record does not decide where the
  request goes. The ADR-1343 pilot (2026-10-30) is a Worker setting:
  `APP_EDGE_HOSTS` sends listed app hosts to the compute-node tunnel.
- **It keeps our 502/504 bodies.** Cloudflare replaces an origin's 502 or 504
  body with its own HTML page, before Worker code can see it, on every plan
  below Enterprise. The gateway therefore sends a 502/504 to a Worker as 409
  with `X-Faas-Edge-Original-Status`, and the Worker restores it. Without
  this, an app crash or a budget timeout reached API clients as Cloudflare
  HTML with no request id (H9-96).

Workers are billed per request:

- **Free plan:** 100,000 requests a day for the account.
- **Paid plan:** $5 a month for 10 million requests, then $0.30 per million.

We bill compute per GB-hour. One 256 MB instance serving 100 requests a second
for an hour costs about $0.11 in Worker requests and earns about $0.0026 at the
$0.01/GB-h overage rate. At a billion requests a month the Worker alone costs
about $300.

On 2026-10-30 the owner decided Cloudflare is used for safety only, and
nothing on the request path is billed per request.

What Cloudflare does with origin errors (Cloudflare documentation, checked
2026-10-30): it replaces origin 502 and 504 bodies, and passes 500, 501, 503
and 505 through with their body. Production already shows this: 503
`app_unavailable` and `policy_unavailable` problems reach clients intact.

The pilot measured on 2026-10-30:

- **The tunnel path skips the control plane.** For 20 requests to the pilot
  host, the control plane's forward counter moved +2 (noise; an idle window of
  the same length moved +8). For 20 requests to a control app it moved +20.
- **It is not slower.** From a client in us-east1, a request the gateway
  answers itself took p50 58 ms through the tunnel against 81 ms through the
  control plane.

## Decision

### 1. No Worker on the request path

Cloudflare provides:

- DNS;
- the proxy: TLS, DDoS mitigation, the client connection;
- Tunnel;
- Pages for the frontend.

Nothing billed per request sits on the request path: no Workers, Argo,
Load Balancing or Snippets. Adding one needs a new ADR.

### 2. DNS chooses the origin

This is ADR-1343 §3 as first written.

- **App hosts.** `*.<apps domain>` is a proxied CNAME to
  `<tunnel-id>.cfargotunnel.com`. The tunnel's ingress already maps
  `*.<apps domain>` to `http://127.0.0.1:8090`
  (`deploy/scripts/cloudflare-app-edge-tunnel.sh`).
- **One host at a time.** An explicit record beats the wildcard. A pilot or a
  rollback is one proxied CNAME for one hostname:
  - to the tunnel, to move the host;
  - to the control-plane origin, to keep it on the control plane after the
    wildcard moves.
- **Platform hosts.** The apex, `api.`, `operations.` and `s3.` keep explicit
  proxied records to their current origin. The list is ADR-1343 §3's: it comes
  from `apid.IsPlatformHost` plus `s3.` and is checked in CI. A new platform
  host needs its explicit record before it serves traffic.
- **Alias apps domains** (ADR-20261026100000). Each alias zone gets the same
  wildcard record. Its "Worker route per alias zone" becomes that record.
- **Records live in git.** A script in `deploy/scripts`, in the style of
  `cloudflare-app-edge-tunnel.sh`, applies the records. Its token needs only
  Zone DNS: Edit. The dashboard holds no state the repository does not
  describe.

### 3. Platform errors use 503 at the public edge

- **Every problem a Gregale process writes carries `X-Faas-Error-Code`.**
  `api.WriteProblem` sets it from the problem's code. Today only
  `request_budget_exceeded` sets it.
- **gatewayd-internal removes `X-Faas-Error-Code` from app responses**, so
  only platform problems carry it.
- **gatewayd-public changes the status.** It turns a 502 or 504 response that
  carries `X-Faas-Error-Code` into 503, keeping the body, the code and every
  other header. This replaces the 409 transport. It covers:
  - `request_budget_exceeded`;
  - `long_poll_timeout` (apid through the control-plane proxy);
  - `bad_gateway` ("App connection failed");
  - WebSocket upgrade failures;
  - the githubd proxy;
  - session attach;
  - MCP probes.

  Metrics and logs inside gatewayd-internal and apid keep the original
  status, so their dashboards and alerts do not move.
- **No new `Retry-After`.** A retry after `request_budget_exceeded` or
  `long_poll_timeout` can run non-idempotent work twice. `bad_gateway` keeps
  its retry hint.
- **The `code` field and `X-Faas-Error-Code` are the contract.**
  `docs/errors.md` and the OpenAPI documents list 503 for these codes.
  Shipped CLIs and the Go SDK's retry paths already treat 503 like 502 and
  504 (`cmd/gregale/commands_deployments.go`,
  `cmd/gregale/commands_rollouts_status.go`,
  `sdk/go/internal/bindingrun/status_retry.go`). OpenAPI lists 504 for
  `long_poll_timeout`; changing it regenerates the Node and Python SDKs, so it
  follows the gateway change.
- **An app's own 502 or 504 passes through unchanged.** Cloudflare then
  replaces its body with its own page, as on any site behind Cloudflare.
  `docs/errors.md` says so. Rewriting a customer's status is not ours to do.

### 4. Order

Units release independently, so each step works with the step before it.

1. **Release decision 3.** It works with the Worker still in place: the
   Worker returns any status other than 504 and the 409 transport untouched,
   so a 503 reaches the client as written.
2. **Move platform hosts first.** Before taking a platform host off its
   Worker route, confirm its DNS record points at the origin that serves it
   today. The apex is the open question below.
3. **Pilot app host by DNS, while the wildcard route still has the Worker.**
   - Add a route `<pilot host>/*` with no Worker. The more specific route wins.
   - Point the host's explicit record at the tunnel.
   - Run the pilot check (`deploy/scripts/faas-app-edge-pilot-check.sh`).
4. **Move every app host.**
   - Point the `*.<apps domain>` record at the tunnel.
   - Remove the `*.<apps domain>/*` route.
   - Remove the per-host records from step 3.
5. **Delete the Worker** and `deploy/cloudflare/public-timeout-worker`.
6. **One release later**, remove the 409 transport, the `CF-Worker` handling
   and the `X-Faas-Edge-*` headers from the gateway.

## Consequences

- **Cost.** Cloudflare costs nothing at any request volume: the Free zone,
  Tunnel and Pages. The Workers Free plan's 100,000-a-day cap no longer
  applies.
- **Customer-visible change.** The platform errors listed in decision 3 move
  from 502/504 to 503. A client that keys on the status instead of the code
  sees a change, so the changelog says so.
- **An app's own 502/504 body is lost behind Cloudflare.** The platform's
  problems keep their body and request id.
- **The per-host switch is a DNS record**, not a Worker variable. A record
  change takes effect in seconds for a proxied record.
- **The gateway gets simpler** once the Worker is gone. The 409 transport,
  the Worker-zone check, the Worker's frontend CSP and `app-hosts.json` all go.
- **gatewayd-public metrics change.** Its status labels report 503 for these
  errors, so any rule that keys on 502/504 at the public edge needs a
  check.
- **Leaving Cloudflare stays possible.** Origins are plain HTTP behind
  Cloudflare's proxy, and the tunnel can be replaced by TLS on each node (the
  alternative below). This ADR does not decide that.

## Rejected alternatives

- **Keep the Worker on the Paid plan.** Its cost grows per request while
  revenue grows per GB-hour.
- **Drop the tunnel too: proxied A records to each node's public IP.** Every
  node would open port 443 to Cloudflare's ranges and hold an origin
  certificate. A dead node keeps getting traffic without a paid load balancer.
  This stays the way out of Cloudflare if we ever leave.
- **Pro plan custom error pages or Custom Error Rules.** Their bodies are
  static, so they cannot carry the request id or the problem fields.
- **500 instead of 503.** 500 says the platform has a bug. 503 says the app or
  platform could not serve this request now. Heroku's router uses 503 for its
  own timeouts and crashed apps.
- **Change the status where each error is written.** gatewayd-internal's and
  apid's metrics and alerts would move, and every new error site would have to
  remember the rule. Every public response passes gatewayd-public, so it is
  the one place to apply it.

## Open questions

- **The apex's origin.** The Worker README says the apex keeps the Pages
  origin. The live Worker (version `165ecd0c`, 2026-09-12) sends every host,
  apex included, to `origin.gregale.dev`. Confirm which origin serves
  `gregale.dev` before its route is removed.
- **The account's Workers plan.** On the Free plan the 100,000-a-day cap
  applies until step 5.
