# Gregale public-timeout Worker

Cloudflare Free can replace an origin `502`/`504` body with a generic error
page. Gregale marks its own request-budget timeout with
`X-Faas-Error-Code: request_budget_exceeded`; this Worker preserves or
reconstructs that RFC 7807 response while leaving unmarked CDN/origin failures
unchanged. If a TLS/proxy frontend strips the marker but preserves the
canonical `application/problem+json` body, the Worker accepts that exact
`code=request_budget_exceeded` envelope as a compatibility fallback.

Cloudflare replaces an origin 504 before Worker code can inspect it. Requests
from this Worker carry Cloudflare's `CF-Worker: gregale.dev` header; the public
gateway therefore transports an origin 504 as HTTP 409 with the private
`X-Faas-Edge-Original-Status: 504` marker. The Worker restores the original 504
and removes the marker before returning the response to the customer.

Cloudflare does the same to origin 502s, so an app that crashed mid-request
(for example OOM-killed) reached API clients as Cloudflare HTML with no
request id. The Worker now sends `X-Faas-Edge-Restorable: 502,504`, and the
gateway transports a 502 (the platform's `bad_gateway` problem or an app's own
502) only for a Worker that advertises it. Deploy order does not matter: an
older Worker never receives a transported 502.

## Deploy

1. Create a DNS-only `origin.gregale.dev` record pointing at the Caddy origin.
   It must not be covered by this Worker route.
2. Copy `wrangler.toml.example` to `wrangler.toml` and set the origin hostname.
3. Deploy with Wrangler:

   ```sh
   npx wrangler deploy \
     --config deploy/cloudflare/public-timeout-worker/wrangler.toml
   ```

4. Attach the Worker to the customer-facing routes (`gregale.dev/*`,
   `api.gregale.dev/*`, and `*.gregale.dev/*`). On the apex frontend route the Worker keeps
   the Pages origin and adds the browser security policy to every HTML
   response, including SPA fallbacks. API and application routes retain the
   configured origin override and timeout normalization.
5. Verify both HTTP/1.1 and HTTP/2 against a budget-limited endpoint. The
   response must remain HTTP 504 with `Content-Type: application/problem+json`,
   `code=request_budget_exceeded`, `X-Faas-Request-Id`, and
   `Cache-Control: no-store`. A synthetic origin 504 without the marker must
   pass through untouched.
6. Run `deploy/scripts/frontend-security-headers-smoke.sh`. It checks the
   landing page, dashboard, and a deep SPA route and rejects wildcard CORS on
   those HTML documents.

The Worker uses `cf.resolveOverride`, which changes DNS resolution without
changing the customer `Host` header used for app routing. Keep the origin
hostname private and monitor Worker request quotas before enabling it for all
customer traffic.

## App edge pilot (ADR-1343)

`APP_EDGE_ORIGIN_HOSTNAME` and `APP_EDGE_HOSTS` send app hosts to the
compute-node app edge's Cloudflare Tunnel instead of `ORIGIN_HOSTNAME`.
`APP_EDGE_HOSTS` is a list of exact hostnames (the pilot) or `*` for every app
host: one label under `APPS_DOMAIN` (default `gregale.dev`) that is a valid,
unreserved app name per `app-hosts.json`, which mirrors `pkg/api` and is
checked by `pkg/api/app_hosts_worker_test.go`. `APP_EDGE_EXCLUDE_HOSTS` keeps
exact hostnames on the control plane either way. Every other host, and every
host while `APP_EDGE_ORIGIN_HOSTNAME` or `APP_EDGE_HOSTS` is unset, keeps the
control-plane origin. Because this Worker picks
the origin for `*.gregale.dev`, a DNS record alone cannot move an app host to
the tunnel. Follow `docs/runbooks/AppEdgeCutover.md`.

## Rollback

Detach the Worker from the customer routes. The origin remains unchanged and
continues to emit the canonical timeout response for direct/Caddy traffic.
