# Route priority

When every instance of your app is busy, new requests wait in a short queue
until an instance frees up; when your app is parked, requests wait while it
wakes. Route priority decides who waits less and who is turned away first
when either queue is full:

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

## Queue outcomes

`gregale routes priority my-api` (and the `queue` block of `GET`) shows, per
class over the last 24 hours, how many requests waited, took a lower class's
place, lost their place, or were turned away. If nothing had to wait, it says
so.

## Which routes are which

- **Saved rules**: up to 20, as `[METHOD] PATH`. A path is a route template
  (`/users/{id}`) or a glob (`/exports/*`); without a method, every method
  matches. The first matching rule wins.
- **Default**: with nothing saved, the routes in your
  [route health](route-health.md) selection are critical.
- **Crawlers and link-preview bots** that match no rule are bulk. Uptime
  monitors are normal, so your availability checks are not turned away.
- Everything else is normal.

## While the app wakes

A parked app holds requests while it wakes, up to your plan's wake queue
(4 requests on Free, 16 on Hobby, 64 on Pro, 128 on Scale). Priorities apply
there too:

- When the wake queue is full, a request takes the place of the newest
  waiting request of a lower class, which gets `503` with `Retry-After`. The
  request that started the wake keeps its place.
- When the app is up, critical requests go first, normal ones 25 ms later and
  bulk ones 25 ms after that, so your critical routes get the first free
  capacity.

The gateway loads your priorities in the background while the app wakes. If
they are not loaded yet when the wake finishes, every waiting request is let
through together, as without priorities.

Changes take effect within 30 seconds. Priorities only matter while requests
wait; with free capacity, nothing waits. They do not change how many
instances your plan allows.
