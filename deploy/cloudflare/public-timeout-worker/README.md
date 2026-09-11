# Gregale public-timeout Worker

Cloudflare Free can replace an origin `502`/`504` body with a generic error
page. Gregale marks its own request-budget timeout with
`X-Faas-Error-Code: request_budget_exceeded`; this Worker preserves or
reconstructs that RFC 7807 response while leaving unmarked CDN/origin failures
unchanged.

## Deploy

1. Create a DNS-only `origin.gregale.dev` record pointing at the Caddy origin.
   It must not be covered by this Worker route.
2. Copy `wrangler.toml.example` to `wrangler.toml` and set the origin hostname.
3. Deploy with Wrangler:

   ```sh
   npx wrangler deploy \
     --config deploy/cloudflare/public-timeout-worker/wrangler.toml
   ```

4. Attach the Worker to the customer-facing routes (`api.gregale.dev/*`,
   `*.gregale.dev/*`, and any enabled `*.apps.gregale.dev/*` route).
5. Verify both HTTP/1.1 and HTTP/2 against a budget-limited endpoint. The
   response must remain HTTP 504 with `Content-Type: application/problem+json`,
   `code=request_budget_exceeded`, `X-Faas-Request-Id`, and
   `Cache-Control: no-store`. A synthetic origin 504 without the marker must
   pass through untouched.

The Worker uses `cf.resolveOverride`, which changes DNS resolution without
changing the customer `Host` header used for app routing. Keep the origin
hostname private and monitor Worker request quotas before enabling it for all
customer traffic.

## Rollback

Detach the Worker from the customer routes. The origin remains unchanged and
continues to emit the canonical timeout response for direct/Caddy traffic.
