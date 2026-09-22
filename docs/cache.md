# Declarative response caching

Gregale can cache public `GET` and `HEAD` API responses at the gateway. A
cache rule declares which route is cacheable and for how long; Gregale builds
the key, enforces response safety, propagates invalidations, and optionally
shares entries across gateway instances through Redis.

## Create a rule

```sh
gregale edge-rules create \
  --app shop \
  --kind cache \
  --match-host api.example.com \
  --match-path '/products/*' \
  --match-method GET \
  --cache-max-age-seconds 30 \
  --cache-stale-while-revalidate-seconds 60 \
  --cache-stale-if-error-seconds 300 \
  --cache-vary-on Accept-Language
```

This makes a successful public response fresh for 30 seconds. For the next 60
seconds, Gregale may return the stale response immediately and refresh that
exact key in the background. If the origin fails, the response remains eligible
for stale-on-error for up to 300 seconds after freshness expires.

Cache keys include the app, rule, HTTP method, normalized path, sorted query,
and the configured `vary_on` header values. The allowed vary headers are
`Accept-Language` and `Accept-Encoding`.

## Safety rules

Requests with `Authorization` or any cookie bypass the cache. Responses with
`Set-Cookie`, `Cache-Control: no-store`, or `Cache-Control: private` are not
stored. Only the documented safe status codes and bodies up to 1 MiB are
eligible. A cache miss or cache-backend failure always falls through to the
application.

## Purge entries

Purge an app completely:

```sh
gregale cache purge shop
```

Purge matching normalized paths:

```sh
gregale cache purge shop --path '/products/*'
```

Rule changes and deployments also invalidate affected app entries. Purges
apply to the local gateway caches and, when configured, the shared Redis tier.

## Distributed caching

Operators can set `FAAS_GATEWAY_RESPONSE_CACHE_REDIS_URL` in
`/etc/faas/secrets/gatewayd-internal/gatewayd-internal.env` to a `redis://` or
`rediss://` URL. The gateway keeps its bounded in-process cache as L1 and uses
Redis as an optional L2. Redis is an optimization, not a source of truth: if it
is unavailable at startup or during a request, Gregale continues with the local
cache and origin.

Cached records expire at the later configured stale boundary. They are not
written to Postgres or gateway disks.
