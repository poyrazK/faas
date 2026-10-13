# Route priority

When every instance of your app is busy, new requests wait in a short queue
until an instance frees up. Route priority decides who waits less and who is
turned away first when that queue is full:

```sh
gregale routes priority my-api                         # show
gregale routes priority my-api \
  --critical "POST /checkout" --critical "/login" \
  --bulk "GET /exports/*"                              # replace
gregale routes priority my-api --reset                 # back to the default
gregale routes priority my-api --clear                 # no priorities at all
```

The API is `GET`, `PUT` and `DELETE /v1/apps/{slug}/route-priorities`.

## Classes

| Class | While waiting | When the queue is full |
| --- | --- | --- |
| `critical` | Served before normal and bulk requests | Takes the place of a waiting bulk or normal request |
| normal | Served after critical, before bulk | Takes the place of a waiting bulk request |
| `bulk` | Served last | Turned away (`503` with `Retry-After`) |

Within a class, requests are served in arrival order, and the request already
at the front of the queue is never moved. A request that loses its place gets
the same `503` with `Retry-After` as a full queue.

## Which routes are which

- **Saved rules**: up to 20, as `[METHOD] PATH`. A path is a route template
  (`/users/{id}`) or a glob (`/exports/*`); without a method, every method
  matches. The first matching rule wins.
- **Default**: with nothing saved, the routes in your
  [route health](route-health.md) selection are critical.
- **Crawlers and link-preview bots** that match no rule are bulk. Uptime
  monitors are normal, so your availability checks are not turned away.
- Everything else is normal.

Changes take effect within 30 seconds. Priorities only matter while every
instance is busy; with free capacity, nothing waits. They do not change how
many instances your plan allows or how cold wakes are admitted.
