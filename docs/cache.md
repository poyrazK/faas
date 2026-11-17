# Declarative response caching

Gregale can cache public `GET` and `HEAD` API responses at the gateway. A
cache rule declares which route is cacheable and for how long; Gregale builds
the key, enforces response safety, propagates invalidations, and can share
entries across gateways.

## Create a rule

Inside a linked project, declare the common case directly:

```sh
gregale edge cache GET /products/:id for 30s \
  --stale-while-revalidate 1m \
  --stale-if-error 5m \
  --vary-on Accept-Language
```

Pass `--app shop` when the current directory is not linked, and `--host
api.example.com` to override the app's canonical hostname. Named route
segments such as `:id` match one path segment; the command stores the gateway
glob `/products/*`. `GET` and `HEAD` are supported.

The lower-level edge-rule form exposes the same configuration explicitly:

```sh
gregale edge rule create \
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

Cache keys distinguish the app, deployment cohort, rule, HTTP method, hostname,
path escaping, original query string and configured `vary_on` header values.
Query order is preserved: `?id=1&id=2` and `?id=2&id=1` can select different
resources. Encoded slashes and an explicit empty `?` also keep separate entries.
The allowed vary headers are `Accept-Language` and `Accept-Encoding`. Rule
matching and path purges continue to use decoded paths.

## Safety rules

Requests with `Authorization` or any application cookie bypass the cache.
This checks header presence, including empty or repeated authorization fields
and cookies an origin may parse differently. Responses with
`Set-Cookie`, `Cache-Control: no-store`, or `Cache-Control: private` are not
stored. Only the documented safe status codes and bodies up to 1 MiB are
eligible. A cache miss or cache-backend failure always falls through to the
application.

A response served from the cache, fresh or stale, counts toward the app's
monthly egress like a response from the app itself
([plans](plans.md#what-each-limit-means)). It does not wake the app or use
compute.

## Purge entries

Purge an app completely:

```sh
gregale edge cache purge shop
```

Purge matching normalized paths:

```sh
gregale edge cache purge shop --path '/products/*'
```

For content shared across routes, the app can mark a cacheable origin response
with `Cache-Tag: product:42, collection-winter`. Gregale stores these tags as
invalidation metadata and removes the header from the client response. Purge
all entries carrying one tag within the app, regardless of path:

```sh
gregale edge cache purge shop --tag product:42
```

Tags are case-insensitive ASCII identifiers using letters, digits, `-`, `_`,
`.`, `:`, or `/`. A response may have up to 32 distinct tags, each at most 128
bytes, in at most 2 KiB of `Cache-Tag` header values. Invalid tags make the
response ineligible for caching. `--tag` and `--path` are mutually exclusive.
The purge request is asynchronous and durable. A successful command records
the request; gateways apply it from a replayable ledger, so a missed
notification does not strand stale entries. Check
`GET /v1/apps/{slug}/policy/status` and its `response_cache` component to see
whether every serving gateway has invalidated its local cache and optional
shared Redis tier. `active` means the purge was applied; `pending` means at
least one gateway is behind or its shared-cache invalidation failed.

Rule changes and deployments also invalidate affected app entries. Purges
apply to every cache tier.

## Where cached responses live

Cached responses are held in memory on Gregale's gateways and can be shared
between them. The cache is an optimization, not a source of truth: when an
entry is missing or the shared tier is unavailable, the request goes to your
app. Cached records expire at the later configured stale boundary and are never
written to disk.
