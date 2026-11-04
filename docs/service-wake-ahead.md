# Service wake-ahead

When a parked app calls another parked app over `<slug>.svc.gregale`, Gregale
holds the call while the second app restores. The same happens when an app
opens a [private TCP connection](networking.md#private-tcp-between-services) to
a parked service such as a database or a queue. On a fully cold path such as
`public-api → auth → billing`, those restores happen one after another.

Service wake-ahead lets an app start restoring the services it usually calls
while the app itself is still waking, so the restores overlap:

```sh
gregale wake-ahead public-api on
gregale wake-ahead public-api        # show the setting
gregale wake-ahead public-api off
```

The API is `GET` and `PUT /v1/apps/{slug}/service-wake-ahead` with
`{"enabled": true}`. Changes take effect within 30 seconds.

Wake-ahead is **on by default** for a service in a project under the `project`
service policy, the default for new project services, because those services
call their siblings without declaring them
([ADR-20261025105812](adr/20261025105812-wake-ahead-default-for-project-services.md)).
It is off by default for every other app and for PR previews. Your own choice
always wins: `gregale wake-ahead <app> off` turns the default off for that app.
`GET` reports the effective setting; an empty `updated_at` means the app is on
its default.

## What gets woken

Gregale wakes the services your app declares until it has measured which ones
your app really calls, and from then on only those:

- After each cold wake of the app, the calls and private TCP connections it
  makes to other services in the next 10 seconds are recorded. A connection
  is recorded when it opens, however long it then stays open.
- Until the app has woken 20 times, the services it declares in `depends_on`
  are woken ahead, in the order they are declared. Only services the app is
  allowed to call count; see [internal services](networking.md#internal-services).
- From the 20th wake, a service is woken ahead only if the app called it after
  at least half of those wakes and found it parked on at least half of those
  calls, whether it is declared or not. A declared service your app rarely
  calls after waking stops being woken ahead.
- At most three services are woken ahead per wake, and only ones that are not
  already running. A service woken ahead can wake its own services, two levels
  deep, if it has wake-ahead on too.

PR preview apps neither learn nor wake services ahead. Measurement happens in
each gateway and is kept when the gateway restarts.

## What was measured

`gregale wake-ahead my-api` (and the `learned` block of `GET`) shows what the
gateways measured for your app, whether wake-ahead is on or off:

- each service your app calls after a cold wake, how often it calls it
  (`CALLED`), how often that service was parked or already woken ahead
  (`PARKED`), and whether the edge qualifies;
- over the last 24 hours, how many wake-aheads were started, used (your app
  called the service while it was still fresh), unused or failed.

Measurements live in each gateway and are kept when it restarts. When gateway
metrics cannot be read, the block says so.

## Limits and cost

- A service woken ahead runs, and is billed like any running instance, from the
  moment it is woken. If it is not called, it parks again after its idle
  timeout. Before the first 20 wakes, a declared service your app does not call
  after waking is woken anyway and counts as `unused`.
- Wake-ahead goes through the same admission as any wake: your plan's
  concurrency limit and the platform RAM ceiling apply, and a request that
  arrives for the service joins the restore already under way.
- Gregale skips wake-ahead while the platform is busy (60% or more of its RAM
  ceiling in use) and never stops another app to make room.
- Restores started by wake-ahead show the trigger `service.wake_ahead` in
  `gregale wake-timeline`.
